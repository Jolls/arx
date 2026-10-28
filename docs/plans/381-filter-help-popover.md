# #381 Help popover for filter / search wildcards

Prereq: #382 already implemented on the branch (`;` AND, `!` exclude). Do not change filter/search behavior.

## Findings (current code)
- Bootstrap 5.3.3 JS bundle loaded in `arx_go/templates/shared/layout.html:54`. Tooltips init in the inline `DOMContentLoaded` block (`layout.html:58-62`, `[data-bs-toggle="tooltip"]`). No popover init exists. Bootstrap Icons loaded (`bi bi-*`).
- Filter rows are server-rendered `<tr class="filter-row">` in templates (not JS). JS only reads them (`filterThs()`, `compileGlob` at `static/shared/app.js:347`) and injects date-range controls into `th[data-filter-type="date"]` (`app.js:387`). Column visibility toggles hide `th` by `data-col`.
- Templates: core pages parse `layout.html + partials.html + page` (`templates.go:60-65`); records pages parse `layout.html + page` only (`templates.go:80`) and records funcs lack core funcs, so `partials.html` cannot be added to records. Only `layout.html` is common to both, so shared snippets must live in `layout.html` (it currently only has `{{define "layout"}}`).
- Template tests: source-lint style via `readTemplate` (`layout_theme_test.go`), and render style via `parseTemplates(templatesFS)` + `renderToString` (`templates_parse_test.go:123`, e.g. `TestAllLotsTemplateRenders_*`).

## Surfaces
Filter rows (`<tr class="filter-row">`):
1. `arx_go/templates/parts/index.html:52`
2. `arx_go/templates/pos/pos.html:30`
3. `arx_go/templates/suppliers/suppliers.html:26`
4. `arx_go/templates/contacts/contacts.html:30`
5. `arx_go/templates/records/index.html:21`
6. `arx_go/templates/records/records_index.html:74` (first filter `th` is empty `col-select`)
7. `arx_go/templates/records/test_report.html:33`
8. `arx_go/templates/parts/part_records.html:21`
9. `arx_go/templates/parts/part_lot_trace.html:129`
10. `arx_go/templates/parts/part_unit_trace.html:122`

Search boxes (backed by `/api/parts/search`, `/api/suppliers/search`, both `db.EscapeLike`):
- Part: `templates/parts/part_bom_edit.html` headers `Part Number`/`Description` (lines 13-14) and the same two `th` for new rows share that header (rows at 24-30 and `<template id="new-row-tpl">` 91-99 use `bom-pn-input`/`bom-desc-input`); `templates/pos/po_edit.html` headers `Part Number`/`Description` (lines 275, 277; inputs wired at 377-378, 634-636).
- Supplier: `templates/pos/po_edit.html:77` (supplier) and `:138` (receiver); `templates/parts/part_sourcing.html:160` and `:236`; `templates/parts/part_pricing.html:96`; `templates/parts/part_pricing_form.html:20`; `templates/contacts/contact_edit.html:31`; `templates/settings/settings.html:702` (default receiver).

## Changes

