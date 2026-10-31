//go:build integration

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// traceCard returns the Traceability card's HTML from a part Summary page, or "" when the card
// is absent.
func traceCard(t *testing.T, h *Handler, id int) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.PartDetail(rec, withID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/part/%d/summary", id), nil), id))
	assertStatus(t, "PartDetail", rec, http.StatusOK)
	body := rec.Body.String()
	i := strings.Index(body, "<span>Traceability</span>")
	if i < 0 {
		return ""
	}
	return body[i:]
}

// cardCount returns the link text of the card row whose href starts with prefix ("" if no such link).
func cardCount(card, prefix string) string {
	i := strings.Index(card, `href="`+prefix)
	if i < 0 {
		return ""
	}
	rest := card[i:]
	a, b := strings.Index(rest, ">"), strings.Index(rest, "</a>")
	return rest[a+1 : b]
}

// Seed: 3013 ASM-1003 (lot_serial, BOM): 1 lot, 1 unit, 1 build. 3007 RAW-1002 (lot): 2 lots,
// no BOM. 3005 ASM-1001 (serial, BOM): 2 units, 1 build.
func TestIntegration_PartTraceabilityCard(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	for _, c := range []struct {
		id                  int
		pn                  string
		lots, units, builds string
	}{
		{3013, "ASM-1003", "1", "1", "1"},
		{3007, "RAW-1002", "2", "", ""},
		{3005, "ASM-1001", "", "2", "1"},
	} {
		card := traceCard(t, h, c.id)
		if card == "" {
			t.Errorf("part %d: no Traceability card", c.id)
			continue
		}
		for _, w := range []struct{ path, want string }{{"/lots", c.lots}, {"/units", c.units}, {"/builds", c.builds}} {
			href := w.path + "?f.col-pn=" + c.pn
			if got := cardCount(card, href); got != w.want {
				t.Errorf("part %d: %s count = %q, want %q", c.id, w.path, got, w.want)
			}
		}
	}
}

// A part with no lot/serial tracking and no BOM gets no card.
func TestIntegration_PartTraceabilityCard_Hidden(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	if card := traceCard(t, h, 3002); card != "" {
		t.Errorf("part 3002: unexpected Traceability card")
	}
}

func TestIntegration_CountPartBuilds(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	for id, want := range map[int]int{3005: 1, 3012: 1, 3013: 1, 3002: 0} {
		got, err := h.inventory().CountPartBuilds(t.Context(), id)
		if err != nil || got != want {
			t.Errorf("CountPartBuilds(%d) = %d, %v; want %d", id, got, err, want)
		}
	}
}
