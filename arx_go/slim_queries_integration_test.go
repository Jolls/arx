//go:build integration

package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// captureSQL runs fn with DEBUG_MODE on and returns every SQL statement the
// [SQL] logger saw (#264: the slim-query call sites are asserted by statement shape).
func captureSQL(t *testing.T, h *Handler, fn func()) []string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags, prevDebug := log.Writer(), log.Flags(), h.cfg().DebugMode
	log.SetOutput(&buf)
	log.SetFlags(0)
	h.cfg().DebugMode = true
	defer func() {
		h.cfg().DebugMode = prevDebug
		log.SetFlags(prevFlags)
		log.SetOutput(prevOut)
	}()
	fn()
	var out []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(l, "[SQL] ") {
			out = append(out, l)
		}
	}
	return out
}

// assertNoSQLContaining fails for every captured statement containing marker.
func assertNoSQLContaining(t *testing.T, label string, stmts []string, marker string) {
	t.Helper()
	for _, s := range stmts {
		if strings.Contains(s, marker) {
			t.Errorf("%s ran a wide query (contains %q): %.200s", label, marker, s)
		}
	}
}

// PartBOMEdit on a part with no BOM lines renders an empty editor.
func TestIntegration_PartBOMEdit_EmptyBOM(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PartBOMEdit(rec, withID(httptest.NewRequest(http.MethodGet, "/part/x/bom/edit", nil), f.L1))
	body := rec.Body.String()
	if strings.Contains(body, "pl[") {
		t.Errorf("PartBOMEdit on a leaf rendered BOM line inputs")
	}
}

// fetchSupplierBulkOrderOptions falls back to the column defaults for a nil or unknown supplier.
func TestIntegration_BulkOrderOptions_NilAndUnknownSupplier(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	req := getReq("/po/x")
	unknown := 2147483000
	for label, id := range map[string]*int{"nil": nil, "unknown": &unknown} {
		d, src := h.fetchSupplierBulkOrderOptions(req, id)
		if d != "comma" || src != "internal" {
			t.Errorf("%s supplier: got (%q, %q), want (comma, internal)", label, d, src)
		}
	}
}

// The #264 call sites must read only what they need: no ~40-column part select per PO line,
// no price/EXISTS subquery for the BOM editor, no contact join for a supplier code, no
// thumbnail/below-min columns for the parts CSV.
func TestIntegration_SlimQueries_ResolvePolRev(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()
	stmts := captureSQL(t, h, func() { h.resolvePolRev(getReq("/po/x"), "", strconv.Itoa(f.P1)) })
	if len(stmts) != 1 {
		t.Fatalf("resolvePolRev ran %d statements, want 1", len(stmts))
	}
	assertNoSQLContaining(t, "resolvePolRev", stmts, "JOIN uom")
}

func TestIntegration_SlimQueries_PartBOMEdit(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()
	stmts := captureSQL(t, h, func() {
		h.PartBOMEdit(httptest.NewRecorder(), withID(httptest.NewRequest(http.MethodGet, "/part/x/bom/edit", nil), f.P))
	})
	assertNoSQLContaining(t, "PartBOMEdit", stmts, "MIN(p.price_ea)")
}

func TestIntegration_SlimQueries_POSupplierCode(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()
	stmts := captureSQL(t, h, func() { h.POPrint(httptest.NewRecorder(), withIDStr(getReq("/po/x/print"), f.Full)) })
	assertNoSQLContaining(t, "POPrint", stmts, "contact_phone")
}

func TestIntegration_SlimQueries_BulkOrderOptions(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	sid := 1001
	stmts := captureSQL(t, h, func() { h.fetchSupplierBulkOrderOptions(getReq("/po/x"), &sid) })
	if len(stmts) != 1 {
		t.Fatalf("fetchSupplierBulkOrderOptions ran %d statements, want 1", len(stmts))
	}
	assertNoSQLContaining(t, "fetchSupplierBulkOrderOptions", stmts, "contact_phone")
}

func TestIntegration_SlimQueries_PartsExportCSV(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	stmts := captureSQL(t, h, func() {
		h.PartsExportCSV(httptest.NewRecorder(), httptest.NewRequest("GET", "/parts/export.csv", nil))
	})
	assertNoSQLContaining(t, "PartsExportCSV", stmts, "below_min")
	assertNoSQLContaining(t, "PartsExportCSV", stmts, "part_attachment")
}

