# Plan: #813 — Form definition CRUD + history test coverage

## Scope
Add build-tagged integration tests (`//go:build integration`) for `arx_go/records.go`
handlers: `FormDef`, `FormDefHistory`, `EditFormDef`, `SaveFormDef`, `ArchiveStep`.
These handlers are DB-driven (queries, a transaction, a DB trigger for history) with no
pure-Go logic worth unit-testing in isolation — follow the existing pattern in
`arx_go/integration_test.go` (live ArxDev connection via `liveHandler(t)`, handlers called
directly with `httptest.NewRecorder()` + `withID`/`chi.NewRouteContext()`, no CSRF token
needed since middleware is bypassed when calling handlers directly). Do NOT add unit tests
or new helper files — everything goes in `arx_go/integration_test.go`.

## File to edit
`arx_go/integration_test.go` — append new test functions at the end of the file. Reuse
existing helpers already in that file: `liveHandler`, `withID`, `postForm`, `assert302`,
`assertStatus`. Add one new helper (see below).

## New helper needed
```go
// withIDAndTestID injects chi route params "id" and "testID" (used by ArchiveStep's
// POST /forms/{id}/tests/{testID}/archive route).
func withIDAndTestID(req *http.Request, id, testID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("testID", strconv.Itoa(testID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
```
Place it next to `withIDAndAttID` (around line 61 of the current file).

## Relevant fixed seed data (SQL/seed_test_data.sql, do not modify)
- `form` 6001: part_number_id 3010, `test_order = '6101,6102,6103,6104,6105,6106,6107,6108'`,
  `is_locked=1`, `revision=1`.
- `form_row` (steps) on form 6001:
  - 6101 heading "Electrical Tests" (type=1)
  - 6102 "Output Voltage", spec_nom '5.00', not archived
  - 6103 "Current Draw", spec_max currently `'200'` (seed does `UPDATE form_row SET
    spec_max='200' WHERE id=6103` right after insert — original inserted value was
    `'250'` — this UPDATE is what wrote the one pre-existing `form_row_history` row for
    6103, but its `changed_at` is whatever moment the seed script last ran, NOT a fixed
    date — do not rely on it for `FormDefHistory` tests; create your own throwaway
    history row instead, see below).
  - 6104 "Insulation Resistance", `archived=1` (the seeded archived/retired step)
  - 6105-6108: `pf_type` filled/comment/list/range variants, 6108 has `hide_formula =
    '{record.type}!=Re-Test'`.
- `form_record` 7002 (locked, `is_locked=1`, form_id 6001, `test_order =
  '6101,6102,6103,6104'`, `form_revision=1`) — a locked record whose frozen snapshot must
  never change. Do NOT mutate form 6001's steps/order in tests that check this — seed
  form_row rows 6101-6108 have `updated_at` pinned to a sentinel checked by
  `TestIntegration_UpdatedAtSentinel`; any UPDATE through `SaveFormDef`/`ArchiveStep`
  against those rows would break that unrelated test. All mutating tests below must seed
  their OWN throwaway part/form/form_row/form_record rows, exactly like
  `TestIntegration_YieldSummary` etc. already do. Only GET-based read-only tests (FormDef,
  EditFormDef) may read seeded form 6001.

## Table helpers to use (never hardcode names)
`h.cfg.PartsTable()`, `h.cfg.FormsTable()`, `h.cfg.StepsTable()` (=`form_row`),
`h.cfg.RecordsTable()` (=`form_record`), `h.cfg.FormRowHistoryTable()`
(=`form_row_history`), `h.cfg.ResultsTable()`.

## Test 1 — TestIntegration_FormDef_RendersStepsAndArchivedToggle
Read-only, uses seeded form 6001. No cleanup needed.
```go
rec := httptest.NewRecorder()
h.FormDef(rec, withID(httptest.NewRequest(http.MethodGet, "/forms/6001/def", nil), 6001))
```
Assert:
- `rec.Code == http.StatusOK`.
- Body contains `"Show archived"` — proves `HasArchived` was computed true because seeded
  step 6104 is archived.
- Body contains `"Insulation Resistance"` — proves the archived step is still rendered in
  the HTML (only CSS/JS-hidden via the toggle), matching the code comment at
  `records.go` ~459-463 ("Archived steps are still rendered here but hidden by default").
  A regression that skips archived steps entirely from the definition view must fail this.
- Body contains `"Retest Voltage Check"` (step 6108) — proves the unresolvable
  `hide_formula` (`{record.type}!=Re-Test`, no record context in this view) does NOT hide
  the step, per the documented "fall back to showing the step" behavior at ~454-456.
- Body contains `"Output Voltage"` and `"Current Draw"` (basic sanity that ordinary steps
  render).

