# Detail-view Dashboards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the stacked "Details" sub-tab of Part, Supplier, and Contact with a 2-wide Bootstrap card grid that summarizes sibling sub-tabs (recent POs, price trend, inventory, linked parts) and links to them.

**Architecture:** Each detail handler (`PartDetail`, `SupplierDetail`, `ContactDetail`) gains a few cheap `TOP N` read queries whose results are added to the existing render map. The three `*_detail.html` templates are rewritten from stacked `.detail-section` blocks into a `row row-cols-1 row-cols-md-2 g-3` grid of `.card`s. The Part price-trend card reuses the existing `window.renderPriceHistory` JS. No schema changes, no new tables, no new routes.

**Tech Stack:** Go (net/http + go-chi), `html/template`, SQL Server via `h.query*Context` wrappers, Bootstrap 5.3, dependency-free SVG (`static/pm/price_history.js`).

## Global Constraints

- Never hardcode table names — always use `h.cfg.*Table()` helpers (e.g. `PartsTable`, `POTable`, `POLineTable`, `PriceTable`, `CompanyTable`, `ContactTable`, `InventoryTxnTable`, `SupplierPartTable`, `UnitTable`, `CompanyAttachmentsTable`, `AttachmentsTable`).
- All DB access goes through `h.queryContext` / `h.queryRowContext` / `h.execContext` — never `h.db.*` directly.
- SQL Server parameter placeholders are `@p1`, `@p2`, … (not `?`).
- Prefer Bootstrap classes over custom CSS/inline styles. Buttons always pair base + variant (`btn btn-secondary`). Use `table table-sm`, `card`, `badge`, spacing utilities (`mb-3`, `g-3`).
- Each card **omits itself** when its data is absent or the relevant category flag (`ShowOrders` / `ShowInventory` / `ShowPricing`) is false — no empty cards.
- Match existing template idioms: `formatDate`, `derefInt`, `printf`, the `po_status_badge` partial, and the attachment-link helpers (`isHTTPURL`, `isLocalDir`, `isLocalFile`, `isPDF`, `localFileURL`, `localDirURL`, `attachLabel`, `fileBaseName`, `supplierLocalFileURL`, `supplierLocalDirURL`).
- Repo uses CRLF and is not gofmt-clean — edit via targeted edits, do not whole-file reformat.
- Build/test from inside the module: `cd arx_go && go build ./...`. Quick manual run: `cd arx_go && go run .` (port 4568). Do not run `go build ./...` from repo root.
- One CHANGELOG entry for the whole branch (added in the final task), format: `## [0.3.X] - 2026-06-30 ` then `- <summary> ([#521](https://github.com/Jolls/arx-legacy/issues/521))`.

---

### Task 1: Part dashboard — recent-POs and recent-transactions card data

**Files:**
- Modify: `arx_go/parts.go` — add two helpers + extend the `PartDetail` render map (render call at `arx_go/parts.go:256`).

**Interfaces:**
- Produces:
  - `type partPOSummary struct { Number, SupplierName, Status string; DateOrdered *time.Time; Qty, UnitCost float64 }`
  - `type partTxnSummary struct { Type string; Qty float64; Date string }`
  - `func (h *Handler) recentPartPOs(ctx context.Context, partID string, limit int) []partPOSummary`
  - `func (h *Handler) recentPartTxns(ctx context.Context, partID string, limit int) []partTxnSummary`
  - Render-map keys added to `PartDetail`: `"RecentPOs"` (`[]partPOSummary`), `"RecentTxns"` (`[]partTxnSummary`).

- [ ] **Step 1: Add the two helper types + functions to `arx_go/parts.go`**

Place after the existing `PartOrders` handler (near `arx_go/parts.go:1139`). Both queries mirror the existing `PartOrders` / `PartTransactions` queries but with `TOP (@p2)` and newest-first ordering. On any error they return `nil` so the card simply omits itself.

```go
// partPOSummary is one row in the Part dashboard "Recent POs" card (#521).
type partPOSummary struct {
	Number       string
	SupplierName string
	Status       string
	DateOrdered  *time.Time
	Qty          float64
	UnitCost     float64
}

// recentPartPOs returns the most recent PO lines for a part, newest first,
// capped at limit. Returns nil on error so the caller can omit the card.
func (h *Handler) recentPartPOs(ctx context.Context, partID string, limit int) []partPOSummary {
	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p2) po.number, po.supplier_name, po.status, po.date_ordered,
		       pol.qty, pol.unit_cost
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE pol.part_id = @p1
		ORDER BY po.date_ordered DESC, po.ID DESC
	`, pol, po), partID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []partPOSummary
	for rows.Next() {
		var s partPOSummary
		var num, sup, status sql.NullString
		var d sql.NullTime
		var qty, cost sql.NullFloat64
		if rows.Scan(&num, &sup, &status, &d, &qty, &cost) != nil {
			continue
		}
		s.Number, s.SupplierName, s.Status = num.String, sup.String, status.String
		s.Qty, s.UnitCost = qty.Float64, cost.Float64
		if d.Valid {
			s.DateOrdered = &d.Time
		}
		out = append(out, s)
	}
	return out
}

