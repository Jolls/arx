# #417 Part Summary: Traceability card

Branch: `feature/417-part-traceability-card`. Model: Sonnet (no schema change).

## Findings (verified in current code)

- Part flags (arx_go/models/part.go):
  - Lots: `Part.ShowLots()` = `IsLotTracked` (`TracksLots(TrackingMode)`: `lot` | `lot_serial`).
  - Units: `Part.ShowUnits()` = `TracksSerials(TrackingMode)` (`serial` | `lot_serial`).
  - Builds: `Part.ShowBuild()` = `HasBOM && ShowInventory()` (the Build detail tab rule).
- Summary dashboard: `PartDetail` in arx_go/parts.go (~L263-278 builds `LotCount`/`UnitCount` via `h.lotCountForPart` / `h.unitCountForPart`, passed to the template at ~L299-316). Template: arx_go/templates/parts/part_detail.html: Inventory card L104-130, existing Lots card L182-211 (`Total lots`), Units card L213-242 (`Total units`). No build count exists anywhere (only `ListPartBuilds`/`ListBuildHistory` row queries in internal/inventory/inventory.sql).
- Counts: `CountPartLots` = `COUNT(*) FROM lot WHERE part_id`; `CountPartUnits` likewise. `ListAllLots`/`ListAllUnits`/`ListAllBuilds` are unfiltered cross-part lists (INNER JOIN part), no active filter; the list status filter defaults to All. So per-part counts equal that part's rows in the lists.
- Pre-fill: already supported. `restoreFilterStateFromURL()` in arx_go/static/shared/app.js (#513) reads `f.<data-col>` from the query string; URL wins over sessionStorage; the input is a normal editable input and `saveFilterStateToURL` mirrors edits back. Part Number column key is `col-pn` on lots, units and builds (arx_go/filter_cols.go). So the link is `/lots?f.col-pn=<part number>` (same for `/units`, `/builds`). No JS or handler change needed.
- Match semantics (app.js `termMatcher`/`matchesRow`): case-insensitive SUBSTRING, unless the value contains `*` or `?` (then whole-field glob). `;` splits terms; a leading `!` excludes. A plain `ASM-1001` therefore also matches `ASM-1001-X` or `XASM-1001`, so a list can show more rows than the card count. See Open question 1.

## File changes

1. `internal/inventory/inventory.sql`: add after `CountPartUnits` (L184-185):
   ```sql
   -- name: CountPartBuilds :one
   SELECT COUNT(*) FROM build WHERE part_id = sqlc.arg(part_id);
   ```
2. Run `sqlc generate` (repo root); commit the regenerated `internal/dbq` (never hand-edit).
3. `internal/inventory/inventory.go`: add next to `CountPartUnits` (L498), same shape:
   ```go
   // CountPartBuilds returns the total number of builds of a part.
   func (s *Service) CountPartBuilds(ctx context.Context, partID int) (int, error) {
   	n, err := s.q.CountPartBuilds(ctx, partID)
   	return int(n), err
   }
   ```
4. `arx_go/build.go` (or `arx_go/lot.go` beside `lotCountForPart`; use lot.go to match the sibling wrappers): add
   ```go
   func (h *Handler) buildCountForPart(ctx context.Context, partID int) (int, error) {
   	return h.inventory().CountPartBuilds(ctx, partID)
   }
   ```
5. `arx_go/parts.go` `PartDetail`:
   - After the units block (~L278): `var buildCount int; if p.ShowBuild() { buildCount, _ = h.buildCountForPart(r.Context(), p.ID) }`.
   - Add `"BuildCount": buildCount,` to the render map (~L311).
   - `LotCount`/`UnitCount` are already set only when `ShowLots`/`ShowUnits`; reuse them.
6. `arx_go/templates/parts/part_detail.html`: insert a new card immediately after the Inventory card (after L130), wrapped in `{{if or .Part.ShowLots .Part.ShowUnits .Part.ShowBuild}}`. Same markup pattern as sibling cards (`col` > `card h-100` > `card-header` with `<span>Traceability</span>` > `card-body`). Body rows use `detail-row`:
   - `{{if .Part.ShowLots}}<div class="detail-row"><strong>Lots:</strong><span><a href="/lots?f.col-pn={{.Part.PartNumber}}">{{.LotCount}}</a></span></div>{{end}}`
   - Units: same with `.Part.ShowUnits`, `/units?f.col-pn=...`, `.UnitCount`.
   - Builds: same with `.Part.ShowBuild`, `/builds?f.col-pn=...`, `.BuildCount`.
   - html/template URL-escapes `{{.Part.PartNumber}}` in the query position; no manual `urlquery`.
7. `CHANGELOG.md`: new `## [0.8.x]` entry (next patch, today's date) under `### Added`: "Part Summary Traceability card with lot/unit/build counts linking to filtered lists ([#417](https://github.com/Jolls/arx/issues/417))".
8. No SQL DDL, seed, SCHEMA.md, migration, or app.js change. No RELEASE_NOTES change unless a release ships.

Files touched (exact): `internal/inventory/inventory.sql`, `internal/dbq/*` (generated), `internal/inventory/inventory.go`, `arx_go/lot.go`, `arx_go/parts.go`, `arx_go/templates/parts/part_detail.html`, `CHANGELOG.md`, plus tests below (`arx_go/part_traceability_test.go`, `arx_go/part_traceability_integration_test.go`).

## Test plan

### 1) Coverage audit (existing tests on touched code)
- `PartDetail`: arx_go/part_orders_integration_test.go `TestIntegration_PartDetail_AttachmentsAndPrice` (renders `/part/{id}/summary`, section-slices the body); arx_go/part_detail_integration_test.go (render at L160); arx_go/categories_integration_test.go (render at L366); arx_go/part_summary_details_integration_test.go.
- `lotCountForPart`/`unitCountForPart`/`recentPartLots`/`recentPartUnits`: arx_go/inventory_integration_test.go `TestIntegration_InventoryPartCards` (3007 -> 2 lots; 3005 -> 2 units).
- `CountPartBuilds` / `buildCountForPart`: none (new).
- Model flags `ShowLots/ShowUnits/ShowBuild`: `ShowInventory` covered in arx_go/models/category_test.go `TestShowInventory`; `ShowLots`/`ShowUnits`/`ShowBuild`/`TracksLots`/`TracksSerials` have no direct test found (verify with grep before implementing; characterize if absent).
- Filter-from-URL / substring: JS only, no automated test (manual). List pages: arx_go/filter_row_test.go `TestFilterTable_NewPagesHaveFilterRow`, `TestRowBuildersCoverListEndpoints`; arx_go/integration_test.go ~L4552-4580 (AllLots/AllUnitsRows/AllBuildsRows content); arx_go/popout_integration_test.go L220-221.
- part_detail.html unit render: no template-only test exists.

### 2) Characterization tests (must pass on unchanged code)
In `arx_go/part_traceability_test.go` (no DB):
- `TestPartShowFlags_Characterize`: table over `TrackingMode` none/lot/serial/lot_serial: `ShowLots` true for lot, lot_serial; `ShowUnits` true for serial, lot_serial; `ShowBuild` true only when `HasBOM && Tabs.Inventory`.
In `arx_go/part_traceability_integration_test.go` (`//go:build integration`, `liveHandler`):
- `TestIntegration_PartSummary_LotsUnitsCards_Characterize`: GET `/part/3013/summary`; body contains `Total lots:` and `Total units:` (existing cards stay unless Open question 2 says otherwise); GET `/part/3012/summary` contains `Total lots:` and not `Total units:`.
- Existing `TestIntegration_InventoryPartCards` already pins the lot/unit counts.

### 3) Red tests (fail today; stubs compile)
Stub for compile: in lot.go add `buildCountForPart` returning `0, nil` first if writing tests before implementation.
Integration (`arx_go/part_traceability_integration_test.go`), helper `traceCard(body)` slices from `<span>Traceability</span>` to the card's closing; helper `getSummary(id)` as in part_orders_integration_test.go L231-236:
- `TestIntegration_PartTraceabilityCard_LotSerial`: part 3013 shows all three. Asserts card contains `href="/lots?f.col-pn=ASM-1003"` with count 1, `/units?f.col-pn=ASM-1003` count 1, `/builds?f.col-pn=ASM-1003` count 1. Fails today: no card.
- `TestIntegration_PartTraceabilityCard_LotOnly`: part 3007 (lot, no BOM): card has `/lots?...RAW-1002` with count 2; no `/units?` and no `/builds?` link (3007 has 1 unit row 8502 but is not serial-tracked, so units must be hidden). Fails today: no card.
- `TestIntegration_PartTraceabilityCard_SerialBuilt`: part 3005 (serial, BOM): `/units?f.col-pn=ASM-1001` count 2, `/builds?...` count 1, no `/lots?` link. Fails today.
- `TestIntegration_PartTraceabilityCard_Hidden`: a part with none of the flags (e.g. seed BUY part 3002, tracking none, no BOM): body has no `<span>Traceability</span>`. Passes today (vacuous); keep as guard.
- `TestIntegration_PartTraceability_CountsMatchLists`: for 3013, 3007, 3005 parse each card count and assert it equals the number of rows in `/api/lots/rows` | `/api/units/rows` | `/api/builds/rows` JSON whose `pn` equals the part number (exact match, mirrors "counts match the listed rows"). Fails today: no card to parse.
- `TestIntegration_CountPartBuilds`: `h.inventory().CountPartBuilds(ctx, 3005)==1`, `3012==1`, `3013==1`, `3002==0`. Fails today: method undefined (compile stub returns 0 -> assertion fails).
Unit (no DB), `arx_go/part_traceability_test.go`:
- `TestPartDetailTemplate_TraceabilityLinkEscapes`: execute the card (or full part_detail.html via the existing parts-template harness in templates_parse_test.go `renderPartsList` pattern) with `PartNumber: "A&B 1"`; expect `f.col-pn=A%26B%201` in the href. Fails today: no card.

### 4) Manual-only
- Click each count: list opens with Part Number filter pre-filled and only that part's rows; the pagination "Total matching" equals the card count.
- Edit/clear the filter in the opened list: rows update; clearing shows all rows; Back from a row restores view.
- Part whose number is a substring of another's (see Open question 1): verify list vs count.
- Card layout beside Inventory at phone and desktop widths (Bootstrap grid); dark mode.
- Part with `*`, `?`, `;` or leading `!` in its number (see Open question 1).

### Seed data
No new seed rows needed: 3013 (ASM-1003: lot 8306, unit 8501, build 8203), 3007 (RAW-1002: lots 8301/8303, unit 8502 unreachable via card), 3005 (ASM-1001: units 8503/8504, build 8201), 3012 (ASM-1002: lot 8302, build 8202), 3002 (no lots/units/builds) already exist. If Open question 1 resolves to "need a substring-collision part", add to `seed_test_data.sql` (plus SCHEMA/seed sync rules) a part e.g. `(id 3099, 'ASM-1001-X', ...)` with one lot; confirm id range/columns at that time.

## Open questions
1. Exact vs substring: the list filter is a case-insensitive substring (glob only with `*`/`?`; `;` and leading `!` are operators). Linking `?f.col-pn=ASM-1001` will also show `ASM-1001-X`/`XASM-1001` rows, and part numbers containing `*`, `?`, `;` or leading `!` filter wrongly, so card count and list rows can differ, which breaks the "counts match" criterion. Options: (a) accept substring behavior, (b) add an exact-match syntax to the shared filter engine (app.js; out of the stated scope), (c) link with the whole-field glob form (e.g. escape-free `ASM-1001` cannot do this without a wildcard). Which?
2. Existing Lots (L182) and Units (L213) cards on the Summary already show `Total lots`/`Total units` with "View all" to the per-part tabs. Keep them as is alongside the new card (plan assumes yes, duplicate counts), or remove/merge them?
3. Card placement: "beside Inventory" is implemented as immediately after the Inventory card in the same `row-cols` grid (it wraps per breakpoint, not strictly adjacent). OK?
4. Builds shown when `ShowBuild()` (HasBOM && stockable). A part with historical builds but no BOM now would show no builds link; acceptable, or should it show when `BuildCount > 0`?

## Resolved decisions (user, 2026-10-30)
- Accept the list's substring match for the Part Number filter; no filter-engine change.
- Keep the existing Lots and Units summary cards unchanged; add the Traceability card directly after Inventory in the same grid.
- Builds count/link follows ShowBuild() only.
