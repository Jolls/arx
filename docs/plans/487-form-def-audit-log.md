# #487 Form definition view: audit log of release/unlock events

## Context (verified in current code)
- Templates: `arx_go/templates.go` `parseTemplates`. Records pages are parsed as `records:<page>` from files `templates/shared/layout.html` + the page, with `recordsTemplateFuncs()` (`arx_go/render_records.go`). That func map has no `dict`; `formatDateTime` takes `*time.Time`, `localTimePtr` exists. `templates/shared/*.html` are each also parsed as a standalone `layout:shared/<name>` page unless named in the `switch rel` skip list (`layout.html`, `partials.html`).
- Record audit log: `arx_go/templates/records/records_show.html` lines 359-~420 (`{{if .Events}}` ... `Audit log ({{len .Events}})` ... table with Results column and snapshot sub-rows). Data keys on that page: `Events` (`[]models.RecordEvent`, built in `RecordDetail`, `arx_go/records.go` ~1085-1090), `Snapshots` (`map[int][]models.SnapshotDiffRow`), `UserLoc` (added by `renderRecords`).
- Form page: `FormDef` (`arx_go/records.go` 298-373) already calls `h.records().ListFormTimeline` (line 360, error ignored) and `buildTimeline` (390). `form_def.html` ends with `{{end}}` (modal if-close) then `{{end}}` (define close).
- `form_events` columns: id, form_id, event_type (NOT NULL), username (nullable), event_date, comments (nullable). Handlers write `locked` / `unlocked`.
- `records.TimelineRow = dbq.ListFormTimelineRow` (`internal/records/records.go:46`); only users: `FormDef`, `buildTimeline`, `records_timeline_test.go`.
- `models.RecordEvent` (`arx_go/models/trmodels.go:217`) = {ID, TestRecordID, EventType, Username, EventDate *time.Time, Comments}. This is the one row shape the partial consumes for both record and form events (form events leave TestRecordID 0).

## Design
1. One shared partial `templates/shared/audit_log.html` defining `{{define "audit_log"}}`, called as `{{template "audit_log" .}}` with the whole page data map. It reads `.Events`, `.UserLoc`, `.AuditResults` (bool; true only on the record page) and `.Snapshots`. No `dict` needed.
2. One badge mapping: the `if/else if` chain moves verbatim into the partial.
3. Form events come from the same query as the timeline (no extra round trip, `FormDef` budget stays 3).

## File changes

### 1. `internal/records/records.sql` — replace `ListFormTimeline` (lines 47-57)
```sql
-- name: ListFormTimeline :many
-- The definition page's timeline and audit-log sources, oldest first (#481, #487): each step history
-- entry (event_id 0, event_type '') and every form_events row (is_lock = it is a release). Edit rows sort
-- before events at the same instant; events keep id order.
SELECT changed_at AS at, form_row_id, ''::text AS username, FALSE AS is_lock,
       0 AS event_id, ''::text AS event_type, ''::text AS comments
FROM form_row_history
WHERE form_row_id IN (SELECT fr.id FROM form_row fr WHERE fr.form_id = sqlc.arg(form_id))
UNION ALL
SELECT fe.event_date, 0, COALESCE(fe.username, ''), fe.event_type = 'locked',
       fe.id, fe.event_type, COALESCE(fe.comments, '')
FROM form_events fe
WHERE fe.form_id = sqlc.arg(form_id)
ORDER BY at, event_id;
```
Then run `sqlc generate` at repo root; commit the regenerated `internal/dbq/records.sql.go`. Resulting `ListFormTimelineRow`: `At time.Time, FormRowID int, Username string, IsLock bool, EventID int, EventType string, Comments string`. (If sqlc types `event_id` as `int32`, add `0::int AS event_id`.) `internal/records/records.go` needs no change (alias follows).

### 2. `arx_go/records.go`
- `buildTimeline` (390): at the top of the second `for _, row := range rows` loop (before `if row.IsLock`), add
  `if row.EventType != "" && !row.IsLock { continue } // unlocks and other form events belong to the audit log, not the timeline`
  The first loop (counting locks) is unchanged. Update the func comment to say non-release events are skipped.
- `FormDef` (359-362): keep `var histPoints []historyPoint`, add `var events []models.RecordEvent`. Inside the existing `if rows, err := ...; err == nil {` block, after `buildTimeline`, add:
  ```go
  for _, row := range rows {
      if row.EventType == "" {
          continue
      }
      events = append(events, models.RecordEvent{ID: row.EventID, EventType: row.EventType,
          Username: row.Username, EventDate: &row.At, Comments: row.Comments})
  }
  ```
  Update the comment above it to mention the audit log. Add `"Events": events,` to the data map (364-372). `UserLoc` is already injected by `renderRecords`.
