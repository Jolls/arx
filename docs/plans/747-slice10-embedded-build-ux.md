# Slice 10 (#747) — Embedded Build-at-Test-Time UX

**Status:** PLAN ONLY. Optional, highest-risk, last-landing slice of epic #736. Re-scopes #716.
Pure code, no migration. Depends on slice 8 (#745, create/render logic) and interacts with
slice 9 (#746, traceability view). The user may choose NOT to implement this slice after
reviewing this plan.

## Resolved decisions (user, this session)

- **OQ1 → Single-unit, qty=1 (option C).** Inline panel builds exactly one unit: consume BOM×1, create one unit as output, receive, all in the record-save transaction. Batch builds stay on the unchanged standalone Build tab. Batch-at-test-time is out of scope for v0.7.
- **OQ2 → seam confirmed satisfiable.** Slice 8 (#745) adds `upsertUnitForRecord(ctx, tx, partID, serial, buildID, lotID)` — a tx-accepting helper enforcing `CK_unit_provenance` + per-part serial uniqueness and setting `form_record.unit_id`. Slice 10 calls it inside the record-save tx, passing the same-tx `buildID`. If the landed slice-8 helper differs, refactor it into this seam as task 1.
- **OQ3 → panel already re-enabled by slice 8** (per #745 Q1 decision). Slice 10 only swaps the "Build this unit" button behavior from navigate-away to inline-panel-expand; no separate un-hide needed.
- **OQ4 → implement (all three slices included this batch).**

## 0. Original open questions (superseded by the decisions above)

**OQ1 — Consumption vs. receipt timing (the carried-forward main open item).** Per Q8 a `form_record` covers at most one unit, so the inline test-time build is inherently **single-unit** (the record *is* the one unit under test). Options:
- (A) consume components at panel-save but defer output lot/unit + receipt until pass/fail is known (build row exists, output provisional);
- (B) consume AND receive both immediately, hand-correct failures (what `PartBuildCreate` does today — issue calls this "the wrong shape");
- (C) constrain the inline panel to qty=1: consume per-assembly BOM × 1 and create exactly one `unit` (slice-8 lazy creation) as the output, in the same save that records results.
- **Recommendation: C.** The batch "N passed of M built" reconciliation that makes (B) wrong never arises for single-unit. A failing unit keeps its row (`is_active=0` for scrap) and its legitimately-consumed components. Batch builds stay on the unchanged standalone Build tab (workflow 1).
- **Flag:** if the user wants qty>1 batch builds *at test time*, option (A)'s deferred-receipt design is required and this becomes a much larger slice — recommend ruling batch-at-test-time out of scope for v0.7.

**OQ2 — How the build transaction merges with slice 8's (#745) unit-creation-at-test-time logic.** #747 needs slice 8 to expose a tx-accepting helper — roughly `createUnit(ctx, tx *txLogger, partID int, lotID, buildID *int, serial string) (int, error)` — that (a) accepts a caller-started tx, (b) enforces Q5's `CHECK (lot_id IS NOT NULL OR build_id IS NOT NULL)` + per-part serial uniqueness, (c) links `form_record.unit_id`.
- **Recommendation:** hard interface dependency — confirm the slice-8 helper (`upsertUnitForRecord` per the #745 plan) exists and threads a same-tx `build_id`; if not, #747's first task is to refactor slice-8's inline unit-creation into that seam.

**OQ3 — Re-enabling the existing Lot/Build dropdowns.** The entire `Trace` block on `record_edit.html` (lines 62–100) is currently **commented out** (#677/#687, deferred to v0.7.0 #702).
- **Recommendation:** confirm whether slice 8 (open q1 of #745) already re-enables it. If slice 8 already un-hid it, #747 only swaps the button behavior; otherwise #747 re-enables the whole trace block.

**OQ4 — Whether to implement this slice at all (issue says optional).** Given OQ1/OQ2 plus "highest behavioral risk," do not start until OQ1/OQ2 are decided; the user may defer/drop this slice.

Everything below is fully specified CONTINGENT on OQ1=C (single-unit), OQ2=helper exists, OQ3=#747 owns re-enabling the trace block.

## 1. Current state (verified against tree)
- `record_edit.html`: Lot/Build dropdowns + "Build this unit" button + `Trace.Buildable` conditional exist only inside a `{{/* ... */}}` comment (62–100), disabled since 0.6.0. Button, when live, is `<a href="/part/{id}/build?return_record={id}">` that navigates away.
- `build.go` `PartBuildCreate` (POST /part/{id}/build) is the transactional core, already one tx via `h.beginTx`: insert build → (if lot-tracked output) createLot + link output_lot_id → per-component `recordInventoryTxn("issue", -consumed, …, lotID, &buildID)` → `recordGenealogy(parentLot, outputLot, consumed)` (lot→lot only) → `recordInventoryTxn("receipt", qty, …)` → on `return_record`, UPDATE record's lot_id/build_id → Commit → redirect `/records/{id}/edit?built=1`.
- `records.go` `SaveResults` (POST /records/{id}/edit) is **NOT transactional**: per-result UPDATE/INSERT via `h.execContext` and a final record UPDATE, each auto-committed. Linkage read by `recordLinkageArgs`, written in the final `UPDATE … SET …,lot_id,build_id`.
- `loadRecordTrace`/`RecordTrace` (~1619–1703) already computes `Buildable` (bomCount>0), `IsLotTracked`, `Lots`, `Builds`, linked lot/build. `EditRecord` GET passes `Trace`.
- `loadBuildComponents` (build.go ~103) already produces BOM-line + per-lot-picker structure (`buildComponent{PartID,PartNumber,Title,Category,QtyPer,StockOnHand,IsLotTracked,Lots}`).
- tx-accepting helpers: `createLot`, `recordGenealogy`, `recordInventoryTxn`, `lotBelongsToPart`, `activeLotsForPart`.

## 2. Design (contingent on OQ1=C)
Inline panel is a single-unit build. On record-edit save, when panel expanded with component lot picks, the ONE `SaveResults` transaction also: (a) inserts a `build` row (qty=1); (b) issues each stocked BOM component (per-assembly qty × 1) via `recordInventoryTxn`, recording picked lot; (c) creates output `unit` via slice-8 helper (`build_id`=new build, `lot_id`=output lot if lot-tracked, `serial_number`=record's serial); (d) if output part lot-tracked, output lot + receipt txn + genealogy edge per consumed lot; (e) genealogy unit-endpoint edges for consumed serialized child units; (f) sets `form_record.unit_id` (+ build_id/lot_id per Q8 invariant) — all before the result loop's commit.

Batch builds stay on the unchanged standalone Build tab. Both converge on the same end state.

## 3. Transaction restructuring (records.go SaveResults)
1. Wrap body in `tx, err := h.beginTx(...); defer tx.Rollback()`; replace `h.execContext`/`h.queryRowContext` in handler with `tx.*`. End `tx.Commit()`. (Non-build path: just atomicity.)
2. After result loop, BEFORE final record UPDATE: guarded `if fv(r,"build_panel")=="1" { … }` — entered only when panel submitted; plain saves unaffected.
3. Inside: port PartBuildCreate core (via shared helper §4) qty=1: validate Buildable + picked lots, insert build, issue components, create output lot+unit, write genealogy, set record's unit_id/build_id/lot_id in the final UPDATE.
4. Preserve locked-record/is_locked guards.

## 4. Shared build helper (build.go)
Extract PartBuildCreate's transactional body (~297–427, excluding outer tx begin/commit and return_record block) into:
```
func (h *Handler) performBuild(r *http.Request, tx *txLogger, partID int, outputLotTracked bool,
    qty float64, buildDate time.Time, note string, lines []bomLine, lotPicks map[int]int)
    (buildID int, outputLotID int, err error)
```
`PartBuildCreate` calls it inside its existing tx (identical behavior); `SaveResults` build block calls it qty=1. Promote `bomLine` (currently local) to package-level. Single required abstraction, no duplication.

## 5. Unit creation seam (depends on #745 — OQ2)
Build block calls slice-8 helper to create output unit in the same tx, passing `buildID` and output `lotID` (nil if not lot-tracked). If #745 didn't expose a tx-accepting seam, refactor first. Helper enforces Q5 CHECK + per-part serial uniqueness, sets `form_record.unit_id`.

## 6. Template changes (record_edit.html)
1. Un-comment/restore `Trace` block (OQ3), dropdowns unchanged.
2. Replace "Build this unit" `<a href=…/build?return_record=…>` with `<button type="button" data-toggle-build-panel>` expanding inline `<div id="build-panel" class="d-none">` inside `#edit-form`.
3. Panel renders `Trace.Components` (new field, via `loadBuildComponents`) as a table mirroring part_build.html: component, qty/ea, on-hand, `lot[{PartID}]` `<select>` per lot-tracked component. Hidden `<input name="build_panel" value="1">` enabled only when panel open.
4. No qty field (single-unit).

## 7. JS (inline vanilla — matches codebase)
IIFE wiring `[data-toggle-build-panel]` click → toggle `#build-panel` `d-none` + enable/disable hidden `build_panel` input & `lot[…]` selects (disabled inputs don't POST). Remove `[data-build-this-unit]` saveDraft-on-navigate hook and `?built=1` auto-restore branch (~270–309) — dead with an inline panel. Leave general #259 autosave intact.

## 8. Handler/render wiring
- `RecordTrace` gains `Components []buildComponent`; `loadRecordTrace(..., editable=true)` populates via `h.loadBuildComponents(ctx, record.PartNumberID)` when `t.Buildable`.
- `EditRecord` already passes `Trace`; no route changes. `/part/{id}/build` GET/POST stay for workflow 1; keep `return_record` linkage code (avoid touching workflow 1).

## 9. Testing
- `go build`/`vet`/`test ./...`.
- Extend `integration_test.go`: POST /records/{id}/edit with `build_panel=1` + lot picks → asserts one build row, one unit (build_id/serial), component issue txns, output receipt (if lot-tracked), genealogy edges, form_record.unit_id/build_id set (committed); plus failure-injection asserting rollback writes nothing.
- Verify plain save (no `build_panel`) is behaviorally identical to today.

## 10. Risk register
- Highest behavioral risk; lands alone. Making SaveResults transactional changes failure semantics of every record save — validate no test depends on partial-write behavior. Slice-8 seam (OQ2) is critical path. Keep surgical: one `performBuild` helper, one promoted `bomLine`, one new RecordTrace field, template + JS, tx wrapping.
