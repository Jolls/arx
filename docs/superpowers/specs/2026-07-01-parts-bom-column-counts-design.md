# Parts/BOM denormalized counts + column visibility (#550)

## Problem
`Part.PNFILLinks` / `Part.PNPOLinks` (DB columns `attachment_count` / `po_line_count`)
are maintained by SQL triggers but only fetched by the single-part detail handler —
never shown on the Parts list or the BOM view. Issue #550 asks to surface them there,
and to add the existing Records-tab column hide/show feature (#386) to both views.

## Approach

1. **Share the column-toggle engine.** Move the generic column-visibility code
   (`data-col-table`, `data-toggle-col`, `data-col`, localStorage persistence) from
   `arx_go/static/tr/app.js` into `arx_go/static/pm/app.js`, which is already loaded
   by both `pm/layout.html` and `tr/layout.html`. No behavior change for Records;
   makes the feature available to Parts/BOM without duplicating it.

2. **Parts list** (`templates/pm/index.html`, `PartsRows` in `parts.go`):
   - Add `attachment_count`, `po_line_count` to the `PartsRows` SQL query and JSON row.
   - Add "Attachments" and "PO Lines" columns to the table and to
     `pm/app.js`'s `ROW_BUILDERS['/api/parts/rows']` / `CELL_TEXT['/api/parts/rows']`.
   - Add `data-col="col-x"` to every `<th>` (both header and filter rows) and every
     `<td>` in the row builder, plus a "Columns" dropdown (matching
     `records_index.html`'s markup) with a checkbox per column, all toggleable.

3. **BOM view** (`templates/pm/part_bom.html`, `PartBOM` in `parts.go`):
   - Add `pn.attachment_count, pn.po_line_count` to the BOM query and scan into new
     `BOMItem` fields.
   - Add "Attachments" and "PO Lines" columns to the table.
   - Add the same "Columns" dropdown; mark every `<th>`/`<td>` with `data-col`. This
     table is server-rendered, so no JS row-builder changes are needed here.

## Out of scope
- `part_bom_edit.html` (edit view) — issue only asks for the BOM *view*.
- Any change to how the counts are computed/maintained (triggers untouched).

## Testing
- `go test ./...` (existing suite) must still pass.
- Manual check: Parts list and BOM view show the two new columns; Columns dropdown
  hides/shows them and persists across reload; Records tab dropdown still works
  unchanged after the app.js move.