## Test 2 — TestIntegration_FormDefHistory_ReturnsPreChangeSnapshot
Do NOT use the seeded form_row_history row (nondeterministic `changed_at`). Seed a
throwaway part → form → form_row, make a raw UPDATE to create a deterministic same-day
history row, then query.
```go
h, cleanup := liveHandler(t)
defer cleanup()
ctx := context.Background()

// seed part (like TestIntegration_YieldSummary), form (test_order = the one step's id
// once known), and one form_row with spec_max = "100".
// ... INSERT part -> partID
// ... INSERT form (part_number_id=partID, test_order='', is_locked=0, is_active=1) -> formID
// ... INSERT form_row (form_id=formID, type=0, parameter='Historical Step', spec_max='100') -> testID
// UPDATE form (test_order=strconv.Itoa(testID)) so OrderedTestIDs works if needed (not
// required for FormDefHistory, which queries form_row directly by form_id).

// Raw UPDATE (triggers trg_form_row_history, snapshotting the OLD spec_max='100'):
h.DB().ExecContext(ctx, fmt.Sprintf(
    `UPDATE %s SET spec_max='150' WHERE id=@p1`, h.cfg.StepsTable()), testID)

defer cleanup of form_row_history WHERE form_row_id=testID, then form_row, form, part.

today := time.Now().Format("2006-01-02")
rec := httptest.NewRecorder()
req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/%d/def/history?at=%s", formID, today), nil)
h.FormDefHistory(rec, withID(req, formID))
```
Assert:
- `rec.Code == http.StatusOK`.
- Decode JSON body into `struct{ Steps []struct{ ID int; SpecMax string; Changed bool } \`json:"steps"\` }`.
- Exactly one step in the response, `ID == testID`.
- `Changed == true`.
- `SpecMax == "100"` — the PRE-change value from the history snapshot, NOT the current
  `"150"`. This is the core regression assertion for `FormDefHistory`: get this backwards
  (post-change value) and locked-record historical rendering silently shows the wrong
  spec.

