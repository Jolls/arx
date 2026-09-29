//go:build integration

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// partAttachmentRow is the stored state of one part_attachment row.
type partAttachmentRow struct {
	FileName, Category, Rev, Comment, Hash sql.NullString
	SortOrder, SupplierPartID, MfgPartID   sql.NullInt64
	IsActive                               bool
}

func loadPartAttachment(t *testing.T, h *Handler, ctx context.Context, attID int) partAttachmentRow {
	t.Helper()
	var a partAttachmentRow
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT file_name, category, part_revision, comment, hash, sort_order, supplier_part_id, mfg_part_id, is_active
		FROM %s WHERE id=$1`, h.cfg().AttachmentsTable()), attID,
	).Scan(&a.FileName, &a.Category, &a.Rev, &a.Comment, &a.Hash, &a.SortOrder, &a.SupplierPartID, &a.MfgPartID, &a.IsActive); err != nil {
		t.Fatalf("load part_attachment %d: %v", attID, err)
	}
	return a
}

// createPartAttachment posts PartAttachmentCreate and returns the new row's id.
func createPartAttachment(t *testing.T, h *Handler, ctx context.Context, partID int, form url.Values) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.PartAttachmentCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/attachments", partID), form), partID))
	assert302(t, "PartAttachmentCreate", rec)
	var id int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT MAX(id) FROM %s WHERE part_id=$1`, h.cfg().AttachmentsTable()), partID).Scan(&id); err != nil {
		t.Fatalf("capture new attachment id: %v", err)
	}
	return id
}

