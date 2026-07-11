# Design: Migrate Arx off SQL Server to Postgres + SQLite (#625)

Status: draft for review
Date: 2026-07-10
Issue: [#625](https://github.com/Jolls/arx-legacy/issues/625) - "Postgres + SQLite support (drop Azure/SQL-Server-only requirement)"
Companion inventory: [docs/plans/tsql-inventory.md](./tsql-inventory.md)

## Scope decisions

These were settled before designing. They matter because they change the shape of the work.

- **End state: Postgres + SQLite only.** SQL Server support - and the `go-mssqldb` driver, and all T-SQL DDL - is removed once Postgres parity is reached and the live data is migrated. This is a genuine *migration off* SQL Server, not just "support more engines." Note this is broader than the issue comment's "second/third supported engines" framing - it adds a data-migration and cutover dependency and eventually deletes the SQL Server code path entirely.
- **Strategy: dual-dialect abstraction, gradual cutover.** Build a dialect seam that keeps the app running on SQL Server (ArxProd) throughout, add Postgres alongside it, validate, cut over, then delete SQL Server. This is the only path that keeps the live parts system safe during a database replacement, and the dialect layer it produces is exactly what SQLite needs afterward. (A big-bang Postgres rewrite was rejected: no fallback during cutover, near-impossible to test incrementally. A query builder / ORM was rejected in the issue's decision comment: fights the debug-mode SQL logging, more machinery than a 2-engine target justifies.)
- **Phase order: Postgres first, SQLite second.** Postgres is semantically closer to SQL Server (real types, sequences, `RETURNING`, row-level triggers), so it is the gentler first port. SQLite's quirks (no real type system, no sequence object, session-var-less triggers) come second, on top of a proven layer.
- **Schema provisioning: SQLite auto-bootstraps; Postgres stays human-run reference DDL for now.** Auto-bootstrapping every engine is a later, separate step.
- **Data migration (ArxProd -> Postgres) is out of scope for this plan.** It is named as the cutover dependency but tracked as a separate effort.

## 1. Architecture: the `Dialect` seam

One new abstraction lives in `arxlib/db`: a `Dialect` interface. The existing query choke points do the translation work, so the ~110 inline-SQL call sites barely change.

```
config.Engine ("postgres" | "sqlite" | "sqlserver")
      |
      v
db.Connect(engine, dsn) --> selects driver + returns a Dialect
      |
      v
Handler holds a Dialect --> h.queryContext / execContext / beginTx
                              |  (already the single SQL choke point)
                              +- Tier 1: rewrite @pN -> $N / ? / @pN, GETDATE() -> now-token
                              +- Tier 2: structural bits call dialect.X() helpers
```

**Two-tier translation** is the central design decision:

- **Tier 1 - central, safe textual rewrites**, applied inside the existing `queryContext`/`execContext`/`txLogger` wrappers and driven by the active dialect. Two transforms only, both provably safe:
  - `@pN` placeholder markers (already numbered by go-mssqldb convention) -> `$N` (Postgres) / `?` (SQLite) / unchanged (SQL Server). A clean regex over `@p\d+`. Touches zero of the hundreds of call sites.
  - `GETDATE()` -> the dialect's current-timestamp token. A distinctive token, negligible collision risk. Covers ~30 inline uses with no call-site edits.
- **Tier 2 - explicit call-site helpers** for the structural constructs that cannot be safely regex-rewritten. These become `dialect.X(...)` calls - the "thin, targeted helpers" from the issue's decision comment, not a query builder.

**Drivers:**

- Postgres: `github.com/jackc/pgx/v5/stdlib` (registers a `database/sql` driver).
- SQLite: **`modernc.org/sqlite` - pure Go, no cgo.** This is non-negotiable given the `-H windowsgui` single-binary Windows build; a cgo-based SQLite driver would break the cross-compile and single-exe story.
- SQL Server: `github.com/microsoft/go-mssqldb`, retained until cutover, then removed.

## 2. The dialect-sensitive surface

Maps directly to the six categories identified in the T-SQL inventory.

| Category | Today (T-SQL) | Handled by | Postgres / SQLite |
|---|---|---|---|
| Placeholders | `@p1` | Tier-1 rewrite | `$1` / `?` |
| Current time | `GETDATE()` | Tier-1 rewrite | `CURRENT_TIMESTAMP` |
| Pagination | `TOP (@n)` + `OFFSET/FETCH` | `dialect.LimitOffset()` | `LIMIT / OFFSET` |
| Insert + get id | `OUTPUT INSERTED.id` / `SCOPE_IDENTITY()` | `dialect.InsertReturningID()` | `RETURNING id` |
| Upsert (1 site) | `MERGE` | `dialect.Upsert()` | `ON CONFLICT DO UPDATE` |
| Safe int cast | `TRY_CAST(x AS INT)` | `dialect.TryCastInt()` | regex-guard + `CAST` / permissive `CAST` |
| Month-start (1 site) | `DATEFROMPARTS(...)` | `dialect.MonthStart()` | `date_trunc` / `strftime` |

Two inventory items are fixed as **source cleanups** rather than dialect helpers, because removing them is simpler than abstracting them:

- `ISNULL` -> `COALESCE` (4 sites). SQL Server supports both, so this is a behavior-preserving drop-in that can land in Phase 0.
- `'%' + @pn + '%'` LIKE-wildcard concatenation -> build the `%term%` string in Go and pass it as a single parameter. No dialect has a string-concat syntax common to all three engines (SQL Server uses `+`, Postgres/SQLite use `||`, SQLite lacks `CONCAT()` on older builds), so eliminating SQL-level concat is cleaner than wrapping it.

### Notable per-site gotchas carried from the inventory

- **`pos.go` `SCOPE_IDENTITY()` (2-3 sites):** these deliberately avoid `OUTPUT INSERTED.id` because SQL Server blocks that clause on tables with triggers. The `dialect.InsertReturningID()` helper must encapsulate this SQL-Server-specific branch so the quirk does not leak into call sites; on Postgres/SQLite it is a plain `RETURNING id`.
- **`triggers.sql` (5 triggers):** the least portable file in the repo. Denormalized-count maintenance (`company.SUNumOfLNKs/SUNumOfPOs`, `part.attachment_count/po_line_count`) plus the `test_definition_history` audit-snapshot trigger. No mechanical translation exists for T-SQL's `inserted`/`deleted` pseudo-tables and `UPDATE ... FROM ... JOIN`. Full rewrite required (see section 3).
- **PO-number `SEQUENCE`:** `purchase_order.sql` uses `NEXT VALUE FOR dbo.PO_Number_Seq`. Postgres has native sequences; SQLite has no sequence object (needs a counter table or transactional `MAX()+1`).

## 3. Schema / DDL

- **Per-engine DDL trees.** Add `SQL/postgres/*.sql` now and `SQL/sqlite/*.sql` in the SQLite phase. The existing `SQL/*.sql` remains the SQL Server set until cutover, then is deleted.
- **Triggers - the hard part (5 of them).** Do **not** assume a mechanical 1:1 rewrite. The triggers were an Azure/SQL-Server-era default (originally a Claude suggestion for the Azure setup), not a deliberate design choice, so before rewriting anything the trigger phase **starts by reconsulting the implementation owner (Jolls)** on two questions: (1) which of the 5 triggers are actually still needed at all, and (2) whether the survivors should stay triggers or move to something more native to Postgres/SQLite - app-side logic, generated/computed columns, or views - instead of being ported as triggers. Only the triggers that survive that review get rewritten.
  For any that do stay as triggers: full rewrite to row-level PL/pgSQL (`NEW`/`OLD`, `FOR EACH ROW`) for Postgres. The audit trigger's `CONTEXT_INFO()` username recovery would map to a Postgres session GUC (the app sets `SET LOCAL arx.username = ...` per transaction, the trigger reads it via `current_setting()`). **SQLite cannot read session variables inside triggers** - a known constraint that reinforces the "reconsider triggers vs. app-side" review above, and will likely move audit-actor handling app-side for that engine regardless.
- **PO-number sequence.** Postgres native sequence (`nextval()`); SQLite counter-table or transactional `MAX()+1`.
- **SQLite auto-bootstrap.** Embed the SQLite DDL and create the schema on first open when the tables are absent. Postgres provisioning stays manual for now.

## 4. Config / driver plumbing

- New `Engine` field on `config.Base` (`postgres` | `sqlite` | `sqlserver`), default `sqlserver` during the transition, sourced from env / `local.json`.
- `Base.BuildDSN` becomes engine-aware: scheme and query parameters differ per engine, and the SQLite DSN is a file path rather than a `scheme://` URL.
- `db.Connect(engine, dsn)` selects the driver and returns the matching `Dialect`. All three drivers are blank-imported during the transition; go-mssqldb is dropped at cutover.
- The test-mode DB swap (`Base.ActiveDBName`, the ArxDev/ArxProd split) generalizes to a per-engine "active target" so test mode works on each engine.

## 5. Test strategy

- Parametrize the `//go:build integration` suite by engine (env-selected) and add a Postgres ArxDev target. Generalize the existing hard `arxdev`-substring prod-guard to a per-engine "not prod" guard so the safety net survives the driver change.
- Add **pure-function unit tests** for the placeholder/GETDATE rewriter and each Tier-2 dialect helper. These need no database, run in the default `go test ./...` sweep, and form a cheap regression net that catches dialect drift.

## 6. Phasing

Each phase ends at a concrete verification gate. Phases are sequential; nothing in a later phase is needed to prove an earlier one.

- **Phase 0 - Prep cleanups** (behavior-preserving, still 100% on SQL Server):
  `ISNULL` -> `COALESCE`; LIKE-wildcard concat moved into Go parameters; funnel both pagination idioms through a single (SQL Server) pagination helper; document the `SCOPE_IDENTITY` trigger gotcha in `SQL/schema.md`.
  **Gate:** `go test ./...` green; no behavior change on ArxProd/ArxDev.
- **Phase 1 - Dialect scaffolding:**
  `Engine` config + engine-aware DSN + driver selection in `db.Connect`; the `Dialect` interface with a **SQL Server implementation that reproduces today's behavior exactly**; the Tier-1 rewriter wired into the wrappers; the Tier-2 structural categories routed through `dialect.X()` helpers.
  **Gate:** the app still runs on SQL Server and the integration suite is green under the SQL Server dialect - proving the seam is behavior-preserving before any new engine exists.
- **Phase 2 - Postgres dialect:**
  Postgres implementation of every helper; `SQL/postgres/*.sql` DDL; the PO-number sequence. **Trigger sub-step starts with a review checkpoint with Jolls** (which triggers are still needed, and triggers vs. native/app-side alternatives - see section 3); only the survivors are rewritten as PL/pgSQL.
  **Gate:** integration suite green against a Postgres ArxDev.
- **Phase 3 - Cutover and SQL Server removal:**
  (Data migration ArxProd -> Postgres is the external dependency here.) Point config at Postgres, validate, then delete the SQL Server dialect, `go-mssqldb`, and the T-SQL `SQL/*.sql` DDL.
  **Gate:** tests green on Postgres only; a grep of the app and schema confirms no T-SQL constructs remain.
- **Phase 4 - SQLite dialect:**
  SQLite implementation of every helper; `SQL/sqlite/*.sql`; SQLite triggers; the sequence substitute; first-run schema auto-bootstrap via the pure-Go driver.
  **Gate:** integration suite green against SQLite; a zero-setup local run (no external DB server) works end to end.

## 7. Out of scope

Named here as dependencies, not built by this plan:

- ArxProd -> Postgres **data migration** (separate effort; the Phase 3 gate depends on it).
- Postgres **auto-bootstrap** provisioning (later step; Postgres stays human-run reference DDL for now).
- Settings-UI engine picker beyond the minimum needed to select an engine.

## Open questions for review

1. Does Jolls agree with the broader "migrate *off* SQL Server" end state (delete go-mssqldb + T-SQL DDL after cutover), versus the issue comment's original "second/third engine" framing that keeps SQL Server supported?
2. Triggers: confirmed with Jolls that the trigger phase reopens the design rather than porting 1:1 - reviewing which of the 5 are still needed and whether survivors should become native/app-side logic instead of triggers (they were an Azure-era default). SQLite's inability to read session variables in triggers feeds directly into this. Resolved in principle; the actual per-trigger review happens at the start of Phase 2.
3. Is `pgx/v5/stdlib` the preferred Postgres driver, or is there a house preference (`lib/pq`)?
