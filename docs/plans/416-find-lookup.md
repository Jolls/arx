# #416 plan: find lot / unit / build by id, lot number or serial

Branch: `feature/416-find-lookup`. Paths below are repo-relative. Items marked (Q#) depend on an answer in **Open questions**.

## Facts the change relies on (verified in current code)
- Detail URLs: lot `/part/{partID}/lots/{lotID}` (`PartLotTrace`, `arx_go/lot.go:154`, rejects a lot whose part != `{id}`); unit `/part/{partID}/units/{unitID}` (`PartUnitTrace`, `main.go:329`). **There is no build detail page.** The existing build link (`arx_go/static/shared/app.js:232`) is `/part/{partID}/lots/{outLotId}` when `build.output_lot_id` is set, else `/part/{partID}/build`.
- lot, unit, build ids come from one sequence `trace_id_seq` (#420): an id names exactly one record across the three tables. `unit.serial_number` is unique per `(part_id, serial_number)` only (`SQL/unit.sql`); `lot.lot_number` is free text, defaults to the lot's own id (seed: `lot_number = id::text`, so one lot matches both "id" and "lot number" for the same q).
- Route registration: `arx_go/main.go` `buildRouter`, e.g. `b.handle(http.MethodGet, "/lots", withAuth, h.AllLots)` (line 271). `withAuth = chain(h.RequireAuth)`.
- Inventory domain: `internal/inventory/inventory.sql` (+ `inventory.go`, `inventory.sql` already in `sqlc.yaml` queries, line 47). Handler accessor `h.inventory()` (`arx_go/inventory.go:27`).
- Main nav: `arx_go/templates/shared/layout.html` line 29-32 (`<nav class="tabs">`, ranges over `MainTabs` from `arx_go/section_tabs.go`). `h.render` (`handlers.go:741`) injects `CurrentUser`, `applySectionBar`. List pages `/lots` `/units` `/builds` render `parts/all_*.html` with `"ActiveTab": "records"`.
- CSS: `app.css:109-115`: `.tabs{display:flex}`, `.tab{flex:1}`, `.tab:last-child{border-right:none}`.
- `TestBuildRouter_AllAppRoutesRequireAuth` (`middleware_test.go:461`) iterates every registered route and asserts it is auth-gated, so `/find` registered via `withAuth` is covered with no new test.
- `arx_go/sqlc_converted_lint_test.go` forbids raw SQL in `arx_go` non-test files: all SQL goes in `inventory.sql`.

## File changes

### 1. `internal/inventory/inventory.sql` (append after `ListBuildUnits`)
Add query `FindTraceRecords :many`: three `SELECT`s `UNION ALL`ed, one per table, each joined to `part p`. Params: `sqlc.arg(q)::text` and `sqlc.narg(id)::int` (NULL when q is not a usable id). Columns (same order in all three):
`kind text` ('lot'|'unit'|'build'), `id`, `part_id`, `part_number` (`COALESCE(p.part_number,'')`), `part_description` (`COALESCE(p.description,'')`), `output_lot_id int` (`b.output_lot_id` for build; `NULL::int` for lot/unit), `matched_id bool` (`COALESCE(x.id = sqlc.narg(id)::int, FALSE)`), `matched_name bool` (lot: `l.lot_number = sqlc.arg(q)::text`; unit: `u.serial_number = sqlc.arg(q)::text`; build: `FALSE`).
- lot: `FROM lot l JOIN part p ...  WHERE l.id = narg(id) OR l.lot_number = arg(q)`
- unit: same with `u.id` / `u.serial_number`
- build: `WHERE b.id = narg(id)`
- `ORDER BY` kind order (lot, unit, build) then id, so output is deterministic. One row per record even when it matches on both id and name (the flags carry both).
- Matching is exact (`=`), no `LIKE`/lower(). Includes inactive lots/units (no `is_active` filter) (Q3, Q4).
- Run `sqlc generate` (repo root); commit `internal/dbq`. Check generated types: `output_lot_id` should be `*int`, flags `bool`, `kind` `string`; if sqlc infers the `NULL::int` column or the UNION differently, adjust the cast, not the generated file.

### 2. `internal/inventory/inventory.go`
Add (near the "All units/builds" service funcs):
- `type FindResult struct { Kind string; ID, PartID int; PartNumber, PartDescription string; OutputLotID int; MatchedID, MatchedName bool }` (`OutputLotID` 0 when none, like `BuildListRow.OutputLotIDVal`).
- `func (s *Service) FindRecords(ctx context.Context, q string) ([]FindResult, error)`: parse `id` with `strconv.Atoi(q)`; pass non-nil `*int` only if it parsed and is `> 0` and `<= math.MaxInt32` (ids are int4), else nil. Call `s.q.FindTraceRecords`, map rows to `FindResult`. Does not trim; caller trims (Q2).

### 3. `arx_go/find.go` (new file, `package main`)
- `func (h *Handler) Find(w, r)`: `q := strings.TrimSpace(r.URL.Query().Get("q"))` (Q2). If `q == ""` render the page with no result and no message (Q5). Else `res, err := h.inventory().FindRecords(r.Context(), q)`; on error `serverError`-style/`h.renderError(w, r, "Error searching: "+err.Error())` (match `lot.go` style).
  - `len(res) == 1`: `http.Redirect(w, r, findURL(res[0]), http.StatusFound)` (same code `lot.go:279` uses).
  - `len(res) > 1`: render `parts/find.html` with `"Results": []findRow`.
  - `len(res) == 0`: render `parts/find.html` with `"Q": q`, `"NotFound": true` (Q6: stays on `/find`).
  - Render data always includes `"ActiveTab": "records"`, `"TestMode": h.cfg().TestMode`, `"Q": q` (like `AllLots`, `lot.go:297`).
- `func findURL(r inventory.FindResult) string` (pure): lot -> `/part/%d/lots/%d`; unit -> `/part/%d/units/%d`; build -> `/part/%d/lots/%d` using `OutputLotID` when non-zero, else `/part/%d/build` (Q1).
- `func matchedLabel(r inventory.FindResult) string` (pure): lot: "ID", "Lot number", or "ID, Lot number"; unit: "ID", "Serial number", or "ID, Serial number"; build: "ID".
- `type findRow struct { Kind, Label, PartNumber, PartDescription, Matched, URL string }` where `Label` is `Lot 8301` / `Unit 8505` / `Build 8201` (Kind capitalized + id). Built from `FindResult` with `findURL`/`matchedLabel`.

### 4. `arx_go/main.go`
After the `/builds` line (271-273 block) add: `b.handle(http.MethodGet, "/find", withAuth, h.Find)`. Any logged-in user (same `withAuth` as the list pages).

### 5. `arx_go/templates/parts/find.html` (new)
`{{define "content"}}` page (auto-picked up by `parseTemplates`' `templates/parts/*.html` glob, `templates.go:41`). Bootstrap only:
- heading text "Find lot, unit or build".
- if `.NotFound`: `<div class="alert alert-warning" role="alert">No record found for <strong>{{.Q}}</strong></div>` (html/template escapes `.Q`).
- if `.Results`: `<table class="table table-bordered table-hover table-sm">` with header `<tr class="table-dark">`: Type, Record, Part Number, Description, Matched. Each row links the Record cell to `{{.URL}}`; Type cell shows `Lot|Unit|Build`.
- No JS file needed.

### 6. `arx_go/templates/shared/layout.html`
Inside `<nav class="tabs">` (line 29-32), after `{{end}}` of the `range .MainTabs`, add (only when `.CurrentUser`, same gate as the Sign out form on line 19):
`<form method="get" action="/find" role="search" class="nav-find d-flex align-items-center px-2"><input type="search" name="q" class="form-control form-control-sm" placeholder="Find lot / unit / build" aria-label="Find lot, unit or build" autocomplete="off"></form>`
GET form: no CSRF token. Enter submits natively, so a scanner that types the value + Enter works with no JS. (Q7 placement.)

### 7. `arx_go/static/app.css`
- Line 115: `.tab:last-child { border-right: none; }` -> `.tab:last-of-type { border-right: none; }` (the form is now the last child of `nav.tabs`; `a.tab:last-of-type` keeps the last tab borderless; section-tab navs contain only `<a>`, unchanged).
- Add after it: `.nav-find { width: 14rem; }`. (Bootstrap has no width class that fits; one exact value.)

### 8. `CHANGELOG.md`
Per CLAUDE.md, one entry for the PR under the next `## [0.8.x]` (top is `0.8.91`; use a new patch number unless 0.8.91 is still unreleased), `### Added`: "Find box in the main nav and `/find?q=` look up a lot, unit or build by id, lot number or serial ([#416](https://github.com/Jolls/arx/issues/416))". No `RELEASE_NOTES.md` change (not a release).

### 9. Seed (for the ambiguous-value integration test) - needs human reseed
`SQL/seed_test_data.sql`, in the `INSERT INTO unit` block (line ~559) add one row:
`(8505, 3012, NULL, NULL, '8301', '2026-07-21T00:00:00', TRUE, 'manual')`
- id 8505 is inside the existing `trace_id_seq` range (setval 8599, line 796): no `setval` change. Part 3005 and 3013 and 3007 rows are untouched, so `TestIntegration_PartTraceabilityCard`, `recentPartUnits(3005)` etc. keep their counts; part 3012 has no unit-count assertion (verified by grep). Part 3005 / ASM-1001 / Skyrunner sentinel is not touched.
- Update the comment above the block (lines ~549-558) with one line describing 8505 (#416: numeric serial equal to lot 8301's id/lot_number, for the /find ambiguity test).
- `SQL/SCHEMA.md` has no per-row seed list (no change expected; grep `8504` there to confirm).
- **Human step: reseed ArxDev** (or `docker compose -f SQL/compose.yml restart db` for the throwaway container, which reseeds). Do not reseed ArxDev from Claude.

### 10. Tests
See Test plan.

## Test plan

### 1. Coverage audit (existing tests touching changed code)
- Nav / layout: `TestContactDetailTemplateRenders` (`templates_parse_test.go:158`, asserts `--main-tabs:5`, `data-section="suppliers"`, `class="tab active"`, `>Contacts</a>`); `TestYieldTemplateRenders` (`:189`); `TestApplySectionBar_MainTabs`, `TestApplySectionBar_OtherSectionsArePopdowns`, `TestSectionBars_MainTabsKnown` (`section_tabs_test.go`).
- Template parsing: `TestCoreTemplatesParse` (`templates_parse_test.go:51`) parses every `templates/<coreTabDir>/*.html`, so the new `parts/find.html` is parse-checked automatically; `TestSharedStandaloneTemplatesParse` parses `layout.html`.
- Routing/auth: `TestBuildRouter_AllAppRoutesRequireAuth` (`middleware_test.go:461`) will cover `/find` once registered.
- Lint: `sqlc_converted_lint_test.go` (no raw SQL in `arx_go`).
- Sibling pages / destinations: `TestAllListPagesLinkEachOther`, `TestUnitAndBuildCellTriggers` (`popout_unit_build_test.go:144`, pins the build link `app.js:232`), `TestIntegration_AllLots`, `TestIntegration_AllUnitsAndBuilds` (`integration_test.go:4551`, `:4574`), `TestIntegration_PartLotTrace` (`:4420`), `TestIntegration_PartUnitTraceHandler` (`:3756`), `TestIntegration_TraceIDsSharedAcrossLotUnitBuild` (`inventory_integration_test.go:605`).
- Units counted in seed-dependent tests that the new seed row must not break: `TestIntegration_PartTraceabilityCard` (`part_traceability_integration_test.go:41`), `recentPartUnits(3005,5)` (`inventory_integration_test.go:273`), `TestIntegration_AllUnitsAndBuilds`.
- No existing test covers a layout render with a non-nil `CurrentUser` (the nav `CurrentUser` branch).

### 2. Characterization tests (pass on unchanged code, must still pass after)
Add to `arx_go/templates_parse_test.go` (not integration):
- `TestLayoutNav_LastTabHasNoRightBorder`: read `static/app.css` from the embedded FS used elsewhere in the package tests (follow how `TestPopoutJS_*` at `popout_unit_build_test.go:101` reads `app.js`; if app.css is embedded the same way) and assert it contains a rule making the last main tab borderless: on unchanged code assert the string `.tab:last-child { border-right: none; }`; this test is expected to be updated in step 7 of the change (it pins current behavior so the selector edit is deliberate). If app.css is not readable from tests, drop this test and list it under Manual-only.
- `TestLayoutNav_TabsUnchangedForSignedInUser`: render `layout` (same helper as `TestContactDetailTemplateRenders`) with `CurrentUser` set to a `&User{DisplayName: "T"}`, assert the five main tab labels (`Parts`, `Vendors`, `POs`, `Records`, `Reports`) appear in order inside `<nav class="tabs">` and `--main-tabs:5` is present. Passes today; guards the nav range from the layout edit.
- Integration (`arx_go/find_integration_test.go`, `//go:build integration`): none beyond the existing ones listed in the audit; run the full integration suite after the seed change.

### 3. Red tests (acceptance criteria; stub `Find`, `findURL`, `matchedLabel`, `FindRecords`, `FindResult` first so they compile and fail on assertion)
Unit (no DB), `arx_go/find_test.go`:
- `TestFindURL`: lot `{Kind:"lot",PartID:3012,ID:8302}` -> `/part/3012/lots/8302`; unit `{Kind:"unit",PartID:3013,ID:8501}` -> `/part/3013/units/8501`; build with `OutputLotID:8306,PartID:3013` -> `/part/3013/lots/8306`; build with `OutputLotID:0,PartID:3005` -> `/part/3005/build`. Fails today: stub returns "".
- `TestMatchedLabel`: lot id+name -> "ID, Lot number"; lot name only -> "Lot number"; unit serial only -> "Serial number"; build -> "ID". Fails today: stub returns "".
- `TestFindTemplateRenders`: render `parts/find.html` under the layout (like `renderPartsList`, `templates_parse_test.go:286`) with (a) `NotFound:true, Q:"<b>x</b>"` -> contains `No record found for` and the HTML-escaped q, no raw `<b>`; (b) two `findRow`s -> contains both `href`s, "Lot", "Unit", matched labels. Fails today: template file does not exist (parse error).
- `TestLayoutNav_HasFindForm`: layout render with a non-nil `CurrentUser` contains `action="/find"`, `method="get"`, `name="q"`; render with `CurrentUser` nil does not contain `action="/find"`. Fails today: no form in layout.

Integration, `arx_go/find_integration_test.go` (`//go:build integration`, use `liveHandler`; call `h.Find` directly with `httptest.NewRequest(GET, "/find?q=...")`, read `rec.Code`, `Location`, `Body`, as `popout_integration_test.go` does; no cleanup needed, read-only):
- `TestIntegration_Find_ResolvesLotUnitBuild`: `q=8302` -> 302, `Location=/part/3012/lots/8302` (lot matched on id AND lot_number: one result, not two); `q=8303` -> `/part/3007/lots/8303`; `q=SN-3013-001` -> `/part/3013/units/8501`; `q=8501` -> same unit by id; `q=8203` -> `/part/3013/lots/8306` (build with output lot); `q=8201` -> `/part/3005/build` (build, no output lot). Fails today: `Find` stub writes no redirect (code 200, empty).
- `TestIntegration_Find_AmbiguousListsAll`: `q=8301` -> 200, body contains `/part/3007/lots/8301` (lot 8301: id + lot number) and `/part/3012/units/8505` (new seed unit, serial '8301'), "ID, Lot number", "Serial number", part numbers `RAW-1002` and `ASM-1002`; no `Location` header. Fails today: stub. Requires the seed row (item 9).
- `TestIntegration_Find_NoMatch`: `q=NO-SUCH-VALUE`, `q=99999999999` (> int4), `q=0` -> each 200, no `Location`, body contains `No record found for` plus the (escaped) q. Fails today: stub.
- `TestIntegration_Find_BlankQ`: `q=` and no q -> 200, no `Location`, no "No record found" (Q5). Fails today: stub.
- `TestIntegration_Find_ExactOnly`: `q=SN-3013` and `q=sn-3013-001` -> no match message (exact, case-sensitive; Q4). Fails today: stub.

### 4. Manual-only
- Visual check of the nav input at desktop width and at <576px (does it crowd the five main tabs; popdown section bars still sit under their tab); dark mode.
- Scanner behavior: real barcode scanner (type + Enter) into the nav box lands on the record.
- Last main tab keeps no right border after the `:last-of-type` selector change.
- Section-tab popdown hover/keyboard (`app.js:1437+`) still works with the form inside `nav.tabs`.
- Reseed ArxDev / restart the local container so `8505` exists, then run `ARX_TEST_DSN=... go test -tags integration ./arx_go/...`.

## Verify
`go build ./... && go vet ./... && go test ./...`; `sqlc generate` then `sqlc diff` clean; integration suite on the local container (per CLAUDE.md pre-commit step 2).

## Open questions
1. **Build destination.** No build detail page exists. Plan reuses the existing list-page convention: output lot trace if the build made a lot, else the part's Build tab (`/part/{id}/build`, which shows the history, not that specific build). Confirm, or should #416 wait for a build page?
2. **Whitespace/case:** plan trims leading/trailing whitespace on q (scanners can append newline/space) and matches case-sensitively. Confirm (or want case-insensitive?).
3. **Inactive records:** plan includes inactive lots/units (`is_active = FALSE`) in matches, since "every match counts". Confirm.
4. **Mixed numeric:** q like `8301` is also tried as an id; q with leading zeros (`08301`) parses as id 8301 but not as lot number 8301. Acceptable?
5. **Blank q on `/find`:** plan renders the page with no message and no redirect. OK?
6. **"Stays on the page" for no match:** plan lands on `/find?q=...` showing the inline message (a full page load). The alternative (message shown on the page the user was on, without navigating) needs a JSON endpoint plus JS in `app.js`. Which?
7. **Nav placement:** plan puts the input in `<nav class="tabs">` after the main tabs (needs the `.tab:last-child` -> `:last-of-type` CSS tweak). Alternative: in the dark `.header-actions` bar next to the theme toggle (no tab-border CSS change). Which?
8. **Seed row:** unit 8505 on part 3012 (a `lot`-tracked part, so a manual unit there is unusual) is chosen because every other part with units has unit-count assertions. Acceptable, or prefer a different part / adding a serial-tracked part?
9. **Changelog version:** next patch number after `0.8.91` (is 0.8.91 already released/merged)?

## Resolved decisions
1. Build match redirects exactly like the list-page link (output lot trace if the build produced a lot, else `/part/{id}/build`).
2. Trim whitespace on `q`; match case-sensitively.
3. Include inactive lots and units in matches.
4. Numeric: `q` is also tried as an id, so `08301` matches id 8301 but not lot number `8301`. Accepted.
5. Blank `q` on `/find`: render the page with no message and no redirect.
6. No match: full load of `/find?q=<q>` with the inline "No record found for <q>" message (no JSON endpoint, no JS).
7. **Nav placement: header bar.** Put the lookup form in `.header-actions` in `arx_go/templates/shared/layout.html`, before the user-name link: always-visible small text input (`form-control form-control-sm`, ~160px wide, `bi-search` icon / submit button, `name="q"`, `method="get" action="/find"`, `d-none d-sm-flex` like the logo so it hides on narrow screens). **Do not touch `<nav class="tabs">` or the `.tab:last-child` CSS**; drop file change 7's `.tab:last-of-type` item and keep only whatever header-actions styling is needed (prefer Bootstrap utilities over custom CSS). Update the layout characterization test accordingly (assert the form in the header, nav unchanged).
8. Seed unit 8505 on part 3012, serial '8301', as in file change 9. Human reseed required (the same reseed covers #422's new seed build 8204).
9. Changelog: top released entry is 0.8.91 (2026-10-30, #420 is in main), so this batch is the next patch 0.8.92 (one combined entry for #416, #422, #423).
