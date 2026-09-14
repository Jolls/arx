# #55 — Preferred Supplier card on part detail

## Decisions

**Card name: "Preferred Supplier".**
- "Supplier Reference" was rejected: the dashboard's first card is already named "Reference", and two cards named `Reference` / `Supplier Reference` side by side read as related when they are not.
- "Preferred" (not "Primary") matches the app's own vocabulary for `part.default_supplier_id`: the `Preferred` badge and `Set preferred` button in `arx_go/templates/parts/part_pricing.html:23-31`, the tooltip "Cost rollup uses this supplier's cheapest active price", and `ensureDefaultSupplier` in `arx_go/parts.go:2156`. The issue's word "primary" refers to the same pointer; using the established UI term avoids inventing a second name for one concept.

**"Primary supplier" == `part.default_supplier_id`.** Confirmed, not an open question. It is the single canonical pointer (FK to `company.id`, `SQL/azure/part.sql:56`), it is what the rollup delta already compares against (`arx_go/parts.go:381-392`), it is settable from the UI, and the data-quality report flags BUY parts missing it (`arx_go/reports.go:841-845`). `supplier_part.preference` is a per-link ranking that may be unset on every row, so it cannot identify a unique primary supplier.

**Coexist, do NOT replace the Pricing card.** The new card is added; `part_detail.html`'s Pricing card, Price Trend card, `/part/{id}/pricing` and `/part/{id}/price-history` are all left byte-for-byte unchanged. Rationale:
1. The dashboard's convention is strictly one card per sub-tab (Photos→attachments, Inventory→transactions, Lots→lots, Recent POs→orders). Supplier data belongs to the **Suppliers** sub-tab; cost/rollup data belongs to **Pricing**. Merging them produces one card with two "View all →" targets.
2. The gates are independently configurable: `CategoryTabs.Pricing` and `CategoryTabs.Suppliers` are separate booleans (`arx_go/models/part.go`) an admin can set differently per category in Settings. A merged card would need a compound `or` gate and per-section inner conditionals, or it would silently hide cost data for a `Pricing=true, Suppliers=false` category.
3. The two cards show genuinely different numbers — `part.current_cost` (stored/rolled cost) vs. the supplier's active `price.price_ea`. Distinct labels ("Current Cost" vs. "Supplier Price") keep them from reading as duplicates.

**Field list (exactly four rows), in order:** Supplier (name, linked to `/supplier/{id}`), Supplier P/N, Supplier Description, Supplier Price.
Lead Time and Min Increment were considered and **excluded** — not requested by the issue, and CLAUDE.md rule 2 ("nothing speculative"). They remain visible on the Suppliers tab one click away. Supplier name is included despite not being listed in the issue because a card about "the preferred supplier" is meaningless without naming it.

**Visibility gate: `.Part.ShowSuppliers`.** No new `Show*()` method. It is the existing gate for the `/part/{id}/suppliers` sub-tab this card links to (`arx_go/models/part.go:139`), so the card never links to a disabled tab. The card renders with an empty state when the part has no preferred supplier yet, matching the "No orders yet." / "No lots yet." convention.

## Go changes — `arx_go/parts.go` only

### 1. New summary struct

Add next to the other dashboard summary types (`partPOSummary` / `partTxnSummary`):

```go
// preferredSupplierSummary is the part detail dashboard's Preferred Supplier
// card (#55): the part's preferred supplier (part.default_supplier_id, #465)
// plus its supplier_part reference fields. HasLink is false when the supplier
// is pinned but no supplier_part row exists for it yet.
type preferredSupplierSummary struct {
	SupplierID   int
	SupplierName string
	SupplierPN   string
	SupplierDesc string
	HasLink      bool
	Price        *float64 // cheapest active price from this supplier; nil = none
}
```

### 2. New fetch helper

Place near the other `recentPart*` helpers:

