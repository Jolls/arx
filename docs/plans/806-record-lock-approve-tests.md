# #806 — Record lock/approve state machine tests

## Scope

Add integration coverage (real ArxDev tx/state) for `records.go`'s
`LockRecord` (~1908), `ApproveRecord` (~1936), `UnlockRecord` (~1972),
`BulkLockRecords` (~2031), and the completion-audit chain they drive in
`records_history.go` (`completeRecordTx` → `logCompletionSnapshot` →
`snapshotRecordResults`).

New file: **`arx_go/records_lock_integration_test.go`** (`//go:build integration`,
`package main`). Follows the existing pattern in `arx_go/integration_test.go`:
reuses its unexported helpers (`liveHandler`, `withID`, `postForm`, `assertStatus`,
`assert302`, `adminCtx`, `ctxUserKey`) since they're in the same package/build tag —
do not redeclare them.

Do **not** reuse the pinned seed records 7001–7014: `TestIntegration_UpdatedAtSentinel`
asserts records 7001–7005 (and steps 6101–6108, etc.) keep an untouched `updated_at`
sentinel. Locking/approving/unlocking any of them would break that test. Every test
below seeds its own throwaway part/form/form_row/form_record/result rows (mirroring
`TestIntegration_RecordFilters`'s seeding style) and cleans them up in `defer`.

## Shared seed helper

Add a helper at the top of the new file:

```go
// seedLockTestForm creates a throwaway part + form + one data-row form_row,
// returning both ids plus a cleanup func. Shared by every test in this file.
func seedLockTestForm(t *testing.T, h *Handler, ctx context.Context) (partID, formID, testID int, cleanup func())
```

Body:
1. INSERT into `h.cfg.PartsTable()` — `part_number` = `"ITEST-LOCK-" + strconv.FormatInt(time.Now().UnixNano(), 10)`, `revision='A'`, `title='Integration Test Part'`, `release_status='U'`, `is_active=1`. Scan new `partID`.
2. INSERT into `h.cfg.FormsTable()` — `part_number_id=@partID`, `test_order=''`, `is_locked=0`, `is_active=1`. Scan `formID`.
3. INSERT into `h.cfg.StepsTable()` — `form_id=@formID`, `type=0`, `parameter='Output Voltage'`. Scan `testID` (form_row id).
4. `UPDATE` the form's `test_order` to `strconv.Itoa(testID)` (mirrors `TestIntegration_AttachStepPassFail`'s pattern) so `snapshotRecordResults`'s `test_order`-based ordering has something to key off.
5. `cleanup` deletes, in FK order: `record_event_results` (via join through `record_events.form_record_id` in this form's records), `record_events` (same join), `result` (`form_record_id IN (SELECT id FROM records WHERE form_id=@formID)`), `form_record` (`form_id=@formID`), `form_row` (`id=@testID`), `form` (`id=@formID`), `part` (`id=@partID`). Use `smokeExec`-style best-effort deletes (ignore errors, matching existing cleanup blocks) or plain `_, _ = h.DB().ExecContext(...)`.

Add a second helper:

```go
// seedWIPRecord inserts one WIP form_record (is_locked=0, is_approved=0) under
// formID with a single result row (form_row_id=testID, no result value set — MISSING),
// returning the new record id.
func seedWIPRecord(t *testing.T, h *Handler, ctx context.Context, formID, testID int, serial string) int
```

INSERT into `h.cfg.RecordsTable()`: `form_id=@formID`, `record_date='2026-07-01'`,
`serial_number=@serial`, `subject_part_number=''`, `subject_pn_description=''`,
`test_order=strconv.Itoa(testID)`, `comments='New Release'`, `is_locked=0`,
`is_approved=0`, `is_active=1`. Scan new id. Then INSERT into `h.cfg.ResultsTable()`:
`form_record_id=@newID`, `form_row_id=@testID`, `result='5.00'`, `pass_fail=1`,
`parameter='Output Voltage'` (type defaults to 0/NULL — a data row, matching
`snapshotRecordResults`'s `COALESCE(type,0)=0` filter). Return the record id.

Context helpers (add alongside, local to this file since `adminCtx` in
`integration_test.go` doesn't set `CanApproveRecords`):

```go
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
```

## Test 1 — `TestIntegration_LockApproveUnlockLifecycle`

Full WIP → Complete → Approved → Unlock(back to WIP) → re-Complete sequence on
one record, asserting DB state and audit rows after each transition.

Setup: `seedLockTestForm`, then `seedWIPRecord` → `recordID`.

Steps (each posts through the real handler, not raw SQL, then asserts via a
direct `SELECT`):

1. **Lock (WIP → Complete).**
   `h.LockRecord(rec, adminCtx(withID(postForm(fmt.Sprintf("/records/%d/lock", recordID), url.Values{}), recordID)))`
   - `assert302(t, "LockRecord", rec)`; Location == `/records/<id>`.
   - `SELECT is_locked, is_approved FROM records WHERE id=@p1` → `is_locked=true, is_approved=false`.
   - `SELECT COUNT(*) FROM record_events WHERE form_record_id=@p1 AND event_type='completed'` → `1`.
   - `SELECT COUNT(*) FROM record_event_results rer JOIN record_events re ON re.id=rer.event_id WHERE re.form_record_id=@p1` → `1` (one snapshot row, matching the single seeded result), and that row's `result='5.00'`, `parameter='Output Voltage'`.

2. **Lock again (idempotency / no-op on already-locked).**
   Call `h.LockRecord` again with the same recordID/ctx.
   - `assert302` still (handler doesn't special-case this — it just redirects regardless of `RowsAffected`).
   - `SELECT COUNT(*) FROM record_events WHERE form_record_id=@p1 AND event_type='completed'` still `1` (guard `is_locked=@bool(false)` in the UPDATE means the second call's `UPDATE` affects 0 rows, so `logCompletionSnapshot` is not called again — no duplicate event/snapshot).

3. **Approve without permission → 403.**
   `h.ApproveRecord(rec, nonReviewerCtx(withID(postForm(.../approve, {}), recordID)))`
   - `assertStatus(..., http.StatusForbidden)`.
   - `SELECT is_approved` still `false`.

4. **Approve with permission (Complete → Approved).**
   `h.ApproveRecord(rec, reviewerCtx(withID(postForm(.../approve, {}), recordID)))`
   - `assert302`.
   - `SELECT is_approved` → `true`.
   - `SELECT COUNT(*) FROM record_events WHERE form_record_id=@p1 AND event_type='approved'` → `1`.

5. **Approve again (already approved) → no-op.**
   Same call repeated.
   - `assert302` (handler always redirects; guard `is_approved=@bool(false)` means 0 rows affected).
   - `record_events` `approved` count still `1`.

6. **Unlock an approved record without permission → 403.**
   `h.UnlockRecord(rec, nonReviewerCtx(withID(postForm(.../unlock, url.Values{"comment": {"trying to unlock"}}), recordID)))`
   - `assertStatus(..., http.StatusForbidden)`.
   - `is_locked`/`is_approved` unchanged (`true`/`true`).

7. **Unlock without a comment → 400.**
   `h.UnlockRecord(rec, reviewerCtx(withID(postForm(.../unlock, url.Values{}), recordID)))`
   - `assertStatus(..., http.StatusBadRequest)`.

8. **Unlock with permission + comment (Approved → WIP).**
   `h.UnlockRecord(rec, reviewerCtx(withID(postForm(.../unlock, url.Values{"comment": {"correcting a reading"}}), recordID)))`
   - `assert302`.
   - `SELECT is_locked, is_approved` → `false, false`.
   - `SELECT COUNT(*) FROM record_events WHERE form_record_id=@p1 AND event_type='unlocked'` → `1`; and that row's `comments = 'correcting a reading'`.

9. **Re-lock (second WIP → Complete) creates a second snapshot.**
   Update the seeded result row's value first (e.g. `UPDATE result SET result='5.05' WHERE form_record_id=@recordID AND form_row_id=@testID`) so the second snapshot differs from the first — this exercises `logCompletionSnapshot` being called a second time for the same record, proving it isn't a "first completion only" path.
   Call `h.LockRecord` again (adminCtx).
   - `assert302`.
   - `SELECT COUNT(*) FROM record_events WHERE form_record_id=@p1 AND event_type='completed'` → `2` now.
   - `SELECT COUNT(*) FROM record_event_results rer JOIN record_events re ON re.id=rer.event_id WHERE re.form_record_id=@p1` → `2` total snapshot rows across both events (1 + 1), and the second event's snapshot row has `result='5.05'`.

## Test 2 — `TestIntegration_BulkLockRecords`

Setup: `seedLockTestForm` → `formID`/`testID`. Seed **three** WIP records under
`formID` via `seedWIPRecord` (serials `"B1"`, `"B2"`, `"B3"`) → `id1, id2, id3`.
Also seed a **fourth** WIP record under a *different* throwaway form (call
`seedLockTestForm` a second time, or inline a second form under the same part) →
`otherFormID`/`id4` — this proves `BulkLockRecords`'s `formID` guard
(`completeRecordTx(ctx, id, formID, username)` passes `formID>0`, adding
`AND form_id=@p2` to the UPDATE) actually scopes the bulk action to the target form.

Call:
```go
vals := url.Values{"record_ids[]": {strconv.Itoa(id1), strconv.Itoa(id2), strconv.Itoa(id3), strconv.Itoa(id4), "not-a-number", "0"}}
rec := httptest.NewRecorder()
h.BulkLockRecords(rec, adminCtx(withID(postForm(fmt.Sprintf("/forms/%d/records/bulk-lock", formID), vals), formID)))
```
(`BulkLockRecords` reads the form id via `chi.URLParam(r, "id")`, so use `withID`.)

Assertions:
- `assert302(t, "BulkLockRecords", rec)`.
- `Location` header equals `/forms/<formID>/records?locked=3` (id4 doesn't count — wrong form; `not-a-number` and `0` are skipped by the `strconv.Atoi`/`id<=0` guard before ever reaching `completeRecordTx`).
- `SELECT is_locked FROM records WHERE id IN (id1,id2,id3)` → all `true`.
- `SELECT is_locked FROM records WHERE id=id4` → still `false` (form guard held).
- `SELECT COUNT(*) FROM record_events WHERE form_record_id IN (id1,id2,id3) AND event_type='completed'` → `3` (one per locked record).
- `SELECT COUNT(*) FROM record_events WHERE form_record_id=id4` → `0`.

## Test 3 — lot-tracked completion (documents current disabled-gate behavior)

**Read `completeRecordTx` in `arx_go/records_history.go` (lines ~146–150) before
writing this test** — it currently contains only a comment:

> `#677/#687 — the lot-tracked-needs-a-lot gate is disabled for 0.6.0: the
> lot/build picker UI is hidden (unfinished, deferred to v0.7.0 #702), so
> there's no way for a user to satisfy this check. Re-enable alongside the
> picker.`

There is **no code path** in `completeRecordTx` that returns `errRecordNeedsLot`
today — the UPDATE has no lot-linkage check at all. `errors.Is(err, errRecordNeedsLot)`
in `LockRecord` is live but unreachable. See "Open question" below — this test
documents *current* behavior (completion succeeds despite no lot) rather than the
400 path the issue describes, pending that decision.

`TestIntegration_LockLotTrackedRecordWithNoLot`:

Setup: reuse existing seed part **3007** (`RAW-1002`, `tracking_mode='lot'`,
lot-tracked per `SQL/seed_test_data.sql` line 412/416) rather than a throwaway
part — its tracking mode is already pinned by the seed. Create a throwaway form
under part 3007 (`h.cfg.FormsTable()`, `part_number_id=3007`) + one form_row, then
a WIP `form_record` with `part_id=3007`, `lot_id=NULL`, `unit_id=NULL` (no lot
linked — the exact scenario in the issue). Clean up form/form_row/form_record/
result rows in `defer`; do not touch part 3007 itself (shared seed row).

- Call `h.LockRecord` (adminCtx) on this record.
- Assert `assert302` (current behavior: succeeds) and `is_locked=true` afterward.
- Comment directly above the assertion citing #677/#687 and this plan, so a
  future re-enable of the gate (alongside the #702 picker) fails this test loudly
  instead of silently — the fix at that point is to flip the assertion to expect
  `StatusBadRequest` and a body containing `errRecordNeedsLot`'s message.

## Resolved decision

Confirmed: Test 3 (`TestIntegration_LockLotTrackedRecordWithNoLot`) proceeds
exactly as scoped above — assert current behavior (`LockRecord` succeeds,
`is_locked=true`) with a comment citing #677/#687/#702 directly above the
assertion, so a future re-enable of the gate fails this test loudly instead of
silently.
