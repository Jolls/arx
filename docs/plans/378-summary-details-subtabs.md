# #378 Parts: Summary (dashboard) + Details (all fields) subtabs

Branch: `feature/378-summary-details-subtabs`. No schema change, no migration, no seed change.

## Current state (verified)
- `arx_go/main.go:275-276`: `/part/{id}` and `/part/{id}/details` both -> `h.PartDetail`.
- `arx_go/parts.go:190` `PartDetail`: renders `parts/part_detail.html` with `"ActiveSubTab": "details"` (line 301). Redirects to `/part/{id}/bom` only when `p.HasBOM` and `r.URL.Path == "/part/{id}"` (line 203); `/details` never redirected.
- `arx_go/templates/shared/partials.html` `part_tabs`: line 216 `<a href="/part/{{$id}}/details" ... eq $sub "details">Details</a>`; lines 262-264 show Duplicate + Edit when `eq $sub "details"`.
- `part_edit.html` is rendered with `ActiveSubTab: "edit"`; its tab bar shows no active tab and no Edit/Duplicate buttons (unchanged).
- Category visibility is via `h.applyCategoryTabs` + `Part.Show*()`; tab bar already gates on it. New Details handler must call `applyCategoryTabs` too.

## Part column coverage (`part` table vs `models.Part` via `fetchPartFull`/`GetPart`)
Already exposed (query `GetPart`, `internal/parts/parts.sql:174`, `parts.Part`, `fetchPartFull` parts.go:551): id, part_number, category, revision, description, detail, release_status, requested_by, notes, user_field_1..10, created_date, last_rollup_cost, last_rollup_at, attachment_count, primary_attachment_id, current_cost, is_active, po_line_count, modified_date, uom_id (+ abbreviation `UnitAbbr`), stock_on_hand, reorder_min, tracking_mode.

Missing (must add):
1. `price_id` - not in `GetPart`, `parts.Part`, or `models.Part`.
2. `default_supplier_id` - `models.Part.DefaultSupplierID` exists but `GetPart`/`parts.Part`/`fetchPartFull` do not fill it.
3. Supplier name for `default_supplier_id` (needs `company.name` join; no existing field).

## File changes

### 1. `internal/parts/parts.sql` - `GetPart`
- Add `p.price_id, p.default_supplier_id, COALESCE(c.name, '') AS default_supplier_name` to the select list.
- Add `LEFT JOIN company c ON c.id = p.default_supplier_id` after the existing `LEFT JOIN uom u`.
- Run `sqlc generate` at repo root; commit regenerated `internal/dbq` (never hand-edit). Nullable int columns map to `*int` per `sqlc.yaml` overrides.

### 2. `internal/parts/parts.go`
- `Part` struct (line 148): add `PriceID *int`, `DefaultSupplierID *int`, `DefaultSupplierName string`.
- Find the `GetPart` mapping function in this file (the one that builds `Part` from the `dbq.GetPartRow`) and map the three new fields. `parts.Part` is also the create/update input (`partInput`); the new fields are ignored by CreatePart/UpdatePart (do not touch those queries).

### 3. `arx_go/models/part.go`
- `Part` struct: add `PriceID *int` and `DefaultSupplierName string` (`DefaultSupplierID` already exists).

### 4. `arx_go/parts.go`
- `fetchPartFull` (line 560): add `DefaultSupplierID: p.DefaultSupplierID, DefaultSupplierName: p.DefaultSupplierName, PriceID: p.PriceID` to the returned `models.Part`.
- `PartDetail` (line 188 comment, line 301): comment -> `GET /part/{id} and /part/{id}/summary`; `"ActiveSubTab": "details"` -> `"summary"`. Redirect guard on line 203 unchanged (`/part/{id}` only; `/summary` does not redirect to BOM).
- Add `func (h *Handler) PartDetails(w, r)` (GET `/part/{id}/details`), modeled on `PartEdit` (parts.go ~405-440): 
  - `partPageBase(w, r, id, "details")` for basic part/back URL/label (same call pattern PartEdit uses); `fetchPartFull` for `full`; `h.applyCategoryTabs(ctx, &full)`; `full.ThumbnailURL = p.ThumbnailURL`.
  - Render `parts/part_details.html` with `"Part": full, "ActiveTab": "parts", "ActiveSubTab": "details", "NavBackURL", "NavBackLabel", "TestMode": h.cfg().TestMode`.
  - Error handling identical to `PartDetail` (`sql.ErrNoRows` -> `renderError(w, r, "Part not found")`, other -> `"Error retrieving part: "+err.Error()`).
