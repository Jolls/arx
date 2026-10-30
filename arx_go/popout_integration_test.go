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

func TestIntegration_PopoutUnit(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutUnit, "8501")
	if code != 200 {
		t.Fatalf("8501: status %d", code)
	}
	expectContains(t, "unit 8501", body, "SN-3013-001", "ASM-1003", `href="/part/3013/lots/8306"`, "Build #8203",
		"2 records: 0 pass, 1 fail", `href="/records/7014"`, `href="/records/7013"`, "Fail", "Not evaluated")
	_, body = popoutBody(t, h.PopoutUnit, "8503")
	expectContains(t, "unit 8503", body, "1 record: 1 pass, 0 fail", "Pass", "Consumed into", "SN-3013-001")
	_, body = popoutBody(t, h.PopoutUnit, "8504")
	expectContains(t, "unit 8504", body, "&mdash;", "No test records.")
	for _, id := range []string{"999999", "abc"} {
		if code, _ := popoutBody(t, h.PopoutUnit, id); code != 404 {
			t.Errorf("unit %s: status %d, want 404", id, code)
		}
	}
}

func TestIntegration_PopoutBuild(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	code, body := popoutBody(t, h.PopoutBuild, "8203")
	if code != 200 {
		t.Fatalf("8203: status %d", code)
	}
	expectContains(t, "build 8203", body, "Build #8203", "ASM-1003", `href="/part/3012/lots/8302"`, `href="/part/3007/lots/8303"`,
		`href="/part/3007/lots/8301"`, `href="/part/3005/units/8503"`, `href="/part/3013/units/8501"`, `href="/part/3013/lots/8306"`)
	_, body = popoutBody(t, h.PopoutBuild, "8201")
	expectContains(t, "build 8201", body, "No components recorded.", `href="/part/3005/units/8503"`)
	_, body = popoutBody(t, h.PopoutBuild, "8202")
	expectContains(t, "build 8202", body, `href="/part/3007/lots/8301"`, "No units produced.")
	for _, id := range []string{"999999", "abc"} {
		if code, _ := popoutBody(t, h.PopoutBuild, id); code != 404 {
			t.Errorf("build %s: status %d, want 404", id, code)
		}
	}
}

func TestIntegration_PopoutPartThumb(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for _, id := range []string{"999999", "abc"} {
		if code, _ := popoutBody(t, h.PopoutPartThumb, id); code != 404 {
			t.Errorf("thumb %s: status %d, want 404", id, code)
		}
	}
	code, body := popoutBody(t, h.PopoutPartThumb, "3005")
	if code != 200 || strings.Contains(body, "Used in") {
		t.Errorf("thumb 3005: status %d body %q", code, body)
	}
}

func TestIntegration_PopoutUnitBuildTriggers(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	get := func(fn http.HandlerFunc, target string, r func(*http.Request) *http.Request) string {
		rec := httptest.NewRecorder()
		fn(rec, r(httptest.NewRequest(http.MethodGet, target, nil)))
		return rec.Body.String()
	}
	none := func(r *http.Request) *http.Request { return r }
	// All Units/Builds rows are built client-side (#411); their triggers are checked in app.js.
	expectContains(t, "all units rows", get(h.AllUnitsRows, "/api/units/rows", none), `"id":8501`, `"id":8503`)
	expectContains(t, "all builds rows", get(h.AllBuildsRows, "/api/builds/rows", none), `"id":8203`)
	expectContains(t, "part units", get(h.PartUnits, "/x", func(r *http.Request) *http.Request { return withID(r, 3013) }),
		`/popout/unit/8501`, `/popout/build/8203`)
	expectContains(t, "unit trace", get(h.PartUnitTrace, "/x", func(r *http.Request) *http.Request { return withIDAndUnitID(r, 3013, 8501) }),
		`/popout/build/8203`, `/popout/unit/8503`)
	expectContains(t, "part detail", get(h.PartDetail, "/x", func(r *http.Request) *http.Request { return withID(r, 3013) }), `/popout/unit/8501`)
	expectContains(t, "record", get(h.RecordDetail, "/x", func(r *http.Request) *http.Request { return withID(r, 7013) }), `/popout/build/8203`)
}
