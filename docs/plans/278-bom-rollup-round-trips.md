# #278 BOM rollup and build cost: cut database round trips

Goal: rollup = 1 tree read + 1 batched write; build cost = 1 tree read (+ existing 2 Pass-2 reads). No schema change, no migration. Rolled-up costs, leaf quantities, build-cost totals, cycle reporting unchanged.

## Coordination with #264 (applied first, same branch)
Shared files (both plans edit them; add, don't rewrite):
- `internal/parts/parts.sql`, `internal/parts/parts.go`, regenerated `internal/dbq/parts.sql.go` (run `sqlc generate` AFTER #264's changes are present).
- `arx_go/parts.go`: #264 touches `PartBOMEdit` / `BOMPastePreview` (the `ListBOMLines` call near line 933) and other call sites; #278 touches only `rollupCost`, `PartRollupCost`, `aggregateLeafQty` (lines ~995-1161). Do not touch #264's call sites.
- `ListBOMLines` stays (still used by `fetchBOMItems` at parts.go:624 and, until #264 lands, the paste preview). Do not remove or alter it. #278 does not use #264's `ListBOMEdges`.
- `CHANGELOG.md`: one entry per PR. If #264 already added a `## [0.8.x]` entry on this branch, add #278's `### Changed` bullet to that entry; otherwise add `## [0.8.44] - <date>` (top is currently 0.8.43).
- Test files: #278 adds a NEW file `arx_go/bom_rollup_roundtrip_integration_test.go`, so no merge overlap with #264's tests. New unit tests go in `arx_go/parts_test.go` (append only).

## Current code (verified)
- `rollupCost(ctx, pnid, visited, memo)` at `arx_go/parts.go:998`: `ListBOMLines` per distinct assembly (memo), path-scoped `visited` for cycles, leaf cost via `bomLeafCost(false, 0, PreferredPrice, CurrentCost, "")`, assembly cost via recursion (stored `last_rollup_cost` is NOT read). `memo` ends up holding the root + every assembly (`has_bom`) reached; root with no lines gets `{cost: 0}`.
- `PartRollupCost` at `arx_go/parts.go:1039`: calls `rollupCost`, errors on cycle, then in one tx loops `svc.SetPartRollup` over `memo` (`parts.New(tx)`, `tx` is `*txLogger`).
- `aggregateLeafQty(ctx, pnid, parentQty, visited, leaves)` at `arx_go/parts.go:1127`: one `ListBOMLines` per occurrence, no memo; breaks out of the sibling loop on a cycle. Only caller: `buildCost` (parts.go:1177). Pass 2 (`BuildCostParts`, `ActivePriceTiers`) already batched; unchanged.
- Numeric handling: sqlc.yaml overrides map `numeric` to `float64` (nullable to `*float64`); `qty` is `NUMERIC(15,5)`, `last_rollup_cost` is `NUMERIC(16,8)`. `ListBOMLines` already returns `preferred_price` as `::numeric` -> `float64` and `COALESCE(current_cost, 0)`. Keep exactly these expressions in the new query so values are identical.
- Array params: generated code for `int[]` would go through lib/pq `pq.Array` (see comment above `ListBuildCostParts`), so array params are passed as comma-joined text and cast in SQL (`string_to_array(sqlc.arg(x)::text, ',')::int[]`), via `idList` in `internal/parts/parts.go:690`. Use the same pattern.
- Round trips are countable: `recordRoundTrip` (arx_go/handlers.go:171) increments `sqlStats.count` for every `queryContext/queryRowContext/execContext` and every `txLogger` Exec/Query when a `*sqlStats` is in ctx under `ctxSQLStatsKey` (pattern used at integration_test.go:1270). `BeginTx`/`Commit` are not counted.

## File changes

### 1. `internal/parts/parts.sql`
Add (near `ListBOMLines`, line ~216):
- `-- name: ListBOMTree :many` with `sqlc.arg(root_id)::int`. Reachable-set CTE, then the edges of those parents:
  - `WITH RECURSIVE tree(part_id) AS (SELECT sqlc.arg(root_id)::int UNION SELECT b.component_part_id FROM bom b JOIN tree t ON b.parent_part_id = t.part_id)` -- `UNION` (not `UNION ALL`) de-duplicates, so each part is expanded once and recursion terminates on a cyclic BOM without a path array or `CYCLE` (works on any Postgres version; each assembly is expanded once regardless of how often it repeats).
  - Select: `pl.parent_part_id, pl.component_part_id, pl.qty, COALESCE(pn.current_cost, 0) AS current_cost`, the exact `preferred_price` subquery expression from `ListBOMLines` (`COALESCE((SELECT MIN(p.price_ea) FROM price p WHERE p.part_id = pn.id AND p.is_active = TRUE AND p.supplier_id = pn.default_supplier_id), 0)::numeric`), and `EXISTS(SELECT 1 FROM bom c WHERE c.parent_part_id = pn.id) AS has_bom`. `FROM bom pl JOIN part pn ON pl.component_part_id = pn.id WHERE pl.parent_part_id IN (SELECT part_id FROM tree) ORDER BY pl.parent_part_id, pl.line_number, pl.id`.
  - Columns omitted vs the issue's list: `category` (rollup passes `""` to `bomLeafCost` and build cost does not use it), `last_rollup_cost` (rollup recomputes), part-number/description (not used by either walk).
- Replace `SetPartRollup` (line 296) with `-- name: SetPartRollups :exec`: `UPDATE part SET last_rollup_cost = u.cost, last_rollup_at = CURRENT_TIMESTAMP FROM unnest(string_to_array(sqlc.arg(ids)::text, ',')::int[], string_to_array(sqlc.arg(costs)::text, ',')::numeric[]) AS u(id, cost) WHERE part.id = u.id;` (single statement => every assembly gets the same `CURRENT_TIMESTAMP`, as today). `SetPartRollup` is orphaned by this change (only caller is parts.go:1074), so remove it.
- No `sqlc.yaml` change (file already listed). Run `sqlc generate` from repo root; commit `internal/dbq`.

### 2. `internal/parts/parts.go`
- Add type `BOMTreeEdge{ParentID, ComponentID int; Qty, CurrentCost, PreferredPrice float64; HasBOM bool}` (next to `BOMLine`).
- Add `func (s *Service) BOMTree(ctx, rootID int) (map[int][]BOMTreeEdge, error)`: one `s.q.ListBOMTree`, grouped by `ParentID`, preserving row order (parent, line_number, id).
- Replace `SetPartRollup` (line 685) with `SetPartRollups(ctx, costs map[int]float64) error`: build `ids` via `idList` and `costs` via `strconv.FormatFloat(v, 'f', -1, 64)` joined with commas (same key order for both), call `s.q.SetPartRollups`. No-op (return nil, no query) when `costs` is empty.

### 3. `arx_go/parts.go` (only the functions below)
- Split `rollupCost`: keep `func (h *Handler) rollupCost(ctx, pnid, visited, memo) (rollupResult, error)` with the SAME signature (existing tests and `PartRollupCost` keep calling it). New body: `tree, err := h.parts().BOMTree(ctx, pnid)`, then `return rollupWalk(tree, pnid, visited, memo), nil`.
- New pure `rollupWalk(tree map[int][]parts.BOMTreeEdge, pnid int, visited map[int]bool, memo map[int]rollupResult) rollupResult`: the existing recursion verbatim (visited check -> memo check -> visited set/defer delete -> loop edges: `HasBOM` recurses, else `bomLeafCost(false, 0, e.PreferredPrice, e.CurrentCost, "")`; `total += unitCost * e.Qty`; memo store), with no ctx/error. A part with no entry in `tree` yields cost 0 and is memoized (matches today's empty-BOM root).
- `PartRollupCost`: replace the `for partID, result := range memo { svc.SetPartRollup }` loop with: build `costs := map[int]float64` from `memo`, one `svc.SetPartRollups(r.Context(), costs)` inside the existing tx; keep the tx, error message `"Error saving rollup cost: "+err.Error()`, commit, and redirect as is.
- Split `aggregateLeafQty`: keep `func (h *Handler) aggregateLeafQty(ctx, pnid, parentQty, visited, leaves) (bool, error)` signature; body loads `h.parts().BOMTree(ctx, pnid)` once and returns `leafQtyWalk(tree, pnid, parentQty, visited, leaves), nil`.
- New pure `leafQtyWalk(tree, pnid, parentQty, visited, leaves) bool`: existing recursion verbatim (cycle returns true; `extQty := e.Qty * parentQty`; `HasBOM` recurses and `break`s on cycle; else `leaves[e.ComponentID] += extQty`). Repeated sub-assemblies are re-walked in memory (no memo; keeps current semantics), no DB I/O.
- `buildCost` and Pass 2: unchanged.
- Update the comment on `bomLeafCost` / doc comments of the two walkers only where they now describe a per-assembly `ListBOMLines`.

### 4. Not changed
`internal/parts/parts.sql` `ListBOMLines`, `fetchBOMItems`, BOM view/edit/children, any schema/migration/seed, `SQL/SCHEMA.md`, `ROADMAP.md` (issue not listed there; verify with grep at implementation, remove the item if it is).

### 5. `CHANGELOG.md`
`### Changed`: "BOM cost rollup and build cost load the BOM tree in one query, and rollup saves all assembly costs in one statement (fewer database round trips) ([#278](https://github.com/Jolls/arx/issues/278))". No RELEASE_NOTES entry (not user-facing).

## Test plan

### (1) Coverage audit (existing, all build-tag `integration`, live ArxDev)
- `arx_go/rollup_cost_integration_test.go`: `TestIntegration_RollupCost_Fixture` (leaf pricing, fresh sub-assembly rollup, memo = {P,S}), `TestIntegration_BuildCost_Fixture` (consolidated leaf qty, tier pick), `TestIntegration_PartRollupCost_Fixture` (written costs, shared timestamp, leaves untouched).
- `arx_go/integration_test.go`: `TestIntegration_BuildCostConsolidation` (qty 300, seed 3005), `TestIntegration_BuildCostBelowAllTiers`, `TestIntegration_BuildCostCycleDetection`, `TestIntegration_PartBuildCostHandler`, `TestIntegration_BuildCostTierShiftsWithQty`, `TestIntegration_BuildCostDoesNotWriteRollup`, `TestIntegration_RollupCostNested` (3012 = 9.19, 3005 = 27.23), `TestIntegration_RollupCostMemoization` (poisoned `memo[3012]`; still valid because `rollupWalk` keeps the memo check), `TestIntegration_RollupCostCycleDetection`, `TestIntegration_PartRollupCostHandler`, `TestIntegration_PartRollupCostHandlerCycleDoesNotWrite`.
- Unit: `arx_go/parts_test.go` covers `bomLeafCost` (the table test) and `pickTier`; no unit coverage of the walkers or write-back exists.
- Gaps (covered below): sub-assembly repeated in several places, cycle not at the root, 3-node and self-loop cycles, empty-BOM root, assembly-count independence, round-trip counts.

### (2) Characterization tests (must pass on UNCHANGED code; integration; written and run before step 1-3)
New file `arx_go/bom_rollup_roundtrip_integration_test.go` (`//go:build integration`, package main). Throwaway rows only (no seed rows, no reseed). Helper `seedRepeatedSubBOM(t, h)` using `smokeUniq`/`smokeExec`/`h.queryRowContext` inserts in the style of `seedBOM`, with cleanup deleting `bom`, `price`, then `part` rows:
- Parts (all `category` `ASM` for T/S/M so the BOM tab is visible; L is `BUY`, `current_cost` 0.4, `default_supplier_id` 1001, one active `price` row supplier 1001, `price_ea` 2.0, `pack_size` 1): T, M, S, L.
- Lines: T->S line 1 qty 1; T->M line 2 qty 2; T->S line 3 qty 5; M->S line 1 qty 3; S->L line 1 qty 4. (S occurs in three places.)
Tests:
- `TestIntegration_RollupCost_RepeatedSubAssembly`: `h.rollupCost(T)` cost 96 (S = 8, M = 24, T = 8 + 48 + 40), cycle false, `memo` keys exactly {T, M, S}.
- `TestIntegration_BuildCost_RepeatedSubAssembly`: `h.buildCost(T, 1)` -> one line for L, `QtyNeeded` 48 (12 S x 4), `PackSize` 1, `UnitPrice` 2.0, `ExtCost` 96, `Total` 96; and `buildCost(T, 10)` -> `QtyNeeded` 480, `Total` 960 (price still tier 1 as the only tier).
- `TestIntegration_PartRollupCost_RepeatedSubAssembly`: POST handler writes `last_rollup_cost` T 96, M 24, S 8 with one shared `last_rollup_at`; L untouched (NULL).
- `TestIntegration_RollupCost_EmptyBOMRoot`: part with no lines -> cost 0, cycle false, `memo` = {root: 0}; handler writes 0 (not NULL) to it with `last_rollup_at` set.
- `TestIntegration_Cycles_NonRootAndLongerShapes`: for each of (a) root R->A, A<->B (cycle not through root), (b) 3-node A->B->C->A, (c) self-loop A->A: `rollupCost` `cycle == true`, `buildCost` `Cycle == true`, `PartRollupCost` handler writes nothing (all `last_rollup_cost` NULL). Each case also must terminate (test would hang/timeout on regression; add a 30 s `context.WithTimeout` on ctx).
Existing seed-based tests above already pin 3005/3012 numbers; no need to duplicate.

### (3) Red tests (fail today, pass after; integration; same new file; ctx carries `&sqlStats{}` via `context.WithValue(ctx, ctxSQLStatsKey, st)`)
- `TestIntegration_RollupCost_OneRead`: `h.rollupCost(ctxWithStats, T, ...)` on the repeated-sub fixture; assert `st.count == 1`. Fails today: one `ListBOMLines` per distinct assembly (T, M, S = 3).
- `TestIntegration_BuildCost_ReadsIndependentOfRepeats`: `h.buildCost` on the repeated-sub fixture; assert `st.count == 3` (tree + `BuildCostParts` + `ActivePriceTiers`). Fails today: `aggregateLeafQty` issues one `ListBOMLines` per occurrence (T, S, M, S, S, S ... > 1) + 2 = more than 3.
- `TestIntegration_BuildCost_SeedTreeReads`: `h.buildCost(3005, 300)` on seed data; assert `st.count == 3`. Fails today (3005 + 3012 per occurrence + 2).
- `TestIntegration_PartRollupCost_RoundTripsIndependentOfAssemblyCount`: helper `seedWideBOM(t, h, k)` (root T with k sub-assemblies S1..Sk each with one priced leaf line, ASM categories); run the `PartRollupCost` handler with a `sqlStats` ctx for k=1 and k=4; assert equal `st.count` for both and that all k+1 assemblies have `last_rollup_cost` written with one shared timestamp. Fails today: count grows by 2 per extra assembly (one read and one UPDATE each).
- (Unit, no DB, written with the change, pass only after) in `arx_go/parts_test.go`, on hand-built `map[int][]parts.BOMTreeEdge`: `TestRollupWalk_RepeatedSubAssembly` (costs/memo as above), `TestRollupWalk_Cycle` (2-node, 3-node, self-loop, cycle below root -> `cycle` true), `TestRollupWalk_MemoReused` (pre-seeded memo value used), `TestRollupWalk_EmptyRoot` (cost 0, memoized), `TestLeafQtyWalk_RepeatedSubAssembly` (leaves 48 for qty 1), `TestLeafQtyWalk_Cycle`. They do not compile before the change, so they are not characterization.
- The text-encoded numeric write path is covered by the cost assertions (1e-4 via `assertFloatEqual`) in `TestIntegration_PartRollupCost_RoundTripsIndependentOfAssemblyCount` and the existing `TestIntegration_PartRollupCost_Fixture`.

### (4) Manual-only
- Compare a rollup of a real large assembly in the UI (Rollup button on a BOM tab with nested sub-assemblies, against a dev DB) before/after: BOM tab total and each sub-assembly's "Rollup" badge unchanged; `DEBUG_MODE=true` shows one tree query + one UPDATE.
- Open `/part/{id}/build-cost?qty=N` for a nested assembly; with `DEBUG_MODE=true`, confirm the BOM tree query appears once (round-trip counts are otherwise only asserted in integration tests).
- Pre-commit: `sqlc diff` clean, `go build/vet/test ./...` pass, then `ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/...` (ArxDev only; no migration involved).

Which tests need what: all of (2) and (3) except the pure-walker unit tests need the live ArxDev integration suite. Unit tests (`go test ./...`) cover only the pure walkers. No new seed rows; no reseed required (fixtures are created and deleted by the tests). If the existing seed tests fail on stale data, tell the user to reseed (do not reseed).

## Order of work
1. Write (2) and (3) integration tests + `seedRepeatedSubBOM`/`seedWideBOM`; run (2) on unchanged code: all pass; run (3): the four red tests fail.
2. Edit `parts.sql` (add `ListBOMTree`, replace `SetPartRollup`), `sqlc generate`.
3. Edit `internal/parts/parts.go`, then `arx_go/parts.go`; add the pure-walker unit tests.
4. `go build ./... && go vet ./... && go test ./...`, then the integration run: (1), (2), (3) all green.
5. CHANGELOG entry.

## Open questions
1. De-duplicating recursion: the plan uses `UNION` over reachable part ids instead of the issue's "path array or `CYCLE`" guard (terminates on cycles, each assembly expanded once, no Postgres 14 requirement). The cycle itself is still detected in Go from the returned edges. Confirm this is acceptable.
2. The tree query omits `category` (listed in the issue's column set) because neither walk uses it. Confirm, or say to include it.
3. Remove `SetPartRollup` (orphaned after the batch write-back) or keep it? Plan removes it per the orphaned-code rule.

## Resolved decisions (user, 2026-10-01)
- Cycle guard: `WITH RECURSIVE ... UNION` de-dup; cycle detection stays in Go. No PG14 `CYCLE` clause.
- Omit `category` from the tree query.
- Remove `SetPartRollup` query and service method (no callers after batching).
