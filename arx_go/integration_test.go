//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
)

// liveHandler opens a real DB connection to ArxDev and returns a Handler plus a
// cleanup function that removes any rows created during the test.
func liveHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	dsn := os.Getenv("ARX_TEST_DSN")
	if dsn == "" {
		t.Skip("set ARX_TEST_DSN (ArxDev) to run integration tests")
	}
	if !strings.Contains(strings.ToLower(dsn), "arxdev") {
		t.Fatal("integration tests must target the ArxDev database")
	}

	cfg := arxbase.Load("dev")
	cfg.TestMode = true

	database, err := arxdb.Connect(dsn)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}

	h := New(database, cfg, templatesFS, nil)

	cleanup := func() {
		database.Close()
	}
	return h, cleanup
}

// withID injects a chi route context carrying the given "id" URL parameter.
func withID(req *http.Request, id int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// withIDAndAttID injects chi route params for both "id" and "attID".
func withIDAndAttID(req *http.Request, id, attID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("attID", strconv.Itoa(attID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// postForm builds a POST request with URL-encoded form body.
func postForm(target string, vals url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// assert302 fails the test if the recorder's status is not 302, printing the body.
func assert302(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("%s: got status %d, want 302. body: %s", label, rec.Code, rec.Body.String())
	}
}

// assertStatus fails the test if the recorder's status doesn't match want, printing the body.
func assertStatus(t *testing.T, label string, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: got status %d, want %d. body: %s", label, rec.Code, want, rec.Body.String())
	}
}

// TestIntegration_PartLifecycle exercises the full part + attachment round-trip
// against the ArxDev database:
//
//  1. Create a part (identity INSERT with OUTPUT INSERTED.PNID)
//  2. Update the part title
//  3. Add an attachment (triggers trg_FIL_part_count → PN.PNFILLinks = 1)
//  4. Soft-delete the attachment (trigger → PN.PNFILLinks = 0)
//  5. Hard-delete the test rows (cleanup)
func TestIntegration_PartLifecycle(t *testing.T) {
	h, baseCleanup := liveHandler(t)
	defer baseCleanup()

	ctx := context.Background()
	partNumber := "ITEST-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// ── 1. Create ─────────────────────────────────────────────────────────────
	createVals := url.Values{
		"part_number":    {partNumber},
		"revision":       {"A"},
		"title":          {"Integration Test Part"},
		"release_status": {"U"},
		"active":         {"1"},
	}
	rec := httptest.NewRecorder()
	h.PartsCreate(rec, postForm("/parts", createVals))
	assert302(t, "PartsCreate", rec)

	// Parse the new PNID from the redirect Location: /part/{id}
	loc := rec.Header().Get("Location")
	pnIDStr := strings.TrimPrefix(loc, "/part/")
	pnID, err := strconv.Atoi(pnIDStr)
	if err != nil || pnID == 0 {
		t.Fatalf("PartsCreate: could not parse PNID from Location %q: %v", loc, err)
	}

	// Register cleanup — hard-delete FIL rows then the PN row.
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), pnID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), pnID)
	}()

	// ── 2. Update ─────────────────────────────────────────────────────────────
	newTitle := "Updated Integration Test Part"
	updateVals := url.Values{
		"part_number":    {partNumber},
		"title":          {newTitle},
		"release_status": {"U"},
		"active":         {"1"},
	}
	rec = httptest.NewRecorder()
	h.PartUpdate(rec, withID(postForm(fmt.Sprintf("/part/%d", pnID), updateVals), pnID))
	assert302(t, "PartUpdate", rec)

	var gotTitle string
	err = h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT title FROM %s WHERE id=@p1`, h.cfg.PartsTable()), pnID,
	).Scan(&gotTitle)
	if err != nil {
		t.Fatalf("SELECT title after PartUpdate: %v", err)
	}
	if gotTitle != newTitle {
		t.Errorf("PartUpdate: title = %q, want %q", gotTitle, newTitle)
	}

	// ── 3. Attachment create + trigger check ───────────────────────────────────
	attVals := url.Values{
		"FILFileName": {"http://example.test/itest"},
		"FILPNRev":   {"A"},
		"category":    {"integration-test-category"},
	}
	rec = httptest.NewRecorder()
	h.PartAttachmentCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/attachments", pnID), attVals), pnID))
	assert302(t, "PartAttachmentCreate", rec)

	var filLinks int
	err = h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT attachment_count FROM %s WHERE id=@p1`, h.cfg.PartsTable()), pnID,
	).Scan(&filLinks)
	if err != nil {
		t.Fatalf("SELECT PNFILLinks after attach create: %v", err)
	}
	if filLinks != 1 {
		t.Errorf("PNFILLinks after attach create = %d, want 1", filLinks)
	}

	// Capture the new FILID for the delete step.
	var filID int
	err = h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT MAX(id) FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), pnID,
	).Scan(&filID)
	if err != nil || filID == 0 {
		t.Fatalf("could not retrieve id after attach create: %v", err)
	}

	// Regression (#527): PartAttachmentCreate must persist the submitted
	// "category" form value, not an unrelated field.
	var attCategory string
	err = h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT category FROM %s WHERE id=@p1`, h.cfg.AttachmentsTable()), filID,
	).Scan(&attCategory)
	if err != nil {
		t.Fatalf("SELECT category after attach create: %v", err)
	}
	if attCategory != "integration-test-category" {
		t.Errorf("attachment category after create = %q, want %q", attCategory, "integration-test-category")
	}

	// ── 4. Attachment soft-delete + trigger check ──────────────────────────────
	rec = httptest.NewRecorder()
	h.PartAttachmentDelete(rec, withIDAndAttID(
		postForm(fmt.Sprintf("/part/%d/attachments/%d/delete", pnID, filID), url.Values{}),
		pnID, filID,
	))
	assert302(t, "PartAttachmentDelete", rec)

	err = h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT attachment_count FROM %s WHERE id=@p1`, h.cfg.PartsTable()), pnID,
	).Scan(&filLinks)
	if err != nil {
		t.Fatalf("SELECT PNFILLinks after attach delete: %v", err)
	}
	if filLinks != 0 {
		t.Errorf("PNFILLinks after attach delete = %d, want 0", filLinks)
	}
}

