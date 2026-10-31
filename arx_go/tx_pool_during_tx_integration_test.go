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
	"time"
)

// Group A-E regression tests for #432: each handler below opens a transaction and then
// used to read through the connection pool while that transaction was still held, which
// needs a *second* pool connection. With only one connection in the pool, a handler that
// still does this blocks on database/sql's connection wait until the context deadline
// fires. Capping the pool at 1 connection and giving each call a short-but-generous
// deadline turns that stall into a visible failure on unfixed code, and a fast, ordinary
// success on fixed code — directly exercising the acceptance criterion ("no pool query
// between beginTx and commit/rollback") rather than relying on code review alone.
//
// oneConnDeadline is generous for CI/local-container RTT but far short of a connection-wait
// deadlock, which would otherwise hang until this value anyway.
const oneConnDeadline = 5 * time.Second

// assertFast fails t if elapsed is not comfortably under oneConnDeadline, which would
// indicate the call actually waited on a second connection rather than completing normally.
func assertFast(t *testing.T, name string, elapsed time.Duration) {
	t.Helper()
	if elapsed >= 1*time.Second {
		t.Errorf("%s: took %s (>= 1s) with a 1-connection pool — looks like it waited on a second connection", name, elapsed)
	}
}

func TestIntegration_PoolDuringTx_PartBOMSave(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedBOM(t, h)
	defer cleanup()
	h.database().SetMaxOpenConns(1)

	vals := url.Values{}
	vals.Set("new_pl[a][PLItem]", "99")
	vals.Set("new_pl[a][PLQty]", "1")
	vals.Set("new_pl[a][PLPartNumber]", f.PN[f.L2]) // forces PartBOMSave's GetPartByNumber path

	ctx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.PartBOMSave(rec, withID(postForm("/part/x/bom", vals).WithContext(ctx), f.P))
	elapsed := time.Since(start)
	assert302(t, "PartBOMSave", rec)
	assertFast(t, "PartBOMSave", elapsed)

	lines := bomLines(t, h, f.P)
	want := fmt.Sprintf("%d:99:1", f.L2)
	found := false
	for _, l := range lines {
		if l == want {
			found = true
		}
	}
	if !found {
		t.Errorf("PartBOMSave: new line %q not found in %v (part-by-number resolution didn't run)", want, lines)
	}
}

func TestIntegration_PoolDuringTx_POCreate(t *testing.T) {
	h, f := lifecycleSetup(t)
	h.database().SetMaxOpenConns(1)

	vals := url.Values{
		"supplier_id":                 {strconv.Itoa(f.Co)},
		"new_pol[0][POLItem]":         {"1"},
		"new_pol[0][POLPNPartNumber]": {f.PN1},
		"new_pol[0][POLDesc]":         {"desc"},
		"new_pol[0][POLQty]":          {"1"},
		"new_pol[0][POLCost]":         {"1"},
		"new_pol[0][POLPNID]":         {strconv.Itoa(f.P1)}, // blank POLRev forces resolvePolRev's GetPartRevision path
	}
	saved := h.cfg().POFolderRoot
	h.cfg().POFolderRoot = ""
	defer func() { h.cfg().POFolderRoot = saved }()

	ctx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.POCreate(rec, postForm("/pos", vals).WithContext(ctx))
	elapsed := time.Since(start)
	assert302(t, "POCreate", rec)
	assertFast(t, "POCreate", elapsed)

	num, id := createdPONumberFromLocation(t, h, rec)
	t.Cleanup(func() { cleanupPO(context.Background(), h, id) })
	var rev string
	if err := h.queryRowContext(context.Background(), `SELECT revision_snapshot FROM po_line WHERE po_id=$1`, id).Scan(&rev); err != nil {
		t.Fatalf("select po_line revision for %s: %v", num, err)
	}
	if rev != "C" { // f.P1's seeded revision, per poFixture's comment
		t.Errorf("po_line.revision = %q, want %q (resolvePolRev fallback didn't run)", rev, "C")
	}
}

