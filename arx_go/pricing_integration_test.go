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
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pricingFixture is a BUY part preferring supplier 1002, with prices:
// ActA/ActB active 1002 rows (pack 10 / pack 1), InC an inactive 1002 pack-1
// row, InAcme the only (inactive) 1001 row, and Bare a 1003 row with every
// nullable column NULL. Other is a second part with its own active price.
type pricingFixture struct {
	Part, Other                   int
	ActA, ActB, InC, InAcme, Bare int
	OtherPrice                    int
}

func seedPricing(t *testing.T, h *Handler) (f pricingFixture, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	base := smokeUniq("ITEST-PRC")
	cleanup = func() {
		for _, id := range []int{f.Part, f.Other} {
			smokeExec(ctx, h, `DELETE FROM price WHERE part_id=$1`, id)
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
	}
	scan := func(q string, args ...any) int {
		t.Helper()
		var id int
		if err := h.queryRowContext(ctx, q, args...).Scan(&id); err != nil {
			cleanup()
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	part := `INSERT INTO part (part_number, category, default_supplier_id) VALUES ($1,'BUY',1002) RETURNING id`
	f.Part = scan(part, base+"-A")
	f.Other = scan(part, base+"-B")
	price := `INSERT INTO price (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`
	f.ActA = scan(price, f.Part, 1002, 10, 0.2, 2, "2026-03-01", true)
	f.ActB = scan(price, f.Part, 1002, 1, 0.25, 0.25, "2026-03-01", true)
	f.InC = scan(price, f.Part, 1002, 1, 0.3, 0.3, "2026-01-01", false)
	f.InAcme = scan(price, f.Part, 1001, 1, 0.5, 0.5, "2026-01-01", false)
	f.Bare = scan(price, f.Part, 1003, nil, nil, nil, nil, nil)
	f.OtherPrice = scan(price, f.Other, 1002, 1, 9, 9, "2026-01-01", true)
	return f, cleanup
}

// priceRow reads price id as JSON columns; nil when the row is gone.
func priceRow(t *testing.T, h *Handler, id int) map[string]any {
	t.Helper()
	var raw string
	err := h.queryRowContext(context.Background(),
		`SELECT COALESCE((SELECT row_to_json(p)::text FROM price p WHERE id=$1), 'null')`, id).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// defaultSupplier reads a part's default_supplier_id (0 when NULL).
func defaultSupplier(t *testing.T, h *Handler, part int) int {
	t.Helper()
	var v *int
	if err := h.queryRowContext(context.Background(),
		`SELECT default_supplier_id FROM part WHERE id=$1`, part).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return 0
	}
	return *v
}

func pricePost(h *Handler, fn http.HandlerFunc, target string, part int, vals url.Values, priceID int) *httptest.ResponseRecorder {
	req := postForm(target, vals)
	if priceID != 0 {
		req = withIDAndPriceID(req, part, priceID)
	} else {
		req = withID(req, part)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// withIDAndPriceID sets the "id" and "priceID" path values.
func withIDAndPriceID(req *http.Request, id, priceID int) *http.Request {
	req.SetPathValue("id", strconv.Itoa(id))
	req.SetPathValue("priceID", strconv.Itoa(priceID))
	return req
}

var (
	priceGroupSupplier = regexp.MustCompile(`href="/supplier/(\d+)"`)
	priceGroupRowID    = regexp.MustCompile(`/pricing/(\d+)/(?:edit|activate)"`)
	squashSpace        = regexp.MustCompile(`\s+`)
)

// TestIntegration_PartPricing_Renders pins the supplier groups (by supplier
// name), their row order, the Inactive/Preferred badges and NULL price cells.
func TestIntegration_PartPricing_Renders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPricing(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PartPricing(rec, withID(httptest.NewRequest(http.MethodGet, "/part/x/pricing", nil), f.Part))
	var got []string
	chunks := strings.Split(rec.Body.String(), `<div class="detail-section">`)[1:]
	for _, c := range chunks {
		g := priceGroupSupplier.FindStringSubmatch(c)[1]
		if strings.Contains(c, ">Inactive</span>") {
			g += " inactive"
		}
		if strings.Contains(c, ">Preferred</span>") {
			g += " preferred"
		}
		for _, m := range priceGroupRowID.FindAllStringSubmatch(c, -1) {
			g += " " + m[1]
		}
		got = append(got, g)
	}
	want := []string{
		fmt.Sprintf("1001 inactive %d", f.InAcme),
		fmt.Sprintf("1003 inactive %d", f.Bare),
		fmt.Sprintf("1002 preferred %d %d %d", f.ActB, f.ActA, f.InC),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pricing groups:\n got %q\nwant %q", got, want)
	}
	if len(chunks) == 3 {
		prec := squashSpace.ReplaceAllString(chunks[2], " ")
		for _, s := range []string{">Precision Machining Co</a>", "$0.250000", "$2.0000", "$0.200000"} {
			if !strings.Contains(prec, s) {
				t.Errorf("1002 group missing %q", s)
			}
		}
		bare := squashSpace.ReplaceAllString(chunks[1], " ")
		if n := strings.Count(bare, `<td class="text-end"> — </td>`); n != 2 {
			t.Errorf("1003 group: %d blank price cells, want 2", n)
		}
	}
}

// TestIntegration_PriceEdit_Renders pins the edit form's values and the
// not-found guard for another part's price.
func TestIntegration_PriceEdit_Renders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPricing(t, h)
	defer cleanup()

	get := func(price int) string {
		rec := httptest.NewRecorder()
		h.PriceEdit(rec, withIDAndPriceID(httptest.NewRequest(http.MethodGet, "/x", nil), f.Part, price))
		return squashSpace.ReplaceAllString(rec.Body.String(), " ")
	}
	body := get(f.ActA)
	for _, s := range []string{
		fmt.Sprintf(`action="/part/%d/pricing/%d"`, f.Part, f.ActA), `value="Precision Machining Co"`, `value="1002"`,
		`required value="10"`, `value="0.2"`, `value="2"`, `value="2026-03-01"`,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("PriceEdit(ActA): body missing %q", s)
		}
	}
	if body := get(f.Bare); !strings.Contains(body, `required value=""`) || !strings.Contains(body, `value="1003"`) {
		t.Error("PriceEdit(Bare): want blank pack size and supplier 1003")
	}
	if body := get(f.OtherPrice); !strings.Contains(body, "Price not found") {
		t.Error("PriceEdit(another part's price): want Price not found")
	}
}

// TestIntegration_PriceCreate_Columns pins the inserted columns, the pack/each
// fill-in, the today default, the first-price default supplier, and errors.
func TestIntegration_PriceCreate_Columns(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPricing(t, h)
	defer cleanup()
	ctx := context.Background()
	smokeExec(ctx, h, `UPDATE part SET default_supplier_id=NULL WHERE id=$1`, f.Part)

	newest := func() map[string]any {
		t.Helper()
		var id int
		if err := h.queryRowContext(ctx, `SELECT MAX(id) FROM price WHERE part_id=$1`, f.Part).Scan(&id); err != nil {
			t.Fatal(err)
		}
		m := priceRow(t, h, id)
		delete(m, "id")
		return m
	}
	target := fmt.Sprintf("/part/%d/pricing", f.Part)
	rec := pricePost(h, h.PriceCreate, target, f.Part, url.Values{"supplier_id": {"1001"}, "pack_size": {"4"}, "price_ea": {"1.5"}, "effective_date": {"2026-05-06"}}, 0)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != target {
		t.Fatalf("PriceCreate: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	want := map[string]any{"part_id": float64(f.Part), "supplier_id": 1001.0, "pack_size": 4.0, "price_ea": 1.5, "price_pack": 6.0,
		"effective_date": "2026-05-06", "is_active": true}
	if got := newest(); !reflect.DeepEqual(got, want) {
		t.Errorf("created price:\n got %v\nwant %v", got, want)
	}
	if got := defaultSupplier(t, h, f.Part); got != 1001 {
		t.Errorf("default supplier after first price = %d, want 1001", got)
	}

	// Blank pack size and date: NULL pack, no fill-in, dated today; the default supplier stays.
	pricePost(h, h.PriceCreate, target, f.Part, url.Values{"supplier_id": {"1003"}, "price_pack": {"7"}}, 0)
	want = map[string]any{"part_id": float64(f.Part), "supplier_id": 1003.0, "pack_size": nil, "price_ea": nil, "price_pack": 7.0,
		"effective_date": userToday(h), "is_active": true}
	if got := newest(); !reflect.DeepEqual(got, want) {
		t.Errorf("created price (blanks):\n got %v\nwant %v", got, want)
	}
	if got := defaultSupplier(t, h, f.Part); got != 1001 {
		t.Errorf("default supplier after second price = %d, want 1001", got)
	}

	for name, c := range map[string]struct {
		vals url.Values
		want string
	}{
		"no supplier": {url.Values{"price_ea": {"1"}}, "Invalid supplier"},
		"duplicate active pack": {url.Values{"supplier_id": {"1002"}, "pack_size": {"10"}, "price_ea": {"1"}},
			"A price already exists for this supplier and pack size."},
	} {
		if body := pricePost(h, h.PriceCreate, target, f.Part, c.vals, 0).Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("PriceCreate %s: body missing %q", name, c.want)
		}
	}
}

// TestIntegration_PriceUpdate pins update as deactivate-old + insert-new, and
// the rollback when the new row collides with another active price.
func TestIntegration_PriceUpdate(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPricing(t, h)
	defer cleanup()
	ctx := context.Background()

	target := fmt.Sprintf("/part/%d/pricing/%d", f.Part, f.ActB)
	rec := pricePost(h, h.PriceUpdate, target, f.Part, url.Values{"supplier_id": {"1002"}, "pack_size": {"1"}, "price_pack": {"0.27"}, "effective_date": {"2026-06-07"}}, f.ActB)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != fmt.Sprintf("/part/%d/pricing", f.Part) {
		t.Fatalf("PriceUpdate: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if priceRow(t, h, f.ActB)["is_active"] != false {
		t.Error("PriceUpdate: old row still active")
	}
	var newID int
	if err := h.queryRowContext(ctx, `SELECT MAX(id) FROM price WHERE part_id=$1`, f.Part).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	got := priceRow(t, h, newID)
	delete(got, "id")
	want := map[string]any{"part_id": float64(f.Part), "supplier_id": 1002.0, "pack_size": 1.0, "price_ea": 0.27, "price_pack": 0.27,
		"effective_date": "2026-06-07", "is_active": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PriceUpdate new row:\n got %v\nwant %v", got, want)
	}

	// Moving the new row onto ActA's pack size collides: rolled back, the edited row stays active.
	body := pricePost(h, h.PriceUpdate, target, f.Part, url.Values{"supplier_id": {"1002"}, "pack_size": {"10"}, "price_ea": {"1"}}, newID).Body.String()
	if !strings.Contains(body, "A price already exists for this supplier and pack size.") {
		t.Error("PriceUpdate collision: want the duplicate-price message")
	}
	if priceRow(t, h, newID)["is_active"] != true {
		t.Error("PriceUpdate collision: edited row was deactivated despite the rollback")
	}
	// Another part's price id: its row is untouched (the insert still happens).
	pricePost(h, h.PriceUpdate, target, f.Part, url.Values{"supplier_id": {"1001"}, "pack_size": {"3"}, "price_ea": {"1"}}, f.OtherPrice)
	if priceRow(t, h, f.OtherPrice)["is_active"] != true {
		t.Error("PriceUpdate: deactivated another part's price")
	}
	if body := pricePost(h, h.PriceUpdate, target, f.Part, url.Values{"price_ea": {"1"}}, f.ActA).Body.String(); !strings.Contains(body, "Invalid supplier") {
		t.Error("PriceUpdate no supplier: want Invalid supplier")
	}
}

// TestIntegration_PriceActivateDeactivateDelete pins the per-row actions, the
// part guard, delete's inactive-only rule, and activate's collision error.
func TestIntegration_PriceActivateDeactivateDelete(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPricing(t, h)
	defer cleanup()

	act := func(fn http.HandlerFunc, price int) string {
		rec := pricePost(h, fn, "/x", f.Part, url.Values{}, price)
		if rec.Code == http.StatusSeeOther {
			return ""
		}
		return rec.Body.String()
	}
	if body := act(h.PriceDeactivate, f.ActA); body != "" {
		t.Fatalf("PriceDeactivate: %s", body)
	}
	if priceRow(t, h, f.ActA)["is_active"] != false {
		t.Error("PriceDeactivate: row still active")
	}
	act(h.PriceDeactivate, f.OtherPrice)
	if priceRow(t, h, f.OtherPrice)["is_active"] != true {
		t.Error("PriceDeactivate: deactivated another part's price")
	}
	if body := act(h.PriceActivate, f.ActA); body != "" {
		t.Fatalf("PriceActivate: %s", body)
	}
	if priceRow(t, h, f.ActA)["is_active"] != true {
		t.Error("PriceActivate: row still inactive")
	}
	// InC shares ActB's supplier and pack size.
	if body := act(h.PriceActivate, f.InC); !strings.Contains(body, "Cannot activate: another active price exists for this supplier and pack size.") {
		t.Error("PriceActivate collision: want the duplicate-price message")
	}
	act(h.PriceDelete, f.ActB)
	if priceRow(t, h, f.ActB) == nil {
		t.Error("PriceDelete: deleted an active price")
	}
	act(h.PriceDelete, f.InC)
	if priceRow(t, h, f.InC) != nil {
		t.Error("PriceDelete: inactive price still there")
	}
	smokeExec(context.Background(), h, `UPDATE price SET is_active=FALSE WHERE id=$1`, f.OtherPrice)
	act(h.PriceDelete, f.OtherPrice)
	if priceRow(t, h, f.OtherPrice) == nil {
		t.Error("PriceDelete: deleted another part's price")
	}
}

// TestIntegration_PricePreferred pins setting the preferred supplier.
func TestIntegration_PricePreferred(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPricing(t, h)
	defer cleanup()

	rec := pricePost(h, h.PricePreferred, "/x", f.Part, url.Values{"supplier_id": {"1001"}}, 0)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != fmt.Sprintf("/part/%d/pricing", f.Part) {
		t.Fatalf("PricePreferred: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := defaultSupplier(t, h, f.Part); got != 1001 {
		t.Errorf("default supplier = %d, want 1001", got)
	}
	if body := pricePost(h, h.PricePreferred, "/x", f.Part, url.Values{"supplier_id": {"0"}}, 0).Body.String(); !strings.Contains(body, "Invalid supplier") {
		t.Error("PricePreferred(0): want Invalid supplier")
	}
}
