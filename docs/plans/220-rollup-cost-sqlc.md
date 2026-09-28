# #220 — BOM cost rollup and build cost to sqlc

Part of #220 / #190. Fifth slice of the `arx_go/parts.go` catalog: the BOM cost rollup and the
qty-break cost to build N.

## Scope
- `rollupCost`, `PartRollupCost` (tx write-back), `aggregateLeafQty`, `buildCost`'s batched part-info and
  price-tier lookups (`fetchPartInfoByID`, `fetchPriceTiersByPart`).
- Removed: `hasOwnBOMExpr`, `sqlInClause`, `buildCostPartInfo`, `fetchPartInfoByID`, `fetchPriceTiersByPart`.
- Out of scope, still raw SQL: orders/records cards, price history, PartDetail's attachment/price
  queries, `ensureDefaultSupplier`. `parts.go` does not join `convertedFiles`.

## Changes
- `internal/parts/parts.sql` (+ `sqlc generate`):
  - `ListBuildCostParts`, `ListActivePriceTiers` (`= ANY(string_to_array(sqlc.arg(ids)::text, ',')::int[])`),
    `SetPartRollup` (`last_rollup_cost`, `last_rollup_at = CURRENT_TIMESTAMP`).
  - IN-lists take a comma-joined text param (service helper `idList`): sqlc's database/sql output wraps an
    `int[]` param in lib/pq's `pq.Array`, and adding lib/pq just for that was rejected (user's call).
  - No new BOM-walk query: both walkers reuse `ListBOMLines`, which already returns the same
    component/qty/current_cost/preferred_price/has_bom columns over the same `bom JOIN part` (FK-enforced,
    so the join drops no rows for `aggregateLeafQty`). NULL current_cost/preferred_price already COALESCE
    to 0, which is what the old `sql.NullFloat64` scans produced.
- `internal/parts/parts.go`: `BuildCostPart`, `PriceTier` and the three service methods; package doc.
- `arx_go/parts.go`: walkers call `h.parts().ListBOMLines`; `PartRollupCost` writes via `parts.New(tx)`;
  `buildCost` calls the two batched methods directly and keys tiers by `partSupplierKey` as before.

## Decision — NULL price_ea / pack_size tier rows
The old tier scan into `float64` failed the whole build cost on a NULL `price_ea`/`pack_size` (both
columns are nullable with defaults). The service skips such rows instead — the same thing rollup's
`MIN(price_ea)` does with a NULL price. Not reachable through the UI (the pricing forms fill both).

## Test plan
1. Coverage audit (`integration_test.go`): seed-BOM build cost at three qtys (consolidation, tier shift,
   below all tiers), cycle detection for both walkers, non-assembly part, build-cost handler (+ bad qty),
   build cost is read-only, nested rollup, memo reuse, rollup handler write-back + cycle rollback.
   `parts_test.go`: `bomLeafCost`, `pickTier` (unit).
   Gaps: inactive and non-default-supplier prices, NULL leaf columns, a sub-assembly's stale stored
   rollup, leaves left untouched by the write-back.
2. Characterization (`arx_go/rollup_cost_integration_test.go`, `seedBOM` fixture), passing on unchanged code:
   `RollupCost_Fixture` (rollupCost values for P and S), `BuildCost_Fixture` (full `Lines` equality incl.
   missing/NULL lines and total), `PartRollupCost_Fixture` (writes P and S with one timestamp, ignores
   S's stored 3.5, leaves the leaves NULL).
3. Manual-only: BOM tab "Roll up cost" button, build-cost page.
