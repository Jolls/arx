//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

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
		`SELECT MAX(id) FROM %s WHERE part_id=$1`, h.cfg().SupplierPartTable()), partID,
	).Scan(&spID); err != nil {
		t.Fatalf("seedSupplierPart: capture new id: %v", err)
	}
	return spID, func() {
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=$1", h.cfg().SupplierPartTable()), spID)
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

// TestIntegration_SupplierPartEdit_WrongPartScope verifies the AND part_id=$2
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
		`SELECT supplier_pn, lead_time, min_increment FROM %s WHERE id=$1`, h.cfg().SupplierPartTable()), spID,
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
		`SELECT supplier_pn FROM %s WHERE id=$1`, h.cfg().SupplierPartTable()), spID,
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
		`SELECT supplier_pn FROM %s WHERE id=$1`, h.cfg().SupplierPartTable()), spID,
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
		`SELECT COUNT(*) FROM %s WHERE part_id=$1`, h.cfg().SupplierPartTable()), partID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining supplier_part rows: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining supplier_part rows for part %d = %d, want 1", partID, remaining)
	}
	var remainingID int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE part_id=$1`, h.cfg().SupplierPartTable()), partID,
	).Scan(&remainingID); err != nil {
		t.Fatalf("select remaining supplier_part row: %v", err)
	}
	if remainingID != spID2 {
		t.Errorf("remaining supplier_part id = %d, want sibling id %d (%d should have been deleted)", remainingID, spID2, spID1)
	}
}

// createSupplierPart posts form to SupplierPartCreate for partID.
func createSupplierPart(h *Handler, partID int, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.SupplierPartCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/suppliers", partID), form), partID))
	return rec
}

// deleteSourcingRows removes the rows SupplierPartCreate (DigiKey import
// included, attachments aside) writes for partID. Defer it after the part and
// company cleanups so it runs first (FK order).
func deleteSourcingRows(ctx context.Context, h *Handler, partID int) {
	smokeExec(ctx, h, fmt.Sprintf("UPDATE %s SET default_supplier_id=NULL WHERE id=$1", h.cfg().PartsTable()), partID)
	smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE part_id=$1", h.cfg().PriceTable()), partID)
	deleteMfgParts(ctx, h, partID)
	smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE part_id=$1", h.cfg().SupplierPartTable()), partID)
}

// countByPart counts table's rows for partID.
func countByPart(t *testing.T, h *Handler, ctx context.Context, table string, partID int) int {
	t.Helper()
	var n int
	if err := h.queryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE part_id=$1", table), partID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

type importedPrice struct {
	packSize, priceEA, pricePack float64
	effectiveDate                string
}

// activeImportedPrices returns partID's active prices from supplierID by pack size.
func activeImportedPrices(t *testing.T, h *Handler, ctx context.Context, partID, supplierID int) []importedPrice {
	t.Helper()
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pack_size, price_ea, price_pack, effective_date::text FROM %s
		WHERE part_id=$1 AND supplier_id=$2 AND is_active ORDER BY pack_size`, h.cfg().PriceTable()), partID, supplierID)
	if err != nil {
		t.Fatalf("query prices: %v", err)
	}
	defer rows.Close()
	var out []importedPrice
	for rows.Next() {
		var p importedPrice
		if err := rows.Scan(&p.packSize, &p.priceEA, &p.pricePack, &p.effectiveDate); err != nil {
			t.Fatalf("scan price: %v", err)
		}
		out = append(out, p)
	}
	return out
}

const dkTwoBreaks = `[{"break_quantity":1,"unit_price":0.5,"total_price":0.5},{"break_quantity":10,"unit_price":0.4,"total_price":4}]`

