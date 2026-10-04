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

// withIDAndMidID sets the "id" and "mid" path values (mfg-parts routes).
func withIDAndMidID(req *http.Request, id, mid int) *http.Request {
	req.SetPathValue("id", strconv.Itoa(id))
	req.SetPathValue("mid", strconv.Itoa(mid))
	return req
}

// seedMfgPart creates a mfg_part row via MfgPartCreate and returns its id plus
// a cleanup that hard-deletes it. MfgPartCreate redirects to the parent list
// URL (no child id in Location), so the new id is captured via MAX(id) for
// the part, mirroring the filID-capture pattern in TestIntegration_PartLifecycle.
func seedMfgPart(t *testing.T, h *Handler, ctx context.Context, partID, mfgID int) (int, func()) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.MfgPartCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/mfg-parts", partID), url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {smokeUniq("SMOKE-MPN")},
		"description":     {"smoke mfg part"},
	}), partID))
	if rec.Code >= 400 {
		t.Fatalf("seedMfgPart: MfgPartCreate status %d, body: %s", rec.Code, rec.Body.String())
	}
	var mid int
	if err := h.queryRowContext(ctx,
		`SELECT MAX(id) FROM mfg_part WHERE part_id=$1`, partID,
	).Scan(&mid); err != nil {
		t.Fatalf("seedMfgPart: capture new id: %v", err)
	}
	return mid, func() {
		smokeExec(ctx, h, "DELETE FROM mfg_part WHERE id=$1", mid)
	}
}

// TestIntegration_PartMfgParts verifies PartMfgParts' active-row filter
// (fetchMfgParts) and manufacturer-dropdown filter (fetchManufacturers)
// against pinned seed data (#817).
func TestIntegration_PartMfgParts(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PartMfgParts(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3002/mfg-parts", nil), 3002))
	assertStatus(t, "PartMfgParts", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"CX-4471", "Contoso Manufacturing", "Precision Machining Co"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartMfgParts(3002): body missing %q", want)
		}
	}
	if strings.Contains(body, "CX-9999-OBSOLETE") {
		t.Errorf("PartMfgParts(3002): body unexpectedly contains inactive mfg_part %q", "CX-9999-OBSOLETE")
	}
	if strings.Contains(body, "Global Distribution") {
		t.Errorf("PartMfgParts(3002): manufacturer dropdown unexpectedly contains non-manufacturer %q", "Global Distribution")
	}
}

// TestIntegration_MfgPartEdit verifies the edit form renders the seeded row (#817).
func TestIntegration_MfgPartEdit(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	mid, mpc := seedMfgPart(t, h, ctx, partID, mfgID)
	defer mpc()

	var mpn string
	if err := h.queryRowContext(ctx,
		`SELECT mfg_part_number FROM mfg_part WHERE id=$1`, mid,
	).Scan(&mpn); err != nil {
		t.Fatalf("select seeded mfg_part: %v", err)
	}

	rec := httptest.NewRecorder()
	h.MfgPartEdit(rec, withIDAndMidID(httptest.NewRequest(http.MethodGet, "/part/x/mfg-parts/x/edit", nil), partID, mid))
	assertStatus(t, "MfgPartEdit", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), mpn) {
		t.Errorf("MfgPartEdit: body missing seeded MPN %q", mpn)
	}
}

// TestIntegration_MfgPartEdit_NotFound verifies the not-found guard (#817).
func TestIntegration_MfgPartEdit_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()

	rec := httptest.NewRecorder()
	h.MfgPartEdit(rec, withIDAndMidID(httptest.NewRequest(http.MethodGet, "/part/x/mfg-parts/x/edit", nil), partID, 999999999))
	if !strings.Contains(rec.Body.String(), "Manufacturer part not found") {
		t.Errorf("MfgPartEdit not found: body missing %q", "Manufacturer part not found")
	}
}

