//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestIntegration_BuildRFQPlan_Graph pins buildRFQPlan over a live BOM: needs sum
// across paths, sub-assemblies net stock before exploding, purchased parts are
// leaves, NULL text columns read as blank, and lines group by default supplier.
func TestIntegration_BuildRFQPlan_Graph(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var ids []int
	defer func() {
		for _, id := range ids {
			smokeExec(ctx, h, `DELETE FROM bom WHERE parent_part_id=$1 OR component_part_id=$1`, id)
		}
		for _, id := range ids {
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
	}()
	seed := func(label string, category, description, revision, reorderMin, supplierID any, stock float64) (int, string) {
		t.Helper()
		partNumber := smokeUniq("ITEST-RFQ-" + label)
		var id int
		if err := h.queryRowContext(ctx,
			`INSERT INTO part (part_number, category, description, revision, stock_on_hand, reorder_min, default_supplier_id)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			partNumber, category, description, revision, stock, reorderMin, supplierID,
		).Scan(&id); err != nil {
			t.Fatalf("seed part %s: %v", label, err)
		}
		ids = append(ids, id)
		return id, partNumber
	}
	link := func(parent, child int, qty float64) {
		t.Helper()
		if _, err := h.execContext(ctx,
			`INSERT INTO bom (parent_part_id, component_part_id, qty) VALUES ($1,$2,$3)`, parent, child, qty); err != nil {
			t.Fatalf("seed bom %d->%d: %v", parent, child, err)
		}
	}

	root, _ := seed("ROOT", "ASM", "root", "A", nil, nil, 0)
	b1, b1PN := seed("B1", "BUY", "bought one", "B", 5.0, 1001, 1)
	sub, _ := seed("SUB", "MFG", "sub", "A", nil, nil, 1)
	c, cPN := seed("C", "RAW", nil, nil, nil, nil, 0)
	u, _ := seed("U", nil, "uncategorized", "A", nil, nil, 0)
	p, _ := seed("P", "BUY", "bought with bom", "A", nil, 1001, 100)
	d, _ := seed("D", "BUY", "under a bought part", "A", nil, 1001, 0)
	link(root, b1, 3)
	link(root, sub, 1)
	link(sub, b1, 2)
	link(sub, c, 4)
	link(root, u, 1)
	link(root, p, 1)
	link(p, d, 1)

	var acmeName string
	if err := h.queryRowContext(ctx, `SELECT name FROM company WHERE id = 1001`).Scan(&acmeName); err != nil {
		t.Fatalf("load supplier 1001 name: %v", err)
	}

	plan, err := h.buildRFQPlan(ctx, root, 2)
	if err != nil {
		t.Fatalf("buildRFQPlan: %v", err)
	}

	if len(plan.Groups) != 1 {
		t.Fatalf("groups = %+v, want one (supplier 1001)", plan.Groups)
	}
	g := plan.Groups[0]
	if g.SupplierID != 1001 || g.SupplierName != acmeName {
		t.Errorf("group = %d %q, want 1001 %q", g.SupplierID, g.SupplierName, acmeName)
	}
	if len(g.Lines) != 1 {
		t.Fatalf("group lines = %+v, want only B1 (D sits under a purchased part)", g.Lines)
	}
	l := g.Lines[0]
	// B1: 2×3 direct + (2−1 SUB stock)×2 = 8 needed; 8 − 1 stock + 5 reorder_min = 12.
	if l.Part.ID != b1 || l.Part.PartNumber != b1PN || l.Need != 8 || l.Qty != 12 {
		t.Errorf("B1 line = id %d %q need %v qty %v, want %d %q need 8 qty 12", l.Part.ID, l.Part.PartNumber, l.Need, l.Qty, b1, b1PN)
	}
	if l.Part.Description != "bought one" || l.Part.Revision != "B" || l.Part.Category != "BUY" || l.Part.Stock != 1 {
		t.Errorf("B1 fields = %+v", l.Part)
	}
	if !l.Part.ReorderMin.Valid || l.Part.ReorderMin.Float64 != 5 || !l.Part.SupplierID.Valid || l.Part.SupplierID.Int64 != 1001 || l.Part.HasBOM {
		t.Errorf("B1 reorder/supplier/bom = %+v", l.Part)
	}

	if len(plan.Unsupplied) != 1 {
		t.Fatalf("unsupplied = %+v, want only C", plan.Unsupplied)
	}
	cl := plan.Unsupplied[0]
	if cl.Part.ID != c || cl.Part.PartNumber != cPN || cl.Need != 4 || cl.Qty != 4 {
		t.Errorf("C line = id %d %q need %v qty %v, want %d %q need 4 qty 4", cl.Part.ID, cl.Part.PartNumber, cl.Need, cl.Qty, c, cPN)
	}
	if cl.Part.Description != "" || cl.Part.Revision != "" || cl.Part.ReorderMin.Valid || cl.Part.SupplierID.Valid {
		t.Errorf("C fields = %+v, want blank text, no reorder_min, no supplier", cl.Part)
	}
}

// TestIntegration_NextBaseNumber_ScansAllParts pins that nextBaseNumber
// suggests from every part_number under the saved config, and that
// PartsNextNumber returns the same suggestion as JSON.
func TestIntegration_NextBaseNumber_ScansAllParts(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	_, _, cleanupPart := seedThrowawayPart(t, h, ctx, "NEXTNUM")
	defer cleanupPart()

	rows, err := h.queryContext(ctx, `SELECT part_number FROM part`)
	if err != nil {
		t.Fatalf("scan part numbers: %v", err)
	}
	var all []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		all = append(all, s)
	}
	rows.Close()
	want := suggestBaseNumber(h.loadBaseNumberConfig(ctx), all)

	got, err := h.nextBaseNumber(ctx)
	if err != nil {
		t.Fatalf("nextBaseNumber: %v", err)
	}
	if got != want {
		t.Errorf("nextBaseNumber = %q, want %q", got, want)
	}

	rec := httptest.NewRecorder()
	h.PartsNextNumber(rec, httptest.NewRequest("GET", "/api/parts/next-number", nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode PartsNextNumber body %q: %v", rec.Body.String(), err)
	}
	if body["suggestion"] != want || body["error"] != "" {
		t.Errorf("PartsNextNumber = %v, want suggestion %q", body, want)
	}
}
