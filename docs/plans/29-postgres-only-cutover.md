# #29 — Postgres-only cutover

`main` is Postgres-only; SQL Server lives on `release/0.7`. Remove every SQL Server code path,
the Dialect seam, and the runtime `Rewrite()` pass. Version 0.8.3.

## Resolved decisions
- **Placeholder/GETDATE conversion:** one-off token-only script (not committed): `@p(\d+)` → `$\1`
  and `GETDATE()` → `CURRENT_TIMESTAMP` in `*.go` under `arx_go/` and `internal/`. Byte-identical to
  what `postgresDialect.Rewrite` does at runtime today, so no behavior change. Line endings
  preserved. Verified via `git diff --numstat` + a check that every changed line differs only by
  those tokens.
- **Stale `engine`/`test_engine` (local.json) and `DB_ENGINE`/`TEST_DB_ENGINE` (env):** ignored
  silently. Fields removed; `encoding/json` skips unknown keys; next Settings save drops them.
- **Dialect call-site inlining:** a one-off `go/ast` tool (not committed) inlines
  `h.dia().X(<literal args>)` / `d.X(...)` used as `fmt.Sprintf` arguments into the format string
  at the matching `%s` verb and deletes the argument (refuses if the verb isn't `%s`).
  Remaining sites (string concatenation, variables, helpers taking a `Dialect`) by hand.
  Postgres forms: `BoolLiteral`→`TRUE`/`FALSE`; `ToggleBoolExpr(c)`→`NOT c`;
  `TopClause`→`""`; `LimitClause(p)`→` LIMIT p`; `MonthStartExpr`→`date_trunc('month', CURRENT_DATE)::date`;
  `NextSequenceValueExpr(s)`→`nextval('s')`; `BoolFromCondition(c)`→`(c)`;
  `InsertReturningID`/`InsertSelectReturningID`→literal `INSERT ... RETURNING id`;
  `UpsertAppConfig`→literal `INSERT ... ON CONFLICT`; `SetAuditUser`→`SELECT set_config('arx.username', $1, true)` with the username arg;
  `TryCastInt(e)`→`CASE WHEN e ~ '^[0-9]+$' THEN CAST(e AS INTEGER) END`.
  (Post-review: kept as one helper, `serialIntExpr(col)` in records.go, instead of 7 inline copies.)
- **Kept in `internal/db`:** `scanNamedParams`, `NamedParamRefs`, and `RewriteNamedParams` as a
  plain function (named-query `@name` → `$N` is still required). Move to `internal/db/namedparams.go`;
  delete `dialect.go`. `dialect_test.go` → `namedparams_test.go` keeping only named-param tests.
- `SQL/postgres/README.md`: fold surviving (non-translation) notes into `SQL/SCHEMA.md#postgres`, delete file.
- Historical `docs/plans/*`, `CHANGELOG.md` history, `docs/architecture-review-*` untouched.

## File changes
### internal/
- `internal/db/db.go`: `Connect(dsn string) (*sql.DB, error)` using `pgx` only; drop go-mssqldb import; package doc → Postgres.
- `internal/db/dialect.go`: delete; named-param funcs → `namedparams.go` (`RewriteNamedParams(query string, orderedNames []string) string`).
- `internal/config/base.go`: remove `Engine`, `TestEngine`, `DBEngine()`; `BuildDSN` always postgres URL (sslmode=require); update Base doc comment.
- `internal/config/config.go`, `local.go`: remove `Engine`/`TestEngine` env + local.json wiring.
- `go.mod`/`go.sum`: drop go-mssqldb (`go mod tidy`).

### arx_go/
- `handlers.go`: remove `dialect` from conn state, `dia()`, `topLimit` (callers drop `top`), `Rewrite` calls in wrappers and `txLogger.rewrite`; `New(db, cfg, ...)`; `connectDB func(dsn string) (*sql.DB, error)`.
- `main.go`, `settings.go` (reconnect, `DBEngine`/`TestEngine` template data, `test_engine` form field), `cmd/backfill_attachment_hash/main.go`: follow new signatures.
- `templates/settings/settings.html`: remove the Test Connection "Engine" row.
- Helpers taking `arxdb.Dialect` (`primaryAttachmentEnsureSQL`, `hasOwnBOMExpr`, `recordFilters.whereClauses`): drop the param.
- `named_query.go`: `arxdb.RewriteNamedParams(...)`.
- `unit.go`: GETDATE comment/code per script.
- All `*.go`: dia() inlining + token script.
- Tests: `attachments_test.go`, `records_filters_test.go` drop sqlserver cases/dialect args;
  `settings_save_test.go` / `settings_save_integration_test.go` new `connectDB` signature, drop `cfg.Engine`;
  `integration_target_test.go` drop sqlserver scheme (non-postgres scheme → error);
  `timestamps_integration_test.go`, `tx_boundaries_integration_test.go` drop `Name() != "postgres"` skips;
  `migrations_lint_test.go` drop the azure set; `internal/config/base_test.go` drop engine tests.

### SQL / docs
- Delete `SQL/azure/` (all).
- `SQL/SCHEMA.md`, `SQL/schema_diagram.md`, `README.md`, `CONTRIBUTING.md`, `docs/conventions.md`: remove Azure/T-SQL instructions.
- `CLAUDE.md`: rewrite "Test mode" engine bits, T-SQL migration rules → Postgres, "Database — SQL Server" → Postgres (pgx; lowercase column names), integration-test sentinel note (Postgres seed only), "What NOT to touch".
- `ROADMAP.md` / `docs/FUTURE_GOALS.md`: remove #29 items if listed.
- `CHANGELOG.md`: `## [0.8.3]`; `arx_go/RELEASE_NOTES.md`: no entry (no user-facing feature) beyond noting Settings engine selector removal? — not user-facing enough; skip.

## Test plan
1. **Coverage audit:** whole Postgres integration suite (`-tags integration`, CI `postgres-integration`) exercises every rewritten query; `records_filters_test.go` (placeholder numbering), `attachments_test.go` (primary-attachment SQL), `settings_save_test.go` (reconnect), `internal/config/base_test.go` (DSN), `internal/db/dialect_test.go` named-param tests.
2. **Characterization:** none new — the integration suite already pins query behavior on Postgres.
3. **Red tests:**
   - `TestBuildDSN_AlwaysPostgres` (internal/config): `Base{}` with no engine → DSN starts `postgres://` and has `sslmode=require`. Fails today (defaults to `sqlserver://`).
   - `TestNoSQLServerSQLInGo` (arx_go, unit): scans non-test and test `*.go` in `arx_go/` for `@p\d`, `GETDATE(`, `OUTPUT INSERTED`, `SCOPE_IDENTITY`, `NEXT VALUE FOR`, `CONTEXT_INFO`, `TOP (`. Fails today on `@p1`.
   - `TestIntegration_UtilitiesChecksRun` (post-review): runs every Utilities check; caught the inliner's `%[3]s` artifact.
4. **Manual-only:** Settings page renders without Engine selector and reconnects; smoke a create (part/PO/record) and a named-query run.
