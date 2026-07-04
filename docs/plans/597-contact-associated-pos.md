# Plan — Show a contact's associated POs (issue #597)

## Context

On a contact's detail page there's currently no way to see which purchase orders
that contact is on. This is awkward today because `purchase_order` only stores a
**name-string snapshot** of the contact (`supplier_contact` / `receiver_contact`,
`VARCHAR(127)`) — there is no foreign key back to `contact.id`, so POs can't be
looked up reliably by contact (names collide and break on rename).

Per the issue and confirmed choices, we will **add nullable FK columns while
keeping the name snapshot** for print/display, wire the PO contact `<select>` to
also submit the contact id, backfill existing POs by name+company match, and add
a "Purchase Orders" card to the contact detail page. Scope covers **both** the
supplier contact and the receiver contact.

## Approach

Two new nullable columns on `purchase_order`: `supplier_contact_id` and
`receiver_contact_id`, each FK → `contact.id`. The existing name-snapshot columns
stay as-is (still what prints on the PO). The PO form's two contact dropdowns —
which today submit the contact *name* as the option value — gain a parallel
hidden `*_contact_id` field populated from a `data-cnid` on each option. The
contact detail page queries POs where either id matches.

## Changes

### 1. Schema / DDL / seed / migration

- **`SQL/PO.sql`** — add after the respective name columns:
  - `supplier_contact_id INT` (comment: FK → contact.id; NULL if not chosen / legacy)
  - `receiver_contact_id  INT`
  - Two `ALTER TABLE ... ADD CONSTRAINT FK_purchase_order_supplier_contact FOREIGN KEY (supplier_contact_id) REFERENCES dbo.contact (id)` and the receiver equivalent, mirroring the existing enforced `FK_purchase_order_company`/`_receiver`. Nullable, enforced (contact rows are soft-deleted, never hard-deleted, so the FK is safe).
- **`SQL/migrations/migrate_po_contact_id.sql`** (new, human-run reference script) —
  `ALTER TABLE ADD` the two columns + FKs, then backfill:
  ```sql
  UPDATE po SET supplier_contact_id = c.id
  FROM purchase_order po JOIN contact c
    ON c.company_id = po.supplier_id AND c.display_name = po.supplier_contact;
  -- same for receiver_contact_id / receiver_id / receiver_contact
  ```
  (Add the FK constraints after the backfill so no partially-matched row blocks it.)
- **`SQL/seed_test_data.sql`** — in the PO update blocks (~lines 286–299) set the
  ids on the seeded POs so the feature has fixtures: PO 5002 → `supplier_contact_id=2003`
  (Bob Lee), PO 5003 → `supplier_contact_id=2001` (John Doe); both → `receiver_contact_id=2005`
  (Pat Dock). This gives contact 2001/2003/2005 detail pages a linked PO to render.
- **`SQL/schema.md`** — extend the `purchase_order` table-reference row noting the
  two new nullable contact FKs and that the name snapshot is retained.

No new `cfg.*Table()` helper is needed (no new table; `ContactTable()`/`POTable()` already exist).

### 2. Go model — `arx_go/models/purchase_order.go`

Add `SupplierContactID *int` and `ReceiverContactID *int` (next to the existing
`SupplierContact` / `ReceiverContact` string fields).

### 3. Go handlers — `arx_go/pos.go`

- **Create** (INSERT ~line 500): add `supplier_contact_id, receiver_contact_id` to
  the column list + placeholders, bind `nullableInt(fv(r, "supplier_contact_id"))`
  and `nullableInt(fv(r, "receiver_contact_id"))` (reuse existing `nullableInt`).
- **Update** (~line 735): add `supplier_contact_id=@pX, receiver_contact_id=@pY`.
- **fetchPO** (SELECT ~line 1712, scan ~line 1724): select the two columns, scan
  into `sql.NullInt64`, assign to the new `*int` model fields.
- **RFQConvert** award insert (~line 2328): add both columns to the column list
  **and** the `SELECT` list so the FK carries onto the awarded PO.