// partTxnSummary is one row in the Part dashboard "Inventory" card (#521).
type partTxnSummary struct {
	Type string
	Qty  float64
	Date string
}

// recentPartTxns returns the most recent inventory transactions for a part,
// newest first, capped at limit. Returns nil on error.
func (h *Handler) recentPartTxns(ctx context.Context, partID string, limit int) []partTxnSummary {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p2) txn_type, qty, txn_date
		FROM %s WHERE part_id = @p1 ORDER BY txn_date DESC, id DESC
	`, h.cfg.InventoryTxnTable()), partID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []partTxnSummary
	for rows.Next() {
		var s partTxnSummary
		var d sql.NullTime
		var qty sql.NullFloat64
		if rows.Scan(&s.Type, &qty, &d) != nil {
			continue
		}
		s.Qty = qty.Float64
		if d.Valid {
			s.Date = d.Time.Format("2006-01-02")
		}
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 2: Populate the render map in `PartDetail`**

In `PartDetail` (`arx_go/parts.go:256`), just before the `h.render(...)` call, add:

```go
	var recentPOs []partPOSummary
	var recentTxns []partTxnSummary
	if p.ShowOrders {
		recentPOs = h.recentPartPOs(r.Context(), id, 5)
	}
	if p.ShowInventory {
		recentTxns = h.recentPartTxns(r.Context(), id, 5)
	}
```

Then add these two keys to the existing `map[string]any` passed to `h.render`:

```go
		"RecentPOs":         recentPOs,
		"RecentTxns":        recentTxns,
```

(`id` is the `chi.URLParam` string already in scope; `p.ShowOrders` / `p.ShowInventory` are the category flags set by `applyCategoryTabs`.)

- [ ] **Step 3: Build to verify it compiles**

Run: `cd arx_go && go build ./...`
Expected: exits 0, no output.

- [ ] **Step 4: Commit**

```bash
git add arx_go/parts.go
git commit -F - <<'EOF'
Add recent-PO and recent-transaction card data to PartDetail (#521)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
```

---

### Task 2: Part dashboard — extract shared price-history helper

**Files:**
- Modify: `arx_go/parts.go` — extract price-point assembly out of `PartPriceHistory` (`arx_go/parts.go:1145`) into a helper; call it from both `PartPriceHistory` and `PartDetail`.

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type pricePoint struct { Date string; Cost float64; PO, Supplier, Source string }` — promoted from the local type inside `PartPriceHistory` to package scope (JSON tags preserved).
  - `func (h *Handler) partPricePoints(ctx context.Context, partID string) []pricePoint`
  - Render-map keys added to `PartDetail`: `"PriceDataJSON"` (`template.JS`), `"HasPriceData"` (`bool`).

- [ ] **Step 1: Promote `pricePoint` to package scope**

Cut the local `type pricePoint struct {...}` block from inside `PartPriceHistory` (`arx_go/parts.go:1152-1158`) and paste it at package scope just above `PartPriceHistory`:

```go
// pricePoint is one unit-cost-over-time sample for the price-history chart (#284),
// shared by the Price History tab and the Part dashboard trend card (#521).
type pricePoint struct {
	Date     string  `json:"date"` // YYYY-MM-DD
	Cost     float64 `json:"cost"`
	PO       string  `json:"po"`
	Supplier string  `json:"supplier"`
	Source   string  `json:"source"` // "po" | "price"
}
```

- [ ] **Step 2: Add the `partPricePoints` helper**

Add above `PartPriceHistory`. Move the two queries (PO lines + active price-list entries) verbatim from the current handler body into this helper, returning the assembled slice. Errors return whatever was gathered so far (never fail the page):

```go
// partPricePoints assembles the unit-cost-over-time samples for a part from its
// PO lines and active price-list entries, chronological within each source.
// Shared by PartPriceHistory and PartDetail (#521).
func (h *Handler) partPricePoints(ctx context.Context, partID string) []pricePoint {
	var points []pricePoint

	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	if rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT po.number, po.supplier_name, po.date_ordered, pol.unit_cost
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE pol.part_id = @p1 AND po.date_ordered IS NOT NULL
		ORDER BY po.date_ordered
	`, pol, po), partID); err == nil {
		for rows.Next() {
			var num, sup sql.NullString
			var d sql.NullTime
			var cost float64
			if rows.Scan(&num, &sup, &d, &cost) == nil && d.Valid {
				points = append(points, pricePoint{
					Date: d.Time.Format("2006-01-02"), Cost: cost,
					PO: num.String, Supplier: sup.String, Source: "po",
				})
			}
		}
		rows.Close()
	}

	pr, comp := h.cfg.PriceTable(), h.cfg.CompanyTable()
	if prRows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT c.name, p.effective_date, p.price_ea
		FROM %s p
		LEFT JOIN %s c ON p.supplier_id = c.id
		WHERE p.part_id = @p1 AND p.is_active = 1 AND p.effective_date IS NOT NULL
		ORDER BY p.effective_date
	`, pr, comp), partID); err == nil {
		for prRows.Next() {
			var sup sql.NullString
			var d sql.NullTime
			var ea sql.NullFloat64
			if prRows.Scan(&sup, &d, &ea) == nil && d.Valid && ea.Valid {
				points = append(points, pricePoint{
					Date: d.Time.Format("2006-01-02"), Cost: ea.Float64,
					PO: "", Supplier: sup.String, Source: "price",
				})
			}
		}
		prRows.Close()
	}
	return points
}
```

