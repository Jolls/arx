# Advanced filters on the record list — Design (#247)

> Issue: [#247](https://github.com/Jolls/arx-legacy/issues/247) — Advanced filters on the record list page
> Branch: `feature/record-list-filters-247`
> Status: approved 2026-06-28

## Goal

Extend the per-form records list (`GET /forms/{id}/records`) beyond the current
WIP-only toggle with filter controls for **lifecycle status**, **type**,
**pass/fail**, and **date range**. Filters persist as query params so a filtered
view is shareable/bookmarkable.

**No DB change** — pure binary swap.

## Why server-side (not the #449 pattern)

The Recording-Reports filter (#449, PR #453) is client-side: per-column text
inputs filtering rendered rows with JS `.includes()`, not persisted to the URL.
That pattern cannot satisfy #247's explicit requirements:

- **Shareable/bookmarkable URLs** → state must live in query params.
- **Date range** and **pass/fail** → pass/fail is stored per *result*
  (`test_result.pass_fail`, nullable BIT); a record-level verdict must be
  aggregated across result rows, which JS over a paginated row set can't do.

So #247 filters **server-side** in the `RecordsList` handler. This is a
deliberately different pattern from #449, justified by the shareable-URL and
aggregation requirements — not an inconsistency.

## Current state

`RecordsList` ([arx_go/records.go:223](../../../arx_go/records.go)) loads the form
header, then records with `WHERE form_id=@p1 AND is_active=1`, optionally
`AND is_locked=0` when WIP-only (default; `?wip=false` shows all). Order:
`TRY_CAST(serial_number AS INT) DESC, record_date DESC`.

Template `records_index.html` renders a WIP toggle button, a client-side column
dropdown, sortable headers, and client-side pagination. The **Select column +
bulk-lock** toolbar are gated on `WIPOnly`.

"Type" in the UI is the free-text `test_record.comments` column (the model field
is literally documented `// used as "Type" in the UI`).

## Filter controls

A Bootstrap filter row above the table, rendered as a `GET` form so Apply writes
params to the URL. Each control repopulates from the current query params.

| Control | Param | Values / behavior |
|---|---|---|
| **Status** `<select>` | `status` | `wip` (default) / `complete` / `approved` / `all`. **Replaces** the WIP toggle. |
| **Type** combobox | `type` | `<input list="type-options">` + `<datalist>` populated with distinct `comments` for this form. Exact match when set; free-typeable. |
| **Pass/Fail** `<select>` | `pf` | `all` (default) / `pass` / `fail`. |
| **From** `<input type=date>` | `from` | `record_date >= from`. |
| **To** `<input type=date>` | `to` | `record_date < to + 1 day` (whole day inclusive). |
| **Apply** button | — | Submits the GET form. |
| **Clear** link | — | Links to bare `/forms/{id}/records`. |

## Server logic (`RecordsList`)

Build the record query incrementally. Base:
`WHERE form_id=@pN AND is_active=1`. Append parameterized clauses per supplied
param (never interpolate user values into SQL):

- **status**
  - `wip` → `AND is_locked = 0`
  - `complete` → `AND is_locked = 1 AND is_approved = 0`
  - `approved` → `AND is_approved = 1`
  - `all` → no clause
  - missing/unrecognized → treated as `wip` (preserves today's default view)
- **type** → `AND comments = @pN`
- **pf** (EXISTS over `test_result`, aliased table via `cfg.ResultsTable()`):
  - `fail` → `AND EXISTS (SELECT 1 FROM <results> tr WHERE tr.record_id = <rec>.id AND tr.pass_fail = 0)`
  - `pass` → `AND NOT EXISTS (… pass_fail = 0) AND EXISTS (… pass_fail = 1)`
    (has at least one evaluated passing result and no failing result)
  - `all` → no clause
- **from** → `AND record_date >= @pN`
- **to** → `AND record_date < DATEADD(day, 1, @pN)`

Order clause unchanged.

Datalist source — a small separate query:
```sql
SELECT DISTINCT comments FROM <records>
WHERE form_id = @p1 AND is_active = 1 AND comments <> ''
ORDER BY comments
```

Pass selected filter values + the datalist options + a `StatusWIP` bool into the
template via a small `Filters` view struct (replaces the bare `WIPOnly` key).

## Knock-on adjustments

- **Select column + bulk-lock**: gate on `status == "wip"` (the new `StatusWIP`
  flag) instead of `WIPOnly`. Bulk-lock only applies to WIP records.
- **Column toggle / sort / pagination**: unchanged — they operate over whatever
  server-filtered rows render.
- **Post-bulk-lock redirect** (`?locked=N`): unchanged; lands on the default WIP
  view and shows the success banner.
- **`?wip=false`** is dropped, superseded by `status=all`. It is an internal
  toggle link, not a documented/bookmarked URL.

## Verification

The filter logic lives in SQL, so a `ComputePassFail`-style unit test isn't the
right fit. Recommended: a **build-tagged integration test** (`//go:build
integration`, ArxDev only) that seeds a handful of records with varied
status/type/date and result pass_fail values, then asserts each filter param
returns the expected record set — including the pass/fail aggregation edge case
(record with only NULL pass_fail results appears under `all` but neither `pass`
nor `fail`).

## Out of scope

- Saved / named filters.
- Multi-select on any filter.
- Normalizing or validating the free-text `comments`/Type values.
- Any new column or table.