- `PartsCreate` redirect (line 396) and `PartUpdate` redirect (line 483): target left as-is unless open question 1 is answered otherwise.

### 5. `arx_go/main.go`
- Line 275 stays: `/part/{id}` -> `h.PartDetail`.
- Insert `b.handle(http.MethodGet, "/part/{id}/summary", withAuth, h.PartDetail)`.
- Line 276: `/part/{id}/details` -> `h.PartDetails`.

### 6. `arx_go/templates/shared/partials.html` (`part_tabs`)
- Line 216: replace with two links in order: `<a href="/part/{{$id}}/summary" class="sub-tab{{if eq $sub "summary"}} active{{end}}">Summary</a>` then `<a href="/part/{{$id}}/details" class="sub-tab{{if eq $sub "details"}} active{{end}}">Details</a>`.
- Lines 262-264: keep `{{if eq $sub "details"}}` Duplicate/Edit block as is (now Details-only; Summary has none).
- Header comment at line ~271 and `models/part.go` comment ("Universal tabs (Details, ...)") : change "Details" -> "Summary, Details" (comment text only).

### 7. `arx_go/templates/parts/part_details.html` (new)
- `{{define "content"}}`: `{{template "part_breadcrumb" .}}`, `<div class="content">`, `{{template "part_tabs" .}}`, then one Bootstrap card ("All fields") containing `detail-row` rows (same markup as `part_detail.html` Reference card: `<div class="detail-row"><strong>Label:</strong><span>value</span></div>`). Empty/NULL values render `—`.
- Rows, in this order (label: source):
  ID: `.Part.ID`; Part Number; Category; Revision; Description; Detail; Release Status (U/A/D mapped to Under Review/Active/Deprecated as in `part_status_bar`); Active (Yes/No from `IsActive`); Requested By; Notes; User 1..User 10 (all ten, from `UserFieldsForEdit`: `.Label`/`.Value`); Unit (`UnitAbbr`, plus UoM id `UnitID`); Tracking Mode (`TrackingMode`); Stock On Hand (`StockOnHand`); Reorder Min (`ReorderMin`, nil -> `—`); Current Cost (`CurrentCost`); Last Rollup Cost (`LastRollupCost`); Last Rollup At (`LastRollupAt`); Created Date; Modified Date (both via `formatDate`); Attachment Count; PO Line Count; Primary Attachment ID (`PrimaryAttachmentID`); Default Supplier (`DefaultSupplierName`, plus id `DefaultSupplierID`); Price ID (`PriceID`).
- No category-based hiding of rows (all part columns always listed); category visibility is applied only through `part_tabs`.
- Register the new template wherever templates are embedded/listed: check `arx_go/templates_parse_test.go` and the template loader for a hard-coded list; embed glob `templates/parts/*` should pick it up (verify; add to list only if one exists).

### 8. `arx_go/templates/parts/part_detail.html`
- No content change (stays the dashboard). Confirm no Edit/Duplicate markup exists in it (none today; buttons come only from `part_tabs`).

### 9. Link/reference audit (every `/details` or `"details"` hit; part-scoped only)
| Location | Change |
|---|---|
| `arx_go/main.go:276` | handler -> `h.PartDetails` |
| `arx_go/parts.go:188` comment | update |
| `arx_go/parts.go:301` `ActiveSubTab: "details"` | -> `"summary"` |
| `arx_go/templates/shared/partials.html:216` | split into Summary + Details links |
| `arx_go/templates/shared/partials.html:262` `eq $sub "details"` | unchanged (Details-only) |
| `arx_go/part_detail_integration_test.go:160` (`TestIntegration_PartDetail_Renders`) | request path `/part/%d/details` -> `/part/%d/summary` (handler `h.PartDetail` unchanged) |
| `arx_go/part_orders_integration_test.go:233` (`TestIntegration_PartDetail_AttachmentsAndPrice`) | request path -> `/summary` |
| `arx_go/main.go:275` `/part/{id}`, `NavBackURL`/`setNavContext` `/part/%d` (parts.go:55,236), `reports.go:216`, `utilities.go:160`, all templates linking `/part/{{id}}` | no change; they land on Summary by default (`resourceBase` truncates to 3 segments so nav context is unaffected) |
| `NavBackURL` consumers (all subtab handlers) | no change; none reference `/details` |
| `suppliers.go:106`, `pos.go:303`, `partials.html:109,126` `"details"` | NOT part pages; do not touch |
| `<details>` HTML elements in templates | unrelated; do not touch |
| `CHANGELOG.md` historical `/details` text | do not touch |

No other `/part/{id}/details` links, redirects, or breadcrumbs exist in the repo (grepped `*.go`, `*.html`, `*.js`, `*.md`).

