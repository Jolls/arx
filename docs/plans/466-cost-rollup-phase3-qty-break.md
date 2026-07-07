# [ENG-3c] Cost Rollup Phase 3 — Qty-Break-Aware "Cost to Build N" (#466)

## Goal / user story

A user opens a top-level assembly and asks: *"I want to buy 100 of these — what does it
cost to build all 100, including every subassembly and shared part?"*

The tool walks the full BOM tree, **aggregates the total quantity needed for each leaf part
across all its occurrences** (e.g. the same screw pulled from three different subassemblies
sums into one demand figure), picks the price tier that actually applies at that aggregated
quantity, and shows an itemized breakdown plus a grand total.

This is a **new on-demand calculation**, not a change to the existing per-unit rollup.

## Design decisions (locked)

| # | Decision |
|---|---|
| 1 | **Two separate modes.** The existing qty=1 rollup (`rollupCost` → `last_rollup_cost`) is unchanged: preferred-supplier cheapest active price, no tier logic. The new "cost to build N" mode is a distinct calculation. |
| 2 | **Tier rule:** for aggregated qty *Q*, pick the active price row for the part's **default supplier** with the **largest `pack_size` ≤ Q**. |
| 3 | **Fallback:** if *Q* is below **every** tier's `pack_size` (no tier qualifies), the line is marked **`missing`** — user must add/fix pricing and rerun. No extrapolation, no `current_cost` fallback in this mode. |
| 4 | **Supplier scope:** default/preferred supplier only. |
| 5 | **Aggregation across paths:** a leaf part's demand is summed across *all* occurrences in the tree before the tier lookup (the whole point — shared screws consolidate). |
| 6 | **Subassemblies:** always expanded via their BOM (built, not bought) — same as today. A subassembly's own direct price rows are ignored; only true leaves (no BOM) get priced. |
| 6b | **No OPS/labor special-case.** Unlike `rollupCost`/`bomLeafCost` (which fall back to `current_cost` as an hourly rate for OPS parts), the build-cost calculation treats every leaf identically: look up active price rows for the default supplier, pick a tier, else `Missing`. OPS parts today have `default_supplier_id = NULL`, so OPS lines will always show `Missing` unless given a default supplier + active price row — this is intentional, not a bug to fix in this PR. |
| 7 | **Read-only.** This calculation does **not** write `last_rollup_cost` or any other column. `last_rollup_cost` stays the qty=1 preferred-price number that feeds the BOM tab, CSV export, and cost reporting (RPT-4). |
| 8 | **Output: consolidated buy-list (flat).** One row per **unique** leaf part, deduped across the whole tree — qty needed, tier `pack_size` used, unit price, ext cost, source — plus a grand total. Reads like the actual purchase order. **Not** an expanded BOM tree: a shared leaf's tier is a *global* property of its total qty, which a per-occurrence tree row would misrepresent. |

## Algorithm — two-pass consolidation

The current `rollupCost` memoizes on `pnid` alone, which assumes one part = one cost
everywhere. That breaks under qty-break pricing (same part, different aggregated qty → different
unit price). Replace the single recursive sum with a two-pass walk:

**Pass 1 — aggregate leaf demand.** Walk the tree from the root with a running quantity
multiplier (`parentQty × lineQty`). Accumulate into `map[pnid]float64` keyed by leaf part id.
Reuse the existing `visited` path-set for cycle detection. Subassemblies (has_bom) recurse;
true leaves accumulate their extended qty. Memoization here is trivial — we're just summing
into the map, so a plain recursive walk is fine (BOMs are small).

**Pass 2 — price each leaf at its aggregated qty.** For each `(pnid, totalQty)` in the map,
query the active price rows for the part's default supplier and select the largest
`pack_size ≤ totalQty`. Compute `unitPrice`, `extCost = unitPrice × totalQty`, and a source
label (`price` / `missing`). Sum `extCost` for the grand total.

New result types (in `parts.go` alongside `rollupResult`):

```go
type buildCostLine struct {
    PNID       int
    PartNumber string
    Title      string
    QtyNeeded  float64
    PackSize   float64  // tier applied (0 when missing)
    UnitPrice  float64
    ExtCost    float64
    Source     string   // "price" | "missing"
}
type buildCostResult struct {
    Lines []buildCostLine
    Total float64
    Cycle bool
}
```

A `buildCost(ctx, rootPNID, buildQty)` method does Pass 1 + Pass 2 and returns
`buildCostResult`. Keep it self-contained; do **not** refactor `rollupCost`.

## Backend

- **New method** `buildCost(ctx, pnid, qty)` in `arx_go/parts.go` (two passes above).
- **New handler** `PartBuildCost` — reads the target part + `qty` form/query param, calls
  `buildCost`, renders the breakdown. On cycle, reuse the existing cycle error message.
