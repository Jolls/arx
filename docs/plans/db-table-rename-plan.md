# DB Table/Column Rename to Schema Convention

## Context

The DB schema was just changed in a non-backwards-compatible way (inventory core dropped `PN.PNQty`, schema bumped to v3, requires `migrate_inventory_core.sql`). That break already forces a coordinated DB change, so this is the moment to also rename the legacy-named tables/columns to match the go-forward convention in `SQL/schema.md` (singular snake_case tables, snake_case columns, `is_` booleans) — paying the migration cost once instead of inventing a future break. **Users are down for the whole effort**, so there is no live-compatibility or staged-deploy concern — we just need each step to build and test locally.

**Decisions locked (from planning Q&A):**
- **Depth:** rename tables **+ columns + PKs** (full DB-side modernization).
- **Scope:** all four groups — legacy parts (`PN`,`FIL`,`CN`,`PL`), `POL`, PO family (`PO`,`PO_history`), test-records PascalCase (`Forms`,`TestRecords`,`TestResults`, plus `Parameter`/`Specification` casing).
- **Rollout:** **one feature branch / one PR, one commit per table/group.** Build + test after each commit (easier to observe/isolate). One `CHANGELOG.md` entry for the PR. No per-step schema-version bump, no deploy, no user-facing binary — `build.bat` to compile/test only.
- **Go side:** **DB + SQL query strings only.** Leave Go struct fields and templates unchanged.
- **PK/FK convention (Rails-style), documented in `schema.md`:** **every PK is bare `id`.** **Every FK is `{stem}_id` referencing that table's `id`.** This overrides the current schema.md rule ("PK = `{table}_id`, never bare `id`") — which is already contradicted in practice (`PO.id`, `company.id`, `price.id`, `supplier_part.id`, `mfg_part.id` all use bare `id`; the test tables use `ID`). It's the lowest-churn option: only the legacy outliers (`PNID`,`FILID`,`CNID`,`PLID`,`POLID`) and the uppercase test `ID`s change; most go-era PKs are already `id`. FK stems: `part_number`→`part_id`, `purchase_order`→`po_id`, `part_attachment`→`attachment_id`, plus already-established `form_id`/`record_id`/`test_id`. Existing FKs (`PO_history.po_id`, `supplier_part.part_id`, `test_result.test_id`, etc.) already match and need no change.

## The central execution rule (read first)

Reads are **positional** (`rows.Scan(&a,&b,…)` — confirmed: no sqlx, no struct tags, no `MapScan`). Therefore:

- Renaming a DB column requires editing **only the column token inside the SQL query string** (the `SELECT … / INSERT … / UPDATE … / WHERE … / JOIN …` text). It does **not** change `.Scan(...)` order, Go struct fields, or templates.
- **Consequence / risk:** Go field names stay the same (`Part.PNID`, `.FILFileName`) even though the DB column becomes `id` / `file_name`. So a token like `PNID` appears in **two** contexts that must be treated differently:
  - inside an SQL string (`SELECT PNID …`) → **rename**
  - as a Go identifier (`&p.PNID`, `{{.PNID}}`) → **leave alone**
  - **Do not do a blanket find/replace.** Edit query strings surgically.
- **No compile-time or `go test` safety net for column typos** — queries are runtime strings and unit tests don't hit a DB. The only nets are integration tests (ArxDev) and manual clickthrough with `DEBUG_MODE=true`. This is why per-commit test points below are mandatory, not optional.

## Per-commit mechanics (same shape every commit)

