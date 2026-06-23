# SQL Schema Conventions

ER diagram: [schema_diagram.md](schema_diagram.md)

## Go-forward convention (new tables)

All new tables use snake_case. Do not extend the legacy prefix style for new work.

### Tables
- Singular noun: `purchase_order`, not `purchase_orders`
- All lowercase snake_case: `company_attachment`, `inventory_transaction`
- New tables must exist in both prod and `ArxDev` (re-run `SQL/_test.sql` to populate)

### Columns
- All lowercase snake_case
- Primary key: bare `id` — e.g. `id INT PRIMARY KEY IDENTITY`
- Foreign key: `{stem}_id` referencing that table's `id` — e.g. `supplier_id INT` pointing at `company.id`. Stems: `part`, `po`, `attachment`, `form`, `record`, `test`.
- Booleans: `is_` prefix — `is_active`, `is_locked` (not `active`, `locked`)
- Timestamps: `created_at`, `updated_at` (DATETIME, DEFAULT GETDATE())
- Avoid SQL reserved words as column names: `name`, `date`, `type`, `order`, `value`, `key`
  — use `display_name`, `order_date`, `record_type`, etc.

### Example

```sql
CREATE TABLE company_attachment (
    id          INT            PRIMARY KEY IDENTITY,
    supplier_id INT            NOT NULL,     -- FK → company.id
    file_path   NVARCHAR(1024) NOT NULL,     -- LOCAL:... path or https:// URL
    notes       NVARCHAR(512),
    sort_order  INT,
    created_at  DATETIME       DEFAULT GETDATE(),
    updated_at  DATETIME       DEFAULT GETDATE()
);
```

### What to do with legacy tables
- The legacy tables are being renamed to snake_case in the db-table-rename effort (one commit per table/group): `CN`→`contact` (commit 1), `PL`→`bom` (2), `FIL`→`part_attachment` (3), `PO`/`PO_history`→`purchase_order`/`purchase_order_history` (4), `POL`→`po_line` (5), `PN`→`part_number` (6), the test-records group (`Forms`→`form`, `TestRecords`→`test_record`, `TestResults`→`test_result`, plus `Parameter`/`Specification` lowercasing) (commit 7), `part_number`→`part` (commit 8, table name only — the `part_number` column is kept). Go struct fields intentionally retain the old names during this effort — DB column names and Go field names will diverge until a follow-up cleanup aligns them.
- New columns added to a legacy table that has not yet been renamed should still use the legacy prefix style. Once a table is renamed, use snake_case for any new columns.
- When a legacy table is fully replaced/migrated, use the go-forward convention for the replacement.

---

## Legacy conventions (existing tables)

Two eras of tables exist. When extending or mirroring a legacy table, follow its existing style.

### Table naming

Two eras of tables exist in this schema. Follow the era of the table you are extending or parallel-ing.

**Legacy tables** (VBA/Ruby era) — uppercase short abbreviation; being renamed in the db-table-rename effort:

| Table | Abbreviation | Notes |
|-------|-------------|-------|
| `part` | — | Part numbers / parts catalog (renamed from `PN` in commit 6, then `part_number`→`part` in commit 8; the `part_number` column is retained) |
| `part_attachment` | — | File/URL attachments (to parts) (renamed from `FIL` in db-table-rename commit 3) |
| `supplier_part` | — | Sourcing links (migrated from `LNK`) |
| `mfg_part` | — | Manufacturer part numbers |
| `bom` | —     | Parts list / BOM (renamed from `PL` in db-table-rename commit 2) |
| `po_line` | — | PO line items (renamed from `POL` in db-table-rename commit 5) |

**Go-era tables** — lowercase snake_case:

| Table | Notes |
|-------|-------|
| `company` | Mixed: has both generic columns and leftover `SU`-prefixed columns |
| `contact` | Contacts (renamed from `CN` in db-table-rename commit 1) |
| `purchase_order` | Clean snake_case (renamed from `PO` in db-table-rename commit 4) |
| `purchase_order_history` | Clean snake_case (renamed from `PO_history` in db-table-rename commit 4) |
| `inventory_transaction` | Clean snake_case |
| `price` | Clean snake_case |

