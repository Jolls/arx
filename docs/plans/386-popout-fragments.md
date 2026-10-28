# Plan: #386 popouts, new stock/PO/supplier/contact fragments

Part of #368. No schema change (no migration, no seed change, no `SQL/*.sql` edit). No new permissions: popout routes use `withAuth` only, same as the three existing popout routes (`arx_go/main.go:314-316`). Not touching `RELEASE_NOTES.md` (per-release only).

## Current code (verified)

- Popout JS: `arx_go/static/shared/app.js:1274-1348` (IIFE). `var cache = {}` keyed by URL holds the HTML string forever (per page load); `show(el)` line 1312 `if (cache[url] !== undefined) { render(cache[url]); return }`, fetch success sets `cache[url] = html` (line 1315). Trigger is any element with `data-popout-url`; nearest trigger wins (`closest`), so a badge with its own `data-popout-url` inside a cell with another is fine.
- Render: `h.renderPopout(w, name, data)` at `arx_go/handlers.go:642` executes a named template from the `"popouts"` set. That set is parsed from `templates/shared/popouts.html` alone with core funcs (`arx_go/templates.go:51-55`); `shared/partials.html` is NOT in it, so `{{template "po_status_badge"}}` is unavailable inside a popout. Core funcs available: `formatDate`, `printf`, `deref`, `derefInt`, `nonZero`, `isPos`, etc. (`handlers.go:893-1001`).
- Existing handlers: `arx_go/api.go:157-207` (`PopoutLotConsumers`, `PopoutUnitConsumers`, `PopoutPartWhereUsed`); routes `arx_go/main.go:314-316`. `PopoutPartWhereUsed` uses `h.parts().ListWhereUsed` + `h.fetchPartBasic` (thumbnail from `models.Part.ThumbnailURL`, stock from `StockOnHand`).
- Existing services to reuse: `parts.Service.GetPreferredSupplier` (`internal/parts/parts.go:891`, returns `sql.ErrNoRows` when none), `PreferredSupplierPrice` (`:899`, nil when none); `purchasing.Service.GetSupplier` (fields `Name, ContactName, ContactPhone, ContactEmail`, `internal/purchasing/purchasing.go:35-56`), `GetPO(number)` (`:436`), `ListPOLines(number)` (`:462`, `POLine{LineNumber, PartNumberSnapshot, Description, Qty, ReceivedQty, PartID}`); `contacts.Service.Get(id)` (fields `DisplayName, Email, Phone1, CompanyID, SupplierName`) and `POs(contactID)` (`PO{Number, Status, Role, Counterparty, DateOrdered, Total}`, `internal/contacts/contacts.go:119`). Handler accessors: `h.parts()`, `h.inventory()`, `h.purchasing()` (`arx_go/suppliers.go:111`), `h.contacts()` (`arx_go/contacts.go:16`).
- PO statuses (`SQL/purchase_order.sql:65`): `rfq, draft, open, sent, partially_received, closed, cancelled`; labels in `poStatusLabels` (`arx_go/pos.go:1136`). PO routes are keyed by PO number string (`/po/{id}` where id is the number, e.g. `5006R1`).
- No stock "location" concept exists anywhere in `SQL/*.sql` (grep `location` = 0 hits). Stock by lot = `SUM(inventory_transaction.qty) GROUP BY lot_id`; ledger rows with `lot_id IS NULL` are part-level.
- Seed facts (SQL/seed_test_data.sql): part 3007 RAW-1002 stock_on_hand 16, reorder_min 25, lot-tracked; ledger 5901 (+20, lot_id 8301, line 5504), 5902 (-5), 5903 (-1), 5904 (+2) with NULL lot_id; lot 8303 has no ledger rows. PO 5003 `partially_received`, total 205.00, one line 5504 (3007, qty 50, received 20, `lead_time_days` 50, PO `date_ordered` 2026-04-01, `date_requested` NULL). PO 5002 `open`, total 72.80, lines 5502 (3001 qty 20) + 5503 (3002 qty 200), both received 0, supplier contact 2003. Company 1002 (default contact 2003 Bob Lee, 555-0201, bob.lee@precisionmachining.test): POs 5002 open, 5005 cancelled, 5007 closed, 5008 draft (date_ordered 2026-06-01), 5009 draft, 5011 rfq. Part 3002 BUY-1001: default supplier 1002, supplier_part 4002 `PMC-M3X8`, min active price 0.03, stock 0, used in 3005. Contact 2003 employer = company 1002.

