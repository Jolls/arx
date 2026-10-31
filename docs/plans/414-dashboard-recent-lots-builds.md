# #414 Dashboard: Recent Lots and Recent Builds cards

Branch: `feature/414-dashboard-recent-lots-builds`

## Findings (verified in code)
- `ReportsDashboard` (`arx_go/reports.go:65`) calls one `h.dashboardX(ctx, 5)` per card, each `renderError`s on failure, then sets `data["X"]`; template `arx_go/templates/reports/dashboard.html` renders cards in `row row-cols-1 row-cols-md-2 g-3 mb-3` rows (card = `card h-100` > `card-header` + `card-body` > `table-responsive-sm` > `table table-sm table-hover mb-0`; empty = `<p class="text-muted mb-0">...</p>`). Last two rows are full-width: Below Reorder Point (`row-cols-1`), Recent Activity (`row-cols-1`, no `mb-3`).
- Lots: `inventory.Service.ListAllLots` -> `listLots(ctx, nil, nil)` -> sqlc `ListLots` (`internal/inventory/inventory.sql:73`), which already takes nullable `n` LIMIT and orders `created_at DESC, id DESC`. `ListRecentPartLots` passes `(&partID,&n)`. No new SQL needed for lots; only a service method `listLots(ctx, nil, &n)`. `LotRow` has `ID, LotNumber, PartID, PartNumber, PartDescription, CreatedAt`.
- Lot page route: `GET /part/{partID}/lots/{lotID}` (`PartLotTrace`, main.go:310). Existing link convention (`part_lots.html:34`): `href="/part/{{partID}}/lots/{{id}}"` + `data-popout-url="/popout/part/{{partID}}/lots/{{id}}/consumers"`.
- Builds: table `build` has NO status column (cols: id, part_id, output_lot_id, qty, build_date, username, note, created_at) and no name/identifier; UI identifier is the integer id (`Build #<id>`, lot_description "Build #<id>"). There is NO standalone build page: only `GET /popout/build/{id}` (`PopoutBuild`) and, in `app.js:232`, the All Builds row links `href="/part/{partId}/lots/{outLotId}"` (if output lot) else `/part/{partId}/build`, with `data-popout-url="/popout/build/{id}"`.
- Builds list: only `ListAllBuilds` (unbounded, `inventory.sql:270`); no limited variant -> new sqlc query needed.
- Existing tests: `TestIntegration_ReportsDashboard_RendersAllCards` (`arx_go/integration_test.go:1451`) asserts card header strings in the body. Seed already has lots 8301/8302/8303/8306 and builds 8201/8202/8203 (`SQL/seed_test_data.sql:473-515`); no new seed rows needed.

## File changes

### 1. `internal/inventory/inventory.sql`
Add after `ListAllBuilds` (line ~277):
```sql
-- name: ListRecentBuilds :many
-- Dashboard Recent Builds card (#414): n newest, same ordering/shape as ListAllBuilds.
SELECT b.id, b.part_id, COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       b.qty, b.build_date, b.output_lot_id, COALESCE(l.lot_number, '') AS lot_number, COALESCE(b.note, '') AS note
FROM build b
JOIN part p ON p.id = b.part_id
LEFT JOIN lot l ON l.id = b.output_lot_id
ORDER BY b.build_date DESC, b.id DESC
LIMIT sqlc.arg(row_limit)::int;
```
Then run `sqlc generate` (repo root); commit `internal/dbq` changes (generated `ListRecentBuildsRow` is expected to have identical fields to `ListAllBuildsRow`, so the positional conversion in step 2 works; if the field order differs, adjust).

### 2. `internal/inventory/inventory.go`
- After `ListAllLots` (line ~221) add:
```go
// ListRecentLots returns the n newest lots of every part.
func (s *Service) ListRecentLots(ctx context.Context, n int) ([]LotRow, error) {
	return s.listLots(ctx, nil, &n)
}
```
- After `ListAllBuilds` (line ~627-640) add `ListRecentBuilds(ctx, n int) ([]BuildListRow, error)` mirroring `ListAllBuilds` body but calling `s.q.ListRecentBuilds(ctx, n)` and the same `BuildListRow{r.ID, r.PartID, ...}` conversion.

