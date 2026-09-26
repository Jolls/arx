//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

// TestIntegration_PartTrackingModeRoundTrip pins that tracking_mode round-trips
// through PartsCreate/PartUpdate and that IsLotTracked is derived from it.
func TestIntegration_PartTrackingModeRoundTrip(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	vals := url.Values{
		"part_number":    {smokeUniq("TRK-PN")},
		"revision":       {"A"},
		"description":    {"tracking mode round trip"},
		"category":       {"BUY"},
		"release_status": {"U"},
		"active":         {"1"},
		"tracking_mode":  {"lot"},
	}
	rec := httptest.NewRecorder()
	h.PartsCreate(rec, postForm("/parts", vals))
	id := locID(t, rec, "/part/")
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg().PartsTable()), id)

	check := func(step, wantMode string, wantLot bool) {
		t.Helper()
		p, err := h.fetchPartBasic(ctx, strconv.Itoa(id))
		if err != nil {
			t.Fatalf("%s: fetchPartBasic: %v", step, err)
		}
		if p.TrackingMode != wantMode || p.IsLotTracked != wantLot {
			t.Errorf("%s: TrackingMode=%q IsLotTracked=%v, want %q %v", step, p.TrackingMode, p.IsLotTracked, wantMode, wantLot)
		}
	}
	check("create", "lot", true)

	for _, tc := range []struct {
		mode    string
		wantLot bool
	}{{"none", false}, {"lot_serial", true}} {
		vals.Set("tracking_mode", tc.mode)
		rec := httptest.NewRecorder()
		h.PartUpdate(rec, withID(postForm(fmt.Sprintf("/part/%d", id), vals), id))
		assert302(t, "PartUpdate "+tc.mode, rec)
		check("update "+tc.mode, tc.mode, tc.wantLot)
	}
}

// TestIntegration_DeadSchemaObjectsGone checks the live schema after the
// dead-column cleanup (#31): legacy company columns renamed, dropped columns and
// the logs/release_notes tables gone.
func TestIntegration_DeadSchemaObjectsGone(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	columns := func(table string) map[string]bool {
		rows, err := h.queryContext(ctx,
			`SELECT lower(column_name) FROM information_schema.columns WHERE lower(table_name)=@p1`, table)
		if err != nil {
			t.Fatalf("columns of %s: %v", table, err)
		}
		defer rows.Close()
		cols := map[string]bool{}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols[c] = true
		}
		return cols
	}

	company := columns("company")
	for _, c := range []string{"notes", "supplier_code", "supplier_part_count", "po_count"} {
		if !company[c] {
			t.Errorf("company.%s missing", c)
		}
	}
	for _, c := range []string{"suweb", "sucontact1", "sunotes", "susuppliercode", "sunumoflnks", "sunumofpos"} {
		if company[c] {
			t.Errorf("company.%s still present", c)
		}
	}
	if columns("part")["is_lot_tracked"] {
		t.Error("part.is_lot_tracked still present")
	}

	var n int
	if err := h.queryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.tables WHERE lower(table_name) IN ('logs','release_notes')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d of logs/release_notes tables still present", n)
	}
}