- `RecordDetail` data map (~1102): add `"AuditResults": true,`.

### 3. `arx_go/templates/shared/audit_log.html` (new)
Contents: `{{define "audit_log"}}` + the block currently at `records_show.html` 359-end-of-audit-block (the whole `{{if .Events}} ... {{end}}`), with these edits only:
- Header row: `<th>Comment</th>{{if .AuditResults}}<th>Results</th>{{end}}`.
- Replace `{{$snap := index $.Snapshots .ID}}` with `{{$snap := ""}}{{if $.AuditResults}}{{$snap = index $.Snapshots .ID}}{{end}}`.
- The Results `<td>...</td>` is wrapped in `{{if $.AuditResults}}...{{end}}`.
- Snapshot sub-row stays inside `{{if $snap}}` (never true when `AuditResults` is false); its `colspan="5"` unchanged.
- Everything else (collapse id `auditLog`, badge chain, `formatDateTime (localTimePtr $.UserLoc .EventDate)`, class names) copied verbatim.
End with `{{end}}`.

### 4. `arx_go/templates/records/records_show.html`
Replace lines 359-end of the audit block (`{{if .Events}}` through its closing `{{end}}`) with `{{template "audit_log" .}}`. Read the file to the block's end before editing.

### 5. `arx_go/templates/records/form_def.html`
Insert `{{template "audit_log" .}}` on its own line immediately before the final line `{{end}}` (the define close, after the unlock-modal `{{end}}`).

### 6. `arx_go/templates.go`
- Line 49 case: `case "shared/layout.html", "shared/partials.html", "shared/audit_log.html":` (partial is not a page).
- Line 82: `add("records:"+base, records, "templates/shared/layout.html", "templates/shared/audit_log.html", page)`.
- Update the doc comment above `parseTemplates` only if it lists files (it does not; leave).

### 7. Existing tests that parse `records_show.html`/records pages (must add the partial file or they fail at execute)
- `arx_go/render_records_test.go` lines 94 and 161: `ParseFS(templatesFS, "templates/records/records_show.html")` -> add `"templates/shared/audit_log.html"` as a second file.
- `arx_go/templates_parse_test.go` `TestRecordsTemplatesParse` (~line 91): add `"templates/shared/audit_log.html"` to the non-print `ParseFS` list; `TestYieldTemplateRenders` (~192) and anything else parsing a records page: add it too (harmless).

## Test plan

### 1. Coverage audit (existing)
- `arx_go/records_timeline_test.go`: `TestBuildTimeline` (all cases), `TestTimelinePositions`.
- `arx_go/render_records_test.go`: `TestRecordsShowMainCell_AttachStepNonMatchingSerial`, `TestRecordsShowMainCell_LegacyNonAttachRowStillRenders` (records_show executes; Events nil, so audit block not exercised).
- `arx_go/templates_parse_test.go`: `TestRecordsTemplatesParse`, `TestCoreTemplatesParse`.
- `arx_go/records_reads_integration_test.go`: `TestIntegration_RecordsReads_RecordDetail...` (asserts "Audit log (3)", users, comments, oldest-first, snapshot values 5.5/4.9, ~line 288), `TestIntegration_RecordsReads_FormDefPages`, `TestIntegration_RecordsReads_FormDefTimeline` (~549).
- `arx_go/integration_test.go`: `TestIntegration_FormDef_ReleaseMarker` (~4079), the FormDef(6001) steps test (~3948).
- `arx_go/route_budget_test.go`: row "form def" budget 3 (and `TestRouteBudgetsCoverAllGETRoutes`).
- `arx_go/parallel_pages_integration_test.go:192` references `ListRecordEvents` (unchanged).
- Round-trip: unit coverage of the SQL itself is only via the integration tests above.

### 2. Characterization tests (new; pass on unchanged code, still pass after)
- `TestRecordsShowAuditLog_RenderUnchanged` in `arx_go/render_records_test.go`: execute `records_show.html` "content" via `minimalRecordsShowData(row)` with `"Events"` set to 5 `models.RecordEvent` (types `approved`, `completed`, `unlocked`, `locked`, `custom`; one with Comments; EventDate non-nil in UTC) and `"Snapshots"` = `map[int][]models.SnapshotDiffRow{<completed event ID>: {one row with Status "changed", one "unchanged"}}`. Compare the whitespace-collapsed (`strings.Join(strings.Fields(s), " ")`) substring from `<div class="mt-4">` through the audit block end against a golden string captured by running the test once on UNCHANGED code. After the change, only the parse file list (item 7) and `"AuditResults": true` in the data change; the golden must stay identical. This is the "record page output unchanged incl. Results column" verification.
- `TestBuildTimeline` already pins lock/edit behaviour; no extra characterization needed.
- None knowingly locks in a bug.

