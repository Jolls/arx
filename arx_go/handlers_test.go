package main

import (
	"net/url"
	"testing"
	"time"
)

func TestFormatDate(t *testing.T) {
	d := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		input *time.Time
		want  string
	}{
		{&d, "2024-03-05"},
		{nil, "N/A"},
	}
	for _, c := range cases {
		if got := formatDate(c.input); got != c.want {
			t.Errorf("formatDate(%v) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1_048_575, "1024.0 KB"},
		{1_048_576, "1.0 MB"},
		{5_242_880, "5.0 MB"},
	}
	for _, c := range cases {
		if got := formatFileSize(c.input); got != c.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestAttachLabel(t *testing.T) {
	cases := []struct {
		filename, category, want string
	}{
		{"foo/bar/doc.pdf", "Drawing", "Drawing"},     // category wins
		{"foo/bar/doc.pdf", "", "doc.pdf"},            // no category → basename
		{"foo\\bar\\doc.pdf", "", "doc.pdf"},          // backslash path
		{"doc.pdf", "", "doc.pdf"},                    // no directory
	}
	for _, c := range cases {
		if got := attachLabel(c.filename, c.category); got != c.want {
			t.Errorf("attachLabel(%q, %q) = %q, want %q", c.filename, c.category, got, c.want)
		}
	}
}

func TestPolRowToArgs(t *testing.T) {
	cases := []struct {
		row      polRow
		wantItem int
		wantQty  float64
		wantCost float64
		wantPNID interface{}
	}{
		{
			polRow{Item: "3", Qty: "2.5", Cost: "10.99", PNID: "42"},
			3, 2.5, 10.99, 42,
		},
		{
			polRow{Item: "", Qty: "", Cost: "", PNID: ""},
			0, 0, 0, nil, // blanks → zero values, nil pnid
		},
		{
			polRow{Item: "1", Qty: "1", Cost: "5.00", PNID: "0"},
			1, 1, 5.00, nil, // PNID=0 treated as unset
		},
		{
			polRow{Item: "bad", Qty: "bad", Cost: "bad", PNID: "bad"},
			0, 0, 0, nil, // unparseable → zero values
		},
	}
	for _, c := range cases {
		item, qty, cost, pnid := polRowToArgs(c.row)
		if item != c.wantItem || qty != c.wantQty || cost != c.wantCost || pnid != c.wantPNID {
			t.Errorf("polRowToArgs(%+v) = (%d, %g, %g, %v), want (%d, %g, %g, %v)",
				c.row, item, qty, cost, pnid, c.wantItem, c.wantQty, c.wantCost, c.wantPNID)
		}
	}
}

func TestExtractPolRows(t *testing.T) {
	form := url.Values{
		"pol[1][POLItem]":         {"1"},
		"pol[1][POLPNPartNumber]": {"PN-001"},
		"pol[1][POLDesc]":         {"Widget"},
		"pol[1][VendorPN]":        {"V-123"},
		"pol[1][POLQty]":          {"  5  "}, // whitespace trimmed
		"pol[1][POLCost]":         {"9.99"},
		"pol[1][POLPNID]":         {"7"},
		"pol[2][POLItem]":         {"2"},
		"pol[2][POLDesc]":         {"Gadget"},
		"other[1][POLItem]":       {"99"}, // different prefix — ignored
	}

	rows := extractPolRows(form, "pol")

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	r1 := rows["1"]
	if r1.Item != "1" || r1.PartNumber != "PN-001" || r1.Desc != "Widget" ||
		r1.VendorPN != "V-123" || r1.Qty != "5" || r1.Cost != "9.99" || r1.PNID != "7" {
		t.Errorf("row 1 mismatch: %+v", r1)
	}

	r2 := rows["2"]
	if r2.Item != "2" || r2.Desc != "Gadget" {
		t.Errorf("row 2 mismatch: %+v", r2)
	}

	if _, ok := rows["other"]; ok {
		t.Error("rows with different prefix should not be included")
	}
}

func TestResourceBase(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"/part/42/bom", "/part/42"},
		{"/part/42/bom/edit", "/part/42"},   // deeper path truncated to 3 segments
		{"/part/42", "/part/42"},            // exactly 3 segments — unchanged
		{"/part/42/", "/part/42"},           // trailing slash stripped
		{"/supplier/7", "/supplier/7"},
		{"/po", "/po"},                      // short path returned as-is
	}
	for _, c := range cases {
		if got := resourceBase(c.input); got != c.want {
			t.Errorf("resourceBase(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
