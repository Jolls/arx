//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// Covers records.go's LockRecord/ApproveRecord/UnlockRecord/BulkLockRecords — the
// is_locked/is_approved WIP/Complete/Approved state machine — and the completion-audit
// chain they drive in records_history.go (completeRecordTx -> logCompletionSnapshot ->
// snapshotRecordResults). #806.
//
// Does NOT reuse the pinned seed records 7001-7014: TestIntegration_UpdatedAtSentinel
// asserts those keep an untouched updated_at sentinel, and locking/approving/unlocking
// any of them would break that test. Every test here seeds its own throwaway
// part/form/form_row/form_record/result rows and cleans them up.

// seedLockTestForm creates a throwaway part + form + one data-row form_row, returning
// both ids plus the form_row id and a cleanup func. Shared by every test in this file.
func seedLockTestForm(t *testing.T, h *Handler, ctx context.Context) (partID, formID, testID int, cleanup func()) {
	t.Helper()
	partNumber := "ITEST-LOCK-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter) OUTPUT INSERTED.id VALUES (@p1, 0, 'Output Voltage')`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg.FormsTable()), strconv.Itoa(testID), formID); err != nil {
		t.Fatalf("set form test_order: %v", err)
	}

	cleanup = func() {
		smokeExec(ctx, h, fmt.Sprintf(
			`DELETE FROM %s WHERE event_id IN (SELECT id FROM %s WHERE form_record_id IN (SELECT id FROM %s WHERE form_id=@p1))`,
			h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable(), h.cfg.RecordsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(
			`DELETE FROM %s WHERE form_record_id IN (SELECT id FROM %s WHERE form_id=@p1)`,
			h.cfg.RecordEventsTable(), h.cfg.RecordsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(
			`DELETE FROM %s WHERE form_record_id IN (SELECT id FROM %s WHERE form_id=@p1)`,
			h.cfg.ResultsTable(), h.cfg.RecordsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}
	return partID, formID, testID, cleanup
}

// seedWIPRecord inserts one WIP form_record (is_locked=0, is_approved=0) under formID
// with a single result row (form_row_id=testID), returning the new record id.
func seedWIPRecord(t *testing.T, h *Handler, ctx context.Context, formID, testID int, serial string) int {
	t.Helper()
	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, record_date, serial_number, subject_part_number, subject_pn_description,
		 test_order, record_type, is_locked, is_approved, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '2026-07-01', @p2, '', '', @p3, 'New Release', 0, 0, 1)`,
		h.cfg.RecordsTable()), formID, serial, strconv.Itoa(testID),
	).Scan(&recordID); err != nil {
		t.Fatalf("seed WIP record: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_record_id, form_row_id, result, pass_fail, parameter) VALUES (@p1, @p2, '5.00', 1, 'Output Voltage')`,
		h.cfg.ResultsTable()), recordID, testID); err != nil {
		t.Fatalf("seed WIP record result: %v", err)
	}
	return recordID
}

// reviewerCtx returns req with a TR-reviewer session user (CanApproveRecords=true).
func reviewerCtx(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey,
		&User{ID: 8001, Username: "admin", CanApproveRecords: true}))
}

// nonReviewerCtx returns req with a logged-in but non-reviewer session user.
func nonReviewerCtx(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey,
		&User{ID: 8002, Username: "tester"}))
}