- **Duplicate-as-RFQ** path (~line 1999, where supplier fields are blanked): also
  set `source.SupplierContactID = nil` (receiver is preserved, so leave its id).

### 4. Template — `arx_go/templates/pm/po_edit.html`

- Add two hidden inputs near each contact select:
  `<input type="hidden" id="supplier_contact_id" name="supplier_contact_id" value="{{if .PO.SupplierContactID}}{{derefInt .PO.SupplierContactID}}{{end}}">`
  and the receiver equivalent.
- Server-rendered options (lines ~77, ~132): add `data-cnid="{{.CNID}}"`.
- `fillContactFields` (~line 395): when an option is chosen, set
  `document.getElementById(prefix+'_contact_id').value = opt.dataset.cnid || ''`;
  when cleared to "-- Select --" (early-return branch), clear the hidden id.
- `refreshContacts` JS (~line 481): add `opt.dataset.cnid = c.id` (the
  `/api/suppliers/{id}/contacts` endpoint already returns `id` — `api.go:64`).

### 5. Contact detail — `arx_go/contacts.go` + `templates/pm/contact_detail.html`

- New helper `contactPOs(ctx, contactID)` (model on the `siblingContacts` pattern,
  ~line 119) returning a small struct per PO: `POID, Number, Role ("Supplier"/"Receiver"),
  CounterpartyName (supplier_name), Status, DateOrdered, TotalCost`.
  Query:
  ```sql
  SELECT id, number,
         CASE WHEN supplier_contact_id=@p1 THEN 'Supplier' ELSE 'Receiver' END AS role,
         supplier_name, status, date_ordered, total_cost
  FROM <POTable> WHERE supplier_contact_id=@p1 OR receiver_contact_id=@p1
  ORDER BY date_ordered DESC, id DESC
  ```
  Wire the result into `ContactDetail`'s render map as `"POs"`.
- `contact_detail.html`: add a "Purchase Orders" card (same `{{if .POs}}` +
  Bootstrap `card` pattern as the Siblings card). A `table table-sm` listing
  PO number (link to `/po/{{.POID}}`), status (badge), role, date, total. Use
  existing template helpers (`formatDate`, currency helper if one exists — check
  `handlers.go` funcmap) for formatting.

### 6. CHANGELOG.md

One entry under a new `## [0.x.y]` version:
`- Show a contact's associated purchase orders on the contact detail page ([#597](https://github.com/Jolls/arx-legacy/issues/597))`.

## Verification

- `cd arx_go && go build ./... && go vet ./... && go test ./...` — must pass
  (template parse test `templates_parse_test.go` covers the edited template).
- Manual (user-run, per CLAUDE.md — I won't run the app):
  1. Open PO 5002 in edit, confirm the supplier/receiver contact dropdowns still
     populate address/email and that saving keeps the printed name snapshot.
  2. Create a new PO, pick a supplier contact, save; open that contact's detail
     page and confirm the PO appears under "Purchase Orders".
  3. Open contact 2001 (John Doe) / 2003 (Bob Lee) / 2005 (Pat Dock) detail pages
     and confirm the seeded POs (5002/5003) list with the correct role.
- Integration (ArxDev only, if run): a test in `integration_test.go` asserting
  `contactPOs` returns PO 5003 for contact 2001 (supplier role) and 5002/5003 for
  contact 2005 (receiver role). **Requires ArxDev reseed** (human action) so the
  new seed ids are present — I'll flag this rather than reseed myself.

## Notes / risks

- Existing DBs need `migrate_po_contact_id.sql` run once (reference script, not
  auto-applied) for historical POs to link.
- Enforced FK is safe because contacts are soft-deleted (`is_active=0`), never
  hard-deleted; all id sources (dropdown, award-copy, backfill) yield valid ids.
- Suggested regression test: the `contactPOs` query / role-CASE logic is the one
  piece that could silently break — worth an integration test (item above).
