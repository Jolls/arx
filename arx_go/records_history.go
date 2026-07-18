package main

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"arx/arx_go/models"
)

// errRecordNeedsLot is returned by completeRecordTx when a lot-tracked part's record has
// no lot linked yet (#687) — surfaced by callers as a 400, not a server error.
var errRecordNeedsLot = errors.New("a lot must be selected before this record can be completed — the tested part is lot-tracked")

// snapshotRecordResults copies the record's current data-row results (result) into
// record_event_results, linked to the given Complete event. Rows are inserted in the
// record's frozen display order (test_order, falling back to form_row_id order) so the snapshot
// renders by id. Headings (type > 0) are not captured. Runs inside the caller's tx.
func (h *Handler) snapshotRecordResults(ctx context.Context, tx *txLogger, eventID, recordID int) error {
	var testOrder string
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT COALESCE(test_order,'') FROM %s WHERE id=@p1", h.cfg.RecordsTable()), recordID).
		Scan(&testOrder); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT form_row_id, COALESCE(parameter,''), COALESCE(specification,''), COALESCE(spec_units,''),
		       COALESCE(result,''), pass_fail, COALESCE(comment,'')
		FROM %s WHERE form_record_id=@p1 AND COALESCE(type,0)=0`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		return err
	}
	byTest := map[int]models.RecordResultSnapshot{}
	for rows.Next() {
		var s models.RecordResultSnapshot
		if err := rows.Scan(&s.TestID, &s.Parameter, &s.Specification, &s.SpecUnits,
			&s.Result, &s.PassFail, &s.Comment); err != nil {
			rows.Close()
			return err
		}
		byTest[s.TestID] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ins := fmt.Sprintf(`INSERT INTO %s (event_id, form_row_id, parameter, specification, spec_units, result, pass_fail, comment)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`, h.cfg.RecordEventResultsTable())
	for _, tid := range orderedResultIDs(testOrder, byTest) {
		s := byTest[tid]
		if _, err := tx.ExecContext(ctx, ins, eventID, tid, s.Parameter, s.Specification, s.SpecUnits,
			s.Result, s.PassFail, s.Comment); err != nil {
			return err
		}
	}
	return nil
}

// orderedResultIDs returns the form_row_ids present in byTest, ordered by their position in the
// record's test_order; ids absent from test_order (or all of them when test_order is empty,
// e.g. a legacy record) are appended in ascending numeric order so nothing is dropped.
func orderedResultIDs(testOrder string, byTest map[int]models.RecordResultSnapshot) []int {
	var out []int
	seen := map[int]bool{}
	for _, tid := range (&models.TestRecord{TestOrder: testOrder}).OrderedTestIDs() {
		if _, ok := byTest[tid]; ok && !seen[tid] {
			out = append(out, tid)
			seen[tid] = true
		}
	}
	var rest []int
	for tid := range byTest {
		if !seen[tid] {
			rest = append(rest, tid)
		}
	}
	sort.Ints(rest)
	return append(out, rest...)
}

// loadEventSnapshots returns the result snapshot for each Complete event of a record, keyed
// by event_id and already diffed against the previous Complete snapshot (added/changed/
// unchanged). Events without a snapshot are absent from the map.
func (h *Handler) loadEventSnapshots(ctx context.Context, recordID int) (map[int][]models.SnapshotDiffRow, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT rer.event_id, rer.form_row_id, COALESCE(rer.parameter,''), COALESCE(rer.specification,''),
		       COALESCE(rer.spec_units,''), COALESCE(rer.result,''), rer.pass_fail, COALESCE(rer.comment,'')
		FROM %s rer
		JOIN %s re ON re.id = rer.event_id
		WHERE re.form_record_id = @p1
		ORDER BY re.event_date ASC, re.id ASC, rer.id ASC`,
		h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var order []int // event ids in chronological order
	grouped := map[int][]models.RecordResultSnapshot{}
	for rows.Next() {
		var s models.RecordResultSnapshot
		if err := rows.Scan(&s.EventID, &s.TestID, &s.Parameter, &s.Specification, &s.SpecUnits,
			&s.Result, &s.PassFail, &s.Comment); err != nil {
			continue
		}
		if _, ok := grouped[s.EventID]; !ok {
			order = append(order, s.EventID)
		}
		grouped[s.EventID] = append(grouped[s.EventID], s)
	}

	out := map[int][]models.SnapshotDiffRow{}
	var prev []models.RecordResultSnapshot
	for _, eid := range order {
		out[eid] = diffSnapshot(grouped[eid], prev)
		prev = grouped[eid]
	}
	return out, nil
}

// completeRecordTx locks one WIP record and, on transition, logs the 'completed' event and
// captures the result snapshot (#251) — all in a single tx. When formID > 0 the UPDATE is
// scoped to that form (bulk complete). Returns true if the record was newly completed.
func (h *Handler) completeRecordTx(ctx context.Context, recordID, formID int, username string) (bool, error) {
	guard := ""
	args := []any{recordID}
	if formID > 0 {
		guard = " AND form_id=@p2"
		args = append(args, formID)
	}

	tx, err := h.beginTx(ctx)
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// #677/#687 — the lot-tracked-needs-a-lot gate is disabled for 0.6.0: the lot/build
	// picker UI is hidden (unfinished, deferred to v0.7.0 #702), so there's no way for a
	// user to satisfy this check. Re-enable alongside the picker. See
	// docs/plans/677-hide-testrecord-lot-linkage.md

	res, err := tx.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET is_locked=%s, updated_at=GETDATE() WHERE id=@p1 AND is_locked=%s"+guard,
		h.cfg.RecordsTable(), h.dialect.BoolLiteral(true), h.dialect.BoolLiteral(false)), args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if err := h.logCompletionSnapshot(ctx, tx, recordID, username); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return n > 0, nil
}

// logCompletionSnapshot writes the 'completed' record_events row for a just-locked record and
// captures its result snapshot, within the caller's tx. Shared by single + bulk lock.
func (h *Handler) logCompletionSnapshot(ctx context.Context, tx *txLogger, recordID int, username string) error {
	var eventID int
	insertEvent := h.dialect.InsertReturningID(h.cfg.RecordEventsTable(),
		"form_record_id, event_type, username, event_date",
		"@p1, 'completed', @p2, GETDATE()",
		false)
	if err := tx.QueryRowContext(ctx, insertEvent,
		recordID, username).Scan(&eventID); err != nil {
		return err
	}
	return h.snapshotRecordResults(ctx, tx, eventID, recordID)
}

// diffSnapshot annotates each row of a Complete-event result snapshot with how it changed
// vs. the previous Complete snapshot, matching on form_row_id. prev is nil for the earliest
// snapshot, in which case every row is "unchanged" (a plain baseline with nothing to diff).
func diffSnapshot(curr, prev []models.RecordResultSnapshot) []models.SnapshotDiffRow {
	prevByTest := make(map[int]models.RecordResultSnapshot, len(prev))
	for _, p := range prev {
		prevByTest[p.TestID] = p
	}

	out := make([]models.SnapshotDiffRow, 0, len(curr))
	for _, row := range curr {
		status := "unchanged"
		if prev != nil {
			p, ok := prevByTest[row.TestID]
			switch {
			case !ok:
				status = "added"
			case row.Result != p.Result || row.Comment != p.Comment || !boolPtrEqual(row.PassFail, p.PassFail):
				status = "changed"
			}
		}
		out = append(out, models.SnapshotDiffRow{RecordResultSnapshot: row, Status: status})
	}
	return out
}

// boolPtrEqual reports whether two *bool hold the same value (both nil counts as equal).
func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
