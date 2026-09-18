1. Think Before Coding: don't assume/hide confusion. State assumptions; if multiple interpretations, present don't pick; suggest simpler approach if exists, push back when warranted; if unclear, stop and ask.

2. Simplicity First: minimum code, nothing speculative. No unrequested features/abstractions/flexibility/error-handling for impossible cases. If 200 lines could be 50, rewrite. Test: "would a senior engineer call this overcomplicated?"

3. Surgical Changes: touch only what's needed. Don't improve/refactor/reformat adjacent code; match existing style; mention unrelated dead code, don't delete it. Remove imports/vars/funcs YOUR change orphaned; don't remove pre-existing dead code unless asked. Every changed line should trace to the request.

4. Goal-Driven Execution: define verifiable success criteria, loop until met (e.g. bug fix → repro test → make pass; feature → tests for invalid inputs → pass). For multi-step tasks state brief plan with verify step per item.

5. Tests after bug fixes/features: suggest a regression test when it'd meaningfully catch breakage (non-obvious edge cases, silent-break logic) — briefly, and only write if user agrees. Skip for trivial/UI-only/well-covered changes.

6. Token-Efficient Messages: terse, no preamble/restating/unrequested trailing summary, no just-in-case caveats. Alternatives welcome (standard/idiomatic ones) but skip esoteric ones unless asked. Prefer short direct statements over headers unless content has genuinely distinct parts. Bullet/Outline style communication is preferred. 