// TestIntegration_SupplierPartCreate_Fields pins how create/update store the
// link's fields: trimmed text, blank optionals as NULL, and the effective
// purchase unit (explicit, else the part's own).
func TestIntegration_SupplierPartCreate_Fields(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	supplierID, sc := seedSupplier(t, h, ctx)
	defer sc()
	defer deleteSourcingRows(ctx, h, partID)

	spn := smokeUniq("SMOKE-SPN")
	assert302(t, "SupplierPartCreate", createSupplierPart(h, partID, url.Values{
		"supplier_id":   {strconv.Itoa(supplierID)},
		"preference":    {"3"},
		"supplier_pn":   {"  " + spn + "  "},
		"supplier_desc": {" desc "},
		"lead_time":     {" 3 wk "},
		"min_increment": {"2.5"},
		"unit_id":       {"2"}, // seed uom 2 = PC
	}))
	getReq := httptest.NewRequest(http.MethodGet, "/", nil)
	links, err := h.fetchSupplierLinks(getReq, strconv.Itoa(partID))
	if err != nil {
		t.Fatalf("fetchSupplierLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1", len(links))
	}
	lk := links[0]
	if lk.SupplierID != supplierID || lk.PartID != partID || lk.SupplierPN != spn ||
		lk.SupplierDesc != "desc" || lk.LeadTime != "3 wk" || lk.SupplierName == "" {
		t.Errorf("created link = %+v", lk)
	}
	if lk.Preference == nil || *lk.Preference != 3 {
		t.Errorf("Preference = %v, want 3", lk.Preference)
	}
	if lk.MinIncrement == nil {
		t.Errorf("MinIncrement = nil, want 2.5")
	} else {
		assertFloatEqual(t, "MinIncrement", *lk.MinIncrement, 2.5)
	}
	if lk.UnitID == nil || *lk.UnitID != 2 || lk.PurchaseUnitAbbr != "PC" || !lk.PurchaseUnitIsExplicit {
		t.Errorf("unit = %v %q explicit=%v, want 2 PC true", lk.UnitID, lk.PurchaseUnitAbbr, lk.PurchaseUnitIsExplicit)
	}

	rec := httptest.NewRecorder()
	h.SupplierPartUpdate(rec, withIDAndSpID(postForm(fmt.Sprintf("/part/%d/suppliers/%d", partID, lk.ID), url.Values{
		"supplier_id": {strconv.Itoa(supplierID)},
		"supplier_pn": {spn},
	}), partID, lk.ID))
	assert302(t, "SupplierPartUpdate", rec)
	var baseUnit string
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COALESCE(u.abbreviation, '') FROM %s p LEFT JOIN %s u ON p.uom_id = u.uom_id WHERE p.id=$1`,
		h.cfg().PartsTable(), h.cfg().UomTable()), partID).Scan(&baseUnit); err != nil {
		t.Fatalf("select part unit: %v", err)
	}
	if links, err = h.fetchSupplierLinks(getReq, strconv.Itoa(partID)); err != nil || len(links) != 1 {
		t.Fatalf("fetchSupplierLinks after update: %d links, %v", len(links), err)
	}
	lk = links[0]
	if lk.Preference != nil || lk.MinIncrement != nil || lk.UnitID != nil || lk.SupplierDesc != "" || lk.LeadTime != "" ||
		lk.PurchaseUnitIsExplicit || lk.PurchaseUnitAbbr != baseUnit {
		t.Errorf("cleared link = %+v, want NULL optionals and unit %q inherited", lk, baseUnit)
	}
}

// TestIntegration_SupplierPartCreate_DigiKeyPrices pins the imported price
// rows (active, dated today) and the default-supplier side effect.
func TestIntegration_SupplierPartCreate_DigiKeyPrices(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const supplierID = 1001 // "Acme Fasteners", seeded
	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	defer deleteSourcingRows(ctx, h, partID)

	assert302(t, "SupplierPartCreate", createSupplierPart(h, partID, url.Values{
		"supplier_id":      {strconv.Itoa(supplierID)},
		"dk_import_prices": {"1"},
		"dk_prices_json":   {dkTwoBreaks},
	}))
	got := activeImportedPrices(t, h, ctx, partID, supplierID)
	want := []importedPrice{{1, 0.5, 0.5, time.Now().Format("2006-01-02")}, {10, 0.4, 4, time.Now().Format("2006-01-02")}}
	if len(got) != len(want) {
		t.Fatalf("prices = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("price[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	var defaultSupplier int
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(default_supplier_id, 0) FROM %s WHERE id=$1`, h.cfg().PartsTable()), partID).Scan(&defaultSupplier); err != nil {
		t.Fatalf("select default supplier: %v", err)
	}
	if defaultSupplier != supplierID {
		t.Errorf("default_supplier_id = %d, want %d", defaultSupplier, supplierID)
	}
}

