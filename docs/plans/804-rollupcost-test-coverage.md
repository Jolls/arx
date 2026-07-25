# 804: Test coverage for rollupCost / PartRollupCost

## Approach

Follow the existing `arx_go/integration_test.go` pattern used for `buildCost`
(build-tagged `integration`, live ArxDev, `liveHandler(t)` + `withID`). There is
no sqlmock or DB-mocking library in this codebase — `rollupCost` and
`PartRollupCost` are DB-driven recursive functions with no pure-Go seam, so unit
testing would require inventing a new harness. Match the established
convention instead: all new tests go in `arx_go/integration_test.go`, gated by
`//go:build integration`, run manually against ArxDev exactly like the
`buildCost` tests.

All tests are read-only against the static seed EXCEPT the cycle test, which
follows `TestIntegration_BuildCostCycleDetection`'s pattern: insert two
throwaway parts + BOM rows with a `time.Now().UnixNano()` suffix on the part
number, defer-delete both `bom` and `part` rows.

## Seed data to reuse (SQL/seed_test_data.sql)

Part 3005 (ASM-1001, Widget Assembly) BOM:
- line 3901: component 3002 (BUY-1001, screw), qty 2
- line 3902: component 3003 (BUY-1002, O-ring), qty 4
- line 3903: component 3006 (OPS-1001, labor), qty 0.5
- line 3907: component 3012 (sub-assembly), qty 1

Part 3012 (ASM-1002, Widget Sub-Assembly) BOM:
- line 3905: component 3001 (RAW-1001), qty 2
- line 3906: component 3007 (RAW-1002), qty 1
- line 3908: component 3002 (BUY-1001, screw — same leaf 3005 uses directly), qty 3

Leaf cost inputs (`current_cost` / active price rows for `default_supplier_id`):
- 3002: default_supplier 1002; active price rows 4203 (0.05), 4204 (0.10), 4205
  (0.03) all supplier 1002 → `preferred_price` = `MIN(price_ea)` = **0.03**
  (rollupCost's leaf-cost query is not qty-tier-aware, unlike `buildCost` —
  it always takes the global MIN across all active tiers for the default
  supplier; this is a legacy quirk, not something to fix here — assert it as
  documented behavior).
- 3003: default_supplier 1001, no price rows exist for 3003 → preferred_price
  NULL → falls back to `current_cost` = **0.12**.
- 3006 (OPS labor): `default_supplier_id` is NULL → preferred_price subquery
  matches nothing → NULL → falls back to `current_cost` = **35.00**.
- 3001: default_supplier 1001; price row 4201 active, price_ea 2.50 →
  preferred_price = **2.50**.
- 3007: default_supplier 1001, no price rows → falls back to `current_cost` =
  **4.10**.

Expected rollup costs (verify these against a fresh read of
`SQL/seed_test_data.sql` before hardcoding into assertions — the data above
was read at plan-writing time and could drift):
- `rollupCost(3012)` = 2×2.50 + 1×4.10 + 3×0.03 = 5.00 + 4.10 + 0.09 = **9.19**
- `rollupCost(3005)` = 2×0.03 + 4×0.12 + 0.5×35.00 + 1×9.19
  = 0.06 + 0.48 + 17.50 + 9.19 = **27.23**

## Test cases to add (arx_go/integration_test.go)

All new tests go after `TestIntegration_BuildCostDoesNotWriteRollup` (end of
the buildCost test block), same style: doc comment explaining what's being
guarded and why, table-free straight-line assertions matching the existing
tests' style.

### 1. `TestIntegration_RollupCostNested`
Calls `h.rollupCost(ctx, 3005, map[int]bool{}, map[int]rollupResult{})`
directly (unexported function, same-package test — no HTTP layer).
- Assert `res.cycle == false`.
- Assert `res.cost` ≈ 27.23 (use a float tolerance comparison, e.g.
  `math.Abs(res.cost-27.23) > 0.0001`, matching float comparisons already used
  elsewhere in this file — grep for existing float assertion style and mirror
  it).