## Column naming

**Legacy tables** prefix every column with the table abbreviation:

```
{TABLE_ABBREV}ID          -- primary key (e.g. PNID, FILID, CNID)
{TABLE_ABBREV}{TargetABBREV}ID  -- foreign key (e.g. FILPNID → PN, LNKSUID → supplier)
{TABLE_ABBREV}ColumnName  -- all other columns (e.g. FILFileName, LNKVendorPN)
```

Exception: `order_id` in `part_attachment` (formerly `FIL`) broke the prefix rule and was renamed to `sort_order` in db-table-rename commit 3.

**Go-era tables** use generic snake_case: `id`, `name`, `is_active`, `date_modified`, `supplier_id`.

## Test mode

`TEST_MODE=true` points the DSN at the `ArxDev` database instead of prod. Table names are
identical in both databases — prod vs test is a database-level distinction, not a name suffix.
`cfg.*Table()` helpers return bare names (`part`, `company`, etc.) regardless of test mode.
Never hardcode a table name in Go — always call the helper.

## Triggers

Trigger DDL lives in `SQL/triggers.sql`. ArxDev equivalents are recreated by `SQL/_test.sql` under the same names (no `_Test` suffix — ArxDev uses bare table names).

| Trigger | Table | Effect |
|---------|-------|--------|
| `trg_supplier_part_company_count` | `supplier_part` | Recalculates `company.SUNumOfLNKs` after any INSERT/UPDATE/DELETE |
| `trg_PO_company_count` | `purchase_order` | Recalculates `company.SUNumOfPOs` after any INSERT/UPDATE/DELETE |
| `trg_FIL_part_count` | `part_attachment` | Recalculates `part.attachment_count` (active rows only) after any INSERT/UPDATE/DELETE |
| `trg_POL_part_count` | `po_line` | Recalculates `part.po_line_count` after any INSERT/UPDATE/DELETE |
| `trg_test_definition_history` | `test_definition` | Snapshots old row values into `test_definition_history` AFTER UPDATE (audit trail). |

> **Dropped trigger:** `trg_Tests_history` was a legacy AFTER UPDATE trigger on `test_definition` created when the table was named `Tests`. It referenced the old column `applicable_instrs` (since renamed to `instrument_types`), silently rolling back every UPDATE once the rename was applied. It was dropped in v0.4.1 and superseded by `trg_test_definition_history`.

These fire for all writers (Go app and VBA). Do not update `SUNumOfLNKs`, `SUNumOfPOs`, `part.attachment_count`, or `part.po_line_count` manually in application code.

## DDL file conventions

- One `.sql` file per table, in `SQL/`.
- Scripts are **reference DDL**, not migration runners — the app does not execute them automatically.
- Use `IF OBJECT_ID('dbo.TableName', 'U') IS NOT NULL DROP TABLE dbo.TableName;` at the top.
- Keep the file in sync with schema changes made directly to the live DB.
- Add `TODO:` comments for known drift between the script and the live schema.

---

## Table reference

Key facts per table: primary key, trigger side-effects, and column semantics that affect application code.