### 3. Red tests (fail today, pass after)
- `TestBuildTimeline` new case "unlock rows are not timeline points" (`arx_go/records_timeline_test.go`): rows `lock(at(2,9),"admin")` (set EventType "locked"), `{At: at(3,9), Username:"bob", EventType:"unlocked"}`, `edit(at(4,9),1)`, rev 1; want points = lock marker + edit dot only (edit title `Rev 1 → draft of Rev 2`). Fails today: `EventType` field does not exist (compile) and unlocks would be treated as edit rows.
- `TestAuditLogPartial_FormEvents` (`arx_go/render_records_test.go`): `ParseFS(templatesFS, "templates/shared/audit_log.html")` with `recordsTemplateFuncs()`, execute template `audit_log` with `map[string]any{"Events": []models.RecordEvent{locked/admin/"Released revision 1", unlocked/bob/"Fixing tolerance"}, "UserLoc": time.UTC}`. Assert: contains `Audit log (2)`, `badge bg-secondary">locked`, `badge bg-warning text-dark">unlocked`, `Fixing tolerance`, `bob`; does NOT contain `Results`. Fails today: file does not exist.
- `TestAuditLogPartial_NoEvents`: same with `Events` nil; output (trimmed) is empty. Fails today (same reason).
- `TestAuditLogPartial_ResultsColumn`: `AuditResults: true`, `Snapshots` with one entry; asserts `<th>Results</th>` and `snap<ID>` present. Fails today (same reason).
- `TestIntegration_FormDef_AuditLog` in `arx_go/integration_test.go` next to `TestIntegration_FormDef_ReleaseMarker`: read-only `recGet(h.FormDef, "/forms/6001/def", 6001)`; assert body contains `Audit log (1)`, `Released revision 1`, `admin`, `badge bg-secondary">locked`. Fails today: no audit log on the page.
- `TestIntegration_FormDef_AuditLogUnlockComment` (same file): self-contained via `repInsert` (see `records_reads_integration_test.go` helpers) inserting a `form_events` row (`event_type='unlocked'`, `username='evuser'`, `comments='tolerance fix'`) for the `recSetup` form with cleanup `DELETE`; assert `Audit log (1)` and `tolerance fix` in the `FormDef` body, and that the timeline has no extra point for it (`strings.Count(body, "def-timeline-")` equal before/after insert). Fails today: no audit log.
- Budget: `route_budget_test.go` "form def" row stays 3 (existing test is the check; no edit).

### 4. Manual-only
- Open `/forms/<id>/def` for a released-then-unlocked form: "Audit log (N)" collapsible expands, dates in the viewer's timezone, unlock comment visible, badges match the record page.
- Form with no `form_events`: no audit log section.
- Record page with snapshots: Results column and "View (N)" expand/collapse look identical to before.

## Resolved decisions
1. **Event types: locked/unlocked only.** In the `form_events` branch of `ListFormTimeline` (file change 1) use
   `WHERE fe.form_id = sqlc.arg(form_id) AND fe.event_type IN ('locked', 'unlocked')`, and change the query comment's
   "every form_events row" to "each lock/unlock from form_events". The `buildTimeline` skip guard (file change 2) stays as written.
   Add a red test `TestIntegration_FormDef_AuditLogUnlockComment` variant: also insert an `event_type='archived'` row and assert
   `Audit log (1)` (the archived row is excluded).
2. **Partial wiring: page map + `AuditResults` flag**, parsed into every non-print `records:` page (file changes 3, 6, 7 as written).
3. **Order: `ORDER BY at, event_id`** (file change 1 as written).
4. **Query error: keep the silent omit.** On error, both the timeline and the audit log are absent (file change 2 as written).

## Seed
No new seed rows (form 6001 / row 6201 already seeded). No reseed.

## Verification
`sqlc generate` (then `sqlc diff` clean), `go build ./... && go vet ./... && go test ./...`, integration tests per CLAUDE.md local-container flow. No schema/migration change; no CHANGELOG detail beyond a `### Added` entry under the branch version with `([#487](https://github.com/Jolls/arx/issues/487))`.
