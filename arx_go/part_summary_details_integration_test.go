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
)

// partPage runs handler for part id at path and returns the recorder.
func partPage(handler http.HandlerFunc, id int, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, withID(httptest.NewRequest(http.MethodGet, path, nil), id))
	return rec
}

func wantBody(t *testing.T, label, body string, wants ...string) {
	t.Helper()
	for _, s := range wants {
		if !strings.Contains(body, s) {
			t.Errorf("%s: body missing %q", label, s)
		}
	}
}

func wantNoBody(t *testing.T, label, body string, nots ...string) {
	t.Helper()
	for _, s := range nots {
		if strings.Contains(body, s) {
			t.Errorf("%s: body unexpectedly contains %q", label, s)
		}
	}
}

// Characterization (#378): current dashboard render, BOM redirect, tab gating.
func TestIntegration_PartDetail_Characterization_Dashboard(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetail, bare, fmt.Sprintf("/part/%d", bare))
	assertStatus(t, "PartDetail", rec, http.StatusOK)
	wantBody(t, "dashboard", rec.Body.String(), `<div class="card-header">Reference</div>`)
}

func TestIntegration_PartDetail_Characterization_BOMRedirect(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, _, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetail, full, fmt.Sprintf("/part/%d", full))
	assert302(t, "PartDetail /part/{id}", rec)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/part/%d/bom", full) {
		t.Errorf("Location = %q, want the BOM tab", loc)
	}
	rec = partPage(h.PartDetail, full, fmt.Sprintf("/part/%d/details", full))
	assertStatus(t, "PartDetail /details", rec, http.StatusOK)
}

func TestIntegration_PartTabs_Characterization_EditDuplicateGated(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	edit, dup := fmt.Sprintf(`href="/part/%d/edit"`, bare), fmt.Sprintf(`href="/part/%d/duplicate"`, bare)
	rec := partPage(h.PartDetails, bare, fmt.Sprintf("/part/%d/details", bare))
	wantBody(t, "details tab bar", rec.Body.String(), edit, dup)
	rec = partPage(h.PartAttachments, bare, fmt.Sprintf("/part/%d/attachments", bare))
	assertStatus(t, "PartAttachments", rec, http.StatusOK)
	wantNoBody(t, "attachments tab bar", rec.Body.String(), edit, dup)
}

func TestIntegration_PartTabs_Characterization_CategoryGating(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	rec := partPage(h.PartDetail, 3006, "/part/3006/details")
	assertStatus(t, "PartDetail 3006", rec, http.StatusOK)
	wantBody(t, "OPS tab bar", rec.Body.String(), `<span class="sub-tab disabled" title="Order history does not apply`)
	wantNoBody(t, "OPS tab bar", rec.Body.String(), `href="/part/3006/pricing"`)
}

// Red (#378): Summary tab, default landing, Details tab with every column.
func TestIntegration_PartSummary_Route(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetail, bare, fmt.Sprintf("/part/%d/summary", bare))
	assertStatus(t, "PartDetail /summary", rec, http.StatusOK)
	body := rec.Body.String()
	wantBody(t, "summary", body, `class="sub-tab active">Summary</a>`, fmt.Sprintf(`href="/part/%d/details"`, bare))
	wantNoBody(t, "summary", body, fmt.Sprintf(`href="/part/%d/edit"`, bare), fmt.Sprintf(`href="/part/%d/duplicate"`, bare))
}

func TestIntegration_PartSummary_NoRedirectForBOMPart(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, _, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetail, full, fmt.Sprintf("/part/%d/summary", full))
	assertStatus(t, "PartDetail /summary", rec, http.StatusOK)
	wantBody(t, "summary", rec.Body.String(), `<div class="card-header">Reference</div>`, `class="sub-tab active">Summary</a>`)
}

func TestIntegration_PartDefaultLanding_IsSummary(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetail, bare, fmt.Sprintf("/part/%d", bare))
	wantBody(t, "landing", rec.Body.String(), `class="sub-tab active">Summary</a>`)
}

func TestIntegration_PartDetails_ListsEveryColumn(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, _, primaryAtt, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetails, full, fmt.Sprintf("/part/%d/details", full))
	assertStatus(t, "PartDetails", rec, http.StatusOK)
	body := rec.Body.String()
	wantBody(t, "details", body, `class="sub-tab active">Details</a>`,
		fmt.Sprintf(`href="/part/%d/edit"`, full), fmt.Sprintf(`href="/part/%d/duplicate"`, full),
		"full desc", "full detail", "full notes", "JJ", "2026-01-02", "2026-03-04", "lot", "2.50", "1.25", "3.5", "mL",
		"u1", "u2", "u3", "u4", "u5", "u6", "u7", "u8", "u9", "u10",
		"Created Date", "Modified Date", "Last Rollup At", "Stock On Hand",
		"<strong>ID:</strong><span>"+strconv.Itoa(full)+"</span>",
		"<strong>Primary Attachment ID:</strong><span>"+strconv.Itoa(primaryAtt)+"</span>",
		"<strong>Attachment Count:</strong><span>7</span>", "<strong>PO Line Count:</strong><span>4</span>",
		"<strong>Active:</strong><span>Yes</span>", "<strong>Default Supplier:</strong>", "<strong>Price:</strong>")
	wantNoBody(t, "details", body, `class="part-status-bar"`)
}