func partPrimary(t *testing.T, h *Handler, ctx context.Context, partID int) int {
	t.Helper()
	var id sql.NullInt64
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT primary_attachment_id FROM %s WHERE id=$1`, h.cfg().PartsTable()), partID).Scan(&id); err != nil {
		t.Fatalf("select part primary: %v", err)
	}
	return int(id.Int64)
}

// TestIntegration_PartAttachmentVendorScope pins the vendor-scope round trip (#56):
// stored ids, the page's vendor name, fetchAttachmentsByVendor's keying, the
// rejections, a metadata-only update, and PartSetPrimaryAttachment (#220).
func TestIntegration_PartAttachmentVendorScope(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, _, cleanupPart := seedThrowawayPart(t, h, ctx, "220att")
	defer cleanupPart()
	supplierID, cleanupSupplier := seedSupplier(t, h, ctx)
	defer cleanupSupplier()
	defer deleteSourcingRows(ctx, h, partID)
	spID, _ := seedSupplierPart(t, h, ctx, partID, supplierID)
	mpID, _ := seedMfgPart(t, h, ctx, partID, supplierID)
	defer deletePartAttachments(ctx, h, partID)

	var supplierName string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT name FROM %s WHERE id=$1`, "company"), supplierID).Scan(&supplierName); err != nil {
		t.Fatalf("select supplier name: %v", err)
	}

	form := func(link, scope, order string) url.Values {
		return url.Values{"FILFileName": {link}, "FILPNRev": {"A"}, "category": {"itest-220"},
			"comment": {"c1"}, "order_id": {order}, "vendor_scope": {scope}}
	}
	sAtt := createPartAttachment(t, h, ctx, partID, form("http://example.test/220-s-"+smokeUniq("x"), fmt.Sprintf("s:%d", spID), "3"))
	mAtt := createPartAttachment(t, h, ctx, partID, form("http://example.test/220-m-"+smokeUniq("x"), fmt.Sprintf("m:%d", mpID), ""))
	plainLink := "http://example.test/220-p-" + smokeUniq("x")
	pAtt := createPartAttachment(t, h, ctx, partID, form(plainLink, "", ""))

	s := loadPartAttachment(t, h, ctx, sAtt)
	if int(s.SupplierPartID.Int64) != spID || s.MfgPartID.Valid || s.SortOrder.Int64 != 3 || s.Comment.String != "c1" || s.Rev.String != "A" {
		t.Errorf("supplier-scoped row = %+v, want supplier_part_id %d, no mfg, sort 3, comment c1, rev A", s, spID)
	}
	m := loadPartAttachment(t, h, ctx, mAtt)
	if int(m.MfgPartID.Int64) != mpID || m.SupplierPartID.Valid || m.SortOrder.Valid {
		t.Errorf("mfg-scoped row = %+v, want mfg_part_id %d, no supplier, NULL sort", m, mpID)
	}
	p := loadPartAttachment(t, h, ctx, pAtt)
	if p.SupplierPartID.Valid || p.MfgPartID.Valid || len(p.Hash.String) != 64 {
		t.Errorf("part-level row = %+v, want no scope and a 64-char hash", p)
	}

	rec := httptest.NewRecorder()
	h.PartAttachments(rec, withID(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/part/%d/attachments?edit=%d", partID, sAtt), nil), partID))
	assertStatus(t, "PartAttachments", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{supplierName, fmt.Sprintf(`value="s:%d" selected`, spID)} {
		if !strings.Contains(body, want) {
			t.Errorf("PartAttachments body missing %q", want)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	bySupplier := h.fetchAttachmentsByVendor(req, strconv.Itoa(partID), supplierScopeCol)
	if len(bySupplier) != 1 || len(bySupplier[spID]) != 1 || bySupplier[spID][0].ID != sAtt || bySupplier[spID][0].Category != "itest-220" {
		t.Errorf("fetchAttachmentsByVendor(supplier) = %+v, want only att %d under %d", bySupplier, sAtt, spID)
	}
	byMfg := h.fetchAttachmentsByVendor(req, strconv.Itoa(partID), mfgScopeCol)
	if len(byMfg) != 1 || len(byMfg[mpID]) != 1 || byMfg[mpID][0].ID != mAtt {
		t.Errorf("fetchAttachmentsByVendor(mfg) = %+v, want only att %d under %d", byMfg, mAtt, mpID)
	}

	// Seed supplier_part 4002 belongs to part 3002, not this part.
	for scope, want := range map[string]string{
		"s:4002": "not linked to this part",
		"x:1":    "invalid linked vendor selection",
		"s:abc":  "invalid linked vendor selection",
	} {
		rec := httptest.NewRecorder()
		h.PartAttachmentCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/attachments", partID),
			form("http://example.test/220-bad-"+smokeUniq("x"), scope, "")), partID))
		assertStatus(t, "PartAttachmentCreate "+scope, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("PartAttachmentCreate(%s): body missing %q", scope, want)
		}
	}

	// Metadata-only update: same link, so file_name and hash stay; the scope and sort order clear.
	rec = httptest.NewRecorder()
	h.PartAttachmentUpdate(rec, withIDAndAttID(postForm(fmt.Sprintf("/part/%d/attachments/%d", partID, sAtt), url.Values{
		"FILFileName": {s.FileName.String}, "FILPNRev": {"B"}, "category": {"itest-220b"}, "comment": {"c2"},
		"order_id": {""}, "vendor_scope": {""},
	}), partID, sAtt))
	assert302(t, "PartAttachmentUpdate", rec)
	u := loadPartAttachment(t, h, ctx, sAtt)
	if u.FileName != s.FileName || u.Hash != s.Hash || u.Rev.String != "B" || u.Category.String != "itest-220b" ||
		u.Comment.String != "c2" || u.SortOrder.Valid || u.SupplierPartID.Valid || u.MfgPartID.Valid {
		t.Errorf("after metadata update = %+v, want same file/hash, rev B, category itest-220b, comment c2, NULL sort and scope", u)
	}

	setPrimary := func(filID string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.PartSetPrimaryAttachment(rec, withID(postForm(fmt.Sprintf("/part/%d/primary", partID),
			url.Values{"filid": {filID}}), partID))
		assert302(t, "PartSetPrimaryAttachment", rec)
	}
	setPrimary(strconv.Itoa(pAtt))
	if got := partPrimary(t, h, ctx, partID); got != pAtt {
		t.Errorf("primary after set = %d, want %d", got, pAtt)
	}
	setPrimary("")
	if got := partPrimary(t, h, ctx, partID); got != 0 {
		t.Errorf("primary after clear = %d, want none", got)
	}
}

// TestIntegration_AttachmentWhereUsed pins the where-used page: a link shared by a
// part and a supplier lists both, and an inactive row is left out (#220).
func TestIntegration_AttachmentWhereUsed(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	link := "http://example.test/220-where-" + smokeUniq("x")
	partID, partNumber, cleanupPart := seedThrowawayPart(t, h, ctx, "220wu")
	defer cleanupPart()
	_, cleanupAtt := seedThrowawayAttachment(t, h, ctx, partID, link, "Test")
	defer cleanupAtt()
	goneID, goneNumber, cleanupGone := seedThrowawayPart(t, h, ctx, "220wu-gone")
	defer cleanupGone()
	goneAtt, cleanupGoneAtt := seedThrowawayAttachment(t, h, ctx, goneID, link, "Test")
	defer cleanupGoneAtt()
	smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET is_active=FALSE WHERE id=$1`, h.cfg().AttachmentsTable()), goneAtt)

	supplierID, cleanupSupplier := seedSupplier(t, h, ctx)
	defer cleanupSupplier()
	var companyAtt int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (supplier_id, file_path) VALUES ($1,$2) RETURNING supplier_attachment_id`,
		h.cfg().CompanyAttachmentsTable()), supplierID, link).Scan(&companyAtt); err != nil {
		t.Fatalf("seed company_attachment: %v", err)
	}
	defer deleteCompanyAttachmentRow(ctx, h, companyAtt)
	var supplierName string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT name FROM %s WHERE id=$1`, "company"), supplierID).Scan(&supplierName); err != nil {
		t.Fatalf("select supplier name: %v", err)
	}

	rec := httptest.NewRecorder()
	h.AttachmentWhereUsed(rec, httptest.NewRequest(http.MethodGet, "/attachments/where-used?file="+url.QueryEscape(link), nil))
	assertStatus(t, "AttachmentWhereUsed", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{partNumber, supplierName} {
		if !strings.Contains(body, want) {
			t.Errorf("where-used body missing %q", want)
		}
	}
	if strings.Contains(body, goneNumber) {
		t.Errorf("where-used lists inactive attachment's part %q", goneNumber)
	}
}

// TestIntegration_APIPartLocalAttachments pins the PO import picker's list: active
// LOCAL: files only, with their base names (#156, #220).
func TestIntegration_APIPartLocalAttachments(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, _, cleanupPart := seedThrowawayPart(t, h, ctx, "220local")
	defer cleanupPart()
	defer deletePartAttachments(ctx, h, partID)
	fileAtt, _ := seedThrowawayAttachment(t, h, ctx, partID, `LOCAL:sub\drawing.pdf`, "Test")
	seedThrowawayAttachment(t, h, ctx, partID, "LOCAL:folder/", "Test")
	seedThrowawayAttachment(t, h, ctx, partID, "http://example.test/x.pdf", "Test")
	goneAtt, _ := seedThrowawayAttachment(t, h, ctx, partID, "LOCAL:gone.pdf", "Test")
	smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET is_active=FALSE WHERE id=$1`, h.cfg().AttachmentsTable()), goneAtt)

	rec := httptest.NewRecorder()
	h.APIPartLocalAttachments(rec, withID(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/part/%d/local-attachments", partID), nil), partID))
	assertStatus(t, "APIPartLocalAttachments", rec, http.StatusOK)
	var got []struct {
		ID       int    `json:"id"`
		BaseName string `json:"base_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].ID != fileAtt || got[0].BaseName != "drawing.pdf" {
		t.Errorf("local attachments = %+v, want only [{%d drawing.pdf}]", got, fileAtt)
	}
}

// companyAttachmentRow is the stored state of one company_attachment row.
type companyAttachmentRow struct {
	FilePath    string
	Notes, Hash sql.NullString
	SortOrder   sql.NullInt64
	IsActive    bool
}

func loadCompanyAttachment(t *testing.T, h *Handler, ctx context.Context, attID int) companyAttachmentRow {
	t.Helper()
	var a companyAttachmentRow
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT file_path, notes, hash, sort_order, is_active FROM %s WHERE supplier_attachment_id=$1`,
		h.cfg().CompanyAttachmentsTable()), attID,
	).Scan(&a.FilePath, &a.Notes, &a.Hash, &a.SortOrder, &a.IsActive); err != nil {
		t.Fatalf("load company_attachment %d: %v", attID, err)
	}
	return a
}

