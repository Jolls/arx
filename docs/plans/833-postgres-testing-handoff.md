# Handoff: testing against live Postgres (#833)

Schema DDL (`SQL/postgres/*.sql`), seed data (`seed_test_data.sql` +
`seed_company_logo.sql`), and the app-side dialect gaps are done (PR #835,
merge status: check `gh pr view 835`). The app still defaults to SQL Server —
nothing changes for production. This doc is the next step only: proving it
all works against a real Postgres instance.

## Next step (#833)

1. Load the schema onto a real Postgres DB, in the order documented in
   `SQL/postgres/README.md` ("Suggested run order"), then
   `seed_test_data.sql`, then `seed_company_logo.sql`, then `triggers.sql`
   last.
2. **The target database must be named to satisfy `arx_go/integration_test.go`'s
   safety check**: the DSN string must contain `arxdev` (case-insensitive) or
   the test suite hard-fails (`liveHandler`, `integration_test.go:33-34`) —
   this is the guardrail against ever touching ArxProd. If the existing
   Postgres box only has a database named `arx`, create/rename one to
   `arxdev` first.
3. Point the app at it for a test run:
   - `arx_go/config/local.json`: `test_engine` must be `"postgres"` —
     **`local.json` always wins over env var overrides** (`TEST_DB_ENGINE`
     env var is silently ignored if `local.json` sets `test_engine`, per
     `arxlib/config/config.go:115-116` — don't waste time on the env var if
     `local.json` already has a value).
   - `test_db_server`, `test_db_name` in `local.json`; `test_db_password` in
     the per-user secrets store (`%APPDATA%\Arx\local.json`), never
     `local.json` itself.
   - Postgres connections require `sslmode=require` by default
     (`arxlib/config/base.go`'s `BuildDSN`) — the StartOS Postgres box needs
     TLS actually configured (was being worked on earlier; confirm it's live)
     or the connection is refused.
4. Run the live suite:
   ```powershell
   $env:ARX_TEST_DSN="postgres://<user>:<password>@<host>:<port>/arxdev?sslmode=require"
   go test -tags integration ./arx_go/...
   ```
5. Fix whatever breaks. Known things to check first (logged as a comment on
   #833 from the prior review):
   - A named-query param present in the caller's `params` map but not
     referenced in the stored SQL text — Postgres errors on the unused bound
     arg; SQL Server tolerates it.
   - `Dialect.RewriteNamedParams`'s `@(\w+)` matching is a blind text pass —
     doesn't skip `@`-tokens inside quoted string literals.
   - Named-query args bind as plain Go strings against whatever column type
     they're compared to (INTEGER/DATE included) — confirm Postgres's
     implicit coercion matches SQL Server's, especially
     `max_subbatch_result`'s `CAST(@record_date AS DATE)` (depends on the
     server's `DateStyle` setting).
   - Mixed-case column folding: `arx_go/named_query.go` and
     `arx_go/settings.go` read `rows.Columns()` dynamically — Postgres folds
     unquoted identifiers to lowercase where SQL Server preserved case
     (`SUWeb` → `suweb`, etc.). Verify both paths handle this.
