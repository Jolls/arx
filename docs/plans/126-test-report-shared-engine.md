# #126 Test report on shared table engine

Plan for `arx_go/`. Decision: Copy for Excel ALWAYS uses default (template) column order with the matching default header row, regardless of display order. It copies all filtered rows.

Engine facts relied on (verified):
- `allRows` holds every row client-side. `window.currentFilteredRows` (set in `renderRows`) is the full filtered and sorted set, pre-pagination. No extra fetch is needed.
- Each row keeps its raw JSON fields, so copy reads raw values (the lowercased `_text` is not used).
- `initColumnOrder` requires `data-col` on every first-header `<th>` and a `tr.filter-row`.
- `initColumnOrder` puts the Reset button in the toolbar that is `previousElementSibling` of the closest `.table-wrapper`.

## 1. `arx_go/render_records.go`
- Extract the `applyFormat` FuncMap closure body (around lines 109-137) into a package-level `func applyResultFormat(val, format string) string`.
- Make the FuncMap entry `"applyFormat": applyResultFormat`.
- Behaviour must be identical.

## 2. `arx_go/records.go`
Modify `TestReport` (around line 3295):
- Keep the form and step lookups, and the step 404 check. Keep `stepMeta` and the `step` fields, including `Format` (still shown in the header if used).
- Remove the `reportRow` type, the `resRows` query, the scan loop and the `"Rows"` template key.
- Remove `"Rows"` from the `renderRecords` map only. Everything else stays.

Add `TestReportRows` — GET `/api/forms/{id}/tests/{testID}/report/rows`:
- Parse `id` and `testID` with `chi.URLParam` + `strconv.Atoi`; 404 on error.
- Load the format with `h.queryRowContext(... SELECT COALESCE(format,'') FROM h.cfg.StepsTable() WHERE id=@p1 AND form_id=@p2 ...)`. On `sql.ErrNoRows`, 404.
- Run the existing results query, moved verbatim, via `h.queryContext`, using `cfg.ResultsTable()`, `RecordsTable()` and `dia().BoolLiteral(true)` / `TryCastInt("trec.serial_number")`, with the same ORDER BY. Scan the same columns. `record_date` can scan into `time.Time`, as it does today. Drop `is_locked`.
- Build `out := make([]row, 0)` with JSON tags:
  - `id` (int) — record id
  - `sn`
  - `snPN`
  - `pnId` (int)
  - `date` = `RecordDate.Format("2006-01-02 3:04 PM")`
  - `resultDate` = `""` if `updated_at` is nil, else `Format("2006-01-02 15:04")`
  - `result` = `applyResultFormat(res, format)`
  - `pf` = `"PASS"` / `"FAIL"` / `"—"` (from `sql.NullBool`)
  - `comment`
- Log `[rows] report form=%d test=%d: %d rows in %v`, as `RecordsRows` does. Finish with `writeJSON(w, out)`.

## 3. `arx_go/main.go`
Next to line 401, add `r.Get("/api/forms/{id}/tests/{testID}/report/rows", h.TestReportRows)`.

## 4. `arx_go/static/shared/app.js`
Only the `ROW_BUILDER_PATTERNS` constant is touched. Append one entry; no function is edited.
- `test: /\/api\/forms\/\d+\/tests\/\d+\/report\/rows$/`
- `build: r => ...` returns a `<tr>` with 7 tds in this order:
  - `col-sn`: `<a href="/records/${r.id}">${escHtml(r.sn)}</a>`
  - `col-pn`: link to `/part/${r.pnId}` if `r.pnId`, else plain text (`escHtml(r.snPN)`)
  - `col-record-date`, class `text-nowrap`: `${escHtml(r.date)}`
  - `col-result-date`, class `text-nowrap small text-muted`: `${escHtml(r.resultDate)}`
  - `col-result`, class `text-center font-mono`: `${escHtml(r.result)}`
  - `col-pf`, class `text-center`: badge `bg-success` PASS / `bg-danger` FAIL / `bg-secondary` —
  - `col-comment`: `${escHtml(r.comment)}`
- `cellText: r => [r.sn, r.snPN, r.date, r.resultDate, r.result, r.pf, r.comment]`. The P/F filter and sort text is therefore PASS/FAIL/—, as before.
- This adds a new array element next to the existing two patterns. The #20 sibling plan touches column-resize code elsewhere in the file, so there should be no textual overlap.