func TestIntegration_PoolDuringTx_POUpdate(t *testing.T) {
	h, f := lifecycleSetup(t)

	saved := h.cfg().POFolderRoot
	h.cfg().POFolderRoot = ""
	defer func() { h.cfg().POFolderRoot = saved }()
	num, id := createPO(t, h, "", url.Values{
		"supplier_id":                 {strconv.Itoa(f.Co)},
		"new_pol[0][POLItem]":         {"1"},
		"new_pol[0][POLPNPartNumber]": {f.PN1},
		"new_pol[0][POLDesc]":         {"desc"},
		"new_pol[0][POLQty]":          {"1"},
		"new_pol[0][POLCost]":         {"1"},
	})
	_ = num

	h.database().SetMaxOpenConns(1)
	vals := url.Values{
		"supplier_id":                 {strconv.Itoa(f.Co)},
		"new_pol[0][POLItem]":         {"2"},
		"new_pol[0][POLPNPartNumber]": {f.PN2},
		"new_pol[0][POLDesc]":         {"desc 2"},
		"new_pol[0][POLQty]":          {"1"},
		"new_pol[0][POLCost]":         {"1"},
		"new_pol[0][POLPNID]":         {strconv.Itoa(f.P2)}, // blank POLRev forces resolvePolRev's GetPartRevision path
	}
	ctx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.POUpdate(rec, withIDStr(postForm("/po/x", vals).WithContext(ctx), num))
	elapsed := time.Since(start)
	assert302(t, "POUpdate", rec)
	assertFast(t, "POUpdate", elapsed)

	var rev string
	if err := h.queryRowContext(context.Background(),
		`SELECT revision_snapshot FROM po_line WHERE po_id=$1 AND line_number=2`, id,
	).Scan(&rev); err != nil {
		t.Fatalf("select po_line revision: %v", err)
	}
	if rev != "" { // f.P2 has a NULL revision, per poFixture's comment
		t.Errorf("po_line.revision = %q, want \"\" (f.P2's NULL revision via resolvePolRev fallback)", rev)
	}
}

func TestIntegration_PoolDuringTx_SaveResults_BuildPanel(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const testedPart = 3013
	const comp1, comp1Lot = 3012, 8302
	const comp2, comp2Lot = 3007, 8301
	serial := smokeUniq("POOLTX")

	var recordID, buildID, unitID, outputLotID int
	if err := h.queryRowContext(ctx,
		`INSERT INTO form_record (form_id, part_id, serial_number, subject_part_number, subject_pn_description, record_type, test_order, is_locked, is_active) VALUES (6001, $1, $2, 'ASM-1003', 'Widget Deluxe Assembly', '', '', FALSE, TRUE) RETURNING id`,
		testedPart, serial).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	defer func() {
		smokeExec(ctx, h, `DELETE FROM form_record WHERE id = $1`, recordID)
		if unitID != 0 {
			smokeExec(ctx, h, `DELETE FROM unit WHERE id = $1`, unitID)
		}
		if buildID != 0 {
			if outputLotID == 0 {
				_ = h.queryRowContext(ctx, `SELECT COALESCE(output_lot_id,0) FROM build WHERE id=$1`, buildID).Scan(&outputLotID)
			}
			if outputLotID != 0 {
				smokeExec(ctx, h, `DELETE FROM genealogy WHERE child_lot_id = $1`, outputLotID)
			}
			smokeExec(ctx, h, `DELETE FROM inventory_transaction WHERE note = $1`, fmt.Sprintf("Build #%d", buildID))
			smokeExec(ctx, h, `DELETE FROM build WHERE id = $1`, buildID)
			if outputLotID != 0 {
				smokeExec(ctx, h, `DELETE FROM lot WHERE id = $1`, outputLotID)
			}
		}
		for _, id := range []int{comp1, comp2, testedPart} {
			smokeExec(ctx, h,
				`UPDATE part SET stock_on_hand = (SELECT COALESCE(SUM(qty),0) FROM inventory_transaction WHERE part_id=$1) WHERE id=$1`, id)
		}
	}()

	h.database().SetMaxOpenConns(1)
	reqCtx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	req := withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{
		"build_panel":                 {"1"},
		fmt.Sprintf("lot[%d]", comp1): {strconv.Itoa(comp1Lot)},
		fmt.Sprintf("lot[%d]", comp2): {strconv.Itoa(comp2Lot)},
	}).WithContext(reqCtx), recordID)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.SaveResults(rec, req)
	elapsed := time.Since(start)
	assertStatus(t, "SaveResults(build_panel)", rec, http.StatusSeeOther)
	assertFast(t, "SaveResults(build_panel)", elapsed)

	if err := h.queryRowContext(ctx, `SELECT MAX(id) FROM build WHERE part_id=$1`, testedPart).Scan(&buildID); err != nil {
		t.Fatalf("find build (ArxDev may need reseed): %v", err)
	}
	_ = h.queryRowContext(ctx, `SELECT unit_id FROM form_record WHERE id=$1`, recordID).Scan(&unitID)
	if buildID == 0 {
		t.Fatalf("no build created for %d (loadBuildLines' tx-backed path didn't run)", testedPart)
	}
}

