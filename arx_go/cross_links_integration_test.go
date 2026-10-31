//go:build integration

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Seed (#415): record 7011 -> unit 8503 (part 3005). Lot 8301 (part 3007) was received on PO 5003
// (line 5504, receipt txn 5901) and holds unit 8502; record 7012 is on unit 8502 + lot 8301.
// Lot 8306 (part 3013) holds unit 8501. Lot 8302 (part 3012) has no PO and no units.

func crossGet(t *testing.T, label string, fn http.HandlerFunc, req *http.Request) string {
	t.Helper()
	rec := httptest.NewRecorder()
	fn(rec, req)
	assertStatus(t, label, rec, http.StatusOK)
	return rec.Body.String()
}

func lotTraceBody(t *testing.T, h *Handler, partID, lotID int) string {
	t.Helper()
	req := withIDAndLotID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/part/%d/lots/%d", partID, lotID), nil), partID, lotID)
	return crossGet(t, "PartLotTrace", h.PartLotTrace, req)
}

func expectLinks(t *testing.T, label, body string, present bool, hrefs ...string) {
	t.Helper()
	for _, href := range hrefs {
		if strings.Contains(body, href) != present {
			t.Errorf("%s: contains %q = %v, want %v", label, href, !present, present)
		}
	}
}

// G1: a record links to its unit page.
func TestIntegration_RecordLinksUnit(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	href := `href="/part/3005/units/8503"`
	req := func(path string) *http.Request { return withID(httptest.NewRequest(http.MethodGet, path, nil), 7011) }
	expectLinks(t, "RecordDetail", crossGet(t, "RecordDetail", h.RecordDetail, req("/records/7011")), true, href)
	edit := crossGet(t, "EditRecord", h.EditRecord, req("/records/7011/edit"))
	expectLinks(t, "EditRecord", edit, true, href)
	if !strings.Contains(edit, href+` target="_blank"`) {
		t.Errorf("EditRecord: unit link should open in a new tab (in-progress test session)")
	}
}

// G3 + G9: a purchased lot links to its PO; a manual/built lot does not.
func TestIntegration_LotAndRecordLinkPO(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	expectLinks(t, "lot 8301", lotTraceBody(t, h, 3007, 8301), true, `href="/po/5003"`)
	expectLinks(t, "lot 8302", lotTraceBody(t, h, 3012, 8302), false, `href="/po/`)
	rec := crossGet(t, "RecordDetail", h.RecordDetail, withID(httptest.NewRequest(http.MethodGet, "/records/7012", nil), 7012))
	expectLinks(t, "record 7012", rec, true, `href="/po/5003"`)
}

// G5 (+G6): the PO Receipts table links each receipt to the lot it created.
func TestIntegration_PODetailReceiptLinksLot(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	body := crossGet(t, "PODetail", h.PODetail, withIDStr(httptest.NewRequest(http.MethodGet, "/po/5003", nil), "5003"))
	expectLinks(t, "PODetail 5003", body, true, `href="/part/3007/lots/8301"`)
}

// G8: the lot page lists the units living in the lot.
func TestIntegration_LotTraceListsUnits(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	expectLinks(t, "lot 8301", lotTraceBody(t, h, 3007, 8301), true, `href="/part/3007/units/8502"`)
	expectLinks(t, "lot 8306", lotTraceBody(t, h, 3013, 8306), true, `href="/part/3013/units/8501"`)
	body := lotTraceBody(t, h, 3012, 8302)
	expectLinks(t, "lot 8302", body, false, `href="/part/3012/units/`)
	if !strings.Contains(body, "No units in this lot.") {
		t.Errorf("lot 8302: missing empty-state text for units")
	}
}

// G12: the lot page links to the part's Transactions tab.
func TestIntegration_LotTraceLinksTransactions(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	expectLinks(t, "lot 8301", lotTraceBody(t, h, 3007, 8301), true, `href="/part/3007/transactions">View transactions</a>`)
}