## Decisions fixed by this plan (see Open questions for the ones needing confirmation)

- "Open PO" status set, used everywhere below: `status IN ('open','sent','partially_received')`.
- Popout cache TTL: 45000 ms constant.
- New endpoints (all `GET`, `withAuth`):
  - `/popout/part/{id}/below-min`
  - `/popout/part/{id}/stock`
  - `/popout/part/{id}/bom-component`
  - `/popout/supplier/{id}`
  - `/popout/po/{id}` (id = PO number)
  - `/popout/contact/{id}`
- Invalid numeric id: `http.NotFound` (matches existing handlers). Missing row (`sql.ErrNoRows`): `http.NotFound`. Other errors: `serverError(w, "database error", err)`.
- Lists in popouts capped at 5 rows (lots/PO lines: 5; "and more" line when truncated).
- Popout links use the same hrefs as the rest of the app (`/part/{id}`, `/po/{number}`, `/supplier/{id}`, `/contact/{id}`, `/part/{id}/lots/{lotID}`).

## Changes

### 1. Popout cache TTL (do first) - `arx_go/static/shared/app.js`
- In the popout IIFE add `var TTL_MS = 45000` next to `SHOW_DELAY`/`HIDE_DELAY` (line 1281).
- Change cache entries from the HTML string to `{ html: <string>, at: Date.now() }`.
- `show()`: use the cached entry only if `cache[url] !== undefined && Date.now() - cache[url].at < TTL_MS`; otherwise fall through to `fetch`. On success store `{html, at: Date.now()}` and `render(html)`.
- Update the comment at lines 1274-1279 ("cached per URL" -> "cached per URL for 45 s").
- Applies to all popouts (existing "Used in"/"Consumed into" too); no per-URL override.

### 2. SQL + services
`internal/inventory/inventory.sql` (after `ListPartLedger`, before Lots section):
- `-- name: ListPartLotStock :many`: per active lot of a part with non-zero ledger balance, newest first.
  `SELECT l.id, l.lot_number, SUM(it.qty)::numeric AS qty FROM lot l JOIN inventory_transaction it ON it.lot_id = l.id WHERE l.part_id = sqlc.arg(part_id)::int AND l.is_active = TRUE GROUP BY l.id, l.lot_number, l.created_at HAVING SUM(it.qty) <> 0 ORDER BY l.created_at DESC, l.id DESC;`
- `-- name: GetPartUnlottedQty :one`: `SELECT COALESCE(SUM(qty), 0)::numeric AS qty FROM inventory_transaction WHERE part_id = sqlc.arg(part_id)::int AND lot_id IS NULL;`
- `internal/inventory/inventory.go`: add `LotStock{ID int; LotNumber string; Qty decimal.Decimal}` and `func (s *Service) PartLotStock(ctx, partID int) (lots []LotStock, unlotted decimal.Decimal, err error)` wrapping both queries.

`internal/parts/parts.sql` (after `ListPartOrders`):
- `-- name: ListPartOpenPOLines :many`: open PO lines with remaining qty, plus the total on order in every row.
  Columns: `po.number`, `COALESCE(po.supplier_name,'')`, `COALESCE(po.status,'')`, `pol.qty`, `pol.received_qty`, `(pol.qty - pol.received_qty)::numeric AS remaining`, `po.date_ordered`, `po.date_requested`, `pol.lead_time_days`, `SUM(pol.qty - pol.received_qty) OVER ()::numeric AS on_order_total`.
  `FROM po_line pol JOIN purchase_order po ON pol.po_id = po.id WHERE pol.part_id = sqlc.arg(part_id)::int AND po.status IN ('open','sent','partially_received') AND pol.received_qty < pol.qty ORDER BY COALESCE(po.date_requested, po.date_ordered + pol.lead_time_days) NULLS LAST, po.id, pol.line_number LIMIT sqlc.arg(n)::int;`
  (Expected-date source is Open question 2; this line changes if the answer differs.)
- `internal/parts/parts.go`: add `OpenPOLine{PONumber, SupplierName, Status string; Remaining decimal.Decimal; Expected *time.Time}` + `func (s *Service) ListOpenPOLines(ctx, partID, n int) (lines []OpenPOLine, onOrder decimal.Decimal, err error)`; `onOrder` = `on_order_total` of the first row (zero when no rows). `Expected` computed per Open question 2.

