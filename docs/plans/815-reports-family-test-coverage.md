# 815 — reports.go query/handler/CSV test coverage

## Open questions

1. **`queryOnTimeDelivery` has no seed row that satisfies its own filter.**
   The query requires, on the same `po_line` row: `lead_time_days IS NOT NULL AND
   date_received IS NOT NULL AND po.date_ordered IS NOT NULL`. Checking
   `SQL/seed_test_data.sql`:
   - Only two lines have `lead_time_days` set: `5511` (14) and `5512` (21) — both
     have `date_received = NULL` (they're in-flight RFQ quotes, never received).
   - Lines that do have `date_received` set (`5504`, `5505`) have `lead_time_days
     = NULL`.
   So with current seed data, `queryOnTimeDelivery` always returns an **empty
   slice** — there is no fixture row to assert an on-time/late percentage
   against. The plan below tests the empty-result path only (proves the query
   runs and returns zero rows, catching a SQL-syntax regression), which is
   weaker than the other report families. Closing this gap for real requires a
   new `po_line` seed row with all three fields set (e.g. add `lead_time_days`
   to line `5504`, which already has `date_received`/`date_ordered`) — that's a
   `SQL/seed_test_data.sql` edit needing a human reseed of ArxDev, flagged here
   rather than done unilaterally.

No other ambiguity found — `queryPOCycleTime` and the three data-quality query
variants each have at least one deterministic, seed-pinned case to assert
against (worked out below).

### Resolved decision

User chose: **add the seed row now, ask the user to reseed ArxDev** before the
on-time tests are expected to pass (not the empty-result-only fallback).

Implementation must:
1. Edit `SQL/seed_test_data.sql`: add `lead_time_days` to `po_line` row `5504`
   (which already has `date_received`/`date_ordered` set) so it satisfies
   `queryOnTimeDelivery`'s filter. Compute the value from `date_ordered` →
   `date_received` for line 5504 (read the actual seed row to get exact dates,
   set `lead_time_days` to the whole-day difference).
2. Rewrite `TestIntegration_QueryOnTimeDelivery_EmptyWithCurrentSeed` (test 3)
   and `TestIntegration_ReportsOnTimeExportCSV` (test 9) below to assert the
   real on-time-percentage math against line 5504 instead of an empty result —
   compute the expected row (`SupplierName`, `TotalLines=1`, `OnTimeLines`
   0 or 1 depending on whether 5504 was on-time per the query's on-time
   definition, `OnTimePct`, `AvgDaysLate`) from the actual seed values once
   read.
3. At the end of implementation, tell the user ArxDev needs reseeding
   (`SQL/seed_test_data.sql` changed) before these two tests will pass — per
   CLAUDE.md, reseeding is a human action, never scripted by the agent.

## Test file

All new tests go in the existing `arx_go/integration_test.go` (build tag
`integration`, live ArxDev via `ARX_TEST_DSN`), following the file's existing
`liveHandler(t)` / seed-ID-driven pattern (e.g.
`TestIntegration_DashboardBelowReorderParts`, `TestIntegration_DashboardPendingApprovalPOs`).
No new test file — these are read-only queries against the same handler/DB
surface already exercised there. `arx_go/reports_test.go` (plain unit test,
already covers `resolveSpendDateRange`) is left untouched; nothing new belongs
there since every remaining function needs a live DB.

Rationale for integration-only (no mocked-DB unit tests): confirmed by
`TestIntegration_DashboardBelowReorderParts` / `TestIntegration_DashboardPendingApprovalPOs`
/ `TestIntegration_DashboardStaleWIPRecords` (issue #816's dashboard cards) —
the established pattern for read-only reporting queries in this codebase is a
live-DB integration test asserting against pinned seed IDs, not a mock. These
19 functions follow the same shape (real SQL aggregation/joins against
PO/parts/suppliers), so the same pattern applies.

## Fixture values used (from `SQL/seed_test_data.sql`, pinned IDs)

All spend/on-time/cycle-time tests use an **unbounded date range**
(`reportDateRange{}` zero value, or query string `?range=custom` with no
`from`/`to`) so results are all-time totals independent of "now" — avoids
flakiness from the this_month/this_quarter/ytd presets drifting relative to
seed dates.

Computed from `po_line` (5501–5512) joined to `purchase_order` (5001–5011):

- **Spend by supplier** (all-time): `Acme Fasteners` = 282.50 (5501: 10×2.50 +
  5504: 50×4.10 + 5505: 10×2.50 + 5511: 500×0.055), `Precision Machining Co` =
  108.00 (5502: 20×2.50 + 5503: 200×0.05 + 5506: 500×0.048 + 5512: 500×0.048).
  Sorted descending: Acme Fasteners first.
- **Spend by part**: `RAW-1002` (Stainless Steel Bar Stock) = 205.00 (line
  5504 only), `RAW-1001` (Aluminum Stock 6061) = 100.00 (5501+5502+5505),
  `BUY-1001` (M3x8 SHCS) = 85.50 (5503+5506+5511+5512). Sorted descending:
  RAW-1002, RAW-1001, BUY-1001. Sum of all three = 390.50 = sum of both
  supplier totals above (cross-check).
- **PO cycle time**: only `purchase_order_history` rows `5801`
  (po 5002, status→draft, 2026-04-28) and `5804` (po 5002, status→open,
  2026-05-01) form a LEAD-paired stage/exit; every other history row is
  either an `approval` event (filtered out) or has no following status row
  (exited_at NULL, filtered out). Result: exactly one row, `Stage="draft"`,
  `POCount=1`, `AvgDays=3.0` (2026-04-28 → 2026-05-01 = 72h / 24 = 3.0).
- **Data quality — no attachments**: active BUY/ASM parts with
  `attachment_count=0`. Part `3002` (BUY-1001) has attachment `8101` so it's
  excluded; part `3009` (BUY-1004) is inactive so excluded. Expected present:
  `BUY-1002` (3003), `BUY-1003` (3008), `ASM-1001` (3005), `ASM-1002` (3012),
  `ASM-1003` (3013). Expected absent: `BUY-1001`, `BUY-1004`.
- **Data quality — missing default supplier**: active BUY parts with
  `default_supplier_id IS NULL`. Only `BUY-1003` (3008) qualifies (`3002`/`3003`
  have a default supplier set, `3009` is inactive). Expected: exactly one row,
  `BUY-1003`.
- **Data quality — stale rollup**: active parts with
  `last_rollup_cost IS NULL OR last_rollup_at IS NULL`. Parts `3005` (ASM-1001)
  and `3012` (ASM-1002) have both fields set (not stale); everything else
  active does not. Expected present: `RAW-1002` (3007, used as the anchor
  assertion); expected absent: `ASM-1001`, `ASM-1002`.

## Test functions to add

1. **`TestIntegration_QuerySpendBySupplier`** — covers `querySpendBySupplier`.
   Call directly with `reportDateRange{}`. Assert result contains
   `SupplierName="Acme Fasteners"` with `TotalSpend==282.50` and
   `SupplierName="Precision Machining Co"` with `TotalSpend==108.00`
   (`assertFloatEqual`), and that Acme Fasteners sorts before Precision
   Machining Co (descending order).

2. **`TestIntegration_QuerySpendByPart`** — covers `querySpendByPart`. Call
   directly with `reportDateRange{}`. Assert `RAW-1002` TotalSpend==205.00,
   `RAW-1001` TotalSpend==100.00, `BUY-1001` TotalSpend==85.50, and that they
   appear in that descending order.

3. **`TestIntegration_QueryOnTimeDelivery_EmptyWithCurrentSeed`** — covers
   `queryOnTimeDelivery`. Call directly with `reportDateRange{}`. Assert `err
   == nil` and `len(rows) == 0`, with a comment referencing open question #1
   above (proves the query executes without a SQL error; does not validate the
   on-time-percentage math — no fixture triggers it yet).

4. **`TestIntegration_QueryPOCycleTime`** — covers `queryPOCycleTime`. Call
   directly with `reportDateRange{}`. Assert exactly one row is present with
   `Stage=="draft"`, `POCount==1`, and `AvgDays` within `assertFloatEqual`
   tolerance of `3.0`.

5. **`TestIntegration_QueryDataQualityParts`** — covers `queryDataQualityParts`,
   `queryPartsNoAttachments`, `queryPartsMissingDefaultSupplier`,
   `queryPartsStaleRollup` via three `t.Run` subtests (they share the same
   underlying query builder, differing only in the WHERE fragment, so one
   parent test with subtests avoids three near-duplicate top-level functions):
   - `t.Run("no_attachments", ...)`: call `queryPartsNoAttachments`; assert
     `BUY-1002`, `BUY-1003`, `ASM-1001`, `ASM-1002`, `ASM-1003` are present and
     `BUY-1001` is absent.
   - `t.Run("missing_default_supplier", ...)`: call
     `queryPartsMissingDefaultSupplier`; assert exactly one row and its
     `PartNumber == "BUY-1003"`.
   - `t.Run("stale_rollup", ...)`: call `queryPartsStaleRollup`; assert
     `RAW-1002` is present and `ASM-1001`/`ASM-1002` are absent.

6. **`TestIntegration_ReportsHandlers_EndToEnd`** — covers `ReportsSpend`,
   `ReportsOnTime`, `ReportsCycleTime`, `ReportsDataQuality` as one
   table-driven test (same shape as the existing
   `TestIntegration_RouteRoundTrips`, but with real assertions instead of just
   profiling). For each: build a GET request (spend/on-time/cycle-time use
   `?range=custom` with no `from`/`to` for the same all-time-range reason as
   above; data-quality has no date range param), call the handler directly,
   assert `rec.Code == http.StatusOK` and `!strings.Contains(rec.Body.String(),
   "Error loading")` (the substring common to every `renderError` call in
   reports.go — `h.render`'s underlying template execution errors are the only
   way these would fail once the query succeeds, and this catches both).

7. **`TestIntegration_ReportsSpendBySupplierExportCSV`** — covers
   `ReportsSpendBySupplierExportCSV` (and transitively `writeSpendCSV`). GET
   with `?range=custom`, call handler, parse `rec.Body` with
   `csv.NewReader`. Assert header row `["Supplier","Total Spend"]` and that a
   row `["Acme Fasteners","282.50"]` is present.

8. **`TestIntegration_ReportsSpendByPartExportCSV`** — covers
   `ReportsSpendByPartExportCSV`. Same shape; assert header row
   `["Part Number","Title","Total Spend"]` and a row
   `["RAW-1002","Stainless Steel Bar Stock","205.00"]` present.

9. **`TestIntegration_ReportsOnTimeExportCSV`** — covers
   `ReportsOnTimeExportCSV`. Assert header row
   `["Supplier","Total Lines","On-Time Lines","On-Time %","Avg Days Late"]` and
   zero data rows (per open question #1 — documents the same limitation, not a
   new one).

10. **`TestIntegration_ReportsCycleTimeExportCSV`** — covers
    `ReportsCycleTimeExportCSV`. Assert header row
    `["Stage","PO Count","Avg Days"]` and exactly one data row
    `["draft","1","3.0"]`.

11. **`TestIntegration_ReportsDataQualityNoAttachmentsExportCSV`** — covers
    `ReportsDataQualityNoAttachmentsExportCSV` (and transitively
    `dataQualityCSVRows`). Assert header row
    `["Part Number","Title","Category"]` and a row whose first column is
    `"BUY-1002"` is present.

12. **`TestIntegration_ReportsDataQualityMissingSupplierExportCSV`** — covers
    `ReportsDataQualityMissingSupplierExportCSV`. Assert header row and exactly
    one data row, first column `"BUY-1003"`.

13. **`TestIntegration_ReportsDataQualityStaleRollupExportCSV`** — covers
    `ReportsDataQualityStaleRollupExportCSV`. Assert header row and a row
    whose first column is `"RAW-1002"` is present.

## Coverage mapping (all 19 functions named in #815)

| reports.go function | covered by |
|---|---|
| `querySpendBySupplier` | test 1 |
| `querySpendByPart` | test 2 |
| `ReportsSpend` | test 6 |
| `queryOnTimeDelivery` | test 3 (limited — see open question 1) |
| `ReportsOnTime` | test 6 |
| `ReportsOnTimeExportCSV` | test 9 (limited — see open question 1) |
| `queryPOCycleTime` | test 4 |
| `ReportsCycleTime` | test 6 |
| `ReportsCycleTimeExportCSV` | test 10 |
| `queryDataQualityParts` | test 5 (all 3 subtests) |
| `queryPartsNoAttachments` | test 5 (`no_attachments` subtest) |
| `queryPartsMissingDefaultSupplier` | test 5 (`missing_default_supplier` subtest) |
| `queryPartsStaleRollup` | test 5 (`stale_rollup` subtest) |
| `ReportsDataQuality` | test 6 |
| `dataQualityCSVRows` | tests 11–13 (transitively) |
| `ReportsDataQualityNoAttachmentsExportCSV` | test 11 |
| `ReportsDataQualityMissingSupplierExportCSV` | test 12 |
| `ReportsDataQualityStaleRollupExportCSV` | test 13 |
| `writeSpendCSV` | tests 7–13 (transitively) |
| `ReportsSpendBySupplierExportCSV` | test 7 |
| `ReportsSpendByPartExportCSV` | test 8 |

(`resolveSpendDateRange` already has coverage in `arx_go/reports_test.go`,
unchanged by this plan.)
