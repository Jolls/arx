# SQL Schema Conventions

ER diagram: [schema_diagram.md](schema_diagram.md)

## Go-forward convention (new tables)

All new tables use snake_case. Do not extend the legacy prefix style for new work.

### Tables
- Singular noun: `purchase_order`, not `purchase_orders`
- All lowercase snake_case: `company_attachment`, `inventory_transaction`
- New tables must exist in both prod and `ArxDev` (add DDL to `SQL/<table>.sql` and a seed block to `SQL/seed_test_data.sql`, then re-run it)

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

## Reference test data

`SQL/seed_test_data.sql` wipes all row data in `ArxDev` and loads a small, fixed, synthetic
dataset (issue #545) — not a prod clone. Every reference record has a pinned ID so manual
verification steps, `/verify`, and integration tests can refer to them directly instead of
re-discovering suitable data each session. Re-run the script any time to reset ArxDev to this
known state; identities are reseeded above each range afterward so ad hoc rows created during
testing never collide with the reference set.

The `app_config.company_logo` value is seeded separately, via `SQL/seed_company_logo.sql` —
run it after `seed_test_data.sql` (which clears `app_config` on every run). It's split out
because the value is a large base64 data URI that would swamp `seed_test_data.sql`'s diff.

| ID range | Table | What's there |
|---|---|---|
| 1001-1099 | `company` | 4 companies: supplier, supplier+manufacturer, receiver/ship-to, manufacturer-only; both suppliers have a `default_contact` |
| 2001-2099 | `contact` | Sibling contacts at the same supplier (2001 fully populated: address/website/notes), one per other supplier incl. the receiver company (2005), plus a soft-deleted (`is_active=0`) contact |
| 3001-3099 | `part` | One part per category that matters (RAW, BUY, MFG, ASM, OPS, FORM) plus release-status coverage — an `A`ctive set, one `U`nder-review (3008), one `D`eprecated + inactive (3009); FORM parts 3010 (carries form 6001) and 3011 (spare, for new-form creation); ASM sub-assembly 3012 nested inside 3005's BOM, with its own `last_rollup_cost` (#579) |
| 3901-3999 | `bom` | Assembly 3005 built from 3002 + 3003, plus an OPS labor line (3006) so the cost rollup includes value-add (#465), plus sub-assembly 3012 as a 4th line so the BOM view's expand/collapse toggle and "Rollup" cost-source badge have something to exercise (#579); 3012 has its own BOM (3001 + 3007); 3904 lists unit-under-test 3004 under FORM part 3010 (NewRecord PN picker) |
| 4001-4099 | `supplier_part` | Sourcing links, incl. one (4002) with an `mfg_part_id`, a purchase unit (REEL) ≠ the part's base unit, and `min_increment`/`lead_time` |
| 4101-4199 | `mfg_part` | One active MPN plus a soft-deleted one (exercises the filtered unique index) |
| 4201-4299 | `price` | Active + superseded (history) price rows |
| 5001-5099 | `purchase_order` | One PO per status (`draft`/`open`/`partially_received`/`closed`/`cancelled`), a resolved RFQ group (5006/5007→awarded 5008), and an in-flight `rfq` group (5010/5011); financials on the open/closed POs; 5002/5003 carry full supplier/receiver address + contact snapshot blocks for PO print |
| 5501-5599 | `po_line` | Line items across the above POs, incl. a partial receipt and RFQ-quote lines carrying `lead_time_days` |
| 5801-5899 | `purchase_order_history` | Status + approval events on PO 5002 |
| 5901-5999 | `inventory_transaction` | Receipt/issue/adjustment/count ledger driving `part.stock_on_hand` for part 3007 |
| 6001-6099 | `form` | One locked, released test form (FORM part 3010, unit-under-test 3004) |
| 6101-6199 | `test_definition` | A heading, range-checked data steps, an archived/retired step (6104, still rendered on the historical records that recorded it), and feature steps 6105-6108: `pf_type` filled/comment, `format`, `default_result`, `List:`/`query:` spec_nom pickers, a `{6102}` cross-step token, and a `hide_formula` (6103 is also updated post-insert so `test_definition_history` has a timeline row) |
| 6201-6299 | `form_events` | Release (`locked`) audit event for the form |
| 7001-7099 | `test_record` | WIP / Complete / Approved records against the form, a soft-deleted one, and 7005 — locked pre-#251 style (completed event, no snapshots) so the backfill bulk action appears |
| 7101-7199 | `test_result` | Materialized rows (headings included) for each record x step, incl. a FAIL result on 7005 |
| 7201-7299 | `record_events` | `completed` events on the locked records; 7003 has a full lock → unlock → re-lock history (its two snapshots differ, driving the diff view); 7005's completed event has no snapshots (backfillable) |
| 7301-7399 | `record_event_results` | Frozen result rows for the `completed` snapshots — 7003's two snapshots differ in one value |
| 8001-8099 | `users` | `admin`/`admin` (PO + record approver) and `tester`/`tester` (no approvals) — working bcrypt hashes, ArxDev only |
| 8101-8199 | `part_attachment` | URL-only attachments (no real files needed): 8101 on part 3002 with a `comment` (#585), 8102 on part 3004 with none |
| 1-17 | `unit` | Reference list of units of measure |
| (identity) | `app_config`, `named_queries` | App config: schema version, attachment categories, and the `spec_nom` auto-fill query library |

No `company_attachment` rows are seeded (would require real files/URLs on companies too).
`logs` and `release_notes` are cleared and left empty. `test_definition_history` gets one
trigger-written row (the seed updates step 6103 after insert to exercise the history timeline).

## Triggers

Trigger DDL lives in `SQL/triggers.sql`. These fire identically in ArxDev, since ArxDev shares the exact same schema (bare table names, no `_Test` suffix) — only row data differs, seeded by `SQL/seed_test_data.sql`.

| Trigger | Table | Effect |
|---------|-------|--------|
| `trg_supplier_part_company_count` | `supplier_part` | Recalculates `company.SUNumOfLNKs` after any INSERT/UPDATE/DELETE |
| `trg_PO_company_count` | `purchase_order` | Recalculates `company.SUNumOfPOs` after any INSERT/UPDATE/DELETE |
| `trg_FIL_part_count` | `part_attachment` | Recalculates `part.attachment_count` (active rows only) after any INSERT/UPDATE/DELETE |
| `trg_POL_part_count` | `po_line` | Recalculates `part.po_line_count` after any INSERT/UPDATE/DELETE |
| `trg_test_definition_history` | `test_definition` | Snapshots old row values into `test_definition_history` AFTER UPDATE (audit trail). Defined in `SQL/triggers.sql` alongside the count-maintenance triggers. |

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
| `part` | `id` | Parts catalog (renamed from `PN` in commit 6, then `part_number`→`part` in commit 8; the `part_number` column keeps its name; Go struct fields still use old `PN`-prefixed names). `release_status`: U/A/D, `NOT NULL` default `'U'` (Under Review) — issue #542. `is_active` (was `active`). `user_field_1-10` = configurable fields. `attachment_count` (was `PNFILLinks`) maintained by `trg_FIL_part_count`, `po_line_count` (was `PNPOLinks`) by `trg_POL_part_count` — do not update either in code. `category` is constrained by `CK_part_number_category` to `'' / ASM / BUY / DWG / DOC / FORM / MFG / OPS / RAW / SVC / TOOL`; `OPS` (issue #465) is an operation/labor line whose `current_cost` is an hourly rate, added to an assembly's BOM with `qty = hours`. `last_rollup_cost` is `DECIMAL(16,8) NULL` (NULL = no rollup run). `default_supplier_id` (issue #465) → `company.id` (FK deferred, like `price_id`) = the preferred supplier whose cheapest active `price` row feeds the cost rollup as this part's leaf cost (falls back to `current_cost` when NULL); auto-pinned to the first supplier a price is added for. `unit_id` → `unit.unit_id` (base/inventory unit). `primary_attachment_id` → `part_attachment.id`. `stock_on_hand` (issue #272) is a cached inventory balance = `SUM(inventory_transaction.qty)`, maintained by the app in the same tx as each ledger write — do not edit directly. (Replaces the former `PNQty` column, dropped in schema v3.) |
| `part_attachment` | `id` | File/URL attachments. `part_id` → `part.id` (INT FK, enforced). `file_name` is path or URL — see [docs/conventions.md](../docs/conventions.md) for URL format rules. `category` = free-text document type label; options driven by `app_config.'attachment_categories'`. `comment` (#585) = free-text note per attachment, independent of `category`. `sort_order` controls display order. Soft-delete only (`is_active=0`) — never hard-delete. Writes fire `trg_FIL_part_count`. (Renamed from `FIL` in db-table-rename commit 3; Go struct fields still use old `FIL`-prefixed names.) |
| `bom` | `id` | BOM / parts list. Links a parent part to child parts. `parent_part_id` → `part.id` (parent assembly). `component_part_id` → `part.id` (component part). `line_number` = user-assigned line item number. `qty` = quantity required. (Renamed from `PL` in db-table-rename commit 2; Go struct fields still use old `PL`-prefixed names.) |
| `company` | `id` | Suppliers, manufacturers, vendors. `is_supplier`/`is_manufacturer` flags distinguish roles. `default_contact` → `contact.id`. `SUNumOfLNKs`, `SUNumOfPOs` are denormalized counts maintained by DB triggers — do not update them in code. |
| `contact` | `id` | Contacts, linked to companies. `company_id` → `company.id`. `user_account_link` = Windows/network account for internal users. `is_active = 0` = inactive. (Renamed from `CN` in db-table-rename commit 1; Go struct fields still use old `CN`-prefixed names.) |
| `purchase_order` | `id` | Purchase orders (renamed from `PO` in db-table-rename commit 4). `supplier_id` = who PO goes to; `receiver_id` = bill/ship-to (both FK to `company.id`). `supplier_contact_id`/`receiver_contact_id` (issue #597) → `contact.id` (nullable, enforced FKs) link the chosen contacts so a contact's detail page can list its POs; the `supplier_contact`/`receiver_contact` name snapshots are retained and remain authoritative for print. PO number from sequence: `SELECT NEXT VALUE FOR dbo.PO_Number_Seq`. Writes fire `trg_PO_company_count`. `date_printed` is set automatically via `POST /po/{id}/mark-printed` — do not set it in create/update handlers. `status` (VARCHAR 20, NOT NULL) is authoritative and follows the lifecycle `draft` → `open` → `sent` → `partially_received` → `closed` (plus `cancelled`). `rfq` (issue #270) is a Request-for-Quotation state. RFQ quotes (one `purchase_order` per supplier) share `rfq_group_id` (anchored to the originating quote's own `id`; NULL for ordinary POs). Awarding (`RFQConvert`) **duplicates** the winning quote into a new PO at the bare base number (status `draft`, `rfq_group_id` NULL) and closes out the group — awarded quote → `closed`, the rest → `cancelled` — retaining every RFQ row and its history. A quote can also be declined individually (`rfq` → `cancelled`). The approval gate is bypassed while in `rfq`. **Numbering:** an RFQ group draws one `PO_Number_Seq` value; quotes are stored as `<base>R<n>` (e.g. `1050R1`, `1050R2`) and the awarded PO is created at bare `<base>` — so one PO number is consumed per RFQ regardless of supplier count (`number` is VARCHAR; nothing parses it as an int except `TRY_CAST` in `SQL/seed_test_data.sql`'s `PO_Number_Seq` restart, which safely ignores the `R`-suffixed rows). The PO list hides RFQ-group rows behind a "Show RFQs" toggle. Status changes only via `POST /po/{id}/status`, which logs to `PO_status_history`; create/update handlers do not write `status`. `is_active` is a convenience bit kept in sync by the app (`draft/open/sent/partially_received → 1`, `closed/cancelled → 0`) — do not set it directly. `approval_status` (`not_submitted`/`pending`/`approved`/`rejected`, issue #267) gates sending/printing: a PO can only reach `sent` or be printed once `approved`; editing an approved/pending PO resets it to `not_submitted`. Run `SQL/migrations/migrate_po_status_lifecycle.sql`, then `migrate_po_approval.sql`, then `migrate_po_rfq.sql`, then `migrate_po_contact_id.sql` to migrate existing databases. |
| `purchase_order_history` | `id` | Append-only PO activity log (issues #271 + #267; renamed from `PO_history` in db-table-rename commit 4). `po_id` → `purchase_order.id`. `event_type`: `status` (lifecycle transition: `from_status`→`to_status`, `from_status` NULL on creation) or `approval` (`action`: `submitted`\|`approved`\|`rejected`\|`reset`, with optional `note`). `changed_by` = app user username/login handle (written by the Go handler, not a trigger; consistent with `record_events.username` and `test_definition_history.changed_by`). |
| `po_line` | `id` | PO line items → `purchase_order.id` via `po_id`; `part_id` → `part.id`. `part_number_snapshot`/`revision_snapshot` denormalize the part at order time. `lead_time_days` (issue #270) = supplier's quoted lead time, captured per line on the RFQ comparison grid; NULL = not quoted. `received_qty`/`date_received` (issue #269) = cumulative qty received on the line and the date of the most recent receipt; each receipt also posts an `inventory_transaction` row (`txn_type='receipt'`). Writes fire `trg_POL_part_count`. (Renamed from `POL` in db-table-rename commit 5; Go struct fields still use old `POL`-prefixed names.) |
| `inventory_transaction` | `id` | Append-only stock-movement ledger (issues #272/#274). `part_id` → `part.id`. `txn_type`: `receipt`\|`issue`\|`adjustment`\|`count`. `qty` is **signed** (+ adds, − removes) so on-hand = `SUM(qty)`; `part.stock_on_hand` caches that sum (app-maintained). `username` = app login handle. `po_line_id` → `po_line.id` for receipts (#269), else NULL. `reference`/`note` = PO number / reason / comment. |
| `supplier_part` | `id` | Sourcing links — maps parts to supplier catalog entries. `supplier_id` → `company.id`, `part_id` → `part.id`, `mfg_part_id` → `mfg_part.id` (optional), `unit_id` → `unit.unit_id` (purchase unit; NULL = same as `part.unit_id`). Writes fire `trg_supplier_part_company_count`. |
| `mfg_part` | `id` | Manufacturer part numbers. `part_id` → `part.id`, `mfg_id` → `company.id`. `is_active = 0` = soft-deleted. Unique index is filtered on `is_active = 1` (allows re-adding an MPN after soft-delete). |
| `unit` | `unit_id` | Reference list of units of measure. `unit_type`: count/volume/length/mass/package. Base unit on `part.unit_id`; purchase unit on `supplier_part.unit_id`. |
| `price` | — | Quantity price breaks. |
| `form` | `id` | Test form definitions (renamed from `Forms` in db-table-rename commit 7; Go struct fields still use old names). `part_number_id` → `part.id`. `is_locked`/`is_active` flags. `test_order` = comma-separated `test_definition.id` list. `instrument_types` = comma-separated valid instrument types for this form; drives the Instrument Type dropdown on records. `revision` = integer count of times this form has been released (locked); bumped on every unlock→lock transition, never on save; 0 = never released ("Draft") (#260). |
| `form_events` | `id` | Audit trail for state changes on `form` (locked, unlocked, archived, activated, etc.). `form_id` → `form.id`. `username`: OS username pre-#207, real app username from #207 (cutover 2026-06-19, v0.5.24) onward. Pre-cutover rows carrying legacy/garbage identifiers were audited and normalized where identifiable — see #630. |
| `test_record` | `id` | A test run for one serial number (renamed from `TestRecords` in db-table-rename commit 7). `form_id` → `form.id`. `serial_number_pn`/`serial_number_pn_desc` denormalize the unit-under-test. Three-state lifecycle via `is_locked` + `is_approved` (#249): WIP (`is_locked=0`), Complete (`is_locked=1, is_approved=0`, any user may set/unlock), Approved (both `=1`, only a TR reviewer — `users.can_approve_records` — may set/unlock). `is_active` = soft-delete flag. `test_order` = snapshot of order at record creation. `instrument_type` = free-text label matched against `test_definition.instrument_types` to filter applicable steps. `form_revision` = snapshot of `form.revision` at record creation, capturing the form's last-released revision even if the record was made against a draft (unlocked) form; NULL for records created before #260. |
| `record_events` | `id` | Audit trail for state changes on `test_record` (locked, unlocked, archived, activated, etc.). `test_record_id` → `test_record.id`. `username`: OS username pre-#207, real app username from #207 (cutover 2026-06-19, v0.5.24) onward. Pre-cutover rows carrying legacy/garbage identifiers were audited and normalized where identifiable — see #630. |
| `test_definition` | `id` | Test step definitions. `type` = heading level (0=data, 1/2/3=heading). `hide_formula='HIDE'` hides data rows. `archived` BIT (default 0) retires a step: hidden from new records and the live definition view, still rendered on historical records that have a result for it (the supported replacement for the `HIDE` workaround). `instrument_types` = comma-separated type names; step is hidden when record's `instrument_type` is not in this list (empty = show for all). Pass/fail uses `pf_type` + `ComputePassFail`. UPDATEs fire `trg_test_definition_history`. |
| `test_definition_history` | `id` | Audit trail for `test_definition`. One row per UPDATE, capturing the pre-update values. Populated by `trg_test_definition_history`; never written directly by the app. `changed_by`: OS/DB login (`SYSTEM_USER`) pre-#207, real app username via `CONTEXT_INFO` from #207 (cutover 2026-06-19, v0.5.24) onward. Pre-cutover rows carrying legacy/garbage identifiers were audited and normalized where identifiable — see #630. |
| `test_result` | `id` | One result per step per record (renamed from `TestResults` in db-table-rename commit 7). `record_id` → `test_record.id`, `test_id` → `test_definition.id`. `pass_fail` BIT. `result` = value or VBA image filename or `LOCAL:` path. Snapshot columns (`parameter`, `specification`, `spec_units`, `spec_min`, `spec_nom`, `spec_max`, `pf_type`, `format`, `type`, `hide_formula`, `default_result`) freeze the definition as it was when the record was created — saved records render and evaluate P/F entirely from these, not the live `test_definition` (#487). Rows are **materialized at record creation** for every applicable step (incl. headings, `type` > 0); self/record/form tokens are baked in, `{id}` cross-step tokens resolve at render against the record's own results. "Update to latest" re-pulls the live definition. |
| `record_event_results` | `id` | Per-lock result snapshot (#251). One row per data result captured when a record is completed, linked to the `completed` row in `record_events` via `event_id`. `test_id` → `test_definition.id`; `parameter`, `specification`, and `spec_units` are resolved snapshots frozen at completion (tokens already baked in) so the row renders faithfully without a join and survives later resyncs — same freeze rationale as `test_result` (#487). Written by the Go handler in the same tx as the `completed` event (single + bulk lock) and by the one-time backfill bulk action on the form records page; never updated after insert. Rows are inserted in frozen-row display order so the snapshot renders by `id`. The record detail view diffs each snapshot against the previous `completed` snapshot (added/changed/unchanged by `test_id`). Run `SQL/migrations/migrate_record_event_results.sql` to add the table to existing databases. |
| `users` | `id` | App-level user accounts. `username` = login handle (unique, case-insensitive in practice). `display_name` = shown in the header and written to audit events. `password_hash` = bcrypt. `is_active = 0` = soft-deactivated (cannot log in). `can_approve_po = 1` = may approve/reject POs (issue #267), toggled in Settings → Users. `can_approve_records = 1` = may approve test records and unlock approved ones (issue #249), toggled in Settings → Users. `default_po_contact_id` / `default_po_receiver_id` (both nullable, no FK) = per-user PO defaults edited on the Settings → My Preferences tab; when set they pre-fill the receiver/contact on that user's new POs/RFQs, else the fields start blank (issue #463). Run `SQL/migrations/migrate_users_po_defaults.sql` to add these columns to existing databases. The app sets `CONTEXT_INFO` to `username` before trigger-firing writes so `trg_test_definition_history` records the app user. |
