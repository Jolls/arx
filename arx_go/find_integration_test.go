//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func findReq(t *testing.T, h *Handler, q string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Find(rec, httptest.NewRequest(http.MethodGet, "/find?q="+url.QueryEscape(q), nil))
	return rec
}

func TestIntegration_Find_ResolvesLotUnitBuild(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	for q, want := range map[string]string{
		"8302":        "/part/3012/lots/8302", // lot matched on id AND lot_number: one result
		"8303":        "/part/3007/lots/8303",
		"SN-3013-001": "/part/3013/units/8501",
		"8501":        "/part/3013/units/8501",
		"8203":        "/part/3013/lots/8306", // build with an output lot
		"8201":        "/part/3005/build",     // build with no output lot
		"  8302 ":     "/part/3012/lots/8302", // surrounding whitespace is trimmed
	} {
		rec := findReq(t, h, q)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("q=%q: %d Location %q; want 302 %q", q, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestIntegration_Find_AmbiguousListsAll(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	rec := findReq(t, h, "8301")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Fatalf("status %d Location %q; want 200, none", rec.Code, rec.Header().Get("Location"))
	}
	for _, want := range []string{`/part/3007/lots/8301`, `/part/3012/units/8505`, "ID, Lot number", "Serial number", "<td>8301</td>", "RAW-1002", "ASM-1002"} {
		if !strings.Contains(body, want) {
			t.Errorf("pick list missing %q", want)
		}
	}
}

func TestIntegration_Find_NoMatch(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	for _, q := range []string{"NO-SUCH-VALUE", "99999999999", "0"} {
		rec := findReq(t, h, q)
		if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" || !strings.Contains(rec.Body.String(), "No record found for") {
			t.Errorf("q=%q: %d Location %q; want 200 with no-match message", q, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestIntegration_Find_BlankQ(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	for _, q := range []string{"", "   "} {
		rec := findReq(t, h, q)
		if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" || strings.Contains(rec.Body.String(), "No record found") {
			t.Errorf("q=%q: %d Location %q; want a plain 200 page", q, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestIntegration_Find_ExactOnly(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	for _, q := range []string{"SN-3013", "sn-3013-001"} {
		if body := findReq(t, h, q).Body.String(); !strings.Contains(body, "No record found for") {
			t.Errorf("q=%q matched; lookup must be exact and case-sensitive", q)
		}
	}
}