- This single test covers both "flat BOM" (3012's leg) and "nested BOM"
  (3005→3012 recursion) since 3012 has no further sub-assemblies — no separate
  flat-only part is needed; note in the test comment that 3012's contribution
  IS the flat-BOM case, folded into the nested assertion by calling
  `rollupCost` on 3012 directly too:
  - Also call `h.rollupCost(ctx, 3012, map[int]bool{}, map[int]rollupResult{})`
    in the same test (fresh visited/memo maps) and assert its cost ≈ 9.19.
    This is the "simple flat BOM" case (test requirement #1) — 3012's BOM has
    no sub-assemblies of its own.

### 2. `TestIntegration_RollupCostMemoization`
Calls `h.rollupCost(ctx, 3005, map[int]bool{}, memo)` with a `memo` map the
test owns.
- After the call, assert `memo[3012]` is present and `memo[3012].cost` ≈ 9.19
  — proves the shared leaf/sub-assembly 3012 was memoized rather than
  recomputed (3012 is only referenced once in this seed, so to actually
  exercise reuse-not-recompute, this test must instead pre-seed `memo` with a
  poisoned/sentinel value for 3012 before calling `rollupCost(3005, ...)` and
  assert `rollupCost` returns that sentinel unchanged rather than recomputing
  9.19). Concretely:
  - `memo := map[int]rollupResult{3012: {cost: 999, cycle: false}}`
  - call `rollupCost(ctx, 3005, map[int]bool{}, memo)`
  - assert the returned root cost reflects `9.19`-replaced-by-`999` inflow,
    i.e. total ≈ 0.06 + 0.48 + 17.50 + 999 = **1017.04** — proving the
    pre-seeded memo entry was reused verbatim instead of the DB being
    re-queried and recomputed to 9.19.

### 3. `TestIntegration_RollupCostCycleDetection`
Same throwaway-parts pattern as `TestIntegration_BuildCostCycleDetection`:
insert 2 parts (unique part_number suffixed with `time.Now().UnixNano()`),
insert `bom` rows A→B and B→A, defer-delete both tables' rows.
- Call `h.rollupCost(ctx, idA, map[int]bool{}, map[int]rollupResult{})`.
- Assert `err == nil` and `res.cycle == true`.

### 4. `TestIntegration_PartRollupCostHandler`
Exercises the HTTP handler on part 3005 (success path):
- Read `last_rollup_cost`/`last_rollup_at` for 3005 AND 3012 before, via
  direct `h.DB().QueryRowContext`.
- `req := withID(httptest.NewRequest(http.MethodPost, "/part/3005/rollup-cost", nil), 3005)`
  then `rec := httptest.NewRecorder(); h.PartRollupCost(rec, req)`.
- Assert `rec.Code == http.StatusSeeOther`.
- Read `last_rollup_cost`/`last_rollup_at` for 3005 and 3012 after: assert
  3005's cost ≈ 27.23, 3012's cost ≈ 9.19, and both `last_rollup_at` values
  are non-null, equal to each other (single shared timestamp per the code
  comment), and newer than before.
- Cleanup: restore both parts' `last_rollup_cost`/`last_rollup_at` to their
  pre-test values in a `defer` (this test permanently mutates seed data
  otherwise — unlike the read-only buildCost tests, this one must clean up).

### 5. `TestIntegration_PartRollupCostHandlerCycleDoesNotWrite`
Same throwaway two-part cycle setup as test #3 (own copy, or extract a shared
helper if the existing `TestIntegration_BuildCostCycleDetection` setup can be
reused — see open question below).
- Before calling the handler, read `last_rollup_cost`/`last_rollup_at` for
  both throwaway parts (expect NULL/NULL, just inserted).
- `req := withID(httptest.NewRequest(http.MethodPost, "/part/{idA}/rollup-cost", nil), idA)`
  then invoke `h.PartRollupCost(rec, req)`.
- Assert the response body/error indicates the cycle rejection (matches
  `"BOM contains a cycle"` per the handler's error string).
- Re-read `last_rollup_cost`/`last_rollup_at` for both parts: assert both are
  still NULL — proving the transaction never committed.

### 6. Leaf cost fallback coverage
Not a new test — already covered as a side effect of test #1's assertions
(3002 exercises `preferred_price` MIN-across-tiers, 3003 and 3007 exercise
`current_cost` fallback, 3006 exercises the labor/`current_cost` fallback via
NULL `default_supplier_id`). No separate test needed; add a one-line comment
on test #1 pointing this out so a future reader doesn't think leaf-cost
fallback is untested.

## Resolved decisions

1. Test #4 (`TestIntegration_PartRollupCostHandler`): use write-then-restore-
   in-defer against seed parts 3005/3012, as originally planned. No throwaway
   BOM needed.
2. Test #5: extract a shared helper `seedCyclePair(t, h, ctx) (idA, idB int, cleanup func())`
   in `integration_test.go` and use it for test #3, test #5, AND refactor the
   existing `TestIntegration_BuildCostCycleDetection` to use it too (dedup all
   three occurrences, not just the two new ones).
