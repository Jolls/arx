# Plan: #814 — test unit.go's PartUnits/PartUnitTrace handlers

## Pattern to follow
This repo has no sqlmock/mock-DB unit-test pattern for handlers. Every HTTP handler
test in this codebase is a build-tagged live-DB integration test in
`arx_go/integration_test.go` (`//go:build integration`), using `liveHandler(t)` to get
a real `*Handler` against ArxDev, `withID`/`withIDAndAttID` to inject chi route params,
`httptest.NewRequest` + `httptest.NewRecorder()`, calling the handler method directly
(no router), then asserting on `rec.Code` and/or `strings.Contains(rec.Body.String(), ...)`.
See `TestIntegration_PartBuildCostHandler` (line 929) and `TestIntegration_BuildCostUIWiring`
(line 1245) for the exact shape to copy — status-code assertions for the success case,
body-substring assertions (via `strings.Contains`) for both success and error-message cases.
`unitsForPart`, `fetchUnitRow`, `scanUnitRow`, `unitRowSelect`, `LotIDVal`/`BuildIDVal` are
all exercised indirectly through the two handlers — no separate unit tests for them.

Do NOT introduce sqlmock or a new testing approach. Append to `arx_go/integration_test.go`.

## File to edit
`arx_go/integration_test.go` — append new tests after the last `TestIntegration_*`
function in the file (find the end of file; do not insert in the middle).

## Helper to add
No existing helper injects both `id` and `unitID` chi route params (only
`withIDAndAttID` exists, for `id`+`attID`). Add, near `withIDAndAttID` (around line 61-67):

```go
// withIDAndUnitID injects chi route params for both "id" and "unitID".
func withIDAndUnitID(req *http.Request, id, unitID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("unitID", strconv.Itoa(unitID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
```

## Seed fixtures to rely on (from SQL/seed_test_data.sql — do not add new seed rows)
- Part 3013 (`ASM-1003`, tracking_mode `lot_serial` → `ShowUnits()` true).
- Part 3005 (`ASM-1001`, tracking_mode `serial` → `ShowUnits()` true).
- Part 3007 (`RAW-1002`, tracking_mode `lot` → `ShowUnits()` false — use for the
  tab-not-applicable case).
- Part 3004 (`MFG-1001`, tracking_mode default/none → `ShowUnits()` false — alternative
  tab-not-applicable case; either 3007 or 3004 works, prefer 3004 since it's unambiguous
  "not tracked at all").
- Unit 8501: part_id 3013, lot_id 8306 (lot_number `'8306'`), build_id 8203, serial
  `SN-3013-001`. Its genealogy (edges 8404/8405) gives it a unit parent (8503) and a lot
  parent (8301) — already covered by `TestIntegration_UnitGenealogyTrace`, so the new
  trace-handler test does NOT need to re-assert ancestor correctness, only that the
  handler wires `genealogyTraceRoots` results into the response without erroring.
- Unit 8502: part_id 3007, lot_id 8301, build_id NULL, serial `SN-3007-A1`.
- Unit 8503: part_id 3005, lot_id NULL, build_id 8201, serial `SN-3005-001`.

All three units are pre-seeded and read-only for these tests — no INSERT/DELETE/cleanup
needed (mirrors `TestIntegration_PartBuildCostHandler`'s "Read-only — no cleanup" comment;
add the same comment to the new tests).

## Test 1: TestIntegration_PartUnitsHandler
Covers `PartUnits` + `unitsForPart` + `scanUnitRow` + `unitRowSelect`.

```go
func TestIntegration_PartUnitsHandler(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// Success: 3013 (lot_serial) lists its seeded unit with part/lot join fields.
	req := withID(httptest.NewRequest(http.MethodGet, "/part/3013/units", nil), 3013)
	rec := httptest.NewRecorder()
	h.PartUnits(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PartUnits(3013): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"SN-3013-001", "ASM-1003", "8306"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartUnits(3013): body missing %q", want)
		}
	}

	// Tab not applicable: 3004 is not serial/lot_serial tracked.
	req2 := withID(httptest.NewRequest(http.MethodGet, "/part/3004/units", nil), 3004)
	rec2 := httptest.NewRecorder()
	h.PartUnits(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "does not apply") {
		t.Errorf("PartUnits(3004): expected tab-not-applicable error, got body: %s", rec2.Body.String())
	}
}
```