- [ ] **Step 3: Rewrite `PartPriceHistory` body to use the helper**

Replace the point-gathering block (the two query loops between `var points []pricePoint` and `data, _ := json.Marshal(points)`, i.e. `arx_go/parts.go:1159-1214`) with:

```go
	points := h.partPricePoints(r.Context(), id)
```

Leave the `data, _ := json.Marshal(points)` line and the `h.render(...)` call unchanged. (`id := chi.URLParam(r, "id")` is already in scope in this handler.)

- [ ] **Step 4: Add price data to the `PartDetail` render map**

In `PartDetail`, before `h.render(...)`, add (guarded by pricing flag):

```go
	var priceJSON template.JS
	var hasPriceData bool
	if p.ShowPricing {
		pts := h.partPricePoints(r.Context(), id)
		if len(pts) > 0 {
			data, _ := json.Marshal(pts)
			priceJSON = template.JS(data)
			hasPriceData = true
		}
	}
```

Add to the render map:

```go
		"PriceDataJSON":     priceJSON,
		"HasPriceData":      hasPriceData,
```

(`template` and `encoding/json` are already imported in `parts.go` — see `arx_go/parts.go:7,9`.)

- [ ] **Step 5: Build + run the Price History tab to confirm the refactor is behavior-preserving**

Run: `cd arx_go && go build ./...`
Expected: exits 0.

Then `cd arx_go && go run .`, open `http://localhost:4568/part/<id>/price-history` for a part with PO history, and confirm the chart still renders exactly as before. Stop the server (Ctrl-C).

- [ ] **Step 6: Commit**

```bash
git add arx_go/parts.go
git commit -F - <<'EOF'
Extract shared partPricePoints helper for reuse on Part dashboard (#521)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
```

---

### Task 3: Part dashboard — rewrite `part_detail.html` as a card grid

**Files:**
- Modify: `arx_go/templates/pm/part_detail.html` (full rewrite of the `{{else}}` body between the status bar and `{{end}}{{/* end if .Error */}}`).

**Interfaces:**
- Consumes from Tasks 1-2: `.RecentPOs` (`[]partPOSummary`), `.RecentTxns` (`[]partTxnSummary`), `.PriceDataJSON` (`template.JS`), `.HasPriceData` (`bool`), plus existing `.Part`, `.PrimaryAtt`, `.RollupDelta`, `.RollupDeltaPct`, `.RollupSignificant`, `.AppVersion`.

- [ ] **Step 1: Keep the status bar; replace the stacked sections with a card grid**

Keep lines 1-58 (breadcrumb, tabs, status bar) unchanged. Replace everything from the `{{/* ── Basic info ─── */}}` block (line 60) through line 119 (`Additional Fields` section, i.e. up to but not including line 121 `{{end}}{{/* end if .Error */}}`) with the grid below.

Card omission uses the category flags and data presence. `table table-sm` for row lists. Each activity card header carries a `View all →` link.

