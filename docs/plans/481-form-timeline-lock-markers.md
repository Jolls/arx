# #481 — Form history timeline: release (lock) markers and revision per point

Decisions (from /update-issue): lock markers only (no unlock markers); a calendar day (viewer tz) that has a lock
gets no edit dot; a lock marker's "changed" highlight = steps with history in [previous lock, this lock); timeline
renders when there is any edit day or lock. No schema change.

## File changes

### 1. `internal/records/records.sql` — replace `ListFormHistoryStamps` (lines 47-52)
One query feeds the whole timeline so `FormDef` keeps its round-trip budget of 3 (`route_budget_test.go:103`).
```sql
-- name: ListFormTimeline :many
-- The definition page's timeline sources, oldest first (#481): each step history entry (is_lock false,
-- username '') and each release from form_events (is_lock true, form_row_id 0). Unlocks aren't shown.
SELECT changed_at AS at, form_row_id, ''::text AS username, FALSE AS is_lock
FROM form_row_history
WHERE form_row_id IN (SELECT id FROM form_row WHERE form_id = sqlc.arg(form_id))
UNION ALL
SELECT event_date, 0, COALESCE(username, ''), TRUE
FROM form_events
WHERE form_id = sqlc.arg(form_id) AND event_type = 'locked'
ORDER BY at, is_lock;
```
Run `sqlc generate`. `ListFormStepsAt` is unchanged (its `[day_start, day_end)` args take any range).

### 2. `internal/records/records.go`
- Line 46: `HistoryStamp = dbq.ListFormHistoryStampsRow` → `TimelineRow = dbq.ListFormTimelineRow`.
- Lines 177-179: replace with
  ```go
  // ListFormTimeline returns the form's step-history stamps and release events, oldest first (#481).
  func (s *Service) ListFormTimeline(ctx context.Context, formID int) ([]TimelineRow, error) {
  	return s.q.ListFormTimeline(ctx, formID)
  }
  ```
- Line 181 comment: `// ListFormStepsAt returns the form's steps as they stood at end, flagging those changed in [start, end).`
  (rename params `dayStart, dayEnd` → `start, end` in the signature only; body passes them to the same fields).

### 3. `arx_go/records.go`
- Replace the inline `HistoryPoint` type + bucketing (lines 354-401) with
  `rows, err := h.records().ListFormTimeline(r.Context(), formID)`; on `err == nil` call
  `histPoints = buildTimeline(rows, h.userLocation(r), form.Revision)` (keep today's behavior: a query error
  just omits the timeline). Keep the existing #847/#192 timezone comment above the call.
