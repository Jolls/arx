# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.5.15] - 2026-06-08 
- arx: add hover tooltips to column headers and field labels across Parts Master and Test Records ([#333](https://github.com/Jolls/arx-legacy/issues/333))

## [0.5.14] - 2026-06-07 
- arx: bind to 127.0.0.1 instead of 0.0.0.0 — app is localhost-only ([#361](https://github.com/Jolls/arx-legacy/issues/361))
- test_records: fix isSafeQuery false positives on column names containing keyword substrings (e.g. created_at, updated_at, alternate) by switching to word-boundary regex matching ([#358](https://github.com/Jolls/arx-legacy/issues/358))

## [0.5.13] - 2026-06-07 
- test_records: form definition history now shows the historical step values, not just which rows changed ([#389](https://github.com/Jolls/arx-legacy/issues/389))

## [0.5.12] - 2026-06-05 
- both: add a Records tab to the Parts Master nav and show the shared Parts Master header + tab bar on Test Records pages (minimal slice of the unified shell) ([#421](https://github.com/Jolls/arx-legacy/issues/421))

## [0.5.11] - 2026-06-05 
- parts_master: part subtabs (BOM, Order History, Pricing, Mfg Parts, Suppliers) now show or gray out per part category, with an editable category + tab-visibility table in Settings ([#345](https://github.com/Jolls/arx-legacy/issues/345))

## [0.5.10] - 2026-06-05 
- both: collapse the three Go packages into a single arx_go package ([#422](https://github.com/Jolls/arx-legacy/issues/422))

## [0.5.9] - 2026-06-04 
- parts_master: add build-tagged live-DB integration tests for the part + attachment lifecycle (identity insert, FIL-count trigger) ([#416](https://github.com/Jolls/arx-legacy/issues/416))

## [0.5.8] - 2026-06-04 
- both: unify the session cookie into one shared name across the merged app ([#423](https://github.com/Jolls/arx-legacy/issues/423))

## [0.5.7] - 2026-06-04 
- both: collapse the two config.Config types and Load() functions into one shared arxlib/config.Config ([#419](https://github.com/Jolls/arx-legacy/issues/419))
- both: share a single DB connection pool across the merged app instead of one pool per app ([#420](https://github.com/Jolls/arx-legacy/issues/420))
- both: retire per-app duplicates — single RELEASE_NOTES embed and one ExpectedSchemaVersion/CheckSchemaVersion ([#424](https://github.com/Jolls/arx-legacy/issues/424))

## [0.5.6] - 2026-06-04 
- both: merge Parts Master and Test Records onto a single port (4568); Test Records moves to /records, one unified settings page and config/local.json ([#413](https://github.com/Jolls/arx-legacy/issues/413))

## [0.5.5] - 2026-06-04 
- both: expand the test suite with pure-function unit tests (path/SQL safety, form parsing, query-spec/token helpers) and no-DB httptest coverage of the RequireAuth and CSRF middleware ([#414](https://github.com/Jolls/arx-legacy/issues/414), [#240](https://github.com/Jolls/arx-legacy/issues/240))

## [0.5.4] - 2026-06-04 
- both: TEST_MODE now swaps the connection to a separate ArxDev database instead of _Test table-name suffixes; cfg.*Table() helpers return bare names ([#241](https://github.com/Jolls/arx-legacy/issues/241))

## [0.5.3] - 2026-06-03 
- parts_master_go: remove unused gopkg.in/yaml.v3 dependency, collapse the vestigial config.Settings wrapper into Config, and fix the stale settings.yml reference in the Settings UI ([#321](https://github.com/Jolls/arx-legacy/issues/321))

## [0.5.2] - 2026-06-03 
- arxlib: document LOCAL: FILFileName invariant in conventions.md; fix helper-function location reference ([#375](https://github.com/Jolls/arx-legacy/issues/375))
- test_records_go: drop pf_formula from test_definition and test_definition_history; add migration script ([#385](https://github.com/Jolls/arx-legacy/issues/385))
- both: extract arxlib/config.Base to de-duplicate BuildDSN, DSN, ConnectionSummary, Pick, and GetEnv across the two apps; no behavior change ([#372](https://github.com/Jolls/arx-legacy/issues/372))

## [0.5.1] - 2026-06-03 
- parts_master_go: appConfigGet returns an error so callers can tell a missing key from a DB failure ([#371](https://github.com/Jolls/arx-legacy/issues/371))
- both: read cached schema-version check instead of re-querying app_config on every page load ([#370](https://github.com/Jolls/arx-legacy/issues/370))
- parts_master_go: txLogger now logs Commit/Rollback in debug mode ([#373](https://github.com/Jolls/arx-legacy/issues/373))
- test_records_go: view definition now shows raw spec_nom instead of expanding {id} tokens, so it matches the editor ([#405](https://github.com/Jolls/arx-legacy/issues/405))
- test_records_go: cap Comment column width to 240px so long step comments don't stretch the results table

## [0.5.0] - 2026-06-03 
- infra: merge Parts Master and Test Records into a single Arx.exe with one systray icon; both apps still serve on ports 4568/4569 ([#218](https://github.com/Jolls/arx-legacy/issues/218))

## [0.4.2] - 2026-06-03 
- security: encode CSRF tokens as base64url instead of hex (same entropy, shorter token) ([#377](https://github.com/Jolls/arx-legacy/issues/377))
- docs: relocate scattered code TODOs to tracked references in FUTURE_GOALS.md and issue numbers ([#368](https://github.com/Jolls/arx-legacy/issues/368))
- docs: relocate stale SQL DDL TODOs to #213 references in FUTURE_GOALS.md ([#376](https://github.com/Jolls/arx-legacy/issues/376))
- docs: reconcile FUTURE_GOALS.md — strike through completed Test Records items; fix two doc-drift references ([#384](https://github.com/Jolls/arx-legacy/issues/384))
- build: align go.mod toolchain with workspace go 1.26.3 ([#382](https://github.com/Jolls/arx-legacy/issues/382))
- build: stop running arxlib tests twice per full build; add root test.bat ([#380](https://github.com/Jolls/arx-legacy/issues/380))
- build: rewrite start.ps1 to launch the Go apps ([#349](https://github.com/Jolls/arx-legacy/issues/349))
- parts_master_go: nullableInt now parses to int instead of returning a raw string (caller panicked on type assertion) ([#355](https://github.com/Jolls/arx-legacy/issues/355))
- parts_master_go: raise test-mode PO sequence seed floor from 0 to 99999 so all-non-numeric snapshots can't collide ([#378](https://github.com/Jolls/arx-legacy/issues/378))
- parts_master_go: avoid double-rollback log noise on committed transactions (BOM update, PO create, PO update) ([#367](https://github.com/Jolls/arx-legacy/issues/367))
- test_records_go: audit and document hide_formula nil-context call paths; nil is intentional for form-def view ([#360](https://github.com/Jolls/arx-legacy/issues/360))
- both: poll the port instead of a fixed 600ms sleep before opening the browser ([#364](https://github.com/Jolls/arx-legacy/issues/364))
- both: RFC 6266-encode the Content-Disposition filename with mime.FormatMediaType ([#363](https://github.com/Jolls/arx-legacy/issues/363))
- arxlib: add BrowseFolderContext with exec.CommandContext so HTTP handlers can't hang on an open folder dialog ([#359](https://github.com/Jolls/arx-legacy/issues/359))
- parts_master_go: guard openDebugConsole against repeated allocation ([#379](https://github.com/Jolls/arx-legacy/issues/379))
- parts_master_go: local config ACL confirmed owner-only by inheritance — no code change needed ([#381](https://github.com/Jolls/arx-legacy/issues/381))
- parts_master_go: render() type guard confirmed present in both apps — no fix needed ([#366](https://github.com/Jolls/arx-legacy/issues/366))

## [0.4.1] - 2026-06-02 
- security: CSRF check moved to middleware covering all POST routes in both apps; fixes two previously unprotected PO handlers ([#362](https://github.com/Jolls/arx-legacy/issues/362), [#350](https://github.com/Jolls/arx-legacy/issues/350))
- security: `crypto/rand.Read` error in CSRF token generation now panics instead of silently using a zeroed token ([#351](https://github.com/Jolls/arx-legacy/issues/351))
- security: hardcoded `"change-me-in-production"` session secret replaced with auto-generated random secret persisted to `local.json` ([#352](https://github.com/Jolls/arx-legacy/issues/352))
- infra: `config/local.json` writes are now atomic (tmp → fsync → rename) to prevent zero-byte corruption on crash ([#356](https://github.com/Jolls/arx-legacy/issues/356))
- db: `h.db` access guarded with `sync.RWMutex`; old connection pool closed after reconnect from Settings ([#357](https://github.com/Jolls/arx-legacy/issues/357))
- db: connection pool limits added after `Ping()` — max 25 open, 5 idle, 30 min lifetime ([#353](https://github.com/Jolls/arx-legacy/issues/353))
- db: silent `execContext` and `Scan` failures across both apps now log with context instead of discarding errors ([#354](https://github.com/Jolls/arx-legacy/issues/354))
- infra: `log.Fatal` in HTTP server goroutine replaced with `log.Print` + `systray.Quit()` so `onExit`/`CloseDB` runs on bind errors ([#365](https://github.com/Jolls/arx-legacy/issues/365))
- test_records_go: fix `{record.date}` formula overwriting stored result as decimal on edit ([#391](https://github.com/Jolls/arx-legacy/issues/391))
- test_records_go: Test Date field now stores and displays date + time; inputs use datetime-local picker
- test_records_go: add `{record.datetime}` formula token (MM/DD/YYYY H:MM AM/PM)
- db: drop legacy `trg_Tests_history` trigger that silently rolled back all `test_definition` UPDATEs; document in schema.md and TestRecords.sql

## [0.4.0] - 2026-05-26 
- release: v0.4.0 milestone — conditional step visibility, result format display, units of measure, vendor rename, manufacturer links, Windows login auto-fill

## [0.3.53] - 2026-05-26 
- test_records_go: hide_formula — support `{token}=value` and `{token}!=value` expressions for dynamic per-record step visibility ([#210](https://github.com/Jolls/arx-legacy/issues/210))

## [0.3.52] - 2026-05-26 
- test_records_go: deprecate pf_formula — remove from all step queries and struct; pass/fail uses pf_type + ComputePassFail ([#199](https://github.com/Jolls/arx-legacy/issues/199))

## [0.3.51] - 2026-05-26 
- parts_master_go: rename "Suppliers" section to "Vendors" throughout UI; add Roles row to vendor detail page; fix "Supplier is active" label and manufacturer checkbox formatting ([#336](https://github.com/Jolls/arx-legacy/issues/336))

## [0.3.50] - 2026-05-26 
- parts_master_go: manufacturer name on Mfg Parts tab is now a clickable link to the supplier detail page ([#338](https://github.com/Jolls/arx-legacy/issues/338))

## [0.3.49] - 2026-05-26 
- test_records_go: apply `format` field to result display in show/print views; add format placeholder to edit inputs; expose format column in form def editor ([#209](https://github.com/Jolls/arx-legacy/issues/209))

## [0.3.48] - 2026-05-26 
- parts_master_go: add units of measure — `unit` reference table, base unit on parts (`PNUNID`), purchase unit on sourcing (`supplier_part.unit_id`); supplier parts list shows effective unit with base-unit fallback ([#314](https://github.com/Jolls/arx-legacy/issues/314))

## [0.3.47] - 2026-05-25 
- test_records_go: use `os/user.Current()` instead of `os.Getenv("USERNAME")` for audit username in lock/unlock handlers ([#330](https://github.com/Jolls/arx-legacy/issues/330))
- parts_master_go: pre-populate PO orderer field with Windows login name on new PO ([#330](https://github.com/Jolls/arx-legacy/issues/330))
- parts_master_go: pre-populate Requested By field with Windows login name on new part ([#330](https://github.com/Jolls/arx-legacy/issues/330))

## [0.3.46] - 2026-05-25 
- parts_master_go: add manufacturer part number (MPN) management on part detail page; flag companies as manufacturers; soft-delete support ([#303](https://github.com/Jolls/arx-legacy/issues/303))

## [0.3.45] - 2026-05-25 
- test_records_go: add New Form action on forms index — creates blank form (or optionally copies steps from an existing form) for any unassigned FORM-category PN ([#319](https://github.com/Jolls/arx-legacy/issues/319))
- test_records_go: add Duplicate Form action on form definition page — copies all steps, record_types, and instrument_types to a new form with chosen PN ([#255](https://github.com/Jolls/arx-legacy/issues/255))
- test_records_go: add record_types and instrument_types fields to form definition edit page

## [0.3.44] - 2026-05-25 
- test_records_go: implement Form lock/unlock with audit trail written to `form_events`; require comment on unlock ([#200](https://github.com/Jolls/arx-legacy/issues/200))

## [0.3.43] - 2026-05-25 
- parts_master_go: drop unused `is_active` column from `supplier_part`; remove Active column from supplier linked-parts view

## [0.3.42] - 2026-05-25 
- parts_master_go: add `status` enum to PO (`pending/placed/complete/cancelled/on_hold`); replace `is_active` checkbox with status dropdown; `is_active` kept in sync as convenience bit ([#300](https://github.com/Jolls/arx-legacy/issues/300))

## [0.3.41] - 2026-05-25 
- schema: replace `TestRecordHistory` with `form_events` + `record_events`; migrate 90 rows; real FK constraints on both tables ([#298](https://github.com/Jolls/arx-legacy/issues/298))
- test_records_go: lock/unlock UI for test records with required comment on unlock ([#201](https://github.com/Jolls/arx-legacy/issues/201))
- test_records_go: write lock/unlock audit events to `record_events` using Windows login ([#200](https://github.com/Jolls/arx-legacy/issues/200))

## [0.3.40] - 2026-05-25 
- schema: migrate `FIL.FILPNID` from `VARCHAR` to `INT NOT NULL` with enforced FK to `PN.PNID`; fix `FIL_Test` missing `is_active` DEFAULT in `_test.sql`; correct stale column names in schema docs ([#297](https://github.com/Jolls/arx-legacy/issues/297))

## [0.3.39] - 2026-05-24 
- parts_master_go: rename `FIL.FILNotes` → `FIL.category`; attachment categories now stored in `app_config` and editable via Settings; drop dead `settings.yml` / yaml approach ([#313](https://github.com/Jolls/arx-legacy/issues/313))

## [0.3.38] - 2026-05-24 
- test_records_go: drop `TestResults.form_id` — write-only denormalized column, never read by the app ([#299](https://github.com/Jolls/arx-legacy/issues/299))

## [0.3.37] - 2026-05-24 
- test_records_go: add `instrument_type` field to `TestRecords` and rename `applicable_instrs` → `instrument_types` on `test_definition`; record show/edit/print views now filter steps by instrument type — steps with `instrument_types` set are hidden when the record's `instrument_type` doesn't match ([#203](https://github.com/Jolls/arx-legacy/issues/203))
- test_records_go: add `instrument_types` to `Forms`; record create/edit show a dropdown for Instrument Type when the form has types configured, free-text otherwise ([#203](https://github.com/Jolls/arx-legacy/issues/203))

## [0.3.36] - 2026-05-24 
- docs: refactor CLAUDE.md — move per-table schema reference into `SQL/schema.md`, attachment/PO folder conventions into new `docs/conventions.md`; CLAUDE.md now links to reference docs rather than embedding them ([#315](https://github.com/Jolls/arx-legacy/issues/315))

## [0.3.35] - 2026-05-24 
- parts_master_go: rename `LNK` → `supplier_part` with modernized column names (`LNKID`→`id`, `LNKSUID`→`supplier_id`, `LNKPNID`→`part_id`, `LNKVendorPN`→`supplier_pn`, `LNKVendorDesc`→`supplier_desc`, `LNKLeadtime`→`lead_time`, `LNKChoice`→`preference`, `LNKUse`→`is_active`, etc.); drop obsolete columns (`LNKMFRID`, `LNKMFRPNID`, `LNKUNID`, `LNKToPNID`, `LNKAtQty`, `LNKCurrentCost`, `LNKRFQDate`); add `mfg_part` table for manufacturer part records; add `is_supplier`/`is_manufacturer` role flags to `supplier`; update config helpers, models, handlers, templates, and schema docs; add test + prod migration scripts with backup and validation ([#225](https://github.com/Jolls/arx-legacy/issues/225))
- parts_master_go: rename `supplier` → `company` and `supplier_attachment` → `company_attachment`; manufacturers and distributors share one table distinguished by role flags; update all FK constraint names, config helpers (`CompanyTable`, `CompanyAttachmentsTable`), handlers, `_test.sql`, `triggers.sql`, and schema docs; fix deferred trigger bodies that still referenced `dbo.LNK`/`LNKSUID`/`dbo.supplier` ([#225](https://github.com/Jolls/arx-legacy/issues/225))

## [0.3.34] - 2026-05-23 
- parts_master_go: add price CRUD — create, edit (deactivates old row + inserts new), deactivate, activate; add `effective_date` column for price history; replace unique constraint with filtered index (active rows only) so inactive rows serve as history ([#310](https://github.com/Jolls/arx-legacy/issues/310))

## [0.3.33] - 2026-05-22 
- both apps: rename `Tests` table → `test_definition` (and `Tests_Test` → `test_definition_Test`); update `StepsTable()` config helper, DDL, schema diagram, CLAUDE.md ([#215](https://github.com/Jolls/arx-legacy/issues/215))

## [0.3.32] - 2026-05-22 
- parts_master_go: rename `PNType` → `category` (CHECK-constrained: ASM/BUY/DWG/DOC/FORM/MFG/RAW/SVC/TOOL); add `has_bom` BIT column as explicit BOM capability driver; rename 5 `PN` columns to snake_case (`PNPartNumber`→`part_number`, `PNTitle`→`title`, `PNDetail`→`detail`, `PNStatus`→`status`, `PNActive`→`active`); update all queries, models, templates, and `named_queries` data (schema version 2)

## [0.3.31] - 2026-05-22 
- both apps: embed user-facing release notes in binary; `/whats-new` route; "new version" banner in layout; release notes replace changelog on settings page ([#295](https://github.com/Jolls/arx-legacy/issues/295))

## [0.3.30] - 2026-05-22 
- test_records_go: add image gallery to record detail and print views ([#208](https://github.com/Jolls/arx-legacy/issues/208))

## [0.3.29] - 2026-05-22 
- test_records_go: add print/PDF export view for test records ([#204](https://github.com/Jolls/arx-legacy/issues/204))

## [0.3.28] - 2026-05-22 
- parts_master_go: PO print page auto-names PDF to `<PO Number> <SupplierCode>` via `<title>` tag
- parts_master_go: print button opens PO folder in Explorer after marking printed (`POST /po/{id}/open-folder`), creating folder if it doesn't exist
- parts_master_go: suppress browser URL/date headers from PO print output via `@page { margin: 0 }`
- parts_master_go: capture part revision at time of order — `POL.POLRev VARCHAR(10) NULL` wired up on INSERT and UPDATE; server-side fallback looks up `PN.revision` when POLPNID is set and form field is blank
- parts_master_go: `APIPartSearch` now returns `revision` field; PO edit autocomplete auto-fills Rev on part selection
- parts_master_go: Rev column added to PO detail, edit form, and print views
- parts_master_go: fix PO creation failure on tables with triggers — replace `OUTPUT INSERTED.ID` with combined INSERT + `SCOPE_IDENTITY()` batch
- SQL: add `POLRev VARCHAR(10) NULL` to `po_items.sql` DDL; migration: `ALTER TABLE dbo.POL ADD POLRev VARCHAR(10) NULL`
- closes #220

## [0.3.27] - 2026-05-21 
- parts_master_go: add BOM rollup cost button on BOM tab — `POST /part/{id}/rollup-cost` computes `SUM(PNCurrentCost * PLQty)` for direct components and writes to `PN.PNLastRollupCost` + new `PN.PNLastRollupAt`
- parts_master_go: add Unit Cost column to BOM table (PNCurrentCost per component)
- SQL: add `PN.PNLastRollupAt DATETIME NULL` column (NULL = never run, paired with existing `PNLastRollupCost`)

## [0.3.26] - 2026-05-21 
- SQL: add NOT NULL + FK constraints to LNK, PL, POL, PO, price, supplier, CN, Forms, Tests, TestRecords, TestResults; migration scripts in `SQL/migrate_add_constraints_parts.sql` and `SQL/migrate_add_constraints_tests.sql`
- SQL: rename all auto-generated constraint names to explicit `DF_table_column` / `UQ_table_column` conventions; rename script in `SQL/rename_constraints.sql`
- SQL: update all DDL reference files with explicit CONSTRAINT names, NOT NULL, and FK declarations; remove resolved TODO comments
- SQL: add missing DEFAULT constraints for `supplier.is_active`, `supplier.SUNumOfLNKs`, `supplier.SUNumOfPOs`, `price.pack_size`
- Both apps: add cross-app navigation link in header (Parts Master ↔ Test Records); configurable via `PM_URL` / `TR_URL` env vars, defaulting to localhost ports
- parts_master_go: fix nil panic on new supplier form — `{{if not .Contacts}}` instead of `{{if eq (len .Contacts) 0}}`
- test_records_go: replace `serial_number + 0` sort trick with `TRY_CAST(serial_number AS INT)` in RecordsList and RecordDetail prev/next queries

## [0.3.25] - 2026-05-21 
- SQL: migrate `PN.PNLastRollupCost` from `VARCHAR(255)` to `DECIMAL(16,8) NULL`; NULL = no rollup ever run
- SQL: add `NOT NULL DEFAULT 0` to `PL.PLItem` and `PL.PLQty`; fixes NULL scan error on where-used page
- Add `arxlib/urlutil/urlutil_test.go` — first unit test suite; covers all 9 urlutil functions
- Fix `SafePathSegments` to correctly drop `..` traversal segments (was passing through via `filepath.Base`)

## [0.3.24] - 2026-05-20 
- Both apps: open a Windows console window on startup when `DEBUG_MODE=true` (`console_windows.go`); SQL query logging is now visible without running from a terminal
- Both apps: add `app_config` table (key/value store for DB-side metadata); seed `schema_version = '1'`; add `app_config_Test` variant and update `_test.sql`
- Both apps: add `CheckSchemaVersion` — checks `app_config.schema_version` against `config.ExpectedSchemaVersion` on startup, on settings save, and on Parts/Forms/Records page load; shows a persistent banner if there is a mismatch

## [0.3.23] - 2026-05-20 
- Schema audit: drop dead columns `supplier.SUWeb`, `supplier.SUContact1`, `Forms.custom_sheets`, `POL.POLRev`, `price.price_type` from DDL; update comments on remaining flagged columns confirming status
- `parts_master_go`: add `PO.date_printed` to model, `fetchPO` SELECT/scan, update handler, detail status bar, and edit form; set automatically via `POST /po/{id}/mark-printed` when print button is clicked
- Both apps: link `DEBUG_MODE` to SQL query logging — all `QueryContext`/`QueryRowContext`/`ExecContext`/transaction calls log to terminal when debug mode is on; `parts_master_go` adds wrapper methods; `test_records_go` unifies on `DebugMode` (removes separate `SQLDebug` field)

## [0.3.22] - 2026-05-19 
- `test_records_go`: prev/next navigation arrows on record view — steps through records in the same form by the same ordering as the records list (serial number descending)
- `test_records_go`: debug mode setting — toggleable in Settings; when on, shows raw step fields panel on record view; persisted in `config/local.tr.json`
- Both apps: test mode now toggleable in Settings UI under Developer; persisted in `local.json` which overrides `.env`; confirmation popup warns to close other tabs before switching
- Both apps: remove test mode port switching — each app runs on one port regardless of mode; test mode now only switches table name variants

## [0.3.21] - 2026-05-19 
- Remove `report_text` field — never used in VBA or Go UI; removed from `Tests` DDL, `test_definition_history` DDL and trigger, all Go handlers, models, templates, and migration script; DB column drop (`ALTER TABLE Tests DROP COLUMN report_text`) to be run separately

## [0.3.20] - 2026-05-19 
- DB migration: VBA `ArchiveTest` rows moved from `Tests` into `test_definition_history`; all definition change history now consolidated in one table and surfaced in the form definition timeline UI
- `SQL/migrate_vba_archive_to_history.sql`: migration script with pre-flight checks, `_Test` and prod steps, backup tables, and cleanup placeholders (section 4 deferred — archive rows and column drops pending)
- `SQL/_test.sql`: remove conditional guard on `test_definition_history_Test` recreate — table is now permanent
- `test_records_go`: remove Calc P/F column from read-only record view — stored `pass_fail` is the source of truth; live spec comparison deferred to a future feature
- `TestRecord` xlsm: remove `Revision` column from `tbl_NewTests` test modification sheet

## [0.3.19] - 2026-05-18 
- `TestRecord` VBA: remove `ArchiveTest` mechanism — test definition history is now handled entirely by the `trg_Tests_history` DB trigger writing to `test_definition_history`; removes `ArchiveTest` sub, `Call ArchiveTest` call site, revision increment logic, and `Revision` column from the `Tests` SELECT query


- Both apps: `PM_PORT`/`TR_PORT` and `PM_TEST_PORT`/`TR_TEST_PORT` env vars allow all four ports to be configured in a single shared `.env` file; falls back to `PORT`/`TEST_PORT` for backwards compatibility
- Both apps: app-specific local config files (`config/local.pm.json`, `config/local.tr.json`) prevent each app from clobbering the other's settings when run from the same folder
- `test_records_go`: `max_subbatch_result` named query refined — joins `TestRecords` to filter by `record_date <= @record_date`, preventing later batches from inflating the max when editing historical records

## [0.3.18] - 2026-05-18 
- `parts_master_go`: Add BOM editing — GET `/part/{id}/bom/edit` renders an editable table; POST `/part/{id}/bom` saves changes (add rows, update item#/qty, delete rows) in one transaction; part number typeahead reused from PO line items; "Edit BOM" button added to the read-only BOM view
- `parts_master_go`: `TEST_PORT` env var allows prod and test mode to run simultaneously on different ports
- `test_records_go`: Form definition editor — drag-and-drop row reordering (SortableJS), add new test rows, hide checkbox on heading rows, hide applies to all row types
- `test_records_go`: Named queries — live re-resolution of `{id}` cross-step tokens on the edit page without a save/reload cycle; auto-fill single-result queries trigger P/F immediately; open-link icon for LOCAL: file results; `{form.id}`, `{form.pnid}`, `{form.pn}` tokens added; named query errors now surface in the UI instead of silently leaving the field stuck
- `test_records_go`: Add `pn_primary_attachment` and `form_primary_attachment` named queries using `PN.PNFILIDPrimary` for primary attachment lookup
- `test_records_go`: Add named queries — `recent_serial_numbers_for_form` (last 20 SNs for a form by date), `max_subbatch_result` (max integer result for a test step capped at record date), `bom_pn_by_item` changed to `list`
- `test_records_go`: `result_type = 'multi'` on named queries renders a checkbox list; selected values stored comma-delimited; includes "— Other —" for custom entries
- `test_records_go`: Formula evaluation in `default_result` — expressions like `{113} + {116}` auto-compute live on the edit page; computed inputs are readonly and highlighted blue
- `test_records_go`: `pf_type = 'comment'` always passes regardless of value
- `test_records_go`: Definition history timeline collapses to one dot per calendar day
- `test_records_go`: `spec_nom` syntax lint in form definition editor — flags missing `@` on named query params on blur
- `test_records_go`: `TEST_PORT` env var allows prod and test mode to run simultaneously on different ports

## [0.3.17] - 2026-05-16 
- Both apps now display proper branded icons in the system tray and browser tab
- `parts_master_go`: Arx single-gear icon (Arx-32.png embedded as systray ICO and favicon)
- `test_records_go`: Arx Assemblies double-gear icon (Arx-Assemblies-32.png embedded as systray ICO and favicon)
- `icons/` folder added to repo with SVG sources and PNG rasters at 16/32/64/128/256/512/1024px

## [0.3.16] - 2026-05-16 
- Add `trg_FIL_part_count` trigger on `FIL` to maintain `PN.PNFILLinks` (active rows only)
- `SQL/_test.sql`: add `trg_FIL_Test_part_count`, combined FIL+POL recalibration
- `PartsMaster/Part.cls`: convert FIL hard-delete to soft-delete; add `is_active=1` filter to `BuildFileLinkCollection` and `NumFileLinksViaSQL`; remove `UpdateNumFileLinks` (trigger owns count)
- `FIL.FILPNID` migrated from VARCHAR(255) to INT NOT NULL with FK constraint to PN.PNID
- Document all 4 triggers (+ 4 test variants) in `SQL/SCHEMA.md`, `SQL/FIL.sql`, `SQL/part_number.sql`, `CLAUDE.md`

## [0.3.15] - 2026-05-16 
- Add `trg_FIL_part_count` trigger on `FIL` to maintain `PN.PNFILLinks` (active rows only, handles VARCHAR→INT FILPNID via TRY_CAST)
- `SQL/_test.sql`: add `trg_FIL_Test_part_count`, combine FIL+POL recalibration into one update
- `PartsMaster/Part.cls`: convert FIL hard-delete to soft-delete (`UPDATE SET is_active=0`); add `is_active=1` filter to `BuildFileLinkCollection` and `NumFileLinksViaSQL`; remove `UpdateNumFileLinks` (trigger owns the count)
- Document in `SQL/SCHEMA.md`, `SQL/FIL.sql`, `SQL/part_number.sql`, and `CLAUDE.md`

## [0.3.14] - 2026-05-16 
- Add `trg_POL_part_count` trigger on `POL` to maintain `PN.PNPOLinks` after any INSERT/UPDATE/DELETE
- `SQL/_test.sql`: add `trg_POL_Test_part_count` on `POL_Test` and recalibrate `PN_Test.PNPOLinks` in snapshot
- Document in `SQL/SCHEMA.md`, `SQL/po_items.sql`, `SQL/part_number.sql`, and `CLAUDE.md`

## [0.3.13] - 2026-05-16 
- Document DB triggers in `SQL/SCHEMA.md` (new Triggers section), `SQL/LNK.sql`, `SQL/PO.sql`, and `CLAUDE.md`

## [0.3.12] - 2026-05-16 
- `POCreate` and `POUpdate` handlers wrapped in `db.BeginTx` transactions — PO header, line items, and total update now commit atomically or roll back together
- All DB calls in both handlers now check and surface errors (previously silently discarded)
- `POUpdate`: resolve PO ID once before new-line loop instead of per-row

## [0.3.11] - 2026-05-16 
- Add `SQL/triggers.sql`: DB triggers `trg_LNK_supplier_count` and `trg_PO_supplier_count` keep `supplier.SUNumOfLNKs` / `SUNumOfPOs` accurate after any LNK or PO write, from any client
- `SQL/_test.sql`: recreate equivalent triggers on `_Test` tables after snapshot; recalibrate copied counts
- `SQL/supplier.sql`: update comment — counts are now maintained by triggers, not the application
- `PartsMaster/LNKs.bas`: remove `UpdateSUNumOfLNKs` (was querying `LNK_Test` against prod, overwriting the correct trigger value with a stale count)

## [0.3.10] - 2026-05-16 
- New `arxlib/` shared Go module: `db` (Connect), `urlutil` (IsHTTPURL, IsLocalFile, LocalFileURL, FileIcon, SafePathSegments, etc.), `folderpick` (PowerShell folder picker)
- Go workspace (`go.work`) links arxlib, parts_master_go, test_records_go
- Both apps now delegate duplicate helpers to arxlib; local db/db.go are thin wrappers

## [0.3.9] - 2026-05-16 
- New `test_records_go/` Go app: port of `test_records/` Ruby/Sinatra app to Go
- Same chi/systray/go:embed/go-mssqldb stack as `parts_master_go`; port 4569
- Routes: `/` (forms list), `/forms/{id}/records` (WIP filter), `/records/{id}` (detail with hierarchical test results, p/f badges, image hover preview, LOCAL: file links)
- File serving: `/local/*` (DOC_CONTROL_ROOT), `/images/*` (IMAGE_ROOT, auto-.PNG)
- Settings page with DB connection, IMAGE_ROOT, DOC_CONTROL_ROOT, browse-folder picker
- Test-mode table variants: Forms/Forms_Test, TestRecords/TestRecords_Test, Tests/Tests_Test, TestResults/TestResults_Test
- Updated CLAUDE.md: both Go apps documented, test-mode table expanded

## [0.3.8] - 2026-05-15 
- Dropped unused supplier columns: SUFollowup, SUCode, SUCurDedExRate, SUCurExRate, SUCURID, SUCurReverse, SUNoPhonePrefix, SUContact2, SUDateLast, SUAccount, SUTerms, SUFedTaxID, SUStateTaxID, SUEMail2, SUZipcode, SUState, SUCity (confirmed absent from Go app and VBA codebase)
- SUCity removed from Go model, queries, forms, and templates; supplier search API now joins CN for city
- Added TODO notes on SUWeb and SUContact1 for future removal (superseded by attachments and CN respectively)
- Supplier edit form: live contact details panel (phone, email, city) below default contact dropdown, updates on selection change
- Supplier detail view: dedicated "Default Contact Information" section showing name (linked), phone, email, city joined from CN

## [0.3.7] - 2026-05-15 
- SQL schema cleanup: VARCHAR expansions (FIL, CN, PO, POL), DATE→DATETIME on date_modified/CNDateModified/PNDateModified, DEFAULT GETDATE() on date columns
- Added DEFAULT values to LNK.LNKUse, LNKChoice, LNKCurrentCost; PO.is_active; FIL.order_id
- Added new columns: supplier.SUZipcode/SUState, POL.POLRev, price.price_type
- Added schema_diagram.md ER diagram (new file)
- Diagram updated: supplier_attachment entity and relationships added
- PO.sql: added CREATE SEQUENCE DDL for PO_Number_Seq
- TODO annotations: unsafe changes flagged with verification queries; unused columns flagged (supplier currency fields, SUFollowup, SUCode, SUNoPhonePrefix, PO.date_printed)

## [0.3.6] - 2026-05-15 
- Part attachments: soft-delete (is_active flag); Delete button with confirm on attachments tab
- Part attachments: fixed PartSetPrimaryAttachment to use proper int parameters
- Supplier attachments: default attachment (primary_attachment_id on supplier table); star/Set UI matches parts
- Shared helpers: softDeleteAttachment + setPrimaryAttachment in handlers/attachments.go used by both
- Fixed `not` template function to accept any type instead of requiring bool (was crashing notes_select)

## [0.3.5] - 2026-05-15 
- Suppliers: new Attachments subtab for files and URLs (SUFIL / SUFIL_Test tables)
- Settings: SUPPLIER_FILES_ROOT path field with Browse button (falls back to DOC_CONTROL_ROOT if blank)
- File serving: /supplier-local/* and /supplier-local-dir/* routes for supplier file access

## [0.3.4.2] - 2026-05-14 
- PO edit: supplier and receiver typeahead now validates on blur and blocks save if no valid selection made
- PO new: fixed broken JS caused by {{len .POItems}} on nil — all JavaScript now initializes correctly
- PO new/edit: supplier and receiver are required fields; empty submission blocked with error banner
- Settings: version displayed as "Arx Parts Master vX.Y.Z"
- AppVersion constant in config.go drives both settings display and static asset cache-busting URL
- CSS: .form-input.input-invalid style for red border on invalid typeahead fields

## [0.3.4] - 2026-05-14 
- Removed parts_master_web (Ruby/Sinatra app) — superseded by parts_master_go

## [0.3.3] - 2026-05-14 
- Fixed supplier edit template error (not on []ContactSummary — use len check instead)
- Settings: Default Receiver dropdown now shows suppliers (not contacts) since receiver_id is a supplier ID
- Settings: PO default contact/receiver dropdowns now inside form so they save correctly
- Filter performance: replaceChildren replaces show/hide loop — INP drops from 2000ms to 40ms
- Filter: cell text cached at page load (initRows) — no DOM reads per keystroke
- CSS: contain:layout style on .table-wrapper isolates table repaints from page gradient/shadow
- Cache-busting ?v= query string on static assets to force browser refresh after rebuild

## [0.3.2] - 2026-05-14 
- templates/ and static/ embedded into binary via go:embed — distribute exe only, no supporting folders required
- config/local.json and .env still read from the exe's working directory at runtime

## [0.3.1] - 2026-05-14 
- Settings page: DB connection fields (server, name, user, password) and file path overrides editable in UI
- DB password removed from .env — stored only in gitignored config/local.json; app prompts for it on first run
- All app routes redirect to /settings when no database connection is active (RequireAuth middleware)
- Auto-reconnects on startup if local.json contains a saved password
- Backward compat: existing DATABASE_DSN in .env is parsed to extract server/db/user (password still required via UI)

## [0.3.0] - 2026-05-14 
- Added parts_master_go: full Go port of parts_master_web
- Standalone Windows .exe — no Ruby/gems required
- System tray icon with Open/Quit; auto-opens browser on launch
- All read views: parts, suppliers, contacts, purchase orders, BOM, where-used, attachments, pricing, order history
- All edit/create/delete forms: parts, suppliers, contacts, purchase orders with line items
- PO sequence number, folder creation, duplicate, print, note, folder browser
- Local file and directory serving with path-traversal protection
- Supplier/part/contact search API endpoints for PO edit autocomplete
- CSS custom properties in static/app.css for easy retheme; JS in static/app.js

## [0.2.61] - 2026-05-08 
- Added changelog update instructions to CLAUDE.md

## [0.2.60] - 2026-05-08 
- PO folder name appends -testmode suffix when running in test mode

## [0.2.59] - 2026-05-08 
- PO folder tab replaces Open Folder button; folder listing shows PO header and sub-tabs
- New PO creation auto-creates folder in PO_FOLDER_ROOT named "<number> <SUSupplierCode>"

## [0.2.58] - 2026-05-08 
- Added PO folder subtab: GET /po/:id/folder serves directory listing from PO_FOLDER_ROOT
- Folder lookup matches any directory starting with the PO number (e.g. "4309 Acme")

## [0.2.57] - 2026-05-08 
- Duplicate PO: added "Duplicate PO" button on PO detail/edit view; pre-fills new PO form with supplier, ship-to, line items, and costs from the source PO

## [0.2.56] - 2026-05-08 
- Attachment CRUD: add, edit, and set-primary attachment actions now work on the part attachments tab

## [0.2.55] - 2026-05-08 
- Folder attachments: LOCAL:path\to\folder\ syntax opens a server-side directory listing with file links and a Copy Path button

## [0.2.54] - 2026-05-08 
- Fixed assign_po_fields writing to non-existent columns; supplier/receiver detail fields (address, email, phone, etc.) now save correctly when creating or editing a PO

## [0.2.53] - 2026-05-08 
- Settings view accessible via gear icon in app header
- Changelog rendered from CHANGELOG.md in settings view

## [0.2.52] - 2026-05-08 
- PO supplier schema improved

## [0.2.51] - 2026-05-08 
- Added PO_Test number sequence to remove race condition

## [0.2.50] - 2024-07-18 
- Updating PO's now refresh PO List table

## [0.2.48] - 2023-08-10 
- Updated Qty on Parts List to 5 decimal places

## [0.2.48] - 2023-07-21 
- Added Internal Notes to PO_Test Form

## [0.2.47] - 2023-07-21 
- Creating new Contact doesn't prompt for "SUID" anymore, prompts for "Supplier"

## [0.2.46] - 2023-07-21 
- Fixed "Update Source Costs" where it would only go to a few decimals

## [0.2.45] - 2023-05-25 
- Fixed re/deactivate on PN and CN tables

## [0.2.44] - 2023-02-07 
- Fixed bug on double-click date in PO (vartypecheck error)

## [0.2.43] - 2023-02-03 
- Reduced min PO items from 5 to 1

## [0.2.42] - 2023-02-03 
- Refactored Link creation and viewing

## [0.2.41] - 2023-02-02 
- Fixed issue with new contact creation. also added button to update associated supplier for contact

## [0.2.40] - 2023-02-02 
- Fixed Part Info dbl-click on Supplier bug

## [0.2.39] - 2023-02-01 
- Added Discount Qty for parts onto Parts List

## [0.2.38] - 2023-01-30 
- Fixed bug with adding Part Link to preexisting file from Doc Control

## [0.2.37] - 2023-01-27 
- Add Release Notes now had default incremented version

## [0.2.36] - 2023-01-27 
- Pricing List now handles "Cancel" or "X" on both popups

## [0.2.35] - 2023-01-26 
- Expanding BOM Item now shows Ext Cost with Links

## [0.2.34] - 2023-01-25 
- minor cleanup and refactor

## [0.2.33] - 2023-01-19 
- Doubleclick on File now opens location of file in FileExplorer

## [0.2.32] - 2023-01-19 
- Assy cost now updates upon updating. also upon refreshing

## [0.2.31] - 2023-01-19 
- Fixed where export/backup to csv was only doing limited columns

## [0.2.30] - 2023-01-03 
- Initial draft release of Quanity Pricing

## [0.2.25] - 2022-12-28 
- Purchase History refreshes every time you access it now

## [0.2.24] - 2022-12-28 
- Fixed issue on double-clicking merged cells; Contact issue, PO Inactivate button. ADDED Ext. Costs and FIL Links to Parts List

## [0.2.23] - 2022-11-03 
- Updated dbcharlimit to include more supplier stuff

## [0.2.22] - 2022-10-29 
- Updated PO Form to use Supplier/Contact objects properly

## [0.2.21] - 2022-10-22 
- Updated supplier stuff to use the supplier object more

## [0.2.20] - 2022-10-20
- Removed QuerySUInfo in favor of using Supplier Object

## [0.2.19] - 2022-10-15
- admin Columns now autohide based on userConfig

## [0.2.18] - 2022-10-15
- Fixed bug on PO creation not saving order date

## [0.2.17] - 2022-10-15
- Made default contact more robust on Supplier sheet

## [0.2.16] - 2022-10-14
- Improved sqlExecute logging

## [0.2.15] - 2022-10-14
- Added admin mode to release notes

## [0.2.14] - 2022-10-14
- Added Copy Link to Part Info. So now you can select a link, hit that button to copy it to clipboard for ease of opening in other applications (or attaching to emails)

## [0.2.13] - 2022-10-11
- sqlExecute updates and added release note functionality

## [0.2.12] - 2022-10-02
- Fixed minor release_notes bug

## [0.2.11] - 2022-10-02
- Replaced Supplier Info with Default Contact info

## [0.2.10] - 2022-10-01
- Added prompt to link new Contact with Supplier

## [0.2.9] - 2022-09-30
- Solo and Bulk methods to add Part to Parts List

## [0.2.8] - 2022-09-29
- Added ability to replace part on BOM with new part

## [0.2.7] - 2022-09-23
- Added ability to add HTTP link to Part via clipboard

## [0.2.6] - 2022-09-17
- Updated Logging table and columns to new schema

## [0.2.5] - 2022-09-17
- Fixed column name bug

## [0.2.4] - 2022-09-15
- added release notes to database and Show Release Notes button to UI/Settings

## [0.2.3] - 2022-09-15
- Fixed bug where you couldn't add a PO Line Item if Line Item had no assigned supplier in PartInfo

## [0.2.2] - 2022-09-15
- Fixed bug where when creating new Supplier it didn't filter onto the supplier

## [0.2.1] - 2022-09-15
- When adding new File/Link it will auto-increment the Order

## [0.2.0] - 2022-09-15
- Initial test release
