# Architecture review — 2026-09-24

A fresh look at the whole repo: about 23k lines of non-test Go, 14k lines of tests, 72 templates and two DDL trees. It covers structure only. I didn't hunt for bugs or change any code. Items are ranked by leverage: how much other work each one unblocks or removes.

Assumption: several people use Arx at the same time against one shared database, as the `users` table, approvals, and the shared-OneDrive exe work (#731/#732) suggest.

## Summary

| # | Change | Size | Why now |
|---|---|---|---|
| 1 | One central Arx server instead of a localhost exe per user | XL | Decide before #24 Phase B, because most items below depend on it |
| 2 | A single hard Postgres cutover, then delete the dialect layer and write native SQL | L | Already planned for v0.8, and running two engines side by side is the costliest state to stay in |
| 3 | Make migrations the only schema source, applied by the binary | M | A new table touches 6 places today |
| 4 | Move SQL out of handlers into a data-access layer (sqlc) and drop `cfg.*Table()` | L | One `Handler` has 429 methods, and CI never runs the tests that exercise the SQL |
| 5 | Put each business operation in one function that owns its transaction | M | Some writes can half-finish today (e.g. `SaveResults`) |
| 6 | Store timestamps as UTC `timestamptz` and let the DB assign them | S–M | The Postgres DDL being written now copies the same problem |
| 7 | Use a decimal type for money and quantities end to end | M | Go uses `float64` over `DECIMAL` columns |
| 8 | Keep reference data in tables, not JSON in `app_config` | M | The categories settings page and a DB CHECK constraint contradict each other |
| 9 | Design one audit/versioning mechanism before the v0.9 features | M (design) | Six roadmap items need it, and there are five one-off mechanisms today |
| 10 | Merge `arxlib` and `go.work` into one module | S | Left over from the two-app era |
| 11 | Keep runtime state in one immutable snapshot | S | Data races on `h.cfg` and several cached fields |
| 12 | Parse templates once and give pages typed data | S–M | Templates are re-parsed on every request |

The suggested order is at the end.

---

## 1. Run Arx as one server, not one localhost server per user

**Current state.** Each user runs their own `Arx.exe`, bound to `127.0.0.1` ([main.go:59-62](../arx_go/main.go#L59-L62), [`RequireLocalHost`](../arx_go/handlers.go#L694)). Each copy stores the shared DB credentials in that user's `%APPDATA%\Arx\local.json` and talks directly to the shared DB. The StartOS plan (#24) keeps this model: Postgres moves to the box, while `Arx.exe` stays on each desktop "for filesystem access".

**What this model costs:**
- **Permissions are only advisory.** The client enforces every permission check: admin-only routes, `can_approve_po`, `can_approve_records`, record and form locks, and the admin-only named-query editor. Every client also holds the credentials to go around them. [named_query.go:35-39](../arx_go/named_query.go#L35-L39) admits this: "the real control is DB-level". Shop-wide secrets, such as the DigiKey client secret in `app_config`, are readable by every user.
- **Caches and locks assume a single process.** Categories, the company logo and the DigiKey credentials load once at startup ([main.go:49-52](../arx_go/main.go#L49-L52)). When an admin changes one, other users don't see it until they restart. The same applies to `userCache` (60 s), `lockPartForThumbnail` ([api.go:430-445](../arx_go/api.go#L430-L445)) and the schema-version check, which runs only at startup. Each exe has its own copy.
- **Postgres has to accept connections from the whole LAN**, with one shared password on every desktop. In a server model it would only accept connections from one local process.
- **Stored file paths only work on identically set-up PCs.** The four file-root settings (`DOC_CONTROL_ROOT`, `PO_FOLDER_ROOT`, `SUPPLIER_FILES_ROOT`, `IMAGE_ROOT`) resolve separately on each PC (`%USERPROFILE%` expansion, #731). The DB stores paths that only work if every PC syncs the same folders to the same place.
- **Some roadmap goals are out of reach.** A mobile-friendly UI (#16) and a JSON API (#15) don't help much when the server only answers on `localhost`. A technician on the shop floor can't enter a test record from a tablet.
- **Some code exists only because of this model:**
  - the first-run and broken-connection escape hatches (`RequireAuthOnceConnected`, `RequireAdminOnceConnected`, `canEditConnection`, `dbUnusable`)
  - editing the DB connection from the web UI
  - the per-user secrets store and its migration (#732)
  - the `TEST_MODE` second connection profile
  - systray, `-H windowsgui` and the cgo dependency on Linux
  - the DNS-rebinding guard

**Proposed change.** Ship Arx as a server that runs next to Postgres, either in the same StartOS package or on any shop machine. Users reach it in a browser over the LAN, with TLS. The same binary still works as a single-user local install, since it's already a web server.
- The DB credentials live in one place, and the DB only accepts connections from Arx. Permission checks become real.
- Caches, locks and the schema check become correct again, because only one process runs.
- Migrations can run at startup (item 3).
- **Files:** the server owns file storage. It can mount the existing network share (so `DOC_CONTROL_ROOT` becomes a server path), or attachments can move into a store on the server. The features that act on the user's own PC go away: `POOpenFolder`, which opens Explorer on that PC, and the native folder picker ([arxlib/folderpick](../arxlib/folderpick/)). They are replaced by browse, download and upload, which PO and supplier folders already have.
- Code that can then be deleted: the DB-connection UI in Settings, the per-user secrets store, the `Test*` connection profile (a test setup becomes a second deployment) and the systray.

**Cost and risk.** XL, but most of it is deletion plus file storage. The real loss is clicking to open a folder in Explorer, and linking to arbitrary files on a user's own disk. Check how much the shop relies on those before deciding.

**Timing.** This is a decision rather than code. Make it before #24 Phase B: packaging Postgres by itself for LAN clients locks in the current model.

**Related issues:**
- #189 Decide deployment model: central Arx server
- #24 Package Postgres as a StartOS service
- #15 JSON endpoints for external clients
- #16 Mobile-responsive CSS
- #26 Backup/restore feature
- #167 Host-header allowlist / DNS rebinding (closed)
- #103, #146 Named-query SELECT-only guard (closed)
- #104, #119 Backup ZIP and secret rows in `app_config` (closed)
- #60 DigiKey client ID/secret in `app_config` (closed)
- #120 Admin authorization expressed three ways (closed)

---

## 2. Cut over to Postgres in one step, then remove the dialect layer

**Current state:**
- Queries are written in T-SQL. For Postgres, the app rewrites them at runtime with regular expressions ([dialect.go:163-177](../arxlib/db/dialect.go#L163-L177)). The code comments note that this rewrite also changes quoted text and named-query SQL.
- 182 `h.dia()` call sites insert `BoolLiteral`, `TopClause`/`LimitClause`, `BoolFromCondition` and similar pieces into format strings.
- There are two DDL trees (`SQL/azure`, `SQL/postgres`) and two seed files.
- The migration rules are built around the Azure portal: no `GO`, and new columns wrapped in `EXEC(N'...')`.

**Proposed change.** Treat #29 as one switch-over date rather than a long period of running both engines. Migrate ArxProd's data once, then:
- delete `Dialect`, `Rewrite`, `SQL/azure/` and the SQL Server driver;
- rewrite queries in native Postgres (`$1`, `RETURNING`, `boolean`, `LIMIT`, `ON CONFLICT`). This is mechanical work, and item 4 is the natural place to do it;
- run named queries through a read-only DB role, instead of relying on the `isSafeQuery` regex.

The Postgres DDL is open for editing anyway, and the current port copies these problems. Fix them there:
- timestamp types (item 6)
- a foreign key for part categories (item 8)
- the legacy `SU*` column names
- dead tables and columns: `logs` and `release_notes` have no Go references, plus `SUWeb`, `SUContact1` and `is_lot_tracked` (#31)

**Move CI integration tests (#19) from v0.9 into v0.8.** With a `postgres` service container and the seed data, the ~155 `-tags integration` tests would run on every PR. Today they run only by hand, against the shared ArxDev database, which a person has to reseed. So CI never tests almost any of the SQL.

**Related issues:**
- #21 Postgres support, migrate off Azure/SQL Server
- #28 Postgres integration close-out
- #29 Phase 3: Postgres-only cutover
- #32 Postgres dialect gaps found by integration tests
- #33 Postgres serial allocation fails on `UPDLOCK, HOLDLOCK`
- #31 `is_lot_tracked` cleanup
- #19 Automate integration tests in CI
- #92 Remove dead `_Test` clone tables (closed)
- #146 Named-query guard is a denylist (closed)

---

## 3. Make migrations the schema, and have the binary apply them

**Current state:**
- Adding a table means editing the Azure DDL, the Postgres DDL, the Azure seed, the Postgres seed, a `cfg.*Table()` helper and `SCHEMA.md`, then writing a migration that someone pastes into the Azure portal.
- The `schema_migrations` ledger (#48) exists, but only a lint test reads it.
- The check that the binary matches the DB is separate: an `ExpectedSchemaVersion` bumped by hand ([config.go:21](../arxlib/config/config.go#L21)).

**Proposed change:**
- Embed the migrations with `//go:embed`. Apply pending ones at startup while holding `pg_advisory_lock`, which is safe once there is only one server (item 1). If you want a person to approve each run, apply them from an admin button instead. Until item 1 ships, use the button: exes of different versions starting up would otherwise race each other.
- Replace `ExpectedSchemaVersion` with a check that the latest migration this build knows about has been applied.
- Drop the hand-kept reference DDL. If a readable schema is still useful, generate one file with `pg_dump --schema-only` and have CI fail when it's out of date. Move the column notes from `SCHEMA.md` into `COMMENT ON COLUMN` statements in the migrations.
- Turn the seed data into a fixture file that the test harness loads after migrating.

#91 sits under "Later" on the roadmap. After the cutover it's a small job, and it removes the most error-prone manual step in the process.

**Related issues:**
- #91 Migration runner (goose)
- #48 Versioned migrations ledger (closed)

---

## 4. A data-access layer: move SQL out of handlers

**Current state:**
- `package main` holds about 23k lines and 104 struct types, and `*Handler` has 429 methods.
- Handlers build SQL with `fmt.Sprintf` (329 sites).
- There are 522 calls to `cfg.*Table()` helpers that just return constant names. They survive from when test mode used `_Test`-suffixed tables, and CLAUDE.md already notes they always return bare names.
- Scanning rows into structs is written by hand, with 295 `sql.Null*` uses.
- Queries get copied: the step-loading query appears in `loadSteps` ([records.go:1150](../arx_go/records.go#L1150)) and again inline in `FormDef` ([:478](../arx_go/records.go#L478)), `EditFormDef` ([:770](../arx_go/records.go#L770)) and `SaveResults` ([:2489](../arx_go/records.go#L2489)).
- Only integration tests would catch a renamed column, and CI doesn't run them.

**Proposed change.** Do this after item 2, because it assumes a single DB engine.
- Adopt **sqlc**: queries live in `.sql` files, and sqlc generates typed Go code from them, checked against the migrations. Column and type mistakes become compile errors. sqlc is not an ORM, and the SQL stays easy to read.
- Drop `cfg.*Table()` and write plain table names in the SQL (sqlc needs this anyway).
- Organize code by domain under `internal/` (`parts`, `purchasing`, `inventory`, `records`, `auth`, `web`). Each domain gets a store and a service. Handlers only parse the request, call the service and render the result.
- Convert one domain at a time, starting with whichever one you touch next.

**Related issues:**
- #190 Data-access layer (sqlc), drop `cfg.*Table()`
- #19 Automate integration tests in CI
- #92 Remove dead `_Test` clone tables (closed)

---

## 5. Each business operation owns one transaction

**Current state.** Each handler decides its own transaction boundaries.
- **Half-finished writes are possible.** For example, `SaveResults` writes result rows with plain `h.execContext` ([records.go:2591](../arx_go/records.go#L2591), [:2608](../arx_go/records.go#L2608)) and only opens its transaction later ([:2658](../arx_go/records.go#L2658)). If anything fails after that point, the results stay saved but the record's other fields and any build don't.
- **The locked-record check is a separate read.** It runs before the writes, and nothing stops a lock from landing between the check and the writes.
- **Stored totals and counts are kept up to date in two different ways.** Triggers maintain `attachment_count`, `po_line_count`, `SUNumOfLNKs` and `SUNumOfPOs`. App code maintains `stock_on_hand` ([inventory.go:45](../arx_go/inventory.go#L45)).

**Proposed change:**
- With item 4's service layer, write each operation (`ReceivePO`, `SaveResults`, `Build`, `ConvertRFQ`) as one function that takes a transaction and enforces its own rules.
- Make writes conditional instead of read-then-write, for example `UPDATE ... WHERE NOT is_locked`, then check how many rows changed.
- Pick one approach for stored counts and totals: all triggers, or all app code inside the same transaction. Or drop the stored values and compute them. At this data size, Postgres can `COUNT` over indexed foreign keys without trouble.

**Related issues:**
- #191 Each business operation owns one transaction

---

## 6. Timestamps: UTC `timestamptz`, assigned by the database

**Current state.**
- Columns are `DATETIME` with no time zone, and they are filled from two different clocks:
  - `GETDATE()` in SQL (36 sites), which is the DB server's clock (Azure SQL runs in UTC);
  - the user's local `time.Now()` sent as a parameter ([inventory.go:40](../arx_go/inventory.go#L40), [lot.go:52](../arx_go/lot.go#L52), [build.go:287](../arx_go/build.go#L287), [contacts.go:223](../arx_go/contacts.go#L223), [parts.go:668](../arx_go/parts.go#L668)).
- Every desktop's clock and time zone can differ.
- #847's per-user time zone is built on top of this.
- The Postgres DDL maps these columns to `TIMESTAMP`, still without a zone, so it carries the problem over.

**Proposed change.**
- Use `timestamptz NOT NULL DEFAULT now()` for every audit or event column, and stop sending `time.Now()` for them.
- Convert to the user's time zone only when displaying.
- Dates that users type in (PO date requested, record date) stay `date`.

The cheapest time to do this is in the Postgres DDL, before cutover.

**Related issues:**
- #192 UTC `timestamptz` assigned by the DB

---

## 7. A decimal type for money and quantities

**Current state.** The DB uses `DECIMAL(16,8)` and `DECIMAL(13,6)`, but Go carries every value as `float64`: `Price.PriceEA`, the PO fields `Tax1`, `ShippingCost` and `TotalCost`, the line fields `UnitCost` and `Qty`, `CurrentCost`, `LastRollupCost`, `StockOnHand`, and the template's `mul` function. Rollup costs and PO totals pick up rounding errors from binary floating point.

**Proposed change.** Use a decimal type (`shopspring/decimal` or `pgtype.Numeric`) in models and arithmetic. Do it alongside sqlc (item 4), which regenerates these types anyway.

**Related issues:**
- #193 Decimal type for money and quantities

---

## 8. Reference data in tables, not JSON in `app_config`

**Current state:**
- Part categories are stored as a JSON blob that users can edit in Settings ([categories.go:42-72](../arx_go/categories.go#L42-L72)). But `part.category` has a fixed `CHECK (category IN ('', 'ASM', 'BUY', ...))` ([SQL/azure/part.sql:28](../SQL/azure/part.sql#L28)), and the Postgres port copies it. So the DB rejects a part saved with a category that was added in Settings.
- Attachment categories and the part-numbering settings use the same JSON-blob pattern.
- Every exe caches its own copy of this data.

**Proposed change:**
- Add a `part_category` table (code as primary key, label, tab flags), with a foreign key from `part.category`. Do the same for attachment categories.
- Keep `app_config` for true single-value settings. #17 covers part of this.

**Related issues:**
- #194 Category tables with FK instead of JSON in `app_config`
- #17 `app_config` is a flat key-value table
- #14 `PNUser1-10` flat EAV columns
- #9 Custom field labels (`PNUser1-10`)
- #119 Secret rows in `app_config` (closed)

---

## 9. One audit and versioning mechanism, designed before the v0.9 features

**Current state.** There are at least five separate history mechanisms:
- `purchase_order_history`: an event log written by app code
- `form_row_history`: snapshots taken by a trigger, which gets the user's name from `CONTEXT_INFO` on SQL Server or the `arx.username` setting on Postgres
- `form_events` and `record_events`: event logs written by app code
- `record_event_results`: a snapshot of results at completion
- every `result` row: copies about 12 step-definition columns (the #487 frozen snapshot)

The v0.9 plan then adds six features that each need a versioned record plus who changed it and when: #7, #8, #1 (ECO), #12 (change log), #25 (multi-table audit) and #18 (versioned form definitions).

**Proposed change.** Settle the design once, in #25, before building those features:
- **For "who changed what":** one generic row-audit trigger that writes to `audit_log(table, row_id, op, old jsonb, new jsonb, actor, at)`, reusing the existing `arx.username` setting.
- **For things other rows point to by version** (form definitions, part revisions): explicit version tables. Records then reference a `form_version_id` instead of copying spec columns into every result row.
- Build #18, then #8, then #1 on top of that design.

Building the six features one at a time is likely to add a sixth and seventh mechanism.

**Related issues:**
- #25 Multi-table audit / `test_definition_history` trigger design
- #18 Revision-controlled definitions instead of result snapshots
- #12 Audit / change log
- #8 Part revision history (ECO log)
- #1 ECO process
- #7 Alternate / substitute parts

---

## 10. Merge `arxlib` and `go.work` into one module

This is left over from merging the two apps:
- `arxlib` is a separate module with only one user.
- `Base` is described as "fields common to all Arx apps", and `SessionCookieName` as "shared by all Arx apps".
- `go build ./...` fails when run from the repo root.
- CI runs every step twice, once per module.
- `render` hard-codes `Title = "Arx Parts Master"`.

Use one `go.mod` at the root and move `arxlib/*` to `internal/*`. It's a small, mechanical change. Do it before item 4 starts moving packages around.

**Related issues:**
- #195 Merge `arxlib` and `go.work` into one module

---

## 11. Runtime state: one immutable snapshot

**Current state:**
- `SettingsSave` assigns `h.cfg.*` fields ([settings.go:468-545](../arx_go/settings.go#L468-L545)) while other requests may be reading them.
- `schemaMismatch`, `dbConnError`, `companyLogo` and `partCategories` are plain fields that get written while the app runs.
- #757 made only the DB connection pointer atomic.

**Proposed change:**
- Put the config, the connection and the cached reference data into one struct behind an `atomic.Pointer`, and rebuild and swap it on save.
- Add `go test -race` to CI.

Item 1 removes most of the code that changes this state (the DB connection is no longer edited from the UI), so this item may shrink.

**Related issues:**
- #196 Runtime state snapshot, `-race` in CI

---

## 12. Templates: parse once, typed page data

**Current state:**
- `render` re-parses the layout, the partials and the page from the embedded files on every request ([handlers.go:619-623](../arx_go/handlers.go#L619-L623)), and `renderPrint` does the same.
- Page data is a `map[string]any` (132 sites), with about 10 keys that `render` adds by name.

**Proposed change:**
- Parse every page once at startup and keep them in a map. A broken template then fails at startup instead of on the first visit to that page.
- Give pages typed data structs that embed a shared `Layout` struct.
- Convert one page at a time.

**Related issues:**
- #197 Parse templates once, typed page data

---

## Deliberately left alone

- Server-rendered `html/template` with Bootstrap and a little vanilla JS. The size is right, and an SPA isn't needed.
- chi, gorilla sessions and bcrypt.
- The single-binary build, and `go-pdfium` running on wazero (pure Go, which helps cross-platform builds).
- List pages that load every row and filter in the browser. This is fine at shop scale; revisit it past tens of thousands of parts.
- No ORM. sqlc is not one.

## Suggested order

0. **Anytime (small and independent):** item 10, item 12, and `-race` in CI.
1. **Decide item 1**, the deployment model. It changes what #24 packages.
2. **v0.8 cutover (item 2).** Build items 6 and 8 and the dead-column cleanup into the Postgres DDL, and run the integration tests in CI against a Postgres container (moving #19 up).
3. **Item 3**, the migration runner, right after the cutover. Use the admin-button form until item 1 ships.
4. **Implement item 1:** server-side file storage, then delete the connection UI, the secrets store, the test-mode connection profile and the systray.
5. **Items 4, 5 and 7**, one domain at a time, starting with whatever v0.9 touches first. Item 11 as it comes up, if item 1 hasn't already made it moot.
6. **Item 9's design**, before any v0.9 audit, revision or ECO work.

Much of CLAUDE.md's rule set exists to work around items 1–4, and those rules disappear once they're done: the six places to update per table, the Azure-portal batch rules, `cfg.*Table()`, the dialect helpers, the per-user secrets store and the test-mode connection profile.