// TestIntegration_MfgPartEdit_WrongPartScope verifies the AND part_id=$2
// scoping guard rejects a valid mid under the wrong part (#817).
func TestIntegration_MfgPartEdit_WrongPartScope(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	otherPartID, opc := seedPart(t, h, ctx, "BUY")
	defer opc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	mid, mpc := seedMfgPart(t, h, ctx, partID, mfgID)
	defer mpc()

	rec := httptest.NewRecorder()
	h.MfgPartEdit(rec, withIDAndMidID(httptest.NewRequest(http.MethodGet, "/part/x/mfg-parts/x/edit", nil), otherPartID, mid))
	if !strings.Contains(rec.Body.String(), "Manufacturer part not found") {
		t.Errorf("MfgPartEdit wrong part scope: body missing %q", "Manufacturer part not found")
	}
}

// TestIntegration_MfgPartUpdate verifies MfgPartUpdate persists every changed
// field (#817).
func TestIntegration_MfgPartUpdate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	newMfgID, nmc := seedSupplier(t, h, ctx)
	defer nmc()
	mid, mpc := seedMfgPart(t, h, ctx, partID, mfgID)
	defer mpc()

	newMPN := smokeUniq("SMOKE-MPN-UPDATED")
	rec := httptest.NewRecorder()
	h.MfgPartUpdate(rec, withIDAndMidID(postForm(fmt.Sprintf("/part/%d/mfg-parts/%d", partID, mid), url.Values{
		"mfg_id":          {strconv.Itoa(newMfgID)},
		"mfg_part_number": {newMPN},
		"description":     {"updated description"},
	}), partID, mid))
	assert302(t, "MfgPartUpdate", rec)

	var mpn, desc string
	var gotMfgID int
	if err := h.queryRowContext(ctx,
		`SELECT mfg_part_number, description, mfg_id FROM mfg_part WHERE id=$1`, mid,
	).Scan(&mpn, &desc, &gotMfgID); err != nil {
		t.Fatalf("select updated mfg_part: %v", err)
	}
	if mpn != newMPN || desc != "updated description" || gotMfgID != newMfgID {
		t.Errorf("mfg_part %d = mpn=%q desc=%q mfg_id=%d, want mpn=%q desc=%q mfg_id=%d",
			mid, mpn, desc, gotMfgID, newMPN, "updated description", newMfgID)
	}
}

// TestIntegration_MfgPartUpdate_MissingRequired verifies the required-fields
// guard re-renders without writing (#817).
func TestIntegration_MfgPartUpdate_MissingRequired(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	mid, mpc := seedMfgPart(t, h, ctx, partID, mfgID)
	defer mpc()

	var before string
	if err := h.queryRowContext(ctx,
		`SELECT mfg_part_number FROM mfg_part WHERE id=$1`, mid,
	).Scan(&before); err != nil {
		t.Fatalf("select seeded mfg_part: %v", err)
	}

	rec := httptest.NewRecorder()
	h.MfgPartUpdate(rec, withIDAndMidID(postForm(fmt.Sprintf("/part/%d/mfg-parts/%d", partID, mid), url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {""},
	}), partID, mid))
	assertStatus(t, "MfgPartUpdate missing required", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Manufacturer and MPN are required") {
		t.Errorf("MfgPartUpdate missing required: body missing %q", "Manufacturer and MPN are required")
	}

	var after string
	if err := h.queryRowContext(ctx,
		`SELECT mfg_part_number FROM mfg_part WHERE id=$1`, mid,
	).Scan(&after); err != nil {
		t.Fatalf("select mfg_part after rejected update: %v", err)
	}
	if after != before {
		t.Errorf("mfg_part_number changed on rejected update: %q -> %q, want unchanged", before, after)
	}
}