## 5. `arx_go/templates/records/test_report.html`
- Delete the `{{if .Rows}}...{{else}}...{{end}}` wrapper. Always render the table, because the engine shows "No results" when empty.
- Replace `<div class="table-responsive">` with `<div class="table-wrapper">`. This lets the Reset button join the header toolbar div, which already has `justify-content-between` and 2 children.
- Table: `<table class="table table-bordered table-hover table-sm" id="report-table" data-rows-url="/api/forms/{{.Form.ID}}/tests/{{.Step.ID}}/report/rows">`.
- Header row 1: `<thead><tr class="table-dark">`, dropping `class="table-dark"` from `<thead>`. Use these `<th>`s with `data-col`, keeping the existing text and `text-center` classes:
  - `col-sn` Serial Number
  - `col-pn` Part Number
  - `col-record-date` Record Date
  - `col-result-date` Result Date
  - `col-result` Result
  - `col-pf` P/F
  - `col-comment` Comment
- Row 2: `<tr class="filter-row">` with the same 7 `data-col` keys. Each `<th>` holds `<input type="text" class="form-control form-control-sm" placeholder="Filter" autocomplete="off" aria-label="Filter <name>">`, matching `records_index.html`.
- `<tbody><tr><td colspan="7" class="no-results">Loading&hellip;</td></tr></tbody>`.
- Add the pagination block copied from `records_index.html`: `.pagination-controls`, `.pagination-info`, and prev/next buttons with `onclick="prevPage()"` / `onclick="nextPage()"`.
- Add "Total matching: <span class="record-count">0</span>", also copied from `records_index.html`.
- Remove the `report-count` line, `#report-filter-row`, `applyReportFilter()`, `.report-filter-input`, and the whole `<style>` block.
- Replace the `<script>` with a copy handler. It does not call `copyTableTSV` (the shared helper in `static/table-sort.js` stays, since `part_build_cost.html` uses it):
  - `var H = ['Serial Number','Part Number','Record Date','Result Date','Result','P/F','Comment'];`
  - On `#copy-report` click, take `window.currentFilteredRows || []` (all filtered rows, sorted, all pages).
  - Build lines from `H.join('\t')` plus `[r.sn, r.snPN, r.date, r.resultDate, r.result, r.pf, r.comment].join('\t')` per row.
  - Write with `navigator.clipboard.writeText(lines.join('\n'))`.
  - Show the same "Copied!" / "Copy failed" button feedback (2s restore) as `copyTableTSV`.
  - Default order and header are used regardless of `colOrder`.
  - Wire it inside `DOMContentLoaded`.

## 6. `CHANGELOG.md`
Add a line under a new top `## [x.y.z]` entry, following the file's existing style. Do not touch `AppVersion`.

## Verification (build/vet/test only; do not run the app)
- From `arx_go`: `go build ./...`, `go vet ./...`, `go test ./...`.
- Manual checks for the user:
  - Drag a header, reload: order persists; Reset restores default.
  - Filter (including P/F = PASS/FAIL/—), sort and paginate under a non-default order: cells stay aligned with headers.
  - Copy for Excel pastes all filtered rows in the default order with the default header.
  - In the default order, result formatting and links match the old page.
- Optional regression test, only if the user agrees: a unit test for `applyResultFormat`.

## Resolved decisions (override anything above that conflicts)
1. **Record Date format:** `date` = `RecordDate.Format("2006-01-02 15:04")` (24h, same as `resultDate`), so text sort is correct.
2. **Column order scope:** fixed table id `report-table` — one saved order for all reports.
3. **Date filters:** Record Date and Result Date use the engine's date-range popover, not text inputs. In filter row 2, the `col-record-date` and `col-result-date` `<th>`s are `<th data-col="col-record-date" data-filter-type="date"></th>` (and likewise `col-result-date`) with NO `<input>`; the engine's `initDateFilters()` fills them in. The engine matches on `text.slice(0,10)`, so `cellText` for those two columns must start with `YYYY-MM-DD`; `date` and `resultDate` both do. `resultDate` is `""` when unset, which the engine treats as no date (excluded when a range is set). The other 5 filter `<th>`s keep the text `<input>` as specified above.
4. **Copy for Excel:** always default column order + default header (see top).

## Critical files
- arx_go\records.go
- arx_go\render_records.go
- arx_go\main.go
- arx_go\static\shared\app.js
- arx_go\templates\records\test_report.html
