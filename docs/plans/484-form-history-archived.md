# #484 Form history: record `archived` in form_row_history and use it in the timeline

## Context (verified in current code)
- `form_row_history` snapshot cols (15): `type, parameter, specification, spec_units, spec_min, spec_max, spec_nom, default_result, hide_formula, pf_type, instrument_types, format, comment, category, sheet_name` — exactly the 15 `UpdateStep` writes (`internal/records/records.sql` ~431). `SetStepArchived` (~466) changes only `archived` + `updated_at`. `InsertStep`/`CopyStep` leave `archived` at default FALSE.
- `trg_form_row_history()` (`SQL/triggers.sql` 197-215): statement-level AFTER UPDATE, copies the 15 cols from `oldtab`; `changed_by` from GUC `arx.username` or `''`.
- `form_row.created_at` is nullable `TIMESTAMPTZ DEFAULT now()`.
- `ListFormStepsAt` (`internal/records/records.sql` ~62): LATERAL `h` = first history row with `changed_at >= day_end` ORDER BY `changed_at, id`; each field = `h` value when `h` exists else `t` (live); `changed` = EXISTS over `[day_start, day_end)`.
- `records.FormStepAt = dbq.ListFormStepsAtRow` (`internal/records/records.go:45`); service passthrough 181-184.
- `FormDefHistory` (`arx_go/records.go` ~480-540) builds local `stepState` (JSON: `id,type,parameter,spec_nom,spec_min,spec_max,spec_units,pf_type,default_result,hide_formula,changed`).
- `FormDef` (`arx_go/records.go` ~298) drops steps with `rs.Hidden` (resolved hide formula) before rendering; archived steps render with class `step-archived`.
- `form_def.html`: "Show archived" switch `#show-archived-toggle` (`data-archived-toggle`, `data-archived-table="def-table"`), rendered only if `.HasArchived`; rows `<tr data-step-id=... [class="step-archived"]>`, cells with `data-field`; timeline `div.def-timeline[data-form-id]`, dots `.def-timeline-dot`, Now `.def-timeline-now[data-now]`, label `#def-history-label`.
- `arx_go/static/records/app.js`: lines 1-11 toggle table class `show-archived` on the toggle's `change`; ~528-620 history JS (`originals`, `setActiveDot`, `applyHistory`, `resetToNow`, click delegation). `applyHistory` skips rows absent from the response.
- `arx_go/static/app.css` 498-503: `#def-table tbody tr.step-archived {display:none}`, `.show-archived tr.step-archived {display:table-row}` + tertiary bg.
- Seed (`SQL/seed_test_data.sql`) has no explicit `form_row_history` INSERTs; its only history row is trigger-written by `UPDATE form_row SET spec_max='200' WHERE id=6103` (~653). 6103 is not archived, so after the trigger change that row gets `archived = FALSE` automatically.
- Docker container (`SQL/compose.yml` → `compose_init.sh`): pipes `build_schema.sh` (reference DDL + `triggers.sql` + seed) into psql, then baselines `schema_migrations` with every `SQL/migrations/` file marked applied. **Migrations are NOT executed on the container** — it gets the new column/trigger from edited `SQL/*.sql`; the 484 migration body never runs there by itself.
- `migrations_lint_test.go` (no build tag) defines `const migrationDir = "../SQL/migrations"`, usable from `integration`-tagged tests in the same package.
- Node-driven JS test pattern exists (`export_csv_test.go`, `filter_terms_test.go`: `jsFunc` extracts a column-0 `function name(`; test skips if node absent). History JS lives inside a `DOMContentLoaded` closure.

## File changes

