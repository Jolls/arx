# Plan: #659 (RPT-8) — Supplier Performance & Data Quality Reports

## Context

Issue #659 asks for three standalone reports under the Reports tab, siblings to
Spend Analysis (#283/PR #653): **Supplier On-Time Delivery**, **PO Cycle Time**,
and **Attachment/Data Quality Gaps**. All needed columns already exist (`po_line`,
`purchase_order`, `purchase_order_history`, `part`) — no schema/DDL changes, no
new `SQL/*.sql` files, no ArxDev reseed needed. This closes out the last
reporting issue (RPT-8) in the v0.6 milestone's remaining scope besides #660
(blocked on #273) and #651 (unrelated header icon).

Two Explore passes confirmed the exact schema (column names/types) and the
existing report pattern (dashboard-style handlers in `reports.go`, not the
per-form-picker pattern used by Yield/Failure Modes, since these reports aren't
scoped to a test-record form). A Plan agent then produced a concrete,
implementation-ready design, summarized below.

## Approach

### 0. Generalize `spendDateRange` → `reportDateRange`

`reports.go` currently hardcodes `po.date_ordered` inside `spendDateRange.whereClause`.
Rename the type to `reportDateRange` and parameterize `whereClause` by column:

```go
func (rng reportDateRange) whereClause(column string, startArg int) (string, []any)
```

`resolveSpendDateRange` keeps its name (existing call sites untouched) but returns
`reportDateRange`. Update the two existing Spend Analysis call sites to
`rng.whereClause("po.date_ordered", 1)`. Mechanical rename, no behavior change —
new reports reuse this for `po.date_ordered` (On-Time Delivery) and
`poh.changed_at` (Cycle Time).

### 1. Supplier On-Time Delivery — `GET /reports/on-time`

- Evaluate only `po_line` rows with `lead_time_days IS NOT NULL AND date_received
  IS NOT NULL` and `purchase_order.date_ordered IS NOT NULL` (unquoted/unreceived
  lines excluded, not counted late).
- On-time: `date_received <= DATEADD(day, lead_time_days, po.date_ordered)`.
- Per supplier (`po.supplier_name`, denormalized — no company join needed):
  total lines, on-time lines, on-time % (computed in Go to avoid div-by-zero),
  avg days late (signed, `DATEDIFF(day, due_date, date_received)`).
- Sort worst-performers-first (on-time % ascending, tie-break supplier name) —
  this is a watchlist/exception report, unlike Spend's magnitude ranking.
- Date-range filter on `po.date_ordered` via `reportDateRange`.
- New: `onTimeSupplierRow` struct, `queryOnTimeDelivery`, `ReportsOnTime`,
  `ReportsOnTimeExportCSV`, template `reports/on_time.html` (spend.html's
  date-picker form + one card).

### 2. PO Cycle Time — `GET /reports/cycle-time`

- CTE over `purchase_order_history` (`event_type = 'status'`,
  `to_status IN ('draft','open','sent','partially_received','closed')`) using
  `LEAD(changed_at) OVER (PARTITION BY po_id ORDER BY changed_at, id)` to get
  each stage's exit time; exclude rows with no exit (still in that stage —
  open-ended, would skew the average).
- Aggregate: `AVG(DATEDIFF(hour, entered_at, exited_at) / 24.0)` per stage
  (fractional days, since whole-day truncation would show 0 for fast stages),
  plus count of POs that passed through it.
- Output ordered by natural lifecycle progression (Go-side `cycleTimeStageOrder`
  var), not alphabetically.
- Date-range filter on `poh.changed_at` via `reportDateRange`.
- New: `cycleTimeStageRow` struct, `cycleTimeStageOrder`, `queryPOCycleTime`,
  `ReportsCycleTime`, `ReportsCycleTimeExportCSV`, template `reports/cycle_time.html`.

### 3. Attachment / Data Quality Gaps — `GET /reports/data-quality`

One page, three cards, **no date range** (point-in-time snapshot):

- **No attachments**: `category IN ('BUY','ASM','DWG') AND is_active = 1 AND
  attachment_count = 0` (use the denormalized, trigger-maintained
  `part.attachment_count` column directly — don't re-`COUNT(part_attachment)`).
- **Missing default supplier**: `category = 'BUY' AND is_active = 1 AND
  default_supplier_id IS NULL` — scoped to BUY only, since ASM/DWG parts are
  typically manufactured in-house and don't need a direct purchasing supplier.
- **No/stale cost rollup**: `is_active = 1 AND (last_rollup_cost IS NULL OR
  last_rollup_at IS NULL)` — NULL-only, no age threshold. No existing
  staleness-by-age convention in the codebase to anchor an arbitrary number on;
  "never rolled up" is the unambiguous reading of "missing," and an age
  threshold can be added later if the business defines one. Not scoped to
  BUY/ASM/DWG — any active part can carry a rollup cost.
- Shared `dataQualityPartRow{PartNumber, Title, Category}` struct for all three.
- New: `queryPartsNoAttachments`, `queryPartsMissingDefaultSupplier`,
  `queryPartsStaleRollup`, `ReportsDataQuality`, three export handlers,
  template `reports/data_quality.html` (three cards, no date form).

### 4. CSV export

Include export on all three reports for consistency with Spend Analysis (cheap:
reuses existing `writeSpendCSV` helper + the same query). Easiest place to trim
if scope needs cutting: the three Data Quality Gaps exports (less obviously
"export-worthy" than aggregate numbers) — keep on On-Time Delivery and Cycle Time
if trimming.

### 5. Wiring

- `arx_go/main.go` (~L176): add 7 routes (`/reports/on-time[+export]`,
  `/reports/cycle-time[+export]`, `/reports/data-quality` +3 exports).
- `arx_go/templates/shared/partials.html` `reports_tabs` (~L41-49): add 3 `<a>`
  entries (On-Time Delivery, PO Cycle Time, Data Quality Gaps) after Spend
  Analysis, in issue order.
- All new Go code lives in `arx_go/reports.go` (same file as Spend Analysis —
  matches existing convention of one file per dashboard-style report group).
- Table names via existing `arxlib/config/config.go` helpers only
  (`POLineTable()`, `POTable()`, `POHistoryTable()`, `PartsTable()`) — no new
  helpers needed, no `SQL/schema.md` changes (no new tables).

## Critical files

- `arx_go/reports.go` — all new query/handler code, plus the `reportDateRange` rename
- `arx_go/main.go` — route registration
- `arx_go/templates/shared/partials.html` — `reports_tabs` partial
- `arx_go/templates/reports/spend.html` — pattern to model the 3 new templates on
- New: `arx_go/templates/reports/on_time.html`, `cycle_time.html`, `data_quality.html`

## Verification

- `cd arx_go && go build && go vet ./... && go test ./...` (per CLAUDE.md; no
  integration-test changes needed since no schema/DDL touched, but sanity-check
  `integration_test.go` still compiles).
- Manual verification (I won't run the app myself per CLAUDE.md): user should
  open Reports tab, confirm the 3 new subtabs appear and render with real
  ArxDev/prod data, sanity-check the on-time %, cycle-time averages, and data
  quality lists against a few known POs/parts, and try each CSV export link.
- After implementation, suggest (per CLAUDE.md "Consider Tests") a couple of
  targeted unit tests: the cycle-time `LEAD()` stage-duration math (including a
  PO left mid-stage, which must be excluded) and the on-time boundary condition
  (`date_received` exactly equal to the due date counts as on-time). Don't write
  them until the user agrees.

## Branching

Currently on `main` — before the first commit, create
`feature/issue-659-supplier-performance-reports` and push it, per CLAUDE.md
branching rules. PR description will use `Closes #659`.