```html
    {{/* ── Dashboard grid (#521) ─────────────────────────────── */}}
    <div class="row row-cols-1 row-cols-md-2 g-3">

        {{/* Pricing snapshot — data already on .Part */}}
        {{if .Part.ShowPricing}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Pricing</span>
                    <a href="/part/{{.Part.PNID}}/pricing" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    <div class="detail-row"><strong>Current Cost:</strong><span>${{printf "%.4f" .Part.PNCurrentCost}}</span></div>
                    {{if .Part.PNLastRollupAt}}
                    <div class="detail-row"><strong>Last Rollup:</strong><span>${{printf "%.4f" .Part.PNLastRollupCost}}</span></div>
                    <div class="detail-row"><strong>Rollup vs Price &Delta;:</strong>
                        <span class="{{if .RollupSignificant}}badge bg-warning text-dark{{end}}">
                            {{if gt .RollupDelta 0.0}}+{{end}}${{printf "%.4f" .RollupDelta}} ({{if gt .RollupDeltaPct 0.0}}+{{end}}{{printf "%.1f" .RollupDeltaPct}}%)
                        </span>
                    </div>
                    {{end}}
                </div>
            </div>
        </div>
        {{end}}

        {{/* Inventory — stock on hand + last few moves */}}
        {{if .Part.ShowInventory}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Inventory</span>
                    <a href="/part/{{.Part.PNID}}/transactions" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    <div class="detail-row"><strong>Stock on hand:</strong><span>{{printf "%.2f" .Part.StockOnHand}}</span></div>
                    {{if .RecentTxns}}
                    <table class="table table-sm mb-0 mt-2">
                        <thead><tr><th>Date</th><th>Type</th><th class="text-end">Qty</th></tr></thead>
                        <tbody>
                        {{range .RecentTxns}}
                            <tr><td>{{.Date}}</td><td>{{.Type}}</td><td class="text-end">{{printf "%.2f" .Qty}}</td></tr>
                        {{end}}
                        </tbody>
                    </table>
                    {{else}}<p class="text-muted mb-0 mt-2">No transactions yet.</p>{{end}}
                </div>
            </div>
        </div>
        {{end}}

        {{/* Recent POs */}}
        {{if and .Part.ShowOrders .RecentPOs}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Recent POs</span>
                    <a href="/part/{{.Part.PNID}}/orders" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    <table class="table table-sm mb-0">
                        <thead><tr><th>Date</th><th>PO</th><th>Supplier</th><th class="text-end">Qty</th><th class="text-end">Unit</th><th>Status</th></tr></thead>
                        <tbody>
                        {{range .RecentPOs}}
                            <tr>
                                <td>{{if .DateOrdered}}{{formatDate .DateOrdered}}{{else}}—{{end}}</td>
                                <td><a href="/po/{{.Number}}">{{.Number}}</a></td>
                                <td class="text-truncate" style="max-width:10rem;">{{.SupplierName}}</td>
                                <td class="text-end">{{printf "%.0f" .Qty}}</td>
                                <td class="text-end">${{printf "%.2f" .UnitCost}}</td>
                                <td>{{template "po_status_badge" .Status}}</td>
                            </tr>
                        {{end}}
                        </tbody>
                    </table>
                </div>
            </div>
        </div>
        {{end}}

        {{/* Price trend — reuses window.renderPriceHistory */}}
        {{if .HasPriceData}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Price Trend</span>
                    <a href="/part/{{.Part.PNID}}/price-history" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    <div id="dash-price-chart" class="position-relative"></div>
                </div>
            </div>
        </div>
        {{end}}

        {{/* Reference — basic info + additional fields */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Reference</div>
                <div class="card-body">
                    <div class="detail-row"><strong>Part Number:</strong><span>{{.Part.PartNumber}}</span></div>
                    <div class="detail-row"><strong>Title:</strong><span>{{.Part.Title}}</span></div>
                    <div class="detail-row"><strong>Category:</strong><span>{{.Part.Category}}</span></div>
                    <div class="detail-row"><strong>Revision:</strong><span>{{if .Part.Revision}}{{.Part.Revision}}{{else}}—{{end}}</span></div>
                    <div class="detail-row"><strong>Detail:</strong><span>{{.Part.Detail}}</span></div>
                    <div class="detail-row"><strong>Unit:</strong><span>{{if .Part.UnitAbbr}}{{.Part.UnitAbbr}}{{else}}—{{end}}</span></div>
                    <div class="detail-row"><strong>Requested By:</strong><span>{{.Part.PNReqBy}}</span></div>
                    {{range .Part.UserFields}}
                    <div class="detail-row"><strong>{{.Label}}:</strong><span>{{.Value}}</span></div>
                    {{end}}
                </div>
            </div>
        </div>

        {{/* Attachments — primary attachment pointer */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Attachments</span>
                    <a href="/part/{{.Part.PNID}}/attachments" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    {{if .PrimaryAtt}}
                        {{$f := .PrimaryAtt.FILFileName}}{{$n := .PrimaryAtt.Category}}
                        <div class="detail-row"><strong>Primary:</strong>
                        <span>
                        {{if isHTTPURL $f}}<a href="{{$f}}" target="_blank" rel="noopener noreferrer">{{attachLabel $f $n}}</a>
                        {{else if isLocalDir $f}}<a href="{{localDirURL $f}}">&#128193; {{attachLabel $f $n}}</a>
                        {{else if isLocalFile $f}}{{if isPDF $f}}<a href="{{localFileURL $f}}" target="_blank">{{attachLabel $f $n}}</a>{{else}}<a href="{{localFileURL $f}}" download="{{fileBaseName $f}}">{{attachLabel $f $n}}</a>{{end}}
                        {{else}}{{$f}}{{end}}
                        </span></div>
                    {{else}}<p class="text-muted mb-0">No primary attachment set.</p>{{end}}
                </div>
            </div>
        </div>

        {{/* Notes — full width */}}
        {{if .Part.PNNotes}}
        <div class="col-12">
            <div class="card">
                <div class="card-header">Notes</div>
                <div class="card-body"><p class="mb-0" style="white-space:pre-wrap; word-break:break-word;">{{.Part.PNNotes}}</p></div>
            </div>
        </div>
        {{end}}

    </div>

    {{if .HasPriceData}}
    <script>window.PRICE_HISTORY_DASH = {{.PriceDataJSON}};</script>
    <script src="/static/pm/price_history.js?v={{.AppVersion}}"></script>
    <script>
      document.addEventListener('DOMContentLoaded', function () {
        var el = document.getElementById('dash-price-chart');
        if (el && window.PRICE_HISTORY_DASH && window.renderPriceHistory) {
          window.renderPriceHistory(el, window.PRICE_HISTORY_DASH);
        }
      });
    </script>
    {{end}}
```

Note: the `po_status_badge` partial takes the status code string as its pipeline (`.`), confirmed at `arx_go/templates/pm/partials.html:56`. The Part model field names used (`PNCurrentCost`, `PNLastRollupCost`, `PNLastRollupAt`, `StockOnHand`, `PNReqBy`, `UserFields`, `UnitAbbr`, `PNNotes`, `Detail`, `Revision`, `PNID`) all appear in the current `part_detail.html`.

