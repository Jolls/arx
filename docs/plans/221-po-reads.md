# #221 slice 2: PO reads → sqlc

Part of #221 / #190. Moves the read-only purchase-order sites in `arx_go/pos.go` onto
`internal/purchasing` (slice 1, PR #241, set up the package).

## Scope
- `fetchPO`, `fetchPOItems`, `fetchPOReceipts`, `fetchPOHistory`, `fetchSuggestLinks`,
  `fetchSuggestPrices`, `PORows`, `POsExportCSV`.
- `applyPODefaults` (receiver company name/default contact), `resolvePolRev` (part revision).
- Plain reads in `POPrint` (supplier code), `POOpenFolder` (supplier id by PO number) and
  `RFQCompare` (group grid query).

Out of scope: writes and the reads inside write handlers (slice 3: POCreate, POUpdate,
PODuplicate, PONote, POMarkPrinted, POAddSuggestions, createPOFolder, POImportPartFile), lifecycle
(slice 4), RFQ writes and `rfq_bom.go` (slice 5). `pos.go` stays out of `convertedFiles`. No
`cfg.*Table()` helper loses its last caller (all still used by the slice 3-5 sites).

## Decisions
- `purchasing.PO` has the same fields, in the same order, as `models.PurchaseOrder`, so
  `fetchPO` converts with `models.PurchaseOrder(po)`; a drift is a compile error.
  `purchasing.POLine` carries the attachment id/name/category and `tracking_mode` flat;
  `fetchPOItems` builds `models.PurchaseOrderLine` (`PrimaryAtt`, `IsLotTracked`).
- `resolvePolRev` reuses `parts.GetPart`; `applyPODefaults` and `POPrint` reuse
  `purchasing.GetSupplier`. Errors are still swallowed (missing row → `""`), as today.
- `fetchPOReceipts` lives in purchasing (it's keyed by PO), though it reads
  `inventory_transaction`.
- Service lists are non-nil; the template consumers (`range`/`if` on History, Receipts,
  POItems) and `len()` checks on the suggest lists behave the same for nil and empty.
- `RFQCompare`'s group param and `resolvePolRev`'s part id are parsed with Atoi; a non-numeric value
  now reads as "not found" / `""` instead of a Postgres cast error.

## Fix
`POsExportCSV` (GET /pos/export.csv) always failed: it joined `po_line` on a nonexistent
`po_number` column and selected nonexistent `part_number`/`vendor_pn`. It now joins on
`po_id` and exports `part_number_snapshot`/`vendor_part_number`; columns and formats unchanged.

## Test plan
New `arx_go/po_reads_integration_test.go` (`//go:build integration`); everything except the
export fix passes on the unchanged code first. Raw-SQL fixture: a throwaway company with a
supplier code and two contacts, two parts (one lot-tracked with a revision and primary
attachment, one with no revision), a supplier_part link, active/inactive prices, a fully
populated PO with six lines (freeform, zero-cost, duplicate), history events and receipt/non-receipt
ledger rows, a bare PO with NULL header fields, and an RFQ group with one quote lacking lines.
- fetchPO: every field on the full PO; NULLs on the bare one; unknown number → not found.
- fetchPOItems: line order, snapshots, lead time, received qty/date, attachment, lot tracking.
- fetchPOReceipts: receipts only, txn_date DESC then id DESC. fetchPOHistory: order, NULL → "".
- Suggest links/prices: the NOT EXISTS filters, zero cost, freeform lines, DISTINCT.
- PORows: fixture rows' fields incl. NULL supplier name/dates, number DESC.
- POsExportCSV: fixture lines by line_number, bare PO with blank line columns, number formats.
- applyPODefaults: explicit contact, fallback to the company's default contact, unknown receiver.
- resolvePolRev: form rev wins, blank/unknown/non-numeric id, NULL revision.
- POPrint supplier code in the title; POOpenFolder unknown number → 404.
- RFQCompare: quote without lines still gets a column; lead time and revision shown.

Verify: go build/vet (plus `-tags integration`), go test ./..., sqlc diff, gofmt -l on touched
hunks, live ArxDev integration run.