// createCompanyAttachment posts SupplierAttachmentCreate and returns the new row's id.
func createCompanyAttachment(t *testing.T, h *Handler, ctx context.Context, supplierID int, form url.Values) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.SupplierAttachmentCreate(rec, withID(postForm(fmt.Sprintf("/supplier/%d/attachments", supplierID), form), supplierID))
	assert302(t, "SupplierAttachmentCreate", rec)
	var id int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT MAX(supplier_attachment_id) FROM %s WHERE supplier_id=$1`, h.cfg().CompanyAttachmentsTable()), supplierID).Scan(&id); err != nil {
		t.Fatalf("capture new company attachment id: %v", err)
	}
	return id
}

func updateCompanyAttachment(t *testing.T, h *Handler, supplierID, attID int, form url.Values) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.SupplierAttachmentUpdate(rec, withIDAndAttID(postForm(
		fmt.Sprintf("/supplier/%d/attachments/%d", supplierID, attID), form), supplierID, attID))
	assert302(t, "SupplierAttachmentUpdate", rec)
}

// TestIntegration_SupplierAttachments_ListUpdateDelete pins the supplier attachment
// round trip: stored fields, the listing, a metadata-only update, a file change
// (new hash), SupplierSetPrimaryAttachment, and the soft delete (#220).
func TestIntegration_SupplierAttachments_ListUpdateDelete(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	supplierID, cleanupSupplier := seedSupplier(t, h, ctx)
	defer cleanupSupplier()
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET primary_attachment_id=NULL WHERE id=$1`, "company"), supplierID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE supplier_id=$1`, h.cfg().CompanyAttachmentsTable()), supplierID)
	}()

	link := "http://example.test/220-sup-" + smokeUniq("x")
	attID := createCompanyAttachment(t, h, ctx, supplierID, url.Values{
		"file_path": {link}, "notes": {" note-220 "}, "sort_order": {"2"}})
	a := loadCompanyAttachment(t, h, ctx, attID)
	if a.FilePath != link || a.Notes.String != "note-220" || a.SortOrder.Int64 != 2 || a.Hash.String != hashLinkString(link) || !a.IsActive {
		t.Errorf("created row = %+v, want link, trimmed notes, sort 2, link hash, active", a)
	}

	rec := httptest.NewRecorder()
	h.SupplierAttachments(rec, withID(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/supplier/%d/attachments", supplierID), nil), supplierID))
	assertStatus(t, "SupplierAttachments", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "note-220") {
		t.Errorf("SupplierAttachments body missing the attachment's notes")
	}

	updateCompanyAttachment(t, h, supplierID, attID, url.Values{"file_path": {link}, "notes": {"note-2"}, "sort_order": {""}})
	u := loadCompanyAttachment(t, h, ctx, attID)
	if u.FilePath != link || u.Hash != a.Hash || u.Notes.String != "note-2" || u.SortOrder.Valid {
		t.Errorf("after metadata update = %+v, want same link/hash, notes note-2, NULL sort", u)
	}

	link2 := "http://example.test/220-sup2-" + smokeUniq("x")
	updateCompanyAttachment(t, h, supplierID, attID, url.Values{"file_path": {link2}, "notes": {"note-3"}, "sort_order": {"5"}})
	u = loadCompanyAttachment(t, h, ctx, attID)
	if u.FilePath != link2 || u.Hash.String != hashLinkString(link2) || u.Notes.String != "note-3" || u.SortOrder.Int64 != 5 {
		t.Errorf("after file change = %+v, want link2, its hash, notes note-3, sort 5", u)
	}

	supplierPrimary := func() int {
		t.Helper()
		var id sql.NullInt64
		if err := h.queryRowContext(ctx, fmt.Sprintf(
			`SELECT primary_attachment_id FROM %s WHERE id=$1`, "company"), supplierID).Scan(&id); err != nil {
			t.Fatalf("select supplier primary: %v", err)
		}
		return int(id.Int64)
	}
	setPrimary := func(v string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.SupplierSetPrimaryAttachment(rec, withID(postForm(fmt.Sprintf("/supplier/%d/primary", supplierID),
			url.Values{"attachment_id": {v}}), supplierID))
		assert302(t, "SupplierSetPrimaryAttachment", rec)
	}
	setPrimary("")
	if got := supplierPrimary(); got != 0 {
		t.Errorf("primary after clear = %d, want none", got)
	}
	setPrimary(strconv.Itoa(attID))
	if got := supplierPrimary(); got != attID {
		t.Errorf("primary after set = %d, want %d", got, attID)
	}

	rec = httptest.NewRecorder()
	h.SupplierAttachmentDelete(rec, withIDAndAttID(postForm(
		fmt.Sprintf("/supplier/%d/attachments/%d/delete", supplierID, attID), url.Values{}), supplierID, attID))
	assert302(t, "SupplierAttachmentDelete", rec)
	if loadCompanyAttachment(t, h, ctx, attID).IsActive {
		t.Errorf("attachment still active after delete")
	}
	if got := supplierPrimary(); got != 0 {
		t.Errorf("primary after deleting the only attachment = %d, want none", got)
	}
}

// TestIntegration_SupplierAttachmentUpdate_RemovesOldFileFromSupplierRoot: replacing
// a supplier attachment's LOCAL: file removes the old file from the supplier files
// root it lives in, never a same-named file under Doc Control (#220).
func TestIntegration_SupplierAttachmentUpdate_RemovesOldFileFromSupplierRoot(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	docRoot := tempDocControlRoot(t, h)
	supRoot := t.TempDir()
	prevSupRoot := h.cfg().SupplierFilesRoot
	h.cfg().SupplierFilesRoot = supRoot
	defer func() { h.cfg().SupplierFilesRoot = prevSupRoot }()

	// Unique content, so the file's hash can't match a leftover row and trip the duplicate warning.
	const name = "old-220.txt"
	content := []byte(smokeUniq("220-root"))
	for _, root := range []string{docRoot, supRoot} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0644); err != nil {
			t.Fatalf("write %s: %v", root, err)
		}
	}

	supplierID, cleanupSupplier := seedSupplier(t, h, ctx)
	defer cleanupSupplier()
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET primary_attachment_id=NULL WHERE id=$1`, "company"), supplierID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE supplier_id=$1`, h.cfg().CompanyAttachmentsTable()), supplierID)
	}()
	attID := createCompanyAttachment(t, h, ctx, supplierID, url.Values{"file_path": {"LOCAL:" + name}})

	updateCompanyAttachment(t, h, supplierID, attID, url.Values{"file_path": {"http://example.test/220-new-" + smokeUniq("x")}})

	if _, err := os.Stat(filepath.Join(supRoot, name)); !os.IsNotExist(err) {
		t.Errorf("old supplier file still present (stat err %v), want removed", err)
	}
	if _, err := os.Stat(filepath.Join(docRoot, name)); err != nil {
		t.Errorf("same-named Doc Control file was removed: %v", err)
	}
}