7. Model Selection: default Sonnet. Suggest Opus once (don't repeat if user stays on Sonnet) for: schema/new-table changes, cross-cutting architecture (auth/DB layer/rendering/config), security review (auth/CSRF/session/input validation), multi-package refactor spanning arxlib/+arx_go/. Not for routine feature work/bugfixes/UI/pattern-following handlers.

# Arx Parts Master
@CLAUDE.local.md

Parts master/purchasing system for engineering/manufacturing shop. One Go binary (`arx_go/Arx.exe`):
- `arx_go/` — catalog, suppliers, POs, test records. Port 4568. `package main`.
- `arxlib/` — shared config/DB/URL utils. `package config / db / urlutil / folderpick`.

Archived/removed, ignore in history: Ruby Sinatra apps, VBA workbooks (`archive/`), old separate `parts_master_go/`/`test_records_go/`.

## Building
`cd arx_go; build.bat` → outputs `Arx.exe`, runs `go test ./...` first, uses `-H windowsgui` (systray). Exe gitignored. Version auto-parsed from top `## [x.y.z]` in CHANGELOG.md — never set AppVersion in main.go.
User does quick iteration via `go run .` in `arx_go/` (no exe/systray/full tests) — use build.bat only for shipping or full test sweep.
Do not run the app yourself (go run, launching exe, curling routes) to verify — use go build/vet/test, describe manual verification for user instead.
WSL: a native Linux Go toolchain (not the Windows `go.exe`) works directly against the Linux-filesystem checkout — no cross-OS path/locking issues. `Arx.exe` still can't run from WSL (systray ships via `-H windowsgui`, Windows-only); this only enables build/vet/test.

## Tooling gotchas (Windows + go.work)
- Repo root is a Go workspace spanning arxlib+arx_go, not a module itself — `go build/test ./...` from root fails; run inside module dir or use `arx_go\build.bat`.
- Run `.bat` files via PowerShell tool, not Bash (Bash only captures cmd banner). PowerShell: `cd arx_go; .\build.bat`.
- No `jq` installed — use `gh ... --json <fields> --template '{{...}}'` or PowerShell `ConvertFrom-Json`.
- `gh issue view`/`gh pr view` plain-text (no --json) silently return empty in both Bash/PowerShell tools (pager swallows output, exits 0). Always use `--json title,body,labels,comments` etc.
- WSL build/vet/test: install a native Linux Go toolchain (`apt install golang-go`, matching the version pinned in `go.work`) and `libayatana-appindicator3-dev` (systray's cgo dependency, `arx_go` only — `arxlib` has no extra system deps). Windows-only source (`syscall`-based files like `arxlib/folderpick`, `arx_go/console_windows.go`) needs a `!windows`-tagged counterpart to compile on Linux; check for new ones after adding OS-specific code.
- No bulk file rewrites (`gofmt -w`, `sed -i`). Repo is NOT gofmt-clean and the working tree is mixed CRLF/LF (`core.autocrlf=true`, no `.gitattributes`). Whole-file rewrites reflow unrelated code and/or flip CRLF→LF — noisy diffs that violate surgical-change discipline. Edit via the Edit tool (`replace_all` per file for bulk renames); it preserves line endings. `gofmt -l` flags CRLF files as "unformatted" but the pending diff is just line-ending churn, not real formatting — don't chase it. Verify builds with `build.bat`, not gofmt.

## Key facts
arx_go: package main, port 4568, go-chi router, getlantern/systray, templates `templates/{contacts,parts,pos,records,reports,settings,shared,suppliers}/` embedded (one nav tab per subfolder, shared layout). arxlib: package config/db/urlutil/folderpick.

## Config load order (later wins)
1. `.env` (godotenv, from `../.env` then `.env`) 2. env vars 3. `config/local.json` (always wins; gitignored) 4. per-user secrets store.
local.json resolves relative to Arx.exe's cwd — run from arx_go/ or use start.ps1. First run: Settings writes it. PO defaults (contact/receiver) saved there via Settings UI. Attachment category options in `app_config` DB table, edited via Settings UI.
Secrets (`db_password`, `test_db_password`, `session_secret`) do NOT live in `config/local.json` — they're per-user in `%APPDATA%\Arx\local.json` (`~/.config/arx/local.json` on Linux, via `os.UserConfigDir()`), so a shared/OneDrive exe doesn't leak them across users (#732). `arxlib/config/secrets.go`: `LoadSecrets`/`SaveSecrets`/`SecretsConfig`. First run after upgrade migrates any secrets out of `config/local.json` into the per-user file and scrubs them from the shared file. DB password never in `.env`; first-run prompt via /settings saves it to the per-user store.

## Test mode
`TEST_MODE=true` in .env swaps DB connection via Base's active-profile helpers; table names identical prod/dev, only connection changes. `cfg.*Table()` helpers always return bare names regardless of TestMode — never hardcode table names.
Full second connection profile (not just DB-name swap): TestDBServer/TestEngine/TestDBName/TestDBUser/TestDBPassword, each overriding prod counterpart only when non-empty (blank inherits prod) — so same-server name-only swap still works, but can also point at a fully separate server/engine/creds (e.g. ArxDev-on-Postgres during #625 migration, #672).
Overrides: env (TEST_DB_SERVER/TEST_DB_ENGINE/TEST_DB_NAME[default ArxDev]/TEST_DB_USER) or local.json (test_db_server/test_engine/test_db_name/test_db_user); test password lives in the per-user secrets store (`%APPDATA%\Arx\local.json`, `test_db_password`), never in `config/local.json` or .env (#732). Editable in Settings UI Test Connection section.

New table → update all 4 or test mode breaks (exception: `schema_migrations`, the migration ledger #48 — guarded DDL with no DROP, no seed block, no `*Table()` helper since nothing in Go touches it): 1) `SQL/azure/<table>.sql` DDL (same schema prod+ArxDev) 2) `SQL/azure/seed_test_data.sql` DELETE+fixed-ID INSERT block 3) `arxlib/config/config.go` add `*Table()` helper 4) `SQL/schema.md` table reference section.

Every schema change ships a migration in `SQL/azure/migrations/YYYYMMDDHHMMSS_<issue>_<description>.sql` (timestamp = authoring time; the 28 older `migrate_*.sql` files keep their names) for a human to run — SQL/azure/*.sql is reference DDL, never auto-run. Migrations idempotent/guarded (e.g. `IF COL_LENGTH(...) IS NULL`). Pin to ArxDev: `USE ArxDev;` at top + comment that human changes it for ArxProd. Never default a migration to ArxProd. You author these, never run them (see ArxProd rule).
Every migration self-registers as its LAST step: `IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = <filename timestamp>) INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (<filename timestamp>, 1);` — `TestMigrationsSelfRegister` fails the build otherwise. Breaking migrations still bump `app_config.schema_version` too (different job: binary↔DB gate). Details in `SQL/SCHEMA.md#migrations`.
Migrations must run as a single batch, no `GO`: the human runs these through the Azure portal's query editor, which sends the whole script as one batch and does not honor `GO` as a batch separator. Any statement referencing a column added earlier in that same script (e.g. a CHECK constraint or an UPDATE backfill right after `ALTER TABLE ADD <col>`) fails with "Invalid column name" — batch compilation resolves names against pre-batch metadata, so a same-batch reference to a brand-new column doesn't resolve even though the ALTER ran first in execution order. Fix: wrap the statement referencing the new column in dynamic SQL, `EXEC(N'...')`, which defers name resolution to runtime (see `SQL/azure/migrations/migrate_743_part_tracking_mode.sql` for the pattern).

FK-promotion migrations (adding a FK constraint to a column that previously held only a historically-logical reference) need an orphan check written into the migration, run against ArxProd before the `ADD CONSTRAINT`, since ArxDev's seed is clean and won't surface orphans ArxProd may have accumulated (#775 — `migrate_742` failed on ArxProd this way; `form_record.part_id` orphans had to be repaired by hand). Include this pattern in the migration file so the human running it sees the offending rows before the constraint is added:
```sql
SELECT child.id, child.<fk_col>
FROM dbo.<child_table> child
LEFT JOIN dbo.<parent_table> p ON p.id = child.<fk_col>
WHERE child.<fk_col> IS NOT NULL AND p.id IS NULL;
```

### Integration tests (live DB, test data only)
Build-tagged `arx_go/integration_test.go` (`//go:build integration`), excluded from default `go test ./...` and build.bat. Run manually:
```
$env:ARX_TEST_DSN="sqlserver://user:pass@server?database=arxv&encrypt=true"
go test -tags integration ./arx_go/...
```
`liveHandler` no longer trusts the DSN's database name (a target may just be called `arx`, not literally "ArxDev") — after connecting it queries the fixed-ID seed part `id=3005` and `t.Fatal`s unless it matches `part_number='ASM-1001'`/`title='Skyrunner Standard Drone'`, the same row seeded identically in `SQL/azure/seed_test_data.sql` and `SQL/postgres/seed_test_data.sql`. That's the safeguard against accidentally running against a real database — keep the sentinel values in sync if that seed row ever changes. db_user/db_server from arx_go/config/local.json; the password (db_password) is in the per-user secrets store `%APPDATA%\Arx\local.json` (#732), both gitignored — use database=ArxDev not ArxProd.
Any ad-hoc script/DSN outside integration_test.go should verify it's pointed at test data before running — don't rely on the database name alone.
Reseeding ArxDev is a human action — no sqlcmd/Invoke-Sqlcmd available; don't script SQL/azure/seed_test_data.sql yourself. If a test fails on stale seed data (e.g. TestIntegration_UpdatedAtSentinel), tell user to reseed, don't do it yourself.

## Database — SQL Server
Driver `github.com/microsoft/go-mssqldb`. DSN: `sqlserver://user:password@host?database=MyDB&encrypt=true`.
Column-name gotcha: driver preserves query column casing (SELECT * returns schema-defined names) — scan into struct fields by exact DB column name, never assume lowercase.

## DB schema
Naming/DDL rules: `SQL/schema.md`. ER diagram: `SQL/schema_diagram.md`. Per-table reference (PKs/trigger side-effects/column semantics): `SQL/schema.md#table-reference`. All DDL in `SQL/azure/*.sql` (reference/migration, not auto-run); Postgres equivalents in `SQL/postgres/*.sql`.

## Attachment/folder conventions
See `docs/conventions.md`: FILFileName URL format (http, LOCAL:, LOCAL:dir/), PO folder naming.

## Debug mode
`DEBUG_MODE=true` in .env (or Settings UI) logs SQL queries to terminal. Always use handler wrappers, never `h.db.*` directly: `h.queryContext`, `h.queryRowContext`, `h.execContext`, `tx, err := h.beginTx(ctx)` (*txLogger, Commit/Rollback promoted from *sql.Tx).

## Frontend — Bootstrap 5.3
Prefer Bootstrap classes over custom CSS/inline styles. Buttons always pair base+variant (`btn btn-primary`, never `btn-primary` alone). Use Bootstrap components for alerts/badges/tables/forms before custom styles; utilities (mb-3, d-flex, gap-2, px-4) before inline margin/padding/display. Inline styles last resort only (no Bootstrap equivalent, or exact pixel value needed). When writing template HTML ask: does Bootstrap have a class for this?

## Auth/middleware
`RequireAuth` redirects all app routes to /settings when h.db==nil. Settings + /static/* always accessible. CSRF tokens in gorilla session cookie `arx-session`.

## Plans
Save implementation plans (Plan Mode, issue-tied) to `docs/plans/<issue-id>-<description-stem>.md`.

## Branching
Never commit to main. Before first commit in session, check current branch; if on main, create the branch yourself using the standard below — don't ask for a name. Naming: `feature/<issue-id>-<short-slug>` when the work maps to a GitHub issue (e.g. `feature/754-settings-backup-table-list`), else `feature/<short-description>`. Push branch + open PR, never push main directly.

## Pre-commit sequence
1. go build/vet/test pass.
2. If SQL/handlers/integration-covered code touched, run live ArxDev integration tests, report pass/fail; if fails on stale seed data ask user to reseed (never do it yourself).
3. Present multi-select question for review passes to run (recommend based on diff risk, mark "(Recommended)"), then run chosen ones in order, summarize findings:
   - `/code-review low` — cheap high-confidence pass, no agent spawn. Default for small/low-risk diffs.
   - `/code-review medium` — broader pass. Default for normal feature branches. Spawn agents only if token-efficient to do so.
   - `/code-review high`/Opus-High single agent — deep review. Default for large/risky/cross-cutting (schema/auth/multi-package).
   - `/simplify` — quality-only cleanup (reuse/simplification/efficiency/altitude), no bug-hunting. Combine with a code-review pass or standalone for cleanup diffs.
   - Skip review.
4. Pause for user manual testing.
5. On approval, commit. Never commit without the user's explicit go-ahead — including follow-up fixes on an already-open PR, not just the first commit.

## Repo history
This repo is `Jolls/arx`. History before 2026-08-26 was scrubbed and re-pushed from the private
predecessor `Jolls/arx-legacy` (issue numbers there ran to #887). Existing CHANGELOG/docs links
point at `arx-legacy` **on purpose** — those numbers only resolve there; do not rewrite them to
`Jolls/arx`. New entries use `Jolls/arx`. The 33 issues open at migration were renumbered 1–33.

## Changelog
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Update CHANGELOG.md once per PR/merge (not per commit); all branch changes under one version entry, grouped under `### Added`/`### Changed`/`### Fixed`/`### Removed`/`### Security`/`### Deprecated` subheadings (only include the subheadings you need). Format:
```
## [0.3.X] - YYYY-MM-DD
### Added
- <one-line summary> ([#NNN](https://github.com/Jolls/arx/issues/NNN))
```
Link is a pointer to the issue; omit only if no issue. No comparison links in the footer — the repo doesn't tag every version, so they'd mostly be dead links.
After committing a version bump, tag it: `git tag vX.Y.Z` (push with the branch/PR, not force). Going forward only — no retroactive tagging of historical versions.
While major `x` is 0 (pre-1.0), `z` (patch) increments with every PR; `y` (minor) only bumps for a deliberate milestone release (e.g. 0.6.0).

## Release Notes
`arx_go/RELEASE_NOTES.md`, embedded at compile time, shown in-app. Update per user-facing release (not per patch), one entry covering all changes since last public release, grouped NEW FEATURES/BUG FIXES, plain text (no markdown), user-facing language:
```
Arx vX.Y.Z — Month YYYY
========================

NEW FEATURES

  <Feature Name>
  One sentence, for users not devs, no technical detail. Keep it terse —
  a single plain-language line beats a paragraph; skip caveats/detail a
  user doesn't need to act on.

BUG FIXES

  <brief description>
```

## Cross-Platform Direction
Future release targets Windows + Linux. Not needed now, but avoid design choices (cgo/native deps, OS-specific APIs, path/systray assumptions) that would block it later; flag if an approach paints us into a corner.

## Roadmap
`docs/FUTURE_GOALS.md` — long-term roadmap, prerequisites, architecture decisions. Read before proposing structural/schema changes. On implementing a listed feature, strikethrough (`~~text~~`) rather than delete.

## Issue Triage
Sizing before implementing: `/evaluate-issue <NNN>` skill (cheap Sonnet-low) scopes work, recommends model/reasoning level + whether sub-agents help for the manager/impl session. Recommends only, doesn't implement.

### Milestones (in order)
`v0.6 Major Feature Release` — new user-facing features (PO workflow, inventory, dashboards, audit). `Far Future` — out of scope (mobile, JSON API). Leave unmilestoned only for process/meta items or ongoing doc cleanup with no release dependency.

### Agents
Prefer agents only if token efficient or need different models/effort level. Don't use agents to save time.

### Labels
Severity (any defect): `sev: critical` security hole/data loss/shipped crash; `sev: high` user-visible correctness bug; `sev: med` latent bug, low practical risk; `sev: low` cosmetic/log noise/style.
Area (all issues): `area: security` auth/CSRF/session/crypto; `area: db` queries/tx/pool/schema; `area: build` build scripts/go.mod/toolchain; `area: infra` startup/systray/OS/config; `area: refactor` cleanup no behavior change; `area: test` coverage/test mode.
Existing feature-area labels (ux, export, search, analytics, audit, batch, form-mgmt, data-quality, record-lifecycle) used for v0.6 feature issues.

### Closing issues
Prefer `closes #NNN`/`fixes #NNN` in PR description for auto-close. Multi-issue PRs: keyword must precede EVERY issue number (bare numbers after a comma don't auto-close) — repeat keyword per line:
```
Closes #451
Closes #450
```
Add a one-sentence resolution comment before closing.

## What NOT to touch
SQL/azure/*.sql and SQL/postgres/*.sql = reference DDL only, not migration runners (keep in sync but never auto-run). No DB password in .env. Never hardcode table names — use cfg.*Table(). Never query/connect ArxProd directly.