- New package-level type + func (below `FormDef`, above `timelinePositions`):
  ```go
  // historyPoint is one timeline point (#478/#481): an edit day (At = local midnight) or a release
  // (IsLock, At = the lock instant).
  type historyPoint struct {
  	At       time.Time
  	IsLock   bool
  	Since    time.Time // lock: the previous lock instant, zero for the first
  	Title    string    // tooltip, also the "Showing state after:" label
  	PctLeft  float64   // position along timeline bar (5–95%)
  }

  // buildTimeline turns ListFormTimeline rows (oldest first) into timeline points (#481). Edit rows are
  // bucketed per calendar day in loc, counting distinct steps; a day that has a release gets no edit dot
  // (its edits show through the release's changed highlight). Revisions count back from currentRev: a
  // point's revision is currentRev minus the releases after it, so releases older than form_events still
  // count. Positions come from timelinePositions.
  func buildTimeline(rows []records.TimelineRow, loc *time.Location, currentRev int) []historyPoint
  ```
  Algorithm:
  1. `lockDays := map[string]bool{}` of `row.At.In(loc).Format("2006-01-02")` for lock rows; `totalLocks` = their count.
  2. Walk rows in order with `locksSeen := 0`, `var prevLock time.Time`, a current edit bucket
     `{key string, rows map[int]bool, idx int}`.
     - Lock row: `locksSeen++`; append `historyPoint{At: row.At, IsLock: true, Since: prevLock,
       Title: fmt.Sprintf("Released Rev %d — %s — %s", currentRev-(totalLocks-locksSeen), row.Username,
       row.At.In(loc).Format("2006-01-02 15:04"))}`; `prevLock = row.At`.
     - Edit row: `key := row.At.In(loc).Format("2006-01-02")`; skip if `lockDays[key]`. If `key` differs from
       the current bucket's key, `day, err := time.ParseInLocation("2006-01-02", key, loc)` (skip row on err,
       as today), append `historyPoint{At: day}` and start a new bucket pointing at its index, recording
       `rev := currentRev-(totalLocks-locksSeen)` on the bucket. Add `row.FormRowID` to the bucket's set.
  3. After the walk, set each edit point's `Title` = `fmt.Sprintf("%s — %d change%s — %s", day.Format("2006-01-02"),
     n, plural, revLabel)` where `n` = distinct rows, plural = `"s"` when n > 1, and
     `revLabel` = `"Draft"` when rev == 0 else `fmt.Sprintf("Rev %d → draft of Rev %d", rev, rev+1)`.
     (Simplest: keep a parallel slice of buckets `{idx, rows, rev}` and fill titles at the end.)
  4. `times[i] = points[i].At`; `for i, pct := range timelinePositions(times, time.Now()) { points[i].PctLeft = pct }`.
  Return `nil` for no rows.
- `FormDefHistory` (lines 442-492): accept `at` as a day **or** an RFC3339 instant.
  - Doc comment: `// FormDefHistory — GET /api/forms/{id}/def/history?at=<YYYY-MM-DD> | ?at=<RFC3339>&since=<RFC3339|"">`
    then: "A day returns the state at the end of that day, changed = history within the day (#478). An instant
    (a release marker, #481) returns the state at that instant, changed = history in [since, at); empty since =
    from the start."
  - Logic replacing lines 451-461:
    ```go
    atStr := r.URL.Query().Get("at")
    var start, end time.Time
    if at, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
    	end = at
    	if s := r.URL.Query().Get("since"); s != "" {
    		if start, err = time.Parse(time.RFC3339Nano, s); err != nil {
    			http.Error(w, "bad since param", http.StatusBadRequest)
    			return
    		}
    	}
    } else {
    	// existing day branch (keep its #847/#192 comment): ParseInLocation in h.userLocation(r) → start=dayStart, end=dayStart+1 day; "bad at param" 400 on failure
    }
    ```
    and call `ListFormStepsAt(r.Context(), formID, start.UTC(), end.UTC())`. (Zero `start` = 0001-01-01 UTC, valid timestamptz.)

### 4. `arx_go/templates/records/form_def.html` (lines 77-84)
Replace the `range .HistPoints` body:
```html
    {{range .HistPoints}}
    {{if .IsLock}}
    <button type="button"
            class="def-timeline-dot def-timeline-lock"
            style="left:{{printf "%.1f" .PctLeft}}%"
            data-at="{{.At.UTC.Format "2006-01-02T15:04:05.999999999Z07:00"}}"
            data-since="{{if not .Since.IsZero}}{{.Since.UTC.Format "2006-01-02T15:04:05.999999999Z07:00"}}{{end}}"
            title="{{.Title}}"><i class="bi bi-lock-fill"></i></button>
    {{else}}
    <button type="button"
            class="def-timeline-dot"
            style="left:{{printf "%.1f" .PctLeft}}%"
            data-at="{{.At.Format "2006-01-02"}}"
            title="{{.Title}}">
    </button>
    {{end}}
    {{end}}
```
The `{{if .HistPoints}}` guard stays (locks are now points, so a lock-only form shows the timeline).

### 5. `arx_go/static/records/app.js` (line 613)
```js
    var q = 'at=' + encodeURIComponent(dot.dataset.at)
    if (dot.dataset.since !== undefined) q += '&since=' + encodeURIComponent(dot.dataset.since)
    fetch('/api/forms/' + formId + '/def/history?' + q)
```
Prev/next need no change: lock markers carry `.def-timeline-dot`, so they're in `dots` in DOM (time) order.

### 6. `arx_go/static/app.css` (after line 390)
Icon colour via CSS vars, not `text-success` (Bootstrap utilities are `!important` and would block the active colour).
```css
/* Release (lock) marker on the history timeline (#481): a padlock instead of a dot. */
.def-timeline-dot.def-timeline-lock,
.def-timeline-dot.def-timeline-lock:hover,
.def-timeline-dot.def-timeline-lock.active {
    width: auto; height: auto;
    border: 0; border-radius: 0;
    background: var(--bs-tertiary-bg);
    color: var(--bs-success);
    font-size: 14px; line-height: 1;
}
.def-timeline-dot.def-timeline-lock:hover  { color: var(--bs-success-text-emphasis); }
.def-timeline-dot.def-timeline-lock.active { color: #ed7d31; font-size: 18px; }
```

### 7. `CHANGELOG.md` — one `### Added` line under the next version at commit time.

## Test plan

**Coverage audit**
- `arx_go/records_timeline_test.go` `TestTimelinePositions` — positions (unchanged function).
- `arx_go/integration_test.go` `TestIntegration_FormDefHistory_EndOfDayState` — day-mode endpoint (must stay green).
- `arx_go/integration_test.go` FormDef(6001) render test (~line 3950) — page renders.
- `arx_go/records_reads_integration_test.go:530` — unknown-id handling incl. FormDefHistory.
- `arx_go/records_reads_integration_test.go` `TestIntegration_RecordsReads_FormDefTimeline` — edit-dot titles;
  pins the old title end, so update its expected strings to the new format (found at implementation time).
- `arx_go/route_budget_test.go:103,184` — FormDef budget 3, def history budget 1 (must stay green: proves the UNION keeps round trips flat).

**Characterization tests** — none new; the day-mode integration test above already pins the endpoint behavior the
change touches, and the bucketing moves into `buildTimeline`, which the red tests cover (incl. distinct-step counting).

**Red tests**
- `arx_go/records_timeline_test.go` `TestBuildTimeline` (stub `buildTimeline` returning `nil` first so it compiles).
  Fixed `loc = America/Los_Angeles`; rows built as `records.TimelineRow{At, FormRowID, Username, IsLock}`. Cases
  (assert len, IsLock, At, Since, Title per point; ignore PctLeft):
  1. *edits only, never released* — currentRev 0, two rows same step same day + one row next day → 2 edit points,
     titles `"<d1> — 1 change — Draft"` (distinct steps), `"<d2> — 1 change — Draft"`.
  2. *lock day yields* — currentRev 1: edit d1 (step A), edit d2 (step B), lock d2 by "admin", edit d3 →
     points: edit d1 `"<d1> — 1 change — Draft"`, lock `"Released Rev 1 — admin — <d2 HH:MM>"`
     with zero Since, edit d3 `"<d3> — 1 change — Rev 1 → draft of Rev 2"`. No d2 edit point.
  3. *count back past missing events* — currentRev 3, one lock event, an edit before it and one after → lock
     `"Released Rev 3 …"`, edit before `"… Rev 2 → draft of Rev 3"`, edit after `"… Rev 3 → draft of Rev 4"`.
  4. *two locks same day* — both appear; second's `Since` == first's `At`; titles Rev N-1 / Rev N.
  5. *viewer-tz day* — edit 2026-05-28T23:30Z and lock 2026-05-29T01:00Z are both May 28 in LA → no edit point.
  6. *lock only* — one lock point; `nil` rows → `nil`.
  Fails today: stub returns nil.
- `arx_go/integration_test.go` `TestIntegration_FormDefHistory_ReleaseState`: throwaway part/form/form_row
  (spec_max 100, same setup/cleanup as `…EndOfDayState`, plus `DELETE FROM form_events WHERE form_id=$1`);
  save 150 (tx + SetAuditUser); `records.New(h…).InsertFormEvent(ctx, formID, "locked", "itest", "")`; save 200.
  Read `event_date` back (`SELECT event_date FROM form_events WHERE form_id=$1`). GET
  `?at=<event_date RFC3339Nano>&since=` → 200, one step, `spec_max == "150"`, `changed == true`. Then GET with
  `since=<same instant>` → `changed == false`. Fails today: RFC3339 `at` → 400 "bad at param".
- `arx_go/integration_test.go` `TestIntegration_FormDef_ReleaseMarker`: `FormDef` on seed form 6001 (revision 1,
  one `locked` event 2026-05-28 by admin) → body contains `def-timeline-lock` and `Released Rev 1 — admin`.
  Fails today: no lock markers rendered.

**Manual-only**
- Padlock look (size, colour, hover, active orange) in light and dark themes; line doesn't show through it.
- Tooltips on edit dots and lock markers; "Showing state after: …" label on click.
- Clicking a lock marker updates the table + highlight; prev/next walks dots and markers in order.
- A form with releases but no edits (e.g. lock a fresh form) shows the timeline.
- No new seed rows: seed form 6001 already has a `locked` event (`SQL/seed_test_data.sql:833`).

## Post-implementation simplification (Opus review of the whole timeline feature)
- `buildTimeline`: running `rev` (start at `form.revision` − releases, +1 per release) replaces the bucket
  struct/slice and the trailing title loop; an edit day's point is always `points[len-1]`.
- `historyPoint.Since` → `Query` (the full FormDefHistory query string, built in Go): one template branch
  with `data-query`, and the JS just fetches `history?` + `data-query`.
- CSS: one selector for the lock marker's base rule (the `:hover`/`.active` members were redundant).
- Tests: `ReleaseState` seeds fixed-timestamp history/event rows on `recForm`; `EndOfDayState`
  (pre-existing) moved onto `recForm` + the shared `defHistoryStep` helper; `FormDef_ReleaseMarker` uses `recGet`.
- JS `applyHistory` (pre-existing): one row per step, so plain `stepMap[s.id] = s` and `classList.toggle`.
- `ListFormStepsAt` comment covers release instants as well as day ends.
