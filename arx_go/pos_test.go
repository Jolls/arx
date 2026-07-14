package main

import (
	"testing"

	"arx/arx_go/models"
)

func TestPOApprovalNext(t *testing.T) {
	ok := []struct{ action, from, want string }{
		{"submit", "not_submitted", "pending"},
		{"submit", "rejected", "pending"},
		{"approve", "pending", "approved"},
		{"reject", "pending", "rejected"},
	}
	for _, c := range ok {
		got, valid := poApprovalNext(c.action, c.from)
		if !valid || got != c.want {
			t.Errorf("poApprovalNext(%q,%q) = (%q,%v), want (%q,true)", c.action, c.from, got, valid, c.want)
		}
	}

	bad := []struct{ action, from string }{
		{"submit", "pending"},   // already submitted
		{"submit", "approved"},  // already approved
		{"approve", "not_submitted"},
		{"approve", "approved"}, // not pending
		{"approve", "rejected"},
		{"reject", "not_submitted"},
		{"reject", "approved"},
		{"bogus", "pending"}, // unknown action
	}
	for _, c := range bad {
		if got, valid := poApprovalNext(c.action, c.from); valid {
			t.Errorf("poApprovalNext(%q,%q) = (%q,true), want invalid", c.action, c.from, got)
		}
	}
}

func TestPOApprovalAllowsSend(t *testing.T) {
	if !poApprovalAllowsSend("approved") {
		t.Error("poApprovalAllowsSend(approved) = false, want true")
	}
	for _, s := range []string{"not_submitted", "pending", "rejected", ""} {
		if poApprovalAllowsSend(s) {
			t.Errorf("poApprovalAllowsSend(%q) = true, want false", s)
		}
	}
}

func TestPOApprovalActions(t *testing.T) {
	// A non-approver can submit (when not_submitted/rejected) but never approve/reject.
	for _, status := range []string{"not_submitted", "rejected"} {
		acts := poApprovalActions(status, false)
		if len(acts) != 1 || acts[0].Action != "submit" {
			t.Errorf("poApprovalActions(%q,false) = %+v, want a single submit", status, acts)
		}
	}
	// Pending offers nothing to a non-approver, approve+reject to an approver.
	if acts := poApprovalActions("pending", false); len(acts) != 0 {
		t.Errorf("poApprovalActions(pending,false) = %+v, want none", acts)
	}
	if acts := poApprovalActions("pending", true); len(acts) != 2 {
		t.Errorf("poApprovalActions(pending,true) = %+v, want approve+reject", acts)
	}
	// Approved is terminal — no further actions even for an approver.
	if acts := poApprovalActions("approved", true); len(acts) != 0 {
		t.Errorf("poApprovalActions(approved,true) = %+v, want none", acts)
	}
}

func TestPOCanTransition(t *testing.T) {
	allowed := []struct{ from, to string }{
		{"rfq", "cancelled"}, // decline (#270)
		{"draft", "open"},
		{"draft", "cancelled"},
		{"open", "sent"},
		{"sent", "partially_received"},
		{"sent", "closed"},
		{"partially_received", "closed"},
		{"closed", "open"},     // reopen
		{"cancelled", "draft"}, // reopen
	}
	for _, c := range allowed {
		if !poCanTransition(c.from, c.to) {
			t.Errorf("poCanTransition(%q,%q) = false, want true", c.from, c.to)
		}
	}

	disallowed := []struct{ from, to string }{
		{"rfq", "draft"},                // awarding is a duplicate-to-PO, not a generic transition (#270)
		{"rfq", "sent"},                 // RFQ can only be declined
		{"draft", "rfq"},                // nothing transitions into rfq
		{"draft", "sent"},               // must go through open
		{"draft", "closed"},             // skips the chain
		{"open", "partially_received"},  // must be sent first
		{"closed", "cancelled"},         // closed only reopens to open
		{"cancelled", "open"},           // cancelled only reopens to draft
		{"sent", "draft"},               // no backward jump
		{"draft", "draft"},              // no self-transition
		{"bogus", "open"},               // unknown source
	}
	for _, c := range disallowed {
		if poCanTransition(c.from, c.to) {
			t.Errorf("poCanTransition(%q,%q) = true, want false", c.from, c.to)
		}
	}
}