1. **DDL file** (`SQL/<table>.sql`): update `CREATE TABLE` name, column names, PK/FK/UQ names.
2. **Migration script** (`SQL/migrate_rename_<table>.sql`, idempotent): `EXEC sp_rename 'dbo.OLD','NEW'` for the table, then `EXEC sp_rename 'dbo.NEW.OldCol','NewCol','COLUMN'` per column. (`sp_rename` keeps FKs/indexes/triggers attached automatically.) Run it by hand against the dev DB to test.
3. **Triggers** (`SQL/triggers.sql` for FIL/POL/PN; `trg_test_definition_history` for test group): `sp_rename` does **not** rewrite trigger bodies — any trigger whose body references a renamed table/column must be re-emitted via `CREATE OR ALTER TRIGGER` with the new names.
4. **Config** (`arxlib/config/config.go`): change the `*Table()` helper's returned string (lines 126–152).
5. **Go query strings** (handlers): rename column tokens inside SQL strings only — files listed per commit below.
6. **`SQL/_test.sql`**: update DROP + `SELECT * INTO` to new table name, re-emit triggers + denormalized recalc with new column names (ArxDev copies the renamed schema from prod via `SELECT *`).
7. **Docs:** update `SQL/schema.md` table reference + `SQL/schema_diagram.md` as you go. **One-time (first or last commit): rewrite `schema.md`'s convention section to "PK = bare `id`; FK = `{stem}_id`".** One `CHANGELOG.md` line for the whole PR.

> No `app_config` `schema_version` bump and no `ExpectedSchemaVersion` change this effort — users are down, so the startup mismatch gate isn't needed. (If `CheckSchemaVersion` would block local startup against the renamed dev DB, just keep the existing version value consistent so it passes.)

## Recommended commit order (ascending blast radius)

All on one branch; each row is a commit. Children can be renamed before the hub: a join like `part_attachment.part_id = PN.PNID` works even with mismatched names, so ordering is for risk-rehearsal, not correctness.

| # | Commit | Why this slot | Trigger? |
|---|----|---------------|----------|
| 1 | `CN` → `contact` | Warm-up: establishes the migration+config+_test+docs pattern, no triggers | no |
| 2 | `PL` → `bom` | Small, self-contained (BOM views) | no |
| 3 | `FIL` → `part_attachment` | Introduces trigger handling | `trg_FIL_part_count` |
| 4 | `PO`+`PO_history` → `purchase_order`(+`_history`) | Columns already snake; mainly table rename | (`trg_PO_company_count` body) |
| 5 | `POL` → `po_line` | Adjacent to PO (shares the PO FK) | `trg_POL_part_count` |
| 6 | `PN` → `part_number` | The hub — largest query-string footprint; do once pattern is rehearsed | both PN triggers' bodies |
| 7 | Test-records group | Separate subsystem; tight internal FK web | `trg_test_definition_history` |

---

## Commit 1 — `CN` → `contact`

**Rename map** (`SQL/Contacts.sql`):

| Old | New | | Old | New |
|---|---|---|---|---|
| CNID (PK) | id | | CNWeb | website |
| CNSUID (FK→company.id) | company_id | | CNEmail | email |
| CNName | display_name | | CNActive | is_active |
| CNAddress | address | | CNUserAccountLink | user_account_link |
| CNCity | city | | CNDateModified | updated_at |
| CNState | state | | CNNotes | notes |
| CNZipcode | zipcode | | CNPhone1/CNPhone2 | phone_1 / phone_2 |
| CNCountry | country | | CNFAX | fax |

**Incoming FK:** `company.default_contact` → `contact.id` (update join strings; `company.default_contact` column name unchanged this commit).
**Go query strings:** `contacts.go`, `pos.go` (supplier-contact dropdown), `api.go` (`/api/suppliers/{id}/contacts`), `suppliers.go` (default-contact join), `settings.go` (default-contact dropdown).
**No triggers.**
**Test points:** `/contacts` (list) → `/contact/{id}` (detail) → `/contact/{id}/edit` → save; create via `/contacts/new`; open a supplier `/supplier/{id}` (default-contact display); `/po/{id}/edit` supplier+receiver contact dropdowns populate; Settings default-contact dropdown.

## Commit 2 — `PL` → `bom`

**Rename map** (`SQL/parts_list.sql`):

