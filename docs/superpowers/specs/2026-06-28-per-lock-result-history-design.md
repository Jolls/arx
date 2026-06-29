# Per-lock result history (#251) — design

Track how a test record's results change across its lifecycle by snapshotting the
result values at each **Complete/lock** transition, linked to the existing
`record_events` row, and surfacing the snapshot (with a computed diff) in the
record detail view.

Builds on the record lifecycle (#249/#250) and result materialization (#487),
both shipped.

## Decisions

| Question | Decision |
|---|---|
| Storage shape | New child table `record_event_results`, keyed to `record_events.id`. |
| When to snapshot | Every Complete/lock event only (single + bulk). **Not** on approve/unlock. |
| Display | Expand a Complete event in the timeline to see the full result table as it was, with changes vs. the prior Complete snapshot highlighted. Diff computed at render time. |
| Existing records | One-time **backfill** as a bulk action on `/forms/{id}/records` (reuses the bulk-select toolbar) that runs the snapshot step against selected completed records. Self-hides when done; route slated for removal in a later release. |

### Why Complete-only

A record is **locked** (read-only) between Complete and Approve, and is still at its
completed values at the instant of Unlock. Results can only change while the record is
WIP. So the only point where the result state meaningfully differs from the last capture
is at each Complete. Snapshotting on approve/unlock would store byte-identical duplicates
of the prior Complete snapshot — more storage, zero extra information.

Each re-completion after a reopen-and-edit produces a new version; the diff between
consecutive Complete snapshots is exactly "what changed during that reopen."

### Why a new table (not JSON on `record_events`, not diff-only)

- **Normalized child table** matches how `test_result` already stores frozen
  snapshots (#487), is trivially queryable, and lets the diff be computed at render
  time rather than stored.
- **JSON column** would be the lightest write path but SQL Server JSON is awkward to
  query and breaks the repo's normalized-table convention.
- **Diff-only rows** are smallest but lossy and require comparison logic on the write
  path; the issue flags this as the most speculative.

## New table — `record_event_results`

```sql
CREATE TABLE record_event_results (
  id         INT          PRIMARY KEY IDENTITY,
  event_id   INT          NOT NULL REFERENCES dbo.record_events(id),  -- the Complete event this snapshot belongs to
  test_id    INT          NOT NULL,    -- which step (FK to test_definition.id)
  parameter  VARCHAR(255),             -- resolved snapshot, so the row renders without a join
  result     VARCHAR(255),
  pass_fail  BIT,                       -- 1=PASS, 0=FAIL, NULL=not evaluated
  comment    VARCHAR(255)
);
```

- Rows are inserted **in frozen-row display order**, so rendering is `ORDER BY id`
  — no sort column needed.
- Only data-row values are captured (`result` / `pass_fail` / `comment`) plus
  `parameter` for labeling. Headings and spec columns are **not** stored: the snapshot
  view is a compact `parameter | result | P/F | comment` table.
- `parameter` is the resolved snapshot value, so the history renders without joining
  back to `test_result` / `test_definition` (which may have since changed).

## Write path

Each Complete handler — `LockRecord` and `BulkLockRecords` — after writing the
`record_events` row, captures the new event id and bulk-copies the current
`test_result` rows for the record into `record_event_results`.

The event-insert + snapshot are wrapped in a `beginTx` so an event never exists
without its snapshot (mirroring the transactional history pattern of
`recordPOStatusChange`). `BulkLockRecords` wraps each record's event+snapshot per
iteration.

Shared helper:

```go
// snapshotRecordResults copies the record's current test_result rows into
// record_event_results, linked to the given Complete event. Inserts in frozen-row
// display order so the snapshot renders by id. Shared by the live lock handlers and
// the one-time backfill.
func (h *Handler) snapshotRecordResults(ctx context.Context, tx *txLogger, eventID, recordID int) error
```

## Read / display

In `RecordDetail`:

1. Load all snapshots for the record grouped by `event_id`, oldest → newest.
2. For each snapshot, compute a per-row status vs. the previous snapshot, matching on
   `test_id`: **added** / **changed** / **unchanged**. The earliest snapshot has no
   prior and renders as a plain baseline (no highlighting).
3. The existing Events timeline in `records_show.html` gains an expandable panel
   under each `completed` event showing the snapshot table with changed cells
   highlighted (Bootstrap `table-warning` / badges — no custom CSS).

## Backfill (existing completed records)

Records completed before this ships have no snapshot. A one-time backfill gives each a
baseline so its `completed` event expands and so future re-completions have something to
diff against.

- **Surface:** a bulk action on `/forms/{id}/records`, reusing the existing bulk-select
  toolbar that `BulkLockRecords` already uses (`POST /forms/{id}/records/bulk-...`). The
  user selects records and runs the action against the selection — no new selection UI.
- **Runs through Arx, not raw SQL:** the handler calls `snapshotRecordResults` so the
  snapshot is built/ordered/rendered identically to a live capture. (A pure-SQL script
  would have to re-derive frozen-row display order in T-SQL and couldn't set things up to
  render properly.) This is the whole reason the backfill is "known by Arx": it runs the
  same code path, so its output is an ordinary snapshot the UI already renders — no
  marker or extra column required.
- **What it does:** for each selected completed record (`is_locked=1`) whose latest
  `completed` `record_events` row has no `record_event_results`, call
  `snapshotRecordResults` against that event, capturing the record's **current**
  `test_result` values. Because the record has been locked since that completion, the
  current values *are* the completion values — the snapshot is truthful for the event it
  attaches to. Earlier completions (pre-migration reopen cycles) are not reconstructable
  and are left uncaptured; those older `completed` events simply don't expand.
- **Idempotent:** skips any record whose latest `completed` event already has snapshot
  rows, so it is safe to re-run.
- **Gating:** admin/maintenance-only. Exact permission to confirm during planning
  (candidate: `can_approve_records`, the existing privileged-TR gate).

### Lifecycle — this is migration-only code

- **Self-hiding:** the bulk action renders only when the form has at least one completed
  record lacking a snapshot. Once every completed record in the form is backfilled, the
  button disappears on its own — no stale migration button left in the UI.
- **Planned removal:** the route, handler, and button are a one-time migration aid. A
  follow-up issue tracks deleting them entirely in a later release, after the user
  confirms production data is fully backfilled. (Create this issue when the PR opens.)

## Scope notes

- **Shared code, honestly.** `record_events` is already the TR analog of
  `purchase_order_history`, so there is no literal audit table to merge. What is
  shared is the *pattern*: explicit-write history in a tx (like
  `recordPOStatusChange`) and the timeline-rendering approach. The lock handlers
  keep using `record_events.username` from `currentUser`, consistent with existing
  events (PO's `actorName` fallback-to-"system" helper stays PO-side).

## Required schema sync (per CLAUDE.md)

Adding a table means updating all of:

1. `SQL/TestRecords.sql` — DDL for `record_event_results`.
2. `SQL/_test.sql` — DROP + `SELECT * INTO` so the ArxDev populate script includes it.
3. `arxlib/config/config.go` — `RecordEventResultsTable()` returning the bare name.
4. `SQL/schema.md` — table reference entry.

## Open / to confirm

- **Table name.** Chosen `record_event_results` (clearly a child of `record_events`)
  over `test_result_history`; the latter implies the per-result-edit auditing this
  issue explicitly moved away from.
- **Backfill gating.** Which permission/surface the admin backfill action lives behind
  (Settings page? `can_approve_records`?) — to settle during planning.
- **Testability.** Diff computation (added/changed/unchanged by `test_id`) is pure
  logic and a good unit-test candidate; snapshot ordering likewise. The DB write path
  is covered by the integration suite if exercised there.