- **New route** in `main.go` next to the rollup route:
  `r.Get("/part/{id}/build-cost", h.PartBuildCost)` (GET with `?qty=` — read-only, safe to
  bookmark/share; no CSRF needed since it writes nothing).
- Price query mirrors the existing preferred-price subquery but returns **all** active rows
  for the default supplier so Pass 2 can pick the tier:
  `SELECT price_ea, pack_size FROM <price> WHERE part_id=@p1 AND is_active=1 AND supplier_id=<default_supplier_id> ORDER BY pack_size`.

## Frontend

**Recommended placement:** add a "Cost to Build" panel on the **BOM edit page**
(`templates/pm/part_bom_edit.html`), directly below the existing "Rollup Cost" section — this
is already the cost-management surface and keeps the qty=1 rollup and qty=N build cost visually
adjacent (matches the "toggle on the rollup" idea).

- A small form: numeric `qty` input (default 1) + **Calculate** button → `GET /part/{id}/build-cost?qty=N`.
- Results render on a dedicated page (`part_build_cost.html`) as a **flat consolidated buy-list** —
  a `table table-sm table-bordered`, **one row per unique leaf part** (deduped across the whole
  tree): Part Number → link, Title, Qty Needed (aggregated), Tier (`pack_size`), Unit Price,
  Ext Cost, Source badge (reusing the existing `price`/`missing` badge styles), with a `tfoot`
  grand total. A back link returns to the BOM edit page. Rows sorted by part number (stable output).
- Missing-price lines get the same `badge bg-warning text-dark` "Missing" treatment already used
  in `part_bom.html`, so the user can see exactly which parts need pricing before rerunning.

Layout mockup (locked):

```
Cost to build 100 × ASM-1001

Part        Title          Total Qty  Tier(pack)  Unit $   Ext $     Src
BUY-1001    M3x8 SHCS         500      1000        0.030    15.00    Price
BUY-1002    O-Ring 2-014      400       100        0.045    18.00    Price
RAW-1001    Aluminum Stock    200        10        2.500   500.00    Price
RAW-1002    Stainless Bar     100         —        ——        ——     MISSING
                                                   GRAND TOTAL   $533.00
```

## Test data (ArxDev seed — `SQL/seed_test_data.sql`)

Current seed has no leaf part with multiple **active** tiers, and no leaf shared across two
subassemblies — both are needed to exercise this feature. Extend the seed (human runs the reseed;
we only edit the script):

1. **Multi-tier pricing on the screw (3002 `BUY-1001`, default supplier 1002).** Add active
   qty-break rows alongside the existing `4203`. The `UQ_price_active_combo` index is scoped on
   `(part_id, supplier_id, pack_size)` so multiple active rows at different `pack_size` are legal:
   - pack_size 1 @ 0.10, pack_size 100 @ 0.05 (existing 4203), pack_size 1000 @ 0.03.
2. **Share the screw across a second subassembly.** Add a BOM line placing 3002 inside 3012
   (the sub-assembly), so building the top assembly 3005 pulls 3002 from both line 1 of 3005 and
   from 3012 — proving the cross-subassembly consolidation.
3. Pick a build qty in the test that makes the *aggregated* screw demand cross a tier boundary
   (e.g. lands in the 1000-pack tier) while a per-occurrence qty would not — the whole point of
   aggregating before the price lookup.

Update `SQL/schema.md` only if columns change — they don't here (pure data), so no schema doc edit.

## Tests

- **Unit** (`parts_test.go`, no DB): factor the tier-selection rule into a pure helper
  (`pickTier(rows []priceTier, qty float64) (unitPrice, packSize float64, ok bool)`) and table-test
  it: qty exactly on a boundary, between boundaries, below all tiers (→ missing), single-tier,
  empty. This is the highest-value regression guard (CLAUDE.md §5).
- **Integration** (`integration_test.go`, ArxDev, build-tagged): build-cost the seeded 3005 at a
  qty that crosses the screw's tier boundary; assert the consolidated screw qty, the tier chosen,
  and the grand total. Runs against ArxDev only (DSN `arxdev` guard already enforced).

## Out of scope

- `price_type` column / #222 — **not needed**. Tier selection keys off `pack_size`, which already
  exists; the #466 dependency on #222 is stale.
- No change to `rollupCost`, `bomLeafCost`, the BOM tab, CSV export, or `last_rollup_cost` semantics.
- No new schema/migration — data-only seed additions.

## Verify

`go build`, `go vet`, `go test ./...` from `arx_go/`; then the integration test against ArxDev
(after the user reseeds). Manual: open a top-level assembly's BOM edit page, enter a build qty,
confirm the shared leaf consolidates and the tier/total are correct, and that a below-all-tiers
part shows "Missing".
