# Plan: Issue #280 — Recursive BOM Cost Roll-up (ENG-3)

**Issue:** [#280](https://github.com/Jolls/arx-legacy/issues/280) Cost Roll-up UI  
**Milestone:** v0.6 Major Feature Release

## Acceptance criteria (from issue)

- Run Rollup button triggers BOM tree traversal, computing rolled cost from child part costs and quantities
- Result written to `PNLastRollupCost`; timestamp of last run displayed
- BOM tab shows each child line with its unit cost contribution
- Delta between `PNLastRollupCost` and last purchase price highlighted if significant

## What exists today

`PartRollupCost` (parts.go:724) does a single-level flat SUM:

```sql
SELECT ISNULL(SUM(pn.PNCurrentCost * pl.PLQty), 0)
FROM PL pl JOIN PN pn ON pl.PLPartID = pn.PNID
WHERE pl.PLListID = @p1
```

It uses `PNCurrentCost` (purchase price) on each child — sub-assemblies are not recursed into.

No schema changes are needed. `PNLastRollupCost` (DECIMAL, NULL = never run) and `PNLastRollupAt` already exist on the `PN` table.

## Algorithm choice: Go recursion (not SQL CTE)

**Decision: implement recursion in Go, always-recurse-full-tree with memoization.**

- Cycle detection is the deciding factor. A SQL Server recursive CTE hard-errors at depth 100 and cannot report which part caused the cycle. In Go a path-scoped `visited` map catches cycles precisely.
- Always-recurse ensures rollup results are never stale — stored `PNLastRollupCost` values on child parts are ignored during the walk (they are written as a side effect, not read as input).
- A `memo map[int]rollupResult` prevents recomputing any part that appears more than once in the tree (e.g. Part C referenced by both Part B and Part D). The memo is global to the whole walk; once computed, a node's result is reused on any subsequent encounter.
- Per-line cost-source info (rollup vs. purchase vs. missing) is easiest to build during the Go walk.
- BOMs in this app are small; per-node query cost is negligible.

## Cost source decision

### Long-term target architecture

The long-term goal (see `docs/FUTURE_GOALS.md`) is that **all parts get their cost from the `price` table**, unified through `price.is_preferred` (#223):

- **BUY** parts — supplier is the vendor; `price_ea` on the preferred row is the purchase price.
- **MFG / RAW** parts — your own company is the supplier; `price_ea` is the internal manufacturing/material cost.
- **ASM** parts — your own company is the supplier; `price_ea` is the **value-add** (labor, overhead) on top of the BOM rollup. Total ASM cost = recursive child rollup + value-add `price_ea`.

Once this is in place and `price.is_preferred` (#223) exists, `PN.PNCurrentCost` is fully redundant and can be dropped. It is explicitly a transitional field — new features should not extend its use.

### What's in the schema today

- **`PN.PNCurrentCost`** — static decimal on the part, manually maintained. Transitional fallback only.
- **`price` table** — quantity-break pricing rows per part/supplier. Key columns: `part_id`, `supplier_id`, `price_ea`, `pack_size`, `is_active`, `effective_date`. `price.is_preferred` does not exist yet (blocked on #223); `PN.price_id` is a stale pointer being replaced by it.

### Phase 1 — implement now (`PNCurrentCost` fallback)

For the initial rollup implementation, use `PNCurrentCost` as the leaf cost. This matches current behavior and requires no dependency on #223.

- Add a UI note that rollup cost reflects `PNCurrentCost`; link to the Pricing tab.
- Do **not** add new code paths that read or write `PNCurrentCost` beyond what's needed for the rollup query — avoid deepening the dependency.

### Phase 2 — after `price.is_preferred` (#223)

Switch the leaf-cost query to: preferred `price` row (`is_preferred = 1 AND is_active = 1`), falling back to `PNCurrentCost` only when no preferred row exists. No algorithm change — just the query inside `rollupCost` gains a LEFT JOIN to `price`.

For ASM parts that have a value-add `price` row (your company as supplier), add that `price_ea` to the recursed BOM total:

```
ASM cost = rollupCost(children) + price_ea (value-add row, if any)
```

This is a one-line change inside the recursive walk once the price rows exist.

### Phase 3 — qty-break aware (deferred)

Qty-break pricing (choosing the right `price_ea` based on extended BOM quantity) breaks the memo map — the same part at two different extended quantities has two different costs, so memoization would need to be keyed on `(pnid, qty)` or dropped. Defer until qty-break pricing is a real user need. Flag in a future issue rather than implementing now.

### `CostSource` values in the BOM view

| Value | Meaning |
|---|---|
| `"rollup"` | Child is an assembly; cost was recursively computed (Phase 1+) |
| `"rollup+valueadd"` | Assembly with a value-add price row added on top (Phase 2+) |
| `"price"` | Leaf with a preferred `price` row (Phase 2+) |
| `"current_cost"` | Leaf using `PNCurrentCost` fallback (Phase 1, or fallback in Phase 2) |
| `"missing"` | Leaf with zero/NULL cost and no price row; contribution is 0 |

---

## Implementation steps

### 1. New helper — `rollupCost` in `parts.go`

```go
type rollupResult struct {
    cost  float64
    cycle bool
}

func (h *Handler) rollupCost(ctx context.Context, pnid int, visited map[int]bool, memo map[int]rollupResult) (rollupResult, error)
```

Two maps are passed through the entire walk:
- `visited` — path-scoped (defer-delete on return). Detects cycles only.
- `memo` — global to the whole walk, never deleted. Caches any node's result so a part that appears in the tree multiple times is only computed once.

Rules:
1. If `pnid` is in `visited` → return `{cycle: true}` (cycle detected; break).
2. If `pnid` is in `memo` → return `memo[pnid]` immediately (already computed this walk).
3. `visited[pnid] = true`; `defer delete(visited, pnid)` — path-scoped, not global, so siblings are not falsely flagged.
4. Query direct children, selecting `PLPartID`, `PLQty`, `PNCurrentCost`, and `EXISTS(child has its own BOM)`. **Do not read `PNLastRollupCost` here — always recurse.**
5. For each child, pick unit cost:
   - Child has its own BOM → recurse into `rollupCost(child)`; use result. Propagate `cycle` flag. *(Phase 2: also add child's value-add `price_ea` if it has one.)*
   - Leaf → use `PNCurrentCost` (0 if NULL). *(Phase 2: prefer preferred `price` row; fall back to `PNCurrentCost`.)*
6. `total += unitCost * PLQty`; OR-accumulate the `cycle` flag.
7. Store result in `memo[pnid]` before returning.

The root call passes both as empty maps:
```go
res, err := h.rollupCost(ctx, pnid, map[int]bool{}, map[int]rollupResult{})
```

`PNLastRollupCost` on child parts is never read during the walk — it is only ever written as a side effect of running rollup on that part directly. This ensures the result is always fully recomputed and never stale.

### 2. New fields on `BOMItem` (`models/part.go`)

```go
LineUnitCost float64  // cost used for this line (rolled or purchase)
LineExtCost  float64  // LineUnitCost * PLQty
CostSource   string   // "rollup" | "purchase" | "missing"
ChildHasBOM  bool     // child is itself an assembly
```

`CostSource` values — see "Cost source decision" section for full semantics per phase. Phase 1 values: `"rollup"`, `"current_cost"`, `"missing"`. Phase 2 adds: `"rollup+valueadd"`, `"price"`.

Keep existing `PNCurrentCost` field for Phase 1 — still used in `part_bom.html` and `PartWhereUsed`. Do not add new uses of it.

### 3. `PartRollupCost` handler (parts.go:724)

Replace the flat SUM:

```go
func (h *Handler) PartRollupCost(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    pnid, err := strconv.Atoi(id)
    // ...
    res, err := h.rollupCost(r.Context(), pnid, map[int]bool{}, map[int]rollupResult{})
    if res.cycle {
        h.renderError(w, r, "BOM contains a cycle — fix the BOM before running rollup.")
        return
    }
    h.execContext(r.Context(), fmt.Sprintf(
        `UPDATE %s SET PNLastRollupCost=@p1, PNLastRollupAt=@p2 WHERE PNID=@p3`, pn,
    ), res.cost, time.Now(), pnid)
    http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusSeeOther)
}
```

### 4. `PartBOM` handler — per-line cost source for the read-only view

Extend the BOM query to also select `PNLastRollupCost` (the last written result for display) and a `ChildHasBOM` expression:

```sql
SELECT pl.PLItem, pl.PLQty, pl.PLPartID,
       pn.part_number, pn.title, pn.revision, pn.category,
       pn.PNCurrentCost, pn.PNLastRollupCost, pn.PNLastRollupAt,
       CASE WHEN EXISTS(SELECT 1 FROM {PL} c WHERE c.PLListID = pn.PNID) THEN 1 ELSE 0 END AS ChildHasBOM
FROM {PL} pl JOIN {PN} pn ON pl.PLPartID = pn.PNID
WHERE pl.PLListID = @p1
ORDER BY pl.PLItem
```

In Go, after scanning, set `LineUnitCost`, `CostSource`, `LineExtCost` per row using the same rules as the walk: if `ChildHasBOM`, use `PNLastRollupCost` (the stored display value) and mark `"rollup"`; else use `PNCurrentCost` and mark `"purchase"` or `"missing"`. Sum all `LineExtCost` and pass as `BOMTotal float64` to the template.

Note: the view uses the **stored** `PNLastRollupCost` per child for display purposes — it is not re-walking the tree. This means the BOM view shows what the last rollup computed, not a live recompute. Running rollup updates those stored values and then redirects here.

### 5. `part_bom.html` — per-line contribution + source badge

Add columns to the existing table:

| New header | Content |
|---|---|
| Unit Cost | `{{printf "$%.4f" .LineUnitCost}}` |
| Ext Cost | `{{printf "$%.4f" .LineExtCost}}` |
| Source | Bootstrap badge (see below) |

Badge variants:
- `"rollup"` → `<span class="badge bg-info text-dark">Rollup</span>`
- `"purchase"` → `<span class="badge bg-secondary">Purchase</span>`
- `"missing"` → `<span class="badge bg-warning text-dark">No rollup</span>`

Add a footer row showing `BOMTotal`.

Keep existing Bootstrap table classes. Use `text-end` utility on new numeric columns.

### 6. `part_detail.html` — delta display

In the "Pricing & Quantities" section (around line 102), after the Last Rollup Cost row, add a delta row. Compute in the `PartDetail` handler:

```go
RollupDelta    float64  // PNLastRollupCost - PNCurrentCost
RollupDeltaPct float64  // as a percentage of PNCurrentCost
RollupSignificant bool  // abs(pct) >= 0.05 and both costs > 0
```

*Phase 2 note: once `price.is_preferred` exists, the delta comparison should be `PNLastRollupCost` vs. the preferred `price_ea` row (not `PNCurrentCost`). The handler computation changes; the template does not.*

Template:
```html
{{if .Part.PNLastRollupAt}}
<div class="detail-row">
  <strong>Rollup vs Current Δ:</strong>
  <span class="{{if .RollupSignificant}}badge bg-warning text-dark{{end}}">
    {{printf "$%.4f" .RollupDelta}} ({{printf "%.1f%%" .RollupDeltaPct}})
  </span>
</div>
{{end}}
```

### 7. `part_bom_edit.html` — footer note

Update the footer note text (currently "Sums PNCurrentCost × Qty for each direct BOM component") to:
> "Recursively rolls up child costs from leaves — each sub-assembly is fully recomputed. Parts appearing multiple times in the tree are computed once and reused."

---

## Cycle and missing cost handling

| Scenario | Behavior |
|---|---|
| Cycle detected | Error and refuse to save (`renderError`). Report which part closed the cycle. |
| Leaf with NULL/zero cost | Contributes 0; flagged `"missing"` on BOM view. |
| Assembly child, no rollup yet | Recurse into it; if its children are all missing too, flag `"missing"`. |
| Assembly child, has rollup | Always recurse — stored rollup is ignored during the walk, recomputed fresh. |

---

## Ambiguities to resolve before starting

1. ~~**Force-deep vs. prefer-stored-rollup**~~ — **resolved: always-recurse with memoization.** Stored `PNLastRollupCost` on child parts is never read during the walk; the full tree is always recomputed fresh. Parts appearing multiple times are computed once via the memo map.
2. **Cycle handling policy** — refuse + error (recommended) vs. save best-effort total with a warning banner.
3. **"Significant" delta threshold** — default 5%. Confirm the percentage, and behavior when `PNCurrentCost = 0` (no purchase price → hide delta row or show absolute only).
4. ~~**Which cost source to use for leaf parts?**~~ — **resolved:** Phase 1 uses `PNCurrentCost` (no new dependencies). Phase 2 switches to preferred `price` row + your-company value-add for ASM parts, after `price.is_preferred` (#223) is implemented. See "Cost source decision" section.
5. **BOM view depth** — show only direct children's stored values (recommended) or render a full nested tree in the view.

---

## Files to change

| File | Change |
|---|---|
| `arx_go/parts.go` | Add `rollupCost` helper; rewrite `PartRollupCost`; extend `PartBOM` query; add delta fields in `PartDetail` |
| `arx_go/models/part.go` | Add `LineUnitCost`, `LineExtCost`, `CostSource`, `ChildHasBOM` to `BOMItem` |
| `arx_go/templates/pm/part_bom.html` | New columns, source badges, total footer |
| `arx_go/templates/pm/part_detail.html` | Delta row in pricing section |
| `arx_go/templates/pm/part_bom_edit.html` | Footer note text |