Add a second query in the same test for the "no history that day" branch:
```go
yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
```
Re-call `FormDefHistory` with `at=yesterday`. Assert one step, `Changed == false`, and
`SpecMax == "150"` (falls back to the CURRENT `form_row` value per the query's `ELSE
COALESCE(t.spec_max,...)` branch).

## Test 3 — TestIntegration_EditFormDef_ShowsRawUnsubstitutedValues
Read-only, uses seeded form 6001. No cleanup needed.
```go
rec := httptest.NewRecorder()
h.EditFormDef(rec, withID(httptest.NewRequest(http.MethodGet, "/forms/6001/def/edit", nil), 6001))
```
Assert:
- `rec.Code == http.StatusOK`.
- Body contains the RAW, unresolved token
  `"query:recent_serial_numbers_for_form(@form_id={form.id})"` (step 6107's `spec_nom`) —
  proves `EditFormDef` does NOT run it through `substituteRefs` (per the code comment "no
  substituteRefs, we want to edit the actual stored values" at ~641). If someone later adds
  token substitution to this view, saved edits would corrupt the stored formula/query
  string — this test catches that.
- Body contains `"Show archived"` (same `HasArchived` check as Test 1, but on the edit
  view).

## Test 4 — TestIntegration_SaveFormDef_UpdatesStepSkipsUnchangedAndReordersWithNewRow
Seed a throwaway part + form + two form_row steps (stepA, stepB), both with known
original field values and `test_order = "<stepA>,<stepB>"`.

POST to `SaveFormDef` with:
- For stepA: `original_*` fields all equal to stepA's seeded values EXCEPT
  `parameter_<stepA>` set to a new value (e.g. `"Updated Parameter"`) while
  `original_parameter_<stepA>` is the old value — this is a real change.
- For stepB: ALL `<field>_<stepB>` values equal to `original_<field>_<stepB>` — no change
  submitted.
- One `new_row[0][type]=0`, `new_row[0][parameter]=New Step`, plus other
  `new_row[0][...]` fields blank.
- `step_order = "<stepB>,new_0,<stepA>"` (reordered, with the new row's client token).

```go
rec := httptest.NewRecorder()
h.SaveFormDef(rec, withID(postForm(fmt.Sprintf("/forms/%d/def/edit", formID), vals), formID))
assertStatus(t, "SaveFormDef", rec, http.StatusSeeOther)
```
Assert:
- stepA's `parameter` column in DB is now `"Updated Parameter"`.
- stepB's `parameter` column (and `updated_at`, seed it to a fixed sentinel like
  `'2020-01-01T00:00:00'` at insert time) is UNCHANGED — proves the "skip if nothing
  changed relative to original_*" branch (~789-806) doesn't blindly rewrite untouched
  rows (important for the documented concurrency guarantee: a concurrent editor's
  untouched-by-A change survives).
- A new `form_row` was inserted with `parameter = "New Step"` and `form_id = formID`
  (query `SELECT id FROM form_row WHERE form_id=@p1 AND parameter='New Step'`).
- `form.test_order` (re-SELECT) equals `"<stepB>,<newRealID>,<stepA>"` (the `new_0` token
  swapped for the real inserted ID, in submitted order) — proves the `new_X` token
  substitution and reordering logic (~956-987).

Cleanup: delete form_row_history rows for stepA/stepB (if any), the two seeded + one
inserted form_row rows, the form, the part.

## Test 5 — TestIntegration_ArchiveStep_TogglesArchivedFlag
Seed throwaway part + form + one form_row (`archived=0`).
```go
rec := httptest.NewRecorder()
h.ArchiveStep(rec, withIDAndTestID(
    postForm(fmt.Sprintf("/forms/%d/tests/%d/archive", formID, testID), url.Values{"archived": {"1"}}),
    formID, testID))
assertStatus(t, "ArchiveStep (archive)", rec, http.StatusSeeOther)
```
Assert:
- `rec.Header().Get("Location") == fmt.Sprintf("/forms/%d/def/edit", formID)`.
- DB: `form_row.archived` for testID is now true.

Repeat with `"archived": {"0"}` (or any non-`"1"` value) and assert it flips back to false
(covers the `archived := r.FormValue("archived") == "1"` branch both ways).

## Test 6 — TestIntegration_ArchiveStep_DoesNotAlterLockedRecordSnapshot
**This is the key regression test the issue calls out.** Seed a throwaway part → form →
one form_row (not archived) → one `form_record` row with `is_locked=1`, `test_order =
"<testID>"`, `form_revision = 1` (a frozen historical snapshot).

```go
// capture "before" snapshot values first
var beforeOrder string
var beforeRev sql.NullInt32
h.DB().QueryRowContext(ctx, fmt.Sprintf(
    `SELECT test_order, form_revision FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
).Scan(&beforeOrder, &beforeRev)

rec := httptest.NewRecorder()
h.ArchiveStep(rec, withIDAndTestID(
    postForm(..., url.Values{"archived": {"1"}}), formID, testID))
assertStatus(t, "ArchiveStep", rec, http.StatusSeeOther)

var afterOrder string
var afterRev sql.NullInt32
h.DB().QueryRowContext(ctx, ...).Scan(&afterOrder, &afterRev)
```
Assert `afterOrder == beforeOrder` and `afterRev == beforeRev` (byte-for-byte identical) —
archiving a step must touch only `form_row.archived`, never the locked record's frozen
`test_order`/`form_revision` columns. Also assert `form_row.archived` for testID did in
fact flip to true (sanity check the action actually ran).

## Test 7 — TestIntegration_SaveFormDef_DoesNotAlterLockedRecordSnapshot
Same setup shape as Test 6 (throwaway part/form/form_row/locked form_record), but exercise
`SaveFormDef` instead: POST a real field change for the step (e.g. new `parameter` value,
`original_parameter_<testID>` = old value) with no `step_order` change.

Assert:
- Step's `parameter` updated in DB (sanity the save happened).
- The locked form_record's `test_order` and `form_revision` are unchanged from their
  seeded values (same before/after comparison pattern as Test 6).

Cleanup for both Test 6 and Test 7: delete form_row_history rows for testID, form_record,
form_row, form, part (FK order: form_record → form_row_history → form_row → form → part).

## Style notes for the implementer
- Match existing file conventions: `t.Fatalf` for seed/setup failures, `t.Errorf` for
  assertion failures, `defer` cleanup registered immediately after each successful INSERT
  (see `TestIntegration_YieldSummary` for the pattern), unique throwaway identifiers via
  `"ITEST-<label>-" + strconv.FormatInt(time.Now().UnixNano(), 10)` for part numbers.
- Use `h.cfg.<X>Table()` helpers everywhere; never hardcode `form_row`/`form_record`/etc.
- No new test file, no non-integration unit tests — this issue's handlers have no
  DB-independent logic to unit test in isolation (unlike `diffSnapshot`, which already has
  unit coverage in `records_history_test.go`).

## Resolved decisions
1. **Test 2's day-boundary risk:** accepted as-is, matching this file's existing tolerance
   for minor date-boundary flake risk. No clock-skew-proofing added.
2. **Test 7 scope:** stays limited to `test_order`/`form_revision` on `form_record`, exactly
   as the issue text calls out. No added `result`-row assertion — keeps the change surgical
   to what #813 asks for.