| Old | New |
|---|---|
| PLID (PK) | id |
| PLListID (FK→PN, parent) | parent_part_id |
| PLPartID (FK→PN, component) | component_part_id |
| PLItem | line_number |
| PLQty | qty |

**Go query strings:** `parts.go` (BOM list, where-used, BOM edit/save, cost rollup), `records.go` (one PL↔PN join). Watch the `HasBOM` `EXISTS(SELECT 1 FROM PL …)` subquery and the rollup SELECT.
**No triggers.**
**Test points:** `/part/{id}/bom` (list), `/part/{id}/bom/edit` → save a line, `/part/{id}/where-used`, `POST /part/{id}/rollup-cost` (recomputes from BOM).

## Commit 3 — `FIL` → `part_attachment`

**Rename map** (`SQL/FIL.sql`):

| Old | New |
|---|---|
| FILID (PK) | id |
| FILPNID (FK→PN) | part_id |
| FILFileName | file_name |
| FILPNRev | part_revision |
| category | category (unchanged) |
| order_id | sort_order |
| is_active | is_active (unchanged) |

**Trigger:** `trg_FIL_part_count` body references `FIL`, `FILPNID`, `is_active`, `PN.PNFILLinks` — re-emit with new table/column names. **Preserve `WHERE is_active=1`** (counts active rows only). Also the denormalized recalc block at the bottom of `triggers.sql` and `_test.sql`.
**Cross-ref:** `PN.PNFILIDPrimary` points at `FIL.FILID` (the denorm primary-attachment id) — that PN column is renamed in commit 6, not here; update the `setPrimaryAttachment` query string's table/PK token only.
**Go query strings:** `parts.go` (attachments list/create/update/delete, set-primary), `integration_test.go` (FIL soft-delete + count assertions).
**Test points:** `/part/{id}/attachments` → add a file, edit it, set primary, soft-delete it; confirm the count badge updates (trigger). Run `go test -tags integration` against ArxDev for `PartCreateAndAttach`.

## Commit 4 — `PO` + `PO_history` → `purchase_order` + `purchase_order_history`

Columns are already snake_case and PKs are already bare `id` — so this commit is **almost purely a table rename** (`PO`→`purchase_order`, `PO_history`→`purchase_order_history`). No PK change. `PO_history.po_id` (FK→`PO.id`) already matches the `po` stem and is unchanged.

| Table | Old | New |
|---|---|---|
| `PO` → `purchase_order` | id (PK) | id (unchanged) |
| `PO_history` → `purchase_order_history` | id (PK) | id (unchanged) |
| `PO_history` | po_id (FK→purchase_order.id) | po_id (unchanged) |

**Inbound FK whose join strings reference `PO.id`:** `POL.POLPOID` (renamed in commit 5). Confirm during commit 3 that `FIL.order_id`→`sort_order` was a *display* order, not a PO reference.
**Go query strings:** `pos.go` (detail, create, status transition, approval action, history fetch — `PO`/`PO_history` table tokens only), `parts.go` (`PartOrders` POL↔PO join).
**Triggers:** `trg_PO_company_count` (on `PO` writes, touches `company.SUNumOfPOs` + `PO.supplier_id`) stays attached through `sp_rename`, but re-emit its body so it reads `purchase_order` — verify.
**Test points:** `/pos` (list), `/po/{id}` (detail + history panel), create via `/pos/new`, `POST /po/{id}/status` (status event logged), `POST /po/{id}/approval` (approve/reject → approval event + `approval_status`), `/po/{id}/print`, `/po/{id}/duplicate`.

## Commit 5 — `POL` → `po_line`

**Rename map** (`SQL/po_items.sql`):

| Old | New |
|---|---|
| POLID (PK) | id |
| POLPOID (FK→purchase_order.id) | po_id |
| POLPNID (FK→part_number.id, nullable) | part_id |
| POLPNPartNumber | part_number_snapshot |
| POLRev | revision_snapshot |
| POLItem | line_number |
| POLDesc | description |
| POLQty | qty |
| POLCost | unit_cost |
| VendorPN | vendor_part_number |

