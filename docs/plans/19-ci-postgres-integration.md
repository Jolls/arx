# Plan: CI job running `-tags integration` against a Postgres container (#19)

## Decision (already made, not re-litigated here)

Add a new required job to `.github/workflows/test.yml` that:
- Runs on every `push`/`pull_request` (same triggers as `build-vet-test`).
- Spins up a `postgres` GitHub Actions service container.
- Loads `SQL/postgres/*.sql` DDL + `SQL/postgres/seed_test_data.sql` (+ `seed_company_logo.sql`) into it, in the order documented in `SQL/postgres/README.md`.
- Runs `go test -tags integration ./arx_go/...` against that container via `ARX_TEST_DSN`.
- Is required to pass before merge, exactly like `build-vet-test` (branch-protection change - see "Manual step" below; not a file change in this repo).
- Uses no secrets: the container's superuser password is a literal in the workflow file (ephemeral, container-only, never a real credential).

## Background from the code (read before implementing)

- `arx_go/integration_test.go` build tag: `//go:build integration`. Excluded from default `go test ./...` and `build.bat`.
- `arx_go/integration_target_test.go` (no build tag, always compiles) defines:
  - `integrationDSN()` - returns `ARX_TEST_DSN` if set, else (only if `ARX_TEST_FROM_CONFIG=1`) a DSN built from `config/local.json` + secrets store. **CI must set `ARX_TEST_DSN` directly** and must NOT set `ARX_TEST_FROM_CONFIG` (there is no `config/local.json` in the CI runner, and this repo's memory warns local.json's `test_engine` can silently override an env var - irrelevant here since we bypass `config/local.json` entirely by setting `ARX_TEST_DSN`).
  - `integrationEngine(dsn)` - derives engine from the DSN URL scheme (`postgres`/`postgresql` -> `"postgres"`), and hard-fails unless the DSN's database name (a) does not contain `arxprod` and (b) is case-insensitively exactly `arxdev`. **The CI Postgres database must be named `arxdev`** (lowercase is fine - `strings.EqualFold`).
- `arx_go/integration_test.go`'s `TestMain` calls `integrationDSN()`/`integrationEngine()`, connects via `arxdb.Connect(engine, dsn)` (`internal/db/db.go`, `pgx` driver for `"postgres"`), then asserts the sentinel row `id=3005` has `part_number='ASM-1001'`, `description='Skyrunner Standard Drone'` (from `SQL/postgres/seed_test_data.sql`) before running any test. This is what proves the container was seeded correctly - if seeding order/content is wrong, `TestMain` fails fast with a clear message instead of every test failing individually.
- `SQL/postgres/README.md` "Suggested run order" + "Seeding" sections give the exact file order (parents before children; `triggers.sql` last; `seed_test_data.sql` then `seed_company_logo.sql`).
- `go.mod`: `go 1.27.0`. Existing job uses `actions/setup-go@v7` with `go-version: '1.27.0'` and installs `libayatana-appindicator3-dev` (systray cgo dep) before building - the integration job also compiles `package main` (`arx_go`), so it needs the same apt dependency.

## Known Postgres-only failures in the integration suite (open issues #32, #33)

These are **currently open bugs**, found by previously running this exact suite against a seeded Postgres box, not CI-environment artifacts:

