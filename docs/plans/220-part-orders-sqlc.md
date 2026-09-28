# #220 slice: part orders/records cards + PartDetail queries → sqlc

Part of #220 / #190. Takes `arx_go/parts.go` to zero raw SQL so it joins `convertedFiles`.

## Scope
- `PartOrders` (orders tab), `recentPartPOs`, `recentPartTxns`, `preferredSupplier`,
  `partPricePoints` (PO + price-list samples; Price History tab and dashboard trend card).
- `PartDetail`'s primary-attachment lookup, active-attachment list, and preferred-supplier
  minimum price (rollup delta / Preferred Supplier card price).
- `ensureDefaultSupplier`: service method taking ints. Callers in parts.go/sourcing.go already
  hold ints (`p.ID`, `supplierID`); pos.go's caller Atoi's its two form strings; a parse
  error gives id 0, which matches no row (the old UPDATE failed silently on a bad id, so
  behaviour is unchanged).

Out of scope: api.go, rfq_bom.go (#221), the rest of pos.go.

## Changes
- `internal/parts/parts.sql`: ListPartOrders, ListRecentPartPOs, ListRecentPartTxns,
  GetPreferredSupplier, ListPOPricePoints, ListPriceListPoints,
  PreferredSupplierMinPrice, EnsureDefaultSupplier. `sqlc generate`. PartDetail's attachment
  reads reuse `internal/attachments` (GetPartAttachment, ListPartAttachments).
- `internal/parts/parts.go`: flat types + service methods; package doc updated.
- `arx_go/parts.go`: helpers take `partID int` and call `h.parts()`; result shapes unchanged.
- `arx_go/pos.go`, `arx_go/sourcing.go`: ensureDefaultSupplier call sites.
- `sqlc_converted_lint_test.go`: add `parts.go`. Drop any `cfg.*Table()` helper left with no caller.

## Test plan
New `arx_go/part_orders_integration_test.go` (`//go:build integration`), written and passing
on the unchanged code first. Seeds a BUY part with default supplier 1002, three POs (one with a
NULL date_ordered), inventory txns, supplier_part rows, active/inactive/undated prices and a
spread of attachments; cleans up after.
- PartOrders renders every column, newest first (NULL date first), and the empty state.
- recentPartPOs / recentPartTxns: exact rows, order, limit, NULL handling.
- preferredSupplier: pinned + linked (lowest preference), pinned without link, unpinned → nil.
- partPricePoints: exact samples (undated PO/price rows and inactive prices excluded, pack size).
- PartDetail: primary attachment, top-5 list excluding primary, photos excluding thumbnail,
  preferred price on the card and the rollup delta.
- ensureDefaultSupplier stays covered by the existing PriceCreate / DigiKey import tests.

Verify: go build/vet (plus `-tags integration`), go test ./..., sqlc diff, gofmt -l on touched
hunks, live ArxDev integration run.
