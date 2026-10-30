# #411 plan: column filters for Lots/Builds/Units on a shared partial

## Findings
- Filtering is client-side. `loadListRows()` in `static/shared/app.js` fetches `table[data-rows-url]` JSON, then uses `ROW_BUILDERS` / `CELL_TEXT` / `ROW_BUILDER_PATTERNS`. Filters, date popover, sort, pagination, `f.{key}` URL params, sessionStorage all live in that JS, keyed by `data-col`.
- "Behave like Parts" = convert the 5 pages to the `data-rows-url` flow. New rows endpoints reuse existing inventory services. No SQL / sqlc / `internal/dbq` change.
- Services exist: `h.inventory().ListAllLots/ListAllUnits/ListAllBuilds`, `h.lotsForPart`, `h.unitsForPart`, `LotsWithSources`.
- One `data-rows-url` table per page; the 5 target pages have one list table each.
- Filter-row markup is copy-pasted in 11 templates. `suppliers/suppliers.html` is the cleanest reference.
- `coreTemplateFuncs()` in `handlers.go` already has `dict`.

## 1. Shared partial
- Add `{{define "filter_table"}}` to `templates/shared/partials.html`. Takes dict: `ID` (opt), `RowsURL`, `Class` (opt), `Cols`.
- `Cols` = `[]FilterCol` defined in new `arx_go/filter_cols.go`: `Key, Label, Tip, Class, Style, Filter, Aria, Options, ListID, Datalist, HelpBtn`.
- `Filter` ∈ `text|date|select|datalist|none`. `Options` = `[]{Value, Label, Selected}` for selects.
- Partial emits: colgroup (one `<col>` per column); thead row 1 (`th data-col`, tooltip span if `Tip`, `filter_help_btn` if `HelpBtn`); thead row 2 `tr.filter-row` (`th data-col`, `data-filter-type="date"` for date, else input/select/datalist); tbody Loading row with `colspan=len Cols`; pagination `<nav>` + "Total matching" `<p>` (markup per `docs/plans/399-bootstrap-pagination.md`).
- Input markup = Parts one; `Aria` selects the `form-control form-control-sm ... aria-label` variant used by some tables.
- Page-specific pieces stay in each template (wrapper `div.table-wrapper`, toolbars, extra buttons).
- Convert the 11 existing filter-row templates: `suppliers/suppliers`, `records/index`, `records/records_index`, `records/test_report`, `pos/rfqs`, `pos/pos`, `contacts/contacts`, `parts/index`, `parts/part_unit_trace`, `parts/part_lot_trace`, `parts/part_records`. `data-col` keys, colspans, ids unchanged in effect.
- `records_index` keeps `select-all`, `status-filter` (WIP default, "All" last), `table-responsive-sm`. `parts/index` keeps category select from `.Categories`.
- `filter_help_btn` call becomes `HelpBtn` flag on one column per table.

## 2. Rows endpoints
In `lot.go` / `unit.go` next to page handlers; call service, build JSON row struct, `writeJSON`. Dates pre-formatted `YYYY-MM-DD`, created dates via `localTime h.userLocation`. Register in `main.go` with `withAuth`.

| Route | Handler | Source | Fields |
|---|---|---|---|
| GET /api/lots/rows | AllLotsRows | ListAllLots | id, partId, lot, vendorLot, lotDesc, pn, partDesc, notes, created, active |
| GET /api/units/rows | AllUnitsRows | ListAllUnits | id, partId, sn, pn, partDesc, lotId, lot, created, active |
| GET /api/builds/rows | AllBuildsRows | ListAllBuilds | id, partId, pn, partDesc, qty, date, outLotId, lot, note |
| GET /api/part/{id}/lots/rows | PartLotsRows | lotsForPart + LotsWithSources, same ownership check as PartLots | all-lots fields + hasSources |
| GET /api/part/{id}/units/rows | PartUnitsRows | unitsForPart, same ownership check | all-units fields |

## 3. Page handlers/templates
- `AllLots`, `AllUnits`, `AllBuilds`, `PartLots`, `PartUnits` stop passing row slices; keep other data.
- Rewrite `templates/parts/{all_lots,all_builds,all_units,part_lots,part_units}.html`: keep intro text, cross-links, Expand/Collapse buttons on part_lots; table via `filter_table`; drop `{{if .Lots}}`/"No lots yet." and server-side footer note.
- `part_lots` keeps `id="lots-table"` and `data-part-id`.
- Columns: Lots `col-lot, col-vendorlot, col-lotdesc, col-pn, col-partdesc, col-notes, col-created(date), col-status(select All/Active/Inactive)`; Units `col-sn, col-pn, col-partdesc, col-lot, col-created(date), col-status(All/Active/Scrapped)`; Builds `col-build, col-pn, col-partdesc, col-qty, col-date(date), col-lot, col-note`. Per-part variants omit part columns.
- Remove `sortable`/`sortTable` onclick headers on these pages.