func TestIntegration_RecordFilters(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Seed one throwaway form (part_number_id is NOT NULL but unconstrained).
	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (0, '', 0, 1)`, h.cfg.FormsTable()),
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	}()

	// Seed four records: WIP/Complete/Approved + a type/date spread.
	type seed struct {
		sn          string
		comments    string
		date        string
		locked, app int
	}
	seeds := []seed{
		{"101", "New Release", "2026-01-10", 0, 0}, // WIP
		{"102", "Re-Test", "2026-02-10", 1, 0},     // Complete
		{"103", "Re-Test", "2026-03-10", 1, 1},     // Approved
		{"104", "New Release", "2026-04-10", 0, 0}, // WIP
	}
	for _, s := range seeds {
		if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (form_id, record_date, serial_number, comments, is_locked, is_approved, is_active)
			 VALUES (@p1, @p2, @p3, @p4, @p5, @p6, 1)`, h.cfg.RecordsTable()),
			formID, s.date, s.sn, s.comments, s.locked, s.app); err != nil {
			t.Fatalf("seed record %s: %v", s.sn, err)
		}
	}

	// run applies a filter's clauses to the form's records and returns matching SNs.
	run := func(q url.Values) []string {
		f := parseRecordFilters(q)
		clauses, fargs := f.whereClauses(2)
		query := fmt.Sprintf(
			`SELECT serial_number FROM %s WHERE form_id = @p1 AND is_active = 1%s
			 ORDER BY TRY_CAST(serial_number AS INT)`, h.cfg.RecordsTable(), clauses)
		rows, err := h.queryContext(ctx, query, append([]any{formID}, fargs...)...)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var sn string
			if rows.Scan(&sn) == nil {
				out = append(out, sn)
			}
		}
		return out
	}

	eq := func(name string, got, want []string) {
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}

	eq("wip", run(url.Values{}), []string{"101", "104"})
	eq("complete", run(url.Values{"status": {"complete"}}), []string{"102"})
	eq("approved", run(url.Values{"status": {"approved"}}), []string{"103"})
	eq("all", run(url.Values{"status": {"all"}}), []string{"101", "102", "103", "104"})
	eq("type", run(url.Values{"status": {"all"}, "type": {"Re-Test"}}), []string{"102", "103"})
	eq("daterange", run(url.Values{"status": {"all"}, "from": {"2026-02-01"}, "to": {"2026-03-31"}}),
		[]string{"102", "103"})
}

