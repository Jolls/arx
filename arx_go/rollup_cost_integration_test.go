//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// The seedBOM fixture (bom_integration_test.go) as a cost tree: P = 2×L1 + 0.5×L2 + 1×S + 3×L3, S = 4×L1.
// L1's only qualifying prices are supplier 1001's active 0.30@1 and 0.25@10 — its inactive 0.10 and
// supplier 1002's 0.05 must be ignored. L2 is labor (35, no supplier), L3 has every column NULL, and
// S's stored rollup (3.5) is stale.

// TestIntegration_RollupCost_Fixture: leaves cost their lowest default-supplier price, else current_cost,
// else 0; a sub-assembly is rolled up afresh, not read from its stored rollup.
func TestIntegration_RollupCost_Fixture(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	f, cleanupBOM := seedBOM(t, h)
	defer cleanupBOM()
	ctx := context.Background()

	resS, err := h.rollupCost(ctx, f.S, map[int]bool{}, map[int]rollupResult{})
	if err != nil {
		t.Fatalf("rollupCost(S): %v", err)
	}
	assertFloatEqual(t, "rollupCost(S)", resS.cost, 1.0) // 4 × 0.25

	memo := map[int]rollupResult{}
	resP, err := h.rollupCost(ctx, f.P, map[int]bool{}, memo)
	if err != nil {
		t.Fatalf("rollupCost(P): %v", err)
	}
	if resP.cycle {
		t.Error("rollupCost(P): cycle = true, want false")
	}
	assertFloatEqual(t, "rollupCost(P)", resP.cost, 19.0) // 2×0.25 + 0.5×35 + 1×1.0 + 3×0
	if len(memo) != 2 {
		t.Errorf("rollupCost(P) memo = %v, want exactly P and S", memo)
	}
}

// TestIntegration_BuildCost_Fixture: leaf demand is consolidated across the tree, priced at the default
// supplier's largest qualifying active tier; a leaf without a supplier or tiers is "missing".
func TestIntegration_BuildCost_Fixture(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	f, cleanupBOM := seedBOM(t, h)
	defer cleanupBOM()

	res, err := h.buildCost(context.Background(), f.P, 10)
	if err != nil {
		t.Fatalf("buildCost(P, 10): %v", err)
	}
	want := buildCostResult{
		Lines: []buildCostLine{
			// 2×10 direct + 1×10×4 via S = 60 → the 10-pack tier.
			{PNID: f.L1, PartNumber: f.PN[f.L1], Description: "l1 desc", QtyNeeded: 60, PackSize: 10, UnitPrice: 0.25, ExtCost: 15, Source: "price"},
			{PNID: f.L2, PartNumber: f.PN[f.L2], Description: "labor desc", QtyNeeded: 5, Source: "missing"},
			{PNID: f.L3, PartNumber: f.PN[f.L3], Description: "", QtyNeeded: 30, Source: "missing"},
		},
		Total: 15,
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("buildCost(P, 10):\n got %+v\nwant %+v", res, want)
	}
}

// TestIntegration_PartRollupCost_Fixture: the handler writes the fresh rollup to P and S with one
// timestamp and leaves the leaves untouched.
func TestIntegration_PartRollupCost_Fixture(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	f, cleanupBOM := seedBOM(t, h)
	defer cleanupBOM()
	ctx := context.Background()

	req := withID(httptest.NewRequest(http.MethodPost, fmt.Sprintf("/part/%d/rollup-cost", f.P), nil), f.P)
	rec := httptest.NewRecorder()
	h.PartRollupCost(rec, req)
	assertStatus(t, "PartRollupCost(P)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/part/%d/bom", f.P) {
		t.Errorf("PartRollupCost(P): Location = %q", loc)
	}

	read := func(id int) (sql.NullFloat64, sql.NullTime) {
		var cost sql.NullFloat64
		var at sql.NullTime
		if err := h.queryRowContext(ctx, fmt.Sprintf(
			`SELECT last_rollup_cost, last_rollup_at FROM %s WHERE id=$1`, "part"), id).Scan(&cost, &at); err != nil {
			t.Fatalf("read rollup of part %d: %v", id, err)
		}
		return cost, at
	}
	costP, atP := read(f.P)
	costS, atS := read(f.S)
	assertFloatEqual(t, "P last_rollup_cost", costP.Float64, 19.0)
	assertFloatEqual(t, "S last_rollup_cost", costS.Float64, 1.0)
	if !atP.Valid || !atS.Valid || !atP.Time.Equal(atS.Time) {
		t.Errorf("last_rollup_at: P=%v S=%v, want one shared timestamp", atP, atS)
	}
	if atS.Time.Equal(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)) {
		t.Errorf("S last_rollup_at = %v, still the seeded value", atS.Time)
	}
	for _, id := range []int{f.L1, f.L2, f.L3} {
		if cost, at := read(id); cost.Valid || at.Valid {
			t.Errorf("leaf %d rollup = {%v %v}, want untouched NULL", id, cost, at)
		}
	}
}