### 3. `arx_go/reports.go`
- Add after `dashboardBelowReorderParts` (line ~197):
```go
// dashboardRecentLots lists the newest lots across all parts (issue #414).
func (h *Handler) dashboardRecentLots(ctx context.Context, limit int) ([]LotRow, error) {
	return h.inventory().ListRecentLots(ctx, limit)
}

// dashboardRecentBuilds lists the newest builds across all parts (issue #414).
func (h *Handler) dashboardRecentBuilds(ctx context.Context, limit int) ([]inventory.BuildListRow, error) {
	return h.inventory().ListRecentBuilds(ctx, limit)
}
```
- Add import `"arx/internal/inventory"` to the import block.
- In `ReportsDashboard`, after the `belowReorderParts` block (line ~111) add two blocks in the existing pattern (`recentLots`, `recentBuilds`, limit 5, `renderError(w, r, "Error loading dashboard: "+err.Error())`), and set `data["RecentLots"] = recentLots`, `data["RecentBuilds"] = recentBuilds`.

### 4. `arx_go/templates/reports/dashboard.html`
Insert a new `<div class="row row-cols-1 row-cols-md-2 g-3 mb-3">` with two `col` cards between the Below Reorder Point row (ends line 149) and the Recent Activity row (line 151):
- Card header `Recent Lots`. If `.RecentLots`: table with `<thead><tr><th>Lot</th><th>Part</th><th class="text-end">Created</th></tr></thead>`; each row: `<td><a href="/part/{{.PartID}}/lots/{{.ID}}" data-popout-url="/popout/part/{{.PartID}}/lots/{{.ID}}/consumers">{{.LotNumber}}</a></td>`, `<td>{{.PartNumber}}</td>`, `<td class="text-end text-muted" style="white-space:nowrap;">{{.CreatedAt.Format "2006-01-02"}}</td>`. Else `<p class="text-muted mb-0">No lots yet.</p>`.
- Card header `Recent Builds`. If `.RecentBuilds`: table, columns per Open question 1; Build cell: `<a href="/part/{{.PartID}}/{{if .OutputLotID}}lots/{{.OutputLotIDVal}}{{else}}build{{end}}" data-popout-url="/popout/build/{{.ID}}">Build #{{.ID}}</a>`; Part cell `{{.PartNumber}}`. Else `<p class="text-muted mb-0">No builds yet.</p>`.
- `CreatedAt` is `time.Time` stored UTC; formatting in the template ignores the user's zone. Matching the zone requires pre-formatting in Go (see Open question 2).

### 5. `arx_go/integration_test.go`
Add `"Recent Lots"`, `"Recent Builds"` to the `want` list in `TestIntegration_ReportsDashboard_RendersAllCards`, plus new tests below.

### 6. `CHANGELOG.md`
New top `## [0.8.x]` entry (next patch) under `### Added`: `- Recent Lots and Recent Builds cards on the Reports dashboard ([#414](https://github.com/Jolls/arx/issues/414))`.

Files touched: `internal/inventory/inventory.sql`, `internal/dbq/*` (generated), `internal/inventory/inventory.go`, `arx_go/reports.go`, `arx_go/templates/reports/dashboard.html`, `arx_go/integration_test.go`, `CHANGELOG.md`. No SQL/DDL, seed, migration, SCHEMA.md, or RELEASE_NOTES change.

Latency: two added queries (one per card), no extra round trips; both `LIMIT`ed.

## Test plan