// TestIntegration_AttachStepPassFail exercises the pf_type = "attach" pass/fail
// evaluation (#587) through the real SaveResults DB round-trip — no image file
// is ever written; a plain string standing in for a filename is posted as the
// result value, matching how the paste-image endpoint hands off to Save
// (it only writes a filename into the result_<testID> form field).
func TestIntegration_AttachStepPassFail(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (0, '', 0, 1)`, h.cfg.FormsTable()),
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE record_id IN (SELECT id FROM %s WHERE form_id=@p1)`,
				h.cfg.ResultsTable(), h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.StepsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	}()

	var testID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter, pf_type)
		 OUTPUT INSERTED.id VALUES (@p1, 0, 'Screenshot/File Panel Photo', 'attach')`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed test_definition: %v", err)
	}

	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg.FormsTable()),
		strconv.Itoa(testID), formID); err != nil {
		t.Fatalf("set form test_order: %v", err)
	}

	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, serial_number, serial_number_pn, serial_number_pn_desc, test_order, comments, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'ITEST-587', '', '', @p2, '', 0, 1)`, h.cfg.RecordsTable()),
		formID, strconv.Itoa(testID),
	).Scan(&recordID); err != nil {
		t.Fatalf("seed test_record: %v", err)
	}

	// ── Post a stand-in filename (no disk write) and confirm PASS ──────────────
	fakeFilename := "SN123_rID" + strconv.Itoa(recordID) + "_tID" + strconv.Itoa(testID) + "_20260101_000000.png"
	rec := httptest.NewRecorder()
	h.SaveResults(rec, withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{
		fmt.Sprintf("result_%d", testID): {fakeFilename},
	}), recordID))
	assertStatus(t, "SaveResults (attach, filled)", rec, http.StatusSeeOther)

	var result string
	var passFail sql.NullBool
	err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT result, pass_fail FROM %s WHERE record_id=@p1 AND test_id=@p2`,
		h.cfg.ResultsTable()), recordID, testID,
	).Scan(&result, &passFail)
	if err != nil {
		t.Fatalf("SELECT result/pass_fail after filled save: %v", err)
	}
	if result != fakeFilename {
		t.Errorf("result = %q, want %q", result, fakeFilename)
	}
	if !passFail.Valid || !passFail.Bool {
		t.Errorf("pass_fail after filled attach result = %v, want true (PASS)", passFail)
	}

	// ── Clear it and confirm MISSING (pass_fail NULL) ──────────────────────────
	rec = httptest.NewRecorder()
	h.SaveResults(rec, withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{
		fmt.Sprintf("result_%d", testID): {""},
	}), recordID))
	assertStatus(t, "SaveResults (attach, cleared)", rec, http.StatusSeeOther)

	err = h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT result, pass_fail FROM %s WHERE record_id=@p1 AND test_id=@p2`,
		h.cfg.ResultsTable()), recordID, testID,
	).Scan(&result, &passFail)
	if err != nil {
		t.Fatalf("SELECT result/pass_fail after clearing: %v", err)
	}
	if result != "" {
		t.Errorf("result after clearing = %q, want empty", result)
	}
	if passFail.Valid {
		t.Errorf("pass_fail after clearing attach result = %v, want NULL (MISSING)", passFail)
	}
}

