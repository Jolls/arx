# Plan: #387 unit serial popout, build number popout, thumbnail unification

Part of #368. No schema change (no migration, no `SQL/*.sql` DDL edit). One seed edit (see Test plan, Open question 1). Popout routes use `withAuth` only, like the existing popout routes (`arx_go/main.go:314-322`). `RELEASE_NOTES.md` untouched (per-release only).

## Current code (verified)

- Popout JS: `arx_go/static/shared/app.js:1274-1348` (one IIFE; trigger = nearest ancestor with `data-popout-url`, `closest`). Cache is per-URL with 45 s TTL.
- Old thumbnail JS: `arx_go/static/shared/app.js:1238-1272` (IIFE on `.pn-thumb`, builds a `div.pn-thumb-tip.hover-preview-box`, reads `data-thumb`). CSS: `arx_go/static/app.css:373-374` (comment names `.pn-thumb-tip`) and `:403-413` (`.pn-thumb-tip` rules). `.pn-thumb` itself has no CSS rule.
- `.pn-thumb` is emitted in exactly two places, both breadcrumbs, neither also has a `data-popout-url`: `arx_go/templates/shared/partials.html:197` and `arx_go/templates/parts/part_edit.html:5` (`<span class="pn-thumb" data-thumb="{{.Part.ThumbnailURL}}">` + `bi-image` icon, only when `.Part.ThumbnailURL`). No JS emits `.pn-thumb`. The parts grid (`app.js:183`) and BOM/where-used cells already get the image through `popout_part_where_used` / `popout_part_bom_component` (`Thumb`, #385/#386), so they are not touched.
- `.img-hover-wrap`/`.img-hover-preview` (attachments, `app.css:384-401`) is a separate CSS-only mechanism; out of scope, leave.
- Existing unit popout: `PopoutUnitConsumers` (`arx_go/api.go:176-189`) -> `h.inventory().UnitConsumers` -> `popout_lot_consumers` ("Consumed into"), route `main.go:316` `/popout/part/{id}/units/{unitID}/consumers`. Triggers using it: `templates/parts/all_units.html:36` (serial cell) and `static/shared/app.js:1164` (`buildLotSubRow`, used for both lot and unit nodes; unit nodes get `/units/<id>/consumers`).
- Unit serial cells with no popout today: `templates/parts/part_units.html:33`, `templates/parts/part_detail.html:230` (recent units), `templates/parts/part_unit_trace.html:52` and `:89` (`.IsUnit` branch of ancestor/descendant rows; `.PartID`, `.ID`, `.Number` available).
- Build number cells (no popout today): `templates/parts/all_builds.html:37` (`#{{.ID}}`, link to output lot or `/part/{{.PartID}}/build`), `templates/parts/part_units.html:35` (plain text `Build #{{.BuildIDVal}}`), `templates/parts/part_unit_trace.html:25` (`{{.Build.Label}}`, `.Build` is `*BuildOption{ID,Label}`), `templates/records/records_show.html:131` (`{{.Trace.LinkedBuild.Label}}`, `*BuildOption`). `templates/parts/part_build.html` history rows show no build number; no JS builds a build-number cell.
- Data available without schema change: `unit.build_id`, `build.output_lot_id`, `genealogy` (parent lot/unit -> child lot/unit, no build column), `form_record.unit_id`, `result.pass_fail` (TRUE pass / FALSE fail / NULL not evaluated), `result.type` (0 = data row, 1 = heading). Consumed components of a build = genealogy parents whose child is `build.output_lot_id` OR a unit with `unit.build_id = build.id`. Produced units = `unit WHERE build_id = build.id`.
- Existing services: `inventory.Service.GetUnit` (`found` bool), `GetBuildOption`, `UnitConsumers`, `ListAllBuilds`; `h.inventory()`, `h.records()`, `h.fetchPartBasic` (thumbnail), `capPopoutRows`/`popoutRows` (`api.go:213-222`), `renderPopout` (`handlers.go:684`). `popouts` template set = `shared/popouts.html` only (no partials).

## Decisions fixed by this plan (see Open questions for ones needing confirmation)

- New routes (GET, `withAuth`, id = numeric): `/popout/unit/{id}`, `/popout/build/{id}`, `/popout/part/{id}/thumb`. Invalid id or missing row -> `http.NotFound`; other errors -> `serverError(w, "database error", err)`.
- Remove `PopoutUnitConsumers`, its route (`main.go:316`) and `/consumers` use for units; `PopoutLotConsumers` and the lot route stay. `popout_lot_consumers` stays (lot popout + consumer nodes that are units).
- Unit popout shows: serial (link `/part/{part}/units/{id}`), Active/Scrapped badge, Part (link `/part/{part}`), Lot (link or dash), Build (`Build #N`, link-free text), Test records summary, then "Consumed into" (the content the serial cell shows today, so nothing is lost). Lists cap at 5 with "and more".
- Test-record state per record (open question 2 default): `Fail` if any data result is FALSE; else `Pass` if at least one data result is TRUE; else `Not evaluated`. Header line: `N records: P pass, F fail` (totals over all active records of the unit; only the first 5 rows listed).
- Build popout shows: `Build #N`, part (link), qty, date, user, output lot (link to `/part/{part}/lots/{lot}` or none), "Consumed" list (lots and units, aggregated qty, links), "Produced units" list (links). Cap 5 each with "and more".
- Thumbnail popout: fragment = `<img class="popout-thumb">` only (breadcrumb cell has no other content).
- Attribute order: where an existing test asserts `href="...">text<` (all_builds), put `data-popout-url` BEFORE `href` on that anchor.

## Changes

### 1. SQL + services
`internal/inventory/inventory.sql` (append after `ListAllBuilds`):
- `-- name: GetBuildDetail :one`: `SELECT b.id, b.part_id, COALESCE(p.part_number,'') AS part_number, COALESCE(p.description,'') AS part_description, b.qty, b.build_date, b.username, b.output_lot_id, COALESCE(l.lot_number,'') AS lot_number FROM build b JOIN part p ON p.id = b.part_id LEFT JOIN lot l ON l.id = b.output_lot_id WHERE b.id = sqlc.arg(id);`
- `-- name: ListBuildConsumed :many`: per distinct consumed node, `node_type`, `id`, `number`, `part_id`, `part_number`, `SUM(qty_consumed)::numeric AS qty`, ordered `node_type, id`, `LIMIT sqlc.arg(n)::int`. Two `UNION ALL` branches (lots via `g.parent_lot_id`, units via `g.parent_unit_id`) over `genealogy g JOIN build b ON b.id = sqlc.arg(build_id)::int` with `WHERE g.child_lot_id = b.output_lot_id OR g.child_unit_id IN (SELECT id FROM unit WHERE build_id = b.id)`; wrap in a subquery and `GROUP BY` node_type/id/number/part_id/part_number. Same `'lot'::text`/`'unit'::text` literals as `ListTraceAncestors` (`inventory.sql:110`).
- `-- name: ListBuildUnits :many`: `SELECT u.id, u.serial_number, u.part_id, u.is_active FROM unit u WHERE u.build_id = sqlc.arg(build_id)::int ORDER BY u.id LIMIT sqlc.arg(n)::int;`
- `internal/inventory/inventory.go`: add `BuildDetail` struct (fields of `GetBuildDetail`, `OutputLotID *int`) + `OutputLotIDVal()` like `BuildListRow` (`:620`); `func (s *Service) GetBuildDetail(ctx, id int) (BuildDetail, bool, error)` (found=false on `sql.ErrNoRows`, same shape as `GetUnit`); `type BuildConsumed struct{NodeType string; ID int; Number string; PartID int; PartNumber string; Qty decimal.Decimal}` with `IsUnit()`; `func (s *Service) ListBuildConsumed(ctx, buildID, n int) ([]BuildConsumed, error)`; `type BuildUnit struct{ID int; SerialNumber string; PartID int; IsActive bool}`; `func (s *Service) ListBuildUnits(ctx, buildID, n int) ([]BuildUnit, error)`.

`internal/records/records.sql` (append):
- `-- name: ListUnitRecordSummary :many`: `SELECT r.id, r.record_date, COALESCE(r.record_type,'') AS record_type, (COUNT(res.id) FILTER (WHERE res.pass_fail = TRUE))::int AS passed, (COUNT(res.id) FILTER (WHERE res.pass_fail = FALSE))::int AS failed FROM form_record r LEFT JOIN result res ON res.form_record_id = r.id AND res.type = 0 WHERE r.unit_id = sqlc.arg(unit_id)::int AND r.is_active = TRUE GROUP BY r.id ORDER BY r.record_date DESC NULLS LAST, r.id DESC;`
- `internal/records/records.go`: `type UnitRecordSummary struct{ID int; Date *time.Time; Type string; Passed, Failed int}` with `func (u UnitRecordSummary) State() string` returning `"Fail"`/`"Pass"`/`"Not evaluated"` per the rule above; `func (s *Service) UnitRecordSummary(ctx, unitID int) ([]UnitRecordSummary, error)` (use existing `nullTime`-style conversion for the date).

Run `sqlc generate` (repo root) and commit `internal/dbq`. If sqlc emits `interface{}` for `record_date` or the `COUNT FILTER`, add the cast used by neighbouring queries.

### 2. Templates - `arx_go/templates/shared/popouts.html`
Add (no layout/partials deps):
- `popout_unit` (data: `Serial`, `ID`, `PartID`, `PartNumber`, `IsActive bool`, `LotID *int`, `LotNumber`, `BuildLabel` (string, "" when none), `Passed`/`Failed` record totals as `RecTotal`, `RecPass`, `RecFail int`, `Records []records.UnitRecordSummary` (first 5), `RecMore bool`, `Consumers []inventory.TraceNode` (first 5), `ConsMore bool`): title = serial link; badge; Part link; Lot link (`/part/{{.PartID}}/lots/{{derefInt .LotID}}`) or `&mdash;`; Build text or `&mdash;`; `popout-title` "Test records" + `N records: P pass, F fail` (or "No test records."); list of `<a href="/records/{{.ID}}">#{{.ID}}</a> {{.Type}} {{formatDate .Date}} - {{.State}}` (date omitted when nil); then "Consumed into" section with the same row markup as `popout_lot_consumers` lines 11-17 (inline it; do not nest `{{template}}` since that block has its own title and empty text); "and more" lines when truncated.
- `popout_build` (data: `ID`, `PartID`, `PartNumber`, `Qty`, `Date`, `Username`, `OutputLotID *int`, `OutputLot`, `Consumed []inventory.BuildConsumed` (5), `ConsMore`, `Units []inventory.BuildUnit` (5), `UnitsMore`): title `Build #{{.ID}}`; part link, `qty {{printf "%.5g" .Qty}}`, date, user; output lot link or "No output lot"; "Consumed" list (lot -> `/part/{{.PartID}}/lots/{{.ID}}`, unit -> `/part/{{.PartID}}/units/{{.ID}}` + `unit` badge; `{{.PartNumber}} (qty {{printf "%.5g" .Qty}})`), empty text "No components recorded."; "Produced units" list of serial links, empty text "No units produced."
- `popout_part_thumb` (data: `Thumb string`): `{{if .Thumb}}<img class="popout-thumb" src="{{.Thumb}}" alt="preview">{{end}}`.
- Update the header comment only if it needs it (no change required).

### 3. Handlers - `arx_go/api.go` and `arx_go/main.go`
- Delete `PopoutUnitConsumers` (`api.go:176-189`) and its route line (`main.go:316`).
- Add `PopoutUnit`: `strconv.Atoi(r.PathValue("id"))` -> NotFound; `h.inventory().GetUnit` (not found -> NotFound); build label via `h.fetchBuildOption` when `unit.BuildID != nil` (nil result -> ""); `h.records().UnitRecordSummary`; totals and `State()` rollup in Go, cap with `capPopoutRows`; `h.inventory().UnitConsumers(ctx, unit.ID)` then `capPopoutRows`; render `popout_unit`.
- Add `PopoutBuild`: `GetBuildDetail` (not found -> NotFound), `ListBuildConsumed(ctx, id, popoutRows+1)`, `ListBuildUnits(ctx, id, popoutRows+1)`, `capPopoutRows` each; render `popout_build`.
- Add `PopoutPartThumb`: Atoi id -> NotFound; `h.fetchPartBasic`; render `popout_part_thumb` with `{"Thumb": p.ThumbnailURL}`. (Missing part: `fetchPartBasic` returns an error; treat `sql.ErrNoRows` as NotFound like `PopoutSupplier`.)
- Routes in `main.go` next to `:314-322`: `/popout/unit/{id}`, `/popout/build/{id}`, `/popout/part/{id}/thumb`.

### 4. Triggers (`data-popout-url`; keep href/classes; one trigger per cell)
Unit serial -> `/popout/unit/<unit id>`:
- `all_units.html:36` (replace the `/consumers` URL with `/popout/unit/{{.ID}}`).
- `part_units.html:33` (`{{.ID}}`), `part_detail.html:230` (`{{.ID}}`), `part_unit_trace.html:52` and `:89` (`{{.ID}}`, unit nodes only).
- `static/shared/app.js:1164` (`buildLotSubRow`): popout string becomes `isUnit ? '/popout/unit/' + node.ID : '/popout/part/' + node.PartID + '/lots/' + node.ID + '/consumers'`.
Build number -> `/popout/build/<build id>`:
- `all_builds.html:37`: both anchors (output-lot and `/build` branch) get `data-popout-url="/popout/build/{{.ID}}"` placed before `href`.
- `part_units.html:35`: wrap `Build #{{.BuildIDVal}}` in `<span data-popout-url="/popout/build/{{.BuildIDVal}}">`.
- `part_unit_trace.html:25`: wrap `{{.Build.Label}}` in `<span data-popout-url="/popout/build/{{.Build.ID}}">`.
- `records_show.html:131`: wrap `{{.Trace.LinkedBuild.Label}}` in `<span data-popout-url="/popout/build/{{.Trace.LinkedBuild.ID}}">`.
Thumbnail -> `/popout/part/<id>/thumb`:
- `partials.html:197` and `part_edit.html:5`: replace `class="pn-thumb" data-thumb="{{.Part.ThumbnailURL}}"` with `data-popout-url="/popout/part/{{.Part.ID}}/thumb"` (still only inside `{{if .Part.ThumbnailURL}}`).

### 5. Remove old thumbnail mechanism
- `static/shared/app.js`: delete the comment + IIFE at `:1238-1272`.
- `static/app.css`: delete `.pn-thumb-tip` rules (`:403-413` incl. its comment); change comment at `:373-374` to name only `.img-hover-preview` and `.hover-popout`. Keep `.hover-preview-box` (used by popout and `.img-hover-preview` shares the look).
- `popout_part_where_used` stays as-is; update the stale comment in `PopoutPartWhereUsed` (`api.go:203-204`) only if it names `.pn-thumb` (it names "thumbnail hover (#696)"): reword to "the thumbnail rides this popout (#385)".

### 6. CHANGELOG.md
New top entry `## [0.8.79] - 2026-10-28`: `### Added` unit serial and build number popouts; `### Changed` part-number breadcrumb thumbnail now uses the shared popout (old `.pn-thumb-tip` removed), `/popout/part/{id}/units/{unitID}/consumers` replaced by `/popout/unit/{id}`; each linking `https://github.com/Jolls/arx/issues/387`.

## Test plan

Integration tests are `//go:build integration` (`arx_go/*_integration_test.go`); run per CLAUDE.md against the local throwaway container (`ARX_TEST_DSN` explicit, restart db to reset). Run `go build/vet/test ./...` and `sqlc diff` for the unit-level tests.

### 1) Coverage audit (existing)
- `arx_go/popout_cache_test.go`: `TestPopoutJS_CachesByURL`, `TestPopoutJS_CacheHasTTL`, `TestPopoutJS_GridBelowMinURLInterpolated` (JS source substring checks; unaffected). No test references `pn-thumb`/`pn-thumb-tip`.
- `arx_go/templates_parse_test.go`: `TestParseTemplates` (`popouts` key must still parse), `TestPopoutPartWhereUsedThumb`, `TestPopoutFragmentsExecute` (table of fragments; add new rows here), `TestAllUnitsTemplateRenders` (`:331`, asserts `href="/part/2/units/1"` substring; survives), `TestAllBuildsTemplateRenders` (`:344`, asserts `href="/part/2/lots/9">#5<` and `href="/part/2/build">#6<` -> requires `data-popout-url` before `href`, see Decisions).
- `arx_go/popout_integration_test.go`: `TestIntegration_PopoutPartWhereUsed_Characterization`, `TestIntegration_PopoutTriggers` (render-and-grep pattern; extend), helpers `popoutBody`, `expectContains`.
- `arx_go/integration_test.go`: `TestIntegration_AllUnitsAndBuilds` (`:4565`, `/units` serials, `/builds` `#8201..#8203`), `TestIntegration_PartUnitsHandler` (`:3724`), `TestIntegration_PartUnitTraceHandler` (`:3752`, body contains `SN-3013-001`, `8306`, `8203`; wrong-part / missing unit paths). All substring checks that survive the attribute additions.
- `arx_go/sqlc_converted_lint_test.go`: no raw SQL in `arx_go` non-test files (new handlers use services only).
- Not covered today: `PopoutUnitConsumers` and `PopoutLotConsumers` handlers; the `.pn-thumb` JS/CSS; `part_units.html`/`part_unit_trace.html`/`records_show.html` build cells; no test asserts breadcrumb thumbnail markup.

### 2) Characterization tests (must pass on unchanged code)
- `TestIntegration_PopoutUnitConsumers_Characterization` (`arx_go/popout_integration_test.go`): call `h.PopoutUnitConsumers` with path values `id=3005`, `unitID=8503` (unit 8503 is a parent in genealogy edge 8404); assert 200 and body contains `Consumed into` and `SN-3013-001`. Pins the consumers content the new `popout_unit` must keep. (Deleted/replaced by the new unit test when the handler is removed.)
- `TestIntegration_AllUnitsTriggers_Characterization` (same file): `h.AllUnits` body contains `data-popout-url="/popout/part/3013/units/8501/consumers"` and `data-popout-url="/popout/part/3013/lots/8306/consumers"`. (Replaced by the red trigger test below.)
- No breadcrumb-thumbnail characterization test: no seeded part has a `ThumbnailURL` (Open question 6); the markup is pinned by the template test in part 3.
- `TestAllBuildsTemplateRenders` / `TestAllUnitsTemplateRenders` already pin link markup; keep passing.

### 3) Red tests (fail today, pass after; stubs listed so they compile)
Stubs to add so tests compile (bodies `panic("TODO")` or `http.NotFound(w, r)` until implemented): `func (h *Handler) PopoutUnit(w http.ResponseWriter, r *http.Request)`, `func (h *Handler) PopoutBuild(w http.ResponseWriter, r *http.Request)`, `func (h *Handler) PopoutPartThumb(w http.ResponseWriter, r *http.Request)`; `func (s *Service) UnitRecordSummary(ctx context.Context, unitID int) ([]UnitRecordSummary, error)` and `type UnitRecordSummary struct{ID int; Date *time.Time; Type string; Passed, Failed int}` + `func (u UnitRecordSummary) State() string` in `internal/records`; `GetBuildDetail`/`ListBuildConsumed`/`ListBuildUnits` + types in `internal/inventory`. New templates `popout_unit`, `popout_build`, `popout_part_thumb` do not exist yet, so template tests fail with "no such template".

No build tag (`arx_go/templates_parse_test.go`, extend `TestPopoutFragmentsExecute` table; fail today: template missing):
- `popout_unit`: fixture with serial `SN-1`, lot `L-7`, build label `Build #5 - qty 1 - 2026-05-25`, records `[{ID:7014 Failed:1 Passed:1}, {ID:7011 Passed:2}]`, totals 2/1/1, one consumer. Assert contains `href="/part/2/units/1"`, `href="/part/2/lots/7"`, `Build #5`, `2 records: 1 pass, 1 fail`, `href="/records/7014"`, `Fail`, `Consumed into`. Empty fixture: `No test records.`, `Not consumed into anything yet.`; `RecMore` true -> `and more`.
- `popout_build`: fixture consumed lot + unit, produced unit, output lot; assert `Build #8203`, `href="/part/9/lots/8301"`, `href="/part/9/units/8503"`, `Produced units`, `href="/part/9/units/8501"`; empty fixture: `No components recorded.`, `No units produced.`, `No output lot`.
- `popout_part_thumb`: `Thumb:"/local/t.png"` -> contains `<img class="popout-thumb" src="/local/t.png"`; `Thumb:""` -> no `<img`.
- `TestUnitRecordSummaryState` (`internal/records/records_test.go`): `{Failed:1,Passed:3}` -> `Fail`; `{Passed:2}` -> `Pass`; `{}` -> `Not evaluated`. Fails today: type missing (stub returns "").
- `TestPopoutJS_ThumbMechanismRemoved` (`arx_go/popout_cache_test.go`, uses `readAppJS`): app.js contains neither `pn-thumb` nor `pn-thumb-tip`; also read `static/app.css` via `staticFS` and assert no `.pn-thumb-tip`. Fails today: both present.
- `TestPopoutJS_LotSubRowUnitTrigger`: app.js contains `'/popout/unit/' + node.ID` and not `'/units/' : '/lots/') + node.ID + '/consumers'`. Fails today.
- `TestBreadcrumbThumbUsesPopout` (`templates_parse_test.go`): render `parts/part_edit.html` (use `renderPartsList` helper pattern, `Part` with `ThumbnailURL:"/local/t.png"`, not new/duplicate) and assert contains `data-popout-url="/popout/part/<id>/thumb"` and does not contain `pn-thumb` or `data-thumb=`. Fails today. (If the helper cannot render `part_edit.html` standalone, assert on the `partials.html` breadcrumb through any part sub-tab page, e.g. `part_units.html`.)
- Extend `TestAllUnitsTemplateRenders` / `TestAllBuildsTemplateRenders`: all_units serial anchor contains `data-popout-url="/popout/unit/1"` and not `/consumers`; all_builds contains `data-popout-url="/popout/build/5" href="/part/2/lots/9">#5<` and `data-popout-url="/popout/build/6" href="/part/2/build">#6<`. Fails today.
- `TestOnePopoutPerCell` (`templates_parse_test.go`): for the rendered all_units, all_builds, part_units, part_unit_trace pages (fixtures as in existing render tests), split each `<td>...</td>` and assert `strings.Count(td, "data-popout-url") <= 1`. Passes today for all_units/all_builds; guards the new work on part_units/part_unit_trace.

Integration (`arx_go/popout_integration_test.go`, `//go:build integration`; call handlers with `popoutBody(t, h.PopoutX, "<id>")`; fail today: handlers absent/stub):
- `TestIntegration_PopoutUnit`: id `8501` -> 200, contains `SN-3013-001`, `ASM-1003`, `8306` (lot link `/part/3013/lots/8306`), `Build #8203`, `2 records: 0 pass, 1 fail`, `href="/records/7014"`, `href="/records/7013"`; id `8503` -> contains `Consumed into`, `SN-3013-001`, `1 record: 1 pass, 0 fail` (template uses `{{.RecTotal}} record{{if ne .RecTotal 1}}s{{end}}`; update the `popout_unit` fixture assertion above to match: `2 records: 1 pass, 1 fail`); id `8504` (manual, no lot/build) -> `&mdash;` for lot and build, `No test records.`; id `999999` -> 404; id `abc` -> 404.
- `TestIntegration_PopoutBuild`: id `8203` -> contains `Build #8203`, `ASM-1003`, `/part/3012/lots/8302`, `/part/3007/lots/8303`, `/part/3007/lots/8301`, `/part/3005/units/8503`, `/part/3013/units/8501`, output lot `8306`; id `8201` -> `No components recorded.` and `/part/3005/units/8503`; id `8202` -> `/part/3007/lots/8301` (edge 8401) and `No units produced.`; id `999999` -> 404.
- `TestIntegration_PopoutPartThumb`: id `abc` -> 404; id `999999` -> 404; id `3005` -> 200 (body has `<img` only if the seeded part has a local thumbnail; assert 200 and no `Used in`).
- `TestIntegration_PopoutTriggers` (extend): `h.AllUnits` contains `/popout/unit/8501` and `/popout/unit/8503`, not `/units/8501/consumers`; `h.AllBuilds` contains `/popout/build/8203`; `h.PartUnits` (part 3013) contains `/popout/unit/8501` and `/popout/build/8203`; `h.PartUnitTrace` (3013, 8501) contains `/popout/build/8203` and `/popout/unit/8503` (ancestor unit row); `h.PartDetail` (3013) contains `/popout/unit/8501`; `h.RecordShow` for record 7013 (the linked-build cell) contains `/popout/build/8203`.
- `TestIntegration_PopoutUnitConsumersRemoved`: not needed (route removal verified by compile + `TestIntegration_PopoutTriggers` absence checks).

Seed (see Open question 1): `SQL/seed_test_data.sql` currently has NULL `pass_fail` on every result of unit-linked records 7011-7014 (`:706-724`). To make Pass/Fail assertable, edit those INSERT rows in place (no new rows, no new ids): result 7150 -> TRUE and 7151 -> FALSE (record 7014, unit 8501 => Fail); results 7138 and 7139 -> TRUE (record 7011, unit 8503 => Pass). Records 7013 and 7012 stay NULL (Not evaluated). No change to `SQL/SCHEMA.md` (no new table). Before editing, grep tests for ids 7011/7014 and yield/dashboard counts (`pass_fail`, `any_fail`, `ListFailureModes`, `TestIntegration_Yield*`) and update expectations those edits change. Human reseed of ArxDev required afterwards (do not run the seed on ArxDev; local container is fine).

### 4) Manual-only
- Hover a unit serial on `/units`, part Units tab, part dashboard recent units, unit trace ancestor/descendant rows, and an expanded unit sub-row on a part's Lots tab: popout shows lot, build, test-record summary, consumed-into; links navigate; no second box.
- Hover a build number on `/builds`, part Units tab, unit trace header, and a test record's Build row: consumed lots/units and produced units with links.
- Part page breadcrumb (`/part/{id}/...` and edit): hover shows the thumbnail via the shared popout; no leftover `.pn-thumb-tip` box; part without a thumbnail shows no icon/hover.
- Popout positioning near viewport top/bottom edges (image load re-places), dark mode, phone width.
- Hover latency against the remote DB for the unit popout (3 queries: unit, build option, record summary, consumers) and build popout (3 queries).

## Open questions
1. Seed: OK to edit existing `pass_fail` values on results 7138/7139 (record 7011) and 7150/7151 (record 7014) in `SQL/seed_test_data.sql` so a Pass and a Fail unit exist? Alternative is new unit/record/result rows, which shifts unit/record counts asserted elsewhere.
2. Pass/fail rule per record: default is Fail if any data result FALSE, else Pass if any TRUE, else Not evaluated. Different rule (e.g. Pass only when every evaluated step is TRUE and none NULL; or use the record's locked/approved state)?
3. Route naming: plan uses flat `/popout/unit/{id}` and `/popout/build/{id}` (unit and build ids are globally unique) and removes `/popout/part/{id}/units/{unitID}/consumers`. Keep the `/popout/part/{id}/...` nesting instead?
4. Unit popout keeps the existing "Consumed into" section (combined into one fragment, per "combine into a single fragment"). Confirm, or drop it?
5. AC "No cell carries more than one popout trigger": existing multi-trigger cells outside this issue (parts grid `app.js:183` part number + Below Min badge, `partials.html:302` and `part_detail.html:113` stock figure + Below Min badge) are not changed here. In scope, or only the cells this issue touches?
6. Build-number trigger placement: plan covers `all_builds`, `part_units`, unit trace, and `records_show` (Build row). `records_show` is adjacent to the out-of-scope "records result summary"; keep it? Also, no seeded part appears to have a local-file thumbnail (`ThumbnailURL`), so the thumbnail integration assertion is limited to 200/404 and the template test; add a seeded thumbnail attachment (new seed rows + `part_attachment` counts) or accept manual-only for the image?

## Resolved decisions (final; supersede any default above)
1. Seed: edit existing pass_fail values on results 7138/7139 (record 7011) and 7150/7151 (record 7014) in SQL/seed_test_data.sql so one Pass and one Fail unit exist. User reseeds ArxDev.
2. Per-record rule: Fail if any data result is FALSE; else Pass if any TRUE; else Not evaluated.
3. Routes: flat `/popout/unit/{id}`, `/popout/build/{id}`, `/popout/part/{id}/thumb`; remove PopoutUnitConsumers and `/popout/part/{id}/units/{unitID}/consumers`.
4. Unit popout keeps "Consumed into", combined into the single unit fragment.
5. One-trigger-per-cell: only cells this issue touches. The 3 existing number + Below Min badge pairs (app.js:183, partials.html:302, part_detail.html:113) stay as two triggers on separate elements; AC is reworded to "no single element carries more than one popout trigger". Do not change those cells.
6. Keep the build-number trigger on the Build row in records_show.html.
7. No seeded thumbnail rows; breadcrumb thumbnail covered by a template test + manual check.