### 1. `arx_go/templates/shared/layout.html` (append after `{{end}}` of "layout"; the single source of popover text)
- `{{define "filter_help_content"}}` — HTML fragment (only tags in Bootstrap's popover allowlist: `ul li code strong p`). Items, each with a short example:
  - plain text = case-insensitive substring (`abc`)
  - `*` = any run of characters (`65*-013`)
  - `?` = any single character (`AB?-01`)
  - with `*`/`?` the match is whole-field (`65*-013*-*`)
  - multiple terms separated by `;` = AND, spaces around `;` ignored (`ABC*; *-01`)
  - leading `!` excludes (`ABC*; *-01; !*-OBS`)
  - a leading `!` and a literal `;` cannot be searched
  - date columns: pick from/to range
  - select columns: pick a value
- `{{define "search_help_content"}}` — `*` = any run of characters (`motor*12`); always substring; `?`, `%`, `_` match themselves.
- `{{define "filter_help_btn"}}` and `{{define "search_help_btn"}}` — each renders:
  `<button type="button" class="btn btn-link p-0 text-body-secondary" data-bs-toggle="popover" data-bs-html="true" data-bs-trigger="focus" data-bs-container="body" data-bs-placement="bottom" data-bs-title="Filter help|Search help" data-bs-content="{{template "..._content"}}" aria-label="Filter help|Search help"><i class="bi bi-info-circle"></i></button>`
  (`html/template` attribute-escapes the nested content; `container=body` avoids clipping by `.table-wrapper` overflow.)
- In the existing `DOMContentLoaded` script (`layout.html:58-62`) add: `document.querySelectorAll('[data-bs-toggle="popover"]').forEach(el => new bootstrap.Popover(el));`

### 2. Filter-row templates (the 10 files above)
Insert `{{template "filter_help_btn"}}` inside the filter-row `th` per placement decision in Open question 1 (wrap in `<div class="d-flex align-items-center gap-1">` with the existing input). No other markup change.

### 3. Search-box templates
- Part: add `{{template "search_help_btn"}}` inside the `Part Number` `th` in `part_bom_edit.html:13` area and `po_edit.html:275` (one per table, not per row).
- Supplier: add `{{template "search_help_btn"}}` next to each of the 8 supplier inputs listed (sibling after the `position:relative; flex:1` wrapper div inside `detail-row`; for the supplier inputs in `po_edit.html` this includes receiver).

### 4. No CSS/JS file changes
Bootstrap utilities only; popover init is in layout (above). `app.js`/`compileGlob`/`db.EscapeLike` untouched.

### 5. Docs
- `CHANGELOG.md`: add under the branch's current top version `### Added`: "Help popover on list-table filters and part/supplier search boxes describing wildcard syntax ([#381](https://github.com/Jolls/arx/issues/381))".
- `arx_go/RELEASE_NOTES.md`: only if the release entry is being written (user-facing, one terse line); otherwise skip.

## Test plan

### 1) Coverage audit
- Existing: `TestParseTemplates`, `TestCoreTemplatesParse`, `TestRecordsTemplatesParse` (parse only; catch a missing `{{template "filter_help_btn"}}` define but not absence of icon on a surface); `compileGlob` behavior has no JS tests (no JS test runner in repo or CI); `internal/db/likeescape_test.go` covers `EscapeLike`. Nothing asserts any filter/search help text or icon today.

### 2) Characterization tests
- None needed: behavior is unchanged and `EscapeLike` is already covered; the existing parse tests keep guarding that every page still parses with the new defines.

### 3) Red tests (new file `arx_go/filter_help_test.go`; fail on current code, pass after)
- `TestFilterHelp_EveryFilterRowHasHelpButton`: walk `templatesFS` `templates/**/*.html`; for each file containing `class="filter-row"`, assert it contains `{{template "filter_help_btn"}}` (use `readTemplate`). Also assert the count of such files equals 10 so a new filter-row page without the icon fails.
- `TestFilterHelp_SearchBoxFilesHaveHelpButton`: assert `{{template "search_help_btn"}}` appears in `part_bom_edit.html`, `po_edit.html` (>=3: part header, supplier, receiver), `part_sourcing.html` (>=2), `part_pricing.html`, `part_pricing_form.html`, `contact_edit.html`, `settings.html`.
- `TestFilterHelp_RendersSharedContent`: using `parseTemplates(templatesFS)`, execute `filter_help_btn` and `search_help_btn` from both a core key (`layout:suppliers/suppliers.html`) and a records key (`records:index.html`) via `renderToString`; assert output contains `data-bs-toggle="popover"`; assert filter content (after HTML unescape) contains substring/`*`/`?`/whole-field/`;`/`!`/date/select phrases, and search content contains `*` and states `?` is literal; assert both keys render identical output (single source).
- `TestFilterHelp_PopoverInitInLayout`: `readTemplate("templates/shared/layout.html")` contains `bootstrap.Popover`.
- `TestFilterHelp_TextDefinedOnce`: assert a distinctive phrase from the content (e.g. "any single character") appears in no template file other than `layout.html`.

### 4) Manual-only
- Click the (i) on each of the 10 tables: popover opens below, is not clipped by the table wrapper, closes on outside click, readable in light and dark theme.
- Confirm popover text matches actual behavior by trying each example in a filter (`65*-013*-*`, `?`, `ABC*; *-01; !*-OBS`) and in the part/supplier search boxes (`*` works, `?`/`%`/`_` literal).
- Check icon placement on narrow widths and with the first column hidden via the Columns menu (parts, records).
- Check BOM edit new-row (cloned `<template>`) and PO edit line headers show the icon once.

## Open questions
1. Filter-row icon placement: per-table first filter `th` (hidden when that column is toggled off; `records_index.html` first `th` is the empty `col-select` cell) vs. a single spot outside the table (toolbar above each table, not strictly "in the filter row"). Which? Default if unanswered: first filter `th` that is always visible; for `records_index.html` the `col-select` `th`.
2. Part search icon for line tables: one icon in the column header (plan default) vs. per row next to each input?
3. Include the Settings "Default Receiver" typeahead (`settings.html:702`) and PO Receiver (`po_edit.html:138`) as supplier search surfaces? Plan includes them since they hit `/api/suppliers/search`; issue lists only "part and supplier search boxes".
4. Fixed `data-bs-trigger="focus"` (click to open, click away to close) acceptable vs. hover/click?

## Resolved decisions
- Filter-row icon goes in the first always-visible filter `th` (the `col-select` `th` on records_index.html).
- Part-line tables (part_bom_edit.html, po_edit.html): one icon in the Part Number column header.
- PO Receiver (po_edit.html) and Settings Default Receiver inputs count as supplier search boxes.
- `data-bs-trigger="focus"` is accepted.
