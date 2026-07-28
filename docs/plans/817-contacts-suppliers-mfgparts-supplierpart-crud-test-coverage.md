# #817 — CRUD test coverage for contacts, suppliers, mfg_parts, sourcing

Part of #801 (test coverage gap audit). Plan only — no code in this branch yet.

## Scope decisions (not open questions — stated so the implementer doesn't re-litigate them)

- **New file per area, build-tag `integration`, package `main`** — matches the
  most recent precedent (`records_lock_integration_test.go`, #806) rather than
  growing the single `integration_test.go` monolith or the table-driven
  `smoke_post_test.go`. Four new files:
  - `arx_go/contacts_integration_test.go`
  - `arx_go/suppliers_integration_test.go`
  - `arx_go/mfg_parts_integration_test.go`
  - `arx_go/sourcing_integration_test.go`
- **Reuse existing helpers, don't duplicate**: `liveHandler`, `withID`,
  `withIDAndAttID`-style constructors, `postForm`, `assert302`, `assertStatus`
  from `integration_test.go`; `seedPart`, `seedSupplier`, `smokeExec`,
  `smokeUniq`, `locID`, `countRows` from `smoke_post_test.go` (same package,
  same build tag — directly callable).
- **Never mutate pinned/seeded rows in Update/Delete tests.** Read-only tests
  (Detail/Edit/Rows/List) freely use pinned fixture IDs. Any test that writes
  (Update/Delete) seeds its own throwaway row and cleans it up, for two
  reasons: (a) `TestIntegration_UpdatedAtSentinel` in `integration_test.go`
  asserts contact ids 2001-2005 keep an untouched `updated_at` sentinel — an
  Update there would break it directly; (b) company rows 1001-1004 and their
  names/codes/ids are asserted-on by value across many *other* integration
  tests (RFQ/PO/pricing tests reference "Acme Fasteners", supplier_id 1001/1002
  literals, etc.) — mutating them would create fragile cross-test coupling
  even though no single existing assertion currently breaks. mfg_part/
  supplier_part pinned rows (4101/4102, 4001-4003) aren't referenced elsewhere
  by value, but the same throwaway convention is used for consistency.
- **List handlers (`ContactsList`, `SuppliersList`) are out of scope for new
  dedicated tests.** They take no params and run no query — pure
  `h.render(...)` of a static shell. `templates_parse_test.go` already
  guards template-parse safety; a dedicated integration test would add no
  real coverage. This matches the issue's own framing (it calls out
  `ContactDetail`/`contactPOs` and `SupplierDetail`, not `*List`, as the real
  gaps alongside Rows/New/Edit/Update).
- **`*New` handlers (`ContactsNew`, `SuppliersNew`) get a single 200-status
  assertion each, not template-content assertions.** They have no branching
  logic besides render with an empty model — a non-500 check closes the
  "entirely untested" gap the issue flags without coupling the test to
  `contact_edit.html`/`supplier_edit.html` markup that this plan didn't read.
- **New chi-route-param helpers needed** (mirroring `withIDAndAttID` in
  `integration_test.go`), added locally to the file that uses them since nothing
  else needs them:
  - `withIDAndMidID(req, id, mid int) *http.Request` — in
    `mfg_parts_integration_test.go`, for `mfg-parts/{mid}` routes.
  - `withIDAndSpID(req, id, spID int) *http.Request` — in
    `sourcing_integration_test.go`, for `suppliers/{spID}` routes.
- **`MfgPartCreate`/`SupplierPartCreate` redirect to the parent list URL, not a
  child-specific URL** (`/part/{id}/mfg-parts`, `/part/{id}/suppliers`) — so
  seed helpers for these two areas cannot `locID` the new child row from the
  `Location` header (unlike `seedPart`/`seedSupplier`). They must capture the
  new id via a follow-up `SELECT MAX(id) FROM <table> WHERE part_id=@p1`,
  mirroring the `filID` capture pattern already used in
  `TestIntegration_PartLifecycle` (`integration_test.go` lines ~259-266).

## Fixture reference (from `SQL/seed_test_data.sql`)

- Companies: 1001 Acme Fasteners (supplier, default_contact 2001), 1002
  Precision Machining Co (supplier+manufacturer, default_contact 2003), 1003
  Global Distribution (supplier/receiver, default_contact 2005), 1004 Contoso
  Manufacturing (manufacturer only, is_supplier=0).
- Contacts: 2001 John Doe (co 1001, fully populated — address/city/state/zip/
  country/phone_2/fax/website/notes), 2002 Jane Smith (co 1001, active), 2003
  Bob Lee (co 1002), 2004 Sam Retired (co 1001, **is_active=0**), 2005 Pat Dock
  (co 1003).
