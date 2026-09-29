//go:build integration

package main

import (
	"context"
	"testing"

	"arx/internal/inventory"
)

// #268: a build consumes its components in ListBuildLines order, and each consumption locks that
// part row. The order must be the same for every BOM (ascending component id), or two builds that
// share components can lock them in opposite orders and deadlock.
func TestIntegration_BuildLinesOrderedByComponentID(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	parent, _, cleanup := seedThrowawayPart(t, h, ctx, "268")
	t.Cleanup(cleanup)
	var comps []int
	for range 3 {
		id, _, c := seedThrowawayPart(t, h, ctx, "268")
		t.Cleanup(c)
		comps = append(comps, id) // ascending: each id is newer than the last
	}
	tx := invTx(t, h)

	// Insert the BOM lines highest component first, so heap order is descending.
	for i := len(comps) - 1; i >= 0; i-- {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO bom (parent_part_id, component_part_id, line_number, qty) VALUES ($1, $2, $3, 1)`,
			parent, comps[i], len(comps)-i); err != nil {
			t.Fatalf("seed bom: %v", err)
		}
	}

	// bom has no index on parent_part_id, so without an ORDER BY the row order is whatever the
	// planner's join gives. Pin it to nested loop (bom heap order) so the test can't pass by chance.
	for _, s := range []string{`SET LOCAL enable_hashjoin = off`, `SET LOCAL enable_mergejoin = off`} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	lines, err := inventory.New(tx).ListBuildLines(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(comps) {
		t.Fatalf("got %d build lines, want %d", len(lines), len(comps))
	}
	for i, l := range lines {
		if l.ComponentPartID != comps[i] {
			t.Errorf("line %d component = %d, want %d (ascending component id)", i, l.ComponentPartID, comps[i])
		}
	}
}