- [ ] **Step 2: Build + visually verify all card states**

Run: `cd arx_go && go build ./...` (expected exit 0), then `cd arx_go && go run .`.

Open `http://localhost:4568/part/<id>/details` and confirm:
- A part with POs, inventory, and pricing shows Pricing, Inventory, Recent POs, Price Trend, Reference, Attachments cards in a 2-col grid; the trend chart renders.
- A part in a category where `ShowOrders`/`ShowInventory`/`ShowPricing` are false omits those cards with no empty boxes.
- Notes card spans full width when notes exist and is absent otherwise.

Stop the server (Ctrl-C).

- [ ] **Step 3: Commit**

```bash
git add arx_go/templates/pm/part_detail.html
git commit -F - <<'EOF'
Rewrite Part detail view as a 2-wide dashboard card grid (#521)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
```

---

### Task 4: Supplier dashboard — card data + grid rewrite

**Files:**
- Modify: `arx_go/suppliers.go` — add two helpers + extend `SupplierDetail` render map (`arx_go/suppliers.go:98`).
- Modify: `arx_go/templates/pm/supplier_detail.html` (full rewrite of the section stack).

**Interfaces:**
- Produces:
  - `type supplierPOSummary struct { Number, Status string; DateOrdered *time.Time; Total float64 }`
  - `type supplierPartSummary struct { PNID int; PartNumber, Title string }`
  - `func (h *Handler) recentSupplierPOs(ctx context.Context, supplierID string, limit int) []supplierPOSummary`
  - `func (h *Handler) topSupplierParts(ctx context.Context, supplierID string, limit int) []supplierPartSummary`
  - Render-map keys added to `SupplierDetail`: `"RecentPOs"`, `"TopParts"`.

- [ ] **Step 1: Add helpers to `arx_go/suppliers.go`**

Add after `SupplierDetail` (near `arx_go/suppliers.go:103`). PO header columns confirmed at `arx_go/pos.go:2145` (`number`, `status`, `total_cost`, `supplier_id`, and `date_ordered` per `PartOrders`). Linked-parts columns mirror `SupplierParts` (`arx_go/suppliers.go:226`).

```go
// supplierPOSummary is one row in the Supplier dashboard "Recent POs" card (#521).
type supplierPOSummary struct {
	Number      string
	Status      string
	DateOrdered *time.Time
	Total       float64
}

func (h *Handler) recentSupplierPOs(ctx context.Context, supplierID string, limit int) []supplierPOSummary {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p2) number, status, date_ordered, total_cost
		FROM %s WHERE supplier_id = @p1
		ORDER BY date_ordered DESC, ID DESC
	`, h.cfg.POTable()), supplierID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []supplierPOSummary
	for rows.Next() {
		var s supplierPOSummary
		var num, status sql.NullString
		var d sql.NullTime
		var total sql.NullFloat64
		if rows.Scan(&num, &status, &d, &total) != nil {
			continue
		}
		s.Number, s.Status, s.Total = num.String, status.String, total.Float64
		if d.Valid {
			s.DateOrdered = &d.Time
		}
		out = append(out, s)
	}
	return out
}

// supplierPartSummary is one row in the Supplier dashboard "Linked Parts" card (#521).
type supplierPartSummary struct {
	PNID       int
	PartNumber string
	Title      string
}

func (h *Handler) topSupplierParts(ctx context.Context, supplierID string, limit int) []supplierPartSummary {
	sp, pn := h.cfg.SupplierPartTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p2) pn.id, pn.part_number, pn.title
		FROM %s sp JOIN %s pn ON sp.part_id = pn.id
		WHERE sp.supplier_id = @p1
		ORDER BY pn.part_number
	`, sp, pn), supplierID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []supplierPartSummary
	for rows.Next() {
		var s supplierPartSummary
		var num, title sql.NullString
		if rows.Scan(&s.PNID, &num, &title) != nil {
			continue
		}
		s.PartNumber, s.Title = num.String, title.String
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 2: Populate the `SupplierDetail` render map**

In `SupplierDetail` before `h.render(...)` (`arx_go/suppliers.go:98`) add:

```go
	recentPOs := h.recentSupplierPOs(r.Context(), id, 5)
	topParts := h.topSupplierParts(r.Context(), id, 5)
```

Add to the render map:

```go
		"RecentPOs": recentPOs,
		"TopParts":  topParts,
