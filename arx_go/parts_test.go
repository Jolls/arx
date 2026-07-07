package main

import (
	"database/sql"
	"testing"
)

// pref builds a sql.NullFloat64 for the preferred-supplier price (valid).
func pref(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }

// noPref is the absence of a preferred-supplier price.
var noPref = sql.NullFloat64{}

func TestBOMLeafCost(t *testing.T) {
	cases := []struct {
		name        string
		childHasBOM bool
		lastRollup  float64
		preferred   sql.NullFloat64
		currentCost float64
		category    string
		wantCost    float64
		wantSource  string
	}{
		// Assemblies use the stored rollup result.
		{"assembly with rollup", true, 12.5, noPref, 0, "ASM", 12.5, "rollup"},
		{"assembly never rolled", true, 0, noPref, 99, "ASM", 0, "missing"},

		// Leaf with a preferred-supplier price uses it over current_cost.
		{"leaf preferred price", false, 0, pref(3.25), 9.99, "BUY", 3.25, "price"},

		// Labor (OPS) lines have no price row — the hourly rate is current_cost.
		{"OPS labor rate", false, 0, noPref, 85, "OPS", 85, "labor"},
		{"OPS with zero rate", false, 0, noPref, 0, "OPS", 0, "missing"},

		// Ordinary leaf without a preferred price falls back to current_cost.
		{"leaf current_cost fallback", false, 0, noPref, 4.5, "BUY", 4.5, "current_cost"},
		{"leaf no cost at all", false, 0, noPref, 0, "BUY", 0, "missing"},

		// A preferred row of 0 is treated as no price (fall back to current_cost).
		{"preferred zero falls back", false, 0, pref(0), 7, "BUY", 7, "current_cost"},
	}
	for _, c := range cases {
		gotCost, gotSrc := bomLeafCost(c.childHasBOM, c.lastRollup, c.preferred, c.currentCost, c.category)
		if gotCost != c.wantCost || gotSrc != c.wantSource {
			t.Errorf("%s: bomLeafCost = (%.4f, %q), want (%.4f, %q)",
				c.name, gotCost, gotSrc, c.wantCost, c.wantSource)
		}
	}
}

func TestPickTier(t *testing.T) {
	tiers := []priceTier{
		{PriceEA: 0.10, PackSize: 1},
		{PriceEA: 0.05, PackSize: 100},
		{PriceEA: 0.03, PackSize: 1000},
	}
	cases := []struct {
		name      string
		tiers     []priceTier
		qty       float64
		wantPrice float64
		wantPack  float64
		wantOK    bool
	}{
		{"below every tier", tiers, 0.5, 0, 0, false},
		{"exact match on smallest tier", tiers, 1, 0.10, 1, true},
		{"between tiers picks lower tier", tiers, 50, 0.10, 1, true},
		{"exact match on middle tier", tiers, 100, 0.05, 100, true},
		{"between middle and top picks middle", tiers, 500, 0.05, 100, true},
		{"exact match on top tier", tiers, 1000, 0.03, 1000, true},
		{"above every tier picks largest", tiers, 5000, 0.03, 1000, true},
		{"no tiers at all", nil, 100, 0, 0, false},
		{"unordered input still finds largest qualifying", []priceTier{
			{PriceEA: 0.03, PackSize: 1000},
			{PriceEA: 0.10, PackSize: 1},
			{PriceEA: 0.05, PackSize: 100},
		}, 500, 0.05, 100, true},
	}
	for _, c := range cases {
		gotPrice, gotPack, gotOK := pickTier(c.tiers, c.qty)
		if gotOK != c.wantOK || (gotOK && (gotPrice != c.wantPrice || gotPack != c.wantPack)) {
			t.Errorf("%s: pickTier(qty=%v) = (%.4f, %.4f, %v), want (%.4f, %.4f, %v)",
				c.name, c.qty, gotPrice, gotPack, gotOK, c.wantPrice, c.wantPack, c.wantOK)
		}
	}
}