// Full WIP -> Complete -> Approved -> Unlock(back to WIP) -> re-Complete sequence on
// one record, asserting DB state and audit rows after each transition. All four
// handlers redirect with http.StatusSeeOther (303), not 302.
func TestIntegration_LockApproveUnlockLifecycle(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	_, formID, testID, formCleanup := seedLockTestForm(t, h, ctx)
	defer formCleanup()
	recordID := seedWIPRecord(t, h, ctx, formID, testID, "L1")

	// 1. Lock (WIP -> Complete).
	rec := httptest.NewRecorder()
	h.LockRecord(rec, adminCtx(withID(postForm(fmt.Sprintf("/records/%d/lock", recordID), url.Values{}), recordID)))
	assertStatus(t, "LockRecord", rec, http.StatusSeeOther)

	var isLocked, isApproved bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_locked, is_approved FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&isLocked, &isApproved); err != nil {
		t.Fatalf("select after lock: %v", err)
	}
	if !isLocked || isApproved {
		t.Fatalf("after lock: is_locked=%v is_approved=%v, want true/false", isLocked, isApproved)
	}
	assertEventCount(t, h, ctx, recordID, "completed", 1)
	assertSnapshotRowCount(t, h, ctx, recordID, 1)
	var snapResult string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 rer.result FROM %s rer JOIN %s re ON re.id=rer.event_id WHERE re.form_record_id=@p1`,
		h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), recordID,
	).Scan(&snapResult); err != nil {
		t.Fatalf("select snapshot result: %v", err)
	}
	if snapResult != "5.00" {
		t.Errorf("snapshot result = %q, want %q", snapResult, "5.00")
	}

	// 2. Lock again — idempotent no-op (guard blocks the second UPDATE, so no second event).
	rec2 := httptest.NewRecorder()
	h.LockRecord(rec2, adminCtx(withID(postForm(fmt.Sprintf("/records/%d/lock", recordID), url.Values{}), recordID)))
	assertStatus(t, "LockRecord (again)", rec2, http.StatusSeeOther)
	assertEventCount(t, h, ctx, recordID, "completed", 1)

	// 3. Approve without permission -> 403.
	rec3 := httptest.NewRecorder()
	h.ApproveRecord(rec3, nonReviewerCtx(withID(postForm(fmt.Sprintf("/records/%d/approve", recordID), url.Values{}), recordID)))
	assertStatus(t, "ApproveRecord (no permission)", rec3, http.StatusForbidden)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_approved FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&isApproved); err != nil {
		t.Fatalf("select after forbidden approve: %v", err)
	}
	if isApproved {
		t.Errorf("is_approved = true after forbidden approve, want false")
	}

	// 4. Approve with permission (Complete -> Approved).
	rec4 := httptest.NewRecorder()
	h.ApproveRecord(rec4, reviewerCtx(withID(postForm(fmt.Sprintf("/records/%d/approve", recordID), url.Values{}), recordID)))
	assertStatus(t, "ApproveRecord", rec4, http.StatusSeeOther)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_approved FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&isApproved); err != nil {
		t.Fatalf("select after approve: %v", err)
	}
	if !isApproved {
		t.Fatalf("is_approved = false after approve, want true")
	}
	assertEventCount(t, h, ctx, recordID, "approved", 1)

	// 5. Approve again (already approved) -> no-op.
	rec5 := httptest.NewRecorder()
	h.ApproveRecord(rec5, reviewerCtx(withID(postForm(fmt.Sprintf("/records/%d/approve", recordID), url.Values{}), recordID)))
	assertStatus(t, "ApproveRecord (again)", rec5, http.StatusSeeOther)
	assertEventCount(t, h, ctx, recordID, "approved", 1)

	// 6. Unlock an approved record without permission -> 403.
	rec6 := httptest.NewRecorder()
	h.UnlockRecord(rec6, nonReviewerCtx(withID(postForm(fmt.Sprintf("/records/%d/unlock", recordID), url.Values{"comment": {"trying to unlock"}}), recordID)))
	assertStatus(t, "UnlockRecord (no permission)", rec6, http.StatusForbidden)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_locked, is_approved FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&isLocked, &isApproved); err != nil {
		t.Fatalf("select after forbidden unlock: %v", err)
	}
	if !isLocked || !isApproved {
		t.Errorf("after forbidden unlock: is_locked=%v is_approved=%v, want true/true (unchanged)", isLocked, isApproved)
	}

	// 7. Unlock without a comment -> 400.
	rec7 := httptest.NewRecorder()
	h.UnlockRecord(rec7, reviewerCtx(withID(postForm(fmt.Sprintf("/records/%d/unlock", recordID), url.Values{}), recordID)))
	assertStatus(t, "UnlockRecord (no comment)", rec7, http.StatusBadRequest)

	// 8. Unlock with permission + comment (Approved -> WIP).
	rec8 := httptest.NewRecorder()
	h.UnlockRecord(rec8, reviewerCtx(withID(postForm(fmt.Sprintf("/records/%d/unlock", recordID), url.Values{"comment": {"correcting a reading"}}), recordID)))
	assertStatus(t, "UnlockRecord", rec8, http.StatusSeeOther)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_locked, is_approved FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&isLocked, &isApproved); err != nil {
		t.Fatalf("select after unlock: %v", err)
	}
	if isLocked || isApproved {
		t.Fatalf("after unlock: is_locked=%v is_approved=%v, want false/false", isLocked, isApproved)
	}
	assertEventCount(t, h, ctx, recordID, "unlocked", 1)
	var unlockComment string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 comments FROM %s WHERE form_record_id=@p1 AND event_type='unlocked'`, h.cfg.RecordEventsTable()), recordID,
	).Scan(&unlockComment); err != nil {
		t.Fatalf("select unlock comment: %v", err)
	}
	if unlockComment != "correcting a reading" {
		t.Errorf("unlock comment = %q, want %q", unlockComment, "correcting a reading")
	}

	// 9. Re-lock (second WIP -> Complete) creates a second snapshot. Change the seeded
	// result value first so the second snapshot differs from the first.
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET result='5.05' WHERE form_record_id=@p1 AND form_row_id=@p2`, h.cfg.ResultsTable()),
		recordID, testID); err != nil {
		t.Fatalf("update result before re-lock: %v", err)
	}
	rec9 := httptest.NewRecorder()
	h.LockRecord(rec9, adminCtx(withID(postForm(fmt.Sprintf("/records/%d/lock", recordID), url.Values{}), recordID)))
	assertStatus(t, "LockRecord (re-lock)", rec9, http.StatusSeeOther)
	assertEventCount(t, h, ctx, recordID, "completed", 2)
	assertSnapshotRowCount(t, h, ctx, recordID, 2)
	var secondSnapResult string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 rer.result FROM %s rer JOIN %s re ON re.id=rer.event_id
		 WHERE re.form_record_id=@p1 ORDER BY re.id DESC`,
		h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), recordID,
	).Scan(&secondSnapResult); err != nil {
		t.Fatalf("select second snapshot result: %v", err)
	}
	if secondSnapResult != "5.05" {
		t.Errorf("second snapshot result = %q, want %q", secondSnapResult, "5.05")
	}
}

