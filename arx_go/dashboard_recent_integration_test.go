//go:build integration

package main

import (
	"testing"
	"time"
)

// Seed: lots 8306 (2026-05-30), 8302 (05-25) are the two newest; builds 8203 (05-30) then 8202,
// 8201 on a shared date, so id DESC breaks the tie.
func TestIntegration_DashboardRecentLots(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	items, err := h.dashboardRecentLots(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != 8306 || items[1].ID != 8302 {
		t.Fatalf("recent lots (n=2) = %+v, want ids [8306 8302]", items)
	}
	// Created is formatted in the user's zone, so it may land a day either side of the seed's UTC date.
	if _, err := time.Parse("2006-01-02", items[0].Created); err != nil || items[0].PartNumber == "" {
		t.Errorf("first lot = %+v, want a part number and a YYYY-MM-DD created date", items[0])
	}
	all, err := h.dashboardRecentLots(t.Context(), 50)
	if err != nil || len(all) < 4 {
		t.Errorf("recent lots (n=50) = %d rows, %v; want at least 4", len(all), err)
	}
}

func TestIntegration_DashboardRecentBuilds(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	items, err := h.dashboardRecentBuilds(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != 8203 || items[1].ID != 8202 {
		t.Fatalf("recent builds (n=2) = %+v, want ids [8203 8202]", items)
	}
	if items[0].PartNumber == "" {
		t.Errorf("first build has no part number: %+v", items[0])
	}
}
