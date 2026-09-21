package main

import (
	"database/sql"
	"strings"
	"testing"
)

func TestPlanRFQLines(t *testing.T) {
	buy := func(c string) bool { return c == "BUY" }
	p := func(id int, pn, cat string, stock float64, hasBOM bool) rfqPart {
		return rfqPart{ID: id, PartNumber: pn, Category: cat, Stock: stock, HasBOM: hasBOM}
	}
	qtyOf := func(t *testing.T, lines []rfqLine, id int) (float64, bool) {
		t.Helper()
		for _, l := range lines {
			if l.Part.ID == id {
				return l.Qty, true
			}
		}
		return 0, false
	}

	t.Run("stock covers need, no line even below reorder_min", func(t *testing.T) {
		a := p(2, "A", "BUY", 100, false)
		a.ReorderMin = sql.NullFloat64{Float64: 120, Valid: true}
		lines, err := planRFQLines(1, 5, map[int][]bomEdge{1: {{2, 1}}}, map[int]rfqPart{2: a}, buy)
		if err != nil || len(lines) != 0 {
			t.Fatalf("got %v, %v; want no lines", lines, err)
		}
	})

	t.Run("short part raised to reorder_min", func(t *testing.T) {
		a := p(2, "A", "BUY", 10, false)
		a.ReorderMin = sql.NullFloat64{Float64: 30, Valid: true}
		lines, _ := planRFQLines(1, 50, map[int][]bomEdge{1: {{2, 1}}}, map[int]rfqPart{2: a}, buy)
		if q, ok := qtyOf(t, lines, 2); !ok || q != 70 {
			t.Fatalf("qty = %v (found %v), want 70", q, ok)
		}
	})

	t.Run("sub-assembly stock nets before exploding; shared part netted once", func(t *testing.T) {
		// root -> 2x SUB(stock 3) -> 1x C ; root -> 1x C. N=5.
		// SUB need 10, explode 7 -> C 7 + direct 5 = 12, stock 2 -> qty 10.
		edges := map[int][]bomEdge{1: {{2, 2}, {3, 1}}, 2: {{3, 1}}}
		parts := map[int]rfqPart{2: p(2, "SUB", "ASM", 3, true), 3: p(3, "C", "BUY", 2, false)}
		lines, err := planRFQLines(1, 5, edges, parts, buy)
		if err != nil {
			t.Fatal(err)
		}
		if q, ok := qtyOf(t, lines, 3); !ok || q != 10 || len(lines) != 1 {
			t.Fatalf("lines = %v, want single line qty 10", lines)
		}
	})

	t.Run("purchased part with BOM is not exploded; non-purchased no-BOM ignored", func(t *testing.T) {
		edges := map[int][]bomEdge{1: {{2, 1}, {3, 1}}, 2: {{4, 1}}}
		parts := map[int]rfqPart{2: p(2, "B", "BUY", 0, true), 3: p(3, "LABOR", "OPS", 0, false), 4: p(4, "D", "BUY", 0, false)}
		lines, _ := planRFQLines(1, 1, edges, parts, buy)
		if len(lines) != 1 || lines[0].Part.ID != 2 {
			t.Fatalf("lines = %v, want only part 2", lines)
		}
	})

	t.Run("cycle is an error", func(t *testing.T) {
		edges := map[int][]bomEdge{1: {{2, 1}}, 2: {{1, 1}}}
		parts := map[int]rfqPart{2: p(2, "X", "ASM", 0, true), 1: p(1, "ROOT", "ASM", 0, true)}
		_, err := planRFQLines(1, 1, edges, parts, buy)
		if err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want cycle error", err)
		}
	})
}