- **#33** - `CreateRecord` (`POST /forms/{id}/records/new`, `records.go` serial-number allocation) uses `WITH (UPDLOCK, HOLDLOCK)`, a SQL Server-only locking hint with no Postgres equivalent. Every test path that creates a new form record via this handler fails outright with a syntax error. In the current suite this hits at minimum the `smoke_post_test.go` case that POSTs to the record-create route (`//go:build integration`, table-driven over "high-value create routes").
- **#32** - dialect gaps found running the suite against Postgres:
  - `OUTPUT inserted.id` (SQL Server-only) used in test-helper seed code, not app code: breaks `TestIntegration_UpsertGeneratedAttachment`, `TestIntegration_PasteResultImageWrite`, `TestIntegration_LockApproveUnlockLifecycle`, `TestIntegration_BulkLockRecords`, `TestIntegration_LockLotTrackedRecordWithNoLot`.
  - `DATEADD`/`DATEDIFF` interval-unit identifiers not translated: breaks `TestIntegration_QueryOnTimeDelivery`, `TestIntegration_QueryPOCycleTime`, `TestIntegration_ReportsHandlers_EndToEnd/ReportsOnTime`, `.../ReportsCycleTime`, `TestIntegration_ReportsOnTimeExportCSV`, `TestIntegration_ReportsCycleTimeExportCSV`.
  - Raw `@p1`-style placeholders not rewritten in test-helper seed code: breaks tests using `seedMfgPart` (`mfg_parts_integration_test.go`), `seedSupplierPart` (`sourcing_integration_test.go`), and helpers in `suppliers_integration_test.go`.
  - Possible stale-data mismatches (not confirmed as a real bug - issue says may resolve with a clean reseed): `TestIntegration_QuerySpendBySupplier`, `TestIntegration_QuerySpendByPart`, `TestIntegration_QueryDataQualityParts`, `TestIntegration_ReportsDataQualityMissingSupplierExportCSV`.

A **required** job running the full suite unmodified will be red on every PR from day one because of #32/#33, which blocks all merges - see Open Questions. This plan does not decide how to handle this; it stops short of editing test files and flags the decision below.

## File changes

### 1. `.github/workflows/test.yml`

Add a second job, `postgres-integration`, alongside `build-vet-test`. Full new job YAML (append at the end of the `jobs:` block, same indentation level as `build-vet-test`):

```yaml
  postgres-integration:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: postgres
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: arxdev
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    env:
      ARX_TEST_DSN: postgres://postgres:postgres@localhost:5432/arxdev?sslmode=disable
    steps:
      - uses: actions/checkout@v7

      - uses: actions/setup-go@v7
        with:
          go-version: '1.27.0'

      - name: Install systray cgo dependency
        run: sudo apt-get update && sudo apt-get install -y libayatana-appindicator3-dev postgresql-client

      - name: Load schema, triggers, and seed data
        run: |
          set -e
          DSN="$ARX_TEST_DSN"
          for f in \
            uom.sql \
            contact.sql \
            company_attachment.sql \
            company.sql \
            part.sql \
            mfg_part.sql \
            supplier_part.sql \
            price.sql \
            bom.sql \
            part_attachment.sql \
            purchase_order.sql \
            po_line.sql \
            inventory_transaction.sql \
            build.sql \
            lot.sql \
            unit.sql \
            form.sql \
            form_row.sql \
            form_record.sql \
            result.sql \
            genealogy.sql \
            form_row_history.sql \
            form_events.sql \
            record_events.sql \
            record_event_results.sql \
            app_config.sql \
            named_queries.sql \
            users.sql \
            logs.sql \
            release_notes.sql \
            schema_migrations.sql \
            triggers.sql \
          ; do
            psql "$DSN" -v ON_ERROR_STOP=1 -f "SQL/postgres/$f"
          done
          psql "$DSN" -v ON_ERROR_STOP=1 -f SQL/postgres/seed_test_data.sql
          psql "$DSN" -v ON_ERROR_STOP=1 -f SQL/postgres/seed_company_logo.sql

      - name: Integration test
        run: go test -tags integration ./arx_go/...
```