// TestIntegration_MfgPartUpdate_WrongPartScope documents current behavior: the
// UPDATE's part_id mismatch is a silent no-op (no rows-affected check), not a
// cross-part write (#817).
func TestIntegration_MfgPartUpdate_WrongPartScope(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	otherPartID, opc := seedPart(t, h, ctx, "BUY")
	defer opc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	mid, mpc := seedMfgPart(t, h, ctx, partID, mfgID)
	defer mpc()

	var before string
	if err := h.queryRowContext(ctx,
		`SELECT mfg_part_number FROM mfg_part WHERE id=$1`, mid,
	).Scan(&before); err != nil {
		t.Fatalf("select seeded mfg_part: %v", err)
	}

	rec := httptest.NewRecorder()
	h.MfgPartUpdate(rec, withIDAndMidID(postForm(fmt.Sprintf("/part/%d/mfg-parts/%d", otherPartID, mid), url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {smokeUniq("SMOKE-MPN-SHOULDNOTAPPLY")},
	}), otherPartID, mid))
	assert302(t, "MfgPartUpdate wrong part scope", rec)

	var after string
	if err := h.queryRowContext(ctx,
		`SELECT mfg_part_number FROM mfg_part WHERE id=$1 AND part_id=$2`, mid, partID,
	).Scan(&after); err != nil {
		t.Fatalf("select mfg_part under its correct part id: %v", err)
	}
	if after != before {
		t.Errorf("mfg_part_number changed despite part_id mismatch: %q -> %q, want unchanged (silent no-op)", before, after)
	}
}

// createMfgPart posts MfgPartCreate for partID. Callers defer
// deleteMfgParts(partID) before the part/supplier cleanups run (FK order).
func createMfgPart(h *Handler, partID int, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.MfgPartCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/mfg-parts", partID), form), partID))
	return rec
}

// deleteMfgParts hard-deletes every mfg_part row on partID.
func deleteMfgParts(ctx context.Context, h *Handler, partID int) {
	smokeExec(ctx, h, "DELETE FROM mfg_part WHERE part_id=$1", partID)
}

// TestIntegration_MfgPartCreate pins MfgPartCreate's stored row: trimmed MPN
// and description, the part and manufacturer ids, and is_active (#220).
func TestIntegration_MfgPartCreate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	defer deleteMfgParts(ctx, h, partID)

	mpn := smokeUniq("SMOKE-MPN")
	rec := createMfgPart(h, partID, url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {"  " + mpn + "  "},
		"description":     {"  created desc  "},
	})
	assert302(t, "MfgPartCreate", rec)

	var gotMfgID int
	var gotMPN, gotDesc string
	var active bool
	if err := h.queryRowContext(ctx,
		`SELECT mfg_id, mfg_part_number, description, is_active FROM mfg_part WHERE part_id=$1`, partID,
	).Scan(&gotMfgID, &gotMPN, &gotDesc, &active); err != nil {
		t.Fatalf("select created mfg_part: %v", err)
	}
	if gotMfgID != mfgID || gotMPN != mpn || gotDesc != "created desc" || !active {
		t.Errorf("mfg_part = mfg_id=%d mpn=%q desc=%q active=%v, want mfg_id=%d mpn=%q desc=%q active=true",
			gotMfgID, gotMPN, gotDesc, active, mfgID, mpn, "created desc")
	}
}

// TestIntegration_MfgPartCreate_EmptyDescription pins that a blank description
// is stored as an empty string rather than NULL (#220).
func TestIntegration_MfgPartCreate_EmptyDescription(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	defer deleteMfgParts(ctx, h, partID)

	rec := createMfgPart(h, partID, url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {smokeUniq("SMOKE-MPN")},
	})
	assert302(t, "MfgPartCreate empty description", rec)

	var isNull bool
	var desc string
	if err := h.queryRowContext(ctx,
		`SELECT description IS NULL, COALESCE(description, '') FROM mfg_part WHERE part_id=$1`, partID,
	).Scan(&isNull, &desc); err != nil {
		t.Fatalf("select created mfg_part: %v", err)
	}
	if isNull || desc != "" {
		t.Errorf("description null=%v value=%q, want non-NULL ''", isNull, desc)
	}
}

// TestIntegration_MfgPartCreate_MissingRequired verifies the required-fields
// guard re-renders without inserting (#220).
func TestIntegration_MfgPartCreate_MissingRequired(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	defer deleteMfgParts(ctx, h, partID)

	rec := createMfgPart(h, partID, url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {"  "},
	})
	assertStatus(t, "MfgPartCreate missing required", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Manufacturer and MPN are required") {
		t.Errorf("MfgPartCreate missing required: body missing %q", "Manufacturer and MPN are required")
	}

	var n int
	if err := h.queryRowContext(ctx,
		`SELECT COUNT(*) FROM mfg_part WHERE part_id=$1`, partID,
	).Scan(&n); err != nil {
		t.Fatalf("count mfg_part: %v", err)
	}
	if n != 0 {
		t.Errorf("mfg_part rows = %d after rejected create, want 0", n)
	}
}