## 4. static/shared/app.js
- Add `ROW_BUILDER_PATTERNS` for `/api/lots/rows`, `/api/units/rows`, `/api/builds/rows`, `/api/part/\d+/lots/rows`, `/api/part/\d+/units/rows`.
- Builders reproduce current cell markup (popout links, badges). Notes cells: `text-truncate d-inline-block`, `max-width:280px`, `title`, mirroring `note_cell`.
- Part-lots builder emits `<tr data-path="${id}">` and `bom-expand-toggle` only when `hasSources`, so `toggleLotRow`/`expandAllLots` keep working.
- `cellText` order matches thead. Status text `active`/`inactive` (units `active`/`scrapped`).

## 5. static/app.css
No change expected.

## 6. Audit (record in PR)
- Filtered: all_lots, all_builds, all_units, part_lots, part_units; 11 existing tables moved onto the partial.
- Candidates part_transactions, part_orders, supplier_parts, supplier_pos, mfg_parts, other non-list tables: see Resolved decisions.

## Test plan
### Coverage audit
- `filter_help_test.go`: `TestFilterHelp_EveryFilterRowHasHelpButton` hard-codes the 11 templates; rewrite to assert every `filter_table` caller has exactly one `HelpBtn` column and the partial emits `class="filter-row"`. `TestFilterHelp_RendersSharedContent` unchanged.
- `templates_parse_test.go`: `TestAllLotsTemplateRenders_UTCLocation`/`_UserZone` assert server-rendered `<td>2026-01-02</td>` → move to rows-endpoint tests. `TestAllUnitsTemplateRenders`/`TestAllBuildsTemplateRenders` → replace with shell render check + endpoint JSON checks. `TestParseTemplates`/`TestCoreTemplatesParse` cover partial parsing.
- `integration_test.go`: `TestIntegration_PartUnitsHandler` (~line 3724, parts 3013/3004) → assert shell; add rows assertions in new endpoint test.
- `inventory_integration_test.go` untouched. Run `parts_list_integration_test.go`, `po_reads_integration_test.go`, `suppliers_integration_test.go` unchanged.

### Characterization (pass on unchanged code)
- New `arx_go/filter_table_char_test.go`, `TestFilterTable_ExistingTablesStructure`: render each of the 11 templates under layout; extract ordered thead `data-col` keys, filter-row keys, date-filter columns, select option values, `data-rows-url`, `id`, Loading-row colspan, pagination `<nav>` and `record-count` presence. Compare to committed golden map; re-run after refactor.
- Also pins: records_index status select defaults to WIP; parts/index category options.

### Red tests (fail today)
- `TestFilterTable_NewPagesHaveFilterRow`: 5 pages contain `tr.filter-row` + `data-rows-url`. Fails: no filter row.
- `TestAllLotsRows_JSON` (integration): seeded lots, `created` in user zone incl. 03:00 UTC → prior-day LA. Fails: 404.
- `TestAllUnitsRows_JSON`, `TestAllBuildsRows_JSON`: field mapping incl. nullable `lotId`/`outLotId`.
- `TestPartLotsRows_JSON`: `hasSources`, ownership/404 for non-lot-controlled part. `TestPartUnitsRows_JSON` (parts 3013, 3004).
- `TestFilterTable_PartialRendersAllFilterKinds`: text, date, select, datalist, none.
- `TestFilterTable_NoDuplicatedFilterRowMarkup`: `class="filter-row"` only in `partials.html`.
- Go string check that the 5 URL patterns exist in `app.js`.

### Manual-only
- Each of 5 pages: type in each filter, date popover, status select, header sort, paginate, copy URL into new tab → same view (`f.{key}`, `df.`/`dt.`).
- Reorder/resize columns.
- Per-part Lots: expand/collapse row with sources, Expand All/Collapse All.
- Regression sweep of the 11 converted pages; Parts: category select, date popovers, CSV export; hover popouts.

## Resolved decisions
- Migration breadth: convert all 11 existing filter-row tables onto the partial.
- Empty state: engine no-results row + "Total matching"; Expand All on part lots is per page. Both accepted.
- Partial API: Go `FilterCol` struct in `arx_go/filter_cols.go`.
- Detail tabs get no filters (user decision). This OVERRIDES sections 2, 3, 4 and the test plan wherever they mention per-part lots/units:
  - Drop `part_lots.html`, `part_units.html`, `PartLotsRows`, `PartUnitsRows`, the `/api/part/{id}/{lots,units}/rows` routes, their `app.js` patterns, `TestPartLotsRows_JSON`, `TestPartUnitsRows_JSON`, and the part_lots Expand/Collapse concern. Per-part Lots/Units tabs stay untouched (server-rendered, unfiltered); `PartLots`/`PartUnits` handlers and `TestIntegration_PartUnitsHandler` unchanged.
  - Only `all_lots`, `all_builds`, `all_units` are converted (3 rows endpoints: `/api/lots/rows`, `/api/units/rows`, `/api/builds/rows`).
  - Audit: part_transactions, part_orders, mfg_parts, supplier_parts, supplier_pos, part_lots, part_units are intentionally excluded as detail tabs. Record this in the PR.
- Helper scope: `filter_table` is for list pages only; detail tabs keep current markup, no filters-off option.
- Existing filtered detail pages `part_lot_trace`, `part_unit_trace`, `part_records` are still converted onto the partial (all 11 existing tables).
