//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// withIDAndSpID injects chi route params "id" and "spID" (sourcing routes).
func withIDAndSpID(req *http.Request, id, spID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("spID", strconv.Itoa(spID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// seedSupplierPart creates a supplier_part row via SupplierPartCreate and
// returns its id plus a cleanup that hard-deletes it. SupplierPartCreate
// redirects to the parent list URL (no child id in Location), so the new id
// is captured via MAX(id) for the part, same as seedMfgPart.
func seedSupplierPart(t *testing.T, h *Handler, ctx context.Context, partID, supplierID int) (int, func()) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.SupplierPartCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/suppliers", partID), url.Values{
		"supplier_id":   {strconv.Itoa(supplierID)},
		"preference":    {"1"},
		"supplier_pn":   {smokeUniq("SMOKE-SPN")},
		"supplier_desc": {"smoke supplier part"},
		"lead_time":     {"5"},
	}), partID))
	if rec.Code >= 400 {
		t.Fatalf("seedSupplierPart: SupplierPartCreate status %d, body: %s", rec.Code, rec.Body.String())
	}
	var spID int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT MAX(id) FROM %s WHERE part_id=@p1`, h.cfg().SupplierPartTable()), partID,
	).Scan(&spID); err != nil {
		t.Fatalf("seedSupplierPart: capture new id: %v", err)
	}
	return spID, func() {
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg().SupplierPartTable()), spID)
	}
}

// TestIntegration_PartSourcing verifies PartSourcing's assembled page against
// pinned seed supplier_part 4002 (#817).
func TestIntegration_PartSourcing(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PartSourcing(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3002/suppliers", nil), 3002))
	assertStatus(t, "PartSourcing", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"PMC-M3X8", "Precision Machining Co", "2-3 weeks"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartSourcing(3002): body missing %q", want)
		}
	}
}

// TestIntegration_FetchActivePricesBySupplier verifies the active-price
// filter and pack_size ordering directly against pinned seed price rows for
// part 3002 / supplier 1002 (#817).
func TestIntegration_FetchActivePricesBySupplier(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/part/3002/suppliers", nil)
	prices := h.fetchActivePricesBySupplier(req, "3002")

	rows, ok := prices[1002]
	if !ok {
		t.Fatalf("prices[1002] not present, want 3 active price rows (got keys %v)", prices)
	}
	if len(rows) != 3 {
		t.Fatalf("len(prices[1002]) = %d, want 3 (inactive row 4202 must be excluded)", len(rows))
	}
	wantPackSize := []float64{1, 100, 1000}
	wantPriceEA := []float64{0.10, 0.05, 0.03}
	for i, row := range rows {
		if row.PackSize == nil || row.PriceEA == nil {
			t.Fatalf("prices[1002][%d] has nil PackSize/PriceEA: %+v", i, row)
		}
		assertFloatEqual(t, fmt.Sprintf("prices[1002][%d].PackSize", i), *row.PackSize, wantPackSize[i])
		assertFloatEqual(t, fmt.Sprintf("prices[1002][%d].PriceEA", i), *row.PriceEA, wantPriceEA[i])
	}
}

// TestIntegration_SupplierPartEdit verifies the edit form renders the seeded
// supplier_part row (#817).
func TestIntegration_SupplierPartEdit(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierPartEdit(rec, withIDAndSpID(httptest.NewRequest(http.MethodGet, "/part/x/suppliers/x/edit", nil), 3002, 4002))
	assertStatus(t, "SupplierPartEdit", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "PMC-M3X8") {
		t.Errorf("SupplierPartEdit(3002,4002): body missing %q", "PMC-M3X8")
	}
}

// TestIntegration_SupplierPartEdit_NotFound verifies the not-found guard (#817).
func TestIntegration_SupplierPartEdit_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierPartEdit(rec, withIDAndSpID(httptest.NewRequest(http.MethodGet, "/part/x/suppliers/x/edit", nil), 3002, 999999999))
	if !strings.Contains(rec.Body.String(), "Supplier link not found") {
		t.Errorf("SupplierPartEdit not found: body missing %q", "Supplier link not found")
	}
}

// TestIntegration_SupplierPartEdit_WrongPartScope verifies the AND part_id=@p2
// scoping guard rejects a valid spID under the wrong part (#817).
func TestIntegration_SupplierPartEdit_WrongPartScope(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SupplierPartEdit(rec, withIDAndSpID(httptest.NewRequest(http.MethodGet, "/part/x/suppliers/x/edit", nil), 3001, 4002))
	if !strings.Contains(rec.Body.String(), "Supplier link not found") {
		t.Errorf("SupplierPartEdit wrong part scope: body missing %q", "Supplier link not found")
	}
}

// TestIntegration_SupplierPartUpdate verifies SupplierPartUpdate persists
// every changed field (#817).
func TestIntegration_SupplierPartUpdate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	supplierID, sc := seedSupplier(t, h, ctx)
	defer sc()
	spID, spc := seedSupplierPart(t, h, ctx, partID, supplierID)
	defer spc()

	newSPN := smokeUniq("SMOKE-SPN-UPDATED")
	rec := httptest.NewRecorder()
	h.SupplierPartUpdate(rec, withIDAndSpID(postForm(fmt.Sprintf("/part/%d/suppliers/%d", partID, spID), url.Values{
		"supplier_id":   {strconv.Itoa(supplierID)},
		"preference":    {"2"},
		"supplier_pn":   {newSPN},
		"lead_time":     {"10"},
		"min_increment": {"25"},
	}), partID, spID))
	assert302(t, "SupplierPartUpdate", rec)

	var supplierPN, leadTime string
	var minIncrement float64
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT supplier_pn, lead_time, min_increment FROM %s WHERE id=@p1`, h.cfg().SupplierPartTable()), spID,
	).Scan(&supplierPN, &leadTime, &minIncrement); err != nil {
		t.Fatalf("select updated supplier_part: %v", err)
	}
	if supplierPN != newSPN || leadTime != "10" {
		t.Errorf("supplier_part %d = supplier_pn=%q lead_time=%q, want supplier_pn=%q lead_time=10", spID, supplierPN, leadTime, newSPN)
	}
	assertFloatEqual(t, "min_increment", minIncrement, 25)
}

