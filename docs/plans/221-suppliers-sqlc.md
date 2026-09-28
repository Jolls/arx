# #221 slice 1: suppliers → sqlc

Part of #221 / #190. First purchasing-domain slice: moves `arx_go/suppliers.go` and the supplier
lookups in `api.go`/`pos.go` onto a new `internal/purchasing` package (plus one method each on
`internal/contacts` and `internal/attachments`).

## Scope
- `suppliers.go`: `SuppliersRows`, `SupplierDetail` (primary attachment, `recentSupplierPOs`,
  `topSupplierParts`), `SuppliersCreate`, `SupplierUpdate`, `SupplierParts` (linked parts + PO
  links), `SupplierPOs`, `fetchSupplier`. → `convertedFiles`.
- `api.go`: `APISupplierSearch`, `APISupplierContacts`. api.go stays out of `convertedFiles`
  (record/form lookup is #223).
- `pos.go`: `contactsForSupplier` (same query as `APISupplierContacts`) and
  `fetchSupplierBulkOrderOptions` (a pure `company` read). Moved here from slice 2.

Out of scope: every other pos.go / rfq_bom.go site (slices 2-5). No `cfg.*Table()` helper loses
its last caller (Company/Contact/SupplierPart/PO/POLine/Uom/Attachments/CompanyAttachments are
still used in pos.go, rfq_bom.go and elsewhere).

## Decisions
- `internal/purchasing` owns company/PO/po_line queries; supplier-centric reads that join
  `supplier_part`/`part` (linked parts, top parts, PO links) live here too, since
  `parts.ListSupplierParts` is keyed by part.
- `internal/contacts` stays separate: new `ListActiveForCompany`, used by both
  `APISupplierContacts` and `contactsForSupplier`.
- `internal/attachments`: new `GetCompanyAttachment(id)` for the detail page's primary attachment
  (same semantics as today: by id only, no active filter).
- POReceive's inventory writes (`createLot`, `recordInventoryTxn`) stay with #222; slice 4 converts
  only POReceive's own purchase_order/po_line statements on the same tx.
- `recentSupplierPOs`' "limit <= 0 = unlimited" becomes one query with `LIMIT sqlc.narg(n)`
  (`LIMIT NULL` = no limit).
- `SupplierParts` (its own id/name read) and `fetchSupplierBulkOrderOptions` reuse `GetSupplier`
  instead of separate company queries; the error messages are unchanged.
- `APISupplierSearch` keeps the case-sensitive `LIKE`; `supplier_only` becomes a boolean param.
- Supplier id from the URL is parsed with Atoi; a non-numeric id now fails as "not found" / a
  no-op update instead of a Postgres cast error.

## Fix
`APISupplierSearch` and `APISupplierContacts` returned JSON `null` for no rows (nil slice). The
typeahead `show()` (`suppliers.length`) and PO-edit `refreshContacts` (`contacts.forEach`) throw
on `null`. Service lists are non-nil, so these become `[]`.

## Changes
- `internal/purchasing/purchasing.sql` + `purchasing.go` (new), `sqlc.yaml` queries.
- `internal/contacts/contacts.sql`/`.go`: `ListActiveForCompany`.
- `internal/attachments/attachments.sql`/`.go`: `GetCompanyAttachment`.
- `sqlc generate` → `internal/dbq`.
- `arx_go/suppliers.go`, `api.go`, `pos.go` handlers call `h.purchasing()` / `h.contacts()` /
  `h.attachments()`; `suppliers.go` → `convertedFiles`.

## Test plan
New `arx_go/suppliers_sqlc_integration_test.go` (`//go:build integration`), passing on the
unchanged code first. Seeds a throwaway company with contacts, a primary attachment, parts +
supplier_part links (explicit/inherited unit, thumbnail), POs (dated/undated, RFQ quote) via raw
SQL, cleans up. Seed companies 1001-1004 read-only.
- Rows: fixture row fields/counts.
- Detail: primary attachment notes; recent POs capped at 5, `date_ordered DESC` (NULL first);
  top parts capped at 5 by part_number; other contacts exclude default/inactive.
- POs tab: all POs, order, totals.
- Parts tab: order, thumb, unit explicit/inherited, min increment, PO links (RFQ excluded, number
  DESC); not found.
- Create: fields persisted, bulk-order defaults, default_contact, duplicate-name error.
- Update: bulk-order options persisted / invalid → defaults.
- Search API: short q, match order, city, inactive excluded, supplier_only, case-sensitive, 20-row
  limit, no match `null` (→ `[]`).
- Contacts API: active only, order, NULL → "", no contacts `null` (→ `[]`).
- PO detail: bulk-order delimiter/PN source from the supplier; defaults for seed 1001.

Verify: go build/vet (plus `-tags integration`), go test ./..., sqlc diff, gofmt -l on touched
hunks, live ArxDev integration run.