**Trigger:** `trg_POL_part_count` → re-emit; **counts ALL rows** (no `is_active` filter, unlike FIL). Update the denorm recalc for `PN.PNPOLinks` too.
**Inbound FK:** `inventory_transaction.po_line_id` → `po_line.id` (update join string; column name already snake).
**Go query strings:** `pos.go` (line CRUD, vendor-PN/price-history selects, detail). Note: the form-field parser `case "POLItem":` maps **POST keys**, not DB columns — those are template-driven names and stay (templates aren't touched). `parts.go` (`PartOrders`).
**Test points:** `/po/{id}/edit` → add/edit/delete a line, save; `/po/{id}` line table renders; `/po/{id}/print`; `/part/{id}/orders` (part order history + count badge from trigger). Integration test for `trg_POL_part_count`.

## Commit 6 — `PN` → `part_number` (the hub)

Note: table `part_number` will contain a column also named `part_number` (the human-readable string) — legal, just be precise.

**Rename map** (`SQL/part_number.sql`) — already-snake columns (`part_number`,`category`,`has_bom`,`revision`,`title`,`detail`,`release_status`,`user_field_1-10`,`price_id`,`stock_on_hand`) stay:

| Old | New | | Old | New |
|---|---|---|---|---|
| PNID (PK) | id | | PNCurrentCost | current_cost |
| PNReqBy | requested_by | | active | is_active |
| PNNotes | notes | | PNPOLinks (denorm) | po_line_count |
| PNDate | created_date | | PNDateModified | modified_date |
| PNLastRollupCost | last_rollup_cost | | PNUNID (FK→unit) | unit_id |
| PNLastRollupAt | last_rollup_at | | PNFILIDPrimary (denorm) | primary_attachment_id |
| PNFILLinks (denorm) | attachment_count | | | |

**FK columns in children already named `part_id`** (`supplier_part`, `mfg_part`, `price`, `inventory_transaction`) reference `part_number.id` and stay correct — only the join-string table/PK token (`PN.PNID` → `part_number.id`) changes. Legacy children were renamed to the `part_id` stem in their own commits (FIL.part_id, po_line.part_id, bom.parent/component_part_id).
**Triggers:** both `trg_FIL_part_count` and `trg_POL_part_count` write `PN.PNFILLinks`/`PNPOLinks` — re-emit bodies to target `part_number.attachment_count`/`po_line_count` and join `part_id`. Update `_test.sql` recalc.
**Cross-DB (no FK):** `Forms.PNID` / `TestRecords.part_number_id` reference `PN.PNID` logically across DBs — no constraint, but note for commit 7 docs.
**Go query strings:** widest footprint — `parts.go`, `inventory.go`, `pos.go`, `records.go`, `suppliers.go`, `sourcing.go`, `api.go`, `integration_test.go`, `handlers_test.go`. Every `PNID`/`PN…` token inside an SQL string.
**Test points:** full part clickthrough — `/` list, `/part/{id}` all tabs (Details, BOM, Where-Used, Attachments, Orders, Transactions, Pricing, MFG, Suppliers), `/part/{id}/edit` → save, create part, `POST /part/{id}/adjust-stock` (stock_on_hand cache), `POST /part/{id}/rollup-cost`. Run full integration suite against ArxDev.

## Commit 7 — Test-records group → `form` / `test_record` / `test_result`

Tightly coupled by FKs + the `Parameter`/`Specification` casing fix that spans three tables — keep as one commit.

PKs all become bare `id` (the uppercase `ID` → `id`). Existing FK stems (`form_id`, `record_id`, `test_id`) already fit and don't change.

| Table | Old PK | New PK | Other renames |
|---|---|---|---|
| `Forms` → `form` | ID | id | PNID→part_number_id; locked→is_locked; active→is_active |
| `TestRecords` → `test_record` | ID | id | serial_number_PN→serial_number_pn; serial_number_PNDesc→serial_number_pn_desc; locked→is_locked; active→is_active |
| `TestResults` → `test_result` | ID | id | Parameter→parameter; Specification→specification |
| `test_definition` (table unchanged) | id | id | Parameter→parameter; Specification→specification |
| `test_definition_history` (unchanged) | id | id | Parameter→parameter; Specification→specification |

**FKs (join-string target only — column names unchanged):** `test_definition.form_id`→`form.id`; `form_events.form_id`→`form.id`; `record_events.test_record_id`→`test_record.id`; `test_result.record_id`→`test_record.id`; `test_result.test_id` + `test_definition_history.test_id`→`test_definition.id`.
**Trigger:** `trg_test_definition_history` body references `Parameter`/`Specification` in its INSERT + `DELETED` selection — re-emit lowercased. (Historical note in DDL: a prior trigger silently rolled back after a column rename — verify this trigger compiles against the new casing.)
**Named queries** (`named_queries` rows in `SQL/TestRecords.sql`): `pos_for_pn`, `recent_serial_numbers_for_form`, `max_subbatch_result` embed table/column names in their stored SQL — update those seed strings.
**Go query strings:** `records.go` (forms/records/results CRUD, the `Parameter`/`Specification` columns), `render_tr.go`. Any case-normalization that coped with capitalized `Parameter`/`Specification` can be simplified once columns are lowercase — but only if clearly dead; leave it otherwise.
**Test points (templates in `templates/tr/`):** `/records` (forms list), `/forms/{id}/records` and create a record, `/forms/{id}/def` + `/forms/{id}/def/edit` → save (writes `test_definition` + history; exercises Parameter/Specification), `/records/{id}` detail + `/records/{id}/edit` → save results (Parameter/Specification scan), lock/unlock a form and a record (`form_events`/`record_events`), `/api/forms/{id}/def/history`.

---

## Global verification (every commit)

1. `cd arx_go && .\build.bat` (PowerShell) — runs `go test ./...` + builds. Catches Go compile issues, **not** SQL column typos.
2. Apply that commit's `migrate_rename_*.sql` to **ArxDev** by hand; re-run `SQL/_test.sql` if repopulating.
3. Run `Arx.exe` with `TEST_MODE=true` + `DEBUG_MODE=true` (SQL logged to terminal) and click the commit's test points; a missed column shows as a runtime SQL error in the log.
4. For trigger commits (3, 5, 6): `go test -tags integration ./arx_go/...` with `ARX_TEST_DSN` pointing at ArxDev.
5. Grep audit before committing: search the SQL files + touched `.go` query strings for any lingering old token **inside SQL string literals** (ignore Go identifiers/templates, which intentionally keep old names).

## Risks / call-outs

- **Divergence by design:** Go field `PNID` will map to DB column `id` (and `FILFileName`→`file_name`, etc.). Acceptable per the chosen approach (smallest diff), but document it once in `SQL/schema.md` so future devs know Go names ≠ DB names for legacy tables. A later optional commit could realign Go field names if desired.
- **No compile-time net for SQL strings** — manual test points + integration tests are the safety net; do not skip them.
- **Form POST field keys** (e.g. `pol[{{.POLID}}][POLItem]` in templates, parsed by `case "POLItem":` in Go) are template/handler contract names, **not** DB columns — they stay unchanged since templates aren't touched.
- **Constraint name tidiness** (PK/FK/UQ names containing old abbreviations) is optional; functional correctness doesn't require renaming them, but doing so in the same migration keeps `schema.md` honest.
- **schema.md convention rewrite:** the old "PK = `{table}_id`, never bare `id`" rule is being **replaced** with "PK = bare `id`; FK = `{stem}_id`". Record the new rule and the stem list (`part`, `po`, `attachment`, plus existing `form`/`record`/`test`) so future tables follow it; the prior wording is now wrong and must be removed, not appended to.
