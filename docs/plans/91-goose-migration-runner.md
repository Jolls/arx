# #91 — goose migration runner (`migrate status|up`)

Branch: `feature/91-goose-migration-runner`. Version: **0.8.4**.

## Resolved decisions

1. **Delivery:** runner logic in new package `internal/migrate` (`package migrate`); thin console
   command `arx_go/cmd/migrate` (`package main`) calls `migrate.Run`. Run as
   `go run ./arx_go/cmd/migrate status|up [--yes]` from the repo root. `Arx.exe` is unchanged
   (`-H windowsgui` can't host a CLI: shell doesn't wait, no stdout/stdin, exit code lost). The
   acceptance-criteria wording "`Arx.exe migrate`" is delivered as `arx_go/cmd/migrate`; say so in the PR.
   Long-term path (not this PR): ship `arx-migrate.exe` from `build.bat` once a Postgres ArxProd
   exists (#189); optional read-only "N pending" in Settings; StartOS package could reuse `internal/migrate`.
2. **Confirmation:** typed database name for `up` on a non-ArxDev database; `--yes` skips.
3. **No shippable exe** in `build.bat` for now; `go run` only.
4. **libpq env defaults** (`PGPASSWORD`, `PGHOST`, pgpass) that pgx applies are accepted.
5. **Missing baseline:** both `status` and `up` refuse with the backfill instruction.
6. **Wrapping:** one `-- +goose StatementBegin` / `-- +goose StatementEnd` block around the whole body.
7. **Review follow-up (medium review):** the runner prints server notices (`cfg.OnNotice` →
   `NOTICE: <msg>`); SELECT output stays discarded. #192's header no longer tells operators to
   edit the zone before running (embedded + checksummed → write a new migration). The #31/#194
   preview SELECTs are left as-is (historical; already applied everywhere).

## Technical decisions (verified against goose v3.27.3 / pgx v5.11.0)

- **goose API:** Provider API only:
  `goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithTableName("schema_migrations"))`.
- **Ledger shape:** no schema change. goose inserts `(version_id, is_applied)` and reads
  `tstamp, is_applied` / `version_id, is_applied`. Our `tstamp TIMESTAMPTZ NULL DEFAULT now()` vs
  goose's `timestamp NOT NULL` is harmless (goose never inserts tstamp).
- **Version 0:** goose only inserts version 0 when it creates the table. With an existing empty
  table, `Up` fails "missing zero version migration". So the baseline writes version 0 + every
  migration; the runner pre-checks the table exists and has a version-0 row (also stops goose from
  creating its own differently-typed table).
- **One block per file:** goose runs the block as one `ExecContext` with no args; pgx v5 uses the
  simple protocol when there are no args, so a multi-statement body runs inside goose's per-file
  transaction, together with the ledger insert.
- **Embedding:** new package `arx/SQL/postgres/migrations` (`embed.go`, `//go:embed *.sql`).
- **DSN:** only `ARX_MIGRATE_DSN` via injected `getenv`. Never import `arx/internal/config` or
  godotenv. Parse with `pgx.ParseConfig`; connect with `stdlib.OpenDB(*cfg)`. Print
  `Target: host:port/database (user U)`, never the password. Parse errors are not wrapped (no DSN echo).
- **Confirmation (`up` only):** `strings.EqualFold(cfg.Database, "ArxDev")` → no prompt. Else
  `--yes` skips; else operator types the exact database name; EOF/mismatch → `ErrNotConfirmed`.
  No outright arxprod ban. Prompt happens before connecting. `status` never prompts.
- **Forward-only:** no `down`; lint forbids `-- +goose Down` and `-- +goose NO TRANSACTION`.
- **Checksums:** `SQL/postgres/migrations/checksums.txt`, `sha256sum --text` format
  (`<sha256>  <file>`; leading `*` on name accepted). `.gitattributes` forces `.sql` to LF.
- **Release notes:** none (operator tooling).

## File changes

### 1. `go.mod` / `go.sum`
`go get github.com/pressly/goose/v3@v3.27.3` then `go mod tidy`.

### 2. New `SQL/postgres/migrations/embed.go`
```go
// Package migrations embeds the Postgres migration files so the migrate runner
// (internal/migrate, #91) applies exactly the set it was built with.
package migrations

import "embed"

// FS holds every *.sql file in this directory, at its root.
//
//go:embed *.sql
var FS embed.FS
```

### 3. New `internal/migrate/migrate.go` (`package migrate`)
Doc comment: usage via `arx_go/cmd/migrate`; credentials only from `ARX_MIGRATE_DSN` (DDL-capable
login), never app config/secrets, never saved; forward-only; `up` on non-ArxDev needs typed name or `--yes`.

- `const DSNEnv = "ARX_MIGRATE_DSN"`; `var ErrNotConfirmed = errors.New("not confirmed; nothing applied")`.
- `func Run(ctx, args []string, getenv func(string) string, in io.Reader, out io.Writer) error`:
  1. `len(args)==0` or `args[0]` not `status`/`up` → `errors.New("usage: migrate status | migrate up [--yes]")`.
     `flag.NewFlagSet(args[0], flag.ContinueOnError)`, output `io.Discard`, `yes := fs.Bool("yes", false, ...)`;
     parse `args[1:]` (return error); `fs.NArg() > 0` → usage error.
  2. `dsn := getenv(DSNEnv)`; empty → `fmt.Errorf("%s is not set: set it to a DSN for a DDL-capable login (app config and secrets are never used)", DSNEnv)`.
  3. `cfg, err := pgx.ParseConfig(dsn)`; err → `fmt.Errorf("%s is not a valid Postgres connection string", DSNEnv)`.
     `cfg.Database == ""` → `fmt.Errorf("%s must name a database", DSNEnv)`.
     `fmt.Fprintf(out, "Target: %s:%d/%s (user %s)\n", cfg.Host, cfg.Port, cfg.Database, cfg.User)`.
  4. `up` && !EqualFold(ArxDev) && !*yes → `confirm(cfg.Database, in, out)`; return its error if non-nil.
  5. `db := stdlib.OpenDB(*cfg); defer db.Close()`; `db.PingContext` err → `fmt.Errorf("connect: %w", err)`.
  6. `checkLedger(ctx, db)`.
  7. `p, err := newProvider(db)`.
  8. `status`: `p.Status(ctx)`; each → `fmt.Fprintf(out, "%-8s %s\n", s.State, s.Source.Path)`.
  9. `up`: `res, err := p.Up(ctx)`; `var pe *goose.PartialError; if errors.As(err, &pe) { res = pe.Applied }`;
     `fmt.Fprintln(out, r)` each; return err if non-nil; `len(res)==0` → `No pending migrations.`
- `confirm(database, in, out) error`: prints
  `%q is not ArxDev. Type the database name to apply pending migrations to it: `; reads one line
  (`bufio.NewReader(in).ReadString('\n')`); `ErrNotConfirmed` unless `strings.TrimSpace(line) == database`.
- `checkLedger(ctx, db) error`: `SELECT count(*) FROM schema_migrations WHERE version_id = 0`;
  query error → `fmt.Errorf("reading schema_migrations (load SQL/postgres/schema_migrations.sql first): %w", err)`;
  count 0 → `errors.New("schema_migrations has no baseline row (version_id 0); baseline the ledger first, see SQL/SCHEMA.md#migrations")`.
- `NewProvider(db *sql.DB) (*goose.Provider, error)` — exported so the integration tests in `arx_go` reuse it.

### 4. New `arx_go/cmd/migrate/main.go`
Doc comment with usage. `main()`: `if err := migrate.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout); err != nil { fmt.Fprintln(os.Stderr, "migrate:", err); os.Exit(1) }`.

### 5. Convert the 4 migrations
Per file, SQL bodies otherwise unchanged (leave the preview `SELECT`s in #31/#194):
- a. Replace the 3-line "Pinned to ArxDev ... psql -v ON_ERROR_STOP=1 ..." header paragraph with
  `-- Idempotent. Applied by the migrate runner (arx_go/cmd/migrate, #91), one transaction per file.`
- b. Replace `BEGIN;` + blank + the 5-line arxdev `DO` guard with `-- +goose Up` / `-- +goose StatementBegin`.
- c. Replace the trailing self-register `INSERT ...;` + blank + `COMMIT;` with `-- +goose StatementEnd` (last line).
- d. `20260926092325_31_dead_column_cleanup.sql` only: also delete the "First Postgres migration: the
  ledger may not exist yet" comment and its `CREATE TABLE IF NOT EXISTS schema_migrations (...)` block.
- Header comments must not contain `+goose`.

### 6. New `SQL/postgres/migrations/checksums.txt`
After step 5: `cd SQL/postgres/migrations && sha256sum --text *.sql` → file content (LF).

### 7. `arx_go/migrations_lint_test.go`
- Keep `migrationName`; drop `migrationGuard`, `migrationInsert`. Add `crypto/sha256`, `encoding/hex`, `sort`.
- `migrationForbidden` ordered `{label, re}`:
  `(?im)^\s*(BEGIN|COMMIT|ROLLBACK)\s*;` → "BEGIN/COMMIT/ROLLBACK (the runner wraps each file in a transaction)";
  `(?i)INSERT\s+INTO\s+schema_migrations` → "INSERT INTO schema_migrations (the runner records the version)";
  `(?i)current_database\s*\(` → "current_database() guard (the runner confirms the target)".
- `migrationProblems(body string) []string`: significant lines = trimmed, non-blank, not a plain `--`
  comment (keep `-- +goose` lines). Require first == `-- +goose Up`, second == `-- +goose StatementBegin`,
  last == `-- +goose StatementEnd`, exactly 3 `-- +goose` lines; else one message
  "must be: header comments, -- +goose Up, -- +goose StatementBegin, body, -- +goose StatementEnd (no other +goose annotations)".
  Then `"contains "+label` per forbidden match.
- `manifestProblems(manifest string, files map[string][]byte) []string`: per non-blank line
  `strings.Fields`; != 2 fields → problem. Key `strings.TrimPrefix(f[1], "*")`. For each file (sorted):
  no entry → `"<name>: no entry in checksums.txt; add:\n<hash>  <name>"`; mismatch →
  `"<name>: sha256 <got>, checksums.txt says <want>; committed migrations are immutable, revert and add a new migration"`.
  Leftover entries → `"checksums.txt lists <name>, which does not exist"`.
- `TestMigrationsGooseFormat` replaces `TestMigrationsSelfRegister` (same walk, name regex, dup check).
- `TestMigrationChecksums`: real manifest vs all `*.sql`.
- `TestMigrationProblems`, `TestManifestProblems`: table tests (R10, R11).

### 8. New `arx_go/migrate_integration_test.go` (`//go:build integration`)
Tests C1, I1, I2.

### 9. `SQL/postgres/build_schema.sh`
Line 2 comment → `# Prints the full Postgres schema + test seed + migration-ledger baseline as one SQL script, for a fresh database:`.
Append:
```bash
# Ledger baseline (#91): the reference DDL already includes every migration, so record
# goose's version 0 and each migration file as applied. Guarded, because re-running this
# script keeps schema_migrations (its DDL has no DROP).
versions="(0)"
for f in migrations/*.sql; do b=${f##*/}; versions+=", (${b%%_*})"; done
echo "INSERT INTO schema_migrations (version_id, is_applied)"
echo "SELECT v, TRUE FROM (VALUES $versions) t(v)"
echo "WHERE NOT EXISTS (SELECT 1 FROM schema_migrations s WHERE s.version_id = t.v);"
```

### 10. `SQL/postgres/schema_migrations.sql` — header comment only
```
-- schema_migrations: ledger of applied migrations (#48), written by the goose-backed
-- migrate runner (internal/migrate, #91); build_schema.sh baselines a fresh DB.
-- Column shape is goose's ledger (id / version_id / is_applied / tstamp). goose never
-- inserts tstamp, so the NULL/TIMESTAMPTZ difference from goose's own DDL is harmless.
```

### 11. `.github/workflows/test.yml` (`postgres-integration`), after `Integration test`
```yaml
      - name: Migration runner smoke test (#91)
        run: |
          ARX_MIGRATE_DSN="$ARX_TEST_DSN" go run ./arx_go/cmd/migrate status
          ARX_MIGRATE_DSN="$ARX_TEST_DSN" go run ./arx_go/cmd/migrate up
```

### 12. `SQL/SCHEMA.md`
`## Migrations`: keep intro, append "The timestamp is also the goose version." Replace bullets
(`**Run:**` … `**Ledger semantics:**`) with: Format (goose); No transaction or ledger code;
Idempotent (unchanged); Immutable once committed (checksums.txt); Lint
(`TestMigrationsGooseFormat`/`TestMigrationChecksums`); Checks must raise (runner discards SELECT
output — use `RAISE EXCEPTION` in `DO`); Renames (unchanged); Run (`go run ./arx_go/cmd/migrate
status|up [--yes]`, `ARX_MIGRATE_DSN` DDL-capable login, target printed, non-ArxDev typed
confirmation, app never auto-applies); Ledger baseline (runner requires table + version-0 row;
build_schema baselines; one-time backfill SQL below); Not `app_config.schema_version` (first two
sentences kept; last → "Breaking migrations still bump `schema_version`; the runner records them like any other.").
```sql
INSERT INTO schema_migrations (version_id, is_applied)
SELECT v, TRUE FROM (VALUES (0), (20260926092325), (20260926092835), (20260926093727), (20260926120000)) t(v)
WHERE NOT EXISTS (SELECT 1 FROM schema_migrations s WHERE s.version_id = t.v);
```
Table-reference `schema_migrations` row: owned by the goose runner (`internal/migrate`, #91);
`version_id` = filename prefix (0 = goose baseline); runner inserts in the same tx; build_schema
baselines; `Arx.exe` never touches it; no `*Table()` helper; guarded DDL, excluded from seed.

### 13. `CLAUDE.md`
- Package list: `internal/` — add `migrate` (both mentions).
- `schema_migrations` exception: "nothing in Go touches it" → "only the migrate runner (#91) touches it".
- Replace the Postgres-only migration paragraph + self-register paragraph with goose authoring rules
  (format, no BEGIN/COMMIT/guard/INSERT/Down/NO TRANSACTION, idempotent, checksums.txt, never edit
  committed migration, lint names, schema_version bump, renames patch named_queries, SCHEMA.md link,
  build_schema also baselines) and a "Running migrations" paragraph: `go run ./arx_go/cmd/migrate
  status|up`; creds only `ARX_MIGRATE_DSN`; Claude may run status/up against **ArxDev only**, only
  when `ARX_MIGRATE_DSN` is already set — never construct/read/echo it, never pass `--yes`, stop if
  printed Target isn't ArxDev; if unset ask the user. ArxProd off-limits.
- FK-promotion: orphan check must `RAISE EXCEPTION` inside a `DO` block (runner discards SELECT output).
- Reseeding paragraph: append — if the migrate integration tests fail for missing baseline, tell the
  user to backfill (SCHEMA.md#migrations); don't do it yourself.

### 14. `CONTRIBUTING.md` line 15
→ `- Schema changes ship as a goose migration in `SQL/postgres/migrations/`, applied with `go run ./arx_go/cmd/migrate up` (never at app startup) by a DDL-capable login from `ARX_MIGRATE_DSN`.`

### 15. `CHANGELOG.md` — `## [0.8.4] - 2026-09-26`
Added: migrate command; checksums.txt lint. Changed: goose format + lint + build_schema baseline. All link #91.

### 16. `ROADMAP.md`, `docs/FUTURE_GOALS.md`
Delete the `Migration runner … (#91)` bullet in each.

## Test plan

### 1. Coverage audit
- `arx_go/migrations_lint_test.go` `TestMigrationsSelfRegister` — replaced.
- `arx_go/timestamps_integration_test.go` `TestIntegration_AuditColumnsAreTimestamptz` — pins `schema_migrations.tstamp`; unchanged.
- `arx_go/postgres_only_lint_test.go` `TestNoSQLServerSQLInGo` — scans `cmd/*/*.go`.
- `arx_go/dead_schema_names_test.go` `TestDeadSchemaNamesRemoved` — migrations dir exempt.
- `arx_go/integration_test.go` `TestMain`/`liveHandler` — ArxDev + sentinel guard reused.
- CI `postgres-integration` "Load schema" step exercises `build_schema.sh` incl. the baseline INSERT.

### 2. Characterization tests
- **C1 `TestIntegration_MultiStatementExec`**: `h.DB().ExecContext(ctx, "SELECT 1;\nSELECT 2;")` → no error.
  Pins pgx no-args simple-protocol multi-statement exec.
- `TestMigrationsSelfRegister` passes today and locks in behavior this change removes; deleted, replaced by R9.

### 3. Red tests
`internal/migrate/migrate_test.go` (red: package doesn't exist; stub signatures first). DSNs use
`127.0.0.1:1?sslmode=disable&connect_timeout=2`; `t.Setenv("PGDATABASE", "")`.
- **R1 `TestRun_Usage`** — nil, `{"down"}`, `{"status","extra"}`, `{"up","-bogus"}` → error; no `Target:`.
- **R2 `TestRun_DSNOnlyFromEnv`** — recording getenv returning "" → error mentions `ARX_MIGRATE_DSN`; only that key requested.
- **R3 `TestRun_NoAppConfigImport`** — `go/parser` ImportsOnly on `migrate.go`: no `arx/internal/config`, no `github.com/joho/godotenv`.
- **R4 `TestRun_BadDSN`** — `postgres://u:s3cret@host:notaport/db` → error, no `s3cret` in err/out; `postgres://u:p@127.0.0.1:1/` → `must name a database`.
- **R5 `TestRun_PrintsTarget`** — status on `.../ArxProd` → `Target: 127.0.0.1:1/ArxProd (user u)`, no `s3cret`.
- **R6 `TestRun_ConfirmationGate`** — ErrNotConfirmed: up+ArxProd+EOF; up+ArxProd+`arxprod\n`. Connect error (gate passed): up+ArxProd+`ArxProd\n`; up --yes+ArxProd; up+arxdev (no prompt text); status+ArxProd (no prompt).
- **R7 `TestNewProvider_ListsEmbeddedMigrations`** — `sql.Open("pgx", ...)` (no connect) → `NewProvider` ok; `ListSources()` one per `*.sql` in `../../SQL/postgres/migrations`, versions = filename prefixes, ≥4.
- **R8 `TestConfirm`** — `ArxProd\n` nil; `ArxProd` EOF nil; ` ArxProd \n` nil; `x\n` ErrNotConfirmed.

`arx_go/migrations_lint_test.go`:
- **R9 `TestMigrationsGooseFormat`** — real files clean (red: current files lack annotations, have BEGIN/guard/INSERT).
- **R10 `TestMigrationProblems`** — valid body → none; missing Up; SQL before Up; missing StatementEnd; Down; NO TRANSACTION; `BEGIN;`; `COMMIT;`; INSERT INTO schema_migrations; current_database() → ≥1 each.
- **R11 `TestManifestProblems`** — match none; `*name` none; changed byte; missing entry; stale entry; 3-field line.
- **R12 `TestMigrationChecksums`** — real manifest vs files (red: file missing).

`arx_go/migrate_integration_test.go`:
- **I1 `TestIntegration_MigrationsAllApplied`** — `migrate.NewProvider(h.DB())` Status all `goose.StateApplied`; failure message says baseline ArxDev (SQL/SCHEMA.md#migrations) or run migrate up.
- **I2 `TestIntegration_FailedMigrationLeavesNoLedgerRow`** — goose provider on `fstest.MapFS{"99990101000000_91_rollback_probe.sql": "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\nSELECT 1/0;\n-- +goose StatementEnd\n"}` with `WithTableName("schema_migrations")`; `Up` → `*goose.PartialError` containing `division by zero`; ledger count for 99990101000000 = 0. Feasible as DML-only `arx` login.
- Successful real `up` is manual/CI only (arx login has no DDL; nothing pending on fresh DB).

### 4. Manual-only
- **M1 (human):** backfill ArxDev ledger as `postgres` with the SQL in §12. If I1/I2 get `permission denied for table schema_migrations`: `GRANT SELECT ON schema_migrations TO arx`.
- **M2:** `$env:ARX_MIGRATE_DSN='postgres://postgres:<pw>@127.0.0.1:5433/ArxDev?sslmode=require'; go run ./arx_go/cmd/migrate status` → Target + 4 `applied`; `up` → `No pending migrations.`
- **M3:** unset `ARX_MIGRATE_DSN` → clear "not set" error.
- **M4 (throwaway container):** delete ledger row 20260926120000 → status pending → up applies. Probe file with `CREATE TABLE probe_91(id int);` + `SELECT 1/0;` → up fails, no table, no ledger row. Delete probe.
- **M5 (throwaway DB `arxscratch`):** up prompts; wrong/empty → refused; typed name proceeds; `--yes` no prompt.
- **M6:** Windows `build.bat` + WSL build/vet/test; no new `*_windows.go`.