- Parts: 3001 RAW-1001, 3002 BUY-1001, 3003 BUY-1002 (all `BUY`/`RAW`
  categories — sourcing/mfg-parts tabs visible).
- mfg_part: 4101 (part 3002, mfg 1004 Contoso, MPN `CX-4471`, active), 4102
  (part 3002, mfg 1004, MPN `CX-9999-OBSOLETE`, **is_active=0**).
- supplier_part: 4001 (part 3001, supplier 1001, no uom/mfg_part_id), 4002
  (part 3002, supplier 1002, mfg_part_id 4101, uom 14/REEL, supplier_pn
  `PMC-M3X8`, min_increment 1000, lead_time `2-3 weeks`, preference 1), 4003
  (part 3003, supplier 1001).
- price (for part 3002 / supplier 1002, all `is_active=1` except noted):
  4202 pack 100 @ 0.06 (**inactive**, superseded), 4203 pack 100 @ 0.05, 4204
  pack 1 @ 0.10, 4205 pack 1000 @ 0.03.

## `arx_go/contacts_integration_test.go`

Local helper: `seedContact(t, h, ctx, companyID int) (id int, cleanup func())`
— mirrors `seedSupplier` in `smoke_post_test.go`: calls `h.ContactsCreate` with
`CNName`, `CNSUID=companyID`, `CNActive=1`, parses id via `locID(t, rec,
"/contact/")`, cleanup hard-deletes the row.

1. `TestIntegration_ContactsRows` — GET `h.ContactsRows`. Decode JSON body.
   Assert the row for id 2001 has `Supplier == "Acme Fasteners"`, `Active ==
   true`. Assert the row for id 2004 is present with `Active == false` (Rows
   includes inactive contacts, unlike sibling/detail filtering below).
