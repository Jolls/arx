# Plan: #447 — Due-date tracking for recurring records (calibration/PM)

Design settled in the issue's plan comment; this file adds the file-level spec and tests.

## Open questions
See "Resolved decisions" — items marked **(ask)** are awaiting the user.

## Resolved decisions
1. **Model:** `form.recurrence_days INTEGER NULL CHECK (> 0)`; `form_record.next_due_date DATE NULL`.
2. **Non-breaking migration** (no `schema_version` bump): additive nullable columns, same as #450.
3. **Prefill:** `InsertRecord` sets `next_due_date = record_date::date + form.recurrence_days` (NULL if the form has no interval). `DuplicateRecord` (re-test) does the same from `CURRENT_DATE` and the form's interval — it does not copy the source's date.
4. **Editable** on the record-edit page while unlocked (saved via `UpdateRecordAfterSave`); the existing lock/claim path already rejects saves on locked records. Blank input clears it (NULL = untracked). No auto-recompute when `record_date` is edited.
5. **Current due** = per (form_id, unit_id), the latest **locked, active** record (`record_date DESC NULLS LAST, id DESC`) whose unit is active; tracked only if that record's `next_due_date` is non-NULL. (Confirmed by user; differs from the issue's wording "latest record with a due date": a latest record whose date was cleared means "stop tracking".)
6. **Status:** `overdue` if due < today; `soon` if today <= due <= today+30; else none. Fixed 30, a Go const in `internal/records`.
7. **Dashboard card** `due-overdue`: up to 10 rows, ordered by due date ascending (overdue first). Columns: unit serial (links to the unit page), part number, form, due date, "Nd overdue"/"in Nd" badge.
8. **Badges:** unit page (`part_unit_trace.html`) — one badge per tracked form for that unit; record view (`records_show.html`) — shows "Next due" date; badge only when this record is the current one for its (form, unit), computed in `GetRecordHeader` (NOT EXISTS newer locked record) so no extra round trip.
10. **As built — differences from the first draft:**
    - Record-view fields (`next_due_date`, `next_due_superseded`) are on `GetRecord` (the record page's query), not `GetRecordHeader`.
    - `ListDueUnits` takes `today` from Go (`records.Today()`, the user's local date) instead of `CURRENT_DATE`, so the card's filter and its badges (computed in Go) always agree. Inactive forms are excluded too.
    - Seed instrument is `MFG-1002` (category MFG): a `BUY` part with no supplier tripped the Data Quality "missing supplier" tests.
    - Seed ids: parts 3014 (FORM) / 3015 (MFG), form 6003, step 6116, units 8506-8509, records 7021-7026, results 7167-7172.
    - Unit-page route-budget/stat expectation: queries 7 -> 8.
9. **Branch:** `feature/447-recurring-due-dates` off main (#450 had already merged via PR #456, so nothing to stack on).

## File changes

### Schema
- `SQL/migrations/<authoring-timestamp>_447_recurring_due_dates.sql` — goose format per CLAUDE.md, modeled on `20261112100000_450_form_serial_format.sql`. Body: `ALTER TABLE form ADD COLUMN IF NOT EXISTS recurrence_days INTEGER CONSTRAINT CK_form_recurrence_days CHECK (recurrence_days > 0);` and `ALTER TABLE form_record ADD COLUMN IF NOT EXISTS next_due_date DATE;`. Header notes "Not breaking".
- `SQL/migrations/checksums.txt` — add `sha256sum --text` line.
- `SQL/form.sql`, `SQL/form_record.sql` — add the columns with comments.
- `SQL/seed_test_data.sql` — see Seed.
- `SQL/SCHEMA.md` — `form`, `form_record` table-reference rows + seed-range rows.

### Queries / service (`internal/records/records.sql`, then `sqlc generate`)
- `InsertRecord`: add `next_due_date` computed in SQL from the form's `recurrence_days` (join form by `form_id`; no new Go param).
- `DuplicateRecord`: same, from `CURRENT_DATE`.
- `UpdateRecordAfterSave`: add `next_due_date = sqlc.narg(next_due_date)::date`.
- `GetRecordHeader`: return `next_due_date` and `next_due_superseded boolean`.
- New `SetFormRecurrence` (`UPDATE form SET recurrence_days`), mirroring `SetFormSerialFormat`; form get/list queries return `recurrence_days` (pointer/NULL → `*int`, per `sqlc.yaml` overrides).
- New `ListDueUnits :many` (`DISTINCT ON (r.form_id, r.unit_id)` per decision 5, outer filter on due <= `CURRENT_DATE + 30`, `LIMIT sqlc.arg(row_limit)`), and `ListUnitDue :many` for one unit (all tracked forms, any due date, for the unit-page badge).
- `internal/records/records.go`: service methods; `DueStatus(due, today time.Time) string` + `DueSoonDays = 30`; add fields to `RecordSave`.
- Never edit `internal/dbq` by hand.

### Handlers / templates (`arx_go`)
- `records.go`: form-def save reads `recurrence_days` (blank → NULL; reject non-positive/non-integer with a 400 like the `serial_pad` handling); record save reads `next_due_date` (`YYYY-MM-DD` or blank); view handlers pass due status to templates.
- `models/trmodels.go`: `Form.RecurrenceDays *int`, `TestRecord.NextDueDate *time.Time`, `NextDueSuperseded bool`.
- `reports.go` + `main.go`: `ReportsDashboardDueOverdue` at `GET /api/reports/dashboard/due-overdue`, same shape as `ReportsDashboardStaleWIP`.
- `templates/reports/dashboard.html`: card shell (`data-dashboard-card="due-overdue"`) + `dashboard_card_due_overdue` fragment.
- `templates/records/form_def_edit.html`: "Recurrence (days)" number field with `original_recurrence_days` hidden input (same pattern as serial prefix).
- `templates/records/record_edit.html`: `next_due_date` date input (hidden/disabled when locked; add to the autosave field list near line 347 if it enumerates inputs).
- `templates/records/records_show.html`, `templates/parts/part_unit_trace.html`: due text + Bootstrap badge (`badge text-bg-danger` / `text-bg-warning`).
- Add `DueStatus` badge helper to the records template FuncMap (`render_records.go`).

### Seed (`SQL/seed_test_data.sql`)
Dates must be relative to `CURRENT_DATE` (not fixed) so statuses don't rot. Use the next free ids in each documented range:
- A FORM part + a `calibration` form (`recurrence_days = 365`), one step.
- A serial-tracked UUT part + 4 units: **overdue** (locked record, due `CURRENT_DATE - 10`), **due soon** (due `CURRENT_DATE + 10`), **fine** (due `CURRENT_DATE + 200`), **superseded** (old overdue locked record + newer locked record with a future due).
- Update the part-3005 sentinel guard only if touched (it should not be).
- `setval` resets as the seed already does. Human step: reseed ArxDev (never run it myself).

## Test plan

### 1. Coverage audit
- `TestIntegration_DashboardStaleWIPRecords` (`arx_go/integration_test.go`) — card pattern.
- `TestIntegration_SerialUnitCreationAndRetest`, `TestIntegration_SaveResults_FillsManualUnitProvenance` — cover `InsertRecord`/`DuplicateRecord`/save.
- `route_budget_test.go`, `parallel_pages_integration_test.go` — per-route query budgets; add the new card.
- `TestMigrationsGooseFormat`, `TestMigrationChecksums`, `sqlc_converted_lint_test.go` — run in the default `go test`.

### 2. Characterization tests
None new — insert/save/duplicate behavior is already covered by the integration tests above; they must stay green.

### 3. Red tests
- `TestDueStatus` (`internal/records`, no DB): due = today-1 → overdue; today → soon; today+30 → soon; today+31 → "". Fails today: symbol absent (stub `DueStatus` returning "" first so it fails on assertion).
- `TestIntegration_ListDueUnits` (live DB; one subtest per rule): latest locked wins; newer draft doesn't clear overdue; inactive unit excluded; inactive record excluded; cleared (NULL) latest excluded; record without unit excluded; day-30 included / day-31 excluded; overdue ordered first. Fails today: query absent (stub returning nil).
- `TestIntegration_CreateRecordPrefillsDue`: form with `recurrence_days=365` → new record `next_due_date = record_date + 365`; form without interval → NULL; re-test duplicate recomputes from today. Fails today: column always NULL.
- `TestIntegration_SaveRecordNextDue`: unlocked save persists / clears `next_due_date`; after completing the record, a save attempt leaves it unchanged. Fails today: field not saved.
- `TestIntegration_DashboardDueOverdue`: handler renders seeded overdue + soon units, not the fine one; one query (route budget row added).

### 4. Manual-only
- Form-definition page: Recurrence field saves/blank clears.
- Record-edit page: date input shown, prefilled, editable; absent/disabled once locked.
- Badge colors/layout on unit page and record view; dashboard card layout.

Seed refresh is a human step before the live tests/manual check.
