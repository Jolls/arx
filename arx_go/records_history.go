package main

import (
	"context"
	"errors"
	"sort"

	"arx/arx_go/models"
	"arx/internal/records"
)

// errRecordNeedsLot is returned by completeRecordTx when a lot-tracked part's record has
// no lot linked yet (#687) — surfaced by callers as a 400, not a server error.
var errRecordNeedsLot = errors.New("a lot must be selected before this record can be completed — the tested part is lot-tracked")

// snapshotRecordResults copies the record's current data-row results (result) into
// record_event_results, linked to the given Complete event. Rows are inserted in the
// record's frozen display order (test_order, falling back to form_row_id order) so the snapshot
// renders by id. Headings (type > 0) are not captured. svc runs on the caller's tx.
func (h *Handler) snapshotRecordResults(ctx context.Context, svc *records.Service, eventID, recordID int) error {
	rec, err := svc.GetRecord(ctx, recordID)
	if err != nil {
		return err
	}
	rows, err := svc.ListResults(ctx, recordID)
	if err != nil {
		return err
	}
	byTest := map[int]models.RecordResultSnapshot{}
	for _, r := range rows {
		if r.Type != 0 {
			continue
		}
		s := models.RecordResultSnapshot{TestID: r.FormRowID, Parameter: r.Parameter, Specification: r.Specification,
			SpecUnits: r.SpecUnits, Result: r.Result, Comment: r.Comment}
		if r.PassFail.Valid {
			s.PassFail = &r.PassFail.Bool
		}
		byTest[s.TestID] = s
	}

	for _, tid := range orderedResultIDs(rec.TestOrder, byTest) {
		s := byTest[tid]
		if err := svc.InsertEventResult(ctx, records.EventResult{EventID: eventID, FormRowID: tid, Parameter: s.Parameter,
			Specification: s.Specification, SpecUnits: s.SpecUnits, Result: s.Result, PassFail: s.PassFail, Comment: s.Comment}); err != nil {
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
	rows, err := h.records().ListEventResults(ctx, recordID)
	if err != nil {
		return nil, err
	}

	var order []int // event ids in chronological order
	grouped := map[int][]models.RecordResultSnapshot{}
	for _, e := range rows {
		s := models.RecordResultSnapshot{EventID: e.EventID, TestID: e.FormRowID, Parameter: e.Parameter,
			Specification: e.Specification, SpecUnits: e.SpecUnits, Result: e.Result, PassFail: e.PassFail, Comment: e.Comment}
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

	svc := records.New(tx)
	completed, err := svc.CompleteRecord(ctx, recordID, formID)
	if err != nil {
		return false, err
	}
	if completed {
		if err := h.logCompletionSnapshot(ctx, svc, recordID, username); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return completed, nil
}

// logCompletionSnapshot writes the 'completed' record_events row for a just-locked record and
// captures its result snapshot, within the caller's tx (svc). Shared by single + bulk lock.
func (h *Handler) logCompletionSnapshot(ctx context.Context, svc *records.Service, recordID int, username string) error {
	eventID, err := svc.InsertRecordEvent(ctx, recordID, "completed", username, "")
	if err != nil {
		return err
	}
	return h.snapshotRecordResults(ctx, svc, eventID, recordID)
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
			case row.Result != p.Result || row.Comment != p.Comment || !boolPtrEqual(row.PassFail, p.PassFail) ||
				row.Specification != p.Specification || row.SpecUnits != p.SpecUnits || row.Parameter != p.Parameter:
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