```go
// preferredSupplier loads the part's preferred supplier and its supplier_part
// reference row for the Preferred Supplier card (#55). Returns nil when no
// preferred supplier is pinned (the JOIN drops the row on a NULL
// default_supplier_id). Price is filled in by the caller.
func (h *Handler) preferredSupplier(ctx context.Context, partID string) *preferredSupplierSummary {
	top, limitClause := h.topLimit("@p2")
	pt, co, sp := h.cfg.PartsTable(), h.cfg.CompanyTable(), h.cfg.SupplierPartTable()
	var s preferredSupplierSummary
	var name, pn, desc sql.NullString
	var spID sql.NullInt64
	err := h.queryRowContext(ctx, fmt.Sprintf(`
		SELECT %sc.id, c.name, sp.id, sp.supplier_pn, sp.supplier_desc
		FROM %s p
		JOIN %s c ON c.id = p.default_supplier_id
		LEFT JOIN %s sp ON sp.part_id = p.id AND sp.supplier_id = c.id
		WHERE p.id = @p1
		ORDER BY sp.preference, sp.id
	`+limitClause, top, pt, co, sp), partID, 1).Scan(&s.SupplierID, &name, &spID, &pn, &desc)
	if err != nil {
		return nil
	}
	s.SupplierName = name.String
	s.SupplierPN = pn.String
	s.SupplierDesc = desc.String
	s.HasLink = spID.Valid
	return &s
}
```

Notes for the implementer:
- `h.topLimit("@p2")` is the established cross-dialect row cap (`arx_go/handlers.go:171`, used by `recentPartPOs` at `parts.go:1889`): SQL Server emits `TOP (@p2) ` into the `%s` right after `SELECT`, Postgres emits ` LIMIT @p2` appended after `ORDER BY`. `@p2` is bound to the literal `1`. The cap exists only to guarantee one row if a part ever has two `supplier_part` rows for the same supplier.
- `err != nil` covers `sql.ErrNoRows` (no preferred supplier) and real errors identically, matching the deliberately non-fatal style of the other dashboard helpers (`recentPartPOs` returns nil on error; `fetchActivePricesBySupplier` in `sourcing.go:278` does the same). A missing card beats a failed page render.
- A narrow single-row query is used rather than `h.fetchSupplierLinks` + filter: that helper joins two extra uom rows and returns every supplier link for the part, all discarded except one. Scoping the query to `default_supplier_id` fetches exactly the row the card shows.
- No new SQL file, no migration, no `cfg.*Table()` helper — `SupplierPartTable`, `CompanyTable`, `PartsTable` all exist.

### 3. Wire it into `PartDetail`

`PartDetail` already computes the preferred supplier's cheapest active price at `arx_go/parts.go:385-392` into `prefPrice sql.NullFloat64`. **Reuse it — do not add a second price query.** That block is unconditional and runs before the card blocks.

Insert after the `ShowUnits()` block (currently `parts.go:417-422`) and before the `priceJSON` block:

```go
	var prefSupplier *preferredSupplierSummary
	if p.ShowSuppliers() {
		prefSupplier = h.preferredSupplier(r.Context(), id)
		if prefSupplier != nil && prefPrice.Valid {
			prefSupplier.Price = &prefPrice.Float64
		}
	}
```

Add one key to the `h.render` map (currently `parts.go:435-451`), after `"UnitCount": unitCount,`:

```go
		"PreferredSupplier": prefSupplier,
```

No other Go file changes. No change to `arx_go/models/` (the existing `models.SupplierPart` is not used here — it carries a dozen fields this card does not show, and `preferredSupplierSummary` needs `HasLink` and `Price`, which it lacks). No new template funcs — `deref` already handles `*float64` (`part_detail.html:140`, `part_pricing.html:51`).

## Template change — `arx_go/templates/parts/part_detail.html`

Insert this block between the Photos card's closing `{{end}}` (line 105) and the `{{/* Pricing snapshot ... */}}` comment (line 107), i.e. the new card sits immediately before the Pricing card so the two procurement cards are adjacent. Indentation is 8 spaces at the `{{if`, matching its siblings.