`internal/purchasing/purchasing.sql` (after `ListSupplierPOs`):
- `-- name: GetSupplierPOSummary :one`: `SELECT (COUNT(*) FILTER (WHERE status IN ('open','sent','partially_received')))::int AS open_count, COALESCE(SUM(total_cost) FILTER (WHERE status IN ('open','sent','partially_received')), 0)::numeric AS open_value, MAX(date_ordered) AS last_ordered FROM purchase_order WHERE supplier_id = sqlc.arg(supplier_id)::int;`
- `internal/purchasing/purchasing.go`: `SupplierPOSummary{OpenCount int; OpenValue decimal.Decimal; LastOrdered *time.Time}` + `func (s *Service) GetSupplierPOSummary(ctx, supplierID int) (SupplierPOSummary, error)`.

Then run `sqlc generate` (repo root) and commit `internal/dbq`. Check `sqlc.yaml` overrides give `*time.Time` for `MAX(date_ordered)`; if sqlc emits `interface{}`, cast `MAX(date_ordered)::date`.

No new SQL for the BOM-component, PO, or contact popouts (they reuse the existing service methods above).

### 3. Templates - `arx_go/templates/shared/popouts.html`
Add (each `{{define}}`, no layout/partials deps; status shown as plain label text via a `Label` field supplied by the handler):
- `popout_part_below_min` (data: `OnOrder decimal`, `Lines []parts.OpenPOLine`, `More bool`): title "On order"; total `{{printf "%.5g" .OnOrder}}`; list of `<a href="/po/{{.PONumber}}">{{.PONumber}}</a> {{.SupplierName}} - qty remaining {{printf "%.5g" .Remaining}}, expected {{if .Expected}}{{formatDate .Expected}}{{else}}n/a{{end}}`; empty state "Nothing on order."; "and more" when `.More`.
- `popout_part_stock` (data: `Total decimal`, `Lots []inventory.LotStock`, `Unlotted decimal`, `PartID int`, `More bool`): title "Stock on hand"; total; list of `<a href="/part/{{$.PartID}}/lots/{{.ID}}">{{.LotNumber}}</a> {{printf "%.5g" .Qty}}`; a `Not assigned to a lot` row when `.Unlotted` non-zero (`nonZero`); empty state "No stock recorded."
- `popout_part_bom_component` (data: same keys as `popout_part_where_used`: `Thumb`, `Used`; plus `Stock decimal`, `Supplier string`, `Price *decimal.Decimal`): body = `{{template "popout_part_where_used" .}}` followed by `<div class="popout-title">Stock &amp; sourcing</div>` with "On hand: {{printf "%.5g" .Stock}}", and "Preferred supplier: {{.Supplier}}{{if .Price}} at ${{printf "%.4f" (deref .Price)}}{{end}}" or "No preferred supplier" when `.Supplier` empty.
- `popout_supplier` (data: `ID int`, `Name`, `ContactName`, `ContactPhone`, `ContactEmail string`, `OpenCount int`, `OpenValue decimal`, `LastOrdered *time.Time`): title = linked name; primary contact (name, phone, email as `mailto:`; "No primary contact" when empty); "Open POs: N ($value)" with value via `printf "%.2f"`; "Last order: date or none".
- `popout_po` (data: `Number`, `Label` (status text), `Total decimal`, `Lines []purchasing.POLine` (first 5), `More bool`, `ReceivedLines`, `TotalLines int`): title "PO {{.Number}} - {{.Label}}"; total `${{printf "%.2f" .Total}}`; receive status "{{.ReceivedLines}} of {{.TotalLines}} lines received"; first-N lines list `{{.LineNumber}}. {{.PartNumberSnapshot}} qty {{printf "%.5g" .Qty}}` (part number linked to `/part/{{derefInt .PartID}}` when `.PartID`); "and more" when `.More`.
- `popout_contact` (data: `ID`, `DisplayName`, `Email`, `Phone`, `CompanyID *int`, `Employer string`, `OpenPOs []contacts.PO` (first 5), `More bool`): employer linked to `/supplier/{{derefInt .CompanyID}}` when set; phone; email `mailto:`; open POs list `<a href="/po/{{.Number}}">{{.Number}}</a> {{.Role}} - {{.Counterparty}}`; empty state "No open POs."