// TestIntegration_PasteResultImageGuards exercises APIRecordPasteResultImage's
// DB-backed guard clauses (record not found, record locked) — both return
// before the handler ever touches the filesystem (no MkdirAll/write call is
// reached), so this test never writes an image file to disk.
func TestIntegration_PasteResultImageGuards(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	h.cfg.ImageRoot = t.TempDir() // non-empty so the ImageRoot-configured check passes; never written to

	// 1x1 transparent PNG, base64-encoded — small valid image_data payload.
	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	postPasteImage := func(recordID, testID int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/api/record/%d/step/%d/paste-image", recordID, testID),
			strings.NewReader(fmt.Sprintf(`{"image_data":%q}`, tinyPNG)))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.Itoa(recordID))
		rctx.URLParams.Add("tid", strconv.Itoa(testID))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.APIRecordPasteResultImage(rec, req)
		return rec
	}

	// ── Not found ───────────────────────────────────────────────────────────
	rec := postPasteImage(999999999, 1)
	if rec.Code != http.StatusNotFound {
		t.Errorf("paste-image on missing record: status = %d, want %d. body: %s",
			rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// ── Locked record ───────────────────────────────────────────────────────
	// APIRecordPasteResultImage INNER JOINs to part via form.part_number_id, so
	// (unlike some other seeds in this file) a real part row is required here.
	var partID int
	partNumber := "ITEST-587-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}()

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	}()

	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, serial_number, serial_number_pn, serial_number_pn_desc, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'ITEST-587-LOCKED', '', '', 1, 1)`, h.cfg.RecordsTable()), formID,
	).Scan(&recordID); err != nil {
		t.Fatalf("seed locked test_record: %v", err)
	}

	rec = postPasteImage(recordID, 1)
	if rec.Code != http.StatusConflict {
		t.Errorf("paste-image on locked record: status = %d, want %d. body: %s",
			rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// TestIntegration_UpdatedAtSentinel guards against handler bugs that touch (or cascade an
// update onto) rows other than the one intended: seed_test_data.sql pins updated_at on every
// seeded row in these tables to a fixed sentinel instead of GETDATE(), so any drift here means
// something wrote to a row this test suite never asked to change.
func TestIntegration_UpdatedAtSentinel(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const sentinel = "2020-01-01T00:00:00"

	checks := []struct {
		table string
		idCol string
		ids   []int
	}{
		{h.cfg.ContactTable(), "id", []int{2001, 2002, 2003, 2004, 2005}},
		{h.cfg.UsersTable(), "id", []int{8001, 8002}},
		{h.cfg.StepsTable(), "id", []int{6101, 6102, 6103, 6104, 6105, 6106, 6107, 6108}},
		{h.cfg.RecordsTable(), "id", []int{7001, 7002, 7003, 7004, 7005}},
		{h.cfg.ResultsTable(), "id", []int{
			7101, 7102, 7103, 7104, 7105, 7106, 7107, 7108,
			7109, 7110, 7111, 7112, 7113, 7114, 7115, 7116,
		}},
	}

	for _, c := range checks {
		for _, id := range c.ids {
			var got sql.NullTime
			err := h.DB().QueryRowContext(ctx,
				fmt.Sprintf(`SELECT updated_at FROM %s WHERE %s=@p1`, c.table, c.idCol), id,
			).Scan(&got)
			if err != nil {
				t.Fatalf("SELECT updated_at FROM %s WHERE %s=%d: %v", c.table, c.idCol, id, err)
			}
			if !got.Valid || got.Time.Format("2006-01-02T15:04:05") != sentinel {
				t.Errorf("%s id %d: updated_at = %v, want sentinel %s (row was touched by something, "+
					"or ArxDev needs SQL/seed_test_data.sql re-run)",
					c.table, id, got, sentinel)
			}
		}
	}

	// app_config and named_queries key off setting_key/name, not id.
	var appConfigUpdated sql.NullTime
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT updated_at FROM %s WHERE setting_key=@p1`, h.cfg.AppConfigTable()), "schema_version",
	).Scan(&appConfigUpdated); err != nil {
		t.Fatalf("SELECT updated_at FROM app_config: %v", err)
	}
	if !appConfigUpdated.Valid || appConfigUpdated.Time.Format("2006-01-02T15:04:05") != sentinel {
		t.Errorf("app_config schema_version: updated_at = %v, want sentinel %s (or ArxDev needs reseeding)",
			appConfigUpdated, sentinel)
	}

	var namedQueryUpdated sql.NullTime
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT updated_at FROM %s WHERE name=@p1`, h.cfg.NamedQueriesTable()), "fil_category_for_pn",
	).Scan(&namedQueryUpdated); err != nil {
		t.Fatalf("SELECT updated_at FROM named_queries: %v", err)
	}
	if !namedQueryUpdated.Valid || namedQueryUpdated.Time.Format("2006-01-02T15:04:05") != sentinel {
		t.Errorf("named_queries fil_category_for_pn: updated_at = %v, want sentinel %s (or ArxDev needs reseeding)",
			namedQueryUpdated, sentinel)
	}
}

// TestIntegration_ContactPOs verifies the contact→PO lookup (#597) against the
// pinned seed rows: contact 2001 (John Doe) is the supplier contact on PO 5003;
// contact 2005 (Pat Dock) is the receiver contact on both 5002 and 5003. Guards
// the role CASE and the supplier_contact_id OR receiver_contact_id predicate.
// Read-only — no cleanup.
func TestIntegration_ContactPOs(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Supplier-linked contact: exactly PO 5003, role "Supplier".
	sup := h.contactPOs(ctx, 2001)
	if len(sup) != 1 {
		t.Fatalf("contactPOs(2001): got %d rows, want 1 (ArxDev may need reseeding): %+v", len(sup), sup)
	}
	if sup[0].Number != "5003" || sup[0].Role != "Supplier" {
		t.Errorf("contactPOs(2001)[0]: got {Number:%q Role:%q}, want {5003 Supplier}", sup[0].Number, sup[0].Role)
	}

	// Receiver-linked contact: POs 5002 then 5003 (date_ordered DESC), both "Receiver".
	rec := h.contactPOs(ctx, 2005)
	if len(rec) != 2 {
		t.Fatalf("contactPOs(2005): got %d rows, want 2 (ArxDev may need reseeding): %+v", len(rec), rec)
	}
	wantNums := []string{"5002", "5003"}
	for i, p := range rec {
		if p.Number != wantNums[i] || p.Role != "Receiver" {
			t.Errorf("contactPOs(2005)[%d]: got {Number:%q Role:%q}, want {%s Receiver}", i, p.Number, p.Role, wantNums[i])
		}
	}
}

// TestIntegration_BuildCostConsolidation verifies the #466 qty-break build-cost
// calculation against the pinned seed BOM: part 3005 (Widget Assembly) uses screw
// 3002 directly (bom line 3901, qty 2) AND nests sub-assembly 3012 (bom line 3907,
// qty 1), which itself also directly uses screw 3002 (bom line 3908, qty 3). At a
// build qty of 300, the screw's two occurrences extend to 600 and 900 respectively
// — each individually below the 1000-pack tier (price rows 4203/4205) — but their
// consolidated demand of 1500 crosses into it. This is the entire point of
// aggregating before pricing: a per-occurrence rollup would never reach that tier.
// Read-only — buildCost does not write to the DB, so no cleanup.
func TestIntegration_BuildCostConsolidation(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	res, err := h.buildCost(ctx, 3005, 300)
	if err != nil {
		t.Fatalf("buildCost(3005, 300): %v", err)
	}
	if res.Cycle {
		t.Fatal("buildCost(3005, 300): unexpected cycle")
	}

	byPN := map[string]buildCostLine{}
	for _, l := range res.Lines {
		byPN[l.PartNumber] = l
	}

	screw, ok := byPN["BUY-1001"]
	if !ok {
		t.Fatalf("buildCost(3005, 300): no consolidated line for BUY-1001 (ArxDev may need reseeding): %+v", res.Lines)
	}
	if screw.QtyNeeded != 1500 {
		t.Errorf("BUY-1001 QtyNeeded = %v, want 1500 (600 direct + 900 via sub-assembly 3012)", screw.QtyNeeded)
	}
	if screw.Source != "price" || screw.PackSize != 1000 || screw.UnitPrice != 0.03 {
		t.Errorf("BUY-1001 tier = {Source:%q PackSize:%v UnitPrice:%v}, want {price 1000 0.03} — consolidated qty 1500 should cross into the 1000-pack tier",
			screw.Source, screw.PackSize, screw.UnitPrice)
	}
	if screw.ExtCost != 45 {
		t.Errorf("BUY-1001 ExtCost = %v, want 45 (1500 * 0.03)", screw.ExtCost)
	}

	raw, ok := byPN["RAW-1001"]
	if !ok {
		t.Fatalf("buildCost(3005, 300): no consolidated line for RAW-1001: %+v", res.Lines)
	}
	if raw.QtyNeeded != 600 || raw.Source != "price" || raw.PackSize != 10 || raw.UnitPrice != 2.50 {
		t.Errorf("RAW-1001 = {Qty:%v Source:%q Pack:%v Price:%v}, want {600 price 10 2.50}",
			raw.QtyNeeded, raw.Source, raw.PackSize, raw.UnitPrice)
	}

	// OPS labor (3006) has no default supplier, so it's always "missing" in this
	// mode — no current_cost fallback (decision 6b in the design plan).
	labor, ok := byPN["OPS-1001"]
	if !ok {
		t.Fatalf("buildCost(3005, 300): no consolidated line for OPS-1001: %+v", res.Lines)
	}
	if labor.Source != "missing" {
		t.Errorf("OPS-1001 Source = %q, want \"missing\" (no default supplier, no price fallback)", labor.Source)
	}
}

// TestIntegration_BuildCostBelowAllTiers exercises the "below every tier" fallback
// (decision #3 in the design plan) against the real seeded price rows, not just the
// pure pickTier unit test — proving aggregateLeafQty + buildCost actually wire the
// tier lookup correctly end to end. At a build qty of 0.001, screw 3002's
// consolidated demand (0.005) falls below its smallest tier (pack_size 1), and
// RAW-1001's demand (0.002) falls below its only tier (pack_size 10) — both must
// report "missing", not extrapolate or fall back to current_cost.
// Read-only — no cleanup.
func TestIntegration_BuildCostBelowAllTiers(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	res, err := h.buildCost(ctx, 3005, 0.001)
	if err != nil {
		t.Fatalf("buildCost(3005, 0.001): %v", err)
	}
	if res.Cycle {
		t.Fatal("buildCost(3005, 0.001): unexpected cycle")
	}

	byPN := map[string]buildCostLine{}
	for _, l := range res.Lines {
		byPN[l.PartNumber] = l
	}

	for _, pn := range []string{"BUY-1001", "RAW-1001"} {
		line, ok := byPN[pn]
		if !ok {
			t.Fatalf("buildCost(3005, 0.001): no consolidated line for %s (ArxDev may need reseeding): %+v", pn, res.Lines)
		}
		if line.Source != "missing" {
			t.Errorf("%s Source = %q, want \"missing\" (aggregated qty %v is below every tier's pack_size)", pn, line.Source, line.QtyNeeded)
		}
		if line.ExtCost != 0 {
			t.Errorf("%s ExtCost = %v, want 0 for a missing line", pn, line.ExtCost)
		}
	}
}

// TestIntegration_BuildCostCycleDetection creates a throwaway two-part cycle
// (A's BOM contains B, B's BOM contains A) — a scenario the static seed data
// deliberately doesn't have — and verifies aggregateLeafQty's path-scoped
// cycle detection actually trips instead of recursing forever.
func TestIntegration_BuildCostCycleDetection(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	pn := h.cfg.PartsTable()
	pl := h.cfg.BOMTable()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)

	var idA, idB int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number) OUTPUT INSERTED.id VALUES (@p1)`, pn),
		"ITEST-CYCLE-A-"+suffix).Scan(&idA); err != nil {
		t.Fatalf("seed part A: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number) OUTPUT INSERTED.id VALUES (@p1)`, pn),
		"ITEST-CYCLE-B-"+suffix).Scan(&idB); err != nil {
		t.Fatalf("seed part B: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE parent_part_id IN (@p1,@p2)`, pl), idA, idB)
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id IN (@p1,@p2)`, pn), idA, idB)
	}()

	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (parent_part_id, component_part_id, qty) VALUES (@p1,@p2,1)`, pl), idA, idB); err != nil {
		t.Fatalf("seed bom A->B: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (parent_part_id, component_part_id, qty) VALUES (@p1,@p2,1)`, pl), idB, idA); err != nil {
		t.Fatalf("seed bom B->A: %v", err)
	}

	res, err := h.buildCost(ctx, idA, 10)
	if err != nil {
		t.Fatalf("buildCost(cycle): %v", err)
	}
	if !res.Cycle {
		t.Error("buildCost(cycle): Cycle = false, want true for a self-referencing BOM")
	}
}

// TestIntegration_PartBuildCostHandler exercises the full HTTP handler (route
// param + query-string parsing + template render), not just the buildCost
// function directly — guarding against a wiring bug (e.g. wrong query key,
// template referencing a renamed field) that a function-level test can't catch.
// Uses the same pinned qty=300 scenario as TestIntegration_BuildCostConsolidation.
// Read-only — no cleanup.
func TestIntegration_PartBuildCostHandler(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	get := func(qty string) (int, string) {
		req := withID(httptest.NewRequest(http.MethodGet, "/part/3005/build-cost?qty="+qty, nil), 3005)
		rec := httptest.NewRecorder()
		h.PartBuildCost(rec, req)
		return rec.Code, rec.Body.String()
	}

	code, body := get("300")
	if code != http.StatusOK {
		t.Fatalf("PartBuildCost(qty=300): status %d, want 200", code)
	}
	for _, want := range []string{"BUY-1001", "1500", "0.0300", "45.0000", "RAW-1001", "600", "1545.0000", "Missing"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartBuildCost(qty=300): body missing %q", want)
		}
	}

	for _, badQty := range []string{"0", "-5", "abc", ""} {
		_, body := get(badQty)
		if !strings.Contains(body, "Invalid build quantity") {
			t.Errorf("PartBuildCost(qty=%q): expected \"Invalid build quantity\" error, got body: %s", badQty, body)
		}
	}
}