// assertEventCount fails the test if the record's record_events count for eventType
// doesn't match want.
func assertEventCount(t *testing.T, h *Handler, ctx context.Context, recordID int, eventType string, want int) {
	t.Helper()
	var got int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE form_record_id=@p1 AND event_type=@p2`, h.cfg.RecordEventsTable()),
		recordID, eventType,
	).Scan(&got); err != nil {
		t.Fatalf("count %s events: %v", eventType, err)
	}
	if got != want {
		t.Errorf("%s event count = %d, want %d", eventType, got, want)
	}
}

// assertSnapshotRowCount fails the test if the record's total record_event_results row
// count (across all its Complete events) doesn't match want.
func assertSnapshotRowCount(t *testing.T, h *Handler, ctx context.Context, recordID int, want int) {
	t.Helper()
	var got int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s rer JOIN %s re ON re.id=rer.event_id WHERE re.form_record_id=@p1`,
		h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), recordID,
	).Scan(&got); err != nil {
		t.Fatalf("count snapshot rows: %v", err)
	}
	if got != want {
		t.Errorf("snapshot row count = %d, want %d", got, want)
	}
}

// Three WIP records under one form, plus a fourth under a different form, prove
// BulkLockRecords locks only the target form's records and skips invalid ids.
func TestIntegration_BulkLockRecords(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	_, formID, testID, formCleanup := seedLockTestForm(t, h, ctx)
	defer formCleanup()
	_, otherFormID, otherTestID, otherFormCleanup := seedLockTestForm(t, h, ctx)
	defer otherFormCleanup()

	id1 := seedWIPRecord(t, h, ctx, formID, testID, "B1")
	id2 := seedWIPRecord(t, h, ctx, formID, testID, "B2")
	id3 := seedWIPRecord(t, h, ctx, formID, testID, "B3")
	id4 := seedWIPRecord(t, h, ctx, otherFormID, otherTestID, "B4")

	vals := url.Values{"record_ids[]": {
		strconv.Itoa(id1), strconv.Itoa(id2), strconv.Itoa(id3), strconv.Itoa(id4), "not-a-number", "0",
	}}
	rec := httptest.NewRecorder()
	h.BulkLockRecords(rec, adminCtx(withID(postForm(fmt.Sprintf("/forms/%d/records/bulk-lock", formID), vals), formID)))
	assertStatus(t, "BulkLockRecords", rec, http.StatusSeeOther)

	wantLocation := fmt.Sprintf("/forms/%d/records?locked=3", formID)
	if got := rec.Header().Get("Location"); got != wantLocation {
		t.Errorf("Location = %q, want %q", got, wantLocation)
	}

	for _, id := range []int{id1, id2, id3} {
		var isLocked bool
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT is_locked FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), id,
		).Scan(&isLocked); err != nil {
			t.Fatalf("select record %d: %v", id, err)
		}
		if !isLocked {
			t.Errorf("record %d: is_locked = false, want true", id)
		}
		assertEventCount(t, h, ctx, id, "completed", 1)
	}

	var id4Locked bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_locked FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), id4,
	).Scan(&id4Locked); err != nil {
		t.Fatalf("select record %d: %v", id4, err)
	}
	if id4Locked {
		t.Errorf("record %d (different form): is_locked = true, want false (form guard should hold)", id4)
	}
	assertEventCount(t, h, ctx, id4, "completed", 0)
}