func TestIntegration_PoolDuringTx_PartCreateRFQsConfirm(t *testing.T) {
	h, f := lifecycleSetup(t)
	ctx := context.Background()

	root := smokeUniq("ITEST-POOLTX-RFQ")
	var rootID int
	if err := h.queryRowContext(ctx, `INSERT INTO part (part_number, category, description, revision) VALUES ($1,'ASM','root','A') RETURNING id`, root).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		smokeExec(ctx, h, `DELETE FROM bom WHERE parent_part_id=$1`, rootID)
		smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, rootID)
		rows, err := h.queryContext(ctx, `SELECT id FROM purchase_order WHERE internal_notes=$1`, "Created from BOM of "+root)
		if err != nil {
			return
		}
		var ids []int
		for rows.Next() {
			var id int
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			cleanupPO(ctx, h, id)
		}
	})
	if _, err := h.execContext(ctx, `UPDATE part SET default_supplier_id=$2 WHERE id=$1`, f.P1, f.Co); err != nil {
		t.Fatal(err)
	}
	if _, err := h.execContext(ctx, `INSERT INTO bom (parent_part_id, component_part_id, qty) VALUES ($1,$2,1)`, rootID, f.P1); err != nil {
		t.Fatal(err)
	}

	h.database().SetMaxOpenConns(1)
	reqCtx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	req := withID(postForm("/part/x/create-rfqs", url.Values{
		"n": {"1"}, "pid": {strconv.Itoa(f.P1)}, "qty": {"1"},
	}).WithContext(reqCtx), rootID)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.PartCreateRFQsConfirm(rec, req)
	elapsed := time.Since(start)
	assertStatus(t, "PartCreateRFQsConfirm", rec, http.StatusOK)
	assertFast(t, "PartCreateRFQsConfirm", elapsed)
	if body := rec.Body.String(); !strings.Contains(body, f.CoName) {
		t.Errorf("PartCreateRFQsConfirm: body missing created RFQ's supplier name %q", f.CoName)
	}
}

func TestIntegration_PoolDuringTx_SupplierPartCreate_Failure(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	supplierID, sc := seedSupplier(t, h, ctx)
	defer sc()

	h.database().SetMaxOpenConns(1)
	reqCtx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.SupplierPartCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/suppliers", partID), url.Values{
		"supplier_id": {strconv.Itoa(supplierID)},
		"supplier_pn": {smokeUniq("SMOKE-SPN-POOLTX")},
		"unit_id":     {"999999999"}, // FK violation inside CreateSupplierPart, after beginTx
	}).WithContext(reqCtx), partID))
	elapsed := time.Since(start)
	assertStatus(t, "SupplierPartCreate", rec, http.StatusOK) // re-renders the Sourcing page with the error
	assertFast(t, "SupplierPartCreate failure", elapsed)
	if body := rec.Body.String(); !strings.Contains(body, "Error adding supplier link") {
		t.Errorf("SupplierPartCreate failure: body missing error message, got: %s", body)
	}
}

func TestIntegration_PoolDuringTx_SupplierPartUpdate_Failure(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	supplierID, sc := seedSupplier(t, h, ctx)
	defer sc()
	spID, spc := seedSupplierPart(t, h, ctx, partID, supplierID)
	defer spc()

	h.database().SetMaxOpenConns(1)
	reqCtx, cancel := context.WithTimeout(context.Background(), oneConnDeadline)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.SupplierPartUpdate(rec, withIDAndSpID(postForm(fmt.Sprintf("/part/%d/suppliers/%d", partID, spID), url.Values{
		"supplier_id": {strconv.Itoa(supplierID)},
		"supplier_pn": {smokeUniq("SMOKE-SPN-POOLTX-UPD")},
		"unit_id":     {"999999999"}, // FK violation inside UpdateSupplierPart, after beginTx
	}).WithContext(reqCtx), partID, spID))
	elapsed := time.Since(start)
	assertStatus(t, "SupplierPartUpdate", rec, http.StatusOK) // re-renders the Sourcing page with the error
	assertFast(t, "SupplierPartUpdate failure", elapsed)
	if body := rec.Body.String(); !strings.Contains(body, "Error updating supplier link") {
		t.Errorf("SupplierPartUpdate failure: body missing error message, got: %s", body)
	}
}

// createdPONumberFromLocation extracts a POCreate redirect's PO number and id, same
// convention as createPO in po_writes_integration_test.go.
func createdPONumberFromLocation(t *testing.T, h *Handler, rec *httptest.ResponseRecorder) (number string, id int) {
	t.Helper()
	loc := rec.Header().Get("Location")
	number = strings.TrimSuffix(strings.TrimPrefix(loc, "/po/"), "?suggest_links=1")
	po, ok := h.fetchPO(httptest.NewRecorder(), getReq("/po/x"), number)
	if !ok {
		t.Fatalf("created PO %s not found", number)
	}
	return number, po.ID
}
