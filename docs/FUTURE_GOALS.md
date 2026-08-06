# Arx — Future Goals & Roadmap

> **Purpose:** Guide future Claude sessions on long-term direction so that architectural and schema decisions point toward the end state, not away from it. Read this before proposing structural changes.

## Long-Term Vision

**Parts Master** evolves from a parts catalog + purchasing tool into a full engineering shop ERP:
- Parts catalog with compliance, revision history, alternate parts, and configurable custom fields
- Sourcing layer (AVL, preferred suppliers, lead times, manufacturer tracking)
- Full PO lifecycle: draft → approval → sent → receiving → closed
- Inventory on-hand tracking driven by receipts
- Reporting: spend, yield, cost rollups, dashboards

**Test Records** evolves from a VBA-parity viewer into a production data capture and quality analysis system:
- Full lock/unlock audit trail with reviewer approval workflow
- Form versioning with formal revisions
- Analytics: yield dashboard, failure mode reports, per-result change history
- UX polish: keyboard navigation, auto-save drafts, conditional step visibility

~~**Architecture end state:** A single merged executable serving both apps from one binary and one port, with user authentication. Until merger, the two apps should have a navigation link to each other (port swap).~~ *(done: one binary + one port + one package — #422)*

---

## Architecture Decisions Already Made

| Decision | Rationale |
|---|---|
| Single binary, `go:embed` templates/static | No supporting folders at runtime — copy .exe and run |
| `go-chi` router | Stay with chi for both apps; no framework migration |
| SQL Server only | `go-mssqldb`; no SQLite or Postgres consideration |
| `config/local.json` wins for DB config | DB password never in `.env`, never committed |
| `cfg.*Table()` for all table names | TEST_MODE switching is the only reason; simplifies once ArxDev DB exists |
| Soft-delete only for `FIL` | `is_active=0`; hard delete is off the table |
| DB triggers maintain denorm counts | `PNFILLinks`, `PNPOLinks`, `SUNumOfLNKs`, `SUNumOfPOs` — never update in Go code |
| `NULL` on `PNLastRollupCost` | NULL = no rollup ever run; 0 would mean rollup ran and cost was zero |
| Your own company as supplier for internal parts | MFG, RAW, and ASM parts get `price` rows with your company as `supplier_id`, unifying all part costs through the `price` table. For ASM parts the `price_ea` row represents value-add (labor, overhead) on top of the BOM rollup. Eliminates `PNCurrentCost` as a special field — it becomes a transitional fallback until all parts have `price` rows. |
| Records are fully materialized self-contained snapshots (#487, PR #489) | A saved `test_record` captures every renderable definition field into `test_result` at creation (spec, parameter, limits, units, pf_type, format, section headings). The live `test_definition` is never consulted to render or evaluate a saved record. Editing a form definition never changes existing records. "Update to latest" (`ResyncRecord`) is the only re-pull path. Record insert + materialization are wrapped in a single transaction (#490, PR #491). |

---

## Prerequisite / Foundational Work (Do These First)

These unlock multiple downstream features. Avoid implementing dependent features before prerequisites exist.

### 1. User Authentication (#207)
Required before: audit trails with real usernames (AUD-1, #251), PO approval (#267), reviewer approval (#249), re-open with reason (#250), SET CONTEXT_INFO for triggers.
- No auth framework decided yet; keep it simple (session cookie + single-user or small user table)
- `SET CONTEXT_INFO` before DB writes passes username to triggers

### 2. TEST_MODE → ArxDev database (#241)
Required before: Level 2/3 automated tests (#240), any new test infrastructure.
- Replace `_Test` table suffix with a DSN swap pointing to `ArxDev` database
- Simplifies `*Table()` helpers to just return the table name
- ~~`_test.sql` becomes a "populate ArxDev from prod" script~~ **Superseded by `SQL/azure/seed_test_data.sql` (issue #545): ArxDev is seeded with fixed synthetic data instead of a prod clone.**

### 3. Schema constraints (#213)
Many FK and NOT NULL constraints deferred at table creation. Should be applied incrementally before adding columns that depend on referential integrity. Verify no orphan/NULL rows before each constraint.

### ~~4. `Tests` table rename → `test_definition` (#215)~~
~~The name `Tests` is ambiguous alongside Go's `_test.go` convention. Rename early to avoid compounding rename debt. Affects both apps, `cfg.StepsTable()`, DDL files, CLAUDE.md.~~ **Done in PR #308.**

### 5. Least-privilege app DB login (#751)
Required before: named queries can be considered safe against arbitrary data access (#719 epic). The current `isSafeQuery` keyword blacklist ([named_query.go:20](../arx_go/named_query.go#L20)) is defense-in-depth only — real fix is a SQL Server login with `SELECT`-only rights on the tables named queries are allowed to touch, no cross-DB access to `ArxProd`, no `OPENROWSET`. #625-adjacent: revisit alongside the Postgres dialect work since the login/grants model may need to be re-derived per-engine.

### 6. `price.is_preferred` flag (#223)
Required before: BOM cost rollup UI (ENG-3 #280, RPT-4 #285). The current `PN.price_id` pointer is stale and unmanaged; replace with `is_preferred BIT` + filtered unique index.

Once `is_preferred` exists and all parts (including MFG/RAW/ASM via your own company as supplier) have `price` rows, `PNCurrentCost` becomes fully redundant and can be dropped. Migration path: backfill `price` rows from `PNCurrentCost` values, verify rollup results match, then `ALTER TABLE PN DROP COLUMN PNCurrentCost`.

---

## Parts Master — Feature Roadmap

### Tier 1: Parts Catalog Quality
*No new tables; extends existing PN/LNK/price schema.*

| Issue | Feature | Schema touch |
|---|---|---|
| #281 ENG-4 | Custom field labels for PNUser1-10 | `app_config` entries |
| #278 ENG-1 | Alternate / substitute parts | New `part_alternate` table |
| #279 ENG-2 | Part revision history (ECO log) | New `eco_log` table |
| #83 | Reconsider part types (DWG/PS/CAT…) | PN.PNType values only |
| #220 | POL: capture part revision at order time | `POLRev` (column exists, unwritten) |

### Tier 2: Sourcing & Supplier
*Extends LNK and supplier tables; price.is_preferred is prerequisite for rollup.*

| Issue | Feature | Schema touch |
|---|---|---|
| #275 SUP-1 | AVL attributes on LNK (preferred, MOQ, lead time, pack size, approval status) | `LNK` new columns |
| #225 | LNK: wire up LNKMFRID / LNKMFRPNID (manufacturer sourcing) | `LNK` FK wiring |
| #222 | `price_type` (standard / qty-break / pack) | `price` new column |
| #223 | `price.is_preferred` (**prerequisite for rollup**) | `price` new column + index |
| — | Your company as supplier for internal parts (MFG/RAW/ASM) — enables unified cost model via `price` table; `PNCurrentCost` becomes fallback-only | `company` row + `price` rows; no schema change |
| #9 | Lead time per sourcing link (`LNKLeadtime` exists as VARCHAR, migrate to DECIMAL) | `LNK` column type change |
| #276 SUP-2 | Supplier performance metrics (OTD %, avg lead time) | Requires PO-2 receiving dates |
| #277 SUP-3 | Lead time tracking per PO line (promised vs actual) | `POL` new column; requires PO-2 |

### Tier 3: PO Lifecycle
*Sequential dependencies: PO-1 → PO-4 → PO-2 → INV-1.*

| Issue | Feature | Schema touch | Depends on |
|---|---|---|---|
| #267 PO-1 | PO approval workflow (Draft/Pending/Approved/Rejected) | `PO` approval fields | Auth (#207) |
| #271 PO-4 | PO status lifecycle (Draft→Open→Sent→Partial→Closed→Cancelled) | `PO` status + timestamps | — |
| ~~#269 PO-2~~ | ~~Receiving / goods receipt (received qty + date per POL line)~~ ✓ | `po_line.received_qty` + `date_received` | PO-4 |
| #270 PO-3 | Request for Quotation (RFQ → compare → convert to PO) | New `rfq` / `rfq_line` tables | PO-4 |
| #224 | Auto-name PO file, open folder, auto-create if missing | No schema change (already has folder logic) | — |
| #156 | Import files from part into PO folder | No schema change | — |

### Tier 4: Inventory
*INV-1 is the foundation; INV-2 and INV-3 build on it.*

| Issue | Feature | Schema touch | Depends on |
|---|---|---|---|
| #272 INV-1 | Stock on hand per part | New `stock` table or `PN` field | PO-2 (for auto-update) |
| #273 INV-2 | Min/max / reorder points | `PN` min/max fields | INV-1 |
| #274 INV-3 | Inventory transaction log (receipt/issue/adjustment) | New `inventory_transaction` table | INV-1 |
| #7 | Inventory tracking (lot/batch records, date counted) | Extends INV-3 | INV-3 |

### Tier 5: Reporting & Data
*Most reports query existing tables; no new schema unless noted.*

| Issue | Feature | Depends on |
|---|---|---|
| #280 ENG-3 | Cost roll-up UI (Run Rollup button, delta from last purchase) | `price.is_preferred` (#223) |
| #285 RPT-4 | BOM cost summary tree | ENG-3 |
| #282 RPT-1 | Dashboard / home screen (KPI tiles, recent activity) | INV-2 for reorder tile |
| #283 RPT-2 | Spend analysis report (by supplier, by part, date range) | — |
| #284 RPT-3 | Price history chart (SVG/canvas, cost over time) | — |
| #286 DAT-1 | CSV export (parts, POs, BOM) | — |
| #287 DAT-2 | CSV import for parts (preview, dry-run, commit) | — |
| #288 DAT-3 | Barcode / QR label generation (PDF, server-side) | — |

### Tier 6: Compliance & Audit
*Requires auth for meaningful usernames.*

| Issue | Feature | Depends on |
|---|---|---|
| #289 AUD-1 | Audit / change log (entity_type, old/new value, user, timestamp) | Auth (#207) |
| #290 AUD-2 | RoHS / REACH / conflict minerals compliance flags on PN | — |

---

## Test Records — Feature Roadmap

### Tier 1: VBA Parity Gaps
*Features the VBA app had that the Go app doesn't. Low schema impact.*

| Issue | Feature |
|---|---|
| ~~#248~~ | ~~Lock/unlock record from UI~~ |
| ~~#201~~ | ~~Lock/unlock form from UI~~ |
| ~~#200~~ | ~~TestRecordHistory writes on lock/unlock (audit written to `form_events`/`record_events`)~~ |
| #208 | Image gallery view (VBA had tiled grid; Go has hover-preview only) |
| ~~#209~~ | ~~`format` field applied to result inputs~~ |
| ~~#203~~ | ~~`applicable_instrs` filter (SN-based row filtering, as `instrument_type`)~~ |
| ~~#199~~ | ~~`pf_formula` deprecated~~ |
| #202 | AutoFill on Update (auto-populate result from spec_nom query) |

### Tier 2: Record Lifecycle & Audit
*Sequential: lock UI → audit → unlock reason → reviewer approval.*

| Issue | Feature | Depends on |
|---|---|---|
| ~~#248~~ | ~~Lock/unlock UI~~ | ~~—~~ |
| ~~#200~~ | ~~TestRecordHistory audit writes~~ | ~~#248~~ |
| #250 | Re-open requires logged reason | #248, #200 |
| #249 | Reviewer approval (two-step sign-off) | Auth (#207) |
| #251 | Per-result change history (`TestResultHistory` table) | — |

### Tier 3: Form Definition Features
*Extends `test_definition`/`Forms` schema.*

| Issue | Feature | Schema touch |
|---|---|---|
| #260 | Formal revision numbers on Forms (Rev A/B/C) | `Forms.revision`, `TestRecords.form_revision` |
| #261/256 | Required test steps (block lock if incomplete) | `test_definition.required` BIT |
| #262/257 | Conditional step visibility (`show_if` expression) | `test_definition.show_if` VARCHAR |
| ~~#210~~ | ~~`hide_formula` dynamic expressions (extends HIDE/SHOW)~~ | ~~done in 0.3.53~~ |
| ~~#255~~ | ~~Clone/duplicate a form definition~~ | ~~No schema change~~ |
| #221 | Custom worksheet tab support (future, VBA concept) | `Forms` config field TBD |

### Tier 4: Record UX
*No schema changes.*

| Issue | Feature |
|---|---|
| #263/258 | Keyboard navigation between result inputs (Tab/Enter) |
| #264/259 | Auto-save draft to localStorage, restore on return |
| #254 | Clone a test record (with RMA/Upgrade type preset) |
| #253 | Bulk lock from record list |
| #247 | Advanced filters on record list (date range, type, pass/fail) |
| #246 | Global search across all forms (serial number, PN, date) |

### Tier 5: Analytics & Export
*No new tables unless noted.*

| Issue | Feature | Depends on |
|---|---|---|
| #242 | PDF export of a single test record | — |
| #204 | PDF/print export (same as #242, consolidate) | — |
| #243 | CSV export of record list | — |
| #244 | Pass/fail yield summary per form | — |
| #245 | Failure mode report (top failing steps) | — |
| #252 | Yield dashboard across all forms | — |

---

## Cross-Cutting Concerns

### App Merge vs. Cross-App Navigation (#218, #234)
~~**Long-term goal:** Merge both executables into one binary on one port.~~ *(done — #413)*
~~**Interim:** Add a navigation tab/link in each app pointing to the other app's port.~~ *(done — header cross-links are now same-origin)*
Design new features with merger in mind — avoid deeply embedding port-specific assumptions into the nav or config.

### User Authentication (#207)
No authentication exists yet. When implemented:
- Use `SET CONTEXT_INFO` before each DB write to pass username to triggers
- `TestRecordHistory.username` and `test_definition_history.changed_by` should switch from `SYSTEM_USER` to app-level user
- Simple session cookie with a small user table is sufficient — avoid OAuth complexity unless specifically requested

### TEST_MODE → ArxDev DB (#241)
Current `_Test` table suffix is confusing alongside Go's `_test.go` convention.
**Plan:** Separate `ArxDev` database on the same SQL Server instance. `TEST_MODE` becomes a DSN swap. `*Table()` helpers simplify to just the table name. This is a prerequisite for writing Level 2 (httptest) and Level 3 (integration) tests (#240).

### Automated Test Expansion (#240)
Current state: Level 1 pure unit tests in place for `arxlib`, `parts_master_go/handlers`, `parts_master_go/models`.
Next:
- **Level 2:** `net/http/httptest` handler tests — requires TEST_MODE wired to ArxDev and CSRF disabled in test mode
- **Level 3:** Integration tests — full DB round-trips against ArxDev; validates triggers and sequences

### ~~`Tests` Table Rename (#215)~~
~~Rename `Tests` → `test_definition`, `Tests_Test` → `test_definition_Test`.~~ **Done.**

### Serial Number Migration (#214)
`TestRecords.serial_number` is `VARCHAR(64)` but all active records appear numeric. Migrate to `INT` after verifying: `SELECT * FROM TestRecords WHERE TRY_CAST(serial_number AS INT) IS NULL AND active = 1`. The named query `recent_serial_numbers_for_form` already uses `TRY_CAST` in anticipation.

---

## Schema Evolution Notes

- `POLRev VARCHAR(10)` — exists, unwritten. Wire up when #220 is implemented.
- `LNKLeadtime VARCHAR(55)` — exists, migrate to `DECIMAL(7,2)` when #9 is implemented.
- `LNKMFRID INT`, `LNKMFRPNID INT` — exist, unwired. Use as FKs to `supplier.id` and `PN.PNID` for #225.
- `PN.price_id` — stale pointer; replace with `price.is_preferred` (#223) then drop.
- `PN.PNCurrentCost` — transitional field. Once `price.is_preferred` (#223) is in place and all parts (BUY, MFG, RAW, ASM) have `price` rows with your company as supplier for internal parts, `PNCurrentCost` is redundant. Rollup and pricing should fall back to it until then, but new features should not extend its use. Drop after migration is verified.
- `price_type` — was dropped; re-add when #222 is implemented.
- `Forms.custom_sheets` — was dropped; only re-add if custom worksheet feature is scoped (#221).
- Part categories (code, label, per-category subtab visibility) are stored as JSON in `app_config['part_categories']`, editable in Settings (#345). `PN.category` is a free-text string matched against this list; `models.DefaultCategories()` is the seed when nothing is saved.
- ~~`pf_formula` on `test_definition` — loaded but unevaluated; decide evaluate-or-drop (#199) before #262 adds more step evaluation logic.~~ **#199 done: `pf_formula` deprecated in 0.3.52; `#262` can proceed.**

---

## Schema Cleanup (no issue yet)

- **Drop `company.SUWeb` and `company.SUContact1`** — confirmed dead columns; no Go or VBA references. `SUWeb` superseded by `company_attachment`; `SUContact1` superseded by `default_contact` FK to CN. Migration: `ALTER TABLE company DROP COLUMN SUWeb; ALTER TABLE company DROP COLUMN SUContact1;`. Verify no orphan references before running.

## Test Records — Code Quality (no issue yet)

- **Debug fields cleanup** (`test_records_go/models/models.go` `TestStep` struct, and the matching query in `records.go` ~838): fields `ArchiveID`, `Revision`, `Category`, `SheetName` are still loaded but only needed during form-def authoring. Remove when the edit UI stabilises and those fields are no longer needed client-side.
- **Records index query refactor** (`records.go` ~413): the `RecordsIndex` SQL query is verbose inline SQL; candidate for a SQL view or stored proc once the schema stabilises.
- ~~**Serial number sequence** (`records.go` ~1213): `MAX(serial_number)+1` is not safe under concurrent record creates for the same form. Replace with a per-form sequence/counter table.~~ (Done #369 — atomic in-transaction re-derive with UPDLOCK/HOLDLOCK.)

---

*Last updated: 2026-06-24 — added record snapshot materialization to Architecture Decisions (#487 PR #489, #490 PR #491); milestone table reconciled (v0.4.x/v0.5 milestones deleted from repo).*