```html
        {{/* Preferred Supplier — reference data for part.default_supplier_id (#55) */}}
        {{if .Part.ShowSuppliers}}
        <div class="col">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span>Preferred Supplier</span>
                    <a href="/part/{{.Part.ID}}/suppliers" class="small">View all &rarr;</a>
                </div>
                <div class="card-body">
                    {{with .PreferredSupplier}}
                    <div class="detail-row"><strong>Supplier:</strong><span><a href="/supplier/{{.SupplierID}}">{{.SupplierName}}</a></span></div>
                    <div class="detail-row"><strong>Supplier P/N:</strong><span class="font-monospace">{{if .SupplierPN}}{{.SupplierPN}}{{else}}—{{end}}</span></div>
                    <div class="detail-row"><strong>Supplier Description:</strong><span>{{if .SupplierDesc}}{{.SupplierDesc}}{{else}}—{{end}}</span></div>
                    <div class="detail-row"><strong>Supplier Price:</strong><span>{{if .Price}}${{printf "%.4f" (deref .Price)}}{{else}}—{{end}}</span></div>
                    {{if not .HasLink}}<p class="text-muted mb-0 mt-2">No supplier part details on file.</p>{{end}}
                    {{else}}
                    <p class="text-muted mb-0">No preferred supplier set.</p>
                    {{end}}
                </div>
            </div>
        </div>
        {{end}}
```

Formatting/markup rationale, all matching existing code in this file:
- `detail-row` + `<strong>Label:</strong><span>value</span>` — the Reference/Pricing/Inventory card pattern.
- `—` (em dash) for an empty scalar — the Reference card's Revision/Unit rows (lines 73, 75).
- `%.4f` with a `$` prefix — the Pricing card's cost rows (lines 116, 118). The 6-decimal `%.6f` used on `/part/{id}/pricing` is deliberately not used here; a dashboard summary matches its neighbor card.
- `font-monospace` on the supplier P/N — matches the Suppliers table cell (`part_sourcing.html:34`).
- `/supplier/{id}` is a live route (`arx_go/main.go`) and is already how `part_pricing.html:16` links a supplier.
- No inline styles; Bootstrap utilities only (CLAUDE.md frontend rule).

### Empty / fallback states (all three, exhaustively)

| State | What renders |
|---|---|
| No `default_supplier_id` | Helper returns nil → `<p class="text-muted mb-0">No preferred supplier set.</p>` (the `{{with}}...{{else}}` branch). |
| `default_supplier_id` set, no `supplier_part` row | The four rows render; `SupplierPN`/`SupplierDesc` show `—`; `HasLink` is false so the card appends `<p class="text-muted mb-0 mt-2">No supplier part details on file.</p>`. |
| Supplier + link row, no active `price` row for that supplier | `Price` is nil → Supplier Price cell shows `—`. All other rows unaffected. |
| Category has `Suppliers: false` (DWG/DOC/OPS/FORM by default) | Card omitted entirely, same as the disabled Suppliers sub-tab. |

## Explicitly NOT changed

- `arx_go/templates/parts/part_pricing.html` — no change.
- `arx_go/templates/parts/part_sourcing.html` — no change.
- `arx_go/templates/shared/partials.html` (`part_tabs`) — no change; no new sub-tab.
- `arx_go/sourcing.go` — no change; `fetchSupplierLinks` untouched.
- `arx_go/models/` — no change.
- `SQL/` — no schema change, no migration, no seed change. `supplier_part`, `price`, `part.default_supplier_id` all already exist.
- The Pricing card, Price Trend card, and all pricing routes — unchanged.

## Verification

1. `cd arx_go; go build ./... ; go vet ./...` and `go test ./...` (no new unit test: this is a UI-only card over existing data, CLAUDE.md rule 5 — the existing integration smoke test already renders `PartDetail` and will catch a template parse error).
2. Manual (user runs the app, seeded ArxDev):
   - A BUY part with a preferred supplier and a `supplier_part` row with `supplier_pn`/`supplier_desc` and an active price → all four rows populated, supplier name links to the supplier page, "View all →" goes to the Suppliers tab.
   - A BUY part with no `default_supplier_id` → "No preferred supplier set."
   - A part whose category has Suppliers disabled (e.g. DOC) → card absent.
   - Pricing card, Price Trend card, and the Pricing/Suppliers sub-tabs render exactly as before.
3. CHANGELOG.md: bump patch version, add under `### Added`:
   `- Preferred Supplier card on the part detail dashboard showing the preferred supplier's part number, description, and active price ([#55](https://github.com/Jolls/arx/issues/55))`

## Open questions

None.
