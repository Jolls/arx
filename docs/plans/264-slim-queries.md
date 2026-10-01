# #264 — Slim queries for BOM edit/paste preview, PO line revision, supplier lookups, parts CSV

Refactor only. No schema change, no behavior change, no CHANGELOG-visible user effect beyond a `### Changed` line.

## Coordination with #278
- #278 (applied after #264, same branch) replaces the per-level `ListBOMLines` reads in `rollupCost` and `aggregateLeafQty` (`arx_go/parts.go`) with one WITH RECURSIVE query. **#264 does not touch `rollupCost` or `aggregateLeafQty` at all** (they keep `ListBOMLines`; #278 owns them).
- `ListBOMEdges` here is scoped to `PartBOMEdit` and `PartBOMPastePreview` only.
- Functions #264 touches in `arx_go/parts.go`: `PartBOMEdit`, `PartBOMPastePreview`, `PartsExportCSV` only. #278 must not need those.
- Shared files (`internal/parts/parts.sql`, `internal/parts/parts.go`, `internal/dbq/parts.sql.go`): #264 adds new queries/methods only and edits no existing query. Place `ListBOMEdges` directly after `ListBOMLines` in `parts.sql`; `ListPartsExport` directly after `ListParts`; `GetPartRevision` directly after `GetPart`. `ListBOMLines` and its service method stay (still used by `fetchBOMItems`, `rollupCost`, `aggregateLeafQty` until #278).
- `internal/dbq/*` is regenerated, so no manual merge conflicts; just re-run `sqlc generate` after #278.

## Current-code facts (verified)
- `PartBOMEdit` (parts.go ~728) calls `fetchBOMItems` (shared with `PartBOM`, `APIPartBOMChildren` api.go:135, `BOMExportCSV`; these keep `ListBOMLines`). Template `parts/part_bom_edit.html` uses only `.ID .LineNumber .PartNumber .ComponentPartID .Description .Qty` per item (plus `.Part.*`, `len .BOMItems`).
- `PartBOMPastePreview` (parts.go ~933) uses `ListBOMLines` only to build `existing[ComponentPartID] -> {ID, Qty}`.
- `resolvePolRev` (pos.go 1727) calls `GetPart`, ignores the error, returns `p.Revision` (`COALESCE(revision,'')`). Called at pos.go 531, 650, 669.
- `GetSupplier` call sites and the single field each uses:
  - pos.go:352 `fetchSupplierBulkOrderOptions` — `BulkOrderDelimiter`, `BulkOrderPNSource`; on error returns defaults ("comma","internal"); empty value keeps default.
  - pos.go:380 `applyPODefaults` — `Name`, `DefaultContact`; error ignored (missing -> blank name, nil contact).
  - pos.go:904 `POPrint` — `SupplierCode`; error ignored.
  - pos.go:1749 `createPOFolder` — `SupplierCode`; error ignored.
  - rfq_bom.go:192 `buildRFQPlan` — `Name`; only set when err == nil.
  - rfq_bom.go:325 `insertBOMRFQ` — `DefaultContact`; error ignored.
  - suppliers.go:537 `fetchSupplier` — needs the full row. **Unchanged.**
- `PartsExportCSV` (parts.go 2089) uses `PartNumber, Revision, Description, Detail, RequestedBy, CreatedDate, Category, ModifiedDate, IsActive` from `ListParts`.
- `company.bulk_order_*` are NOT NULL (SQL/postgres/company.sql:20-21), so they scan into plain `string`.

## File changes

### 1. `internal/parts/parts.sql` (new queries, existing ones untouched)
- `-- name: ListBOMEdges :many` after `ListBOMLines`: `SELECT pl.id, pl.line_number, pl.qty, pl.component_part_id, pn.part_number, COALESCE(pn.description, '') AS description FROM bom pl JOIN part pn ON pl.component_part_id = pn.id WHERE pl.parent_part_id = $1 ORDER BY pl.line_number;` (same join/order as `ListBOMLines`; no price subquery, no EXISTS, no counts).
- `-- name: GetPartRevision :one` after `GetPart`: `SELECT COALESCE(revision, '')::text AS revision FROM part WHERE id = $1;` (no rows -> `sql.ErrNoRows`).
- `-- name: ListPartsExport :many` after `ListParts`: same columns/COALESCEs/`ORDER BY p.part_number` as `ListParts` for `id, part_number, revision, description, detail, requested_by, created_date, category, modified_date, is_active` only (no `attachment_count`, `po_line_count`, `below_min`, `thumb_file`; no `thumb_category` arg).

### 2. `internal/purchasing/purchasing.sql` (new queries, `GetSupplier` untouched; add after `GetSupplier`)
- `GetSupplierCode :one` — `SELECT COALESCE(supplier_code, '') AS supplier_code FROM company WHERE id = $1;`
- `GetSupplierName :one` — `SELECT name FROM company WHERE id = $1;`
- `GetSupplierContactDefault :one` — `SELECT name, default_contact FROM company WHERE id = $1;` (no contact join)
- `GetSupplierBulkOrder :one` — `SELECT bulk_order_delimiter, bulk_order_pn_source FROM company WHERE id = $1;`

