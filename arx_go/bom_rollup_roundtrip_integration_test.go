//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// bomGraph builds throwaway parts and BOM lines for the #278 tests; cleanup removes them.
type bomGraph struct {
	t       *testing.T
	h       *Handler
	base    string
	ids     []int
	cleanup func()
}

func newBOMGraph(t *testing.T, h *Handler) *bomGraph {
	g := &bomGraph{t: t, h: h, base: smokeUniq("ITEST-RT")}
	ctx := context.Background()
	g.cleanup = func() {
		for _, id := range g.ids {
			smokeExec(ctx, h, `DELETE FROM bom WHERE parent_part_id=$1 OR component_part_id=$1`, id)
			smokeExec(ctx, h, `DELETE FROM price WHERE part_id=$1`, id)
		}
		for _, id := range g.ids {
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
	}
	t.Cleanup(g.cleanup)
	return g
}

func (g *bomGraph) scan(q string, args ...any) int {
	g.t.Helper()
	var id int
	if err := g.h.queryRowContext(context.Background(), q, args...).Scan(&id); err != nil {
		g.t.Fatalf("seed: %v", err)
	}
	return id
}

// asm adds an ASM part.
func (g *bomGraph) asm(name string) int {
	id := g.scan(`INSERT INTO part (part_number, category) VALUES ($1,'ASM') RETURNING id`, g.base+"-"+name)
	g.ids = append(g.ids, id)
	return id
}

// leaf adds a BUY part with current_cost 0.4 and one active 2.0@1 price from supplier 1001 (its default).
func (g *bomGraph) leaf(name string) int {
	id := g.scan(`INSERT INTO part (part_number, category, current_cost, default_supplier_id) VALUES ($1,'BUY',0.4,1001) RETURNING id`,
		g.base+"-"+name)
	g.ids = append(g.ids, id)
	g.scan(`INSERT INTO price (part_id, supplier_id, price_ea, pack_size, is_active) VALUES ($1,1001,2.0,1,TRUE) RETURNING id`, id)
	return id
}

func (g *bomGraph) line(parent, comp, n int, qty float64) {
	g.t.Helper()
	g.scan(`INSERT INTO bom (parent_part_id, component_part_id, line_number, qty) VALUES ($1,$2,$3,$4) RETURNING id`, parent, comp, n, qty)
}

// repeatedSub: T -> S (1), M (2), S (5); M -> S (3); S -> L (4). S occurs in three places.
type repeatedSub struct{ T, M, S, L int }

func seedRepeatedSub(t *testing.T, h *Handler) repeatedSub {
	g := newBOMGraph(t, h)
	f := repeatedSub{T: g.asm("T"), M: g.asm("M"), S: g.asm("S"), L: g.leaf("L")}
	g.line(f.T, f.S, 1, 1)
	g.line(f.T, f.M, 2, 2)
	g.line(f.T, f.S, 3, 5)
	g.line(f.M, f.S, 1, 3)
	g.line(f.S, f.L, 1, 4)
	return f
}

// seedWideBOM: root T with k sub-assemblies, each with one priced leaf line.
func seedWideBOM(t *testing.T, h *Handler, k int) (root int, assemblies []int) {
	g := newBOMGraph(t, h)
	root = g.asm("T")
	assemblies = []int{root}
	for i := 1; i <= k; i++ {
		s := g.asm(fmt.Sprintf("S%d", i))
		g.line(root, s, i, 1)
		g.line(s, g.leaf(fmt.Sprintf("L%d", i)), 1, 2)
		assemblies = append(assemblies, s)
	}
	return root, assemblies
}

func readRollup(t *testing.T, h *Handler, id int) (sql.NullFloat64, sql.NullTime) {
	t.Helper()
	var cost sql.NullFloat64
	var at sql.NullTime
	if err := h.queryRowContext(context.Background(),
		`SELECT last_rollup_cost, last_rollup_at FROM part WHERE id=$1`, id).Scan(&cost, &at); err != nil {
		t.Fatalf("read rollup of part %d: %v", id, err)
	}
	return cost, at
}

func postRollup(h *Handler, ctx context.Context, id int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/part/%d/rollup-cost", id), nil)
	req = withID(req.WithContext(ctx), id)
	rec := httptest.NewRecorder()
	h.PartRollupCost(rec, req)
	return rec
}

func withStats(ctx context.Context) (context.Context, *sqlStats) {
	st := &sqlStats{}
	return context.WithValue(ctx, ctxSQLStatsKey, st), st
}

// ── Characterization: pass before and after the change ───────────────────────

func TestIntegration_RollupCost_RepeatedSubAssembly(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f := seedRepeatedSub(t, h)

	memo := map[int]rollupResult{}
	res, err := h.rollupCost(context.Background(), f.T, map[int]bool{}, memo)
	if err != nil {
		t.Fatal(err)
	}
	if res.cycle {
		t.Error("cycle = true, want false")
	}
	assertFloatEqual(t, "rollupCost(T)", res.cost, 96) // S=8, M=24, T=8+48+40
	assertFloatEqual(t, "memo[S]", memo[f.S].cost, 8)
	assertFloatEqual(t, "memo[M]", memo[f.M].cost, 24)
	if len(memo) != 3 {
		t.Errorf("memo = %v, want exactly T, M and S", memo)
	}
}

func TestIntegration_BuildCost_RepeatedSubAssembly(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f := seedRepeatedSub(t, h)

	for _, c := range []struct{ qty, wantQty, wantTotal float64 }{{1, 48, 96}, {10, 480, 960}} {
		res, err := h.buildCost(context.Background(), f.T, c.qty)
		if err != nil {
			t.Fatal(err)
		}
		if res.Cycle || len(res.Lines) != 1 || res.Lines[0].PNID != f.L {
			t.Fatalf("buildCost(T, %v) = %+v, want one line for L", c.qty, res)
		}
		assertFloatEqual(t, "QtyNeeded", res.Lines[0].QtyNeeded, c.wantQty)
		assertFloatEqual(t, "UnitPrice", res.Lines[0].UnitPrice, 2.0)
		assertFloatEqual(t, "Total", res.Total, c.wantTotal)
	}
}

func TestIntegration_PartRollupCost_RepeatedSubAssembly(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f := seedRepeatedSub(t, h)

	assertStatus(t, "PartRollupCost", postRollup(h, context.Background(), f.T), http.StatusSeeOther)
	costT, atT := readRollup(t, h, f.T)
	costM, atM := readRollup(t, h, f.M)
	costS, atS := readRollup(t, h, f.S)
	assertFloatEqual(t, "T", costT.Float64, 96)
	assertFloatEqual(t, "M", costM.Float64, 24)
	assertFloatEqual(t, "S", costS.Float64, 8)
	if !atT.Valid || !atT.Time.Equal(atM.Time) || !atT.Time.Equal(atS.Time) {
		t.Errorf("last_rollup_at T=%v M=%v S=%v, want one shared timestamp", atT, atM, atS)
	}
	if c, at := readRollup(t, h, f.L); c.Valid || at.Valid {
		t.Errorf("leaf rollup = {%v %v}, want untouched NULL", c, at)
	}
}

func TestIntegration_RollupCost_EmptyBOMRoot(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	root := newBOMGraph(t, h).asm("E")

	memo := map[int]rollupResult{}
	res, err := h.rollupCost(context.Background(), root, map[int]bool{}, memo)
	if err != nil {
		t.Fatal(err)
	}
	if res.cycle || res.cost != 0 || len(memo) != 1 {
		t.Errorf("rollupCost(empty) = %+v memo=%v, want cost 0, no cycle, memo {root}", res, memo)
	}
	assertStatus(t, "PartRollupCost", postRollup(h, context.Background(), root), http.StatusSeeOther)
	if c, at := readRollup(t, h, root); !c.Valid || c.Float64 != 0 || !at.Valid {
		t.Errorf("empty root rollup = {%v %v}, want 0 with a timestamp", c, at)
	}
}

func TestIntegration_Cycles_NonRootAndLongerShapes(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for name, build := range map[string]func(g *bomGraph) (root int, all []int){
		"cycle below root": func(g *bomGraph) (int, []int) {
			r, a, b := g.asm("R"), g.asm("A"), g.asm("B")
			g.line(r, a, 1, 1)
			g.line(a, b, 1, 1)
			g.line(b, a, 1, 1)
			return r, []int{r, a, b}
		},
		"three node": func(g *bomGraph) (int, []int) {
			a, b, c := g.asm("A"), g.asm("B"), g.asm("C")
			g.line(a, b, 1, 1)
			g.line(b, c, 1, 1)
			g.line(c, a, 1, 1)
			return a, []int{a, b, c}
		},
		"self loop": func(g *bomGraph) (int, []int) {
			a := g.asm("A")
			g.line(a, a, 1, 1)
			return a, []int{a}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			root, all := build(newBOMGraph(t, h))

			res, err := h.rollupCost(ctx, root, map[int]bool{}, map[int]rollupResult{})
			if err != nil || !res.cycle {
				t.Errorf("rollupCost = %+v, %v; want cycle", res, err)
			}
			bc, err := h.buildCost(ctx, root, 1)
			if err != nil || !bc.Cycle {
				t.Errorf("buildCost = %+v, %v; want Cycle", bc, err)
			}
			postRollup(h, ctx, root)
			for _, id := range all {
				if c, at := readRollup(t, h, id); c.Valid || at.Valid {
					t.Errorf("part %d rollup = {%v %v}, want nothing written", id, c, at)
				}
			}
		})
	}
}

