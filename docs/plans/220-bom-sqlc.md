# #220 — BOM lines to sqlc

Part of #220 / #190. Third slice of the `arx_go/parts.go` catalog: the BOM lines.

## Scope
- `fetchBOMItems` (BOM view + `APIPartBOMChildren`), `PartWhereUsed`, `PartBOMEdit` (lines + rollup
  lookup), `PartBOMSave`, `PartBOMPastePreview`, `copyBOM`, `PartDuplicate`'s BOM count, `BOMExportCSV`.
- Out of scope, still raw SQL: rollup/build cost (`rollupCost`, `aggregateLeafQty`, `PartRollupCost`,
  build-cost helpers, which keep `hasOwnBOMExpr`), pricing, orders/records cards, PartDetail's
  attachment/price queries. `parts.go` does not join `convertedFiles`; no `cfg.*Table()` helper loses its
  last caller.

## Changes
- `internal/parts/parts.sql` (+ `sqlc generate`):
  - `ListBOMLines :many`: one query for view, edit, paste diff and export. Line id/number/qty/component,
    COALESCEd text/costs/counts, `has_bom` EXISTS, and `preferred_price` as `COALESCE(MIN(...), 0)::numeric`
    (0 = none, which `bomLeafCost` already treated like NULL).
  - `ListWhereUsed :many`, `GetPartRollup :one` (cost COALESCEd to 0), `GetPartByNumber :one`,
    `DeleteBOMLine`/`UpdateBOMLine` (guarded by `parent_part_id`), `CreateBOMLine`, `CopyBOM`.
- `internal/parts/parts.go`: `BOMLine` (also the write input), `WhereUsed`, `PartRef`, and the matching
  service methods.
- `arx_go/parts.go`:
  - `bomLeafCost` takes the preferred price as `float64` (0 = none); `rollupCost` passes `.Float64`.
  - `fetchBOMItems` parses the id and maps `ListBOMLines`; it now also sets `BOMItem.ID` (the BOM line
    id; nothing reads it in the view or the children JSON). `PartBOMEdit` and `BOMExportCSV` reuse it.
  - `BOMExportCSV` takes the parent part number from `requireTab`'s part.
  - `PartBOMSave` runs the writes via `parts.New(tx)`; a non-numeric line id now fails at `Atoi` instead of
    in Postgres (same error branch). Part-number lookups stay outside the tx, as before.
  - `PartBOMPastePreview` builds its existing-line map from `ListBOMLines`.
  - `PartDuplicate` uses `GetPart`'s `HasBOM` (EXISTS on `bom`), which is the old `COUNT(*) > 0`.
  - `copyBOM` removed (replaced by `CopyBOM`).

## Test plan
1. Coverage audit: `TestIntegration_BuildCostUIWiring` (renders PartBOMEdit/PartBOM markers),
   `TestIntegration_RouteRoundTrips` "part BOM", `TestBOMLeafCost` (unit). Nothing pinned the rest.
2. Characterization (`arx_go/bom_integration_test.go`), passed on unchanged code:
   `FetchBOMItems_Fields`, `BOMExportCSV`, `PartWhereUsed_Renders`, `PartBOMEdit_Renders`,
   `PartBOMSave`, `PartBOMPastePreview`, `PartDuplicate_CopiesBOM`. `FetchBOMItems_Fields` gained the
   line ids afterwards (the one intended difference).
3. Red: none. Pure refactor.
4. Manual-only: BOM tab expand/collapse (children JSON), BOM editor save, paste import, CSV download,
   duplicate part with a BOM.
