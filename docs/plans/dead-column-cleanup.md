# Plan: dead-column cleanup (architecture review item 2, closes #31)

Part of the batch branch (#19 → dead-column → #192 → #194 → #191). The PR body says `Closes #31`, with a note that the derived Go field `IsLotTracked` is kept on purpose (it's computed from `tracking_mode` and used in many templates); only the column, the lockstep writes and the stale comments go. Don't touch `SQL/azure/`.

## Open questions (recommendations assumed below)
1. **New column names.** `SUSupplierCode`→`supplier_code`, `SUNotes`→`notes`, `SUNumOfLNKs`→`supplier_part_count`, `SUNumOfPOs`→`po_count` (the thing counted + `_count`, as `attachment_count` / `po_line_count`).
2. **Rename Go struct fields and HTML form fields too.** Go fields `SupplierCode`, `Notes`, `SupplierPartCount`, `POCount`; form fields `supplier_code` and `notes`. The red grep test (R1) assumes this.
3. **Bump `schema_version` 10 → 11** (dropped/renamed columns are breaking per `internal/config/config.go`).
4. **CLAUDE.md** migration rules only describe `SQL/azure/migrations`. Recommended: one line pointing to `SQL/postgres/migrations/` and SCHEMA.md#migrations. Left out until approved.

## Findings that shape the plan
- Postgres folds the unquoted legacy names to lowercase (`sunotes`, `sunumoflnks` and so on). The migration uses lowercase names.
- **Postgres stores trigger function bodies as text, and renaming a column doesn't rewrite them.** The migration must `CREATE OR REPLACE` both company-count trigger functions in the same transaction as the rename. Otherwise every later insert into `supplier_part` or `purchase_order` fails.
- `TestMigrationsSelfRegister` (`arx_go/migrations_lint_test.go`) only reads `SQL/azure/migrations`, and its regexes need `dbo.` and `VALUES (…, 1)`. On Postgres, `schema_migrations.is_applied` is a BOOLEAN, so the insert has to use `TRUE`. The test needs extending.
- The filename pattern `^\d{14}_\d+_[a-z0-9_]+\.sql$` requires an issue number, so the file is named `…_31_dead_column_cleanup.sql`.
- `pos.go:1064` (POPrint) and `pos.go:2138` (createPOFolder) look up the supplier code and ignore the `Scan` error. A missed rename there would fail silently, and neither path is covered by integration tests today. R1 and C3 cover this.
- Adding `company.notes` doesn't make any existing query ambiguous. Every Go reference to an unqualified `notes` is either a single-table query or qualified (`cn.notes`), and no seeded named query joins `company`.
- Neither dropped table has a `*Table()` helper or any Go reference; in-app release notes come from the embedded `RELEASE_NOTES.md`. The Settings backup doesn't export `logs` or `release_notes`. Its `company.csv` and `part.csv` column headers will change.
- Unrelated drift, not touched here: `SQL/postgres/app_config.sql` seeds `schema_version` `'8'`, while the seed file uses `'10'`.

## 1. Migration (new file)
`SQL/postgres/migrations/<YYYYMMDDHHMMSS at authoring>_31_dead_column_cleanup.sql`. Postgres syntax, idempotent, a human runs it with `psql -v ON_ERROR_STOP=1 -d ArxDev -f <file>`. Order:
1. Header comment: #31 / architecture review item 2; breaking (schema_version 10→11); pinned to ArxDev (a human edits the guard for ArxProd); run with `ON_ERROR_STOP`.
2. `BEGIN;`
3. ArxDev pin: `DO $$ BEGIN IF lower(current_database()) <> 'arxdev' THEN RAISE EXCEPTION 'Pinned to ArxDev (connected to %); edit this guard to run elsewhere', current_database(); END IF; END $$;`
4. `CREATE TABLE IF NOT EXISTS schema_migrations (…)`, identical to `SQL/postgres/schema_migrations.sql`. This is the first Postgres migration, so the ledger may not exist yet.
5. Preview `SELECT id, name, sql FROM named_queries WHERE sql ~* '\m(suweb|sucontact1|is_lot_tracked|logs|release_notes)\M';` with a comment saying these reference dropped objects and must be fixed by hand.
6. `ALTER TABLE company DROP COLUMN IF EXISTS suweb;` then the same for `sucontact1`. `ALTER TABLE part DROP COLUMN IF EXISTS is_lot_tracked;` `DROP TABLE IF EXISTS logs;` `DROP TABLE IF EXISTS release_notes;`
7. One `DO $$ … $$` block. For each pair (`sunotes`→`notes`, `susuppliercode`→`supplier_code`, `sunumoflnks`→`supplier_part_count`, `sunumofpos`→`po_count`): `IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'company' AND column_name = '<old>') THEN ALTER TABLE company RENAME COLUMN <old> TO <new>; END IF;`
8. `CREATE OR REPLACE FUNCTION trg_supplier_part_company_count()` and `trg_PO_company_count()`, copied verbatim from the edited `SQL/postgres/triggers.sql` (step 3). The `CREATE TRIGGER` statements don't change.
9. Patch stored queries, once per renamed column: `UPDATE named_queries SET sql = regexp_replace(sql, '\m<old>\M', '<new>', 'gi') WHERE sql ~* '\m<old>\M';`
10. `UPDATE app_config SET setting_value = '11' WHERE setting_key = 'schema_version' AND setting_value = '10';`
11. Last step, registration: `INSERT INTO schema_migrations (version_id, is_applied) SELECT <ts>, TRUE WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version_id = <ts>);`
12. `COMMIT;`

## 2. Migration lint (`arx_go/migrations_lint_test.go`)
- Add these variables next to the existing ones:
  - ``pgMigrationGuard = regexp.MustCompile(`(?i)FROM\s+schema_migrations\s+WHERE\s+version_id\s*=\s*(\d+)`)``
  - ``pgMigrationInsert = regexp.MustCompile(`(?i)INSERT\s+INTO\s+schema_migrations\s*\(version_id,\s*is_applied\)\s*SELECT\s+(\d+),\s*TRUE`)``
- Wrap the current body of `TestMigrationsSelfRegister` in a loop over `{dir, guard, insert}`: `{../SQL/azure/migrations, migrationGuard, migrationInsert}` and `{../SQL/postgres/migrations, pgMigrationGuard, pgMigrationInsert}`. Run each as a `t.Run` named `azure` or `postgres`. Keep a separate `seen` map per directory. The body logic stays as is, but uses the loop's `dir`, `guard` and `insert` values. `legacyMigrations` is unchanged.
- Update the test's doc comment to mention both directories.

## 3. Postgres reference DDL and seed
- `SQL/postgres/company.sql`:
  - Header comment: replace `SUNumOfLNKs and SUNumOfPOs` with `supplier_part_count and po_count`.
  - Delete the `SUWeb` and `SUContact1` lines.
  - Rename `SUNotes`→`notes`, `SUNumOfLNKs`→`supplier_part_count`, `SUNumOfPOs`→`po_count` and `SUSupplierCode`→`supplier_code`. Keep each column's position, type and comment. Add `-- folder stub for supplier/PO folders` to the `supplier_code` line.
- `SQL/postgres/part.sql`:
  - Delete line 44 (`is_lot_tracked`).
  - Change the `tracking_mode` comment on line 47 to `-- Lot/serial control (#743); authoritative since #745 (is_lot_tracked dropped, #31).`
- `SQL/postgres/lot.sql:5`: change `part.is_lot_tracked gates` to `part.tracking_mode (lot/lot_serial) gates`.
- `SQL/postgres/purchase_order.sql:6` and `SQL/postgres/supplier_part.sql:2`: change the comments to `po_count` and `supplier_part_count`.
- `SQL/postgres/triggers.sql`:
  - Header lines 4–5: use the new column names.
  - Delete the "Mixed-case count columns…" paragraph (lines 23–24).
  - Section comments on lines 31 and 72: use the new names.
  - `SET SUNumOfLNKs` (lines 36, 40, 44) becomes `SET supplier_part_count`. `SET SUNumOfPOs` (lines 77, 81, 85) becomes `SET po_count`.
  - The recalibration block (lines 229–230) uses the new names.
- Delete `SQL/postgres/logs.sql` and `SQL/postgres/release_notes.sql`.
- `SQL/postgres/schema_migrations.sql` header: replace "there is no SQL/postgres/migrations/ yet, so nothing registers into it until the #21 dialect work lands" with "Postgres migrations in SQL/postgres/migrations/ register into it (first: #31)."
- `SQL/postgres/seed_test_data.sql`:
  - Line 29: delete `, along with logs and release_notes` so the line ends "cleared and left empty."
  - Delete lines 83–84 (`DELETE FROM release_notes;` and `DELETE FROM logs;`).
  - Line 115: `'10'` becomes `'11'`.
  - Line 171: the column list becomes `(id, name, notes, is_active, is_supplier, is_manufacturer, supplier_code)`.
  - Delete line 415 (`UPDATE part SET is_lot_tracked…`).
  - Change the comment on lines 417–418 to `-- tracking_mode (#743): 3007/3012/3013 are lot-tracked.`
  - Lines 421–424: drop the trailing clause `; is_lot_tracked stays in sync (3013 already TRUE; 3005 stays FALSE — serial does not imply lot control)`. Replace it with `.`
  - No named_queries row references these names (checked).

## 4. Go
- `internal/config/config.go:21`: set `ExpectedSchemaVersion = "11"`.
- `arx_go/models/supplier.go`: rename the fields `SUSupplierCode`→`SupplierCode`, `SUNotes`→`Notes`, `SUNumOfLNKs`→`SupplierPartCount`, `SUNumOfPOs`→`POCount`.
- `arx_go/suppliers.go`:
  - Line 23 comment: `SUSupplierCode` becomes `supplier_code`.
  - Line 60: `su.supplier_code, su.supplier_part_count, su.po_count`.
  - Lines 237 and 305: `fv(r, "supplier_code")`.
  - Line 247: columns `name, supplier_code, default_contact, is_active, is_supplier, is_manufacturer, notes, date_modified`.
  - Lines 251/321: `fv(r, "supplier_code")`. Lines 256/326: `fv(r, "notes")`.
  - Lines 315–317: `supplier_code=@p2`, `notes=@p7`.
  - Lines 779–781: `su.supplier_code, su.notes`, `su.supplier_part_count, su.po_count`.
  - Lines 805–806 and 814–815: use the new field names.
  - Lines 858, 863, 884, 940, 944, 1005, 1010: `s.SupplierCode`.
  - Lines 1017–1018: `SupplierCode: fv(r, "supplier_code")`, `Notes: fv(r, "notes")`.
- `arx_go/pos.go:1064` and `:2138`: `SELECT supplier_code FROM %s WHERE id=@p1`.
- `arx_go/parts.go:549`: remove `is_lot_tracked, ` from the column list, remove `,@p26` from the placeholders, and remove `models.TracksLots(mode), ` from the arguments (keep `mode`).
- `arx_go/parts.go:663`: `tracking_mode=@p24`, `WHERE id=@p25`, and remove `models.TracksLots(mode),` from the arguments.
- `arx_go/models/part.go`:
  - Line 25 comment becomes `// derived from TrackingMode via TracksLots (#745); not a DB column`.
  - Line 161: delete the sentence ` Replaces the is_lot_tracked read (#745).`
- `arx_go/models/purchase_order.go:65` comment becomes `// derived from part.tracking_mode (TracksLots, #745): receiving this line creates a lot row`.
- `arx_go/lot.go:16`: `(part.is_lot_tracked)` becomes `(part.tracking_mode lot/lot_serial)`.
- Templates:
  - `templates/suppliers/supplier_detail.html`: line 19 `.Supplier.SupplierCode`; line 75 `.Supplier.SupplierPartCount`; line 76 `.Supplier.POCount`; lines 152 and 156 `.Supplier.Notes`.
  - `templates/suppliers/supplier_edit.html`: lines 26–28 use `for`/`id`/`name="supplier_code"` and `.Supplier.SupplierCode`; lines 86–87 use `id`/`name="notes"` and `.Supplier.Notes`.
- Tests:
  - `arx_go/suppliers_test.go`: replace every `SUSupplierCode:` with `SupplierCode:` (12 sites).
  - `arx_go/suppliers_integration_test.go`: lines 135 and 215 use the form key `"supplier_code"`; lines 147, 207 and 224 use `supplier_code` in the SQL; line 229's message says `supplier_code`.
  - `arx_go/integration_test.go`: the comments on lines 1935, 1936 and 2079 change `is_lot_tracked` to `lot-tracked`.

## 5. Docs
- `SQL/SCHEMA.md`:
  - Line 91: "Parts 3007, 3012 + 3013 are `is_lot_tracked = 1`" becomes "Parts 3007 and 3012 have `tracking_mode` `lot`, and 3013 has `lot_serial`".
  - Line 99: delete the sentence about `logs` and `release_notes`.
  - Lines 108, 109 and 116: use `company.supplier_part_count` and `company.po_count`.
  - Line 212 (`part` row): delete the `is_lot_tracked` sentence. Replace the `tracking_mode` text with "…restricts it to `none/lot/serial/lot_serial`; the authoritative lot/serial control (read-swap #745); `is_lot_tracked` dropped in #31."
  - Line 215 (`company` row): `supplier_part_count` and `po_count` (formerly `SUNumOfLNKs`/`SUNumOfPOs`, renamed in #31) are denormalized counts maintained by DB triggers. `notes` was `SUNotes`. `supplier_code` (was `SUSupplierCode`) is the folder stub used to name supplier and PO folders. `SUWeb`/`SUContact1` were dropped in #31.
  - Migrations section: add a "Postgres" subsection covering `SQL/postgres/migrations/YYYYMMDDHHMMSS_<issue>_<desc>.sql`; running with `psql -v ON_ERROR_STOP=1 -d ArxDev -f`; the `current_database()` guard; one `BEGIN`/`COMMIT`; guards via `IF EXISTS`/`IF NOT EXISTS` or `information_schema` checks; last-step registration `INSERT INTO schema_migrations (version_id, is_applied) SELECT <ts>, TRUE WHERE NOT EXISTS (…)`; linted by the `postgres` subtest; renames must `CREATE OR REPLACE` any plpgsql function that references the column and must patch `named_queries.sql` text.
- `SQL/schema_diagram.md`:
  - Company entity: lines 23 and 27–29 use the new names.
  - Delete `bit is_lot_tracked` (lines 71 and 697).
  - Lines 688–689: "`tracking_mode` is why a part enters this flow at all, so it's kept".
  - Delete the `logs` and `release_notes` entities (lines 378–391).
  - Delete the line 476 bullet.
  - Line 477: remove `**logs**` and `**release_notes**` from the list.
  - Line 479: use the new count names.
- `SQL/postgres/README.md`:
  - Line 11: use the new count names.
  - Delete the "Mixed-case column names" bullet (lines 49–55).
  - Run order (lines 112–113): drop `logs` and `release_notes`, so the list ends `…, named_queries, users`.
  - Add a "Migrations" section pointing to `SQL/postgres/migrations/` and SCHEMA.md#migrations.
- `docs/FUTURE_GOALS.md`: line 67 becomes "Arx header icon ([#22](…))". Delete line 68 (the SUWeb/SUContact1 item).
- `ROADMAP.md:14`: "Cleanup: Arx header icon ([#22](…))".
- `docs/conventions.md:123`: `<PO number> <company supplier_code>`.
- `CHANGELOG.md` (single batch entry):
  - `### Removed`: dropped the dead `company.SUWeb`/`SUContact1` and `part.is_lot_tracked` columns and the unused `logs`/`release_notes` tables ([#31](https://github.com/Jolls/arx/issues/31)).
  - `### Changed`: renamed the legacy `company` columns to snake_case (`notes`, `supplier_code`, `supplier_part_count`, `po_count`); schema_version 11; run `SQL/postgres/migrations/<file>`; the Settings backup's `company.csv`/`part.csv` headers change.
  - No RELEASE_NOTES entry: nothing here is user-facing.

## Test plan
**1. Coverage audit** (existing tests that hit the changed code)
- `suppliers_integration_test.go`:
  - SuppliersRows: list query with code and counts.
  - SupplierDetail and SupplierEdit: render the renamed template fields; a missing field is a template runtime error.
  - SupplierUpdate and SupplierUpdate_InvalidFolderStub: posted keys and selected columns.
  - SuppliersNew.
- `smoke_post_test.go`: `seedSupplier` goes through the SuppliersCreate insert; PostRoutesSmoke covers PartsCreate.
- `integration_test.go`: PartLifecycle covers the PartsCreate/PartUpdate SQL. The build and lot tests cover the `IsLotTracked` reads.
- `suppliers_test.go` and `files_test.go`: folder and file serving via `SupplierCode`.
- `migrations_lint_test.go`.
- Gaps: nothing covers `tracking_mode` round-tripping through create/update, the trigger-maintained counts, supplier notes, or the supplier-code lookups in `pos.go` (POPrint, createPOFolder).

**2. Characterization tests** (write first; they pass on current code; after the rename only the form keys and field names change)
- C1 `TestIntegration_SupplierNotesCodeAndCounts` in `suppliers_integration_test.go`:
  1. Seed a supplier with `seedSupplier`.
  2. SupplierUpdate with the code `CHR1` and notes `characterization note`; expect a 302.
  3. Seed a part with `seedPart(t,h,ctx,"BUY")`, then `seedSupplierPart` for that part and supplier.
  4. POCreate with `POFolderRoot` blanked and `supplier_id` set to the supplier. Parse the number from the Location header and look up the id, as `seedThrowawayPO` does. Defer deleting the po_line, history and PO rows. Defers run in reverse order, so the PO goes first and the company last.
  5. SupplierDetail's body must contain `characterization note`, `CHR1`, `Linked Parts:</strong><span>1</span>` and `Purchase Orders:</strong><span>1</span>`.
- C2 `TestIntegration_PartTrackingModeRoundTrip` in a new `arx_go/parts_integration_test.go` (`//go:build integration`):
  1. PartsCreate with `tracking_mode=lot`. `fetchPartBasic` must return TrackingMode `lot` and IsLotTracked true.
  2. PartUpdate to `none`: TrackingMode `none`, IsLotTracked false.
  3. PartUpdate to `lot_serial`: IsLotTracked true.
  4. Clean up by deleting the part.
- C3 `TestIntegration_POCreate_FolderUsesSupplierCode` in `integration_test.go`:
  1. Set `POFolderRoot = t.TempDir()` and restore it on defer.
  2. POCreate with `supplier_id=1001`.
  3. Expect a directory under the temp root whose name starts with the PO number and contains ` ACME`.
  4. Delete the PO rows as in `seedThrowawayPO`.

**3. Red tests** (fail before the change, pass after)
- R1 `TestDeadSchemaNamesRemoved`, a new unit test in `arx_go/dead_schema_names_test.go` with no build tag:
  - Walk `arx_go/` for `.go`, `.html` and `.js` files, skipping this file by name. Also read the top-level `../SQL/postgres/*.sql` files, skipping the `migrations` subdirectory.
  - Fail on ``(?i)\b(suweb|sucontact1|sunotes|sunumoflnks|sunumofpos|susuppliercode|is_lot_tracked)\b``.
  - Fail on ``(?i)\b(FROM|INTO|TABLE)\s+(logs|release_notes)\b`` in those SQL files.
  - Assert `../SQL/postgres/logs.sql` and `release_notes.sql` don't exist.
  - This also catches a missed rename in the unchecked `pos.go` lookups.
- R2: extend `TestMigrationsSelfRegister` (step 2) before the migration exists. It fails on the missing `SQL/postgres/migrations` directory, then passes once the migration file is added.
- R3 `TestIntegration_DeadSchemaObjectsGone` in `integration_test.go`. The queries work on both engines.
  - `SELECT lower(column_name) FROM information_schema.columns WHERE lower(table_name)='company'` must contain `notes`, `supplier_code`, `supplier_part_count` and `po_count`, and none of the six old names.
  - The same query for `part` must not contain `is_lot_tracked`.
  - `information_schema.tables` must have no row where `lower(table_name) IN ('logs','release_notes')`.
  - It stays red until ArxDev is migrated or reseeded. In #19's CI container it passes once the container loads the new DDL. `logs.sql` and `release_notes.sql` are gone from the #19 run order.

**4. Manual only**
- Run the migration against the Postgres ArxDev twice; the second run must be a no-op. Then check with `\d company` / `\d part` and `SELECT * FROM schema_migrations`.
- After the migration, add and remove a supplier link and a PO, and confirm the counts update. This checks the replaced trigger functions.
- Print a PO (POPrint's supplier-code lookup has no test).
- Take a Settings backup and check the `company.csv` and `part.csv` headers.
- Edit a supplier's notes and code in the UI.
- Then reseed ArxDev from the updated `seed_test_data.sql` (a human does this) and run `ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/...`.

### Critical files
- SQL/postgres/migrations/<ts>_31_dead_column_cleanup.sql (new)
- SQL/postgres/triggers.sql
- arx_go/suppliers.go
- arx_go/migrations_lint_test.go
- SQL/postgres/seed_test_data.sql

## Resolved decisions (2026-09-26)
- Open questions 1–3: accepted as recommended (names `supplier_code`, `notes`, `supplier_part_count`, `po_count`; rename Go/form fields; schema_version 10→11).
- Q4 CLAUDE.md: add one short section describing the `SQL/postgres/migrations/` convention (filename, psql ON_ERROR_STOP, ArxDev guard, BEGIN/COMMIT, idempotent guards, last-step self-registration `SELECT <ts>, TRUE WHERE NOT EXISTS`, rename → CREATE OR REPLACE plpgsql + patch named_queries). Leave the rest of the Azure text for the cleanup pass.
- Also remove `logs.sql` and `release_notes.sql` from the `.github/workflows/test.yml` load list (#19).