| Table | PK | Notes |
|-------|----|-------|
| `part` | `id` | Parts catalog (renamed from `PN` in commit 6, then `part_number`→`part` in commit 8; the `part_number` column keeps its name; Go struct fields still use old `PN`-prefixed names). `release_status`: U/A/D. `is_active` (was `active`). `user_field_1-10` = configurable fields. `attachment_count` (was `PNFILLinks`) maintained by `trg_FIL_part_count`, `po_line_count` (was `PNPOLinks`) by `trg_POL_part_count` — do not update either in code. `last_rollup_cost` is `DECIMAL(16,8) NULL` (NULL = no rollup run). `unit_id` → `unit.unit_id` (base/inventory unit). `primary_attachment_id` → `part_attachment.id`. `stock_on_hand` (issue #272) is a cached inventory balance = `SUM(inventory_transaction.qty)`, maintained by the app in the same tx as each ledger write — do not edit directly. (Replaces the former `PNQty` column, dropped in schema v3.) |
| `part_attachment` | `id` | File/URL attachments. `part_id` → `part.id` (INT FK, enforced). `file_name` is path or URL — see [docs/conventions.md](../docs/conventions.md) for URL format rules. `category` = free-text document type label; options driven by `app_config.'attachment_categories'`. `sort_order` controls display order. Soft-delete only (`is_active=0`) — never hard-delete. Writes fire `trg_FIL_part_count`. (Renamed from `FIL` in db-table-rename commit 3; Go struct fields still use old `FIL`-prefixed names.) |
| `bom` | `id` | BOM / parts list. Links a parent part to child parts. `parent_part_id` → `part.id` (parent assembly). `component_part_id` → `part.id` (component part). `line_number` = user-assigned line item number. `qty` = quantity required. (Renamed from `PL` in db-table-rename commit 2; Go struct fields still use old `PL`-prefixed names.) |
| `company` | `id` | Suppliers, manufacturers, vendors. `is_supplier`/`is_manufacturer` flags distinguish roles. `default_contact` → `contact.id`. `SUNumOfLNKs`, `SUNumOfPOs` are denormalized counts maintained by DB triggers — do not update them in code. |
| `contact` | `id` | Contacts, linked to companies. `company_id` → `company.id`. `user_account_link` = Windows/network account for internal users. `is_active = 0` = inactive. (Renamed from `CN` in db-table-rename commit 1; Go struct fields still use old `CN`-prefixed names.) |
| `purchase_order` | `id` | Purchase orders (renamed from `PO` in db-table-rename commit 4). `supplier_id` = who PO goes to; `receiver_id` = bill/ship-to (both FK to `company.id`). PO number from sequence: `SELECT NEXT VALUE FOR dbo.PO_Number_Seq`. Writes fire `trg_PO_company_count`. `date_printed` is set automatically via `POST /po/{id}/mark-printed` — do not set it in create/update handlers. `status` (VARCHAR 20, NOT NULL) is authoritative and follows the lifecycle `draft` → `open` → `sent` → `partially_received` → `closed` (plus `cancelled`). `rfq` (issue #270) is a Request-for-Quotation state. RFQ quotes (one `purchase_order` per supplier) share `rfq_group_id` (anchored to the originating quote's own `id`; NULL for ordinary POs). Awarding (`RFQConvert`) **duplicates** the winning quote into a new PO at the bare base number (status `draft`, `rfq_group_id` NULL) and closes out the group — awarded quote → `closed`, the rest → `cancelled` — retaining every RFQ row and its history. A quote can also be declined individually (`rfq` → `cancelled`). The approval gate is bypassed while in `rfq`. **Numbering:** an RFQ group draws one `PO_Number_Seq` value; quotes are stored as `<base>R<n>` (e.g. `1050R1`, `1050R2`) and the awarded PO is created at bare `<base>` — so one PO number is consumed per RFQ regardless of supplier count (`number` is VARCHAR; nothing parses it as an int except `TRY_CAST` in `_test.sql`, which safely ignores the `R`-suffixed rows). The PO list hides RFQ-group rows behind a "Show RFQs" toggle. Status changes only via `POST /po/{id}/status`, which logs to `PO_status_history`; create/update handlers do not write `status`. `is_active` is a convenience bit kept in sync by the app (`draft/open/sent/partially_received → 1`, `closed/cancelled → 0`) — do not set it directly. `approval_status` (`not_submitted`/`pending`/`approved`/`rejected`, issue #267) gates sending/printing: a PO can only reach `sent` or be printed once `approved`; editing an approved/pending PO resets it to `not_submitted`. Run `SQL/migrations/migrate_po_status_lifecycle.sql`, then `migrate_po_approval.sql`, then `migrate_po_rfq.sql` to migrate existing databases. |
| `purchase_order_history` | `id` | Append-only PO activity log (issues #271 + #267; renamed from `PO_history` in db-table-rename commit 4). `po_id` → `purchase_order.id`. `event_type`: `status` (lifecycle transition: `from_status`→`to_status`, `from_status` NULL on creation) or `approval` (`action`: `submitted`\|`approved`\|`rejected`\|`reset`, with optional `note`). `changed_by` = app user username/login handle (written by the Go handler, not a trigger; consistent with `record_events.username` and `test_definition_history.changed_by`). |
| `po_line` | `id` | PO line items → `purchase_order.id` via `po_id`; `part_id` → `part.id`. `part_number_snapshot`/`revision_snapshot` denormalize the part at order time. `lead_time_days` (issue #270) = supplier's quoted lead time, captured per line on the RFQ comparison grid; NULL = not quoted. `received_qty`/`date_received` (issue #269) = cumulative qty received on the line and the date of the most recent receipt; each receipt also posts an `inventory_transaction` row (`txn_type='receipt'`). Writes fire `trg_POL_part_count`. (Renamed from `POL` in db-table-rename commit 5; Go struct fields still use old `POL`-prefixed names.) |
| `inventory_transaction` | `id` | Append-only stock-movement ledger (issues #272/#274). `part_id` → `part.id`. `txn_type`: `receipt`\|`issue`\|`adjustment`\|`count`. `qty` is **signed** (+ adds, − removes) so on-hand = `SUM(qty)`; `part.stock_on_hand` caches that sum (app-maintained). `username` = app login handle. `po_line_id` → `po_line.id` for receipts (#269), else NULL. `reference`/`note` = PO number / reason / comment. |
| `supplier_part` | `id` | Sourcing links — maps parts to supplier catalog entries. `supplier_id` → `company.id`, `part_id` → `part.id`, `mfg_part_id` → `mfg_part.id` (optional), `unit_id` → `unit.unit_id` (purchase unit; NULL = same as `part.unit_id`). Writes fire `trg_supplier_part_company_count`. |
| `mfg_part` | `id` | Manufacturer part numbers. `part_id` → `part.id`, `mfg_id` → `company.id`. `is_active = 0` = soft-deleted. Unique index is filtered on `is_active = 1` (allows re-adding an MPN after soft-delete). |
| `unit` | `unit_id` | Reference list of units of measure. `unit_type`: count/volume/length/mass/package. Base unit on `part.unit_id`; purchase unit on `supplier_part.unit_id`. |
| `price` | — | Quantity price breaks. |
| `form` | `id` | Test form definitions (renamed from `Forms` in db-table-rename commit 7; Go struct fields still use old names). `part_number_id` → `part.id`. `is_locked`/`is_active` flags. `test_order` = comma-separated `test_definition.id` list. `instrument_types` = comma-separated valid instrument types for this form; drives the Instrument Type dropdown on records. |
| `test_record` | `id` | A test run for one serial number (renamed from `TestRecords` in db-table-rename commit 7). `form_id` → `form.id`. `serial_number_pn`/`serial_number_pn_desc` denormalize the unit-under-test. `is_locked`/`is_active` flags. `test_order` = snapshot of order at record creation. `instrument_type` = free-text label matched against `test_definition.instrument_types` to filter applicable steps. |
| `test_definition` | `id` | Test step definitions. `type` = heading level (0=data, 1/2/3=heading). `hide_formula='HIDE'` hides data rows. `instrument_types` = comma-separated type names; step is hidden when record's `instrument_type` is not in this list (empty = show for all). Pass/fail uses `pf_type` + `ComputePassFail`. UPDATEs fire `trg_test_definition_history`. |
| `test_definition_history` | `id` | Audit trail for `test_definition`. One row per UPDATE, capturing the pre-update values. Populated by `trg_test_definition_history`; never written directly by the app. |
| `test_result` | `id` | One result per step per record (renamed from `TestResults` in db-table-rename commit 7). `record_id` → `test_record.id`, `test_id` → `test_definition.id`. `pass_fail` BIT. `result` = value or VBA image filename or `LOCAL:` path. |
| `users` | `id` | App-level user accounts. `username` = login handle (unique, case-insensitive in practice). `display_name` = shown in the header and written to audit events. `password_hash` = bcrypt. `is_active = 0` = soft-deactivated (cannot log in). `can_approve_po = 1` = may approve/reject POs (issue #267), toggled in Settings → Users. The app sets `CONTEXT_INFO` to `username` before trigger-firing writes so `trg_test_definition_history` records the app user instead of `SYSTEM_USER`. See issue #461 for the planned cleanup once all binaries carry F1. |