Notes on the exact YAML above:
- `POSTGRES_DB: arxdev` makes the service container create the `arxdev` database on startup (satisfies `integrationEngine`'s exact-match guard; the postgres image's entrypoint runs `createdb` for `POSTGRES_DB` if it isn't `postgres`).
- `services.postgres.ports: ["5432:5432"]` publishes the container port to the runner's `localhost`, so `ARX_TEST_DSN` can use `localhost:5432` directly (standard GitHub Actions service-container pattern; no `--network` wiring needed since the job's own steps run on the runner host, not inside another container).
- `sslmode=disable` - the container has no TLS configured; `ARX_TEST_DSN` is set directly here (bypassing `cfg.DSN()`'s `sslmode=require` default noted in `docs/plans/833-postgres-testing-handoff.md`), so this is not a behavior change to app code, only to how the test DSN is assembled for this job.
- `triggers.sql` loaded last per `SQL/postgres/README.md`, after every table file.
- `schema_migrations.sql` placed after `release_notes.sql`, before `triggers.sql` - it has no FK dependencies and isn't in the README's explicit "Suggested run order" list (that list only covers FK-linked tables), but it must exist before `seed_test_data.sql` runs in case any future seed rows reference it (currently none do; harmless either way).
- No secrets used anywhere: `postgres`/`postgres` is a fixed literal for the ephemeral, container-only superuser account, not a real credential.

### 2. No other files change

Do not touch `SQL/postgres/*.sql`, `SQL/azure/*`, `arx_go/integration_test.go`, `arx_go/integration_target_test.go`, or any other Go source under this plan. If the "known failures" open question below resolves to "add skips," that is a **separate, follow-up change** to the specific `*_integration_test.go` files listed above (out of scope for this plan) - do not fold it into this workflow-only change without explicit instruction.

## Manual step (outside this repo's files)

Branch protection ("required status checks") lives in GitHub repo settings, not in `.github/workflows/test.yml`. After this job runs green at least once on a PR, a repo admin must add `postgres-integration` to the required status checks list (same place `build-vet-test` is currently configured) for it to actually gate merges. This plan cannot make that change via a file edit.

## Test plan

4) Manual-only. No unit/integration tests are being added by this plan - it is a CI configuration change. Verification is the job itself running green on a PR (or failing with a clear, expected reason).

Known-failing tests on Postgres today (see "Known Postgres-only failures" above, tracked as open issues #32 and #33) mean the job will NOT be green out of the box. This plan does not modify test files to skip them - see Open Questions.

## Open Questions

1. **How to handle #32/#33 known failures given the job must be required.** Options not decided here: (a) add `t.Skip("see #32")`/`t.Skip("see #33")` to the specific known-failing tests listed above as a follow-up change before/alongside enabling the required check, (b) land this workflow now and accept the job is red until #32/#33 are fixed (blocking merges until then, since it's required), (c) land the job as non-required first, flip to required only after #32/#33 close. Need a decision before this job can actually gate merges as specified.
2. **Postgres image version/tag.** No existing pin found in `SQL/postgres/*` or docs; used `postgres:16` above as a placeholder. Confirm this matches (or is at least compatible with) whatever Postgres version the real StartOS deployment target runs, per `docs/plans/833-postgres-testing-handoff.md` and issue #24.
3. **Branch protection update timing.** Confirm who applies the "required status check" setting and when (immediately vs. after a trial green run) - this repo-settings change can't be scripted as a file edit in this plan.

## Resolved decisions (2026-09-26)
1. **Known failures:** land the job **non-blocking** — add `continue-on-error: true` at the job level of `postgres-integration`. Don't add skips. Flip to required (remove `continue-on-error`, add to branch protection) after the Azure-cleanup work ports the test helpers and fixes #32/#33. New tests in this batch use dialect-neutral helpers (`h.queryRowContext`/`h.execContext`).
2. **Image:** `postgres:17`.
3. **Branch protection:** not changed now (job is non-blocking). The human adds it when flipping to required.
4. **Later groups in this batch edit the load list:** dead-column cleanup removes `logs.sql`/`release_notes.sql`; #194 adds `part_category.sql` (before `part.sql`) and `attachment_category.sql` (before `part_attachment.sql`). Each group updates the workflow list alongside its DDL change.