### 1) Coverage audit
- `TestIntegration_ReportsDashboard_RendersAllCards` (`arx_go/integration_test.go:1451`): covers `ReportsDashboard` assembly/template render (header strings only).
- `TestIntegration_DashboardRecentActivity`, `_StaleWIPRecords`, `_PendingApprovalPOs`, `_BelowReorderParts` (`reports_integration_test.go:294`, `integration_test.go:1354/1389/1417`): per-card query tests; pattern to follow.
- Lots/builds lists: `integration_test.go:~4546-4580` (`AllLotsRows`, `AllBuildsRows`; ids 8201-8203) and `popout_integration_test.go:221`. These cover `ListAllLots`/`ListAllBuilds`; no test covers a limited lots/builds query (`ListRecentPartLots` is covered via `part_orders_integration_test.go` `TestIntegration_PartDashboardCards`).

### 2) Characterization tests
None to add: `RendersAllCards` already pins the current cards; it stays green throughout (new headers are only added after the red phase).

### 3) Red tests (add in `arx_go/integration_test.go`; stubs: add `dashboardRecentLots`/`dashboardRecentBuilds` in `reports.go` returning `nil, nil`, and `ListRecentLots`/`ListRecentBuilds` stubs, so tests compile and fail on assertions)
- `TestIntegration_DashboardRecentLots`: `items, _ := h.dashboardRecentLots(ctx, 2)`; assert `len==2`, items[0].ID==8306 and items[1].ID==8302 (seed created_at 2026-05-30, 05-25; newest first), `PartNumber` non-empty, and that `dashboardRecentLots(ctx, 50)` returns >= 4. Fails today: stub returns nil.
- `TestIntegration_DashboardRecentBuilds`: `h.dashboardRecentBuilds(ctx, 2)`; assert `len==2`, items[0].ID==8203 (2026-05-30), items[1].ID==8202 vs 8201 tie-break by id DESC (8202 first), `PartNumber` non-empty. Fails today: stub returns nil.
- `TestIntegration_ReportsDashboard_RendersAllCards` (extended): assert body contains `Recent Lots`, `Recent Builds`, `/part/3013/lots/8306`, `/popout/build/8203`. Fails today: template lacks the cards.
- Empty state (template only, no DB): execute the dashboard template with empty `RecentLots`/`RecentBuilds` and assert `No lots yet.` / `No builds yet.`; place in an existing template-rendering unit test file if one renders `reports/dashboard.html` (none found in this audit; if none exists, skip and cover under manual).

### 4) Manual-only
- /reports shows both cards below Below Reorder Point, styled like neighbors, stacked on narrow width.
- Lot number link opens the lot trace page; build link opens the popout (and navigates to the output lot page, or the part's build page when no lot).
- On a DB with zero lots/builds both cards show their empty text.
- Created date near midnight matches the user's zone (see Open question 2).

## Open questions
1. Build "status": `build` has no status column and no builds have a lifecycle state (no state anywhere in `SQL/build.sql`). Which third column for Recent Builds: (a) build date, (b) qty, (c) add a new status column (schema change, out of scope)? Plan assumes (a) Build date, formatted `YYYY-MM-DD`.
2. Date zone: `build_date` is a DATE (no zone issue); lot `created_at` is a timestamptz. Pre-format lot created date in Go using the user's zone (as `lotListRows` does via `localTime(loc, ...)`) by introducing a small view struct in `reports.go`, or is plain `.CreatedAt.Format` in the template acceptable? Plan currently assumes the Go pre-format is preferred if answered "yes"; otherwise template format as written.
3. Build "identifier": plan uses `Build #<id>` (matches existing UI) since there is no build number/name column. Confirm.
4. Link target for builds: there is no `/build/{id}` page; plan uses the All Builds convention (output-lot trace page or `/part/{id}/build`, with the popout). Confirm, or should a dedicated page be added?

## Resolved decisions (user, 2026-10-30)
- Recent Builds third column: build date (no status column exists). Build identifier shown as `Build #<id>`.
- Build link target: output lot trace page, else part Build tab (same as the /builds list convention); no new build page.
- Lot created date: pre-format in Go in the user's zone, as lotListRows does.