### 4. Handlers - `arx_go/api.go` (after `PopoutPartWhereUsed`)
- All list popouts: fetch/hold 6 rows, render the first 5, set `More` = true when a 6th existed (no count query). `PopoutPartBelowMin`: `h.parts().ListOpenPOLines(ctx, id, 6)`; render `popout_part_below_min`.
- `PopoutPartStock`: `h.inventory().PartLotStock`, total from `h.fetchPartBasic(...).StockOnHand`; cap lots at 5 (`More` when >5). Render `popout_part_stock` with `PartID`.
- `PopoutPartBOMComponent`: same as `PopoutPartWhereUsed` data (`ListWhereUsed`, `fetchPartBasic`) plus `Stock: p.StockOnHand`, `h.parts().GetPreferredSupplier` (ignore `sql.ErrNoRows`; other error -> serverError), `h.parts().PreferredSupplierPrice` only when a supplier exists. Render `popout_part_bom_component`.
- `PopoutSupplier`: `h.purchasing().GetSupplier` (`sql.ErrNoRows` -> NotFound) + `GetSupplierPOSummary`. Render `popout_supplier`.
- `PopoutPO`: `r.PathValue("id")` = number; `h.purchasing().GetPO(number)` (`sql.ErrNoRows` -> NotFound), `ListPOLines(number)`; compute `ReceivedLines` = lines with `ReceivedQty >= Qty`, `TotalLines` = `len(lines)`, `Label` = `poStatusLabels[po.Status]` (fall back to raw status), pass first 5 lines. Render `popout_po`.
- `PopoutContact`: `h.contacts().Get(id)` (`sql.ErrNoRows` -> NotFound) + `h.contacts().POs(id)`; filter in Go to statuses open/sent/partially_received; first 5. Render `popout_contact`.
- Routes in `arx_go/main.go` after line 316, in the order listed under Decisions.

### 5. Triggers (add `data-popout-url`; keep existing `href`/classes)
Parts:
- Below Min badge -> `/popout/part/{id}/below-min`: `static/shared/app.js:183` (`r.belowMin` badge in `/api/parts/rows` builder, id `r.id`); `templates/shared/partials.html:302` (header bar badge, `.Part.ID`); `templates/parts/part_detail.html:113` (badge, `.Part.ID`).
- On-hand quantity -> `/popout/part/{id}/stock`: wrap the stock figure in a `<span data-popout-url=...>` at `partials.html:302` (the `{{printf "%.2f" .Part.StockOnHand}}...` text, excluding the badge) and `part_detail.html:113`.
- BOM component part number -> replace `/where-used` with `/popout/part/{id}/bom-component` at `templates/parts/part_bom.html:59` (`.ComponentPartID`) and `static/shared/app.js:1052` (`item.ComponentPartID`). Other part-number triggers (`part_where_used.html`, `supplier_parts.html`, `po_detail.html`, `all_*.html`) keep `where-used`.
Supplier name -> `/popout/supplier/{id}`:
- `static/shared/app.js:196` (suppliers grid, `r.id`), `templates/parts/part_sourcing.html:89` (`.SupplierID`).
- `templates/parts/part_orders.html:26`: `SupplierName` is plain text with no id. Requires adding `supplier_id` to `ListPartOrders` (`internal/parts/parts.sql:357`), `parts.PartOrder` (`parts.go:808-833`) and the Go mapping; then render `<a href="/supplier/{{.SupplierID}}" class="part-number-link" data-popout-url=...>`. (Open question 4 on whether to do this.)
PO number -> `/popout/po/{number}`:
- `static/shared/app.js:231` (`r.num`, URL-encode with `encodeURIComponent`), `templates/parts/part_orders.html:25`, `templates/suppliers/supplier_pos.html:26`, `templates/suppliers/supplier_parts.html:50`, `templates/contacts/contact_detail.html:95`.
Contact name -> `/popout/contact/{id}`:
- `static/shared/app.js:214` (contacts grid, `r.id`), `templates/suppliers/supplier_detail.html:39` and `:59`, `templates/pos/po_detail.html:152` and `:171` (only inside the `{{if ...ContactID}}` branch).

### 6. CHANGELOG.md
New top entry `## [0.8.75] - 2026-10-28` under `### Added`: one line listing the new popouts and `### Changed`: one line for the 45 s popout cache TTL, both linking `https://github.com/Jolls/arx/issues/386`.

## Test plan