### 3. `sqlc.yaml`
No change (both query files already listed).

### 4. Run `sqlc generate` (repo root) — regenerates `internal/dbq/parts.sql.go`, `internal/dbq/purchasing.sql.go`. Never hand-edit. Commit the regenerated files.

### 5. `internal/parts/parts.go`
- Add type `BOMEdge struct { ID, LineNumber int; Qty float64; ComponentPartID int; PartNumber, Description string }`.
- Add `(*Service).ListBOMEdges(ctx, parentID) ([]BOMEdge, error)`, `(*Service).GetPartRevision(ctx, id) (string, error)` (returns `sql.ErrNoRows` when missing), `(*Service).ListPartsExport(ctx) ([]ListedPart, error)` (reuses the existing `ListedPart` type; unused fields stay zero). Follow the doc-comment/mapping style of the neighbouring methods.

### 6. `internal/purchasing/purchasing.go`
- Add `(*Service).GetSupplierCode(ctx, id) (string, error)`, `GetSupplierName(ctx, id) (string, error)`, `GetSupplierContactDefault(ctx, id) (name string, defaultContact *int, err error)`, `GetSupplierBulkOrder(ctx, id) (delimiter, pnSource string, err error)`. Each returns zero values plus the query error (including `sql.ErrNoRows`) so call-site error handling is unchanged.

### 7. `arx_go/parts.go`
- `PartBOMEdit`: replace the `fetchBOMItems` call with `h.parts().ListBOMEdges(r.Context(), pid)` (parse `id` with `strconv.Atoi`; on parse or query error keep the existing `h.renderError(..., "Error retrieving BOM: "+err.Error())`), then map each edge to `models.BOMItem{ID, LineNumber, Qty, ComponentPartID, PartNumber, Description}`. Everything else in the handler unchanged.
- `PartBOMPastePreview`: replace `ListBOMLines(r.Context(), p.ID)` with `ListBOMEdges`; `existing` becomes `map[int]parts.BOMEdge`; `ex.ID` / `ex.Qty` usages unchanged.
- `PartsExportCSV`: replace `ListParts(r.Context(), thumbnailCategory)` with `ListPartsExport(r.Context())`.
- Do NOT edit `fetchBOMItems`, `rollupCost`, `aggregateLeafQty`.

### 8. `arx_go/pos.go`
- `resolvePolRev` (1738-1739): `rev, _ := h.parts().GetPartRevision(r.Context(), id); return rev` (comment "unknown part -> \"\"" kept).
- `fetchSupplierBulkOrderOptions` (352): use `GetSupplierBulkOrder`; keep the err-return-defaults and non-empty-override logic.
- `applyPODefaults` (380): `name, defContact, _ := h.purchasing().GetSupplierContactDefault(...)`; `po.ReceiverName = name`; `receiver.DefaultContact` -> `defContact`.
- `POPrint` (904) and `createPOFolder` (1749): use `GetSupplierCode`, ignoring the error as today.

### 9. `arx_go/rfq_bom.go`
- 192: `GetSupplierName`, keep `err == nil` guard.
- 325: `_, defContact, _ := h.purchasing().GetSupplierContactDefault(ctx, g.SupplierID)`; replace `supplier.DefaultContact` (2 uses) with `defContact`.

