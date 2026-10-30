package main

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"arx/arx_go/models"
	"arx/internal/purchasing"
)

func rfqQuote(num, status string, group int, total string, date *time.Time) purchasing.RFQQuoteRow {
	return purchasing.RFQQuoteRow{Number: num, Status: status, RFQGroupID: group, SupplierName: "V" + num,
		DateRequested: date, Total: decimal.RequireFromString(total)}
}

func TestGroupRFQQuotes(t *testing.T) {
	d1 := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// ListRFQQuotes order: newest group first, quotes by number.
	rows := []purchasing.RFQQuoteRow{
		rfqQuote("1051R1", "rfq", 20, "50.00", &d1),
		rfqQuote("1050R1", "closed", 10, "100.00", &d1),
		rfqQuote("1050R2", "cancelled", 10, "80.50", &d2),
	}
	got := groupRFQQuotes(rows)
	if len(got) != 2 {
		t.Fatalf("groups = %d, want 2", len(got))
	}
	if g := got[0]; g.Group != 20 || g.Num != "1051" || g.Status != "open" || g.Cost != "$50.00" || len(g.Quotes) != 1 {
		t.Errorf("group 20 = %+v", g)
	}
	g := got[1]
	if g.Num != "1050" || g.Status != "awarded" || len(g.Quotes) != 2 {
		t.Errorf("group 10 = %+v", g)
	}
	if g.Cost != "$80.50 – $100.00" {
		t.Errorf("cost range = %q, want low–high across quotes", g.Cost)
	}
	if g.Requested != "2026-10-01" {
		t.Errorf("requested = %q, want the earliest quote date", g.Requested)
	}
	if g.Vendors != "V1050R1, V1050R2" {
		t.Errorf("vendors = %q", g.Vendors)
	}
}

func TestRFQGroupStatus(t *testing.T) {
	q := func(statuses ...string) []rfqQuoteJSON {
		out := make([]rfqQuoteJSON, len(statuses))
		for i, s := range statuses {
			out[i].Status = s
		}
		return out
	}
	cases := []struct {
		quotes []rfqQuoteJSON
		want   string
	}{
		{q("rfq", "rfq"), "open"},
		{q("closed", "cancelled"), "awarded"},
		{q("cancelled", "cancelled"), "declined"},
		{q("rfq", "closed"), "open"}, // a quote still awaiting a decision keeps the RFQ open
	}
	for _, c := range cases {
		if got := rfqGroupStatus(c.quotes); got != c.want {
			t.Errorf("rfqGroupStatus(%v) = %s, want %s", c.quotes, got, c.want)
		}
	}
}

func TestPOSectionTab(t *testing.T) {
	// models.PurchaseOrder zero value has no RFQ group.
	if got := poSectionTab(models.PurchaseOrder{}); got != "pos" {
		t.Errorf("ordinary PO = %s, want pos", got)
	}
	g := 7
	if got := poSectionTab(models.PurchaseOrder{RFQGroupID: &g}); got != "rfqs" {
		t.Errorf("RFQ quote = %s, want rfqs", got)
	}
}
