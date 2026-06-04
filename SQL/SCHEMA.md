# SQL Schema Conventions

ER diagram: [schema_diagram.md](schema_diagram.md)

## Go-forward convention (new tables)

All new tables use snake_case. Do not extend the legacy prefix style for new work.

### Tables
- Singular noun: `purchase_order`, not `purchase_orders`
- All lowercase snake_case: `company_attachment`, `part_number`
- New tables must exist in both prod and `ArxDev` (re-run `SQL/_test.sql` to populate)

### Columns
- All lowercase snake_case
- Primary key: `{table_name}_id` — e.g. `supplier_attachment_id` (never bare `id`)
- Foreign key: same name as the PK it references — e.g. `supplier_id INT` pointing at `supplier.supplier_id`
- Booleans: `is_` prefix — `is_active`, `is_locked` (not `active`, `locked`)
- Timestamps: `created_at`, `updated_at` (DATETIME, DEFAULT GETDATE())
- Avoid SQL reserved words as column names: `name`, `date`, `type`, `order`, `value`, `key`
  — use `display_name`, `order_date`, `record_type`, etc.

### Example

```sql
CREATE TABLE company_attachment (
    supplier_attachment_id  INT           PRIMARY KEY IDENTITY,
    supplier_id             INT           NOT NULL,     -- FK → company.id
    file_path               NVARCHAR(1024) NOT NULL,    -- LOCAL:... path or https:// URL
    notes                   NVARCHAR(512),
    sort_order              INT,
    created_at              DATETIME      DEFAULT GETDATE(),
    updated_at              DATETIME      DEFAULT GETDATE()
);
```

### What to do with legacy tables
- Do not rename existing legacy columns — too much churn, the Go app scans by column name.
- New columns added to legacy tables should still use the legacy prefix style to stay consistent within that table.
- When a legacy table is fully replaced/migrated, use the go-forward convention for the replacement.

---

## Legacy conventions (existing tables)

Two eras of tables exist. When extending or mirroring a legacy table, follow its existing style.

### Table naming

Two eras of tables exist in this schema. Follow the era of the table you are extending or parallel-ing.

**Legacy tables** (VBA/Ruby era) — uppercase short abbreviation:

| Table | Abbreviation | Notes |
|-------|-------------|-------|
| `PN`  | `PN`  | Part numbers |
| `FIL` | `FIL` | File/URL attachments (to parts) |
| `supplier_part` | — | Sourcing links (migrated from `LNK`) |
| `mfg_part` | — | Manufacturer part numbers |
| `CN`  | `CN`  | Contacts |
| `PL`  | `PL`  | Parts list / BOM |
| `POL` | `POL` | PO line items |

**Go-era tables** — lowercase snake_case:

| Table | Notes |
|-------|-------|
| `company` | Mixed: has both generic columns and leftover `SU`-prefixed columns |
| `PO` | Clean snake_case |
| `price` | Clean snake_case |

## Column naming

**Legacy tables** prefix every column with the table abbreviation:

```
{TABLE_ABBREV}ID          -- primary key (e.g. PNID, FILID, CNID)
{TABLE_ABBREV}{TargetABBREV}ID  -- foreign key (e.g. FILPNID → PN, LNKSUID → supplier)
{TABLE_ABBREV}ColumnName  -- all other columns (e.g. FILFileName, LNKVendorPN)
```

Exception: `order_id` in `FIL` and `SUFIL` breaks the prefix rule (TODO: should be `FILOrder` / `SUFILOrder`).

**Go-era tables** use generic snake_case: `id`, `name`, `is_active`, `date_modified`, `supplier_id`.

## Test mode

`TEST_MODE=true` points the DSN at the `ArxDev` database instead of prod. Table names are
identical in both databases — prod vs test is a database-level distinction, not a name suffix.
`cfg.*Table()` helpers return bare names (`PN`, `PO`, etc.) regardless of test mode.
Never hardcode a table name in Go — always call the helper.

## Triggers

Trigger DDL lives in `SQL/triggers.sql`. ArxDev equivalents are recreated by `SQL/_test.sql` under the same names (no `_Test` suffix — ArxDev uses bare table names).

| Trigger | Table | Effect |
|---------|-------|--------|
| `trg_supplier_part_company_count` | `supplier_part` | Recalculates `company.SUNumOfLNKs` after any INSERT/UPDATE/DELETE |
| `trg_PO_company_count` | `PO` | Recalculates `company.SUNumOfPOs` after any INSERT/UPDATE/DELETE |
| `trg_FIL_part_count` | `FIL` | Recalculates `PN.PNFILLinks` (active rows only) after any INSERT/UPDATE/DELETE |
| `trg_POL_part_count` | `POL` | Recalculates `PN.PNPOLinks` after any INSERT/UPDATE/DELETE |
| `trg_test_definition_history` | `test_definition` | Snapshots old row values into `test_definition_history` AFTER UPDATE (audit trail). |

