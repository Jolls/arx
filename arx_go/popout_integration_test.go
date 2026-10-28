//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// popoutBody calls a popout handler for id and returns the status and body.
func popoutBody(t *testing.T, fn http.HandlerFunc, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/popout/x", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Code, rec.Body.String()
}

func expectContains(t *testing.T, name, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s: missing %q\n%s", name, w, body)
		}
	}
}

func TestIntegration_PopoutPartWhereUsed_Characterization(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutPartWhereUsed, "3002")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expectContains(t, "where-used", body, "Used in", `href="/part/3005"`)
}

func TestIntegration_PartOrders_Characterization(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	rec := httptest.NewRecorder()
	h.PartOrders(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3002/orders", nil), 3002))
	expectContains(t, "part orders", rec.Body.String(), `>5002</a>`, "Precision Machining")
}

func TestIntegration_PopoutPartStock(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutPartStock, "3007")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expectContains(t, "stock", body, "lots/8301", "Not assigned to a lot", "Stock on hand: 16")
	if strings.Contains(body, "8303") {
		t.Errorf("zero-balance lot 8303 shown\n%s", body)
	}
}

func TestIntegration_PopoutPartBelowMin(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, body := popoutBody(t, h.PopoutPartBelowMin, "3007")
	expectContains(t, "below-min 3007", body, `href="/po/5003"`, "30 remaining")
	_, body = popoutBody(t, h.PopoutPartBelowMin, "3001")
	expectContains(t, "below-min 3001", body, `href="/po/5002"`)
	if strings.Contains(body, "/po/5001") {
		t.Errorf("draft PO 5001 listed\n%s", body)
	}
}

func TestIntegration_PopoutPartBOMComponent(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutPartBOMComponent, "3002")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expectContains(t, "bom-component", body, `href="/part/3005"`, "Precision Machining", "$0.0300")
}

func TestIntegration_PopoutSupplier(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutSupplier, "1002")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expectContains(t, "supplier", body, "Bob Lee", "555-0201", "Open POs: 1", "72.80", "2026-06-01")
	// a supplier with no POs (seed company 1003) has no last-order date
	code, body = popoutBody(t, h.PopoutSupplier, "1003")
	if code != 200 {
		t.Fatalf("supplier without POs: status %d", code)
	}
	expectContains(t, "supplier without POs", body, "Open POs: 0", "Last order: none")
	if code, _ := popoutBody(t, h.PopoutSupplier, "999999"); code != http.StatusNotFound {
		t.Errorf("unknown supplier: status %d, want 404", code)
	}
}

func TestIntegration_PopoutPO(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, body := popoutBody(t, h.PopoutPO, "5002")
	expectContains(t, "po 5002", body, "72.80", "0 of 2 lines received", "RAW-1001")
	_, body = popoutBody(t, h.PopoutPO, "5003")
	expectContains(t, "po 5003", body, "0 of 1 lines received")
	if code, _ := popoutBody(t, h.PopoutPO, "NOPE"); code != http.StatusNotFound {
		t.Errorf("unknown PO: status %d, want 404", code)
	}
}

func TestIntegration_PopoutContact(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutContact, "2003")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expectContains(t, "contact", body, "Precision Machining", "555-0201", `href="/po/5002"`)
	if code, _ := popoutBody(t, h.PopoutContact, "999999"); code != http.StatusNotFound {
		t.Errorf("unknown contact: status %d, want 404", code)
	}
}

func TestIntegration_PartOrders_SupplierTrigger(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	rec := httptest.NewRecorder()
	h.PartOrders(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3002/orders", nil), 3002))
	expectContains(t, "part orders", rec.Body.String(), `data-popout-url="/popout/supplier/1002"`, `data-popout-url="/popout/po/5002"`)
}

func TestIntegration_PopoutTriggers(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	render := func(fn http.HandlerFunc, id int) string {
		rec := httptest.NewRecorder()
		fn(rec, withID(httptest.NewRequest(http.MethodGet, "/x", nil), id))
		return rec.Body.String()
	}
	expectContains(t, "sourcing", render(h.PartSourcing, 3002), `/popout/supplier/1002`)
	bom := render(h.PartBOM, 3005)
	expectContains(t, "bom", bom, `/popout/part/3002/bom-component`)
	if strings.Contains(bom, `/popout/part/3002/where-used`) {
		t.Error("BOM still uses the where-used popout for components")
	}
	expectContains(t, "po detail", render(h.PODetail, 5002), `/popout/contact/2003`)
	expectContains(t, "part header", render(h.PartDetail, 3007), `/popout/part/3007/stock`, `/popout/part/3007/below-min`)
	expectContains(t, "part detail recent POs", render(h.PartDetail, 3007), `/popout/po/5003`)
	expectContains(t, "transactions", render(h.PartTransactions, 3007), `/popout/po/`)
}