### 1) Coverage audit (existing)
- `arx_go/templates_parse_test.go:26` `TestParseTemplates*` (want-list includes the `"popouts"` key): asserts the popout set parses.
- `arx_go/templates_parse_test.go:358` `TestPopoutPartWhereUsedThumb`: executes `popout_part_where_used` with thumb/no-thumb/empty data. Covers the template the BOM-component fragment embeds.
- `arx_go/bom_integration_test.go:169` `TestIntegration_PartWhereUsed_Renders`: asserts the where-used PAGE keeps `data-popout-url="/popout/part/%d/where-used"` (lines 179, 184). Unaffected (page keeps where-used).
- `arx_go/suppliers_sqlc_integration_test.go:250` `TestIntegration_SupplierSQLC_Parts`: counts `data-popout-url="/popout/part/` triggers = 6 (line 263). Prefix-scoped, so unaffected by the new `/popout/po/` triggers on that tab; must keep passing.
- `arx_go/suppliers_sqlc_integration_test.go:231` `TestIntegration_SupplierSQLC_POs` (PO number text `>NNN</a>`): link text must stay `>number</a>` after adding the attribute.
- `arx_go/sqlc_converted_lint_test.go`: no raw SQL in non-test `arx_go` files; must pass (new handlers use services only).
- No test covers the popout handlers (`PopoutPartWhereUsed`, `PopoutLotConsumers`, `PopoutUnitConsumers`) directly, the popout JS cache, or `ListPartOrders`/`PartOrders` page rendering.

### 2) Characterization tests (pass on unchanged code; add first)
- `arx_go/popout_integration_test.go` (`//go:build integration`), `TestIntegration_PopoutPartWhereUsed_Characterization`: call `h.PopoutPartWhereUsed` with `withID(..., 3002)`; assert body contains `Used in` and `href="/part/3005"`. Pins the fragment the BOM-component popout wraps.
- `TestIntegration_PartOrders_Characterization` (same file): call `h.PartOrders` for part 3002 (`withID`); assert body contains `>5002</a>` and `Precision Machining Co`. Pins the page whose supplier/PO cells get triggers.
- `arx_go/static_popout_js_test.go`, `TestPopoutJS_CachesByURL` (no build tag; reads `static/shared/app.js` via `staticFS`, same style as `layout_theme_test.go`): assert the source contains `data-popout-url`-driven `cache[url]` read and write (`cache[url] !== undefined`, `cache[url] =`). Pins current caching so the TTL change is deliberate.

### 3) Red tests (fail today, pass after)
- `TestPopoutJS_CacheHasTTL` (`arx_go/static_popout_js_test.go`): source contains `TTL_MS = 45000` and an expiry comparison `Date.now() - cache[url].at < TTL_MS`. Fails today: no TTL.
- `TestPopoutFragmentsExecute` (`arx_go/templates_parse_test.go`, table-driven like `TestPopoutPartWhereUsedThumb`, no DB): executes each new template with fixture data and asserts key output; fails today with "no such template":
  - `popout_part_below_min`: lines + OnOrder=30 -> contains `/po/5003` and `30`; empty -> `Nothing on order.`
  - `popout_part_stock`: one lot + Unlotted=-4 -> contains `/part/3007/lots/8301` and `Not assigned to a lot`; zero unlotted -> that row absent; empty -> `No stock recorded.`
  - `popout_part_bom_component`: Supplier+Price -> contains `Used in`, `On hand`, `$0.0300`; empty Supplier -> `No preferred supplier`.
  - `popout_supplier`: contains name link `/supplier/1002`, `mailto:`, `Open POs: 1`; empty contact -> `No primary contact`.
  - `popout_po`: contains `/po/` title text, `1 of 2 lines received`, and "and more" only when `More`.
  - `popout_contact`: contains `/supplier/1002`, `mailto:`, open PO link; empty -> `No open POs.`
- `arx_go/popout_integration_test.go` (live ArxDev seed, each calls handler via `httptest` + `withID`/path value, asserts 200 and substrings; each fails today as the handler does not exist):
  - `TestIntegration_PopoutPartStock`: part 3007 -> contains `lots/8301`, `20`, `Not assigned to a lot`, total `16`; does not contain `8303` (zero balance hidden).
  - `TestIntegration_PopoutPartBelowMin`: part 3007 -> contains `/po/5003`, `Acme Fasteners`, remaining `30`; part 3001 -> contains `/po/5002` and not `/po/5001` (draft excluded).
  - `TestIntegration_PopoutPartBOMComponent`: part 3002 -> contains `href="/part/3005"` (Used in), `Precision Machining Co` (preferred supplier), `$0.0300`.
  - `TestIntegration_PopoutSupplier`: company 1002 -> contains `Bob Lee`, `555-0201`, `Open POs: 1`, `72.80`, `2026-06-01`; unknown id -> 404.
  - `TestIntegration_PopoutPO`: `5002` -> contains `Open`, `72.80`, `0 of 2 lines received`, `RAW-1001`; `5003` -> `0 of 1 lines received` (line 5504 received 20 of 50); unknown number -> 404.
  - `TestIntegration_PopoutContact`: 2003 -> contains `Precision Machining Co`, `555-0201`, `/po/5002`; unknown id -> 404.