// TestIntegration_BuildCostTierShiftsWithQty verifies a *different* qty picks a
// *different* tier for the same shared screw (3002): at qty=1, its consolidated
// demand is 2+3=5 (well below the 100 tier used at qty=300 in
// TestIntegration_BuildCostConsolidation), so it must price at the 1-unit tier
// (price row 4204, $0.10) instead. Guards against a tier rule that happens to
// work for one qty but is actually hardcoded or off-by-one.
// Read-only — no cleanup.
func TestIntegration_BuildCostTierShiftsWithQty(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	res, err := h.buildCost(ctx, 3005, 1)
	if err != nil {
		t.Fatalf("buildCost(3005, 1): %v", err)
	}
	var screw *buildCostLine
	for i := range res.Lines {
		if res.Lines[i].PartNumber == "BUY-1001" {
			screw = &res.Lines[i]
		}
	}
	if screw == nil {
		t.Fatalf("buildCost(3005, 1): no consolidated line for BUY-1001 (ArxDev may need reseeding): %+v", res.Lines)
	}
	if screw.QtyNeeded != 5 {
		t.Errorf("BUY-1001 QtyNeeded at qty=1 = %v, want 5 (2 direct + 3 via sub-assembly 3012)", screw.QtyNeeded)
	}
	if screw.Source != "price" || screw.PackSize != 1 || screw.UnitPrice != 0.10 {
		t.Errorf("BUY-1001 tier at qty=1 = {Source:%q PackSize:%v UnitPrice:%v}, want {price 1 0.10} — qty 5 should pick the 1-unit tier, not the 100 or 1000 tier used at higher build qtys",
			screw.Source, screw.PackSize, screw.UnitPrice)
	}
}