// TestIntegration_SupplierPartCreate_DigiKeyNewManufacturer pins "create a
// manufacturer": a manufacturer-only company plus an active mfg_part with no
// description.
func TestIntegration_SupplierPartCreate_DigiKeyNewManufacturer(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgName := smokeUniq("SMOKE-MFG")
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE name=$1", h.cfg().CompanyTable()), mfgName)
	defer deleteSourcingRows(ctx, h, partID)

	mpn := smokeUniq("SMOKE-MPN")
	assert302(t, "SupplierPartCreate", createSupplierPart(h, partID, url.Values{
		"supplier_id":        {"1001"},
		"dk_mfg_choice":      {"create"},
		"dk_mfg_name":        {" " + mfgName + " "},
		"dk_mfg_part_number": {mpn},
	}))
	var mfgID int
	var isSupplier, isMfg bool
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT id, is_supplier, is_manufacturer FROM %s WHERE name=$1`, h.cfg().CompanyTable()), mfgName).
		Scan(&mfgID, &isSupplier, &isMfg); err != nil {
		t.Fatalf("select new manufacturer: %v", err)
	}
	if isSupplier || !isMfg {
		t.Errorf("new company is_supplier=%v is_manufacturer=%v, want false true", isSupplier, isMfg)
	}
	var gotMPN string
	var desc sql.NullString
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT mfg_part_number, description FROM %s WHERE part_id=$1 AND mfg_id=$2 AND is_active`, h.cfg().MfgPartTable()), partID, mfgID).
		Scan(&gotMPN, &desc); err != nil {
		t.Fatalf("select mfg_part: %v", err)
	}
	if gotMPN != mpn || desc.Valid {
		t.Errorf("mfg_part = %q desc=%v, want %q NULL", gotMPN, desc, mpn)
	}
}

