package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
)

// snapshotRecordResults copies the record's current data-row results (test_result) into
// record_event_results, linked to the given Complete event. Rows are inserted in the
// record's frozen display order (test_order, falling back to test_id order) so the snapshot
// renders by id. Headings (type > 0) are not captured. Runs inside the caller's tx; shared
// by the live lock handlers and the one-time backfill.
func (h *Handler) snapshotRecordResults(ctx context.Context, tx *txLogger, eventID, recordID int) error {
	var testOrder string
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT COALESCE(test_order,'') FROM %s WHERE id=@p1", h.cfg.RecordsTable()), recordID).
		Scan(&testOrder); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT test_id, COALESCE(parameter,''), COALESCE(result,''), pass_fail, COALESCE(comment,'')
		FROM %s WHERE record_id=@p1 AND COALESCE(type,0)=0`, h.cfg.ResultsTable()), recordID)
	if err != nil {
		return err
	}
	byTest := map[int]models.RecordResultSnapshot{}
	for rows.Next() {
		var s models.RecordResultSnapshot
		if err := rows.Scan(&s.TestID, &s.Parameter, &s.Result, &s.PassFail, &s.Comment); err != nil {
			rows.Close()
			return err
		}
		byTest[s.TestID] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ins := fmt.Sprintf(`INSERT INTO %s (event_id, test_id, parameter, result, pass_fail, comment)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`, h.cfg.RecordEventResultsTable())
	for _, tid := range orderedResultIDs(testOrder, byTest) {
		s := byTest[tid]
		if _, err := tx.ExecContext(ctx, ins, eventID, tid, s.Parameter, s.Result, s.PassFail, s.Comment); err != nil {
			return err
		}
	}
	return nil
}

// orderedResultIDs returns the test_ids present in byTest, ordered by their position in the
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

// formHasBackfillableRecords reports whether a form has at least one completed record that
// has a 'completed' event but no result snapshot yet — i.e. the one-time #251 backfill has
// work to do. Drives the self-hiding backfill bulk action.
func (h *Handler) formHasBackfillableRecords(ctx context.Context, formID int) bool {
	var n int
	err := h.queryRowContext(ctx, fmt.Sprintf(`
		SELECT CASE WHEN EXISTS (
			SELECT 1 FROM %s tr
			WHERE tr.form_id = @p1 AND tr.is_locked = 1 AND tr.is_active = 1
			  AND EXISTS (SELECT 1 FROM %s re WHERE re.test_record_id = tr.id AND re.event_type = 'completed')
			  AND NOT EXISTS (SELECT 1 FROM %s rer JOIN %s e ON e.id = rer.event_id WHERE e.test_record_id = tr.id)
		) THEN 1 ELSE 0 END`,
		h.cfg.RecordsTable(), h.cfg.RecordEventsTable(),
		h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), formID).Scan(&n)
	return err == nil && n > 0
}

// BackfillRecordHistory — POST /forms/{id}/records/backfill-history
// One-time migration aid (#251): for each selected completed record that has no snapshot yet,
// captures its current results against the record's latest 'completed' event. Idempotent and
// gated to TR reviewers; the bulk action self-hides once a form has nothing left to backfill.
func (h *Handler) BackfillRecordHistory(w http.ResponseWriter, r *http.Request) {
	formID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if u := h.currentUser(r); u == nil || !u.CanApproveRecords {
		http.Error(w, "you do not have permission to backfill record history", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	var done int
	for _, raw := range r.Form["record_ids[]"] {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			continue
		}
		if ok, err := h.backfillRecordTx(r.Context(), id, formID); err == nil && ok {
			done++
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/forms/%d/records?status=complete&backfilled=%d", formID, done), http.StatusSeeOther)
}

// backfillRecordTx snapshots a completed record's current results against its latest
// 'completed' event, but only if the record has no snapshot yet (idempotent). Scoped to
// formID. Returns true if a snapshot was written.
func (h *Handler) backfillRecordTx(ctx context.Context, recordID, formID int) (bool, error) {
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

	// Eligible only if the record is a completed, active record of this form, has a
	// 'completed' event to attach to, and has no snapshot yet. Returns that event's id.
	var eventID int
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT TOP 1 re.id
		FROM %s re
		JOIN %s tr ON tr.id = re.test_record_id
		WHERE re.test_record_id = @p1 AND re.event_type = 'completed'
		  AND tr.form_id = @p2 AND tr.is_locked = 1 AND tr.is_active = 1
		  AND NOT EXISTS (SELECT 1 FROM %s x JOIN %s e ON e.id = x.event_id WHERE e.test_record_id = @p1)
		ORDER BY re.event_date DESC, re.id DESC`,
		h.cfg.RecordEventsTable(), h.cfg.RecordsTable(),
		h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), recordID, formID).Scan(&eventID)
	if err == sql.ErrNoRows {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		committed = true
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := h.snapshotRecordResults(ctx, tx, eventID, recordID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

// loadEventSnapshots returns the result snapshot for each Complete event of a record, keyed
// by event_id and already diffed against the previous Complete snapshot (added/changed/
// unchanged). Events without a snapshot are absent from the map.
func (h *Handler) loadEventSnapshots(ctx context.Context, recordID int) (map[int][]models.SnapshotDiffRow, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT rer.event_id, rer.test_id, COALESCE(rer.parameter,''), COALESCE(rer.result,''),
		       rer.pass_fail, COALESCE(rer.comment,'')
		FROM %s rer
		JOIN %s re ON re.id = rer.event_id
		WHERE re.test_record_id = @p1
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
		if err := rows.Scan(&s.EventID, &s.TestID, &s.Parameter, &s.Result, &s.PassFail, &s.Comment); err != nil {
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

	res, err := tx.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET is_locked=1, updated_at=GETDATE() WHERE id=@p1 AND is_locked=0"+guard,
		h.cfg.RecordsTable()), args...)
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
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (test_record_id, event_type, username, event_date)
		 OUTPUT INSERTED.id VALUES (@p1, 'completed', @p2, GETDATE())`,
		h.cfg.RecordEventsTable()), recordID, username).Scan(&eventID); err != nil {
		return err
	}
	return h.snapshotRecordResults(ctx, tx, eventID, recordID)
}

// diffSnapshot annotates each row of a Complete-event result snapshot with how it changed
// vs. the previous Complete snapshot, matching on test_id. prev is nil for the earliest
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