func TestRFQBaseNumber(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1050R1", "1050"},
		{"1050R2", "1050"},
		{"1050R10", "1050"},   // multi-digit suffix
		{"1050", "1050"},      // no suffix (already a bare PO number)
		{"1050R", "1050R"},    // 'R' without a number is not a suffix
		{"12R3R4", "12R3"},    // only the trailing R<n> is stripped
		{"", ""},
	}
	for _, c := range cases {
		if got := rfqBaseNumber(c.in); got != c.want {
			t.Errorf("rfqBaseNumber(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildRFQGrid(t *testing.T) {
	lines := []rfqScanLine{
		{Number: "1050R1", SupplierName: "Acme", Status: "rfq", Total: 30, HasLine: true, POLID: 1, PartNumber: "P-1", Qty: 2, Cost: 10},
		{Number: "1050R1", SupplierName: "Acme", Status: "rfq", Total: 30, HasLine: true, POLID: 2, PartNumber: "P-2", Qty: 1, Cost: 10},
		{Number: "1050R2", SupplierName: "Globex", Status: "rfq", Total: 50, HasLine: true, POLID: 3, PartNumber: "P-1", Qty: 2, Cost: 25},
		// Globex did not quote P-2 → tests the ragged-row padding path.
		{Number: "1050R3", SupplierName: "Initech", Status: "rfq", Total: 0, HasLine: false}, // a quote with no lines
	}
	suppliers, rows := buildRFQGrid(lines)

	// Three columns, in first-seen order.
	if len(suppliers) != 3 {
		t.Fatalf("got %d suppliers, want 3", len(suppliers))
	}
	for i, name := range []string{"Acme", "Globex", "Initech"} {
		if suppliers[i].SupplierName != name {
			t.Errorf("column %d = %q, want %q", i, suppliers[i].SupplierName, name)
		}
	}

	// Acme has the lowest non-zero total (30); Initech's 0 total is ignored.
	if !suppliers[0].Best {
		t.Error("Acme (lowest total) should be the best supplier")
	}
	if suppliers[1].Best || suppliers[2].Best {
		t.Error("only Acme should be marked the best supplier")
	}

	// Two part rows, each padded to the full column count.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if len(row.Cells) != 3 {
			t.Errorf("row %q has %d cells, want 3 (padded to column count)", row.PartNumber, len(row.Cells))
		}
	}

	// P-1: Acme (10) beats Globex (25); Initech has no cell (padded empty).
	p1 := rows[0]
	if p1.PartNumber != "P-1" {
		t.Fatalf("first row = %q, want P-1", p1.PartNumber)
	}
	if !p1.Cells[0].Best {
		t.Error("P-1: Acme cell should be marked best (10 < 25)")
	}
	if p1.Cells[1].Best {
		t.Error("P-1: Globex cell should not be best")
	}
	if p1.Cells[2].Quoted {
		t.Error("P-1: Initech has no line → cell should be unquoted")
	}

	// P-2: only Acme quoted; Globex/Initech are padded empties.
	p2 := rows[1]
	if p2.PartNumber != "P-2" {
		t.Fatalf("second row = %q, want P-2", p2.PartNumber)
	}
	if !p2.Cells[0].Quoted || !p2.Cells[0].Best {
		t.Error("P-2: Acme cell should be quoted and best")
	}
	if p2.Cells[1].Quoted || p2.Cells[2].Quoted {
		t.Error("P-2: Globex/Initech cells should be empty (padded)")
	}
}

func TestBuildRFQGridEmpty(t *testing.T) {
	suppliers, rows := buildRFQGrid(nil)
	if len(suppliers) != 0 || len(rows) != 0 {
		t.Errorf("buildRFQGrid(nil) = (%d suppliers, %d rows), want (0, 0)", len(suppliers), len(rows))
	}
}

func TestDerivePOReceiptStatus(t *testing.T) {
	line := func(ordered, received float64) models.PurchaseOrderLine {
		return models.PurchaseOrderLine{Qty: ordered, ReceivedQty: received}
	}
	cases := []struct {
		name  string
		items []models.PurchaseOrderLine
		want  string
	}{
		{"nothing received", []models.PurchaseOrderLine{line(10, 0), line(5, 0)}, ""},
		{"some received", []models.PurchaseOrderLine{line(10, 4), line(5, 0)}, "partially_received"},
		{"all fully received", []models.PurchaseOrderLine{line(10, 10), line(5, 5)}, "closed"},
		{"over-receipt counts as full", []models.PurchaseOrderLine{line(10, 12), line(5, 5)}, "closed"},
		{"mixed full and partial", []models.PurchaseOrderLine{line(10, 10), line(5, 2)}, "partially_received"},
		{"no lines", nil, ""},
	}
	for _, c := range cases {
		if got := derivePOReceiptStatus(c.items); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseReceiveDeltas(t *testing.T) {
	items := []models.PurchaseOrderLine{{ID: 1}, {ID: 2}, {ID: 3}}

	// Mixed submission: line 1 a valid qty, line 2 blank (skipped), line 3 zero (ignored).
	form := map[string]string{"recv[1]": "  4.5 ", "recv[2]": "", "recv[3]": "0"}
	got, err := parseReceiveDeltas(items, func(k string) string { return form[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[1] != 4.5 {
		t.Errorf("got %v, want only {1:4.5}", got)
	}

	// Nothing entered → empty map, no error (caller rejects empties).
	if got, err := parseReceiveDeltas(items, func(string) string { return "" }); err != nil || len(got) != 0 {
		t.Errorf("empty submission = (%v, %v), want (empty, nil)", got, err)
	}

	// Negative quantities are ignored, not errors.
	if got, _ := parseReceiveDeltas(items, func(k string) string {
		if k == "recv[2]" {
			return "-3"
		}
		return ""
	}); len(got) != 0 {
		t.Errorf("negative qty produced %v, want empty", got)
	}

	// A non-numeric value is a hard error.
	if _, err := parseReceiveDeltas(items, func(k string) string {
		if k == "recv[1]" {
			return "abc"
		}
		return ""
	}); err == nil {
		t.Error("non-numeric quantity should error")
	}
}

func TestPOStatusActions(t *testing.T) {
	// Every action target must be a valid transition from the current status,
	// and labels/classes must be populated.
	for _, status := range []string{"rfq", "draft", "open", "sent", "partially_received", "closed", "cancelled"} {
		actions := poStatusActions(status)
		if len(actions) == 0 {
			t.Errorf("poStatusActions(%q) returned no actions", status)
		}
		for _, a := range actions {
			if !poCanTransition(status, a.Target) {
				t.Errorf("poStatusActions(%q) offered invalid target %q", status, a.Target)
			}
			if a.Label == "" || a.Class == "" {
				t.Errorf("poStatusActions(%q) target %q missing label/class", status, a.Target)
			}
		}
	}
}