// TestIntegration_SupplierPartCreate_DigiKeyExistingManufacturer pins linking
// the MPN to a manufacturer picked from the list.
func TestIntegration_SupplierPartCreate_DigiKeyExistingManufacturer(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	defer deleteSourcingRows(ctx, h, partID)

	mpn := smokeUniq("SMOKE-MPN")
	assert302(t, "SupplierPartCreate", createSupplierPart(h, partID, url.Values{
		"supplier_id":        {"1001"},
		"dk_mfg_choice":      {strconv.Itoa(mfgID)},
		"dk_mfg_part_number": {mpn},
	}))
	var n int
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE part_id=$1 AND mfg_id=$2 AND mfg_part_number=$3 AND is_active`, h.cfg().MfgPartTable()), partID, mfgID, mpn).
		Scan(&n); err != nil {
		t.Fatalf("count mfg_part: %v", err)
	}
	if n != 1 {
		t.Errorf("mfg_part rows = %d, want 1", n)
	}
}

// TestIntegration_SupplierPartDelete_WrongPartScope verifies a link can't be
// deleted through another part's URL.
func TestIntegration_SupplierPartDelete_WrongPartScope(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partA, pca := seedPart(t, h, ctx, "BUY")
	defer pca()
	partB, pcb := seedPart(t, h, ctx, "BUY")
	defer pcb()
	spID, spc := seedSupplierPart(t, h, ctx, partA, 1001)
	defer spc()

	rec := httptest.NewRecorder()
	h.SupplierPartDelete(rec, withIDAndSpID(postForm(fmt.Sprintf("/part/%d/suppliers/%d/delete", partB, spID), nil), partB, spID))
	assert302(t, "SupplierPartDelete", rec)
	if n := countByPart(t, h, ctx, h.cfg().SupplierPartTable(), partA); n != 1 {
		t.Errorf("part %d supplier links = %d, want 1 (delete via part %d must be a no-op)", partA, n, partB)
	}
}

// TestIntegration_SupplierPartCreate_DigiKeyPriceExists: an imported break at a
// pack size that already has an active price is skipped; the rest import.
func TestIntegration_SupplierPartCreate_DigiKeyPriceExists(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const supplierID = 1001
	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	defer deleteSourcingRows(ctx, h, partID)
	if _, err := h.execContext(ctx, fmt.Sprintf(`INSERT INTO %s (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
		VALUES ($1, $2, 1, 0.9, 0.9, '2020-01-01', TRUE)`, h.cfg().PriceTable()), partID, supplierID); err != nil {
		t.Fatalf("seed price: %v", err)
	}

	rec := createSupplierPart(h, partID, url.Values{
		"supplier_id":      {strconv.Itoa(supplierID)},
		"dk_import_prices": {"1"},
		"dk_prices_json":   {dkTwoBreaks},
	})
	assert302(t, "SupplierPartCreate", rec)
	got := activeImportedPrices(t, h, ctx, partID, supplierID)
	want := []importedPrice{{1, 0.9, 0.9, "2020-01-01"}, {10, 0.4, 4, time.Now().Format("2006-01-02")}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("prices = %+v, want %+v", got, want)
	}
	if n := countByPart(t, h, ctx, h.cfg().SupplierPartTable(), partID); n != 1 {
		t.Errorf("supplier links = %d, want 1", n)
	}
}

// TestIntegration_SupplierPartCreate_DigiKeyMfgPartExists: importing an MPN the
// part already has leaves the existing row and still saves the link.
func TestIntegration_SupplierPartCreate_DigiKeyMfgPartExists(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	defer deleteSourcingRows(ctx, h, partID)
	mid, _ := seedMfgPart(t, h, ctx, partID, mfgID)
	var mpn string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT mfg_part_number FROM %s WHERE id=$1`, h.cfg().MfgPartTable()), mid).Scan(&mpn); err != nil {
		t.Fatalf("select seeded mpn: %v", err)
	}

	rec := createSupplierPart(h, partID, url.Values{
		"supplier_id":        {"1001"},
		"dk_mfg_choice":      {strconv.Itoa(mfgID)},
		"dk_mfg_part_number": {mpn},
	})
	assert302(t, "SupplierPartCreate", rec)
	if n := countByPart(t, h, ctx, h.cfg().MfgPartTable(), partID); n != 1 {
		t.Errorf("mfg_part rows = %d, want 1", n)
	}
	if n := countByPart(t, h, ctx, h.cfg().SupplierPartTable(), partID); n != 1 {
		t.Errorf("supplier links = %d, want 1", n)
	}
}

// TestIntegration_SupplierPartCreate_DigiKeyManufacturerNameTaken: creating a
// manufacturer whose name a company already has shows the friendly message
// and saves nothing.
func TestIntegration_SupplierPartCreate_DigiKeyManufacturerNameTaken(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	companyID, cc := seedSupplier(t, h, ctx)
	defer cc()
	defer deleteSourcingRows(ctx, h, partID)
	var name string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT name FROM %s WHERE id=$1`, h.cfg().CompanyTable()), companyID).Scan(&name); err != nil {
		t.Fatalf("select company name: %v", err)
	}

	rec := createSupplierPart(h, partID, url.Values{
		"supplier_id":        {"1001"},
		"dk_mfg_choice":      {"create"},
		"dk_mfg_name":        {name},
		"dk_mfg_part_number": {smokeUniq("SMOKE-MPN")},
	})
	assertStatus(t, "SupplierPartCreate", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "pick it from the manufacturer list instead") {
		t.Errorf("body missing the name-taken message")
	}
	if n := countByPart(t, h, ctx, h.cfg().SupplierPartTable(), partID); n != 0 {
		t.Errorf("supplier links = %d, want 0", n)
	}
}