### 10. Docs
- `CHANGELOG.md`: new top `## [0.8.x]` entry (next patch; version auto-read by build): `### Changed` - Details tab renamed Summary at `/part/{id}/summary`; `### Added` - new Details tab listing every part field with Edit/Duplicate ([#378](https://github.com/Jolls/arx/issues/378)).
- `arx_go/RELEASE_NOTES.md`: no change (patch, not a user release).

## Test plan

All handler tests here are build-tag `integration` (`arx_go/*_integration_test.go`), run against the local container per CLAUDE.md (`ARX_TEST_DSN=...55432/ArxDev`). Pattern: `liveHandler(t)`, `httptest.NewRecorder`, `h.Handler(rec, withID(httptest.NewRequest(GET, path, nil), id))`, `assertStatus`, `strings.Contains` on body. Fixtures: `seedPartDetail(t, h)` (`part_detail_integration_test.go:26`) returns FULL/BARE parts; FULL is category BUY, tracking `lot`, uom 3, with BOM line, attachments.

### (1) Coverage audit - existing tests touching these handlers
- `part_detail_integration_test.go`: `TestIntegration_FetchPartFull_Fields` (fetchPartFull fields, pins edit-form fields), `TestIntegration_FetchPartBasic_Fields`, `TestIntegration_PartDetail_Renders` (PartDetail via `/part/%d/details`).
- `part_orders_integration_test.go`: `TestIntegration_PartDetail_AttachmentsAndPrice` (PartDetail via `/part/%d/details`).
- `categories_integration_test.go:366` (test containing the NULL-category check, call `h.PartDetail` on `/part/%d`).
- `popout_integration_test.go:151,152,229` (`h.PartDetail` for 3007, 3013 popout links).
- `integration_test.go:1242` (smoke table "part detail", seed 3005, `/part/{id}`; comment at 1239 notes BOM-redirect guard).
- `parts_integration_test.go` (`PartsCreate`/`PartUpdate` tracking mode, asserts 302 only).
- `handlers_test.go:185-188` (`resourceBase` of `/part/42...`).
- `templates_parse_test.go` (template parse; no part_tabs/part_detail assertions).
- No existing test asserts the tab bar contents, Summary/Details labels, or Edit/Duplicate links.

### (2) Characterization tests (pass on unchanged code; add first)
In `arx_go/part_detail_integration_test.go`:
- `TestIntegration_PartDetail_Characterization_Dashboard`: GET `h.PartDetail` for BARE (no BOM) at `/part/{id}` -> 200, body contains `<div class="card-header">Reference</div>`.
- `TestIntegration_PartDetail_Characterization_BOMRedirect`: GET `h.PartDetail` for FULL (has BOM) at `/part/{id}` -> 302 `Location: /part/{id}/bom`; at `/part/{id}/details` -> 200 (no redirect). (After the change the second assertion moves to `/summary`.)
- `TestIntegration_PartTabs_Characterization_EditDuplicateGated`: Details-page render of BARE contains `href="/part/{id}/edit"` and `href="/part/{id}/duplicate"`; GET `h.PartBOM` (or `PartAttachments`) page for the same part contains neither.
- `TestIntegration_PartTabs_Characterization_CategoryGating`: seed 3009/OPS-like gating: part 3006 (OPS, all procurement flags false) tab bar contains `<span class="sub-tab disabled" title="Order history does not apply` and no `/part/3006/pricing"` link.

### (3) Red tests (fail today; pass after change)
In `arx_go/part_detail_integration_test.go`:
- `TestIntegration_PartSummary_Route`: call `h.PartDetail` with path `/part/{id}/summary` for BARE; assert 200, body contains `class="sub-tab active">Summary</a>` and `href="/part/{id}/details"`; assert body does NOT contain `href="/part/{id}/edit"` or `/duplicate"`. Fails today: no Summary tab, active tab is Details, buttons present.
- `TestIntegration_PartSummary_NoRedirectForBOMPart`: FULL at `/part/{id}/summary` -> 200 and contains `<div class="card-header">Reference</div>`. Fails today: tab label/active state differ ("Summary" absent); asserts `Summary` active.
- `TestIntegration_PartDefaultLanding_IsSummary`: BARE at `/part/{id}` -> body contains `class="sub-tab active">Summary</a>`. Fails today (Details active).
- `TestIntegration_PartDetails_ListsEveryColumn`: `h.PartDetails` for FULL at `/part/{id}/details`; assert 200, `class="sub-tab active">Details</a>`, edit + duplicate links present, and each value appears: `full desc`, `full detail`, `C`, `JJ`, `full notes`, `u1`..`u10`, `2026-01-02`, `2026-03-04`, `2026-02-03`, `lot`, `2.50`, `4` (reorder), `1.25`, `3.5`, attachment count `7`, PO line count `4`, `mL` (uom 3), primary attachment id (`primaryAtt`), part id; the labels `Created Date`, `Modified Date`, `Last Rollup At`, `Stock On Hand`, `Attachment Count`, `PO Line Count`, `Price ID`, `Default Supplier`, `Active`. Fails today: `PartDetails` does not exist (compile) / route serves dashboard.
- `TestIntegration_PartDetails_BareShowsPlaceholders`: BARE -> 200, body contains `—` placeholders and all 10 `User N` labels (empty ones included). Fails today (no handler).
- `TestIntegration_PartDetails_ResolvesSupplierAndPrice`: after seeding FULL, `UPDATE part SET default_supplier_id=1002` (existing seed company) and `price_id=<id of a price row inserted for FULL with supplier 1002>`; assert body contains that company's name (read via `SELECT name FROM company WHERE id=1002` in the test) and the price id. Cleanup deletes the price row before the part (extend `seedPartDetail` cleanup or add own `t.Cleanup`; the part DELETE needs `UPDATE part SET price_id=NULL` first). Fails today: fields not fetched.
- `TestIntegration_FetchPartFull_SupplierPrice`: `fetchPartFull` returns `DefaultSupplierID`, `DefaultSupplierName`, `PriceID` for the FULL part after the update above; BARE returns nil/`""`. Fails today (fields not populated; compile error on new struct fields).
- `TestIntegration_PartDetails_NotFound`: id 0/nonexistent -> body contains `Part not found`. Fails today (no handler).
- `TestIntegration_PartDetails_CategoryTabsStillGated`: part 3006 (OPS) at `/details` -> disabled Order History/Pricing/Suppliers spans present, rows still all listed. Fails today (no handler).
Update `partEditFields` in `part_detail_integration_test.go` only if `deepEqualDec` comparison of the existing `TestIntegration_FetchPartFull_Fields` breaks from the new fields (it uses `partEditFields`, which copies selected fields only, so no change expected).

Seed data: no new rows. Uses existing company 1001/1002, uom 3 (FULL seed already sets it), parts 3006 (OPS), 3005 (ASM, has BOM). `price` row for FULL is inserted by the test itself and cleaned up by it.

### (4) Manual-only
- Open `/parts` -> click a part with no BOM: lands on Summary, tab bar order Summary, Details, ...; Summary shows no Edit/Duplicate.
- Click Details: all fields listed; Edit opens `/part/{id}/edit`; Duplicate opens `/part/{id}/duplicate`; Save on edit returns per open question 1.
- Part with a BOM: `/part/{id}` still redirects to BOM; click Summary tab shows dashboard (no redirect).
- Visual check of Details card layout at desktop and phone width (Bootstrap/`detail-row`).
- Back button / breadcrumb from Summary and Details return to the Parts list.
- Visit old bookmark `/part/{id}/details`: shows new Details page (expected).

## Resolved decisions (override anything above that conflicts)
1. **Post-save redirects -> Details.** `PartsCreate` (parts.go:396) and `PartUpdate` (parts.go:483) redirect to `/part/{id}/details` instead of `/part/{id}`. Update any test asserting the old `Location`; manual-only "Save on edit" bullet = lands on Details.
2. **Price shown with supplier name + price_ea.** `GetPart` also gets `LEFT JOIN price pr ON pr.id = p.price_id LEFT JOIN company pc ON pc.id = pr.supplier_id`, selecting `pr.price_ea` (nullable -> pointer per sqlc overrides) and `COALESCE(pc.name, '') AS price_supplier_name`. Add `PricePerEa *float64`-style field (match the type convention of `CurrentCost`/`ReorderMin` in `parts.Part`) and `PriceSupplierName string` to `parts.Part` and `models.Part`; map in `fetchPartFull`. Details row "Price" renders `<supplier name> - $<price_ea %.4f> (ID <price_id>)`, `—` when `price_id` is NULL. `TestIntegration_PartDetails_ResolvesSupplierAndPrice` additionally asserts the price_ea and price supplier name appear.
3. **No status bar** on Details (as planned).
4. **All 10 user fields shown, empty as `—`** (as planned).
5. **`/part/{id}/summary` never redirects to BOM** (as planned).

## Verify
`go build ./... && go vet ./... && go test ./...`; `sqlc generate` leaves no diff after commit; integration run per CLAUDE.md on local container.