func TestIntegration_PartDetails_BareShowsPlaceholders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	_, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	rec := partPage(h.PartDetails, bare, fmt.Sprintf("/part/%d/details", bare))
	assertStatus(t, "PartDetails", rec, http.StatusOK)
	body := rec.Body.String()
	wantBody(t, "details", body, "<strong>Reorder Min:</strong><span>—</span>", "<strong>Price:</strong><span>—</span>")
	for i := 1; i <= 10; i++ {
		wantBody(t, "details", body, fmt.Sprintf("<strong>User %d:</strong>", i))
	}
}

// seedSupplierPrice points part id at supplier 1002 and a fresh price row;
// the returned cleanup must run before the part is deleted.
func seedSupplierPrice(t *testing.T, h *Handler, id int) (cleanup func()) {
	t.Helper()
	ctx := context.Background()
	var priceID int
	if err := h.queryRowContext(ctx, `INSERT INTO price (part_id, supplier_id, price_ea, price_pack, pack_size, is_active)
		VALUES ($1, 1002, 7.5, 7.5, 1, TRUE) RETURNING id`, id).Scan(&priceID); err != nil {
		t.Fatalf("seed price: %v", err)
	}
	cleanup = func() {
		smokeExec(ctx, h, `UPDATE part SET price_id=NULL, default_supplier_id=NULL WHERE id=$1`, id)
		smokeExec(ctx, h, `DELETE FROM price WHERE id=$1`, priceID)
	}
	if _, err := h.execContext(ctx, `UPDATE part SET price_id=$2, default_supplier_id=1002 WHERE id=$1`, id, priceID); err != nil {
		cleanup()
		t.Fatalf("seed part: %v", err)
	}
	return cleanup
}

func TestIntegration_PartDetails_ResolvesSupplierAndPrice(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, _, _, cleanup := seedPartDetail(t, h)
	defer cleanup()
	defer seedSupplierPrice(t, h, full)()

	rec := partPage(h.PartDetails, full, fmt.Sprintf("/part/%d/details", full))
	assertStatus(t, "PartDetails", rec, http.StatusOK)
	wantBody(t, "details", rec.Body.String(),
		"<strong>Default Supplier:</strong><span>Precision Machining Co (ID 1002)</span>",
		"Precision Machining Co - $7.5000 (ID ")
}

func TestIntegration_FetchPartFull_SupplierPrice(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()
	defer seedSupplierPrice(t, h, full)()
	ctx := context.Background()

	p, err := h.fetchPartFull(ctx, strconv.Itoa(full))
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultSupplierID == nil || *p.DefaultSupplierID != 1002 || p.DefaultSupplierName != "Precision Machining Co" ||
		p.PriceID == nil || !p.PriceEa.Equal(d(7.5)) || p.PriceSupplierName != "Precision Machining Co" {
		t.Errorf("full: got supplier %v %q price %v %v %q", p.DefaultSupplierID, p.DefaultSupplierName, p.PriceID, p.PriceEa, p.PriceSupplierName)
	}
	b, err := h.fetchPartFull(ctx, strconv.Itoa(bare))
	if err != nil {
		t.Fatal(err)
	}
	if b.DefaultSupplierID != nil || b.DefaultSupplierName != "" || b.PriceID != nil || b.PriceSupplierName != "" {
		t.Errorf("bare: got supplier %v %q price %v %q", b.DefaultSupplierID, b.DefaultSupplierName, b.PriceID, b.PriceSupplierName)
	}
}

func TestIntegration_PartDetails_NotFound(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	rec := partPage(h.PartDetails, 0, "/part/0/details")
	wantBody(t, "details", rec.Body.String(), "Part not found")
}

func TestIntegration_PartDetails_CategoryTabsStillGated(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	rec := partPage(h.PartDetails, 3006, "/part/3006/details")
	assertStatus(t, "PartDetails 3006", rec, http.StatusOK)
	body := rec.Body.String()
	wantBody(t, "details OPS", body, `<span class="sub-tab disabled" title="Order history does not apply`, "<strong>Created Date:</strong>")
	wantNoBody(t, "details OPS", body, `href="/part/3006/pricing"`)
}

// PartsCreate and PartUpdate land on Details so the user sees the saved fields.
func TestIntegration_PartSave_RedirectsToDetails(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()

	vals := url.Values{"part_number": {smokeUniq("ITEST-RD")}, "category": {"BUY"}, "release_status": {"A"}, "tracking_mode": {"none"}}
	rec := httptest.NewRecorder()
	h.PartsCreate(rec, postForm("/parts", vals))
	assert302(t, "PartsCreate", rec)
	loc := rec.Header().Get("Location")
	id, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(loc, "/part/"), "/details"))
	if err != nil {
		t.Fatalf("PartsCreate Location %q: %v", loc, err)
	}
	defer smokeExec(ctx, h, "DELETE FROM part WHERE id=$1", id)
	if loc != fmt.Sprintf("/part/%d/details", id) {
		t.Errorf("PartsCreate Location = %q", loc)
	}
	rec = httptest.NewRecorder()
	h.PartUpdate(rec, withID(postForm(fmt.Sprintf("/part/%d", id), vals), id))
	assert302(t, "PartUpdate", rec)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/part/%d/details", id) {
		t.Errorf("PartUpdate Location = %q", loc)
	}
}