Assertions:
- 3013 units page: status 200, body contains serial `SN-3013-001`, part number `ASM-1003`,
  and lot number `8306` (proves the lot LEFT JOIN and template render both work).
- 3004 units page: body contains `"does not apply"` (from `tabVisible` gating in
  `partPageBase`), proving the subtab-gating short-circuit still applies to this handler.

## Test 2: TestIntegration_PartUnitTraceHandler
Covers `PartUnitTrace` + `fetchUnitRow` + genealogy wiring + `LotIDVal`/`BuildIDVal`
(exercised via template render, not asserted directly — there's no direct-call site for
them outside template execution).

```go
func TestIntegration_PartUnitTraceHandler(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// Success: unit 8501 belongs to part 3013, has both lot 8306 and build 8203.
	req := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/8501", nil), 3013, 8501)
	rec := httptest.NewRecorder()
	h.PartUnitTrace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PartUnitTrace(3013,8501): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"SN-3013-001", "8306", "8203"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartUnitTrace(3013,8501): body missing %q", want)
		}
	}

	// Invalid unitID: non-numeric route param.
	badReq := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/abc", nil), 3013, 0)
	// withIDAndUnitID always writes a numeric unitID; overwrite the route param directly
	// to inject a non-numeric value instead of using the helper for this one case:
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "3013")
	rctx.URLParams.Add("unitID", "abc")
	badReq = httptest.NewRequest(http.MethodGet, "/part/3013/units/abc", nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	badRec := httptest.NewRecorder()
	h.PartUnitTrace(badRec, badReq)
	if !strings.Contains(badRec.Body.String(), "Invalid unit id") {
		t.Errorf("PartUnitTrace(3013,abc): expected \"Invalid unit id\", got body: %s", badRec.Body.String())
	}

	// Unit belongs to a different part than the URL's {id}: 8502 is part 3007's unit,
	// requested under part 3013.
	wrongPartReq := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/8502", nil), 3013, 8502)
	wrongPartRec := httptest.NewRecorder()
	h.PartUnitTrace(wrongPartRec, wrongPartReq)
	if !strings.Contains(wrongPartRec.Body.String(), "Unit not found for this part") {
		t.Errorf("PartUnitTrace(3013,8502): expected \"Unit not found for this part\", got body: %s", wrongPartRec.Body.String())
	}

	// Nonexistent unit id.
	missingReq := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/999999", nil), 3013, 999999)
	missingRec := httptest.NewRecorder()
	h.PartUnitTrace(missingRec, missingReq)
	if !strings.Contains(missingRec.Body.String(), "Unit not found for this part") {
		t.Errorf("PartUnitTrace(3013,999999): expected \"Unit not found for this part\", got body: %s", missingRec.Body.String())
	}
}
```

Simplify the "invalid unitID" sub-case: since `withIDAndUnitID` takes an `int`, it can't
carry a non-numeric route param. Build that one request manually with `chi.NewRouteContext`
directly (as shown above) instead of forcing the helper to accept strings — keep the helper
signature `(req *http.Request, id, unitID int)` matching `withIDAndAttID`'s style.

Assertions summary for Test 2:
1. `3013/8501` (own unit, both lot+build set): 200; body contains serial, lot number `8306`,
   and build id `8203` (proves `Build` is populated via `fetchBuildOption` and rendered).
2. `3013/abc` (non-numeric unitID): body contains `"Invalid unit id"`.
3. `3013/8502` (valid unit, wrong part): body contains `"Unit not found for this part"`.
4. `3013/999999` (unit does not exist): body contains `"Unit not found for this part"`.

## Verification
```
go build ./...          # (in arx_go/)
go vet ./...
$env:ARX_TEST_DSN="sqlserver://user:pass@server?database=ArxDev&encrypt=true"
go test -tags integration ./arx_go/... -run 'TestIntegration_PartUnits|TestIntegration_PartUnitTrace'
```
All 2 new test functions must pass against ArxDev with existing seed data (no reseed
needed — all fixture rows already exist).

## Open questions
- None. Existing seed data (units 8501/8502/8503, parts 3004/3005/3007/3013) covers every
  case the issue asks for, and the integration-test pattern to follow is unambiguous
  (it's the only handler-test pattern in the repo).