// Documents CURRENT behavior: the lot-tracked-needs-a-lot gate (errRecordNeedsLot) is
// disabled for 0.6.0 (#677/#687, deferred to #702's lot/build picker — see
// records_history.go's completeRecordTx comment). LockRecord succeeds today despite no
// lot being linked. If that gate is re-enabled alongside #702's picker, this assertion
// should flip to expect http.StatusBadRequest and a body containing errRecordNeedsLot's
// message — this test is written to fail loudly at that point, not silently.
func TestIntegration_LockLotTrackedRecordWithNoLot(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const lotTrackedPart = 3007 // RAW-1002, tracking_mode='lot' in seed

	var formID, testID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), lotTrackedPart,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter) OUTPUT INSERTED.id VALUES (@p1, 0, 'Weight')`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg.FormsTable()), strconv.Itoa(testID), formID); err != nil {
		t.Fatalf("set form test_order: %v", err)
	}
	// part_id/lot_id/unit_id NULL — no lot linked, the scenario the issue describes.
	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, part_id, record_date, serial_number, subject_part_number, subject_pn_description,
		 test_order, record_type, is_locked, is_approved, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, @p2, '2026-07-01', 'NL1', '', '', @p3, 'New Release', 0, 0, 1)`,
		h.cfg.RecordsTable()), formID, lotTrackedPart, strconv.Itoa(testID),
	).Scan(&recordID); err != nil {
		t.Fatalf("seed lot-tracked WIP record: %v", err)
	}

	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(
			`DELETE FROM %s WHERE event_id IN (SELECT id FROM %s WHERE form_record_id=@p1)`,
			h.cfg.RecordEventResultsTable(), h.cfg.RecordEventsTable()), recordID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_record_id=@p1`, h.cfg.RecordEventsTable()), recordID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	}()

	rec := httptest.NewRecorder()
	h.LockRecord(rec, adminCtx(withID(postForm(fmt.Sprintf("/records/%d/lock", recordID), url.Values{}), recordID)))
	// #677/#687: gate disabled for 0.6.0 — succeeds despite no lot. Flip to
	// http.StatusBadRequest once the gate is re-enabled alongside #702's lot/build picker.
	assertStatus(t, "LockRecord (lot-tracked, no lot)", rec, http.StatusSeeOther)

	var isLocked bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_locked FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&isLocked); err != nil {
		t.Fatalf("select after lock: %v", err)
	}
	if !isLocked {
		t.Errorf("is_locked = false, want true (current disabled-gate behavior)")
	}
}
