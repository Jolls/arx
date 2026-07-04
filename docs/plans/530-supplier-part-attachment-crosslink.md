# #530 — Crosslink a single Attachment to a supplier_part sourcing row

## Context

On the Part detail page's **Supplier** sub-tab (`part_sourcing.html`), each row is a `supplier_part` record — one supplier's part number/catalog entry for a given internal part. Supplier attachments (datasheets, quotes, catalog pages) are already tracked per-supplier in `company_attachment`, and suppliers/parts already support a single "primary attachment" crosslink (`company.primary_attachment_id`, `part.primary_attachment_id`) via a shared `h.setPrimaryAttachment` helper. There's no way today to link one of a supplier's existing attachments to a *specific* sourcing row, so issue #530 asks for that: pick one attachment for a sourcing row, and hyperlink the Supplier Part Number cell to open it.

This only crosslinks to an attachment that already exists on the supplier — no new upload capability from this tab.

## Approach

Add `supplier_part.attachment_id` (nullable FK → `company_attachment.supplier_attachment_id`). Expose it as a `<select>` in the existing **Edit** form only (not the Add form, since the Add form's supplier is chosen via typeahead and its attachments aren't known until the row is saved). Render the Supplier Part Number as a hyperlink when set, reusing the existing supplier-local-file URL/PDF/HTTP-link template logic already used in `supplier_attachments.html`.

## Changes

### 1. Schema — `SQL/supplier_part.sql`
- Add column: `attachment_id INT NULL` (snake_case — table is already renamed, per `SQL/schema.md:39`).
- Add FK constraint alongside the existing four: `FK_supplier_part_attachment FOREIGN KEY (attachment_id) REFERENCES dbo.company_attachment (supplier_attachment_id)`.
- Update `SQL/schema.md` table-reference entry for `supplier_part` to document the new FK.
- No `*Table()` helper needed (`SupplierPartTable()` already exists) — this is a column add, not a new table, so the CLAUDE.md "new table" checklist doesn't fully apply. Still, check whether `SQL/seed_test_data.sql`'s existing `supplier_part` seed rows (4001-4099 range) should have the new column added to their column lists — leave NULL for existing rows unless the user wants a seeded example.

### 2. Model — `arx_go/models/supplier.go`
In `SupplierPart` struct (~line 38), add:
- `AttachmentID *int` — raw FK, mirrors `UnitID *int`.
- `AttachmentFilePath string` — joined display field, mirrors `PurchaseUnitAbbr` (populated via LEFT JOIN, empty string when nil).

### 3. Handlers — `arx_go/sourcing.go`
- **`fetchSupplierLinks`** (~line 173): add `LEFT JOIN company_attachment ca ON sp.attachment_id = ca.supplier_attachment_id` and select `sp.attachment_id, ca.file_path AS attachment_file_path`; scan into the new struct fields (nullable int/string handling, same pattern as `unit_id`/`pu.abbreviation` today).
- **`SupplierPartEdit`** (~line 70): add `attachment_id` to the SELECT and scan (same `sql.NullInt64` pattern as the row's other nullable columns), plus a query to fetch the current supplier's attachment list (`SELECT supplier_attachment_id, file_path, notes FROM company_attachment WHERE supplier_id = @p1 ORDER BY sort_order`) to populate the dropdown — pass this list into the template as e.g. `SupplierAttachments`.
- **`SupplierPartUpdate`** (~line 127): add `attachment_id=@pN` to the UPDATE, parsed via the existing `nullableInt(r.FormValue("attachment_id"))` helper (same pattern as `unit_id`).
- **`SupplierPartCreate`**: no change (attachment picker is edit-only per decision).

### 4. Template — `arx_go/templates/pm/part_sourcing.html`
- **Row display** (~line 34): wrap Supplier PN in a link when `.AttachmentFilePath` is set, reusing the existing link-rendering logic (HTTP URL vs `supplierLocalFileURL` vs plain) currently in `supplier_attachments.html` (~lines 28-44) — extract/mirror that conditional rather than duplicating divergent logic.
- **Edit form** (~lines 87-136): add a `<select name="attachment_id">` populated from `SupplierAttachments` (passed from `SupplierPartEdit`), with an empty/"None" option, defaulting to `.EditingLink.AttachmentID`. Options label: attachment filename/notes, similar to how `supplier_attachments.html` displays file entries.

### 5. Files not touched
- No new route/handler needed for serving the file — reuse whatever route already serves `company_attachment.file_path` today (the existing `supplierLocalFileURL` helper / `/supplier/{id}/file/*` or `/supplier-local/` mount used in `supplier_attachments.html` and `supplier_detail.html`).
- `arxlib/config/config.go` — no change (table helper already exists).

## Verification
1. `cd arx_go && go build && go vet && go test ./...` — must pass.
2. Manual (user, per CLAUDE.md — do not run `go run .`/`Arx.exe` myself):
   - Open a part with existing supplier sourcing rows and a supplier that has at least one attachment uploaded.
   - Edit a sourcing row, confirm the new attachment dropdown lists that supplier's attachments, pick one, save.
   - Confirm the Supplier Part Number cell now renders as a link and opens the correct file.
   - Confirm a sourcing row with no attachment linked still renders Supplier PN as plain text (no regression).
   - Confirm unrelated columns (preference, lead time, unit, etc.) still save correctly.
3. Consider a unit test for the new nullable `attachment_id` parsing/scan path in `sourcing.go` if there's an existing test file covering `SupplierPartUpdate`/`fetchSupplierLinks` (suggest, don't write without confirming with user, per CLAUDE.md §5).