### 10. `CHANGELOG.md`
One entry under the new top `## [0.8.x]` version (or the branch's existing one): `### Changed` — "Slimmer queries for BOM edit/paste preview, PO line revision, supplier lookups and parts CSV export ([#264](https://github.com/Jolls/arx/issues/264))". No RELEASE_NOTES change.

### Not changed
`suppliers.go` `fetchSupplier`, `GetSupplier`, `ListBOMLines`, `ListParts`, `GetPart`, `fetchBOMItems`, any schema/SQL DDL/seed/migration.

## Verify
`sqlc generate` then `sqlc diff` clean; `go build ./... && go vet ./... && go test ./...`; `ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/...`.

## Test plan

### (1) Coverage audit — existing tests on touched functions (all `integration` tag, live ArxDev, unless noted)
- `PartBOMEdit`: `arx_go/bom_integration_test.go` `TestIntegration_PartBOMEdit_Renders` (bom id, line number, part number, PNID hidden field, line order, rollup text); `arx_go/integration_test.go` `TestIntegration_BuildCostUIWiring` (page renders for 3005).
- `PartBOMPastePreview`: `bom_integration_test.go` `TestIntegration_PartBOMPastePreview` (noop/update/new/error rows; confirm disabled).
- `fetchBOMItems` (unchanged, regression guard): `TestIntegration_FetchBOMItems_Fields`, `TestIntegration_BOMExportCSV`.
- `resolvePolRev`: `po_reads_integration_test.go` `TestIntegration_POReads_ResolvePolRev` (form wins, empty pnid, rev "C", NULL rev, unknown id, non-numeric); indirectly `po_writes_integration_test.go` POCreate/POUpdate tests and `integration_test.go` POCreate/POUpdate at lines ~1298/1340/3473.
- `fetchSupplierBulkOrderOptions`: `suppliers_sqlc_integration_test.go` `TestIntegration_SupplierSQLC_POBulkOrderOptions` (tab/vendor and default comma).
- `applyPODefaults`: `po_reads_integration_test.go` `TestIntegration_POReads_ApplyPODefaults` (fallback contact, explicit contact, no receiver, unknown receiver).
- `POPrint` supplier code: `TestIntegration_POReads_PrintAndOpenFolder` (title with code).
- `createPOFolder`: `po_writes_integration_test.go` (test at ~line 416, code / no supplier / unknown supplier / test-mode suffix); `suppliers_integration_test.go` `TestIntegration_POCreate_FolderUsesSupplierCode`.
- `buildRFQPlan` supplier name: `rfq_bom_integration_test.go` `TestIntegration_BuildRFQPlan_Graph` (`g.SupplierName`).
- `PartsExportCSV`: `parts_list_integration_test.go` `TestIntegration_PartsExportCSV_Fields` (header + NULL handling + inactive).
- Unit (no DB): `sqlc_converted_lint_test.go` (no raw SQL in handlers) — must still pass.
- **Gaps:** `insertBOMRFQ` / `PartCreateRFQsConfirm` default-contact path has no test; `POPrint` with no/unknown supplier has none; `fetchSupplierBulkOrderOptions` with unknown supplier id has none directly.

### (2) Characterization tests (new; must pass on UNCHANGED code; integration tag, live ArxDev)
- `TestIntegration_PartCreateRFQsConfirm_UsesSupplierDefaultContact` (new, `rfq_bom_integration_test.go`): seed a supplier with a default contact, run `PartCreateRFQsConfirm` for an assembly whose purchased part uses it; assert the created RFQ row has `supplier_contact`/`supplier_email`/city from that contact, and blank contact fields for a supplier with no default contact. Cleanup via existing fixtures' pattern.
- `TestIntegration_POReads_PrintNoSupplier` (new, `po_reads_integration_test.go`): POPrint on a PO with nil supplier and on one whose supplier id is unknown -> 200, title is the bare number (no code suffix).
- `TestIntegration_POReads_BulkOrderOptions_UnknownSupplier` (new): `h.fetchSupplierBulkOrderOptions(req, &unknownID)` and `nil` -> `("comma","internal")`.
- `TestIntegration_PartBOMEdit_EmptyBOM` (new, `bom_integration_test.go`): `PartBOMEdit` on a leaf part (`f.L3`) -> 200, no `pl[` inputs, `newRowIdx = 0`.

### (3) Red tests
No red tests: behavior-preserving. Characterization in (1) and (2) pins the outputs of every changed call site; the only observable change is the SQL issued. (Optional, not required: none added, since asserting on SQL text would be a judgement call — see open question 2.)

### (4) Manual-only
- With `DEBUG_MODE=true`, open `/part/<assembly>/bom/edit`, run a BOM paste preview, create/update a PO with blank revision fields, open a PO detail and print, export `/parts/export.csv`: confirm the logged SQL is the slim queries (no `price` subquery for the edit/paste, no ~40-column `part` select per PO line, no `contact` join for supplier code/name lookups, no `part_attachment` subquery for the CSV).
- Visual check that the BOM edit page rows (item, part number, description, qty, delete) look identical to before.

## Open questions
1. Supplier slimming granularity: this plan adds four queries (`GetSupplierCode`, `GetSupplierName`, `GetSupplierContactDefault`, `GetSupplierBulkOrder`). Prefer fewer/more? (E.g. fold name into `GetSupplierCode`.)
2. `PartBOMEdit` needs `part_number` + `description` in addition to id/component/qty (issue text lists only id/component/qty), so `ListBOMEdges` includes them; paste preview ignores the extras. Acceptable, or should paste preview get a separate narrower query?
3. Should the red-test section stay "No red tests: behavior-preserving", or do you want a query-count/SQL-shape assertion (e.g. via the debug SQL logger) added?
4. `ListPartsExport` returns the existing `ListedPart` type with unused fields zeroed; OK, or add a dedicated `ExportPart` type?

## Resolved decisions (user, 2026-10-01)
- Supplier slimming: four queries as planned (GetSupplierCode, GetSupplierName, GetSupplierContactDefault, GetSupplierBulkOrder).
- ListBOMEdges includes part_number + description; PartBOMEdit and paste preview share it.
- Red tests: ADD a query-count assertion (counting DBTX wrapper) proving the slimmed call sites issue the slim queries / expected number of queries, in addition to the characterization tests. Fails on current code (wide queries / per-line GetPart), passes after.
- ListPartsExport reuses ListedPart, unused fields left zero.
