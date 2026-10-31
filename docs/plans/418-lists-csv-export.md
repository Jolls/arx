# #418 plan: Export CSV on Lots/Units/Builds lists

## Findings
- Filtering is client-side (#411). `loadListRows()` in `arx_go/static/shared/app.js` fetches the full JSON from `table[data-rows-url]` into `allRows`; `matchesRow(row, getFilterColumns())` filters; pagination only slices for display.
- `exportCSV()` (app.js ~line 654) already exports all filtered rows (not just the page) client-side for `/api/parts/rows` and `/api/pos/rows` via a `configs` map keyed by rows URL (`filename`, `headers`, `row`). Unknown URLs return silently. Parts/POs templates call it via `onclick="exportCSV()"`.
- So: no server route, no SQL, no sqlc, no Go handler change. Add three entries to `configs` and one button per template.
- `allRows` items carry the JSON fields of `lotListRow`/`unitListRow`/`buildListRow` (`arx_go/lot.go`); dates are already `YYYY-MM-DD` in the user's zone.
- The Build column's visible value is the build id (`r.id`), so it is exported (it is a displayed column, not a hidden internal ID). `partId`, `lotId`, `outLotId` are not exported.

## Changes

### 1. `arx_go/static/shared/app.js` — `exportCSV()` `configs`
Add after the `'/api/pos/rows'` entry (header names = table `<th>` text, same order as thead / `CELL_TEXT`):

```js
'/api/lots/rows': {
    filename: 'lots.csv',
    headers: ['Lot Number','Vendor Lot','Lot Description','Part Number','Part Description','Notes','Created','Status'],
    row: r => [r.lot, r.vendorLot, r.lotDesc, r.pn, r.partDesc, r.notes, r.created, r.active ? 'Active' : 'Inactive'],
},
'/api/units/rows': {
    filename: 'units.csv',
    headers: ['Serial #','Part Number','Description','Lot','Created','Status'],
    row: r => [r.sn, r.pn, r.partDesc, r.lot, r.created, r.active ? 'Active' : 'Scrapped'],
},
'/api/builds/rows': {
    filename: 'builds.csv',
    headers: ['Build','Part Number','Description','Qty','Build Date','Output Lot','Note'],
    row: r => [r.id, r.pn, r.partDesc, r.qty, r.date, r.lot, r.note],
},
```
No other app.js change (quoting, Blob download, filter application reused as is). Empty lot/note export as empty string (the table's dash placeholder is not exported).

### 2. Templates — add button as first child of the header button row (`<div class="d-flex gap-2 ms-3">`, line 8 in each)
Insert in `arx_go/templates/parts/all_lots.html`, `all_units.html`, `all_builds.html`:
```html
<button type="button" class="btn btn-sm btn-outline-secondary" onclick="exportCSV()"><i class="bi bi-download"></i> Export CSV</button>
```
(Same markup as `parts/index.html` line 28.)

### 3. `CHANGELOG.md`
New top entry `## [0.8.90] - 2026-10-30` (bump from 0.8.89), `### Added`:
`- Export CSV button on the Lots, Units and Builds lists; exports all rows matching the current column filters ([#418](https://github.com/Jolls/arx/issues/418))`

### 4. Not changed
`arx_go/main.go`, `arx_go/lot.go`, `internal/*`, `SQL/*`, `RELEASE_NOTES.md` (per-release only), `ROADMAP.md` (#418 not listed).

## Files touched
- `arx_go/static/shared/app.js`
- `arx_go/templates/parts/all_lots.html`
- `arx_go/templates/parts/all_units.html`
- `arx_go/templates/parts/all_builds.html`
- `CHANGELOG.md`
- `arx_go/export_csv_test.go` (new, tests below)

## Test plan

### 1. Coverage audit (existing tests on touched code)
- `arx_go/filter_row_test.go`: `TestFilterTable_NewPagesHaveFilterRow` (renders all_lots/units/builds, asserts filter row + `data-rows-url`), `TestRowBuildersCoverListEndpoints` (app.js has ROW_BUILDERS + CELL_TEXT entries per URL; counts `'/api/x/rows'` occurrences with `>= 2`, so new `configs` keys don't break it), `TestLotListRows_UserZone`, `TestUnitListRows`, `TestBuildListRows` (JSON row shapes the CSV reads).
- `arx_go/filter_terms_test.go`: `TestFilterTermsJS_Characterization`, `TestFilterTermsJS_Terms` (node-driven `matchesRow`; provides the `jsFunc` slicer and node-skip pattern reused here).
- `arx_go/integration_test.go` ~4546-4580: lots/units/builds rows endpoint tests (names around `AllLotsRows`/`AllUnitsRows`/`AllBuildsRows`).
- `arx_go/templates_parse_test.go`: `renderPartsList` helper; template parse tests.
- No existing test covers `exportCSV()` itself (Parts/POs export untested in JS).

### 2. Characterization tests (pass on unchanged code)
In new `arx_go/export_csv_test.go`, using node (skip if not on PATH) and the same `jsFunc` + `staticFS` approach as `filter_terms_test.go`; a shared helper `runExportCSV(t, rowsURL string, rows []map[string]any, filters []string) (filename, csv string)` slices `compileGlob, termMatcher, parseFilterTerms, makeTextFilter, matchesRow, exportCSV` from app.js and runs them with stubs: `allRows` (each row gets `_text` as lowercased `CELL_TEXT`-order strings supplied by the test), `document.querySelector` -> `{dataset:{rowsUrl}}`, `document.getElementById` -> `null`, `getFilterColumns` -> `filters.map(makeTextFilter)`, `Blob` capturing content, `URL.createObjectURL/revokeObjectURL`, `document.createElement('a')` capturing `download`/`click`.
- `TestExportCSV_PartsCharacterization`: `/api/parts/rows`, 2 rows, one filtered out by a filter on the Part Number slot -> filename `parts.csv`, header line `Part Number,Revision,Description,Detail,Requested By,Date,Category,Modified,Attachments,PO Lines,Active`, only matching row exported, CRLF line endings, a value containing `,` and `"` quoted/escaped. Passes today.
- `TestExportCSV_UnknownURLNoop`: unknown rows URL -> no download triggered. Passes today.

### 3. Red tests (fail today)
Same file; stubs compile and run today.
- `TestExportCSV_Lots`: 3 lot rows (one with comma in notes, one inactive); no filters -> filename `lots.csv`, header `Lot Number,Vendor Lot,Lot Description,Part Number,Part Description,Notes,Created,Status`, 3 data rows, `Inactive` for the inactive one, no id/partId values. Fails: `configs['/api/lots/rows']` missing so no download (filename empty).
- `TestExportCSV_LotsFiltered`: 3 rows, text filter on Part Number slot (index 3) matching 2, plus a second case with a `!`-exclude filter and a Status-slot `inactive` filter -> exactly the matching rows, in `allRows` order. Fails: same reason.
- `TestExportCSV_LotsAllPages`: 60 rows (> `ROWS_PER_PAGE`), no filters -> 60 data rows. Fails: same reason.
- `TestExportCSV_Units`: rows incl. one with `lotId` 0/`lot` "" and one scrapped -> `units.csv`, header `Serial #,Part Number,Description,Lot,Created,Status`, empty Lot cell for no-lot row, `Scrapped` status. Fails: config missing.
- `TestExportCSV_UnitsFiltered`: Status filter `scrapped` returns only scrapped units. Fails: config missing.
- `TestExportCSV_Builds`: rows incl. `outLotId` 0 -> `builds.csv`, header `Build,Part Number,Description,Qty,Build Date,Output Lot,Note`, first cell is build id, empty Output Lot for no-lot build. Fails: config missing.
- `TestExportCSV_BuildsFiltered`: date-range text filter on Build Date slot (text filter, e.g. `2026-05`) returns matching rows only. Fails: config missing.
- `TestListPagesHaveExportButton`: renders `all_lots.html`, `all_units.html`, `all_builds.html` via `renderPartsList`; each contains `onclick="exportCSV()"` and `Export CSV`, and the button appears before the cross-list link (`href="/units"` / `href="/lots"`) in the output. Fails: no button.
- `TestExportCSV_HeadersMatchTable`: for each of the three pages, extract first-row `<th>` text (strip tags, `{{filter_help_btn}}` output, whitespace) in order and assert it equals the `headers` array in the corresponding `configs` entry (parsed from app.js via the node harness output of the header line). Fails: config missing.

### 4. Manual-only
- On /lots, /units, /builds: click Export CSV with no filters -> file `lots.csv`/`units.csv`/`builds.csv` opens in Excel with all rows.
- Apply a text filter, a date range, and a Status select, with data spanning multiple pages -> CSV contains all matching rows across pages, none of the others.
- Reload via copied URL with `f.` params -> export reflects restored filters.
- Click a header to sort, then export -> rows are in the sorted order.
- Button visual placement beside All Units/All Builds (etc.) links at narrow width (wraps cleanly).
- Existing Parts and POs Export CSV still download.

### Seed data
None needed. Existing seed already has lots, units 8501/8503 and builds 8201-8203 for the integration tests; the CSV tests use inline row fixtures.

## Open questions
1. Column order/visibility: CSV always uses default thead order even if the user reordered columns (`colOrder`); the Lots/Units/Builds pages have no column-hide control. Acceptable?
2. Status wording in CSV: `Active`/`Inactive` (lots), `Active`/`Scrapped` (units), matching the badge text, versus Parts' `true`/`false`. Confirm badge text.
3. Include the Build id in the CSV (it is the visible first column but also the internal id)? Plan includes it because the table shows it and the header must match the table.
4. Is node guaranteed in CI? New JS tests skip when node is absent (same as `filter_terms_test.go`); if node is absent the red tests would silently skip.

## Resolved decisions (user, 2026-10-30)
- Column order: default order is fine; no column-hide on these pages.
- CSV Status text: Active/Inactive (Lots), Active/Scrapped (Units), matching the table badges.
- Builds first column exported with header "Build" (matches table).
- JS tests via node skip when node is missing, like filter_terms_test.go.
