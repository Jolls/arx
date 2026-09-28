# #220 slice: part search + supplier-part autofill APIs → sqlc

Part of #220 / #190. Last parts-domain slice: moves the two part-table reads left in `arx_go/api.go`
onto `internal/parts`.

## Scope
- `APIPartSearch` (`GET /api/parts/search?q=&by=`): `part_number LIKE`, or `description`/`detail`
  LIKE when `by=desc`, ordered by part_number, 25 rows. One query with a `by_desc` boolean param
  instead of the Sprintf'd WHERE.
- `APISupplierPN` (`GET /api/supplier-part?part_id=&supplier_id=`): most-preferred supplier_part
  row's supplier_pn/min_increment plus the smallest-pack active price_ea. Listed under #221 but
  it only reads parts tables. Non-numeric ids now fail Atoi instead of failing in Postgres; both
  paths return `{"supplier_pn": ""}`.

Out of scope: APISupplierSearch/APISupplierContacts and rfq_bom.go's remaining sites (#221),
api.go's record/form lookup (#223). api.go stays out of `convertedFiles`. No `cfg.*Table()`
helper loses its last caller (PartsTable/SupplierPartTable/PriceTable are still used elsewhere).

## Fix
Part search with no matches returned JSON `null` (nil slice), which breaks `parts.length` in the
BOM-edit and PO-edit autocomplete JS. The service returns an empty slice, so this becomes `[]`.

## Changes
- `internal/parts/parts.sql`: SearchParts, GetSupplierPartDefaults. `sqlc generate`.
- `internal/parts/parts.go`: `PartMatch`, `SupplierPartDefaults` + service methods; package doc.
- `arx_go/api.go`: both handlers parse/call/render via `h.parts()`.

## Test plan
New `arx_go/api_part_integration_test.go` (`//go:build integration`), passing on the unchanged
code first. Seeds its own parts/supplier_part/price rows against supplier 1003/1004, cleans up.
- Search: short q → `[]`; by pn (substring, order, case-sensitive, NULL description/detail → "");
  by desc matching description or detail but not part_number; 25-row limit; no match → `null`
  (flipped to `[]` with the conversion).
- Supplier PN: missing/non-numeric ids; no link (price present but ignored); lowest preference
  wins; min_increment NULL/0 omitted; price inactive-only / NULL price_ea omitted; smallest active
  pack wins. The existing seed-based test (3002/1002) stays.

Verify: go build/vet (plus `-tags integration`), go test ./..., sqlc diff, gofmt -l on touched
hunks, live ArxDev integration run.