2. `TestIntegration_ContactDetail` — `h.ContactDetail` with `withID(..., 2001)`.
   Assert 200. Assert body contains `"John Doe"`, `"100 Fastener Blvd"`,
   `"Dayton"` (fully-populated fields prove `fetchContact` scans every
   column). Assert body contains `"Jane Smith"` (2002 — active sibling at the
   same company) but does **not** contain `"Sam Retired"` (2004 — inactive,
   same company; proves `siblingContacts`' `is_active` filter). Assert body
   contains `"5003"` (contact 2001's supplier-role PO, cross-checking
   `contactPOs` is actually wired into the rendered page, not just unit-tested
   in isolation by the existing `TestIntegration_ContactPOs`).
3. `TestIntegration_ContactDetail_NotFound` — `withID(..., 999999999)`. Assert
   200 (render path, not an HTTP error status) and body contains
   `"Contact not found"`.
4. `TestIntegration_ContactEdit` — `withID(..., 2003)`. Assert 200, body
   contains `"Bob Lee"`.
5. `TestIntegration_ContactEdit_NotFound` — `withID(..., 999999999)`. Assert
   body contains `"Contact not found"`.
6. `TestIntegration_ContactUpdate` — seed a throwaway contact under company
   1001 via `seedContact`. POST `h.ContactUpdate` changing `CNName`,
   `CNEmail`, `CNSUID` (switch to company 1002), `CNActive=0`. `assert302` to
   `/contact/{id}`. Re-`SELECT display_name, email, company_id, is_active`
   directly and assert all four persisted.
7. `TestIntegration_ContactUpdate_MissingName` — against the same throwaway
   contact, POST with `CNName=""`. Assert 200 (re-render, no redirect), body
   contains `"Contact name is required"`. Re-`SELECT display_name` and assert
   it is unchanged from the original seed value (proves no partial write).
8. `TestIntegration_ContactsNew` — GET `h.ContactsNew`. Assert
   `rec.Code == http.StatusOK`. (No content assertions — see scope decisions.)

## `arx_go/suppliers_integration_test.go`

No new seed helper needed — reuse `seedSupplier` from `smoke_post_test.go`
directly for throwaway company rows (it already sets `is_active`,
`is_supplier`, `is_manufacturer`).

1. `TestIntegration_SuppliersRows` — GET `h.SuppliersRows`. Decode JSON. Find
   the row for id 1001: assert `Name == "Acme Fasteners"`, `Contact ==
   "John Doe"` (via `default_contact` join), `Country == "USA"` (contact
   2001's `country` field), `Active == true`.
2. `TestIntegration_SupplierDetail` — `withID(..., 1001)`. Assert 200, body
   contains `"Acme Fasteners"`. Assert `OtherContacts` renders `"Jane Smith"`
   (2002 — active, not the default contact) but not `"John Doe"` (2001 — is
   the default contact, excluded by the handler's explicit skip) and not
   `"Sam Retired"` (2004 — inactive, excluded by `contactsForSupplier`'s
   `is_active` filter). Assert `TopParts` renders `"RAW-1001"` (part 3001,
   linked via supplier_part 4001, supplier_id 1001).
3. `TestIntegration_SupplierDetail_NotFound` — `withID(..., 999999999)`.
   Assert body contains `"Supplier not found"`.
4. `TestIntegration_SupplierEdit` — `withID(..., 1002)`. Assert 200, body
   contains `"Precision Machining Co"` and `"Bob Lee"` (2003, via
   `contactsForSupplier`).
5. `TestIntegration_SupplierEdit_NotFound` — `withID(..., 999999999)`. Assert
   body contains `"Supplier not found"`.
6. `TestIntegration_SupplierUpdate` — seed a throwaway company via
   `seedSupplier`. POST `h.SupplierUpdate` changing `name`,
   `SUSupplierCode`, `is_active=0`, `is_supplier=1`, `is_manufacturer=0`,
   `default_contact=""`. `assert302` to `/supplier/{id}`. Re-`SELECT name,
   SUSupplierCode, is_active, is_supplier, is_manufacturer, default_contact`
   and assert all persisted (default_contact NULL).
7. `TestIntegration_SupplierUpdate_MissingName` — same throwaway row, POST
   `name=""`. Assert 200, body contains `"Supplier name is required"`.
   Re-`SELECT name` unchanged.
8. `TestIntegration_SupplierUpdate_InvalidFolderStub` — same throwaway row,
   POST `SUSupplierCode="a/b"`. Assert 200, body contains the
   `validateFolderStub` error text (`` `folder stub cannot contain` ``).
   Re-`SELECT SUSupplierCode` and assert it's unchanged from before this POST
   (proves the validation short-circuits before the UPDATE runs).
9. `TestIntegration_SuppliersNew` — GET `h.SuppliersNew`. Assert
   `rec.Code == http.StatusOK`.

## `arx_go/mfg_parts_integration_test.go`

Add `withIDAndMidID`. Local helper
`seedMfgPart(t, h, ctx, partID, mfgID int) (mid int, cleanup func())` — calls
`h.MfgPartCreate` via `postForm`/`withID`, then
`SELECT MAX(id) FROM mfg_part WHERE part_id=@p1` to capture the new id
(no id in the redirect Location — see scope decisions), cleanup hard-deletes
by id.

1. `TestIntegration_PartMfgParts` — `withID(..., 3002)`. Assert 200. Body
   contains `"CX-4471"` (active mfg_part 4101) and `"Contoso Manufacturing"`
   (join). Body does **not** contain `"CX-9999-OBSOLETE"` (4102, inactive —
   proves `fetchMfgParts`' `is_active` filter). Manufacturers dropdown
   (`fetchManufacturers`) includes `"Precision Machining Co"` (1002,
   is_manufacturer=1) but body does not contain `"Global Distribution"`
   (1003, is_manufacturer=0 — proves the manufacturer-list filter).
2. `TestIntegration_MfgPartEdit` — seed a throwaway part (`seedPart`) +
   throwaway manufacturer (`seedSupplier`) + `seedMfgPart`. `withIDAndMidID`
   GET `h.MfgPartEdit`. Assert 200, body contains the seeded MPN string.
3. `TestIntegration_MfgPartEdit_NotFound` — same throwaway part, `mid =
   999999999`. Assert body contains `"Manufacturer part not found"`.
4. `TestIntegration_MfgPartEdit_WrongPartScope` — seed a *second* throwaway
   part; call `MfgPartEdit` with the first part's valid `mid` but the second
   part's `id`. Assert body contains `"Manufacturer part not found"` (proves
   the `AND part_id=@p2` scoping guard).
5. `TestIntegration_MfgPartUpdate` — using the seed from test 2, POST
   `h.MfgPartUpdate` changing `mfg_part_number`, `description`, and `mfg_id`
   (switch to a second throwaway manufacturer via `seedSupplier`). `assert302`
   to `/part/{id}/mfg-parts`. Re-`SELECT mfg_part_number, description, mfg_id`
   and assert all three persisted.
6. `TestIntegration_MfgPartUpdate_MissingRequired` — same seeded row, POST
   `mfg_part_number=""`. Assert 200, body contains
   `"Manufacturer and MPN are required"`. Re-`SELECT mfg_part_number`
   unchanged.
7. `TestIntegration_MfgPartUpdate_WrongPartScope` — attempt
   `MfgPartUpdate` with the seeded mid but a *different* throwaway part's id.
   Assert302 still fires (the handler has no rows-affected check — this
   documents current behavior, not a bug to fix here). Re-`SELECT` the row
   under its *correct* part id and assert it is unchanged (proves the
   mismatched-part UPDATE was a silent no-op, not a cross-part write).
8. `TestIntegration_MfgPartDelete` — seed one throwaway part with **two**
   mfg_part rows (different manufacturers) via `seedMfgPart` x2. POST
   `h.MfgPartDelete` for the first mid. `assert302`. Re-`SELECT is_active`:
   first row is `0` (soft-deleted), sibling row is still `1` (delete doesn't
   affect siblings on the same part).

## `arx_go/sourcing_integration_test.go`

Add `withIDAndSpID`. Local helper
`seedSupplierPart(t, h, ctx, partID, supplierID int) (spID int, cleanup func())`
— calls `h.SupplierPartCreate`, then
`SELECT MAX(id) FROM supplier_part WHERE part_id=@p1` to capture the new id
(same redirect-has-no-child-id issue as mfg_part), cleanup hard-deletes by id.

1. `TestIntegration_PartSourcing` — `withID(..., 3002)`. Assert 200. Body
   contains `"PMC-M3X8"` (supplier_part 4002's `supplier_pn`),
   `"Precision Machining Co"` (join), `"2-3 weeks"` (`lead_time`).
2. `TestIntegration_FetchActivePricesBySupplier` — direct Go-level call (not
   through the HTTP handler, since price formatting in the template wasn't
   read for this plan): `h.fetchActivePricesBySupplier(r, "3002")`. Assert the
   returned map has key `1002` with exactly 3 entries (price rows 4203/4204/
   4205 — **not** 4202, which is inactive), ordered by pack_size ascending per
   the query's `ORDER BY ... pack_size`: `{PackSize:1, PriceEA:0.10}`,
   `{PackSize:100, PriceEA:0.05}`, `{PackSize:1000, PriceEA:0.03}`.
3. `TestIntegration_SupplierPartEdit` — `withIDAndSpID(..., 3002, 4002)`.
   Assert 200, body contains `"PMC-M3X8"`.
4. `TestIntegration_SupplierPartEdit_NotFound` — `withIDAndSpID(..., 3002,
   999999999)`. Assert body contains `"Supplier link not found"`.
5. `TestIntegration_SupplierPartEdit_WrongPartScope` — `withIDAndSpID(...,
   3001, 4002)` (4002 belongs to part 3002, not 3001). Assert body contains
   `"Supplier link not found"` (proves the `AND part_id=@p2` scoping guard).
6. `TestIntegration_SupplierPartUpdate` — seed a throwaway part (`seedPart`) +
   throwaway supplier (`seedSupplier`) + `seedSupplierPart`. POST
   `h.SupplierPartUpdate` changing `supplier_pn`, `preference`, `lead_time`,
   `min_increment`. `assert302` to `/part/{id}/suppliers`. Re-`SELECT
   supplier_pn, preference, lead_time, min_increment` and assert all
   persisted.
7. `TestIntegration_SupplierPartUpdate_MissingSupplier` — same seeded row,
   POST `supplier_id=""`. Assert 200, body contains `"Supplier is required"`.
   Re-`SELECT supplier_pn` unchanged (proves no partial write).
8. `TestIntegration_SupplierPartDelete` — seed one throwaway part with
   **two** supplier_part rows (different suppliers) via `seedSupplierPart` x2.
   POST `h.SupplierPartDelete` for the first spID. `assert302`. Re-`SELECT
   COUNT(*) FROM supplier_part WHERE part_id=@p1`: assert it's exactly 1
   (hard DELETE, unlike `MfgPartDelete`'s soft-delete — the surviving row's id
   must equal the sibling's, not the deleted one).

## Verification

- `cd arx_go; go build ./... && go vet ./...` (new files must compile under
  the `integration` build tag too: `go vet -tags integration ./...`).
- `go test ./...` (default sweep) must stay unaffected — new files are
  build-tag gated.
- Run live: `go test -tags integration ./arx_go/... -run
  'ContactsRows|ContactDetail|ContactEdit|ContactUpdate|ContactsNew|SuppliersRows|SupplierDetail|SupplierEdit|SupplierUpdate|SuppliersNew|PartMfgParts|MfgPartEdit|MfgPartUpdate|MfgPartDelete|PartSourcing|FetchActivePricesBySupplier|SupplierPartEdit|SupplierPartUpdate|SupplierPartDelete'`
  against `ARX_TEST_DSN` pointed at ArxDev.
- Confirm `TestIntegration_UpdatedAtSentinel` still passes after the full
  suite runs (guards that no new test accidentally touched pinned contact
  rows 2001-2005).
