# SQL Schema Conventions

ER diagram: [schema_diagram.md](schema_diagram.md)

## Go-forward convention (new tables)

All new tables use snake_case. Do not extend the legacy prefix style for new work.

### Tables
- Singular noun: `purchase_order`, not `purchase_orders`
- All lowercase snake_case: `supplier_attachment`, `part_number`
- `_Test` sibling required for every table the Go app touches (see [Test-mode table variants](#test-mode-table-variants))

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
CREATE TABLE supplier_attachment (
    supplier_attachment_id  INT           PRIMARY KEY IDENTITY,
    supplier_id             INT           NOT NULL,     -- FK → supplier.supplier_id (once supplier is migrated)
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
| `LNK` | `LNK` | Part-supplier links |
| `CN`  | `CN`  | Contacts |
| `PL`  | `PL`  | Parts list / BOM |
| `POL` | `POL` | PO line items |

**Go-era tables** — lowercase snake_case:

| Table | Notes |
|-------|-------|
| `supplier` | Mixed: has both generic columns and leftover `SU`-prefixed columns |
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

## Test-mode table variants

Every table used by the Go app must have a `_Test` sibling created alongside it.
`cfg.*Table()` helpers in `config/config.go` switch between prod and test names at runtime.
Never hardcode a table name in Go — always call the helper.

| Prod | Test |
|------|------|
| `PN` | `PN_Test` |
| `FIL` | `FIL_Test` |
| `supplier_attachment` | `supplier_attachment_Test` |
| `PL` | `PL_Test` |
| `PO` / `POL` | `PO_Test` / `POL_Test` |
| `supplier` | `supplier_Test` |
| `LNK` | `LNK_Test` |
| `CN` | `CN_Test` |
| `price` | `price_Test` |
| `app_config` | `app_config_Test` |

## Triggers

Trigger DDL lives in `SQL/triggers.sql`. `_Test` table equivalents are created inside `SQL/_test.sql`.

| Trigger | Table | Effect |
|---------|-------|--------|
| `trg_LNK_supplier_count` | `LNK` | Recalculates `supplier.SUNumOfLNKs` after any INSERT/UPDATE/DELETE |
| `trg_PO_supplier_count` | `PO` | Recalculates `supplier.SUNumOfPOs` after any INSERT/UPDATE/DELETE |
| `trg_FIL_part_count` | `FIL` | Recalculates `PN.PNFILLinks` (active rows only) after any INSERT/UPDATE/DELETE |
| `trg_POL_part_count` | `POL` | Recalculates `PN.PNPOLinks` after any INSERT/UPDATE/DELETE |
| `trg_LNK_Test_supplier_count` | `LNK_Test` | Same as `trg_LNK_supplier_count`, targeting `supplier_Test` |
| `trg_PO_Test_supplier_count` | `PO_Test` | Same as `trg_PO_supplier_count`, targeting `supplier_Test` |
| `trg_FIL_Test_part_count` | `FIL_Test` | Same as `trg_FIL_part_count`, targeting `PN_Test` |
| `trg_POL_Test_part_count` | `POL_Test` | Same as `trg_POL_part_count`, targeting `PN_Test` |

These fire for all writers (Go app and VBA). Do not update `SUNumOfLNKs`, `SUNumOfPOs`, `PNFILLinks`, or `PNPOLinks` manually in application code.

## DDL file conventions

- One `.sql` file per table, in `SQL/`.
- Scripts are **reference DDL**, not migration runners — the app does not execute them automatically.
- Use `IF OBJECT_ID('dbo.TableName', 'U') IS NOT NULL DROP TABLE dbo.TableName;` at the top.
- Keep the file in sync with schema changes made directly to the live DB.
- Add `TODO:` comments for known drift between the script and the live schema.
