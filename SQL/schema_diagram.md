# Database Schema

Reflects the SQL Server DDL in `SQL/azure/*.sql` (the standard; `SQL/postgres/*.sql` is a
dialect-translated port of the same tables). Solid relationship lines (`--`) are enforced
FOREIGN KEY constraints; dotted lines (`..`) are logical-only references with no DB-level
constraint — see "Non-enforced references" in Notes.

The [full schema](#full-schema) below has all 31 tables. For a smaller, more digestible view,
see the [diagrams by domain](#diagrams-by-domain) further down — each one is scoped to a
single area of the app, with entities it references from other domains shown as PK-only
stubs (full columns live in that entity's home diagram).

## Full schema

```mermaid
erDiagram

    %% ===== Companies & contacts =====

    company {
        int     id                    PK
        varchar name                  "UNIQUE"
        varchar notes
        bit     is_active
        bit     is_supplier
        bit     is_manufacturer
        int     supplier_part_count   "denormalized, trigger-maintained"
        int     po_count              "denormalized, trigger-maintained"
        varchar supplier_code
        int     default_contact       FK
        int     primary_attachment_id FK
    }

    company_attachment {
        int      supplier_attachment_id PK
        int      supplier_id            FK
        nvarchar file_path              "LOCAL:... or https://"
        nvarchar notes
        int      sort_order
        bit      is_active
    }

    contact {
        int     id            PK
        varchar display_name
        int     company_id    FK
        varchar email
        varchar phone_1
        varchar phone_2
        varchar website
        bit     is_active
    }

    %% ===== Core parts catalog =====

    part {
        int     id                    PK
        varchar part_number           "UNIQUE"
        varchar category              "FK part_category.code"
        varchar revision
        varchar description
        varchar release_status        "U/A/D"
        int     primary_attachment_id FK
        int     price_id              FK "deferred, not enforced"
        int     default_supplier_id   FK "deferred, not enforced"
        int     uom_id                FK
        int     attachment_count      "denormalized, trigger-maintained"
        int     po_line_count         "denormalized, trigger-maintained"
        decimal stock_on_hand         "cached SUM(inventory_transaction.qty)"
        decimal reorder_min
        varchar tracking_mode         "none/lot/serial/lot_serial"
        decimal last_rollup_cost
        bit     is_active
    }

    part_attachment {
        int     id            PK
        int     part_id       FK
        varchar file_name     "path or URL"
        varchar category
        varchar part_revision
        varchar comment
        int     sort_order
        bit     is_active     "soft-delete only"
    }

    bom {
        int     id                 PK
        int     parent_part_id     FK
        int     component_part_id  FK
        int     line_number
        decimal qty
    }

    price {
        int     id             PK
        int     part_id        FK
        int     supplier_id    FK
        decimal price_ea
        decimal price_pack
        decimal pack_size
        date    effective_date
        bit     is_active
    }

    supplier_part {
        int     id            PK
        int     supplier_id   FK
        int     part_id       FK
        int     mfg_part_id   FK
        int     uom_id        FK
        varchar supplier_pn
        varchar supplier_desc
        decimal min_increment
        varchar lead_time
        int     preference
    }

    mfg_part {
        int     id              PK
        int     part_id         FK
        int     mfg_id          FK
        varchar mfg_part_number
        varchar description
        bit     is_active
    }

    uom {
        int     uom_id       PK
        varchar abbreviation
        varchar display_name
        varchar unit_type    "count/volume/length/mass/package"
    }

    %% ===== Purchasing =====

    purchase_order {
        int     id                  PK
        varchar number              "UNIQUE"
        int     supplier_id         FK
        int     receiver_id         FK
        int     supplier_contact_id FK
        int     receiver_contact_id FK
        decimal total_cost
        date    date_ordered
        date    date_closed
        varchar status              "draft/open/sent/partially_received/closed/cancelled/rfq"
        varchar approval_status     "not_submitted/pending/approved/rejected"
        int     rfq_group_id
        bit     is_active
    }

    purchase_order_history {
        int      id          PK
        int      po_id       FK
        varchar  event_type  "status/approval"
        varchar  from_status
        varchar  to_status
        varchar  action      "submitted/approved/rejected/reset"
        varchar  changed_by
        datetime changed_at
    }

    po_line {
        int     id                   PK
        int     po_id                FK
        int     part_id              FK
        varchar part_number_snapshot
        int     line_number
        decimal qty
        decimal unit_cost
        varchar vendor_part_number
        int     lead_time_days
        decimal received_qty
        date    date_received
    }

    %% ===== Inventory, lot & serial traceability =====

    inventory_transaction {
        int     id         PK
        int     part_id    FK
        varchar txn_type   "receipt/issue/adjustment/count"
        decimal qty        "signed"
        date    txn_date
        varchar username
        int     po_line_id FK
        int     lot_id     FK
        int     build_id   FK
    }

    lot {
        int      id                PK
        int      part_id           FK
        varchar  lot_number
        varchar  lot_description
        varchar  vendor_lot_number
        varchar  source            "purchase/build/adjust"
        int      po_line_id        FK
        varchar  notes             "free-text batch notes"
        datetime created_at
        bit      is_active
    }

    build {
        int     id            PK
        int     part_id       FK
        int     output_lot_id FK
        decimal qty
        date    build_date
        varchar username
        varchar note
    }

    unit {
        int      id            PK
        int      part_id       FK
        int      lot_id        FK
        int      build_id      FK
        varchar  serial_number "UNIQUE per part_id"
        varchar  source        "test/manual"
        bit      is_active
        datetime created_at
    }

    genealogy {
        int     id             PK
        int     parent_lot_id  FK "exactly one of parent_lot_id/parent_unit_id set"
        int     parent_unit_id FK
        int     child_lot_id   FK "exactly one of child_lot_id/child_unit_id set"
        int     child_unit_id  FK
        decimal qty_consumed
    }

    %% ===== Test forms & records =====

    form {
        int     id              PK
        int     part_number_id  FK
        varchar test_order      "comma-separated form_row.id list"
        bit     is_locked
        bit     is_active
        varchar form_type       "inspection/test/calibration/checklist/batch record"
        varchar record_types
        varchar instrument_types
        int     revision
    }

    form_row {
        int     id            PK
        int     form_id       FK "logical only, not enforced"
        int     type          "0=data,1-3=heading"
        varchar parameter
        varchar specification
        varchar spec_min
        varchar spec_max
        varchar spec_nom
        varchar spec_units
        varchar pf_type
        varchar granularity   "unit/lot"
        bit     archived
        varchar hide_formula
        varchar instrument_types
    }

    form_row_history {
        int      id          PK
        int      form_row_id FK "logical only, not enforced"
        datetime changed_at
        varchar  changed_by
        int      type
        varchar  parameter
        varchar  specification
        varchar  spec_min
        varchar  spec_max
        varchar  spec_nom
        varchar  hide_formula
        varchar  pf_type
    }

    form_record {
        int      id             PK
        int      form_id        FK "logical only, not enforced"
        int      part_id        FK
        int      lot_id         FK "logical only, not enforced"
        int      build_id       FK "logical only, not enforced"
        int      unit_id        FK
        varchar  serial_number
        datetime record_date
        varchar  instrument_type
        varchar  record_type    "record Type in the UI"
        varchar  notes          "session-level remark"
        bit      is_locked
        bit      is_approved
        bit      is_active
        int      form_revision
    }

    form_events {
        int      id         PK
        int      form_id    FK
        varchar  event_type "locked/unlocked/archived/activated"
        varchar  username
        datetime event_date
        varchar  comments
    }

    result {
        int     id             PK
        int     form_record_id FK "logical only, not enforced"
        int     form_row_id    FK "logical only, not enforced"
        bit     pass_fail
        varchar result
        varchar comment
        varchar parameter
        varchar specification
        varchar spec_min
        varchar spec_max
        varchar spec_nom
        varchar pf_type
        int     type           "0=data,1-3=heading"
    }

    record_events {
        int      id             PK
        int      form_record_id FK
        varchar  event_type     "locked/unlocked/archived/activated"
        varchar  username
        datetime event_date
        varchar  comments
    }

    record_event_results {
        int     id           PK
        int     event_id     FK
        int     form_row_id  FK "logical only, not enforced"
        varchar parameter
        varchar specification
        varchar result
        bit     pass_fail
    }

    %% ===== App config, users & audit =====

    part_category {
        varchar code                  PK
        varchar label
        bit     is_purchased
        bit     is_bom_visible        "plus orders/pricing/mfg_parts/suppliers/inventory tab flags"
        int     sort_order
    }

    attachment_category {
        varchar display_name          PK
        int     sort_order
    }

    app_config {
        varchar  setting_key   PK
        varchar  setting_value
        datetime updated_at
    }

    named_queries {
        int     id          PK
        varchar name        "UNIQUE, referenced by spec_nom"
        varchar description
        varchar sql
        varchar params
        varchar result_type "list/single/multi"
        bit     is_active
    }

    users {
        int     id                      PK
        varchar username                "UNIQUE"
        varchar display_name
        varchar password_hash
        bit     is_active
        bit     can_approve_po
        bit     can_approve_records
        bit     is_admin
        int     default_po_contact_id
        int     default_po_receiver_id
        varchar accent_color
        varchar default_route
        varchar timezone
    }

    %% ===== Relationships: companies & contacts =====
    company             ||--o{ company_attachment : "attachments (supplier_id)"
    company_attachment  ||--o{ company             : "primary attachment for (primary_attachment_id)"
    company             ||..o{ contact             : "contacts (company_id, deferred FK)"
    contact             ||--o{ company             : "default contact for (default_contact)"
    company             ||--o{ supplier_part       : "sourcing links (supplier_id)"
    company             ||--o{ mfg_part            : "as manufacturer (mfg_id)"
    company             ||--o{ price               : "pricing (supplier_id)"
    company             ||--o{ purchase_order      : "as supplier (supplier_id)"
    company             ||--o{ purchase_order      : "as receiver (receiver_id)"
    contact             ||--o{ purchase_order      : "as supplier contact (supplier_contact_id)"
    contact             ||--o{ purchase_order      : "as receiver contact (receiver_contact_id)"

    %% ===== Relationships: core parts catalog =====
    part             ||--o{ part_attachment  : "attached files (part_id)"
    part_attachment  ||--o{ part             : "primary attachment for (primary_attachment_id)"
    part             ||--o{ bom              : "as parent assembly (parent_part_id)"
    part             ||--o{ bom              : "as component (component_part_id)"
    part             ||--o{ supplier_part    : "sourcing links (part_id)"
    part             ||--o{ mfg_part         : "manufacturer PNs (part_id)"
    part             ||--o{ price            : "pricing (part_id)"
    part             ||--o{ po_line          : "PO lines (part_id)"
    uom               ||--o{ part            : "base unit (uom_id)"
    part_category     ||--o{ part            : "category (code)"
    uom               ||--o{ supplier_part   : "purchase unit (uom_id)"
    mfg_part          ||--o{ supplier_part   : "manufacturer PN (mfg_part_id)"

    %% ===== Relationships: purchasing =====
    purchase_order ||--o{ po_line                 : "line items (po_id)"
    purchase_order ||--o{ purchase_order_history  : "activity log (po_id)"

    %% ===== Relationships: inventory, lot & serial traceability =====
    part     ||--o{ inventory_transaction : "stock ledger (part_id)"
    po_line  ||--o{ inventory_transaction : "receipts (po_line_id)"
    lot      ||--o{ inventory_transaction : "movements (lot_id)"
    build    ||--o{ inventory_transaction : "movements (build_id)"
    part     ||--o{ lot                   : "lots (part_id)"
    po_line  ||--o{ lot                   : "purchased lots (po_line_id)"
    part     ||--o{ build                 : "builds (part_id)"
    lot      ||--o{ build                 : "output lot (output_lot_id)"
    part     ||--o{ unit                  : "serialized units (part_id)"
    lot      ||--o{ unit                  : "units in lot (lot_id)"
    build    ||--o{ unit                  : "build-sourced units (build_id)"
    lot      ||--o{ genealogy             : "as parent lot (parent_lot_id)"
    lot      ||--o{ genealogy             : "as child lot (child_lot_id)"
    unit     ||--o{ genealogy             : "as parent unit (parent_unit_id)"
    unit     ||--o{ genealogy             : "as child unit (child_unit_id)"

    %% ===== Relationships: test forms & records =====
    part           ||--o{ form                  : "test forms (part_number_id)"
    part           ||--o{ form_record           : "test records (part_id)"
    lot            ||..o{ form_record           : "tested-unit lot (lot_id, logical only)"
    build          ||..o{ form_record           : "tested-unit build (build_id, logical only)"
    unit           ||--o{ form_record           : "unit under test (unit_id)"
    form           ||..o{ form_row              : "test definitions (form_id, logical only)"
    form           ||..o{ form_record           : "executed records (form_id, logical only)"
    form           ||--o{ form_events           : "audit events (form_id)"
    form_row       ||..o{ result                : "results (form_row_id, logical only)"
    form_row       ||..o{ form_row_history      : "audit snapshots (form_row_id, logical only)"
    form_row       ||..o{ record_event_results  : "snapshot rows (form_row_id, logical only)"
    form_record    ||..o{ result                : "results (form_record_id, logical only)"
    form_record    ||--o{ record_events         : "audit events (form_record_id)"
    record_events  ||--o{ record_event_results  : "lock snapshot (event_id)"
```

## Notes

- **Table naming**: all tables use lowercase snake_case (the legacy uppercase-abbreviation
  names — `PN`, `LNK`, `SU`, `PL`, `POL`, `FIL`, `CN`, `Forms`, `Tests`, `TestRecords`,
  `TestResults` — were fully renamed across #534–#693; see `SQL/SCHEMA.md` for the
  per-table rename history).
- **Non-enforced references** (dotted lines above): a handful of FK-shaped columns have no
  actual `FOREIGN KEY` constraint in the DDL — either explicitly deferred
  (`contact.company_id`, `part.price_id`, `part.default_supplier_id` — see #213/#465) or a
  leftover commented-out `ALTER TABLE ... ADD CONSTRAINT` in the DDL file that was never
  applied (`form_row.form_id`, `form_record.form_id`, `form_record.lot_id`,
  `form_record.build_id`, `result.form_record_id`, `result.form_row_id`,
  `form_row_history.form_row_id`, `record_event_results.form_row_id`). `form_record.part_id`
  and `form.part_number_id` were promoted to real enforced FKs in #742/#744 respectively —
  they are drawn solid above.
- **`genealogy`** endpoints are polymorphic: `CK_gen_one_parent`/`CK_gen_one_child` each
  enforce exactly one of the lot/unit FK pair being set per edge, so a single edge is
  lot→lot, lot→unit, unit→lot, or unit→unit.
- **`app_config`**, **`named_queries`**, and **`users`**
  have no foreign key relationships to other tables.
- Denormalized/trigger-maintained columns (`company.supplier_part_count`/`po_count`,
  `part.attachment_count`/`po_line_count`) are recalculated by triggers in
  `SQL/azure/triggers.sql` — never updated directly in application code.
- Per-table column semantics, trigger side-effects, and full DDL live in `SQL/SCHEMA.md`
  and `SQL/azure/*.sql`.

## Diagrams by domain

### Parts & sourcing

What a part is and what it's built from. `company_schematic` is a compact stub standing in
for `company`/`mfg_part`/`price`/`supplier_part` — the company-side tables that price, source,
and manufacture a part — fully detailed in "Companies" below.

```mermaid
erDiagram

    part {
        int     id                    PK
        varchar part_number           "UNIQUE"
        varchar category              "FK part_category.code"
        varchar release_status        "U/A/D"
        int     uom_id                FK
        int     price_id              FK "deferred, not enforced"
        int     default_supplier_id   FK "deferred, not enforced"
        int     primary_attachment_id FK
        bit     is_active
    }

    part_attachment {
        int     id        PK
        int     part_id   FK
        varchar file_name "path or URL"
        varchar category
        bit     is_active
    }

    bom {
        int     id                 PK
        int     parent_part_id     FK
        int     component_part_id  FK
        decimal qty
    }

    uom {
        int     uom_id       PK
        varchar abbreviation
        varchar unit_type    "count/volume/length/mass/package"
    }

    company_schematic {
        varchar company
        varchar mfg_part
        varchar price
        varchar supplier_part
    }

    part_category {
        varchar code PK
    }

    part                ||--o{ part_attachment    : "attached files (part_id)"
    part_attachment     ||--o{ part               : "primary attachment for (primary_attachment_id)"
    part                ||--o{ bom                : "as parent assembly (parent_part_id)"
    part                ||--o{ bom                : "as component (component_part_id)"
    uom                 ||--o{ part               : "base unit (uom_id)"
    part_category       ||--o{ part               : "category (code)"
    part                ||--o{ company_schematic  : "pricing (price.part_id)"
    part                ||--o{ company_schematic  : "sourcing links (supplier_part.part_id, uom_id)"
    part                ||--o{ company_schematic  : "manufacturer PNs (mfg_part.part_id)"
    company_schematic   ||..o{ part               : "preferred supplier & active price (default_supplier_id, price_id — deferred)"
```

### Companies

Suppliers, manufacturers, and their contacts, plus the sourcing/pricing/manufacturer-PN
tables that bridge to parts. `parts_schematic` is a compact stub standing in for
`part`/`part_attachment`/`bom`/`uom` — fully detailed in "Parts & sourcing" above.

```mermaid
erDiagram

    company {
        int     id                    PK
        varchar name                  "UNIQUE"
        bit     is_supplier
        bit     is_manufacturer
        int     default_contact       FK
        int     primary_attachment_id FK
        bit     is_active
    }

    company_attachment {
        int      supplier_attachment_id PK
        int      supplier_id            FK
        nvarchar file_path              "LOCAL:... or https://"
        bit      is_active
    }

    contact {
        int     id           PK
        varchar display_name
        int     company_id   FK
        varchar email
        bit     is_active
    }

    price {
        int     id          PK
        int     part_id     FK
        int     supplier_id FK
        decimal price_ea
        decimal pack_size
        bit     is_active
    }

    supplier_part {
        int     id          PK
        int     supplier_id FK
        int     part_id     FK
        int     mfg_part_id FK
        int     uom_id      FK
        varchar supplier_pn
        int     preference
    }

    mfg_part {
        int     id              PK
        int     part_id         FK
        int     mfg_id          FK
        varchar mfg_part_number
        bit     is_active
    }

    parts_schematic {
        varchar part
        varchar part_attachment
        varchar bom
        varchar uom
    }

    company              ||--o{ company_attachment : "attachments (supplier_id)"
    company_attachment   ||--o{ company             : "primary attachment for (primary_attachment_id)"
    company              ||..o{ contact             : "contacts (company_id, deferred)"
    contact              ||--o{ company             : "default contact for (default_contact)"
    company               ||--o{ supplier_part       : "sourcing links (supplier_id)"
    company               ||--o{ mfg_part            : "as manufacturer (mfg_id)"
    company               ||--o{ price               : "pricing (supplier_id)"
    mfg_part               ||--o{ supplier_part        : "manufacturer PN (mfg_part_id)"
    parts_schematic         ||--o{ price                : "priced parts (part_id)"
    parts_schematic         ||--o{ supplier_part         : "sourced parts & purchase unit (part_id, uom_id)"
    parts_schematic         ||--o{ mfg_part              : "manufacturer PNs (part_id)"
    company                 ||..o{ parts_schematic        : "preferred supplier & active price for (default_supplier_id, price_id — deferred)"
```

### Purchasing / PO lifecycle

The RFQ → PO → receive flow. `part` is a PK-only stub — see "Parts & sourcing" above;
`company`/`contact` are PK-only stubs — see "Companies" above.

```mermaid
erDiagram

    company {
        int id PK
    }

    contact {
        int id PK
    }

    part {
        int id PK
    }

    purchase_order {
        int     id                  PK
        varchar number              "UNIQUE"
        int     supplier_id         FK
        int     receiver_id         FK
        int     supplier_contact_id FK
        int     receiver_contact_id FK
        varchar status              "draft/open/sent/partially_received/closed/cancelled/rfq"
        varchar approval_status     "not_submitted/pending/approved/rejected"
        int     rfq_group_id
        decimal total_cost
    }

    po_line {
        int     id             PK
        int     po_id          FK
        int     part_id        FK
        int     line_number
        decimal qty
        decimal unit_cost
        decimal received_qty
        int     lead_time_days
    }

    company         ||--o{ purchase_order : "as supplier (supplier_id)"
    company         ||--o{ purchase_order : "as receiver (receiver_id)"
    contact         ||--o{ purchase_order : "as supplier contact (supplier_contact_id)"
    contact         ||--o{ purchase_order : "as receiver contact (receiver_contact_id)"
    purchase_order  ||--o{ po_line        : "line items (po_id)"
    part            ||--o{ po_line        : "ordered part (part_id)"
```

### Inventory, lot & serial traceability

The #676/#736 lot-control and serialization epic: goods receipt and builds create `lot`
rows, individual tested units get a `unit` row, and `genealogy` records which lot/unit was
consumed into which. `po_line` and `form_record` are PK-only stubs — `po_line` is fully
defined in "Purchasing" above, `form_record` in "Test forms & execution" below. `part` is
scoped to just its lot-tracking columns here (full definition in "Parts & sourcing" above) —
`tracking_mode` is why a part enters this flow at all, so it's kept
rather than trimmed to a bare stub.

```mermaid
erDiagram

    part {
        int     id             PK
        varchar part_number
        varchar tracking_mode  "none/lot/serial/lot_serial"
        decimal stock_on_hand  "cached SUM(inventory_transaction.qty)"
    }

    po_line {
        int id PK
    }

    inventory_transaction {
        int     id         PK
        int     part_id    FK
        varchar txn_type   "receipt/issue/adjustment/count"
        decimal qty        "signed"
        int     po_line_id FK
        int     lot_id     FK
        int     build_id   FK
    }

    lot {
        int      id              PK
        int      part_id         FK
        varchar  lot_number
        varchar  lot_description
        varchar  source          "purchase/build/adjust"
        int      po_line_id      FK
        bit      is_active
    }

    build {
        int     id            PK
        int     part_id       FK
        int     output_lot_id FK
        decimal qty
        date    build_date
    }

    unit {
        int      id            PK
        int      part_id       FK
        int      lot_id        FK
        int      build_id      FK
        varchar  serial_number "UNIQUE per part_id"
        varchar  source        "test/manual"
        bit      is_active
    }

    genealogy {
        int     id             PK
        int     parent_lot_id  FK "exactly one of parent_lot_id/parent_unit_id set"
        int     parent_unit_id FK
        int     child_lot_id   FK "exactly one of child_lot_id/child_unit_id set"
        int     child_unit_id  FK
        decimal qty_consumed
    }

    form_record {
        int id      PK
        int lot_id   FK "logical only, not enforced"
        int build_id FK "logical only, not enforced"
        int unit_id  FK
    }

    part     ||--o{ inventory_transaction : "stock ledger (part_id)"
    po_line  ||--o{ inventory_transaction : "receipts (po_line_id)"
    lot      ||--o{ inventory_transaction : "movements (lot_id)"
    build    ||--o{ inventory_transaction : "movements (build_id)"
    part     ||--o{ lot                   : "lots (part_id)"
    po_line  ||--o{ lot                   : "purchased lots (po_line_id)"
    part     ||--o{ build                 : "builds (part_id)"
    lot      ||--o{ build                 : "output lot (output_lot_id)"
    part     ||--o{ unit                  : "serialized units (part_id)"
    lot      ||--o{ unit                  : "units in lot (lot_id)"
    build    ||--o{ unit                  : "build-sourced units (build_id)"
    lot      ||--o{ genealogy             : "as parent lot (parent_lot_id)"
    lot      ||--o{ genealogy             : "as child lot (child_lot_id)"
    unit     ||--o{ genealogy             : "as parent unit (parent_unit_id)"
    unit     ||--o{ genealogy             : "as child unit (child_unit_id)"
    lot      ||..o{ form_record           : "tested-unit lot (lot_id, logical only)"
    build    ||..o{ form_record           : "tested-unit build (build_id, logical only)"
    unit     ||--o{ form_record           : "unit under test (unit_id)"
```

### Test forms & execution

Form definitions vs. the frozen per-record snapshot taken at test time. `lot`/`build`/`unit`
are PK-only stubs — see "Inventory, lot & serial traceability" above for their full columns;
`form_record.lot_id`/`build_id`/`unit_id` is how a test record ties back to what was tested.

```mermaid
erDiagram

    part {
        int id PK
    }

    form {
        int     id              PK
        int     part_number_id  FK
        bit     is_locked
        bit     is_active
        varchar form_type       "inspection/test/calibration/checklist/batch record"
        int     revision
    }

    form_row {
        int     id           PK
        int     form_id      FK "logical only, not enforced"
        int     type         "0=data,1-3=heading"
        varchar parameter
        varchar spec_min
        varchar spec_max
        varchar spec_nom
        varchar granularity  "unit/lot"
    }

    form_record {
        int      id             PK
        int      form_id        FK "logical only, not enforced"
        int      part_id        FK
        int      lot_id         FK "logical only, not enforced"
        int      build_id       FK "logical only, not enforced"
        int      unit_id        FK
        bit      is_locked
        bit      is_approved
        bit      is_active
    }

    result {
        int     id             PK
        int     form_record_id FK "logical only, not enforced"
        int     form_row_id    FK "logical only, not enforced"
        bit     pass_fail
        varchar result
    }

    lot {
        int id PK
    }

    build {
        int id PK
    }

    unit {
        int     id            PK
        varchar serial_number
    }

    part          ||--o{ form         : "test forms (part_number_id)"
    part          ||--o{ form_record  : "test records (part_id)"
    form          ||..o{ form_row     : "test definitions (form_id, logical only)"
    form          ||..o{ form_record  : "executed records (form_id, logical only)"
    form_row      ||..o{ result       : "results (form_row_id, logical only)"
    form_record   ||..o{ result       : "results (form_record_id, logical only)"
    lot           ||..o{ form_record  : "tested-unit lot (lot_id, logical only)"
    build         ||..o{ form_record  : "tested-unit build (build_id, logical only)"
    unit          ||--o{ form_record  : "unit under test (unit_id)"
```

### Audit trails

A recurring pattern that cuts across three unrelated domains: an append-only event log,
sometimes paired with a per-event frozen snapshot table. `form`, `form_row`, `form_record`,
and `purchase_order` are PK-only stubs — see their home diagrams above for full columns.

```mermaid
erDiagram

    form {
        int id PK
    }

    form_events {
        int      id         PK
        int      form_id    FK
        varchar  event_type "locked/unlocked/archived/activated"
        varchar  username
        datetime event_date
    }

    form_row {
        int id PK
    }

    form_row_history {
        int      id          PK
        int      form_row_id FK "logical only, not enforced"
        datetime changed_at
        varchar  changed_by
    }

    form_record {
        int id PK
    }

    record_events {
        int      id             PK
        int      form_record_id FK
        varchar  event_type     "locked/unlocked/archived/activated"
        varchar  username
        datetime event_date
    }

    record_event_results {
        int     id          PK
        int     event_id    FK
        int     form_row_id FK "logical only, not enforced"
        varchar parameter
        varchar result
        bit     pass_fail
    }

    purchase_order {
        int id PK
    }

    purchase_order_history {
        int      id          PK
        int      po_id       FK
        varchar  event_type  "status/approval"
        varchar  from_status
        varchar  to_status
        varchar  action      "submitted/approved/rejected/reset"
        varchar  changed_by
        datetime changed_at
    }

    form             ||--o{ form_events           : "audit events (form_id)"
    form_row         ||..o{ form_row_history      : "audit snapshots (form_row_id, logical only)"
    form_record      ||--o{ record_events         : "audit events (form_record_id)"
    record_events    ||--o{ record_event_results  : "lock snapshot (event_id)"
    form_row         ||..o{ record_event_results  : "snapshot rows (form_row_id, logical only)"
    purchase_order   ||--o{ purchase_order_history : "activity log (po_id)"
```