> **Dropped trigger:** `trg_Tests_history` was a legacy AFTER UPDATE trigger on `test_definition` created when the table was named `Tests`. It referenced the old column `applicable_instrs` (since renamed to `instrument_types`), silently rolling back every UPDATE once the rename was applied. It was dropped in v0.4.1 and superseded by `trg_test_definition_history`.

These fire for all writers (Go app and VBA). Do not update `SUNumOfLNKs`, `SUNumOfPOs`, `PNFILLinks`, or `PNPOLinks` manually in application code.

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
| `PN` | `PNID` | Parts catalog. `release_status`: U/A/D. `user_field_1-10` = configurable fields. `PNFILLinks` maintained by `trg_FIL_part_count`, `PNPOLinks` by `trg_POL_part_count` — do not update either in code. `PNLastRollupCost` is `DECIMAL(16,8) NULL` (NULL = no rollup run). `PNUNID` → `unit.unit_id` (base/inventory unit). |
| `FIL` | `FILID` | File/URL attachments. `FILPNID` → `PN.PNID` (INT FK, enforced). `FILFileName` is path or URL — see [docs/conventions.md](../docs/conventions.md) for URL format rules. `category` = free-text document type label; options driven by `app_config.'attachment_categories'`. `order_id` controls sort. Soft-delete only (`is_active=0`) — never hard-delete. Writes fire `trg_FIL_part_count`. |
| `PL` | — | BOM / parts list. Links a parent part to child parts. |
| `company` | `id` | Suppliers, manufacturers, vendors. `is_supplier`/`is_manufacturer` flags distinguish roles. `default_contact` → `CN.CNID`. `SUNumOfLNKs`, `SUNumOfPOs` are denormalized counts maintained by DB triggers — do not update them in code. |
| `CN` | `CNID` | Contacts, linked to companies. |
| `PO` | `id` | Purchase orders. `supplier_id` = who PO goes to; `receiver_id` = bill/ship-to (both FK to `company.id`). PO number from sequence: `SELECT NEXT VALUE FOR dbo.PO_Number_Seq`. Writes fire `trg_PO_company_count`. `date_printed` is set automatically via `POST /po/{id}/mark-printed` — do not set it in create/update handlers. `status` (VARCHAR 20, NOT NULL) is authoritative: `pending` \| `placed` \| `complete` \| `cancelled` \| `on_hold`. `is_active` is a convenience bit kept in sync by the app (`pending/placed/on_hold → 1`, `complete/cancelled → 0`) — do not set it directly. Run `SQL/migrations/migrate_po_status.sql` to add the column to existing databases. |
| `POL` | `POLID` | PO line items → `PO.id`. |
| `supplier_part` | `id` | Sourcing links — maps parts to supplier catalog entries. `supplier_id` → `company.id`, `part_id` → `PN.PNID`, `mfg_part_id` → `mfg_part.id` (optional), `unit_id` → `unit.unit_id` (purchase unit; NULL = same as `PN.PNUNID`). Writes fire `trg_supplier_part_company_count`. |
| `mfg_part` | `id` | Manufacturer part numbers. `part_id` → `PN.PNID`, `mfg_id` → `company.id`. `is_active = 0` = soft-deleted. Unique index is filtered on `is_active = 1` (allows re-adding an MPN after soft-delete). |
| `unit` | `unit_id` | Reference list of units of measure. `unit_type`: count/volume/length/mass/package. Base unit on `PN.PNUNID`; purchase unit on `supplier_part.unit_id`. |
| `price` | — | Quantity price breaks. |
| `Forms` | `ID` | Test form definitions. `PNID` → `PN`. `test_order` = comma-separated `test_definition.id` list. `instrument_types` = comma-separated valid instrument types for this form; drives the Instrument Type dropdown on records. |
| `TestRecords` | `ID` | A test run for one serial number. `form_id` → `Forms.ID`. `test_order` = snapshot of order at record creation. `instrument_type` = free-text label matched against `test_definition.instrument_types` to filter applicable steps. |
| `test_definition` | `id` | Test step definitions. `type` = heading level (0=data, 1/2/3=heading). `hide_formula='HIDE'` hides data rows. `instrument_types` = comma-separated type names; step is hidden when record's `instrument_type` is not in this list (empty = show for all). Pass/fail uses `pf_type` + `ComputePassFail`. UPDATEs fire `trg_test_definition_history`. |
| `test_definition_history` | `id` | Audit trail for `test_definition`. One row per UPDATE, capturing the pre-update values. Populated by `trg_test_definition_history`; never written directly by the app. |
| `TestResults` | `ID` | One result per step per record. `pass_fail` BIT. `result` = value or VBA image filename or `LOCAL:` path. |
