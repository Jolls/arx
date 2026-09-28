# #220 — RFQ BOM graph and part-number scan to sqlc

Part of #220 / #190. Moves the part/BOM reads in `arx_go/rfq_bom.go` and `arx_go/partnumber.go` into
`internal/parts`; `partnumber.go` joins `convertedFiles`.

## Scope
- `arx_go/partnumber.go`: `nextBaseNumber`'s `SELECT part_number FROM part`.
- `arx_go/rfq_bom.go`: `loadRFQGraph`'s BOM-children query (`bom` + `part`).
- Out of scope (stays raw until #221): `buildRFQPlan`'s supplier-name lookup and all of `insertBOMRFQ`
  (`po_number_seq`, `company.default_contact`, `purchase_order`, `po_history`, `po_line`). `rfq_bom.go`
  does not join `convertedFiles`. No `cfg.*Table()` helper loses its last caller.

## Changes
- `internal/parts/parts.sql` (+ `sqlc generate`):
  - `ListBOMComponents :many` — `pn.id, pl.qty, pn.part_number, COALESCE` description/revision/category
    to `''`, `pn.stock_on_hand, pn.reorder_min, pn.default_supplier_id`, and
    `EXISTS(SELECT 1 FROM bom c WHERE c.parent_part_id = pn.id) AS has_bom`, from `bom pl JOIN part pn`
    `WHERE pl.parent_part_id = $1` (no ORDER BY, same as today).
  - `ListPartNumbers :many` — `SELECT part_number FROM part`.
- `internal/parts/parts.go`: type `BOMComponent{ID int; Qty float64; PartNumber, Description, Revision,
  Category string; Stock float64; ReorderMin *float64; SupplierID *int; HasBOM bool}`;
  `ListBOMComponents(ctx, parentID int)`, `ListPartNumbers(ctx)`.
- `arx_go/rfq_bom.go`: `loadRFQGraph` calls `h.parts().ListBOMComponents` and fills `rfqPart` (keeps
  its `sql.Null*` fields, which the template and `rfq_bom_test.go` use).
- `arx_go/partnumber.go`: `nextBaseNumber` calls `h.parts().ListPartNumbers`.
- `arx_go/sqlc_converted_lint_test.go`: add `partnumber.go`.

## Test plan
1. Coverage audit: `rfq_bom_test.go` (`planRFQLines`, pure); `partnumber_test.go` (`suggestBaseNumber`,
   pure). Nothing covers `loadRFQGraph`/`buildRFQPlan` or `nextBaseNumber` against the DB.
2. Characterization (`arx_go/rfq_bom_integration_test.go`):
   - `BuildRFQPlan_Graph`: root (ASM) ×2 → B1 (BUY, stock 1, reorder_min 5, supplier 1001) ×3; SUB (MFG,
     stock 1) ×1 → B1 ×2, C (RAW, NULL description/revision, no supplier) ×4; U (NULL category) ×1;
     P (BUY, stock 100) ×1 → D (BUY, supplier 1001). Expect one group (1001, seeded name) with B1
     need 8 / qty 12; Unsupplied C need 4 / qty 4 with blank description/revision; D never planned.
   - `NextBaseNumber_ScansAllParts`: `nextBaseNumber` equals `suggestBaseNumber` over the loaded config
     and an independent `SELECT part_number` scan; `PartsNextNumber` returns it as JSON.
3. Red: `partnumber.go` in `convertedFiles` (`TestSQLCConvertedFilesHaveNoRawSQL` fails on the raw scan).
4. Manual-only: Part → BOM → Create RFQs preview/confirm; Settings part-numbering preview; New Part
   form's next-number hint.