### 1. `SQL/migrations/YYYYMMDDHHMMSS_484_form_row_history_archived.sql` (new)
Authoring-time timestamp (> `20261118224417`). Header comments: purpose; backfill is an inference; `archive_id`/`revision`/`granularity` out of scope; non-breaking → no `schema_version` bump (precedent #468); idempotent. Then `-- +goose Up`, `-- +goose StatementBegin`, body, `-- +goose StatementEnd` as last line. No BEGIN/COMMIT, no ledger insert.

Body, in order:
1. `ALTER TABLE form_row_history ADD COLUMN IF NOT EXISTS archived BOOLEAN;`
2. `CREATE OR REPLACE FUNCTION trg_form_row_history()` — identical to `SQL/triggers.sql` after change 4.
3. Backfill — one set-based `UPDATE form_row_history ... WHERE archived IS NULL`:
   - Number rows per step: `ROW_NUMBER() OVER (PARTITION BY form_row_id ORDER BY changed_at, id)` = `rn`.
   - Next state = row `rn + 1`'s snapshot (self-join the numbered CTE), else the live `form_row` row for the newest. Do not use `LEAD()` on a row constructor. Inner join to `form_row` (history of deleted steps stays NULL).
   - `toggle = (15 snapshot cols) IS NOT DISTINCT FROM (15 next-state cols)` (NULL-safe).
   - Value before each row's update: `archived_i = ((live_archived::int + SUM(toggle::int) OVER (PARTITION BY form_row_id ORDER BY rn ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING)) % 2) = 1`.
   - Self-check: step eligible only if `archived_i` of its `rn = 1` row is FALSE; computed per step so a failing step keeps all rows NULL.
   - `UPDATE ... SET archived = <computed> FROM <cte> WHERE history.id = cte.id AND history.archived IS NULL AND step_eligible`.
   - Validate on a throwaway container (Test plan D) before committing.

### 2. `SQL/migrations/checksums.txt`
Append `sha256sum --text` line for the final file.

### 3. `SQL/form_row_history.sql`
Add `archived BOOLEAN` after `sheet_name`, comment: pre-update `form_row.archived`; NULL = not recorded (#484).

### 4. `SQL/triggers.sql` (197-215)
Add `archived` to the INSERT column list and the SELECT from `oldtab`. Byte-equivalent to the migration's function body.

### 5. `SQL/seed_test_data.sql`
Comment-only: header (lines 32-33) and next to `UPDATE ... id = 6103` (~652) — trigger now also records `archived` (FALSE for 6103). No row changes, no reseed (backfill gives ArxDev's existing 6103 row the same FALSE).

### 6. `SQL/SCHEMA.md`
- `form_row_history` table-reference row (~274): `archived` = pre-update flag, trigger-written; NULL = not recorded (predates #484 and backfill couldn't infer); backfill is the toggle-walk inference.
- Triggers table row `trg_form_row_history` (~141): also snapshots `archived`.
- Reference-data lines 118-119: seeded row's `archived` is FALSE.

### 7. `SQL/schema_diagram.md`
Add `boolean archived "pre-update value; NULL = not recorded"` to both `form_row_history` blocks (~272, ~918), matching neighbour style.

### 8. `internal/records/records.sql` — `ListFormStepsAt`
- LATERAL select: add `archived`.
- SELECT: add
  ```sql
  COALESCE(h.archived, t.archived)::bool AS archived,
  (t.created_at IS NULL
   OR t.created_at < sqlc.arg(day_end)::timestamptz
   OR EXISTS (SELECT 1 FROM form_row_history e
              WHERE e.form_row_id = t.id AND e.changed_at < sqlc.arg(day_end)::timestamptz))::bool AS existed
  ```
- Update the query comment for both columns.
- `sqlc generate`; commit `internal/dbq` (`Archived bool`, `Existed bool` on `dbq.ListFormStepsAtRow`). `internal/records/records.go` unchanged (alias).

### 9. `arx_go/records.go` — `FormDefHistory`
Add `Archived bool \`json:"archived"\`` and `Existed bool \`json:"existed"\`` to `stepState`; copy `s.Archived`, `s.Existed`; update handler doc comment. No change to `UpdateStep`/`SetStepArchived`/`ArchiveStep`/`SaveFormDef`.

### 10. `arx_go/templates/records/form_def.html`
After `#def-history-label` (~96) add `<div id="def-history-hidden" class="small text-muted mt-1 d-none"></div>`. JS fills it.

### 11. `arx_go/static/records/app.js` — history block (~528-620)
- At load (`originals` pass) also store each row's original `step-archived` state.
- `applyHistory`, per row present in the response: `tr.classList.toggle('step-archived', s.archived)`; `tr.classList.toggle('d-none', !s.existed)`; keep existing `row-changed` / `data-field` logic.
- After applying, fill/hide the note (see Resolved decisions for the count rule, wording and link). Recompute on the toggle's `change` while a historical dot is active.
- `resetToNow`: restore original `step-archived` state, remove `d-none`, hide/clear the note, restore Show archived (see Resolved decisions).

### 12. `CHANGELOG.md`
New top entry `## [0.8.119] - <date>`, `### Fixed`: the form history view now shows each step as active or archived as it was on that date and hides steps that didn't exist yet ([#484](https://github.com/Jolls/arx/issues/484)).

## Test plan

### 1. Coverage audit
- `arx_go/integration_test.go`: `TestIntegration_FormDefHistory_EndOfDayState`, `TestIntegration_FormDefHistory_ReleaseState` (`ListFormStepsAt` + `FormDefHistory` JSON/`changed`; use `defHistoryStep`, still pass — `existed` is a flag, not a filter); `TestIntegration_TestDefinitionHistoryAudit` (trigger, `changed_by`, snapshot); `TestIntegration_ArchiveStep_TogglesArchivedFlag`, `TestIntegration_ArchiveStep_DoesNotAlterLockedRecordSnapshot` (`SetStepArchived`/`ArchiveStep`); `TestIntegration_SaveFormDef_UpdatesStepSkipsUnchangedAndReordersWithNewRow` (`UpdateStep` + skip-unchanged); `TestIntegration_FormDef_RendersStepsAndArchivedToggle`, `TestIntegration_FormDef_ReleaseMarker`, `TestIntegration_FormDef_AuditLog`, `TestIntegration_FormDef_AuditLogUnlockComment` (`FormDef` render).
- `arx_go/records_reads_integration_test.go`: `TestIntegration_RecordsReads_FormDefPages`, `TestIntegration_RecordsReads_FormDefTimeline`.
- `arx_go/route_budget_test.go`: "def history" (budget 1) and "form def" rows unchanged.
- `arx_go/migrations_lint_test.go`: `TestMigrationsGooseFormat`, `TestMigrationChecksums`, `TestMigrationProblems`, `TestManifestProblems`.
- `arx_go/templates_parse_test.go`: `TestRecordsTemplatesParse`.
- `arx_go/records_timeline_test.go`: `TestBuildTimeline`, `TestTimelinePositions` (unaffected).
- `arx_go/sqlc_converted_lint_test.go` (unaffected); CI `sqlc diff` covers `internal/dbq`.
- Nothing covers the history JS.

### 2. Characterization tests (integration, `arx_go/integration_test.go`; must pass on unchanged code; none lock in a bug)
- `TestIntegration_FormDefHistory_StepCreatedAfterDayStillReturned`: `recForm` step with `created_at = now()`, query `at=2026-07-01` → still in response with current values, `changed=false`.
- `TestIntegration_FormDefHistory_ArchivedStepStillReturned`: insert step `archived=TRUE` directly (no history), query a past day → returned with current values.
- `TestIntegration_ArchiveStep_WritesHistoryRowPerToggle`: `seedThrowawayForm` + `ArchiveStep` handler archive then unarchive → exactly 2 history rows; their `parameter`/`spec_max` snapshots equal the step's unchanged values (the property the backfill relies on).
- `TestIntegration_FormDef_ArchivedRowAndHistoryHooks`: read-only on seeded form 6001 → body contains `<tr data-step-id="6104" class="step-archived">`, `id="show-archived-toggle"`, `id="def-history-label"`, `def-timeline-now`.

### 3. Red tests (fail today on assertion/missing column, pass after; no stubs needed)
**A. `TestIntegration_FormRowHistory_TriggerRecordsArchived`**: `seedThrowawayForm` + `ArchiveStep` archive, unarchive, then a plain parameter UPDATE in `beginTx` + `SetAuditUser`. Read `archived` from `form_row_history` ORDER BY `id` → FALSE, TRUE, FALSE. Fails today: column missing.

**B. `TestIntegration_FormDefHistory_ArchivedAndExistedAtTime`** (next to `EndOfDayState`): extend `defStepState` with `Archived bool \`json:"archived"\``, `Existed bool \`json:"existed"\``. Steps via `repInsert` (no UPDATE → no trigger rows); history inserted raw with explicit `changed_at`/`archived`; viewer tz `"UTC"`.
- E (archived later): live `archived=TRUE`, `spec_max='150'`, `created_at='2026-06-01'`; history `2026-07-01 12:00+00` (`archived=FALSE`, `spec_max='1'`), `2026-07-03 12:00+00` (`archived=FALSE`, `spec_max='150'`). `at=2026-07-01` → `archived=false, changed=true`; `07-02` → `false, false`; `07-03` → `true, true`; `07-04` → `true`.
- N (NULL fallback): live `archived=TRUE`, history rows `archived` NULL → `archived=true` every day.
- X1: `created_at='2026-07-05'`, no history → `existed=false` at `07-01`, `true` at `07-06`.
- X2: `created_at` NULL, no history → `existed=true` at `07-01`.
- X3 (0.7 load): `created_at='2026-07-05'`, history at `2026-07-01 12:00+00` → `existed=true` at `07-02`.
Fails today: JSON lacks `archived`/`existed`.

**C. `TestIntegration_Migration484_BackfillArchived`**: read the `*_484_*.sql` from `migrationDir`, extract text between `-- +goose StatementBegin`/`StatementEnd`, run via `h.execContext` after seeding rows (safe: idempotent, column already exists). Own throwaway form per scenario; live rows via `repInsert`; history raw, `archived` NULL, explicit `changed_at`, snapshot cols set so toggles vs edits are distinguishable; include NULL-in-both columns.
- S1: live `archived=FALSE`; h1 `parameter='A'` (edit), h2 `'B'` (toggle = h3), h3 `'B'` (toggle = live `'B'`) → FALSE, FALSE, TRUE.
- S2: live FALSE, one toggle row → walk ends TRUE → stays NULL.
- S3: preset `archived=TRUE` row untouched; second run changes nothing.
- S4: NULL-heavy cols (e.g. `spec_max` NULL everywhere) still detected as toggles.
- Seed check: step 6103's history row has `archived = FALSE` (read-only; on shared ArxDev needs the user to have run `migrate up`).
Cleanup: history → `form_row` → forms. Fails today: file missing.

**D. Backfill SQL validation:** before committing, run the migration once on a throwaway local container (`docker compose -f SQL/compose.yml up -d --wait db`, `ARX_MIGRATE_DSN` at the container, `down` after). Test C exercises it every integration run.

### 4. Manual-only
- Dot before a step was archived, Show archived off → step active with "changed" highlight.
- Dot before a step existed → row hidden.
- Now → row classes, archived state, Show archived restored; note gone.
- Prev/next across several dots; out-of-order responses still dropped.
- Note: wording, placement, link turns on Show archived; appears only when a changed step on the selected dot is hidden.
- After `migrate up` on ArxDev: a skipped step (NULL rows) falls back to current state; note covers it.
- No reseed required (seed gains comments only). If anyone reseeds anyway, run `migrate up` on shared ArxDev first.

## Verification
- `sqlc generate` → `sqlc diff` clean.
- `go build ./... && go vet ./... && go test ./...`.
- Integration on local container: `docker compose -f SQL/compose.yml up -d --wait db`; `ARX_TEST_DSN=postgres://postgres:postgres@127.0.0.1:55432/ArxDev?sslmode=require go test -tags integration ./arx_go/...`; restart `db` between runs.

## Resolved decisions
1. **Hidden count (change 11):** a step counts as hidden when it is `changed` in the response and either has no `tr[data-step-id]` in the table (hide formula or otherwise absent) or is archived at that time (`s.archived`) while Show archived is off. No server change. The link is offered only when at least one counted step is archived-hidden and `#show-archived-toggle` exists.
2. **Show archived on Now:** leave the switch as the user left it (do not snapshot/restore it). `resetToNow` restores only row state (`step-archived`, `d-none`, cell contents) and hides the note. The acceptance item "restores ... Show archived setting" is satisfied by rows returning to their current archived state under whatever the switch is.
3. **Wording:** `1 changed step is hidden` / `N changed steps are hidden`. Link text `Show archived`; clicking sets `#show-archived-toggle.checked = true` and dispatches `change`. When the toggle is absent, show the note without a link.
4. **Badge fix:** `originals` stores each `data-field` cell's `innerHTML` (not `textContent`) and `resetToNow` restores `innerHTML`, so the archived badge survives Now. `applyHistory` keeps writing `textContent` for historical values.
5. **Seed:** no seed change. Manual test case: archive seed step 6103, then select its earlier edit dot.
6. **JS test:** extract a column-0 top-level function in `app.js`, `function hiddenChangedSteps(steps, rowIDs, showArchived)` — `steps` = response array, `rowIDs` = Set of step ids present in the table (as numbers), `showArchived` = bool. Returns `{count, archived}` where `count` = changed steps that are missing from `rowIDs` or (`s.archived && !showArchived`), and `archived` = how many of those are archived-hidden (and in the table). History JS calls it. Add `arx_go/history_hidden_js_test.go` using the existing `jsFunc` + node pattern (skip without node): cases — none hidden → 0; changed+archived with switch off → count 1, archived 1; same with switch on → 0; changed but not in table → count 1, archived 0; unchanged archived → 0; steps with `existed=false` are not counted (they're hidden because they didn't exist, not changed-hidden — exclude `!s.existed`).
7. **JSON keys:** `archived`, `existed`.

## Review follow-ups (code-review high)
- No Show archived switch (nothing archived now): the history view adds `show-archived` to the table while a dot is selected, so rows archived back then show greyed; the note never counts them. Supersedes decision 3's no-link case.
- Backfill: rows before 2026-06-23 (#403 introduced archiving) or with `changed_by = 'VBA_MIGRATION'` are never toggles.
- `SetStepArchived` is a no-op when the flag already matches (no history row for a repeat request).
- History view adds the archived badge to rows archived at the selected time.
- Backfill test runs only the backfill statement inside a rolled-back transaction (no DDL, no writes outside the runner).