// TestIntegration_SupplierPartUpdate_MissingSupplier verifies the
// required-supplier guard re-renders without writing (#817).
func TestIntegration_SupplierPartUpdate_MissingSupplier(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	supplierID, sc := seedSupplier(t, h, ctx)
	defer sc()
	spID, spc := seedSupplierPart(t, h, ctx, partID, supplierID)
	defer spc()

	var before string
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT supplier_pn FROM %s WHERE id=@p1`, h.cfg().SupplierPartTable()), spID,
	).Scan(&before); err != nil {
		t.Fatalf("select seeded supplier_part: %v", err)
	}

	rec := httptest.NewRecorder()
	h.SupplierPartUpdate(rec, withIDAndSpID(postForm(fmt.Sprintf("/part/%d/suppliers/%d", partID, spID), url.Values{
		"supplier_id": {""},
		"supplier_pn": {smokeUniq("SMOKE-SPN-SHOULDNOTAPPLY")},
	}), partID, spID))
	assertStatus(t, "SupplierPartUpdate missing supplier", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Supplier is required") {
		t.Errorf("SupplierPartUpdate missing supplier: body missing %q", "Supplier is required")
	}

	var after string
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT supplier_pn FROM %s WHERE id=@p1`, h.cfg().SupplierPartTable()), spID,
	).Scan(&after); err != nil {
		t.Fatalf("select supplier_part after rejected update: %v", err)
	}
	if after != before {
		t.Errorf("supplier_pn changed on rejected update: %q -> %q, want unchanged", before, after)
	}
}

// TestIntegration_SupplierPartDelete verifies the hard DELETE only removes the
// targeted row, leaving a sibling supplier_part on the same part intact (#817).
func TestIntegration_SupplierPartDelete(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	supplierID1, sc1 := seedSupplier(t, h, ctx)
	defer sc1()
	supplierID2, sc2 := seedSupplier(t, h, ctx)
	defer sc2()
	spID1, spc1 := seedSupplierPart(t, h, ctx, partID, supplierID1)
	defer spc1()
	spID2, spc2 := seedSupplierPart(t, h, ctx, partID, supplierID2)
	defer spc2()

	rec := httptest.NewRecorder()
	h.SupplierPartDelete(rec, withIDAndSpID(postForm(fmt.Sprintf("/part/%d/suppliers/%d/delete", partID, spID1), nil), partID, spID1))
	assert302(t, "SupplierPartDelete", rec)

	var remaining int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE part_id=@p1`, h.cfg().SupplierPartTable()), partID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining supplier_part rows: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining supplier_part rows for part %d = %d, want 1", partID, remaining)
	}
	var remainingID int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE part_id=@p1`, h.cfg().SupplierPartTable()), partID,
	).Scan(&remainingID); err != nil {
		t.Fatalf("select remaining supplier_part row: %v", err)
	}
	if remainingID != spID2 {
		t.Errorf("remaining supplier_part id = %d, want sibling id %d (%d should have been deleted)", remainingID, spID2, spID1)
	}
}