// TestIntegration_MfgPartCreate_Duplicate verifies UQ_mfg_part_combo surfaces
// as a re-rendered error and leaves one active row (#220).
func TestIntegration_MfgPartCreate_Duplicate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	defer deleteMfgParts(ctx, h, partID)

	form := url.Values{
		"mfg_id":          {strconv.Itoa(mfgID)},
		"mfg_part_number": {smokeUniq("SMOKE-MPN")},
	}
	assert302(t, "MfgPartCreate first", createMfgPart(h, partID, form))
	rec := createMfgPart(h, partID, form)
	assertStatus(t, "MfgPartCreate duplicate", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Error adding manufacturer part") {
		t.Errorf("MfgPartCreate duplicate: body missing %q", "Error adding manufacturer part")
	}

	var n int
	if err := h.queryRowContext(ctx,
		`SELECT COUNT(*) FROM mfg_part WHERE part_id=$1 AND is_active`, partID,
	).Scan(&n); err != nil {
		t.Fatalf("count mfg_part: %v", err)
	}
	if n != 1 {
		t.Errorf("active mfg_part rows = %d after duplicate create, want 1", n)
	}
}

// TestIntegration_MfgPartDelete_WrongPartScope documents current behavior: a
// delete under the wrong part id is a silent no-op (#220).
func TestIntegration_MfgPartDelete_WrongPartScope(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	otherPartID, opc := seedPart(t, h, ctx, "BUY")
	defer opc()
	mfgID, mc := seedSupplier(t, h, ctx)
	defer mc()
	mid, mpc := seedMfgPart(t, h, ctx, partID, mfgID)
	defer mpc()

	rec := httptest.NewRecorder()
	h.MfgPartDelete(rec, withIDAndMidID(postForm(fmt.Sprintf("/part/%d/mfg-parts/%d/delete", otherPartID, mid), nil), otherPartID, mid))
	assert302(t, "MfgPartDelete wrong part scope", rec)

	var active bool
	if err := h.queryRowContext(ctx,
		`SELECT is_active FROM mfg_part WHERE id=$1`, mid,
	).Scan(&active); err != nil {
		t.Fatalf("select mfg_part: %v", err)
	}
	if !active {
		t.Errorf("mfg_part %d is_active = false after wrong-part delete, want true (silent no-op)", mid)
	}
}

// TestIntegration_MfgPartDelete verifies the soft-delete only affects the
// targeted row, not a sibling mfg_part on the same part (#817).
func TestIntegration_MfgPartDelete(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, pc := seedPart(t, h, ctx, "BUY")
	defer pc()
	mfgID1, mc1 := seedSupplier(t, h, ctx)
	defer mc1()
	mfgID2, mc2 := seedSupplier(t, h, ctx)
	defer mc2()
	mid1, mpc1 := seedMfgPart(t, h, ctx, partID, mfgID1)
	defer mpc1()
	mid2, mpc2 := seedMfgPart(t, h, ctx, partID, mfgID2)
	defer mpc2()

	rec := httptest.NewRecorder()
	h.MfgPartDelete(rec, withIDAndMidID(postForm(fmt.Sprintf("/part/%d/mfg-parts/%d/delete", partID, mid1), nil), partID, mid1))
	assert302(t, "MfgPartDelete", rec)

	var active1, active2 bool
	if err := h.queryRowContext(ctx,
		`SELECT is_active FROM mfg_part WHERE id=$1`, mid1,
	).Scan(&active1); err != nil {
		t.Fatalf("select deleted mfg_part: %v", err)
	}
	if err := h.queryRowContext(ctx,
		`SELECT is_active FROM mfg_part WHERE id=$1`, mid2,
	).Scan(&active2); err != nil {
		t.Fatalf("select sibling mfg_part: %v", err)
	}
	if active1 {
		t.Errorf("mfg_part %d is_active = true, want false (soft-deleted)", mid1)
	}
	if !active2 {
		t.Errorf("sibling mfg_part %d is_active = false, want true (unaffected by delete)", mid2)
	}
}