// ── Round trips (#278): fail before the change ───────────────────────────────

func TestIntegration_RollupCost_OneRead(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f := seedRepeatedSub(t, h)
	ctx, st := withStats(context.Background())
	if _, err := h.rollupCost(ctx, f.T, map[int]bool{}, map[int]rollupResult{}); err != nil {
		t.Fatal(err)
	}
	if st.count != 1 {
		t.Errorf("rollupCost issued %d round trips, want 1 (tree read)", st.count)
	}
}

func TestIntegration_BuildCost_ReadsIndependentOfRepeats(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f := seedRepeatedSub(t, h)
	ctx, st := withStats(context.Background())
	if _, err := h.buildCost(ctx, f.T, 1); err != nil {
		t.Fatal(err)
	}
	if st.count != 3 {
		t.Errorf("buildCost issued %d round trips, want 3 (tree + parts + price tiers)", st.count)
	}
}

func TestIntegration_BuildCost_SeedTreeReads(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx, st := withStats(context.Background())
	if _, err := h.buildCost(ctx, 3005, 300); err != nil {
		t.Fatal(err)
	}
	if st.count != 3 {
		t.Errorf("buildCost(3005) issued %d round trips, want 3", st.count)
	}
}

func TestIntegration_PartRollupCost_RoundTripsIndependentOfAssemblyCount(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	counts := map[int]int{}
	for _, k := range []int{1, 4} {
		root, all := seedWideBOM(t, h, k)
		ctx, st := withStats(context.Background())
		assertStatus(t, "PartRollupCost", postRollup(h, ctx, root), http.StatusSeeOther)
		counts[k] = st.count

		var shared time.Time
		for i, id := range all {
			c, at := readRollup(t, h, id)
			want := 4.0 // each sub-assembly: 2 x 2.0
			if i == 0 {
				want = 4.0 * float64(k)
			}
			assertFloatEqual(t, fmt.Sprintf("k=%d part %d", k, id), c.Float64, want)
			if i == 0 {
				shared = at.Time
			} else if !at.Valid || !at.Time.Equal(shared) {
				t.Errorf("k=%d part %d last_rollup_at = %v, want shared %v", k, id, at, shared)
			}
		}
	}
	if counts[1] != counts[4] {
		t.Errorf("round trips grew with assembly count: k=1 -> %d, k=4 -> %d", counts[1], counts[4])
	}
}
