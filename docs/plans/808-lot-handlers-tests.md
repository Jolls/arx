# 808 — Lot handlers & manual stock adjustment test plan

Issue: #808 (sev: high, area: test), part of #801 test-coverage epic.

## Scope

Zero-coverage code to cover:
- `arx_go/lot.go`: `PartLots`, `PartLotTrace`, `LotEdit`, `LotUpdate`, `AllLots`, and helpers `lotsForPart`, `fetchLotRow`, `lotBelongsToPart`, `activeLotsForPart`, `scanLotRow`, `lotRowSelect`.
- `arx_go/inventory.go`: `PartStockAdjust`, specifically its use of `lotBelongsToPart`.

## Approach

Follow the existing pattern in `arx_go/integration_test.go` exactly — this codebase has no DB-mock library (no sqlmock in go.mod), so every handler/helper that touches the DB is covered by a `//go:build integration` test against ArxDev, using the handlers directly (no HTTP server), `httptest.NewRecorder`/`httptest.NewRequest`, and the existing `withID`/`postForm`/`assert302`/`assertStatus` helpers already in that file. Do not add sqlmock or any new test dependency.

**File to edit**: `arx_go/integration_test.go` only. Add one new helper and one new test function block (can be split into multiple `Test*` functions as listed below, but no new file).

## New helper needed

Add alongside `withIDAndAttID` (around line 61-67):

```go
// withIDAndLotID injects chi route params for both "id" and "lotID".
func withIDAndLotID(req *http.Request, id, lotID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("lotID", strconv.Itoa(lotID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
```
(`lotID` is the exact chi URL param name used by the routes: `/part/{id}/lots/{lotID}` and `/part/{id}/lots/{lotID}/edit`, registered in `arx_go/main.go` lines 240-242.)

## Fixture data to use (from `SQL/seed_test_data.sql`, section 10c, lines ~402-462) — do not add new seed rows

- Part 3007 (`RAW-1002`): lot-tracked (`tracking_mode='lot'`), purchased raw. Has two lots:
  - Lot 8301: `lot_number='8301'`, `vendor_lot_number='SS304-LOT-0088'`, `lot_description='PO 5003'`, `is_active=1`, `po_line_id=5504` (a purchased lot).
  - Lot 8303: `lot_number='8303'`, `vendor_lot_number=NULL`, `lot_description='Cycle count - unlabeled found lot'`, `is_active=1`, `po_line_id=NULL`.
- Part 3012 (`ASM-1002`): lot-tracked, manufactured sub-assembly. Lot 8302: `lot_description='Build #8202'`, `po_line_id=NULL`, `is_active=1`.
- Part 3013 (`ASM-1003`): lot-tracked (`tracking_mode='lot_serial'`), top-level assembly. Lot 8306: `lot_description='Build #8203'`, `is_active=1`.
- Part 3005 (`ASM-1001`, Widget Assembly): **not** lot-tracked (`tracking_mode='serial'`) — use this to exercise the "tab not applicable" guard (`tabVisible`/`ShowLots()==false`) on `PartLots`/`PartLotTrace`/`LotEdit`/`LotUpdate`.
- Genealogy: 8301→8302 (edge 8401, qty 1), 8302→8306 (edge 8402, qty 1), 8303→8306 (edge 8403, qty 1). So tracing ancestors of lot 8306 reaches both 8302 (which itself resolves to 8301) and 8303 directly; tracing descendants of lot 8301 reaches 8302 then 8306.
- All four seeded lots (8301, 8302, 8303, 8306) are `is_active=1`. There is no seeded inactive lot — for the "lot exists but is not active" branch of `lotBelongsToPart`, insert and clean up a throwaway inactive lot inline in the test (pattern below), don't rely on seed data.
- Part 3005's inventory: has `ShowInventory()==true` (existing inventory ledger seeded, e.g. transaction 5901 style rows) — `PartStockAdjust` non-lot-tracked-part test cases can use part 3005 directly since the "transactions" tab already applies to it and it needs no lot machinery at all. Confirm 3005 has `ShowInventory()` true before writing (it does — the whole inventory epic seed already treats 3005 as a normal inventoried assembly; if the implementer's local check shows otherwise, use 3012 instead, which is both lot-tracked AND inventoried).

