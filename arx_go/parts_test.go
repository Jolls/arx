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