// TestIntegration_BuildCostDoesNotWriteRollup guards the read-only design decision
// (#7 in the design plan): running the qty-break build cost must never touch
// part.last_rollup_cost / last_rollup_at, since those columns feed the BOM tab,
// CSV export, and cost reporting under a qty=1 assumption. Runs buildCost at a
// qty (300) that would produce a very different number if it were mistakenly
// written back, then asserts the columns are byte-for-byte unchanged.
func TestIntegration_BuildCostDoesNotWriteRollup(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	pn := h.cfg.PartsTable()

	readRollup := func(id int) (sql.NullFloat64, sql.NullTime) {
		var cost sql.NullFloat64
		var at sql.NullTime
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT last_rollup_cost, last_rollup_at FROM %s WHERE id=@p1`, pn), id).Scan(&cost, &at); err != nil {
			t.Fatalf("read last_rollup_cost for part %d: %v", id, err)
		}
		return cost, at
	}

	// Check both the root (3005) and the nested sub-assembly (3012) it touches —
	// a naive "write back to every visited assembly" bug (copy-pasted from
	// rollupCost/PartRollupCost) would hit both.
	beforeCost5, beforeAt5 := readRollup(3005)
	beforeCost12, beforeAt12 := readRollup(3012)

	if _, err := h.buildCost(ctx, 3005, 300); err != nil {
		t.Fatalf("buildCost(3005, 300): %v", err)
	}

	afterCost5, afterAt5 := readRollup(3005)
	afterCost12, afterAt12 := readRollup(3012)

	if beforeCost5 != afterCost5 || beforeAt5 != afterAt5 {
		t.Errorf("part 3005 last_rollup_cost/at changed: before {%v %v}, after {%v %v} — buildCost must be read-only",
			beforeCost5, beforeAt5, afterCost5, afterAt5)
	}
	if beforeCost12 != afterCost12 || beforeAt12 != afterAt12 {
		t.Errorf("part 3012 last_rollup_cost/at changed: before {%v %v}, after {%v %v} — buildCost must be read-only",
			beforeCost12, beforeAt12, afterCost12, afterAt12)
	}
}

// TestIntegration_BuildCostNonAssemblyPart verifies calling build-cost on a part
// with no BOM (a leaf, e.g. the screw 3002 itself) returns zero consolidated
// lines rather than erroring — and that the handler renders the same
// "No BOM data found" message the BOM tab uses for the same case.
func TestIntegration_BuildCostNonAssemblyPart(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	res, err := h.buildCost(ctx, 3002, 10)
	if err != nil {
		t.Fatalf("buildCost(3002, 10): %v", err)
	}
	if len(res.Lines) != 0 {
		t.Errorf("buildCost(3002, 10): got %d lines, want 0 (3002 has no BOM)", len(res.Lines))
	}

	req := withID(httptest.NewRequest(http.MethodGet, "/part/3002/build-cost?qty=10", nil), 3002)
	rec := httptest.NewRecorder()
	h.PartBuildCost(rec, req)
	if !strings.Contains(rec.Body.String(), "No BOM data found") {
		t.Error("PartBuildCost(3002): expected \"No BOM data found\" message for a non-assembly part")
	}
}

// TestIntegration_BuildCostUIWiring is a lightweight content check on the two
// pieces of this feature a server-side Go test cannot otherwise observe: the
// spinner hookup and the clipboard-copy button. It doesn't prove the browser
// actually shows a spinner or writes to the OS clipboard (that needs a human /
// browser automation — see #514's resolution, which deliberately chose not to
// stand up browser automation for this project) — it only guards against the
// markup/script wiring silently breaking (e.g. a renamed function, a missing
// button id) between here and the next manual check.
func TestIntegration_BuildCostUIWiring(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// BOM edit page: the qty form must call runBuildCost(this) on submit (spinner).
	editReq := withID(httptest.NewRequest(http.MethodGet, "/part/3005/bom/edit", nil), 3005)
	editRec := httptest.NewRecorder()
	h.PartBOMEdit(editRec, editReq)
	editBody := editRec.Body.String()
	for _, want := range []string{"Cost to Build", `onsubmit="runBuildCost(this)"`, "function runBuildCost"} {
		if !strings.Contains(editBody, want) {
			t.Errorf("PartBOMEdit: body missing %q", want)
		}
	}

	// BOM tab: Export CSV must live in the Expand/Collapse/Columns button row, not
	// the top back-button row (moved there because it exports this table).
	bomReq := withID(httptest.NewRequest(http.MethodGet, "/part/3005/bom", nil), 3005)
	bomRec := httptest.NewRecorder()
	h.PartBOM(bomRec, bomReq)
	bomBody := bomRec.Body.String()
	exportIdx := strings.Index(bomBody, "Export CSV")
	expandIdx := strings.Index(bomBody, "Expand All")
	backIdx := strings.Index(bomBody, "Back to")
	if exportIdx == -1 || expandIdx == -1 || backIdx == -1 {
		t.Fatalf("PartBOM: missing expected markers (export=%d expand=%d back=%d)", exportIdx, expandIdx, backIdx)
	}
	if !(backIdx < exportIdx && exportIdx < expandIdx) {
		t.Errorf("PartBOM: expected order Back(%d) < Export CSV(%d) < Expand All(%d) — Export CSV should sit in the table button row, after Back and before Expand All",
			backIdx, exportIdx, expandIdx)
	}

	// Build-cost results page: the copy button + TSV script must be present.
	costReq := withID(httptest.NewRequest(http.MethodGet, "/part/3005/build-cost?qty=300", nil), 3005)
	costRec := httptest.NewRecorder()
	h.PartBuildCost(costRec, costReq)
	costBody := costRec.Body.String()
	for _, want := range []string{`id="copy-build-cost"`, "Copy for Excel", "copyTableTSV("} {
		if !strings.Contains(costBody, want) {
			t.Errorf("PartBuildCost: body missing %q", want)
		}
	}
}