- `TestIntegration_PartOrders_SupplierTrigger` (only if Open question 4 = yes): `h.PartOrders` for 3002 body contains `data-popout-url="/popout/supplier/1002"`.
- `TestIntegration_PopoutTriggers` (`popout_integration_test.go`): `h.PartSourcing` for 3002 contains `/popout/supplier/1002`; `h.PartBOM` for 3005 contains `/popout/part/3002/bom-component` and does NOT contain `/popout/part/3002/where-used`; `h.PODetail` for 5002 contains `/popout/contact/2003`. Fails today: triggers absent.
- Per-query service tests are covered by the handler tests above (no separate service tests; matches how `internal/*` is tested here via `arx_go` integration tests).

Seed: no new rows. All values above come from existing fixed-ID rows; no human reseed required beyond ArxDev already being at current seed. Run integration suite per CLAUDE.md (local container, explicit `ARX_TEST_DSN`).

### 4) Manual-only
- Hover Below Min badge (parts list for RAW-1002, part header bar, part dashboard Inventory card): popout shows on-order qty and PO 5003 with expected date; links navigate.
- Hover the stock figure (header bar, dashboard card): lot list + "Not assigned to a lot" row.
- BOM tab: hover a component part number (and an expanded sub-assembly row): thumbnail, Used in, on hand, preferred supplier/price in one popout; no second box.
- Hover supplier name on /suppliers, part Sourcing, part Orders; PO number on /pos, part Orders, supplier Order History, supplier Linked Parts, contact PO card; contact name on /contacts, supplier Details/Contacts card, PO detail.
- Cache TTL: hover a PO popout, change PO status in another tab, hover again after more than 45 s (fresh) and within 45 s (stale); popout stays open while pointer moves trigger -> popout; popout flips above/below near viewport edges.
- Dark mode and phone width: popouts readable, no horizontal overflow.
- Latency: confirm hover on the remote DB feels acceptable for the 2-query popouts (PO: GetPO + ListPOLines; supplier: GetSupplier + summary).

## Open questions
1. Stock "location": no location exists in the schema. Drop "and location" (lots only, as planned), or add a location column/table (separate schema issue)?
2. Expected date for open PO lines: use `purchase_order.date_requested`, else `date_ordered + po_line.lead_time_days`, else n/a (as written), or a different rule?
3. Confirm "open" PO = `open`, `sent`, `partially_received` (drafts and RFQ quotes excluded) for the Below Min on-order list, supplier open PO count/value, and contact open POs. "Last order date" = `MAX(date_ordered)` over all of the supplier's POs (includes drafts that have a date, e.g. 5008).
4. Part Orders tab supplier trigger needs `supplier_id` added to `ListPartOrders`/`PartOrder` (small query change to existing code). OK to include?
5. Trigger placement beyond the issue's named spots (Suppliers tab, part Orders/Sourcing): the plan also adds PO-number triggers on /pos, supplier Order History/Linked Parts, contact PO card and contact-name triggers on /contacts, supplier Details, PO detail. Anything to add (e.g. dashboard PO links, `part_detail` recent POs, `part_transactions` receipt reference) or drop?

## Resolved decisions (from user)
1. Location: lots only, as planned. No location column.
2. Expected date: `date_requested`, else `date_ordered + lead_time_days`, else n/a (as written).
3. "Open" PO = `open`, `sent`, `partially_received` (plan default; not contested). Last order = `MAX(date_ordered)` over all supplier POs.
4. Add `supplier_id` to `ListPartOrders` / `parts.PartOrder`; add the supplier trigger on `part_orders.html:26`. The conditional `TestIntegration_PartOrders_SupplierTrigger` is required.
5. Extra PO-number triggers (`data-popout-url="/popout/po/{{.Number}}"`, keep href/classes) in addition to section 5: `templates/reports/dashboard.html:108`, `templates/parts/part_detail.html:166` (recent POs), `templates/parts/part_transactions.html:70` (receipt reference link only). Add assertions for these three to `TestIntegration_PopoutTriggers` (dashboard handler, part detail for a part with recent POs, part transactions for a part with a receipt, e.g. 3007).