## Test functions to add

### 1. `TestIntegration_LotsForPart` — covers `lotsForPart`, `scanLotRow`, `lotRowSelect`, and `PartLots` handler in one pass

- `h.lotsForPart(ctx, 3007)` — assert returned slice has exactly 2 lots (8301, 8303), both with `PartID==3007`, `PartNumber=="RAW-1002"`. Assert lot 8301's `VendorLot=="SS304-LOT-0088"` and `LotDescription=="PO 5003"`; assert lot 8303's `VendorLot==""` (NULL→empty, proves the `sql.NullString` handling in `scanLotRow`) and `LotDescription=="Cycle count - unlabeled found lot"`.
- Assert ordering: newest first by `created_at` — 8303 (`2026-05-22`) before 8301 (`2026-05-15`)... wait, check actual dates: 8301 is `2026-05-15`, 8303 is `2026-05-22`, so 8303 (newer) must come first. Assert `lots[0].ID == 8303` and `lots[1].ID == 8301`.
- `h.lotsForPart(ctx, 3099999)` (a part id with no lots) — assert empty slice, nil error (not an error case; matches the "Empty (not an error)" doc comment pattern used elsewhere in the file, mirroring `activeLotsForPart`'s doc comment).
- HTTP-level: `h.PartLots(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3007/lots", nil), 3007))` — assert `rec.Code == http.StatusOK` and body contains `"8301"`, `"8303"`, `"RAW-1002"`.
- HTTP-level guard: `h.PartLots(rec, withID(..., 3005))` (non-lot-tracked part) — assert body contains `"does not apply to"` (the `tabVisible` rejection message from `partPageBase`), confirming the Lots tab is correctly gated for non-lot-tracked categories.

### 2. `TestIntegration_FetchLotRow` — covers `fetchLotRow` directly

- `h.fetchLotRow(ctx, 8301)` — assert `found==true`, `err==nil`, `lr.PartID==3007`, `lr.LotNumber=="8301"`, `lr.IsActive==true`.
- `h.fetchLotRow(ctx, 999999999)` (nonexistent id) — assert `found==false`, `err==nil` (per the doc comment: "ok=false (nil error) when the lot does not exist" — this is the `sql.ErrNoRows` branch, make sure the test actually hits it).

### 3. `TestIntegration_LotBelongsToPart` — the shared guard test; the ONE place this is exercised, referenced (not duplicated) by both `PartLotTrace`/`LotEdit`/`LotUpdate` and `PartStockAdjust` scenarios below

Uses a tx from `h.beginTx(ctx)` directly (matching the function's signature, which takes `*txLogger`), committing/rolling back as appropriate — mirror how `PartStockAdjust` itself opens a tx.

- True case: `h.lotBelongsToPart(ctx, tx, 8301, 3007)` → `true, nil`.
- Wrong-part case: `h.lotBelongsToPart(ctx, tx, 8301, 3012)` (lot 8301 belongs to 3007, not 3012) → `false, nil`.
- Nonexistent-lot case: `h.lotBelongsToPart(ctx, tx, 999999999, 3007)` → `false, nil`.
- Inactive-lot case: insert a throwaway lot row directly via SQL inside the test (`INSERT INTO %s (part_id, lot_number, lot_description, is_active) OUTPUT INSERTED.id VALUES (@p1, 'ITEST-INACTIVE', 'itest inactive lot', 0)`, using `h.cfg.LotTable()`), defer-delete it, then assert `h.lotBelongsToPart(ctx, tx, <newID>, 3007)` → `false, nil` (proves the `is_active` condition in the guard's WHERE clause, not just id+part_id).
- Roll back the tx at the end (read-only test, no committed changes except the throwaway lot's own cleanup which should use a separate connection/exec since the guard reads happened inside an uncommitted tx — simplest: do the throwaway insert via `h.DB().ExecContext` directly, outside the tx used for `lotBelongsToPart` calls, so it's visible to that tx's reads and independently cleaned up).

### 4. `TestIntegration_PartLotTrace` — covers `PartLotTrace`, `genealogyTrace`/ancestors+descendants wiring for lots, and the lot-not-found / wrong-part guards

- Ancestors of lot 8306: `h.PartLotTrace(rec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3013/lots/8306", nil), 3013, 8306))` → `rec.Code==200`; body contains `"8302"`, `"8303"`, `"8301"` (all three ancestor lots reachable transitively) and part numbers `"ASM-1002"`, `"RAW-1002"`.
- Guard: lot 8301 requested under the wrong part (`withIDAndLotID(..., 3012, 8301)` — lot 8301 actually belongs to 3007) → body contains `"Lot not found for this part"`.
- Guard: nonexistent lot id (`withIDAndLotID(..., 3007, 999999999)`) → body contains `"Lot not found for this part"`.
- Guard: invalid (non-numeric) `lotID` route param — build the request with `rctx.URLParams.Add("lotID", "abc")` manually (can't use `withIDAndLotID` since it takes an int) → body contains `"Invalid lot id"`.
- Tab-not-applicable guard: `withIDAndLotID(..., 3005, 1)` (non-lot-tracked part) → body contains `"does not apply to"`.

### 5. `TestIntegration_LotEditAndUpdate` — covers `LotEdit` (GET) and `LotUpdate` (POST) together, since Update's happy path is naturally verified by re-fetching after Edit

- `LotEdit` happy path: `h.LotEdit(rec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3007/lots/8301/edit", nil), 3007, 8301))` → `200`, body contains `"8301"`, current `lot_description` (`"PO 5003"`), current vendor lot (`"SS304-LOT-0088"`), and a CSRF token field (assert body contains `"csrf"` or the literal hidden-input name used in `part_lot_edit.html` — check the template for the exact field name before asserting; if unsure, skip asserting the CSRF field specifically and only assert the description/vendor-lot values).
- `LotEdit` guard: wrong part (`withIDAndLotID(..., 3012, 8301)`) → `"Lot not found for this part"`.
- `LotUpdate` happy path: **use a throwaway lot, not 8301/8302/8303/8306** (those are read by `TestIntegration_UpdatedAtSentinel`-adjacent fixtures elsewhere and by other new tests in this file; mutating them risks cross-test interference under `go test`'s default sequential-but-shared-DB execution). Seed a throwaway lot on part 3007 via direct SQL (`INSERT INTO %s (part_id, lot_number, lot_description, is_active) OUTPUT INSERTED.id VALUES (@p1, 'ITEST-LOTUPD', 'orig desc', 1)`), defer-delete it. POST to `LotUpdate` with `lot_description=new desc` and `vendor_lot=NEWVENDOR123`, assert `assert302`, then `SELECT lot_description, vendor_lot_number FROM lot WHERE id=@p1` and assert both were updated.
- `LotUpdate` guard: wrong part — POST against the throwaway lot but with `withIDAndLotID(..., <wrong part id>, <throwaway lot id>)` → body contains `"Lot not found for this part"`, AND re-select the lot afterward to assert `lot_description` is still `"orig desc"` (proves the guard actually blocked the write, not just that it rendered an error page while still updating — mirrors the `TestIntegration_UpdatedAtSentinel` "prove nothing else moved" spirit already established in this file).
- `LotUpdate` invalid lot id: manual route context with `lotID=abc` → body contains `"Invalid lot id"`.
- `LotUpdate` tab-not-applicable: `withIDAndLotID(..., 3005, 1)` → body contains `"does not apply to"`.

### 6. `TestIntegration_AllLots` — covers `AllLots`

- `h.AllLots(rec, httptest.NewRequest(http.MethodGet, "/lots", nil))` → `200`; body contains all four seeded lot numbers `"8301"`, `"8302"`, `"8303"`, `"8306"` and is not scoped to one part (also contains `"RAW-1002"` and `"ASM-1003"` together, proving it's cross-part).

### 7. `TestIntegration_PartStockAdjust` — covers `PartStockAdjust`, reusing the `lotBelongsToPart` guard already proven in test 3 (do not re-derive the guard's true/false logic here — only assert the handler wires it correctly)

Use a **non-lot-tracked part for the simple cases** and **3007 (lot-tracked) for the lot-specific branches**. For every sub-case that commits a real adjustment, capture `part.stock_on_hand` before/after and assert the delta, and clean up the inserted `inventory_transaction` (and any newly-created lot) row(s) in a `defer`.

- Non-lot-tracked happy path: pick a non-lot-tracked, inventoried part (e.g. 3002 or another BOM-leaf part already used elsewhere in this file that has `ShowInventory()==true` and `is_lot_tracked=0` — confirm via `SELECT is_lot_tracked, tracking_mode FROM part WHERE id=3002` if reusing 3002; if it turns out lot-tracked/not-inventoried, substitute any other confirmed-safe part id from the existing fixture, e.g. one of the BOM-leaf raw parts already referenced in `TestIntegration_BuildCostConsolidation`). POST `qty=5`, `reason=itest count correction`, `txn_date=2026-07-01` → `assert302`; assert `stock_on_hand` increased by 5; assert one new `inventory_transaction` row exists with `txn_type='adjustment'`, `qty=5`, `note='itest count correction'`, `lot_id IS NULL`.
- Missing qty / zero qty: POST with `qty=0` → body contains `"Enter a non-zero quantity"`.
- Missing reason: POST with `qty=5`, no `reason` → body contains `"A reason is required"`.
- Lot-tracked, pick existing active lot: POST to part 3007 with `qty=3`, `reason=itest`, `lot_id=8301` → `assert302`; assert new ledger row has `lot_id=8301`; assert `stock_on_hand` increased by 3.
- Lot-tracked, pick a lot that belongs to a DIFFERENT part (reuses the guard from test 3 — e.g. `lot_id=8302`, which belongs to 3012, POSTed against part 3007) → body contains `"Selected lot is not an active lot of this part"`; assert `stock_on_hand` for 3007 is unchanged (no ledger row written) — proves the guard blocks before the DB write, same "before/after unchanged" style as the `LotUpdate` guard test above.
- Lot-tracked, both `lot_id` and `new_lot_number` set → body contains `"Choose an existing lot or enter a new lot number, not both"`.
- Lot-tracked, neither `lot_id` nor `new_lot_number` set → body contains `"select a lot or enter a new lot number"`.
- Lot-tracked, `new_lot_number` creates a brand-new lot inline (exercises `createLot` + the manual-entry lot-creation branch): POST `new_lot_number=ITEST-NEWLOT-<timestamp>`, `qty=2`, `reason=itest new lot` → `assert302`; assert a new `lot` row was created with that `lot_number`, `lot_description='Manual entry'`, `part_id=3007`; assert the new ledger row's `lot_id` matches the new lot's id; clean up both the new lot row and the ledger row.
- Invalid `lot_id` (non-numeric) → body contains `"Invalid lot selection"`.

## Cleanup discipline

Every sub-case that writes (inserts a lot, inserts an inventory_transaction, or mutates `stock_on_hand`/`lot_description`/`vendor_lot_number`) must restore state in a `defer`, following the existing file's pattern (delete inserted rows; for `stock_on_hand`, either delete the ledger row and also reverse the `UPDATE part SET stock_on_hand = stock_on_hand + qty` with an equal-and-opposite adjustment, or re-read and restore the exact prior value — prefer the latter, matching how other tests in this file snapshot/compare before-after rather than assuming a fixed baseline).

## Resolved decisions

1. **Part id for the non-lot-tracked happy path (test 7, first bullet):** use
   **part 3002** (`BUY-1001`, category BUY). Confirmed via `SQL/seed_test_data.sql`:
   `is_lot_tracked`/`tracking_mode` are never set true/`'lot'` for 3002 (only
   3007/3012/3013 are), so it's non-lot-tracked; `ShowInventory()` depends only
   on category (`BUY`/`RAW`/`MFG`/`ASM` are stockable per
   `models.TabsForCategory`), not on pre-existing ledger rows, so 3002 passes
   `requireTab(..., "transactions")` regardless of the fact that only part 3007
   has seeded `inventory_transaction` rows.
2. **CSRF field assertion in `LotEdit`'s test:** skip it — assert only the
   description/vendor-lot values are present in the response body, per the
   plan's own fallback. Not worth reading the template for.