```

(`id := chi.URLParam(r, "id")` is already in scope at `arx_go/suppliers.go:75`.)

- [ ] **Step 3: Rewrite `supplier_detail.html` as a card grid**

Keep the breadcrumb + `{{template "supplier_tabs" .}}` (lines 1-9). Replace the five `.detail-section` blocks (lines 11-80) with:

```html
    <div class="row row-cols-1 row-cols-md-2 g-3">

        {{/* Basic info + roles/status */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Supplier</div>
                <div class="card-body">
                    <div class="detail-row"><strong>Name:</strong><span>{{.Supplier.Name}}</span></div>
                    <div class="detail-row"><strong>Code:</strong><span>{{.Supplier.SUSupplierCode}}</span></div>
                    <div class="detail-row"><strong>Status:</strong>
                        <span>{{if .Supplier.IsActive}}<span class="badge bg-success">Active</span>{{else}}<span class="badge bg-danger">Inactive</span>{{end}}</span></div>
                    <div class="detail-row"><strong>Roles:</strong>
                        <span>
                            {{if .Supplier.IsSupplier}}<span class="badge bg-success">Supplier</span> {{end}}
                            {{if .Supplier.IsManufacturer}}<span class="badge bg-success">Manufacturer</span>{{end}}
                            {{if and (not .Supplier.IsSupplier) (not .Supplier.IsManufacturer)}}—{{end}}
                        </span></div>
                    <div class="detail-row"><strong>Last Modified:</strong><span>{{formatDate .Supplier.DateModified}}</span></div>
                </div>
            </div>
        </div>

        {{/* Default contact */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Default Contact</div>
                <div class="card-body">
                    {{if .Supplier.DefaultContact}}
                    <div class="detail-row"><strong>Name:</strong><span><a href="/contact/{{derefInt .Supplier.DefaultContact}}">{{.Supplier.CNName}}</a></span></div>
                    <div class="detail-row"><strong>Phone:</strong><span>{{if .Supplier.CNPhone1}}{{.Supplier.CNPhone1}}{{else}}—{{end}}</span></div>
                    <div class="detail-row"><strong>Email:</strong><span>{{if .Supplier.CNEmail}}{{.Supplier.CNEmail}}{{else}}—{{end}}</span></div>
                    <div class="detail-row"><strong>City:</strong><span>{{if .Supplier.CNCity}}{{.Supplier.CNCity}}{{else}}—{{end}}</span></div>
                    {{else}}<p class="text-muted mb-0">No default contact set.</p>{{end}}
                </div>
            </div>
        </div>

        {{/* Statistics */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Statistics</div>
                <div class="card-body">
                    <div class="detail-row"><strong>Linked Parts:</strong><span>{{.Supplier.SUNumOfLNKs}}</span></div>
                    <div class="detail-row"><strong>Purchase Orders:</strong><span>{{.Supplier.SUNumOfPOs}}</span></div>
                </div>
            </div>
        </div>

        {{/* Linked parts (top 5) */}}
        {{if .TopParts}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Linked Parts</span>
                    <a href="/supplier/{{.Supplier.ID}}/parts" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    <table class="table table-sm mb-0">
                        <tbody>
                        {{range .TopParts}}
                            <tr><td><a href="/part/{{.PNID}}">{{.PartNumber}}</a></td><td class="text-truncate" style="max-width:14rem;">{{.Title}}</td></tr>
                        {{end}}
                        </tbody>
                    </table>
                </div>
            </div>
        </div>
        {{end}}

        {{/* Recent POs (top 5) — no PO sub-tab, so no View-all link */}}
        {{if .RecentPOs}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Recent POs</div>
                <div class="card-body">
                    <table class="table table-sm mb-0">
                        <thead><tr><th>Date</th><th>PO</th><th class="text-end">Total</th><th>Status</th></tr></thead>
                        <tbody>
                        {{range .RecentPOs}}
                            <tr>
                                <td>{{if .DateOrdered}}{{formatDate .DateOrdered}}{{else}}—{{end}}</td>
                                <td><a href="/po/{{.Number}}">{{.Number}}</a></td>
                                <td class="text-end">${{printf "%.2f" .Total}}</td>
                                <td>{{template "po_status_badge" .Status}}</td>
                            </tr>
                        {{end}}
                        </tbody>
                    </table>
                </div>
            </div>
        </div>
        {{end}}

        {{/* Default attachment */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Attachments</span>
                    <a href="/supplier/{{.Supplier.ID}}/attachments" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    {{if .PrimaryAtt}}
                        {{$f := .PrimaryAtt.FilePath}}{{$n := .PrimaryAtt.Notes}}
                        <div class="detail-row"><strong>Default:</strong><span>
                        {{if isHTTPURL $f}}<a href="{{$f}}" target="_blank" rel="noopener noreferrer">{{attachLabel $f $n}}</a>
                        {{else if isLocalDir $f}}<a href="{{supplierLocalDirURL $f}}">&#128193; {{attachLabel $f $n}}</a>
                        {{else if isLocalFile $f}}{{if isPDF $f}}<a href="{{supplierLocalFileURL $f}}" target="_blank">{{attachLabel $f $n}}</a>{{else}}<a href="{{supplierLocalFileURL $f}}" download="{{fileBaseName $f}}">{{attachLabel $f $n}}</a>{{end}}
                        {{else}}{{$f}}{{end}}
                        </span></div>
                    {{else}}<p class="text-muted mb-0">No default attachment.</p>{{end}}
                </div>
            </div>
        </div>

        {{/* Notes — full width */}}
        {{if .Supplier.SUNotes}}
        <div class="col-12">
            <div class="card">
                <div class="card-header">Notes</div>
                <div class="card-body"><p class="mb-0" style="white-space:pre-wrap; word-break:break-word;">{{.Supplier.SUNotes}}</p></div>
            </div>
        </div>
        {{end}}

    </div>
```

- [ ] **Step 4: Build + visually verify**

Run: `cd arx_go && go build ./...` (exit 0), then `cd arx_go && go run .`.
Open `http://localhost:4568/supplier/<id>` and confirm the grid shows Supplier, Default Contact, Statistics, Linked Parts, Recent POs, Attachments cards; a supplier with no linked parts / no POs omits those cards; Notes spans full width when present. Stop the server.

- [ ] **Step 5: Commit**

```bash
git add arx_go/suppliers.go arx_go/templates/pm/supplier_detail.html
git commit -F - <<'EOF'
Rewrite Supplier detail view as a dashboard card grid (#521)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
```

---

### Task 5: Contact dashboard — sibling contacts + grid rewrite

**Files:**
- Modify: `arx_go/contacts.go` — add one helper + extend `ContactDetail` render map (`arx_go/contacts.go:99`).
- Modify: `arx_go/templates/pm/contact_detail.html` (full rewrite of the section stack).

**Interfaces:**
- Produces:
  - `type siblingContact struct { CNID int; CNName string }`
  - `func (h *Handler) siblingContacts(ctx context.Context, supplierID, excludeContactID int) []siblingContact`
  - Render-map key added to `ContactDetail`: `"Siblings"`.

- [ ] **Step 1: Add the helper to `arx_go/contacts.go`**

Add after `ContactDetail` (near `arx_go/contacts.go:103`). Contact columns confirmed at `arx_go/contacts.go:213` (`id`, `display_name`, `company_id`).

```go
// siblingContact is one row in the Contact dashboard "Related" card (#521).
type siblingContact struct {
	CNID   int
	CNName string
}

// siblingContacts returns other active contacts at the same supplier, excluding
// the current contact. Returns nil when there is no supplier or on error.
func (h *Handler) siblingContacts(ctx context.Context, supplierID, excludeContactID int) []siblingContact {
	if supplierID <= 0 {
		return nil
	}
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, display_name FROM %s
		WHERE company_id = @p1 AND id <> @p2 AND is_active = 1
		ORDER BY display_name
	`, h.cfg.ContactTable()), supplierID, excludeContactID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []siblingContact
	for rows.Next() {
		var s siblingContact
		var name sql.NullString
		if rows.Scan(&s.CNID, &name) != nil {
			continue
		}
		s.CNName = name.String
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 2: Populate the `ContactDetail` render map**

In `ContactDetail` before `h.render(...)` (`arx_go/contacts.go:99`) add:

```go
	var siblings []siblingContact
	if c.CNSUID != nil {
		siblings = h.siblingContacts(r.Context(), *c.CNSUID, c.CNID)
	}
```

Add to the render map:

```go
		"Siblings": siblings,
```

(`c.CNSUID` is `*int` — confirmed by its use at `arx_go/templates/pm/contact_detail.html:19` as `derefInt .Contact.CNSUID`. Verify the model field name/type in `arx_go/models/contact.go` before writing; if it is a non-pointer, drop the nil check and pass it directly.)

- [ ] **Step 3: Rewrite `contact_detail.html` as a card grid**

Keep the header block with breadcrumb + Edit button (lines 1-11). Replace the five `.detail-section` blocks (lines 13-59) with:

```html
    <div class="row row-cols-1 row-cols-md-2 g-3">

        {{/* Basic info */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Contact</div>
                <div class="card-body">
                    <div class="detail-row"><strong>Name:</strong><span>{{.Contact.CNName}}</span></div>
                    <div class="detail-row"><strong>Supplier:</strong>
                        <span>{{if .Contact.CNSUID}}<a href="/supplier/{{derefInt .Contact.CNSUID}}">{{.Contact.SupplierName}}</a>{{else}}None{{end}}</span></div>
                    <div class="detail-row"><strong>Email:</strong><span>{{.Contact.CNEmail}}</span></div>
                    <div class="detail-row"><strong>Web:</strong><span>{{.Contact.CNWeb}}</span></div>
                    <div class="detail-row"><strong>Status:</strong>
                        <span>{{if .Contact.CNActive}}<span class="badge bg-success">Active</span>{{else}}<span class="badge bg-danger">Inactive</span>{{end}}</span></div>
                    <div class="detail-row"><strong>Last Modified:</strong><span>{{formatDate .Contact.CNDateModified}}</span></div>
                </div>
            </div>
        </div>

        {{/* Contact details */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Contact Details</div>
                <div class="card-body">
                    <div class="detail-row"><strong>Phone 1:</strong><span>{{.Contact.CNPhone1}}</span></div>
                    <div class="detail-row"><strong>Phone 2:</strong><span>{{.Contact.CNPhone2}}</span></div>
                    <div class="detail-row"><strong>Fax:</strong><span>{{.Contact.CNFAX}}</span></div>
                </div>
            </div>
        </div>

        {{/* Address */}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Address</div>
                <div class="card-body">
                    <div class="detail-row"><strong>Street:</strong><span>{{.Contact.CNAddress}}</span></div>
                    <div class="detail-row"><strong>City:</strong><span>{{.Contact.CNCity}}</span></div>
                    <div class="detail-row"><strong>State:</strong><span>{{.Contact.CNState}}</span></div>
                    <div class="detail-row"><strong>Zip:</strong><span>{{.Contact.CNZipcode}}</span></div>
                    <div class="detail-row"><strong>Country:</strong><span>{{.Contact.CNCountry}}</span></div>
                </div>
            </div>
        </div>

        {{/* Related — other contacts at the same supplier */}}
        {{if .Siblings}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header">Other Contacts at {{.Contact.SupplierName}}</div>
                <div class="card-body">
                    <ul class="list-unstyled mb-0">
                    {{range .Siblings}}
                        <li><a href="/contact/{{.CNID}}">{{.CNName}}</a></li>
                    {{end}}
                    </ul>
                </div>
            </div>
        </div>
        {{end}}

        {{/* Notes — full width */}}
        {{if .Contact.CNNotes}}
        <div class="col-12">
            <div class="card">
                <div class="card-header">Notes</div>
                <div class="card-body"><p class="mb-0" style="white-space:pre-wrap; word-break:break-word;">{{.Contact.CNNotes}}</p></div>
            </div>
        </div>
        {{end}}

    </div>
```

- [ ] **Step 4: Build + visually verify**

Run: `cd arx_go && go build ./...` (exit 0), then `cd arx_go && go run .`.
Open `http://localhost:4568/contact/<id>` and confirm the grid shows Contact, Contact Details, Address cards; a contact whose supplier has other contacts shows the "Other Contacts" card; a contact with no supplier or no siblings omits it; Notes spans full width when present. Stop the server.

- [ ] **Step 5: Commit**

```bash
git add arx_go/contacts.go arx_go/templates/pm/contact_detail.html
git commit -F - <<'EOF'
Rewrite Contact detail view as a card grid with a related-contacts card (#521)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
```

---

### Task 6: Full build/test sweep, changelog, PR

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `arx_go/RELEASE_NOTES.md` (only if cutting a user-facing release — otherwise skip per repo convention)

- [ ] **Step 1: Run the full test sweep + build**

Run (PowerShell tool, per repo convention for `.bat`):
`cd arx_go; .\build.bat`
Expected: `go test ./...` passes and `Arx.exe` is produced. If any pre-existing test fails unrelated to this change, note it but do not fix unrelated code.

- [ ] **Step 2: Add a single CHANGELOG entry for the branch**

Add at the top of `CHANGELOG.md` (bump the patch from the current top entry):

```
## [0.3.X] - 2026-06-30 
- Redesign Part, Supplier, and Contact detail views as 2-wide dashboard grids summarizing sibling sub-tabs ([#521](https://github.com/Jolls/arx-legacy/issues/521))
```

- [ ] **Step 3: Commit the changelog**

```bash
git add CHANGELOG.md
git commit -F - <<'EOF'
Changelog: detail-view dashboards (#521)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
```

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin feature/detail-view-dashboards-521
```

Then open the PR with `gh pr create` using `--body-file` (write the body to a temp file first — do not use here-strings). The body MUST contain `Closes #521` on its own line so the issue auto-closes on merge.

---

## Self-Review

**Spec coverage:**
- Shared card-grid pattern (Bootstrap `row row-cols-1 row-cols-md-2 g-3`, self-omitting cards, full-width Notes) → Tasks 3, 4, 5. ✓
- Part cards: Pricing (loaded), Inventory ✦, Recent POs ✦, Price trend ✦, Reference, Attachments → Tasks 1-3. ✓ Suppliers card = phase 2, explicitly out of scope. ✓
- Shared price-history helper (spec: "factor into a shared helper so both call it") → Task 2. ✓
- Supplier cards: Default contact, Statistics (existing), Linked parts, Recent POs (no PO sub-tab), Attachments, Notes → Task 4. ✓
- Contact = layout re-flow only + Related card (siblings), no invented activity → Task 5. ✓
- Data changes via `h.query*Context` + `cfg.*Table()`, cheap `TOP N`, guarded so failure omits the card → all helpers return `nil` on error. ✓
- Testing = visual `go run .` for full/omitted card states → verification steps in Tasks 3-5. ✓

**Placeholder scan:** No TBD/TODO; every code step has complete code. The one conditional ("verify `CNSUID` type in the model") is a concrete verification instruction with a defined fallback, not a placeholder.

**Type consistency:** Helper names and struct field names are used identically across handler and template steps (`partPOSummary`, `partTxnSummary`, `pricePoint`, `supplierPOSummary`, `supplierPartSummary`, `siblingContact`; render keys `RecentPOs`, `RecentTxns`, `PriceDataJSON`, `HasPriceData`, `TopParts`, `Siblings`). `po_status_badge` invoked with the status string as pipeline, matching `partials.html`.

## Notes / risks

- The price-trend card reuses `window.renderPriceHistory` as-is (full 820×360 responsive SVG scaled to card width) rather than a new sparkline — DRY, matches the spec's "compact reuse of price_history.js." If the full chart reads too tall in a card during Task 3 visual check, that's the one place a follow-up tweak (a compact render flag) may be wanted; flag it, don't gold-plate here.
- `total_cost` on the PO header is assumed populated (used at `arx_go/pos.go:2145`). If some POs store totals only as line roll-ups, the Supplier "Recent POs" Total may read `$0.00` for those — acceptable for a summary; note if observed.
