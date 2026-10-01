//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// seedAPIParts inserts parts by part number with optional description/detail (nil → NULL) and
// returns their ids plus a cleanup that also clears their supplier_part/price rows.
func seedAPIParts(t *testing.T, h *Handler, parts [][3]any) (ids []int, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	cleanup = func() {
		for _, id := range ids {
			for _, tbl := range []string{"price", "supplier_part"} {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id=$1`, tbl), id)
			}
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
	}
	for _, p := range parts {
		var id int
		if err := h.queryRowContext(ctx, `INSERT INTO part (part_number, description, detail)
			VALUES ($1,$2,$3) RETURNING id`, p[0], p[1], p[2]).Scan(&id); err != nil {
			cleanup()
			t.Fatalf("seed part %v: %v", p[0], err)
		}
		ids = append(ids, id)
	}
	return ids, cleanup
}

type partSearchHit struct {
	PNID        int    `json:"pnid"`
	PartNumber  string `json:"part_number"`
	Revision    string `json:"revision"`
	Description string `json:"description"`
	Detail      string `json:"detail"`
}

func partSearch(t *testing.T, h *Handler, query string) (raw string, hits []partSearchHit) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.APIPartSearch(rec, httptest.NewRequest(http.MethodGet, "/api/parts/search?"+query, nil))
	assertStatus(t, "APIPartSearch", rec, http.StatusOK)
	raw = strings.TrimSpace(rec.Body.String())
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	return raw, hits
}

// APIPartSearch matches part_number (case-insensitive substring) by default, description/detail
// when by=desc, ordered by part_number; NULL description/detail come back as "".
func TestIntegration_APIPartSearch(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	base, tok := smokeUniq("APS"), smokeUniq("dsc")
	ids, cleanup := seedAPIParts(t, h, [][3]any{
		{base + "-B", nil, nil},
		{base + "-A", "Widget " + tok, nil},
		{base + "-C", nil, "note " + tok},
	})
	defer cleanup()
	b, a, c := ids[0], ids[1], ids[2]

	if raw, _ := partSearch(t, h, "q=A"); raw != "[]" {
		t.Errorf("short q body = %s, want []", raw)
	}

	_, hits := partSearch(t, h, "q="+base)
	want := []partSearchHit{
		{PNID: a, PartNumber: base + "-A", Description: "Widget " + tok},
		{PNID: b, PartNumber: base + "-B"},
		{PNID: c, PartNumber: base + "-C", Detail: "note " + tok},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Errorf("by pn = %+v, want %+v", hits, want)
	}

	// by=desc ignores part_number and matches either description or detail.
	if _, hits := partSearch(t, h, "by=desc&q="+base); len(hits) != 0 {
		t.Errorf("by desc on a part number = %+v, want none", hits)
	}
	_, hits = partSearch(t, h, "by=desc&q="+tok)
	if !reflect.DeepEqual(hits, []partSearchHit{want[0], want[2]}) {
		t.Errorf("by desc = %+v, want %+v", hits, []partSearchHit{want[0], want[2]})
	}

	// Case-insensitive: the lowercased prefix matches the same rows.
	if _, lower := partSearch(t, h, "q="+strings.ToLower(base)); !reflect.DeepEqual(lower, want) {
		t.Errorf("lowercase q = %+v, want %+v", lower, want)
	}

	// No match is [] (was null, which broke the autocomplete's parts.length).
	if raw, _ := partSearch(t, h, "q="+base+"-ZZZ"); raw != "[]" {
		t.Errorf("no-match body = %s, want []", raw)
	}
}

// APIPartSearch returns at most 25 rows, the first by part_number.
func TestIntegration_APIPartSearch_Limit(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	base := smokeUniq("APL")
	var parts [][3]any
	for i := 26; i >= 1; i-- {
		parts = append(parts, [3]any{fmt.Sprintf("%s-%02d", base, i), nil, nil})
	}
	_, cleanup := seedAPIParts(t, h, parts)
	defer cleanup()

	_, hits := partSearch(t, h, "q="+base)
	if len(hits) != 25 || hits[0].PartNumber != base+"-01" || hits[24].PartNumber != base+"-25" {
		t.Fatalf("got %d hits (%+v), want %s-01..25", len(hits), hits, base)
	}
}

// APISupplierPN takes the lowest-preference supplier_part row for the pair and the
// smallest-pack active price; min_increment NULL/0 and price_ea NULL are omitted, and a missing
// link returns only an empty supplier_pn even when a price exists.
func TestIntegration_APISupplierPN_Cases(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()
	base := smokeUniq("ASP")
	ids, cleanup := seedAPIParts(t, h, [][3]any{{base + "-1", nil, nil}, {base + "-2", nil, nil}})
	defer cleanup()
	p1, p2 := ids[0], ids[1]
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := h.execContext(ctx, q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	sp := `INSERT INTO supplier_part (part_id, supplier_id, supplier_pn, min_increment, preference) VALUES ($1,$2,$3,$4,$5)`
	pr := `INSERT INTO price (part_id, supplier_id, price_ea, pack_size, is_active) VALUES ($1,$2,$3,$4,$5)`
	exec(sp, p1, 1003, "SP-2", 5, 2)
	exec(sp, p1, 1003, "SP-1", nil, 1)
	exec(pr, p1, 1003, 9, 1, false)
	exec(pr, p1, 1003, 0.4, 100, true)
	exec(pr, p1, 1003, 0.5, 10, true)
	exec(sp, p1, 1004, "SP-Z", 0, 1)
	exec(pr, p1, 1004, nil, 1, true)
	exec(pr, p2, 1003, 3, 1, true)
	exec(sp, p2, 1004, nil, 25, 1)

	for _, c := range []struct {
		query string
		want  map[string]any
	}{
		{fmt.Sprintf("part_id=%d&supplier_id=1003", p1), map[string]any{"supplier_pn": "SP-1", "price_ea": 0.5}},
		{fmt.Sprintf("part_id=%d&supplier_id=1004", p1), map[string]any{"supplier_pn": "SP-Z"}},
		{fmt.Sprintf("part_id=%d&supplier_id=1003", p2), map[string]any{"supplier_pn": ""}},
		{fmt.Sprintf("part_id=%d&supplier_id=1004", p2), map[string]any{"supplier_pn": "", "min_increment": float64(25)}},
		{fmt.Sprintf("part_id=%d", p1), map[string]any{"supplier_pn": ""}},
		{"part_id=abc&supplier_id=1003", map[string]any{"supplier_pn": ""}},
	} {
		rec := httptest.NewRecorder()
		h.APISupplierPN(rec, httptest.NewRequest(http.MethodGet, "/api/supplier-part?"+c.query, nil))
		assertStatus(t, "APISupplierPN", rec, http.StatusOK)
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: decode: %v (body %s)", c.query, err, rec.Body.String())
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.query, got, c.want)
		}
	}
}

// % and _ in the query stay wildcards (#266).
func TestIntegration_APIPartSearch_Wildcards(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	base, tok := smokeUniq("APE"), smokeUniq("dsc")
	ids, cleanup := seedAPIParts(t, h, [][3]any{
		{base + "-50%", nil, nil},
		{base + "-505", nil, nil},
		{base + "-A_1", tok + "_1", nil},
		{base + "-AX1", tok + "X1", nil},
	})
	defer cleanup()

	// "-5%" matches both -50% and -505, not -A_1.
	if _, hits := partSearch(t, h, "q="+url.QueryEscape(base+"-5%")); len(hits) != 2 || hits[0].PNID != ids[0] || hits[1].PNID != ids[1] {
		t.Errorf("q=%%: hits = %+v, want ids %d and %d", hits, ids[0], ids[1])
	}
	// _ is a single-character wildcard: "-A_1" matches both -A_1 and -AX1.
	if _, hits := partSearch(t, h, "q="+base+"-A_1"); len(hits) != 2 || hits[0].PNID != ids[2] || hits[1].PNID != ids[3] {
		t.Errorf("q=_: hits = %+v, want ids %d and %d", hits, ids[2], ids[3])
	}
	if _, hits := partSearch(t, h, "by=desc&q="+tok+"_1"); len(hits) != 2 {
		t.Errorf("by=desc q=_: hits = %+v, want 2", hits)
	}
}
