# Design: Migrate Arx off SQL Server to Postgres (#625)

Status: draft for review
Date: 2026-07-10
Issue: [#625](https://github.com/Jolls/arx-legacy/issues/625) - "Postgres + SQLite support (drop Azure/SQL-Server-only requirement)"
Companion inventory: [docs/plans/tsql-inventory.md](./tsql-inventory.md)

## Scope decisions

These were settled before designing. They matter because they change the shape of the work.

- **End state: Postgres only.** SQL Server support - and the `go-mssqldb` driver, and all T-SQL DDL - is removed once Postgres parity is reached and the live data is migrated. This is a genuine *migration off* SQL Server to a single new engine, not "support more engines." Note this is broader than the issue comment's "second/third supported engines" framing - it adds a data-migration and cutover dependency and eventually deletes the SQL Server code path entirely.
- **SQLite is dropped.** The issue title mentioned SQLite; it is explicitly out of scope. That removes the pure-Go-driver build constraint, the first-run auto-bootstrap workstream, the no-session-variables-in-triggers problem, and the sequence-object substitute.
- **Strategy: transitional dual-dialect scaffolding, gradual cutover.** Build a dialect seam that keeps the app running on SQL Server (ArxProd) throughout, add Postgres alongside it, validate against both, cut over, then delete SQL Server. This is the only path that keeps the live parts system safe during a database replacement. Because the end state is a *single* engine, the dialect seam is deliberately temporary scaffolding, not a permanent portability layer - it is collapsed to Postgres-native code at cutover (see Phase 3). A big-bang Postgres rewrite was rejected: no fallback during cutover, near-impossible to test incrementally. A query builder / ORM was rejected in the issue's decision comment: fights the debug-mode SQL logging, more machinery than a single-engine target justifies.
- **Schema provisioning: human-run reference DDL, as today.** `SQL/postgres/*.sql` files, run by a human like the current `SQL/*.sql`. No auto-bootstrap.
- **Data migration (ArxProd -> Postgres) is out of scope for this plan.** It is named as the cutover dependency but tracked as a separate effort.

## 1. Architecture: the transitional `Dialect` seam

One new abstraction lives in `arxlib/db`: a `Dialect` interface with two implementations - SQL Server (current behavior) and Postgres - that coexist only during the migration. The existing query choke points do the translation work, so the ~110 inline-SQL call sites barely change.

```
config.Engine ("postgres" | "sqlserver")
      |
      v
db.Connect(engine, dsn) --> selects driver + returns a Dialect
      |
      v
Handler holds a Dialect --> h.queryContext / execContext / beginTx
                              |  (already the single SQL choke point)
                              +- Tier 1: rewrite @pN -> $N / @pN, GETDATE() -> now-token
                              +- Tier 2: structural bits call dialect.X() helpers
```

**Two-tier translation** is the central design decision:

- **Tier 1 - central, safe textual rewrites**, applied inside the existing `queryContext`/`execContext`/`txLogger` wrappers and driven by the active dialect. Two transforms only, both provably safe:
  - `@pN` placeholder markers (already numbered by go-mssqldb convention) -> `$N` (Postgres) / unchanged (SQL Server). A clean regex over `@p\d+`. Touches zero of the hundreds of call sites.
  - `GETDATE()` -> the dialect's current-timestamp token. A distinctive token, negligible collision risk. Covers ~30 inline uses with no call-site edits.
- **Tier 2 - explicit call-site helpers** for the structural constructs that cannot be safely regex-rewritten. These become `dialect.X(...)` calls - the "thin, targeted helpers" from the issue's decision comment, not a query builder.

Because Postgres is the only surviving engine, at cutover (Phase 3) the SQL Server implementation is deleted and the seam collapses: Tier-1 placeholder rewriting can be baked into the source as `$N`, and the Tier-2 helpers reduce to a single Postgres implementation (kept only if they still earn their keep as readability wrappers). The plan does not carry a one-implementation interface forward for its own sake.

**Drivers:**

- Postgres: `github.com/jackc/pgx/v5/stdlib` (registers a `database/sql` driver).
- SQL Server: `github.com/microsoft/go-mssqldb`, retained until cutover, then removed.

## 2. The dialect-sensitive surface

Maps directly to the six categories identified in the T-SQL inventory.

| Category | Today (T-SQL) | Handled by | Postgres |
|---|---|---|---|
| Placeholders | `@p1` | Tier-1 rewrite | `$1` |
| Current time | `GETDATE()` | Tier-1 rewrite | `CURRENT_TIMESTAMP` |
| Pagination | `TOP (@n)` + `OFFSET/FETCH` | `dialect.LimitOffset()` | `LIMIT / OFFSET` |
| Insert + get id | `OUTPUT INSERTED.id` / `SCOPE_IDENTITY()` | `dialect.InsertReturningID()` | `RETURNING id` |
| Upsert (1 site) | `MERGE` | `dialect.Upsert()` | `ON CONFLICT DO UPDATE` |
| Safe int cast | `TRY_CAST(x AS INT)` | `dialect.TryCastInt()` | regex-guard + `CAST` |
| Month-start (1 site) | `DATEFROMPARTS(...)` | `dialect.MonthStart()` | `date_trunc` |

Two inventory items are fixed as **source cleanups** rather than dialect helpers, because removing them is simpler than abstracting them:

- `ISNULL` -> `COALESCE` (4 sites). SQL Server supports both, so this is a behavior-preserving drop-in that can land in Phase 0.
- `'%' + @pn + '%'` LIKE-wildcard concatenation -> build the `%term%` string in Go and pass it as a single parameter. SQL Server uses `+` for string concat and Postgres uses `||`, so eliminating SQL-level concat entirely is cleaner than branching it.

### Notable per-site gotchas carried from the inventory

- **`pos.go` `SCOPE_IDENTITY()` (2-3 sites):** these deliberately avoid `OUTPUT INSERTED.id` because SQL Server blocks that clause on tables with triggers. The `dialect.InsertReturningID()` helper must encapsulate this SQL-Server-specific branch so the quirk does not leak into call sites; on Postgres it is a plain `RETURNING id`.
- **`triggers.sql` (5 triggers):** the least portable file in the repo - see the trigger review note in section 3.
- **PO-number `SEQUENCE`:** `purchase_order.sql` uses `NEXT VALUE FOR dbo.PO_Number_Seq`. Postgres has native sequences (`nextval()`), so this ports cleanly (syntax only).

## 3. Schema / DDL

- **Per-engine DDL.** Add `SQL/postgres/*.sql`. The existing `SQL/*.sql` remains the SQL Server set until cutover, then is deleted. Both are human-run reference DDL, matching current convention.
- **Triggers - reconsider before rewriting (per Jolls).** Do **not** assume a mechanical 1:1 rewrite. The 5 triggers were an Azure/SQL-Server-era default (originally a Claude suggestion for the Azure setup), not a deliberate design choice. Before rewriting anything, the trigger sub-step **starts by reconsulting the implementation owner (Jolls)** on two questions: (1) which of the 5 triggers are actually still needed at all, and (2) whether the survivors should stay triggers or move to something more native to Postgres - app-side logic, generated/computed columns, or views - instead of being ported as triggers. Only the triggers that survive that review get rewritten, as row-level PL/pgSQL (`NEW`/`OLD`, `FOR EACH ROW`). The audit trigger's `CONTEXT_INFO()` username recovery, if it survives as a trigger, maps to a Postgres session GUC (the app sets `SET LOCAL arx.username = ...` per transaction; the trigger reads it via `current_setting()`).
- **PO-number sequence.** Postgres native sequence (`CREATE SEQUENCE` / `nextval()`); a straightforward syntax port.

## 4. Config / driver plumbing

- New `Engine` field on `config.Base` (`postgres` | `sqlserver`), default `sqlserver` during the transition, sourced from env / `local.json`.
- `Base.BuildDSN` becomes engine-aware: the Postgres scheme and query parameters differ from the SQL Server DSN.
- `db.Connect(engine, dsn)` selects the driver and returns the matching `Dialect`. Both drivers are blank-imported during the transition; go-mssqldb is dropped at cutover.
- The test-mode DB swap (`Base.ActiveDBName`, the ArxDev/ArxProd split) generalizes to a per-engine "active target" so test mode works on Postgres too.

## 5. Test strategy

- Parametrize the `//go:build integration` suite by engine (env-selected) and add a Postgres ArxDev target. Generalize the existing hard `arxdev`-substring prod-guard to a per-engine "not prod" guard so the safety net survives the driver change.
- Add **pure-function unit tests** for the placeholder/GETDATE rewriter and each Tier-2 dialect helper. These need no database, run in the default `go test ./...` sweep, and form a cheap regression net that catches dialect drift.

## 6. Phasing

Each phase ends at a concrete verification gate. Phases are sequential; nothing in a later phase is needed to prove an earlier one.

**PR boundaries:** each phase is one PR, and the gates are the merge points. Phases 0 and 1 ship together (see [docs/plans/625-postgres-migration-plan-phase0-1.md](./625-postgres-migration-plan-phase0-1.md)). The app runs on SQL Server unchanged through the end of Phase 2 (`sqlserver` stays the default engine), so Phases 0-2 are all behavior-preserving merges. Phase 2 is the safe stopping point: both engines work, SQL Server is still the default, and nothing is deleted - the project can sit here indefinitely if you want to stop short of cutover. Phase 3 is the only irreversible merge and is gated on the external ArxProd -> Postgres data migration; do not merge it until that migration exists and has run.

- **Phase 0 - Prep cleanups** (behavior-preserving, still 100% on SQL Server):
  `ISNULL` -> `COALESCE`; LIKE-wildcard concat moved into Go parameters; funnel both pagination idioms through a single (SQL Server) pagination helper; document the `SCOPE_IDENTITY` trigger gotcha in `SQL/schema.md`.
  **Gate:** `go test ./...` green; no behavior change on ArxProd/ArxDev.
- **Phase 1 - Dialect scaffolding:**
  `Engine` config + engine-aware DSN + driver selection in `db.Connect`; the `Dialect` interface with a **SQL Server implementation that reproduces today's behavior exactly**; the Tier-1 rewriter wired into the wrappers; the Tier-2 structural categories routed through `dialect.X()` helpers.
  **Gate:** the app still runs on SQL Server and the integration suite is green under the SQL Server dialect - proving the seam is behavior-preserving before Postgres exists.
- **Phase 2 - Postgres dialect:**
  Postgres implementation of every helper; `SQL/postgres/*.sql` DDL; the PO-number sequence. **Trigger sub-step starts with a review checkpoint with Jolls** (which triggers are still needed, and triggers vs. native/app-side alternatives - see section 3); only the survivors are rewritten as PL/pgSQL.
  **Gate:** integration suite green against a Postgres ArxDev.
- **Phase 3 - Cutover, SQL Server removal, and seam collapse:**
  (Data migration ArxProd -> Postgres is the external dependency here.) Point config at Postgres, validate, then delete the SQL Server dialect, `go-mssqldb`, and the T-SQL `SQL/*.sql` DDL. Collapse the now-single-engine seam: bake `$N` placeholders into source and reduce the Tier-2 helpers to their Postgres path (keeping only those that still read better as wrappers).
  **Gate:** tests green on Postgres only; a grep of the app and schema confirms no T-SQL constructs and no `sqlserver`/go-mssqldb references remain.

## 7. Out of scope

Named here as dependencies or explicit exclusions, not built by this plan:

- ArxProd -> Postgres **data migration** (separate effort; the Phase 3 gate depends on it).
- **SQLite** support (dropped from the issue scope).
- Schema **auto-bootstrap** provisioning (Postgres stays human-run reference DDL).
- Settings-UI engine picker beyond the minimum needed to select an engine during the transition.

## Resolved decisions

All prior open questions are settled (confirmed by Jolls, 2026-07-10):

1. **End state - migrate off SQL Server to Postgres only. Confirmed.** go-mssqldb and the T-SQL DDL are removed after cutover; SQL Server is not retained as a supported engine.
2. **Triggers - reopen the design rather than port 1:1. Confirmed.** The trigger sub-step reviews which of the 5 are still needed and whether survivors should become native Postgres or app-side logic instead of triggers (they were an Azure-era default). The per-trigger review happens at the start of Phase 2.
3. **Postgres driver - `pgx/v5/stdlib`. Confirmed.**
