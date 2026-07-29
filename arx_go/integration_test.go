//go:build integration

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
	"arx/arxlib/urlutil"
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

	database, dialect, err := arxdb.Connect(cfg.DBEngine(), dsn)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}

	h := New(database, dialect, cfg, templatesFS, nil)
	h.loadPartCategories(context.Background())

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

// withIDAndTestID injects chi route params "id" and "testID" (used by ArchiveStep's
// POST /forms/{id}/tests/{testID}/archive route).
func withIDAndTestID(req *http.Request, id, testID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("testID", strconv.Itoa(testID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// withIDAndLotID injects chi route params for both "id" and "lotID".
func withIDAndLotID(req *http.Request, id, lotID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("lotID", strconv.Itoa(lotID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// withIDAndUnitID injects chi route params for both "id" and "unitID".
func withIDAndUnitID(req *http.Request, id, unitID int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(id))
	rctx.URLParams.Add("unitID", strconv.Itoa(unitID))
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

// assertFloatEqual compares two floats with a small tolerance — rollupCost's
// running-total sum accumulates float64 rounding error (e.g. 27.229999999999997
// instead of 27.23) that an exact != comparison would wrongly flag as a bug.
func assertFloatEqual(t *testing.T, label string, got, want float64) {
	t.Helper()
	const epsilon = 0.0001
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}

// seedCyclePair inserts a throwaway two-part BOM cycle (A's BOM contains B, B's
// BOM contains A) — a scenario the static seed data deliberately doesn't have —
// and returns both part IDs plus a cleanup func that deletes the bom and part
// rows. Shared by tests that need to prove path-scoped cycle detection actually
// trips instead of recursing forever.
func seedCyclePair(t *testing.T, h *Handler, ctx context.Context) (idA, idB int, cleanup func()) {
	t.Helper()
	pn := h.cfg.PartsTable()
	pl := h.cfg.BOMTable()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)

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
	cleanup = func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE parent_part_id IN (@p1,@p2)`, pl), idA, idB)
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id IN (@p1,@p2)`, pn), idA, idB)
	}

	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (parent_part_id, component_part_id, qty) VALUES (@p1,@p2,1)`, pl), idA, idB); err != nil {
		cleanup()
		t.Fatalf("seed bom A->B: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (parent_part_id, component_part_id, qty) VALUES (@p1,@p2,1)`, pl), idB, idA); err != nil {
		cleanup()
		t.Fatalf("seed bom B->A: %v", err)
	}
	return idA, idB, cleanup
}

// TestIntegration_PartLifecycle exercises the full part + attachment round-trip
// against the ArxDev database:
//
//  1. Create a part (identity INSERT with OUTPUT INSERTED.PNID)
//  2. Update the part title
//  3. Add an attachment (triggers trg_FIL_part_count → PN.AttachmentCount = 1)
//  4. Soft-delete the attachment (trigger → PN.AttachmentCount = 0)
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
		"FILPNRev":    {"A"},
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
		t.Fatalf("SELECT AttachmentCount after attach create: %v", err)
	}
	if filLinks != 1 {
		t.Errorf("AttachmentCount after attach create = %d, want 1", filLinks)
	}

	// Capture the new ID for the delete step.
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
		t.Fatalf("SELECT AttachmentCount after attach delete: %v", err)
	}
	if filLinks != 0 {
		t.Errorf("AttachmentCount after attach delete = %d, want 0", filLinks)
	}
}

// TestIntegration_YieldSummary exercises RecordsYieldSummary's actual query
// (LEFT JOIN + GROUP BY over form_record/result, aliased to sidestep the
// "id" column existing on both tables) against a real ArxDev connection, since
// TestComputeYieldBuckets only covers the pure Go aggregation, not the SQL.
func TestIntegration_YieldSummary(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// form.part_number_id FKs part.id (#744), so the throwaway form needs a real part.
	var partID int
	partNumber := "ITEST-YS-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}

	// result.form_row_id FKs to form_row.id, so results need a real step to point at.
	var testID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type) OUTPUT INSERTED.id VALUES (@p1, 0)`, h.cfg.StepsTable()),
		formID).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}

	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_record_id IN (SELECT id FROM %s WHERE form_id=@p1)`,
			h.cfg.ResultsTable(), h.cfg.RecordsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}()

	// Three records: one all-pass, one with a failing result, one with no
	// results at all (should still count toward Total and Passed per the
	// "fail only if a step actually failed" rule).
	type seedRecord struct {
		date      string
		passFails []sql.NullBool // one result row per entry; nil = no results
	}
	seeds := []seedRecord{
		{"2026-03-01", []sql.NullBool{{Bool: true, Valid: true}, {Bool: true, Valid: true}}},
		{"2026-03-15", []sql.NullBool{{Bool: true, Valid: true}, {Bool: false, Valid: true}}},
		{"2026-04-01", nil},
	}
	for _, s := range seeds {
		var recordID int
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (form_id, record_date, serial_number, is_active)
			 OUTPUT INSERTED.id VALUES (@p1, @p2, '1', 1)`, h.cfg.RecordsTable()),
			formID, s.date).Scan(&recordID); err != nil {
			t.Fatalf("seed record: %v", err)
		}
		for _, pf := range s.passFails {
			if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
				`INSERT INTO %s (form_record_id, form_row_id, pass_fail) VALUES (@p1, @p2, @p3)`,
				h.cfg.ResultsTable()), recordID, testID, pf); err != nil {
				t.Fatalf("seed result: %v", err)
			}
		}
	}

	filters := parseRecordFilters(url.Values{})
	dateClause, dateArgs := filters.dateRangeClauses(2)
	args := append([]any{formID}, dateArgs...)

	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT record_date, MAX(CASE WHEN pass_fail = 0 THEN 1 ELSE 0 END)
		FROM %s trec
		LEFT JOIN %s res ON res.form_record_id = trec.id
		WHERE form_id = @p1 AND is_active = 1%s
		GROUP BY trec.id, record_date`,
		h.cfg.RecordsTable(), h.cfg.ResultsTable(), dateClause), args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var records []yieldRecord
	for rows.Next() {
		var rec yieldRecord
		var anyFail int
		if err := rows.Scan(&rec.RecordDate, &anyFail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rec.AnyFail = anyFail == 1
		records = append(records, rec)
	}

	total, monthly := computeYieldBuckets(records, true)
	if total.Total != 3 || total.Passed != 2 || total.Failed != 1 {
		t.Fatalf("total = %+v, want Total=3 Passed=2 Failed=1", total)
	}
	if len(monthly) != 2 {
		t.Fatalf("len(monthly) = %d, want 2 (March + April)", len(monthly))
	}
	if monthly[0].Label != "2026-03" || monthly[0].Total != 2 || monthly[0].Passed != 1 || monthly[0].Failed != 1 {
		t.Fatalf("monthly[0] = %+v, want March bucket Total=2 Passed=1 Failed=1", monthly[0])
	}
	if monthly[1].Label != "2026-04" || monthly[1].Total != 1 || monthly[1].Passed != 1 || monthly[1].Failed != 0 {
		t.Fatalf("monthly[1] = %+v, want April bucket Total=1 Passed=1 Failed=0", monthly[1])
	}
}

func TestIntegration_RecordFilters(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Seed one throwaway form. form.part_number_id FKs part.id (#744), so it needs a real part.
	var partID int
	partNumber := "ITEST-RF-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}

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
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
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
		clauses, fargs := f.whereClauses(h.dia(), 2)
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

	// form.part_number_id FKs part.id (#744), so the throwaway form needs a real part.
	var partID int
	partNumber := "ITEST-ASPF-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_record_id IN (SELECT id FROM %s WHERE form_id=@p1)`,
				h.cfg.ResultsTable(), h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.StepsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
		_, _ = h.DB().ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}()

	var testID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter, pf_type)
		 OUTPUT INSERTED.id VALUES (@p1, 0, 'Screenshot/File Panel Photo', 'attach')`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}

	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg.FormsTable()),
		strconv.Itoa(testID), formID); err != nil {
		t.Fatalf("set form test_order: %v", err)
	}

	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, serial_number, subject_part_number, subject_pn_description, test_order, comments, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'ITEST-587', '', '', @p2, '', 0, 1)`, h.cfg.RecordsTable()),
		formID, strconv.Itoa(testID),
	).Scan(&recordID); err != nil {
		t.Fatalf("seed form_record: %v", err)
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
		`SELECT result, pass_fail FROM %s WHERE form_record_id=@p1 AND form_row_id=@p2`,
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
		`SELECT result, pass_fail FROM %s WHERE form_record_id=@p1 AND form_row_id=@p2`,
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
		`INSERT INTO %s (form_id, serial_number, subject_part_number, subject_pn_description, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'ITEST-587-LOCKED', '', '', 1, 1)`, h.cfg.RecordsTable()), formID,
	).Scan(&recordID); err != nil {
		t.Fatalf("seed locked form_record: %v", err)
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

// TestIntegration_UserPODefaults verifies userByID scans the per-user PO defaults
// columns (#463): admin (8001) is seeded with receiver 1003 + contact 2005; tester
// (8002) has none (NULL → 0). Guards the new SELECT/scan and the seed alignment.
// Read-only — no cleanup.
func TestIntegration_UserPODefaults(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	admin, err := h.userByID(ctx, 8001)
	if err != nil || admin == nil {
		t.Fatalf("userByID(8001): %v", err)
	}
	if admin.DefaultPOReceiverID != 1003 || admin.DefaultPOContactID != 2005 {
		t.Errorf("admin PO defaults = receiver %d / contact %d, want 1003 / 2005 (ArxDev may need reseeding)",
			admin.DefaultPOReceiverID, admin.DefaultPOContactID)
	}

	tester, err := h.userByID(ctx, 8002)
	if err != nil || tester == nil {
		t.Fatalf("userByID(8002): %v", err)
	}
	if tester.DefaultPOReceiverID != 0 || tester.DefaultPOContactID != 0 {
		t.Errorf("tester PO defaults = receiver %d / contact %d, want 0 / 0 (NULL columns)",
			tester.DefaultPOReceiverID, tester.DefaultPOContactID)
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

	idA, _, cyclesCleanup := seedCyclePair(t, h, ctx)
	defer cyclesCleanup()

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

// TestIntegration_RollupCostNested covers both the flat-BOM case (3012, whose
// BOM has no sub-assemblies of its own) and the nested case (3005, which
// recurses into 3012) in one test against the static seed. Also exercises
// every leaf-cost fallback path: 3002's preferred_price is the MIN across all
// active price tiers for its default supplier (rollupCost's leaf-cost query is
// not qty-tier-aware, unlike buildCost — a legacy quirk, asserted here as
// documented behavior, not fixed), 3003/3007 fall back to current_cost (no
// price rows), and 3006 (labor, no default_supplier_id) also falls back to
// current_cost.
func TestIntegration_RollupCostNested(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	res12, err := h.rollupCost(ctx, 3012, map[int]bool{}, map[int]rollupResult{})
	if err != nil {
		t.Fatalf("rollupCost(3012): %v", err)
	}
	if res12.cycle {
		t.Error("rollupCost(3012): cycle = true, want false")
	}
	assertFloatEqual(t, "rollupCost(3012) cost", res12.cost, 9.19)

	res5, err := h.rollupCost(ctx, 3005, map[int]bool{}, map[int]rollupResult{})
	if err != nil {
		t.Fatalf("rollupCost(3005): %v", err)
	}
	if res5.cycle {
		t.Error("rollupCost(3005): cycle = true, want false")
	}
	assertFloatEqual(t, "rollupCost(3005) cost", res5.cost, 27.23)
}

// TestIntegration_RollupCostMemoization proves a shared memo map is reused
// rather than recomputed: pre-seeding memo[3012] with a sentinel value and
// rolling up 3005 (which references 3012 via line 3907) must return the
// sentinel unchanged instead of re-querying and recomputing 3012's real cost.
func TestIntegration_RollupCostMemoization(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	memo := map[int]rollupResult{3012: {cost: 999, cycle: false}}
	res, err := h.rollupCost(ctx, 3005, map[int]bool{}, memo)
	if err != nil {
		t.Fatalf("rollupCost(3005) with poisoned memo: %v", err)
	}
	// 0.06 (screw) + 0.48 (o-ring) + 17.50 (labor) + 999 (poisoned 3012) = 1017.04
	assertFloatEqual(t, "rollupCost(3005) with poisoned memo[3012]=999 (memo entry must be reused, not recomputed)", res.cost, 1017.04)
}

// TestIntegration_RollupCostCycleDetection mirrors
// TestIntegration_BuildCostCycleDetection but exercises rollupCost directly.
func TestIntegration_RollupCostCycleDetection(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	idA, _, cleanupCycle := seedCyclePair(t, h, ctx)
	defer cleanupCycle()

	res, err := h.rollupCost(ctx, idA, map[int]bool{}, map[int]rollupResult{})
	if err != nil {
		t.Fatalf("rollupCost(cycle): %v", err)
	}
	if !res.cycle {
		t.Error("rollupCost(cycle): cycle = false, want true for a self-referencing BOM")
	}
}

// TestIntegration_PartRollupCostHandler exercises the full HTTP handler
// (success path): computes the rollup for 3005 and asserts it writes
// last_rollup_cost/last_rollup_at back to every visited assembly (root 3005
// AND nested sub-assembly 3012) using one shared timestamp. Mutates static
// seed data — restores both parts' original values via defer.
func TestIntegration_PartRollupCostHandler(t *testing.T) {
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

	beforeCost5, beforeAt5 := readRollup(3005)
	beforeCost12, beforeAt12 := readRollup(3012)
	defer func() {
		if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET last_rollup_cost=@p1, last_rollup_at=@p2 WHERE id=@p3`, pn),
			beforeCost5, beforeAt5, 3005); err != nil {
			t.Errorf("restore part 3005 last_rollup_cost/at: %v", err)
		}
		if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET last_rollup_cost=@p1, last_rollup_at=@p2 WHERE id=@p3`, pn),
			beforeCost12, beforeAt12, 3012); err != nil {
			t.Errorf("restore part 3012 last_rollup_cost/at: %v", err)
		}
	}()

	req := withID(httptest.NewRequest(http.MethodPost, "/part/3005/rollup-cost", nil), 3005)
	rec := httptest.NewRecorder()
	h.PartRollupCost(rec, req)
	assertStatus(t, "PartRollupCost(3005)", rec, http.StatusSeeOther)

	afterCost5, afterAt5 := readRollup(3005)
	afterCost12, afterAt12 := readRollup(3012)

	if !afterCost5.Valid {
		t.Error("part 3005 last_rollup_cost after = NULL, want a value")
	} else {
		assertFloatEqual(t, "part 3005 last_rollup_cost after", afterCost5.Float64, 27.23)
	}
	if !afterCost12.Valid {
		t.Error("part 3012 last_rollup_cost after = NULL, want a value")
	} else {
		assertFloatEqual(t, "part 3012 last_rollup_cost after", afterCost12.Float64, 9.19)
	}
	if !afterAt5.Valid || !afterAt12.Valid {
		t.Fatalf("last_rollup_at not set: 3005=%v 3012=%v", afterAt5, afterAt12)
	}
	if afterAt5.Time != afterAt12.Time {
		t.Errorf("last_rollup_at differs between 3005 (%v) and 3012 (%v), want a single shared timestamp", afterAt5.Time, afterAt12.Time)
	}
	if afterAt5.Time.Equal(beforeAt5.Time) {
		t.Error("last_rollup_at for 3005 did not change")
	}
}

// TestIntegration_PartRollupCostHandlerCycleDoesNotWrite verifies the handler
// rejects a cyclic BOM without writing anything — the transaction must never
// commit when rollupCost reports a cycle.
func TestIntegration_PartRollupCostHandlerCycleDoesNotWrite(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	pn := h.cfg.PartsTable()

	idA, idB, cleanupCycle := seedCyclePair(t, h, ctx)
	defer cleanupCycle()

	readRollup := func(id int) sql.NullFloat64 {
		var cost sql.NullFloat64
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT last_rollup_cost FROM %s WHERE id=@p1`, pn), id).Scan(&cost); err != nil {
			t.Fatalf("read last_rollup_cost for part %d: %v", id, err)
		}
		return cost
	}

	req := withID(httptest.NewRequest(http.MethodPost, fmt.Sprintf("/part/%d/rollup-cost", idA), nil), idA)
	rec := httptest.NewRecorder()
	h.PartRollupCost(rec, req)

	if !strings.Contains(rec.Body.String(), "BOM contains a cycle") {
		t.Errorf("PartRollupCost(cycle): body = %q, want it to mention \"BOM contains a cycle\"", rec.Body.String())
	}

	if costA := readRollup(idA); costA.Valid {
		t.Errorf("part %d last_rollup_cost = %v after cycle rejection, want still NULL — transaction must not have committed", idA, costA)
	}
	if costB := readRollup(idB); costB.Valid {
		t.Errorf("part %d last_rollup_cost = %v after cycle rejection, want still NULL — transaction must not have committed", idB, costB)
	}
}

// TestIntegration_BuildCostNonAssemblyPart verifies calling build-cost on a part
// with no BOM (3004, MFG-1001, category MFG — BOM tab always applies for MFG but
// this part has zero BOM lines) returns zero consolidated lines rather than
// erroring — and that the handler renders the same "No BOM data found" message
// the BOM tab uses for the same case. (#675's category-tab gating blocks the BOM
// subtab for BUY parts, so the part used here must be in a category where the
// BOM tab is always visible — see #689.)
func TestIntegration_BuildCostNonAssemblyPart(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	res, err := h.buildCost(ctx, 3004, 10)
	if err != nil {
		t.Fatalf("buildCost(3004, 10): %v", err)
	}
	if len(res.Lines) != 0 {
		t.Errorf("buildCost(3004, 10): got %d lines, want 0 (3004 has no BOM)", len(res.Lines))
	}

	req := withID(httptest.NewRequest(http.MethodGet, "/part/3004/build-cost?qty=10", nil), 3004)
	rec := httptest.NewRecorder()
	h.PartBuildCost(rec, req)
	if !strings.Contains(rec.Body.String(), "No BOM data found") {
		t.Error("PartBuildCost(3004): expected \"No BOM data found\" message for a non-assembly part")
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

// TestIntegration_RouteRoundTrips profiles a curated set of representative GET pages
// against ArxDev, logging SQL round-trip count and DB/total timing per route (#613).
// Calls handlers directly (bypassing buildRouter/RequireAuth/CSRF), so it seeds the
// sqlStats counter into the request context itself rather than relying on profileRequest.
func TestIntegration_RouteRoundTrips(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// Seed IDs from SQL/seed_test_data.sql: 3005 Widget Assembly (has BOM/orders),
	// 3002 M3x8 SHCS (has price history), company 1001 Acme Fasteners, PO 5002,
	// form 6001 (has test records spanning multiple months).
	const seedPartID, seedPriceHistoryPartID, seedSupplierID, seedPOID, seedFormID = 3005, 3002, 1001, 5002, 6001

	cases := []struct {
		name     string
		fn       http.HandlerFunc
		target   string
		id       int    // 0 = no {id} route param
		wantBody string // substring rec.Body must contain for a correct render
	}{
		{"parts list", h.PartsList, "/parts", 0, `id="parts-table"`},
		// "part detail" passes the literal chi pattern "/part/{id}" as the request URL,
		// so r.URL.Path never equals "/part/3005" and PartDetail's BOM-redirect guard
		// is intentionally skipped. This profiles the full non-BOM render path — a real
		// request for 3005 (which has a BOM) would redirect after 2 queries instead.
		{"part detail", h.PartDetail, "/part/{id}", seedPartID, "ASM-1001"},
		{"part BOM", h.PartBOM, "/part/{id}/bom", seedPartID, "ASM-1001"},
		{"part build-cost", h.PartBuildCost, "/part/{id}/build-cost?qty=1", seedPartID, "ASM-1001"},
		{"part price-history", h.PartPriceHistory, "/part/{id}/price-history", seedPriceHistoryPartID, "BUY-1001"},
		{"part orders", h.PartOrders, "/part/{id}/orders", seedPartID, "ASM-1001"},
		{"suppliers list", h.SuppliersList, "/suppliers", 0, `data-rows-url="/api/suppliers/rows"`},
		{"supplier detail", h.SupplierDetail, "/supplier/{id}", seedSupplierID, "Acme Fasteners"},
		{"PO list", h.POList, "/pos", 0, `data-rows-url="/api/pos/rows"`},
		{"PO detail", h.PODetail, "/po/{id}", seedPOID, "PO #5002"},
		{"contacts list", h.ContactsList, "/contacts", 0, `data-rows-url="/api/contacts/rows"`},
		{"records/forms list", h.FormsList, "/records", 0, "FORM-1001"},
		{"records yield summary", h.RecordsYieldSummary, "/forms/{id}/yield", seedFormID, "FORM-1001"},
		{"reports yield picker", h.ReportsYieldPicker, "/reports/yield", 0, "FORM-1001"},
	}

	for _, c := range cases {
		st := &sqlStats{}
		req := httptest.NewRequest(http.MethodGet, c.target, nil)
		if c.id != 0 {
			req = withID(req, c.id)
		}
		req = req.WithContext(context.WithValue(req.Context(), ctxSQLStatsKey, st))
		rec := httptest.NewRecorder()
		start := time.Now()
		c.fn(rec, req)
		t.Logf("[PROFILE] %-20s %-28s %3d round trips  %8s  (status %d)",
			c.name, c.target, st.count, time.Since(start).Round(time.Millisecond), rec.Code)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: got status %d, want 200. body: %s", c.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s: body missing expected marker %q", c.name, c.wantBody)
		}
	}
}

// seedThrowawayPO creates its own PO via POCreate rather than reusing a shared
// seeded row, and returns its id, its (purely numeric) number, and a cleanup
// that hard-deletes it plus its child rows. POFolderRoot is blanked for the
// call so POCreate's createPOFolder no-ops instead of touching real disk.
func seedThrowawayPO(t *testing.T, h *Handler, ctx context.Context) (id int, number string, cleanup func()) {
	t.Helper()

	savedRoot := h.cfg.POFolderRoot
	h.cfg.POFolderRoot = ""
	defer func() { h.cfg.POFolderRoot = savedRoot }()

	rec := httptest.NewRecorder()
	h.POCreate(rec, postForm("/pos", url.Values{
		"supplier_id": {"1001"}, // seeded supplier Acme Fasteners
	}))
	loc := rec.Header().Get("Location")
	number = strings.TrimSuffix(strings.TrimPrefix(loc, "/po/"), "?suggest_links=1")
	if number == "" || number == loc {
		t.Fatalf("could not parse PO number from Location %q", loc)
	}

	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT ID FROM %s WHERE number=@p1", h.cfg.POTable()), number,
	).Scan(&id); err != nil {
		t.Fatalf("look up created PO id: %v", err)
	}

	cleanup = func() {
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_id=@p1", h.cfg.POLineTable()), id)
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_id=@p1", h.cfg.POHistoryTable()), id)
		smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE ID=@p1", h.cfg.POTable()), id)
	}
	return id, number, cleanup
}

// TestIntegration_POUpdate_BlankNewLineNotSaved guards against a whitespace-only
// new PO line getting inserted: extractPolRows trims each field, so a row where
// every field is just spaces must still collapse to blank and be skipped, the
// same as a row with no input at all (#639).
func TestIntegration_POUpdate_BlankNewLineNotSaved(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx)
	defer poCleanup()

	before := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d", h.cfg.POLineTable(), poID))

	numID, err := strconv.Atoi(poNumber)
	if err != nil {
		t.Fatalf("PO number %q is not numeric: %v", poNumber, err)
	}
	rec := httptest.NewRecorder()
	h.POUpdate(rec, withID(postForm("/po/{id}", url.Values{
		"supplier_id":                 {"1001"},
		"new_pol[0][POLPNPartNumber]": {"   "},
		"new_pol[0][POLDesc]":         {"   "},
		"new_pol[0][VendorPN]":        {"   "},
		"new_pol[0][POLQty]":          {"   "},
		"new_pol[0][POLCost]":         {"   "},
	}), numID))
	assert302(t, "POUpdate blank line", rec)

	after := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d", h.cfg.POLineTable(), poID))
	if after != before {
		t.Errorf("po_line count for PO %d changed from %d to %d; whitespace-only new line should not be saved", poID, before, after)
	}
}

// TestIntegration_DashboardStaleWIPRecords verifies the Reports dashboard's
// Stale WIP Records card (#658, RPT-7) against the pinned seed data: record
// 7001 is unlocked (WIP) with created_at pinned to 2026-06-01, well past
// staleWIPThresholdDays, so it must be returned; the other seeded records are
// all locked and must not appear. Read-only — no cleanup.
func TestIntegration_DashboardStaleWIPRecords(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	items, err := h.dashboardStaleWIPRecords(ctx, 10)
	if err != nil {
		t.Fatalf("dashboardStaleWIPRecords: %v", err)
	}

	var found *dashboardStaleWIPItem
	for i := range items {
		if items[i].RecordID == 7001 {
			found = &items[i]
		}
		if items[i].RecordID != 7001 && items[i].RecordID >= 7001 && items[i].RecordID <= 7009 {
			t.Errorf("dashboardStaleWIPRecords: seeded locked record %d should not appear as stale WIP", items[i].RecordID)
		}
	}
	if found == nil {
		t.Fatalf("dashboardStaleWIPRecords: seed record 7001 not found (ArxDev may need reseeding): %+v", items)
	}
	if found.PartNumber != "FORM-1001" {
		t.Errorf("dashboardStaleWIPRecords: record 7001 PartNumber = %q, want FORM-1001 (the form's part, matching dashboardTopFailureModes/dashboardLowestYieldForms convention)", found.PartNumber)
	}
	if found.AgeDays < staleWIPThresholdDays {
		t.Errorf("dashboardStaleWIPRecords: record 7001 AgeDays = %d, want >= %d", found.AgeDays, staleWIPThresholdDays)
	}
}

// TestIntegration_DashboardPendingApprovalPOs verifies the Reports dashboard's
// POs Pending Approval card (#658, RPT-7) against the pinned seed data: PO
// 5009 sits in approval_status='pending' with a 'submitted' history event
// dated 2026-06-20, so it must be returned with a non-zero age. Read-only —
// no cleanup.
func TestIntegration_DashboardPendingApprovalPOs(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	items, err := h.dashboardPendingApprovalPOs(ctx, 10)
	if err != nil {
		t.Fatalf("dashboardPendingApprovalPOs: %v", err)
	}

	var found *dashboardPendingApprovalItem
	for i := range items {
		if items[i].Number == "5009" {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatalf("dashboardPendingApprovalPOs: seed PO 5009 not found (ArxDev may need reseeding): %+v", items)
	}
	if found.AgeDays <= 0 {
		t.Errorf("dashboardPendingApprovalPOs: PO 5009 AgeDays = %d, want > 0 (submitted 2026-06-20)", found.AgeDays)
	}
}

// TestIntegration_DashboardBelowReorderParts verifies the Reports dashboard's
// Below Reorder Point card (#273, INV-2) against the pinned seed data: part 3007
// has stock_on_hand 16 and reorder_min 25, so it must be returned. Read-only —
// no cleanup.
func TestIntegration_DashboardBelowReorderParts(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	items, err := h.dashboardBelowReorderParts(ctx, 50)
	if err != nil {
		t.Fatalf("dashboardBelowReorderParts: %v", err)
	}

	var found *dashboardBelowReorderItem
	for i := range items {
		if items[i].PartID == 3007 {
			found = &items[i]
		}
		if items[i].StockOnHand >= items[i].ReorderMin {
			t.Errorf("dashboardBelowReorderParts: part %d returned with on-hand %g >= min %g",
				items[i].PartID, items[i].StockOnHand, items[i].ReorderMin)
		}
	}
	if found == nil {
		t.Fatalf("dashboardBelowReorderParts: seed part 3007 not found (ArxDev may need reseeding): %+v", items)
	}
	if found.StockOnHand != 16 || found.ReorderMin != 25 {
		t.Errorf("dashboardBelowReorderParts: part 3007 = {on-hand %g, min %g}, want {16, 25} (ArxDev may need reseeding)",
			found.StockOnHand, found.ReorderMin)
	}
}

// TestIntegration_ReportsDashboard_RendersAllCards exercises ReportsDashboard's
// page-handler assembly — the actual page handler that assembles all dashboard
// cards and renders the template — which no test previously invoked, even
// though 3 of its underlying card queries are individually integration-tested
// (#816).
func TestIntegration_ReportsDashboard_RendersAllCards(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.ReportsDashboard(rec, httptest.NewRequest(http.MethodGet, "/reports", nil))

	assertStatus(t, "ReportsDashboard", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{
		"Open POs",
		"POs Received This Month",
		"Recent Activity",
		"Top Failing Steps",
		"Lowest Yield Forms",
		"Stale WIP Records",
		"POs Pending Approval",
		"Below Reorder Point",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ReportsDashboard: body missing %q", want)
		}
	}
}

// TestIntegration_TestDefinitionHistoryAudit exercises the audit path end to end:
// dialect.SetAuditUser stashes the acting user where the trg_form_row_history
// trigger can read it, so an UPDATE to a form_row row produces a snapshot
// attributed to that user. It drives the SetAuditUser + UPDATE through the tx
// wrapper (the same beginTx → SetAuditUser → UPDATE order SaveFormDef/ArchiveStep
// use), so it validates that contract on whichever engine ArxDev runs — SQL Server
// today (CONTEXT_INFO), Postgres after the ArxDev cutover (arx.username session GUC).
func TestIntegration_TestDefinitionHistoryAudit(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Seed a throwaway form + one step to mutate.
	// form.part_number_id FKs part.id (#744), so the throwaway form needs a real part.
	var partID int
	partNumber := "ITEST-TDHA-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	var testID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter) OUTPUT INSERTED.id VALUES (@p1, 0, 'Audit Seed')`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_row_id=@p1`, h.cfg.FormRowHistoryTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}()

	// Set the acting user, then UPDATE the step — both inside one tx, so the
	// trigger sees the user and fires. Mirrors SaveFormDef/ArchiveStep exactly.
	const actor = "itest-auditor"
	tx, err := h.beginTx(ctx)
	if err != nil {
		t.Fatalf("beginTx: %v", err)
	}
	q, arg := h.dia().SetAuditUser(actor)
	if _, err := tx.ExecContext(ctx, q, arg); err != nil {
		tx.Rollback()
		t.Fatalf("SetAuditUser exec: %v", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET parameter=@p1 WHERE id=@p2`, h.cfg.StepsTable()), "Audit Changed", testID); err != nil {
		tx.Rollback()
		t.Fatalf("update step: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Exactly one snapshot row, holding the PRE-update value and attributed to actor.
	var count int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE form_row_id=@p1`, h.cfg.FormRowHistoryTable()), testID,
	).Scan(&count); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if count != 1 {
		t.Fatalf("history rows for test %d = %d, want 1 (trigger should fire once per UPDATE)", testID, count)
	}

	var changedBy, snapParam string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT changed_by, parameter FROM %s WHERE form_row_id=@p1`, h.cfg.FormRowHistoryTable()), testID,
	).Scan(&changedBy, &snapParam); err != nil {
		t.Fatalf("select history: %v", err)
	}
	if changedBy != actor {
		t.Errorf("history changed_by = %q, want %q (SetAuditUser → trigger attribution)", changedBy, actor)
	}
	if snapParam != "Audit Seed" {
		t.Errorf("history parameter = %q, want pre-update value %q", snapParam, "Audit Seed")
	}
}

// TestIntegration_BuildConsumesOnlyStockedComponents drives PartBuildCreate for
// the seeded ASM part 3005 and verifies the #675 consume/produce rules against a
// live DB: the build posts an inventory 'issue' for each inventory-tracked BOM
// component (screws 3002, o-ring 3003, sub-assembly 3012), SKIPS the non-stocked
// OPS labor line (3006), and posts a single 'receipt' for the output part — with
// part.stock_on_hand reflecting both sides. Cleans up every row it writes and
// recomputes the affected parts' cached balances from the ledger.
func TestIntegration_BuildConsumesOnlyStockedComponents(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	inv := h.cfg.InventoryTxnTable()
	bt := h.cfg.BuildTable()
	pn := h.cfg.PartsTable()

	const outputPart = 3005
	const buildQty = 3.0

	stockOf := func(id int) float64 {
		var s float64
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT stock_on_hand FROM %s WHERE id = @p1`, pn), id).Scan(&s); err != nil {
			t.Fatalf("read stock_on_hand for %d: %v", id, err)
		}
		return s
	}
	before3005 := stockOf(outputPart)
	before3002 := stockOf(3002)
	before3006 := stockOf(3006)

	// Registered before the write so a mid-test failure still cleans up. buildID is
	// filled in after the POST; 0 means no build was created (nothing to undo).
	var buildID int
	defer func() {
		if buildID == 0 {
			return
		}
		note := fmt.Sprintf("Build #%d", buildID)
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE note = @p1`, inv), note)
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, bt), buildID)
		for _, id := range []int{3002, 3003, 3006, 3012, outputPart} {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(
				`UPDATE %s SET stock_on_hand = (SELECT ISNULL(SUM(qty),0) FROM %s WHERE part_id = @p1) WHERE id = @p1`,
				pn, inv), id)
		}
	}()

	// Component 3012 is lot-tracked (seed), so building it requires a lot pick even
	// though the output 3005 is not lot-tracked — the pick is recorded on the issue
	// row. 8302 is 3012's seed lot.
	req := withID(postForm("/part/3005/build", url.Values{"qty": {"3"}, "lot[3012]": {"8302"}}), outputPart)
	rec := httptest.NewRecorder()
	h.PartBuildCreate(rec, req)
	assert302(t, "PartBuildCreate", rec)

	// The new build is the highest-id row for this part (seed row 8201 is lower).
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT MAX(id) FROM %s WHERE part_id = @p1`, bt), outputPart).Scan(&buildID); err != nil {
		t.Fatalf("find build id: %v", err)
	}
	note := fmt.Sprintf("Build #%d", buildID)

	// Collect the ledger rows this build posted, keyed by part.
	rows, err := h.DB().QueryContext(ctx, fmt.Sprintf(
		`SELECT part_id, txn_type, qty FROM %s WHERE note = @p1`, inv), note)
	if err != nil {
		t.Fatalf("query build ledger rows: %v", err)
	}
	type txn struct {
		typ string
		qty float64
	}
	got := map[int]txn{}
	for rows.Next() {
		var pid int
		var tt string
		var q float64
		if err := rows.Scan(&pid, &tt, &q); err != nil {
			rows.Close()
			t.Fatalf("scan ledger row: %v", err)
		}
		if _, dup := got[pid]; dup {
			t.Errorf("part %d has more than one ledger row for this build", pid)
		}
		got[pid] = txn{tt, q}
	}
	rows.Close()

	// Stocked components: issued at (qty per assembly × build qty), negative.
	for pid, wantQty := range map[int]float64{3002: -2 * buildQty, 3003: -4 * buildQty, 3012: -1 * buildQty} {
		g, ok := got[pid]
		if !ok {
			t.Errorf("no ledger row for stocked component %d (ArxDev may need reseeding)", pid)
			continue
		}
		if g.typ != "issue" || g.qty != wantQty {
			t.Errorf("component %d: got {%s %v}, want {issue %v}", pid, g.typ, g.qty, wantQty)
		}
	}

	// The OPS labor line (category Inventory off) must be skipped entirely.
	if g, ok := got[3006]; ok {
		t.Errorf("OPS labor part 3006 should be skipped, but got a %s row of %v", g.typ, g.qty)
	}

	// Output part gets exactly one receipt.
	if g, ok := got[outputPart]; !ok {
		t.Errorf("no receipt row for output part %d", outputPart)
	} else if g.typ != "receipt" || g.qty != buildQty {
		t.Errorf("output %d: got {%s %v}, want {receipt %v}", outputPart, g.typ, g.qty, buildQty)
	}

	// stock_on_hand reflects both sides; the skipped OPS part is unchanged.
	if d := stockOf(outputPart) - before3005; d != buildQty {
		t.Errorf("output stock delta = %v, want %v", d, buildQty)
	}
	if d := stockOf(3002) - before3002; d != -2*buildQty {
		t.Errorf("component 3002 stock delta = %v, want %v", d, -2*buildQty)
	}
	if d := stockOf(3006) - before3006; d != 0 {
		t.Errorf("OPS 3006 stock delta = %v, want 0 (skipped)", d)
	}
}

// TestIntegration_BuildLotGenealogy exercises the #676 lot-control build path against
// a live DB. Building the lot-tracked sub-assembly 3012 (seed) must create an output
// lot linked from build.output_lot_id and write exactly one genealogy edge from
// the picked component lot (3007's seed lot 8301) to that output lot, with
// qty_consumed = bom qty × build qty. Cleans up every row it writes (genealogy edge,
// output lot, build, ledger rows) and recomputes the affected parts' cached balances.
func TestIntegration_BuildLotGenealogy(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	inv := h.cfg.InventoryTxnTable()
	bt := h.cfg.BuildTable()
	lt := h.cfg.LotTable()
	lg := h.cfg.GenealogyTable()
	pn := h.cfg.PartsTable()

	const outputPart = 3012  // ASM-1002 sub-assembly, is_lot_tracked in seed
	const trackedComp = 3007 // RAW-1002 component, is_lot_tracked; seed lot 8301
	const compLot = 8301     // 3007's active seed lot
	const buildQty = 2.0

	var buildID, outputLotID int
	defer func() {
		if buildID == 0 {
			return
		}
		// Recover the output lot id if a mid-test failure skipped its capture, so the
		// row is still cleaned up. Delete order respects the FKs: genealogy and the
		// ledger rows (whose lot_id references the output lot) and the build (whose
		// output_lot_id references it) all go before the output lot itself.
		if outputLotID == 0 {
			_ = h.DB().QueryRowContext(ctx, fmt.Sprintf(
				`SELECT ISNULL(output_lot_id, 0) FROM %s WHERE id = @p1`, bt), buildID).Scan(&outputLotID)
		}
		if outputLotID != 0 {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE child_lot_id = @p1`, lg), outputLotID)
		}
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE note = @p1`, inv), fmt.Sprintf("Build #%d", buildID))
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, bt), buildID)
		if outputLotID != 0 {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, lt), outputLotID)
		}
		for _, id := range []int{3001, trackedComp, 3002, outputPart} {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(
				`UPDATE %s SET stock_on_hand = (SELECT ISNULL(SUM(qty),0) FROM %s WHERE part_id = @p1) WHERE id = @p1`,
				pn, inv), id)
		}
	}()

	req := withID(postForm("/part/3012/build", url.Values{
		"qty":                               {"2"},
		fmt.Sprintf("lot[%d]", trackedComp): {strconv.Itoa(compLot)},
	}), outputPart)
	rec := httptest.NewRecorder()
	h.PartBuildCreate(rec, req)
	assert302(t, "PartBuildCreate(3012)", rec)

	// New build is the highest-id build row for 3012 (seed has none for it).
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT MAX(id) FROM %s WHERE part_id = @p1`, bt), outputPart).Scan(&buildID); err != nil {
		t.Fatalf("find build id: %v", err)
	}

	// build.output_lot_id must point at a lot of the output part.
	var lotPart int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT b.output_lot_id, l.part_id FROM %s b JOIN %s l ON b.output_lot_id = l.id WHERE b.id = @p1`,
		bt, lt), buildID).Scan(&outputLotID, &lotPart); err != nil {
		t.Fatalf("build output lot not set (ArxDev may need the lot schema + reseed): %v", err)
	}
	if lotPart != outputPart {
		t.Errorf("output lot part_id = %d, want %d", lotPart, outputPart)
	}

	// Exactly one genealogy edge into the output lot, from the picked component lot.
	var n int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE child_lot_id = @p1`, lg), outputLotID).Scan(&n); err != nil {
		t.Fatalf("count genealogy: %v", err)
	}
	if n != 1 {
		t.Fatalf("genealogy edges into output lot = %d, want 1 (ArxDev may need reseeding)", n)
	}
	var parent int
	var qtyConsumed float64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT parent_lot_id, qty_consumed FROM %s WHERE child_lot_id = @p1`, lg), outputLotID).Scan(&parent, &qtyConsumed); err != nil {
		t.Fatalf("read genealogy: %v", err)
	}
	if parent != compLot {
		t.Errorf("genealogy parent_lot_id = %d, want %d (3007's seed lot 8301)", parent, compLot)
	}
	if qtyConsumed != 1*buildQty { // bom line 3906: 1x 3007 per 3012
		t.Errorf("genealogy qty_consumed = %v, want %v", qtyConsumed, 1*buildQty)
	}

	// The output part still receives its produced stock, and the receipt row carries
	// the output lot (#676 lot-aware ledger).
	note := fmt.Sprintf("Build #%d", buildID)
	var recv float64
	var recvLot int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT ISNULL(SUM(qty),0), ISNULL(MAX(lot_id),0) FROM %s WHERE note = @p1 AND part_id = @p2 AND txn_type = 'receipt'`,
		inv), note, outputPart).Scan(&recv, &recvLot); err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if recv != buildQty {
		t.Errorf("output receipt qty = %v, want %v", recv, buildQty)
	}
	if recvLot != outputLotID {
		t.Errorf("output receipt lot_id = %d, want %d (the output lot)", recvLot, outputLotID)
	}

	// The lot-tracked component's issue row records the consumed lot (#676).
	var issueLot int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT ISNULL(MAX(lot_id),0) FROM %s WHERE note = @p1 AND part_id = @p2 AND txn_type = 'issue'`,
		inv), note, trackedComp).Scan(&issueLot); err != nil {
		t.Fatalf("read component issue: %v", err)
	}
	if issueLot != compLot {
		t.Errorf("component %d issue lot_id = %d, want %d (the consumed lot)", trackedComp, issueLot, compLot)
	}

	// The generalized genealogy walk (lot.go genealogyTrace) resolves the output lot
	// back to its raw vendor lot — the #676 "trace an output lot back to raw vendor
	// lots" acceptance criterion. Seed lot 8301 has a po_line_id, so it flags as vendor.
	ancestors, err := h.genealogyTrace(ctx, outputLotID, "lot", true)
	if err != nil {
		t.Fatalf("genealogyTrace ancestors: %v", err)
	}
	foundVendorLot := false
	for _, a := range ancestors {
		if a.NodeType == "lot" && a.ID == compLot {
			foundVendorLot = true
			if !a.IsVendorLot {
				t.Errorf("traced ancestor lot %d not flagged as a raw vendor lot", compLot)
			}
		}
	}
	if !foundVendorLot {
		t.Errorf("genealogyTrace(%d) ancestors did not resolve back to raw vendor lot %d", outputLotID, compLot)
	}
}

// TestIntegration_BuildReturnsToRecord exercises the "Build this unit" round-trip
// (#677): POSTing a build with a return_record links that WIP test record to the new
// build and its output lot, redirects back to the record's editor with ?built=1, and
// stamps the build's ledger rows with build_id.
func TestIntegration_BuildReturnsToRecord(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	rt := h.cfg.RecordsTable()
	bt := h.cfg.BuildTable()
	lt := h.cfg.LotTable()
	lg := h.cfg.GenealogyTable()
	inv := h.cfg.InventoryTxnTable()
	pn := h.cfg.PartsTable()

	const outputPart = 3012  // ASM-1002, is_lot_tracked + has a BOM in seed
	const trackedComp = 3007 // lot-tracked component; seed lot 8301
	const compLot = 8301

	// Throwaway WIP record whose tested part is the buildable output part.
	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, part_id, serial_number, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (6001, @p1, @p2, 0, 1)`, rt),
		outputPart, smokeUniq("BRR")).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	var buildID, outputLotID int
	defer func() {
		// The record's FKs (lot_id/build_id) reference the build+lot, so it goes first.
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, rt), recordID)
		if buildID != 0 {
			note := fmt.Sprintf("Build #%d", buildID)
			if outputLotID == 0 {
				_ = h.DB().QueryRowContext(ctx, fmt.Sprintf(
					`SELECT ISNULL(output_lot_id,0) FROM %s WHERE id = @p1`, bt), buildID).Scan(&outputLotID)
			}
			if outputLotID != 0 {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE child_lot_id = @p1`, lg), outputLotID)
			}
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE note = @p1`, inv), note)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, bt), buildID)
			if outputLotID != 0 {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, lt), outputLotID)
			}
			for _, id := range []int{3001, trackedComp, 3002, outputPart} {
				smokeExec(ctx, h, fmt.Sprintf(
					`UPDATE %s SET stock_on_hand = (SELECT ISNULL(SUM(qty),0) FROM %s WHERE part_id = @p1) WHERE id = @p1`,
					pn, inv), id)
			}
		}
	}()

	req := withID(postForm(fmt.Sprintf("/part/%d/build", outputPart), url.Values{
		"qty":                               {"1"},
		"return_record":                     {strconv.Itoa(recordID)},
		fmt.Sprintf("lot[%d]", trackedComp): {strconv.Itoa(compLot)},
	}), outputPart)
	rec := httptest.NewRecorder()
	h.PartBuildCreate(rec, req)
	assert302(t, "PartBuildCreate(return_record)", rec)

	// Redirect lands back on the record's editor with the auto-restore marker.
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/records/%d/edit?built=1", recordID) {
		t.Errorf("redirect Location = %q, want /records/%d/edit?built=1", loc, recordID)
	}

	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT MAX(id) FROM %s WHERE part_id = @p1`, bt), outputPart).Scan(&buildID); err != nil {
		t.Fatalf("find build id: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT ISNULL(output_lot_id,0) FROM %s WHERE id = @p1`, bt), buildID).Scan(&outputLotID); err != nil {
		t.Fatalf("read output lot: %v", err)
	}
	if outputLotID == 0 {
		t.Fatal("build produced no output lot (ArxDev may need lot schema + reseed)")
	}

	// The record is now linked to the build and its output lot.
	var gotLot, gotBuild int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT ISNULL(lot_id,0), ISNULL(build_id,0) FROM %s WHERE id = @p1`, rt), recordID).
		Scan(&gotLot, &gotBuild); err != nil {
		t.Fatalf("read record linkage (ArxDev may need the #677 migration): %v", err)
	}
	if gotBuild != buildID {
		t.Errorf("record build_id = %d, want %d", gotBuild, buildID)
	}
	if gotLot != outputLotID {
		t.Errorf("record lot_id = %d, want %d (the output lot)", gotLot, outputLotID)
	}

	// The build's ledger rows carry build_id (#677) — the FK that replaces note-matching.
	var stamped, total int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT SUM(CASE WHEN build_id = @p1 THEN 1 ELSE 0 END), COUNT(*) FROM %s WHERE note = @p2`,
		inv), buildID, fmt.Sprintf("Build #%d", buildID)).Scan(&stamped, &total); err != nil {
		t.Fatalf("read ledger build_id: %v", err)
	}
	if total == 0 || stamped != total {
		t.Errorf("ledger rows with build_id = %d/%d, want all %d stamped", stamped, total, total)
	}
}

// TestIntegration_RecordLinkageSave exercises saving lot/build linkage through the main
// record editor Save (#677): a valid lot/build for the record's part persists, and a
// selection belonging to a different part is rejected without altering the record.
func TestIntegration_RecordLinkageSave(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	rt := h.cfg.RecordsTable()

	const testedPart = 3012 // ASM-1002; seed lot 8302 + build 8202 belong to it
	const goodLot = 8302
	const goodBuild = 8202
	const foreignLot = 8301 // belongs to part 3007, not 3012

	// The string columns are set non-NULL because SaveResults scans them as plain
	// strings (real records always carry these snapshots).
	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, part_id, serial_number, subject_part_number, subject_pn_description, comments, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (6001, @p1, @p2, 'ASM-1002', 'Sub-Assembly', '', '', 0, 1)`, rt),
		testedPart, smokeUniq("RLS")).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	defer smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, rt), recordID)

	// Valid linkage for the record's own part → persisted by SaveResults.
	req := withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{
		"lot_id":   {strconv.Itoa(goodLot)},
		"build_id": {strconv.Itoa(goodBuild)},
	}), recordID)
	rec := httptest.NewRecorder()
	h.SaveResults(rec, req)
	assertStatus(t, "SaveResults(valid linkage)", rec, http.StatusSeeOther)

	var gotLot, gotBuild int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT ISNULL(lot_id,0), ISNULL(build_id,0) FROM %s WHERE id = @p1`, rt), recordID).
		Scan(&gotLot, &gotBuild); err != nil {
		t.Fatalf("read linkage (ArxDev may need the #677 migration): %v", err)
	}
	if gotLot != goodLot || gotBuild != goodBuild {
		t.Fatalf("after save: lot_id=%d build_id=%d, want %d/%d", gotLot, gotBuild, goodLot, goodBuild)
	}

	// A lot belonging to a different part is rejected (400) and leaves the record intact.
	bad := withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{
		"lot_id": {strconv.Itoa(foreignLot)},
	}), recordID)
	badRec := httptest.NewRecorder()
	h.SaveResults(badRec, bad)
	assertStatus(t, "SaveResults(foreign lot)", badRec, http.StatusBadRequest)

	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT ISNULL(lot_id,0) FROM %s WHERE id = @p1`, rt), recordID).Scan(&gotLot); err != nil {
		t.Fatalf("re-read lot: %v", err)
	}
	if gotLot != goodLot {
		t.Errorf("rejected save changed lot_id to %d, want unchanged %d", gotLot, goodLot)
	}
}

// TestIntegration_SerialUnitCreationAndRetest verifies slice 8 (#745): saving a
// serial/lot_serial part's record mints a unit lazily with provenance from the
// picked lot/build, points the record at it via unit_id (leaving lot_id/build_id
// NULL per the Q8 FK-consistency invariant), and a retest — a second record with
// the same serial — re-links the SAME unit rather than creating a duplicate.
func TestIntegration_SerialUnitCreationAndRetest(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	rt := h.cfg.RecordsTable()
	ut := h.cfg.UnitTable()

	const testedPart = 3013 // ASM-1003, tracking_mode lot_serial in seed
	const provLot = 8306    // a lot of 3013
	const provBuild = 8203  // a build of 3013
	serial := smokeUniq("SN-IT")

	// Two records sharing one serial: the original test and a retest.
	mkRecord := func() int {
		var id int
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (form_id, part_id, serial_number, subject_part_number, subject_pn_description, comments, test_order, is_locked, is_active)
			 OUTPUT INSERTED.id VALUES (6001, @p1, @p2, 'ASM-1003', 'Widget Deluxe Assembly', '', '', 0, 1)`, rt),
			testedPart, serial).Scan(&id); err != nil {
			t.Fatalf("seed record: %v", err)
		}
		return id
	}
	rec1 := mkRecord()
	rec2 := mkRecord()
	var unitID int
	defer func() {
		// Records reference the unit via unit_id FK — delete them before the unit.
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id IN (@p1,@p2)`, rt), rec1, rec2)
		if unitID != 0 {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, ut), unitID)
		}
	}()

	save := func(recID int) {
		req := withID(postForm(fmt.Sprintf("/records/%d/edit", recID), url.Values{
			"lot_id":   {strconv.Itoa(provLot)},
			"build_id": {strconv.Itoa(provBuild)},
		}), recID)
		rr := httptest.NewRecorder()
		h.SaveResults(rr, req)
		assertStatus(t, "SaveResults(serial)", rr, http.StatusSeeOther)
	}
	save(rec1)

	// Record 1: unit_id set, lot_id/build_id NULL (Q8 — read through the unit).
	var gotUnit, gotLot, gotBuild sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT unit_id, lot_id, build_id FROM %s WHERE id = @p1`, rt), rec1).
		Scan(&gotUnit, &gotLot, &gotBuild); err != nil {
		t.Fatalf("read record 1 (ArxDev may need reseed): %v", err)
	}
	if !gotUnit.Valid {
		t.Fatal("record 1 unit_id not set after serial save")
	}
	if gotLot.Valid || gotBuild.Valid {
		t.Errorf("record 1 lot_id/build_id should be NULL (Q8), got lot=%v build=%v", gotLot, gotBuild)
	}
	unitID = int(gotUnit.Int64)

	// The unit carries the provenance and the record's serial.
	var uLot, uBuild sql.NullInt64
	var uSerial string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT lot_id, build_id, serial_number FROM %s WHERE id = @p1`, ut), unitID).
		Scan(&uLot, &uBuild, &uSerial); err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if uSerial != serial {
		t.Errorf("unit serial = %q, want %q", uSerial, serial)
	}
	if int(uLot.Int64) != provLot || int(uBuild.Int64) != provBuild {
		t.Errorf("unit provenance lot=%v build=%v, want %d/%d", uLot, uBuild, provLot, provBuild)
	}

	// Retest (record 2, same serial): re-links the SAME unit, no duplicate row.
	save(rec2)
	var gotUnit2 sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT unit_id FROM %s WHERE id = @p1`, rt), rec2).Scan(&gotUnit2); err != nil {
		t.Fatalf("read record 2: %v", err)
	}
	if int(gotUnit2.Int64) != unitID {
		t.Errorf("retest linked unit %d, want same unit %d", gotUnit2.Int64, unitID)
	}
	var unitCount int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE part_id = @p1 AND serial_number = @p2`, ut), testedPart, serial).
		Scan(&unitCount); err != nil {
		t.Fatalf("count units: %v", err)
	}
	if unitCount != 1 {
		t.Errorf("unit count for (part,serial) = %d, want 1 (retest must not duplicate)", unitCount)
	}
}

// TestIntegration_UnitGenealogyTrace verifies slice 9 (#746): the generalized walk
// resolves a serialized unit's mixed lot+unit ancestry from the genealogy edge table.
// Seed unit 8501 (top assembly 3013) has a unit parent (8503) and a lot parent (8301)
// via genealogy edges 8404/8405 — the walk must surface both, tagged by node type.
func TestIntegration_UnitGenealogyTrace(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const rootUnit = 8501 // serial of 3013, seeded with unit+lot parents
	const parentUnit = 8503
	const parentLot = 8301

	ancestors, err := h.genealogyTrace(ctx, rootUnit, "unit", true)
	if err != nil {
		t.Fatalf("genealogyTrace unit ancestors (ArxDev may need reseed): %v", err)
	}
	var sawUnitParent, sawLotParent bool
	for _, a := range ancestors {
		if a.NodeType == "unit" && a.ID == parentUnit {
			sawUnitParent = true
		}
		if a.NodeType == "lot" && a.ID == parentLot {
			sawLotParent = true
			if !a.IsVendorLot {
				t.Errorf("parent lot %d not flagged as vendor lot", parentLot)
			}
		}
	}
	if !sawUnitParent {
		t.Errorf("unit %d ancestry missing unit parent %d", rootUnit, parentUnit)
	}
	if !sawLotParent {
		t.Errorf("unit %d ancestry missing lot parent %d", rootUnit, parentLot)
	}
}

// TestIntegration_BuildAtTestTime verifies slice 10 (#747): saving a record with the
// inline build panel (build_panel=1) builds ONE unit of the part in the same
// transaction — consuming components, receiving output — and links the record to the
// resulting build via a minted unit (Q8: lot/build NULL on the record, carried on the
// unit). Part 3013 (lot_serial) consumes lot-tracked components 3012 (lot 8302) and
// 3007 (lot 8301) per its seed BOM.
func TestIntegration_BuildAtTestTime(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	rt := h.cfg.RecordsTable()
	ut := h.cfg.UnitTable()
	bt := h.cfg.BuildTable()
	lt := h.cfg.LotTable()
	lg := h.cfg.GenealogyTable()
	inv := h.cfg.InventoryTxnTable()
	pn := h.cfg.PartsTable()

	const testedPart = 3013
	const comp1, comp1Lot = 3012, 8302
	const comp2, comp2Lot = 3007, 8301
	const seedMaxBuild = 8203 // highest seeded build id for 3013
	serial := smokeUniq("BAT")

	var recordID, buildID, unitID, outputLotID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, part_id, serial_number, subject_part_number, subject_pn_description, comments, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (6001, @p1, @p2, 'ASM-1003', 'Widget Deluxe Assembly', '', '', 0, 1)`, rt),
		testedPart, serial).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	defer func() {
		// FK-ordered teardown: record (→unit) → unit → genealogy(→output lot) →
		// ledger(→build) → build → output lot; then restore consumed stock.
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, rt), recordID)
		if unitID != 0 {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, ut), unitID)
		}
		if buildID != 0 {
			if outputLotID == 0 {
				_ = h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT ISNULL(output_lot_id,0) FROM %s WHERE id=@p1`, bt), buildID).Scan(&outputLotID)
			}
			if outputLotID != 0 {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE child_lot_id = @p1`, lg), outputLotID)
			}
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE note = @p1`, inv), fmt.Sprintf("Build #%d", buildID))
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, bt), buildID)
			if outputLotID != 0 {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, lt), outputLotID)
			}
		}
		for _, id := range []int{comp1, comp2, testedPart} {
			smokeExec(ctx, h, fmt.Sprintf(
				`UPDATE %s SET stock_on_hand = (SELECT ISNULL(SUM(qty),0) FROM %s WHERE part_id=@p1) WHERE id=@p1`, pn, inv), id)
		}
	}()

	req := withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{
		"build_panel":                 {"1"},
		fmt.Sprintf("lot[%d]", comp1): {strconv.Itoa(comp1Lot)},
		fmt.Sprintf("lot[%d]", comp2): {strconv.Itoa(comp2Lot)},
	}), recordID)
	rec := httptest.NewRecorder()
	h.SaveResults(rec, req)
	assertStatus(t, "SaveResults(build_panel)", rec, http.StatusSeeOther)

	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT MAX(id) FROM %s WHERE part_id=@p1`, bt), testedPart).Scan(&buildID); err != nil {
		t.Fatalf("find build (ArxDev may need reseed): %v", err)
	}
	if buildID <= seedMaxBuild {
		t.Fatalf("no new build created for %d (max id %d)", testedPart, buildID)
	}
	_ = h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT ISNULL(output_lot_id,0) FROM %s WHERE id=@p1`, bt), buildID).Scan(&outputLotID)

	// Record points at a minted unit; lot/build NULL on the record (Q8).
	var gotUnit, gotLot, gotBuild sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT unit_id, lot_id, build_id FROM %s WHERE id=@p1`, rt), recordID).
		Scan(&gotUnit, &gotLot, &gotBuild); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !gotUnit.Valid {
		t.Fatal("record unit_id not set after build-at-test-time")
	}
	if gotLot.Valid || gotBuild.Valid {
		t.Errorf("record lot/build should be NULL (Q8), got lot=%v build=%v", gotLot, gotBuild)
	}
	unitID = int(gotUnit.Int64)

	// The minted unit carries the new build as provenance.
	var uBuild sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT build_id FROM %s WHERE id=@p1`, ut), unitID).Scan(&uBuild); err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if int(uBuild.Int64) != buildID {
		t.Errorf("unit build_id=%v, want new build %d", uBuild, buildID)
	}

	// Both lot-tracked components were issued against the build.
	var issues int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE note=@p1 AND qty < 0`, inv),
		fmt.Sprintf("Build #%d", buildID)).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if issues < 2 {
		t.Errorf("component issues for build = %d, want >= 2 (3012 + 3007)", issues)
	}
}

// TestIntegration_UserAdminRequiresAdmin verifies the user-management endpoints
// reject a non-admin session with 403 (issue #750 privilege-escalation gate) —
// before this fix they were reachable by any logged-in user. Handlers are called
// directly (no RequireAuth middleware), so the session user is injected on the
// request context exactly as RequireAuth would, and requireAdmin short-circuits
// with 403 before any DB write, so the test mutates nothing.
func TestIntegration_UserAdminRequiresAdmin(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	nonAdmin := &User{ID: 8002, Username: "tester"} // seed tester, is_admin = 0

	// userReq builds a POST carrying both the {userID} route param and the
	// non-admin session user on the context.
	userReq := func(target string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("userID", "8002")
		req := postForm(target, url.Values{})
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		return req.WithContext(context.WithValue(req.Context(), ctxUserKey, nonAdmin))
	}

	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"create", h.SettingsUsersCreate},
		{"reset-password", h.SettingsUsersResetPassword},
		{"toggle-active", h.SettingsUsersToggleActive},
		{"toggle-approve", h.SettingsUsersToggleApprove},
		{"toggle-approve-records", h.SettingsUsersToggleApproveRecords},
		{"toggle-admin", h.SettingsUsersToggleAdmin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, userReq("/settings/users"))
			assertStatus(t, tc.name+" non-admin", rec, http.StatusForbidden)
		})
	}
}

// TestIntegration_LoginPost_ValidCredentials exercises LoginPost's normal-login
// success branch (issue #758) — bcrypt compare, session cookie set, redirect to
// the fallback landing route for a fresh user with no DefaultRoute. Every
// redirect in this codebase's handlers uses http.StatusSeeOther (303), not 302.
func TestIntegration_LoginPost_ValidCredentials(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-login-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const password = "correct-horse-battery-staple"
	if err := h.createUser(ctx, username, "Integration Test User", password, false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username)
	}()

	rec := httptest.NewRecorder()
	h.LoginPost(rec, postForm("/login", url.Values{"username": {username}, "password": {password}}))
	assertStatus(t, "LoginPost(valid)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/parts" {
		t.Errorf("Location = %q, want /parts (fallback landing route)", loc)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("no session cookie set on successful login")
	}
}

// TestIntegration_LoginPost_InvalidPassword and TestIntegration_LoginPost_UnknownUsername
// both hit the same "invalid username or password" branch — a wrong password for
// a real user, and a username that doesn't exist at all.
func TestIntegration_LoginPost_InvalidPassword(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-login-badpw-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "the-real-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username)
	}()

	rec := httptest.NewRecorder()
	h.LoginPost(rec, postForm("/login", url.Values{"username": {username}, "password": {"wrong-password"}}))
	assertStatus(t, "LoginPost(wrong password)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login?error=") {
		t.Errorf("Location = %q, want prefix /login?error=", loc)
	}
}

func TestIntegration_LoginPost_UnknownUsername(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.LoginPost(rec, postForm("/login", url.Values{
		"username": {"itest-nonexistent-" + strconv.FormatInt(time.Now().UnixNano(), 10)},
		"password": {"whatever"},
	}))
	assertStatus(t, "LoginPost(unknown username)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/login?error=invalid+username+or+password" {
		t.Errorf("Location = %q, want /login?error=invalid+username+or+password", loc)
	}
}

// adminCtx returns req with the seeded admin user (8001) on the context, the
// same shape RequireAuth would inject after resolving the session.
func adminCtx(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey, &User{ID: 8001, Username: "admin", IsAdmin: true}))
}

// userCtxTZ returns req with a user carrying an explicit timezone on the context,
// so timezone-sensitive handlers (#847) are deterministic regardless of where the
// test process or the DB server thinks "today" is.
func userCtxTZ(req *http.Request, tz string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey,
		&User{ID: 8001, Username: "admin", IsAdmin: true, Timezone: tz}))
}

// withUserID injects a chi route context carrying the given "userID" URL
// parameter — the SettingsUsers* handlers read chi.URLParam(r, "userID"),
// unlike withID's "id" param used elsewhere in this file.
func withUserID(req *http.Request, id int) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", strconv.Itoa(id))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestIntegration_SettingsUsersCreate_Success/_MissingFields cover
// SettingsUsersCreate's two branches with a real admin in context.
func TestIntegration_SettingsUsersCreate_Success(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-create-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	rec := httptest.NewRecorder()
	h.SettingsUsersCreate(rec, adminCtx(postForm("/settings/users", url.Values{
		"username": {username}, "display_name": {"Integration Test User"}, "password": {"a-password"},
	})))
	assertStatus(t, "SettingsUsersCreate", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/settings?tab=users" {
		t.Errorf("Location = %q, want /settings?tab=users", loc)
	}

	var isAdmin bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_admin FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&isAdmin); err != nil {
		t.Fatalf("SELECT after create: %v", err)
	}
	if isAdmin {
		t.Error("user created via SettingsUsersCreate has is_admin=1, want 0 (only bootstrap creates an admin)")
	}
	_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username)
}

func TestIntegration_SettingsUsersCreate_MissingFields(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-create-missing-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	rec := httptest.NewRecorder()
	h.SettingsUsersCreate(rec, adminCtx(postForm("/settings/users", url.Values{
		"username": {username}, "display_name": {"Integration Test User"}, "password": {""},
	})))
	assertStatus(t, "SettingsUsersCreate(missing password)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/settings?tab=users&error=all+fields+required" {
		t.Errorf("Location = %q, want /settings?tab=users&error=all+fields+required", loc)
	}

	var n int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&n); err != nil {
		t.Fatalf("count after rejected create: %v", err)
	}
	if n != 0 {
		t.Errorf("row count for %q = %d, want 0 (no row should be inserted)", username, n)
	}
}

// TestIntegration_SettingsUsersResetPassword_Success/_EmptyPassword cover both
// branches of the password-reset handler.
func TestIntegration_SettingsUsersResetPassword_Success(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-resetpw-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "old-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	var userID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&userID); err != nil {
		t.Fatalf("look up created user id: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID)
	}()

	rec := httptest.NewRecorder()
	h.SettingsUsersResetPassword(rec, withUserID(adminCtx(postForm(
		fmt.Sprintf("/settings/users/%d/password", userID), url.Values{"password": {"new-password"}},
	)), userID))
	assertStatus(t, "SettingsUsersResetPassword", rec, http.StatusSeeOther)

	var hash string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT password_hash FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID,
	).Scan(&hash); err != nil {
		t.Fatalf("SELECT password_hash: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password")) != nil {
		t.Error("password_hash does not match the new password after reset")
	}
}

func TestIntegration_SettingsUsersResetPassword_EmptyPassword(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-resetpw-empty-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "old-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	var userID int
	var beforeHash string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id, password_hash FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&userID, &beforeHash); err != nil {
		t.Fatalf("look up created user: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID)
	}()

	rec := httptest.NewRecorder()
	h.SettingsUsersResetPassword(rec, withUserID(adminCtx(postForm(
		fmt.Sprintf("/settings/users/%d/password", userID), url.Values{"password": {""}},
	)), userID))
	assertStatus(t, "SettingsUsersResetPassword(empty)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/settings?tab=users&error=password+required" {
		t.Errorf("Location = %q, want /settings?tab=users&error=password+required", loc)
	}

	var afterHash string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT password_hash FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID,
	).Scan(&afterHash); err != nil {
		t.Fatalf("SELECT password_hash after rejected reset: %v", err)
	}
	if afterHash != beforeHash {
		t.Error("password_hash changed despite empty password being rejected")
	}
}

// TestIntegration_SettingsUsersToggleActive_Success flips is_active both
// directions and confirms invalidateUserCache ran on each toggle.
func TestIntegration_SettingsUsersToggleActive_Success(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-toggleactive-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "a-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	var userID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&userID); err != nil {
		t.Fatalf("look up created user id: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID)
	}()

	readActive := func() bool {
		var active bool
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT is_active FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID,
		).Scan(&active); err != nil {
			t.Fatalf("read is_active: %v", err)
		}
		return active
	}
	toggle := func() {
		h.userCache[userID] = &userCacheEntry{user: &User{ID: userID}, expires: time.Now().Add(time.Minute)}
		rec := httptest.NewRecorder()
		h.SettingsUsersToggleActive(rec, withUserID(adminCtx(postForm(
			fmt.Sprintf("/settings/users/%d/toggle-active", userID), url.Values{},
		)), userID))
		assertStatus(t, "SettingsUsersToggleActive", rec, http.StatusSeeOther)
		if _, ok := h.userCache[userID]; ok {
			t.Error("userCache entry still present after toggle; invalidateUserCache did not run")
		}
	}

	if !readActive() {
		t.Fatal("newly created user is_active = false, want true (DDL default)")
	}
	toggle()
	if readActive() {
		t.Error("is_active still true after first toggle, want false")
	}
	toggle()
	if !readActive() {
		t.Error("is_active still false after second toggle, want true")
	}
}

func TestIntegration_SettingsUsersToggleActive_CannotDeactivateSelf(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var before bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_active FROM %s WHERE id=8001`, h.cfg.UsersTable()),
	).Scan(&before); err != nil {
		t.Fatalf("read admin is_active: %v", err)
	}

	rec := httptest.NewRecorder()
	h.SettingsUsersToggleActive(rec, withUserID(adminCtx(postForm(
		"/settings/users/8001/toggle-active", url.Values{},
	)), 8001))
	assertStatus(t, "SettingsUsersToggleActive(self)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/settings?tab=users&error=cannot+deactivate+your+own+account" {
		t.Errorf("Location = %q, want /settings?tab=users&error=cannot+deactivate+your+own+account", loc)
	}

	var after bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_active FROM %s WHERE id=8001`, h.cfg.UsersTable()),
	).Scan(&after); err != nil {
		t.Fatalf("re-read admin is_active: %v", err)
	}
	if after != before {
		t.Error("admin is_active changed despite the self-deactivate guard")
	}
}

// TestIntegration_SettingsUsersToggleApprove_Success and
// TestIntegration_SettingsUsersToggleApproveRecords_Success mirror ToggleActive's
// success shape for can_approve_po / can_approve_records (neither has a
// self-guard, unlike ToggleActive/ToggleAdmin).
func TestIntegration_SettingsUsersToggleApprove_Success(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-toggleapprove-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "a-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	var userID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&userID); err != nil {
		t.Fatalf("look up created user id: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID)
	}()

	rec := httptest.NewRecorder()
	h.SettingsUsersToggleApprove(rec, withUserID(adminCtx(postForm(
		fmt.Sprintf("/settings/users/%d/toggle-approve", userID), url.Values{},
	)), userID))
	assertStatus(t, "SettingsUsersToggleApprove", rec, http.StatusSeeOther)

	var canApprove bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT can_approve_po FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID,
	).Scan(&canApprove); err != nil {
		t.Fatalf("read can_approve_po: %v", err)
	}
	if !canApprove {
		t.Error("can_approve_po still false after toggle, want true")
	}
}

func TestIntegration_SettingsUsersToggleApproveRecords_Success(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-toggleapproverec-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "a-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	var userID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&userID); err != nil {
		t.Fatalf("look up created user id: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID)
	}()

	rec := httptest.NewRecorder()
	h.SettingsUsersToggleApproveRecords(rec, withUserID(adminCtx(postForm(
		fmt.Sprintf("/settings/users/%d/toggle-approve-records", userID), url.Values{},
	)), userID))
	assertStatus(t, "SettingsUsersToggleApproveRecords", rec, http.StatusSeeOther)

	var canApprove bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT can_approve_records FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID,
	).Scan(&canApprove); err != nil {
		t.Fatalf("read can_approve_records: %v", err)
	}
	if !canApprove {
		t.Error("can_approve_records still false after toggle, want true")
	}
}

func TestIntegration_SettingsUsersToggleAdmin_Success(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	username := "itest-toggleadmin-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.createUser(ctx, username, "Integration Test User", "a-password", false); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	var userID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE username=@p1`, h.cfg.UsersTable()), username,
	).Scan(&userID); err != nil {
		t.Fatalf("look up created user id: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID)
	}()

	rec := httptest.NewRecorder()
	h.SettingsUsersToggleAdmin(rec, withUserID(adminCtx(postForm(
		fmt.Sprintf("/settings/users/%d/toggle-admin", userID), url.Values{},
	)), userID))
	assertStatus(t, "SettingsUsersToggleAdmin", rec, http.StatusSeeOther)

	var isAdmin bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_admin FROM %s WHERE id=@p1`, h.cfg.UsersTable()), userID,
	).Scan(&isAdmin); err != nil {
		t.Fatalf("read is_admin: %v", err)
	}
	if !isAdmin {
		t.Error("is_admin still false after toggle, want true")
	}
}

func TestIntegration_SettingsUsersToggleAdmin_CannotRemoveOwnAdmin(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var before bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_admin FROM %s WHERE id=8001`, h.cfg.UsersTable()),
	).Scan(&before); err != nil {
		t.Fatalf("read admin is_admin: %v", err)
	}

	rec := httptest.NewRecorder()
	h.SettingsUsersToggleAdmin(rec, withUserID(adminCtx(postForm(
		"/settings/users/8001/toggle-admin", url.Values{},
	)), 8001))
	assertStatus(t, "SettingsUsersToggleAdmin(self)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/settings?tab=users&error=cannot+remove+your+own+admin+rights" {
		t.Errorf("Location = %q, want /settings?tab=users&error=cannot+remove+your+own+admin+rights", loc)
	}

	var after bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT is_admin FROM %s WHERE id=8001`, h.cfg.UsersTable()),
	).Scan(&after); err != nil {
		t.Fatalf("re-read admin is_admin: %v", err)
	}
	if after != before {
		t.Error("admin is_admin changed despite the self-remove guard")
	}
}

// TestIntegration_RunNamedQuery_SingleResult exercises the "single" result-type
// path against a self-created part + primary attachment fixture, rather than
// relying on seeded rows that could drift.
func TestIntegration_RunNamedQuery_SingleResult(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	pn := fmt.Sprintf("ITEST-807-%d", time.Now().UnixNano())
	var partID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, title, is_active) OUTPUT INSERTED.id VALUES (@p1, @p2, 1)`,
		h.cfg.PartsTable()), pn, "807 test part",
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.PartsTable()), partID)

	var attID int
	insertAtt := h.dia().InsertReturningID(h.cfg.AttachmentsTable(),
		"part_id, file_name, category, sort_order, is_active", "@p1, @p2, @p3, 1, 1", true)
	if err := h.DB().QueryRowContext(ctx, insertAtt, partID, "drawing.pdf", "Drawing").Scan(&attID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=@p1", h.cfg.AttachmentsTable()), attID)

	if _, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET primary_attachment_id=@p1 WHERE id=@p2`, h.cfg.PartsTable()), attID, partID); err != nil {
		t.Fatalf("set primary attachment: %v", err)
	}

	qr, err := h.runNamedQuery(ctx, fmt.Sprintf("query:pn_primary_attachment(@pn=%s)", pn))
	if err != nil {
		t.Fatalf("runNamedQuery: %v", err)
	}
	if qr.ResultType != "single" {
		t.Errorf("ResultType = %q, want single", qr.ResultType)
	}
	if len(qr.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1", len(qr.Rows))
	}
	if qr.Rows[0].Value != "drawing.pdf" || qr.Rows[0].Label != "Drawing" {
		t.Errorf("row = %+v, want {Value:drawing.pdf Label:Drawing}", qr.Rows[0])
	}
}

// TestIntegration_RunNamedQuery_ListResult_MultiColumn exercises the "list"
// multi-row/2-column path against the seeded vendor_pns_for_pn named query and
// po_line fixtures (5501/5502/5505, part_number_snapshot RAW-1001).
func TestIntegration_RunNamedQuery_ListResult_MultiColumn(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	qr, err := h.runNamedQuery(ctx, "query:vendor_pns_for_pn(@pn=RAW-1001)")
	if err != nil {
		t.Fatalf("runNamedQuery: %v", err)
	}
	if qr.ResultType != "list" {
		t.Errorf("ResultType = %q, want list", qr.ResultType)
	}
	if len(qr.Rows) < 1 {
		t.Fatal("Rows is empty, want at least 1 (seeded po_line rows for RAW-1001)")
	}
	for _, row := range qr.Rows {
		if row.Value == row.Label {
			t.Errorf("row %+v: Value == Label, want distinct vendor_part_number/description columns", row)
		}
	}
}

// TestIntegration_RunNamedQuery_NoRowsIsNotError confirms zero matches is
// success (empty Rows), not an error.
func TestIntegration_RunNamedQuery_NoRowsIsNotError(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	qr, err := h.runNamedQuery(ctx, "query:parts_matching(@pattern=ZZZ-NO-MATCH-%)")
	if err != nil {
		t.Fatalf("runNamedQuery: %v", err)
	}
	if len(qr.Rows) != 0 {
		t.Errorf("Rows = %d, want 0", len(qr.Rows))
	}
}

// TestIntegration_RunNamedQuery_UnknownName confirms an unregistered name
// surfaces a "not found" error.
func TestIntegration_RunNamedQuery_UnknownName(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.runNamedQuery(ctx, "query:does_not_exist_xyz(@x=1)")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want error containing \"not found\"", err)
	}
}

// TestIntegration_RunNamedQuery_StaleSpecNomParamRename is the regression test
// for the production incident documented in
// SQL/migrations/migrate_max_subbatch_result_param_rename.sql: a named_queries
// row's stored SQL was renamed to a new param name, but a spec_nom usage string
// elsewhere still referenced the old name. parseQuerySpec builds its param map
// from the spec_nom text, not from named_queries.params, so this produces a
// runtime "Must declare the scalar variable" error rather than being caught at
// parse time. This test reproduces that failure mode directly against a
// throwaway named_queries row (not the real max_subbatch_result row).
func TestIntegration_RunNamedQuery_StaleSpecNomParamRename(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	name := fmt.Sprintf("itest_stale_param_%d", time.Now().UnixNano())
	if _, err := h.execContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (name, sql, params, result_type, is_active) VALUES (@p1, @p2, @p3, 'single', 1)`,
		h.cfg.NamedQueriesTable()), name, "SELECT 1 AS val WHERE @new_param = @new_param", "new_param",
	); err != nil {
		t.Fatalf("seed named_queries row: %v", err)
	}
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE name=@p1", h.cfg.NamedQueriesTable()), name)

	_, err := h.runNamedQuery(ctx, fmt.Sprintf("query:%s(@old_param=1)", name))
	if err == nil {
		t.Fatal("expected an error from the stale param-name mismatch, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "declare the scalar variable") {
		t.Errorf("err = %v, want an error containing \"declare the scalar variable\"", err)
	}
}

// TestIntegration_APINamedQuery_EndToEnd covers the HTTP handler wrapper's
// happy path against a seeded named query.
func TestIntegration_APINamedQuery_EndToEnd(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/named-query?spec="+url.QueryEscape("query:parts_matching(@pattern=RAW-%)"), nil)
	rec := httptest.NewRecorder()
	h.APINamedQuery(rec, req)

	assertStatus(t, "APINamedQuery", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"rows"`) {
		t.Errorf("body missing \"rows\" key: %s", rec.Body.String())
	}
}

// seedPOLine inserts one po_line row directly for a PO created via
// seedThrowawayPO and returns its new id.
func seedPOLine(t *testing.T, h *Handler, ctx context.Context, poID, partID int, partNumber string, qty, unitCost float64) int {
	t.Helper()
	var id int
	insert := h.dia().InsertReturningID(h.cfg.POLineTable(),
		"po_id, part_number_snapshot, revision_snapshot, part_id, line_number, description, qty, unit_cost, received_qty",
		"@p1, @p2, 'A', @p3, 1, 'test line', @p4, @p5, 0", true)
	if err := h.DB().QueryRowContext(ctx, insert, poID, partNumber, partID, qty, unitCost).Scan(&id); err != nil {
		t.Fatalf("seedPOLine: %v", err)
	}
	return id
}

// setPOStatus force-sets a PO's status/approval_status directly for test
// setup, bypassing the audited transition path.
func setPOStatus(t *testing.T, h *Handler, ctx context.Context, poID int, status, approval string) {
	t.Helper()
	if _, err := h.execContext(ctx, fmt.Sprintf(
		"UPDATE %s SET status=@p1, approval_status=@p2 WHERE ID=@p3", h.cfg.POTable()),
		status, approval, poID); err != nil {
		t.Fatalf("setPOStatus: %v", err)
	}
}

// approverCtx injects an authorized-approver *User onto the request context,
// mirroring adminCtx above but with CanApprovePO set instead of IsAdmin.
func approverCtx(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey, &User{ID: 8001, Username: "admin", CanApprovePO: true}))
}

func TestIntegration_POStatusTransition_AllowedAndRejected(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx)
	defer poCleanup()
	numID, err := strconv.Atoi(poNumber)
	if err != nil {
		t.Fatalf("PO number %q not numeric: %v", poNumber, err)
	}

	// Rejected transition: draft -> sent must go through open first, and must
	// not write a history row or change status.
	histBefore := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d", h.cfg.POHistoryTable(), poID))
	rec := httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"sent"}}), numID))
	assertStatus(t, "draft->sent rejected", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Cannot change status") {
		t.Errorf("draft->sent: expected rejection message in body, got: %s", rec.Body.String())
	}
	var statusAfterReject string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).
		Scan(&statusAfterReject); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if statusAfterReject != "draft" {
		t.Errorf("draft->sent rejected: status = %q, want unchanged draft", statusAfterReject)
	}
	histAfterReject := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d", h.cfg.POHistoryTable(), poID))
	if histAfterReject != histBefore {
		t.Errorf("draft->sent rejected: history rows = %d, want unchanged %d", histAfterReject, histBefore)
	}

	// Allowed transition: draft -> open.
	rec = httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"open"}}), numID))
	assert302(t, "draft->open", rec)

	var status string
	var isActive bool
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT status, is_active FROM %s WHERE ID=@p1", h.cfg.POTable()), poID,
	).Scan(&status, &isActive); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "open" || !isActive {
		t.Errorf("draft->open: got status=%q is_active=%v, want open/true", status, isActive)
	}

	var fromStatus, toStatus, changedBy string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 from_status, to_status, changed_by FROM %s WHERE po_id=@p1 AND event_type='status' ORDER BY id DESC`,
		h.cfg.POHistoryTable()), poID,
	).Scan(&fromStatus, &toStatus, &changedBy); err != nil {
		t.Fatalf("read history: %v", err)
	}
	if fromStatus != "draft" || toStatus != "open" || changedBy == "" {
		t.Errorf("history row = {from:%q to:%q by:%q}, want {draft open <non-empty>}", fromStatus, toStatus, changedBy)
	}

	// sent -> closed sets date_closed; closed -> open (reopen) clears it.
	setPOStatus(t, h, ctx, poID, "sent", "approved")
	rec = httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"closed"}}), numID))
	assert302(t, "sent->closed", rec)
	var dateClosed sql.NullTime
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT status, date_closed FROM %s WHERE ID=@p1", h.cfg.POTable()), poID,
	).Scan(&status, &dateClosed); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "closed" || !dateClosed.Valid {
		t.Errorf("sent->closed: got status=%q date_closed.Valid=%v, want closed/true", status, dateClosed.Valid)
	}

	rec = httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"open"}}), numID))
	assert302(t, "closed->open reopen", rec)
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT status, date_closed FROM %s WHERE ID=@p1", h.cfg.POTable()), poID,
	).Scan(&status, &dateClosed); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "open" || dateClosed.Valid {
		t.Errorf("closed->open reopen: got status=%q date_closed.Valid=%v, want open/false (cleared)", status, dateClosed.Valid)
	}
}

func TestIntegration_POStatusTransition_ApprovalGateForSent(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx)
	defer poCleanup()
	numID, _ := strconv.Atoi(poNumber)

	setPOStatus(t, h, ctx, poID, "open", "pending")

	rec := httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"sent"}}), numID))
	assertStatus(t, "open->sent without approval", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "must be approved") {
		t.Errorf("open->sent without approval: expected approval-gate message, got: %s", rec.Body.String())
	}
	var status string
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&status)
	if status != "open" {
		t.Errorf("open->sent without approval: status = %q, want unchanged open", status)
	}

	setPOStatus(t, h, ctx, poID, "open", "approved")
	rec = httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"sent"}}), numID))
	assert302(t, "open->sent with approval", rec)
}

func TestIntegration_POStatusTransition_CancelClearsApproval(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx)
	defer poCleanup()
	numID, _ := strconv.Atoi(poNumber)

	setPOStatus(t, h, ctx, poID, "open", "approved")
	rec := httptest.NewRecorder()
	h.POStatusTransition(rec, withID(postForm("/po/{id}/status", url.Values{"target": {"cancelled"}}), numID))
	assert302(t, "open->cancelled", rec)

	var status, approval string
	h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT status, approval_status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID,
	).Scan(&status, &approval)
	if status != "cancelled" || approval != "not_submitted" {
		t.Errorf("cancel: got status=%q approval=%q, want cancelled/not_submitted", status, approval)
	}

	var resetAction string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 action FROM %s WHERE po_id=@p1 AND event_type='approval' ORDER BY id DESC`,
		h.cfg.POHistoryTable()), poID,
	).Scan(&resetAction); err != nil {
		t.Fatalf("read reset history: %v", err)
	}
	if resetAction != "reset" {
		t.Errorf("cancel: last approval history action = %q, want reset", resetAction)
	}
}

func TestIntegration_POReceive_PartialThenFull(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx)
	defer poCleanup()
	numID, _ := strconv.Atoi(poNumber)

	lineID := seedPOLine(t, h, ctx, poID, 3001, "RAW-1001", 10, 2.50) // not lot-tracked
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_line_id=@p1", h.cfg.InventoryTxnTable()), lineID)
	// stock_on_hand is app-maintained, not reversed by deleting the ledger row above —
	// restore it explicitly so this test doesn't leak +10 onto part 3001.
	defer smokeExec(ctx, h, fmt.Sprintf("UPDATE %s SET stock_on_hand = stock_on_hand - 10 WHERE id = 3001", h.cfg.PartsTable()))
	setPOStatus(t, h, ctx, poID, "sent", "approved")

	// Partial receipt: 4 of 10.
	rec := httptest.NewRecorder()
	h.POReceive(rec, withID(postForm("/po/{id}/receive", url.Values{
		fmt.Sprintf("recv[%d]", lineID): {"4"},
	}), numID))
	assert302(t, "partial receive", rec)

	var status string
	var receivedQty float64
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&status)
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT received_qty FROM %s WHERE id=@p1", h.cfg.POLineTable()), lineID).Scan(&receivedQty)
	if status != "partially_received" {
		t.Errorf("after partial receive: PO status = %q, want partially_received", status)
	}
	if receivedQty != 4 {
		t.Errorf("after partial receive: line received_qty = %v, want 4", receivedQty)
	}
	invAfterPartial := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_line_id=%d", h.cfg.InventoryTxnTable(), lineID))
	if invAfterPartial != 1 {
		t.Fatalf("after partial receive: inventory_transaction rows for line = %d, want 1", invAfterPartial)
	}
	var txnType, reference string
	var txnQty float64
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 txn_type, qty, reference FROM %s WHERE po_line_id=@p1 ORDER BY id DESC`,
		h.cfg.InventoryTxnTable()), lineID,
	).Scan(&txnType, &txnQty, &reference)
	if txnType != "receipt" || txnQty != 4 || reference != poNumber {
		t.Errorf("inventory_transaction row = {type:%q qty:%v ref:%q}, want {receipt 4 %q}", txnType, txnQty, reference, poNumber)
	}

	// Full receipt: remaining 6 of 10 -> closed.
	rec = httptest.NewRecorder()
	h.POReceive(rec, withID(postForm("/po/{id}/receive", url.Values{
		fmt.Sprintf("recv[%d]", lineID): {"6"},
	}), numID))
	assert302(t, "final receive", rec)
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&status)
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT received_qty FROM %s WHERE id=@p1", h.cfg.POLineTable()), lineID).Scan(&receivedQty)
	if status != "closed" {
		t.Errorf("after full receive: PO status = %q, want closed", status)
	}
	if receivedQty != 10 {
		t.Errorf("after full receive: line received_qty = %v, want 10", receivedQty)
	}
	invAfterFull := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_line_id=%d", h.cfg.InventoryTxnTable(), lineID))
	if invAfterFull != 2 {
		t.Errorf("after full receive: inventory_transaction rows for line = %d, want 2", invAfterFull)
	}

	partialEvent := countRows(t, h, ctx, fmt.Sprintf(
		"%s WHERE po_id=%d AND event_type='status' AND from_status='sent' AND to_status='partially_received'",
		h.cfg.POHistoryTable(), poID))
	closedEvent := countRows(t, h, ctx, fmt.Sprintf(
		"%s WHERE po_id=%d AND event_type='status' AND from_status='partially_received' AND to_status='closed'",
		h.cfg.POHistoryTable(), poID))
	if partialEvent != 1 || closedEvent != 1 {
		t.Errorf("PO_history status events: sent->partially_received=%d, partially_received->closed=%d, want 1 each", partialEvent, closedEvent)
	}
}

func TestIntegration_POReceive_CreatesLotForLotTrackedPart(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx)
	defer poCleanup()
	numID, _ := strconv.Atoi(poNumber)

	lineID := seedPOLine(t, h, ctx, poID, 3007, "RAW-1002", 5, 4.10) // tracking_mode = 'lot'
	// stock_on_hand is app-maintained, not reversed by deleting the ledger row below —
	// restore it explicitly so this test doesn't leak +5 onto part 3007.
	defer smokeExec(ctx, h, fmt.Sprintf("UPDATE %s SET stock_on_hand = stock_on_hand - 5 WHERE id = 3007", h.cfg.PartsTable()))
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_line_id=@p1", h.cfg.LotTable()), lineID)
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_line_id=@p1", h.cfg.InventoryTxnTable()), lineID)
	setPOStatus(t, h, ctx, poID, "sent", "approved")

	rec := httptest.NewRecorder()
	h.POReceive(rec, withID(postForm("/po/{id}/receive", url.Values{
		fmt.Sprintf("recv[%d]", lineID): {"5"},
		fmt.Sprintf("vlot[%d]", lineID): {"VENDOR-LOT-803"},
	}), numID))
	assert302(t, "lot-tracked receive", rec)

	var lotID int
	var lotDesc, vendorLot string
	var lotPOLineID sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 id, lot_description, vendor_lot_number, po_line_id FROM %s WHERE po_line_id=@p1 ORDER BY id DESC`,
		h.cfg.LotTable()), lineID,
	).Scan(&lotID, &lotDesc, &vendorLot, &lotPOLineID); err != nil {
		t.Fatalf("read created lot: %v", err)
	}
	wantDesc := "PO " + poNumber
	if lotDesc != wantDesc || vendorLot != "VENDOR-LOT-803" || !lotPOLineID.Valid || int(lotPOLineID.Int64) != lineID {
		t.Errorf("lot row = {desc:%q vendorLot:%q poLineID:%v}, want {%q VENDOR-LOT-803 %d}",
			lotDesc, vendorLot, lotPOLineID, wantDesc, lineID)
	}

	var invLotID sql.NullInt64
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 lot_id FROM %s WHERE po_line_id=@p1 ORDER BY id DESC`, h.cfg.InventoryTxnTable()), lineID,
	).Scan(&invLotID)
	if !invLotID.Valid || int(invLotID.Int64) != lotID {
		t.Errorf("inventory_transaction.lot_id = %v, want %d", invLotID, lotID)
	}
}

func TestIntegration_POReceive_RejectsWrongStatus(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	_, poNumber, poCleanup := seedThrowawayPO(t, h, ctx) // status=draft
	defer poCleanup()
	numID, _ := strconv.Atoi(poNumber)

	rec := httptest.NewRecorder()
	h.POReceive(rec, withID(postForm("/po/{id}/receive", url.Values{"recv[1]": {"1"}}), numID))
	assertStatus(t, "receive on draft PO", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Only a sent or partially-received PO") {
		t.Errorf("receive on draft PO: expected rejection message, got: %s", rec.Body.String())
	}
}

func TestIntegration_POApprovalAction_FullWorkflow(t *testing.T) {
	h, hcleanup := liveHandler(t)
	defer hcleanup()
	ctx := context.Background()

	poID, poNumber, poCleanup := seedThrowawayPO(t, h, ctx) // approval_status=not_submitted
	defer poCleanup()
	numID, _ := strconv.Atoi(poNumber)

	rec := httptest.NewRecorder()
	h.POApprovalAction(rec, withID(postForm("/po/{id}/approval", url.Values{"action": {"submit"}}), numID))
	assert302(t, "submit", rec)
	var approval string
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT approval_status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&approval)
	if approval != "pending" {
		t.Errorf("after submit: approval_status = %q, want pending", approval)
	}

	// Reject without an authorized approver on the request context must be rejected.
	rec = httptest.NewRecorder()
	h.POApprovalAction(rec, withID(postForm("/po/{id}/approval", url.Values{"action": {"reject"}, "note": {"needs rework"}}), numID))
	assertStatus(t, "reject without approver", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "not authorized") {
		t.Errorf("reject without approver: expected authorization error, got: %s", rec.Body.String())
	}
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT approval_status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&approval)
	if approval != "pending" {
		t.Errorf("after unauthorized reject: approval_status = %q, want unchanged pending", approval)
	}

	// Reject as an authorized approver.
	rec = httptest.NewRecorder()
	h.POApprovalAction(rec, approverCtx(withID(postForm("/po/{id}/approval",
		url.Values{"action": {"reject"}, "note": {"needs rework"}}), numID)))
	assert302(t, "reject as approver", rec)
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT approval_status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&approval)
	if approval != "rejected" {
		t.Errorf("after reject: approval_status = %q, want rejected", approval)
	}
	var lastAction, lastNote string
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 action, note FROM %s WHERE po_id=@p1 AND event_type='approval' ORDER BY id DESC`,
		h.cfg.POHistoryTable()), poID,
	).Scan(&lastAction, &lastNote)
	if lastAction != "rejected" || lastNote != "needs rework" {
		t.Errorf("history row = {action:%q note:%q}, want {rejected \"needs rework\"}", lastAction, lastNote)
	}

	// Resubmit after reject -> pending, then approve -> approved.
	rec = httptest.NewRecorder()
	h.POApprovalAction(rec, withID(postForm("/po/{id}/approval", url.Values{"action": {"submit"}}), numID))
	assert302(t, "resubmit after reject", rec)

	rec = httptest.NewRecorder()
	h.POApprovalAction(rec, approverCtx(withID(postForm("/po/{id}/approval", url.Values{"action": {"approve"}}), numID)))
	assert302(t, "approve as approver", rec)
	h.DB().QueryRowContext(ctx, fmt.Sprintf("SELECT approval_status FROM %s WHERE ID=@p1", h.cfg.POTable()), poID).Scan(&approval)
	if approval != "approved" {
		t.Errorf("after approve: approval_status = %q, want approved", approval)
	}
}

// withGroupParam injects a chi route context carrying the given "group" URL
// parameter — RFQCompare/RFQCompareSave key off "group", unlike withID's "id".
func withGroupParam(req *http.Request, group string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("group", group)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// seedRFQQuote creates one RFQ quote (a purchase_order with status 'rfq') for
// the given supplier, with one line for part 3001 (RAW-1001). groupID == 0
// creates the first quote in a new group; a non-zero groupID joins that
// existing group as an additional supplier's quote. Returns the new quote's
// PO id and number.
func seedRFQQuote(t *testing.T, h *Handler, ctx context.Context, supplierID, groupID int, qty, unitCost float64) (id int, number string) {
	t.Helper()
	savedRoot := h.cfg.POFolderRoot
	h.cfg.POFolderRoot = ""
	defer func() { h.cfg.POFolderRoot = savedRoot }()

	supplierName := "Acme Fasteners"
	if supplierID == 1002 {
		supplierName = "Precision Machining Co"
	}
	vals := url.Values{
		"rfq":                         {"1"},
		"supplier_id":                 {strconv.Itoa(supplierID)},
		"supplier_name":               {supplierName},
		"new_pol[0][POLItem]":         {"1"},
		"new_pol[0][POLPNPartNumber]": {"RAW-1001"},
		"new_pol[0][POLDesc]":         {"Aluminum Stock 6061"},
		"new_pol[0][POLQty]":          {fmt.Sprintf("%g", qty)},
		"new_pol[0][POLCost]":         {fmt.Sprintf("%g", unitCost)},
		"new_pol[0][POLPNID]":         {"3001"},
	}
	if groupID != 0 {
		vals.Set("rfq_group_id", strconv.Itoa(groupID))
	}

	rec := httptest.NewRecorder()
	h.POCreate(rec, postForm("/pos", vals))
	loc := rec.Header().Get("Location")
	number = strings.TrimSuffix(strings.TrimPrefix(loc, "/po/"), "?suggest_links=1")
	if number == "" || number == loc {
		t.Fatalf("could not parse RFQ quote number from Location %q", loc)
	}
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT ID FROM %s WHERE number=@p1", h.cfg.POTable()), number,
	).Scan(&id); err != nil {
		t.Fatalf("look up created RFQ quote id: %v", err)
	}
	return id, number
}

// cleanupPO deletes po_line/purchase_order_history/purchase_order rows for
// the given PO id — the same cleanup seedThrowawayPO uses, exposed here for
// tests that manage several PO ids directly (RFQ groups, converted POs).
func cleanupPO(ctx context.Context, h *Handler, id int) {
	smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_id=@p1", h.cfg.POLineTable()), id)
	smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE po_id=@p1", h.cfg.POHistoryTable()), id)
	smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE ID=@p1", h.cfg.POTable()), id)
}

func TestIntegration_RFQNew_RendersRFQForm(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/rfqs/new", nil)
	rec := httptest.NewRecorder()
	h.RFQNew(rec, req)

	assertStatus(t, "RFQNew", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "New Request for Quotation") {
		t.Errorf("body missing RFQ marker text: %s", rec.Body.String())
	}
}

func TestIntegration_RFQAddSupplier_ClonesLinesBlanksSupplier(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	quoteID, quoteNumber := seedRFQQuote(t, h, ctx, 1001, 0, 10, 2.50)
	defer cleanupPO(ctx, h, quoteID)

	req := httptest.NewRequest(http.MethodGet, "/rfq/{id}/add-supplier", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", quoteNumber)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.RFQAddSupplier(rec, req)

	assertStatus(t, "RFQAddSupplier", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `value="RAW-1001"`) {
		t.Errorf("body missing cloned part number: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `value="1001"`) {
		t.Error("body still carries the source supplier_id (1001); expected it blanked")
	}
}

func TestIntegration_RFQCompare_BuildsGridWithBestMarkers(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	acmeID, _ := seedRFQQuote(t, h, ctx, 1001, 0, 10, 2.75)         // Acme, total 27.50
	defer cleanupPO(ctx, h, acmeID)
	pmcID, _ := seedRFQQuote(t, h, ctx, 1002, acmeID, 10, 2.40)     // Precision, total 24.00 (cheaper)
	defer cleanupPO(ctx, h, pmcID)

	req := withGroupParam(httptest.NewRequest(http.MethodGet, "/rfq/{group}/compare", nil), strconv.Itoa(acmeID))
	rec := httptest.NewRecorder()
	h.RFQCompare(rec, req)

	assertStatus(t, "RFQCompare", rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "Acme Fasteners") || !strings.Contains(body, "Precision Machining Co") {
		t.Errorf("body missing one of the two supplier names: %s", body)
	}
	if !strings.Contains(body, "Lowest") {
		t.Errorf("body missing the \"Lowest\" best-quote marker: %s", body)
	}
}

func TestIntegration_RFQCompareSave_PersistsCostAndRecomputesTotal(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	quoteID, _ := seedRFQQuote(t, h, ctx, 1001, 0, 10, 0) // unquoted line, cost 0
	defer cleanupPO(ctx, h, quoteID)

	var lineID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		"SELECT id FROM %s WHERE po_id=@p1", h.cfg.POLineTable()), quoteID,
	).Scan(&lineID); err != nil {
		t.Fatalf("look up seeded line id: %v", err)
	}

	rec := httptest.NewRecorder()
	h.RFQCompareSave(rec, withGroupParam(postForm("/rfq/{group}/compare", url.Values{
		fmt.Sprintf("cost_%d", lineID): {"3.25"},
		fmt.Sprintf("lead_%d", lineID): {"14"},
	}), strconv.Itoa(quoteID)))
	assert302(t, "RFQCompareSave", rec)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/rfq/%d/compare", quoteID) {
		t.Errorf("Location = %q, want /rfq/%d/compare", loc, quoteID)
	}

	var unitCost float64
	var leadDays sql.NullInt64
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		"SELECT unit_cost, lead_time_days FROM %s WHERE id=@p1", h.cfg.POLineTable()), lineID,
	).Scan(&unitCost, &leadDays)
	if unitCost != 3.25 {
		t.Errorf("unit_cost = %v, want 3.25", unitCost)
	}
	if !leadDays.Valid || leadDays.Int64 != 14 {
		t.Errorf("lead_time_days = %v, want 14", leadDays)
	}

	var totalCost float64
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		"SELECT total_cost FROM %s WHERE ID=@p1", h.cfg.POTable()), quoteID,
	).Scan(&totalCost)
	if totalCost != 32.50 { // 10 qty * 3.25
		t.Errorf("total_cost = %v, want 32.50", totalCost)
	}
}

// withIDStr injects a chi route context carrying the given "id" URL parameter
// as a literal string — unlike withID, which only handles numeric PO numbers,
// RFQ quote numbers (e.g. "5010R1") aren't numeric.
func withIDStr(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestIntegration_RFQConvert_AwardsWinnerAndCancelsSiblings(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	savedRoot := h.cfg.POFolderRoot
	h.cfg.POFolderRoot = ""
	defer func() { h.cfg.POFolderRoot = savedRoot }()

	acmeID, _ := seedRFQQuote(t, h, ctx, 1001, 0, 10, 2.75)             // Acme, total 27.50
	pmcID, pmcNumber := seedRFQQuote(t, h, ctx, 1002, acmeID, 10, 2.40) // Precision, total 24.00 (winner)
	base := rfqBaseNumber(pmcNumber)

	rec := httptest.NewRecorder()
	h.RFQConvert(rec, withIDStr(postForm("/rfq/{id}/convert", url.Values{}), pmcNumber))
	assert302(t, "RFQConvert", rec)
	if loc := rec.Header().Get("Location"); loc != "/po/"+base {
		t.Errorf("Location = %q, want /po/%s", loc, base)
	}

	var newID int
	if err := h.DB().QueryRowContext(ctx,
		fmt.Sprintf("SELECT ID FROM %s WHERE number=@p1", h.cfg.POTable()), base,
	).Scan(&newID); err != nil {
		t.Fatalf("look up converted PO: %v", err)
	}
	defer cleanupPO(ctx, h, newID)
	defer cleanupPO(ctx, h, acmeID)
	defer cleanupPO(ctx, h, pmcID)

	var newStatus string
	var newSupplierID sql.NullInt64
	var newGroupID sql.NullInt64
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		"SELECT status, supplier_id, rfq_group_id FROM %s WHERE ID=@p1", h.cfg.POTable()), newID,
	).Scan(&newStatus, &newSupplierID, &newGroupID)
	if newStatus != "draft" {
		t.Errorf("new PO status = %q, want draft", newStatus)
	}
	if !newSupplierID.Valid || int(newSupplierID.Int64) != 1002 {
		t.Errorf("new PO supplier_id = %v, want 1002", newSupplierID)
	}
	if newGroupID.Valid {
		t.Errorf("new PO rfq_group_id = %v, want NULL", newGroupID)
	}
	newLines := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d", h.cfg.POLineTable(), newID))
	if newLines != 1 {
		t.Errorf("new PO line count = %d, want 1", newLines)
	}

	var pmcStatus, pmcActive string
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		"SELECT status, CAST(is_active AS VARCHAR) FROM %s WHERE ID=@p1", h.cfg.POTable()), pmcID,
	).Scan(&pmcStatus, &pmcActive)
	if pmcStatus != "closed" {
		t.Errorf("awarded quote status = %q, want closed", pmcStatus)
	}

	var acmeStatus string
	h.DB().QueryRowContext(ctx, fmt.Sprintf(
		"SELECT status FROM %s WHERE ID=@p1", h.cfg.POTable()), acmeID,
	).Scan(&acmeStatus)
	if acmeStatus != "cancelled" {
		t.Errorf("sibling quote status = %q, want cancelled", acmeStatus)
	}

	newPOHistory := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d", h.cfg.POHistoryTable(), newID))
	if newPOHistory != 1 {
		t.Errorf("new PO history rows = %d, want 1 (draft creation)", newPOHistory)
	}
	pmcHistory := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d AND to_status='closed'", h.cfg.POHistoryTable(), pmcID))
	if pmcHistory != 1 {
		t.Errorf("awarded quote history 'closed' rows = %d, want 1", pmcHistory)
	}
	acmeHistory := countRows(t, h, ctx, fmt.Sprintf("%s WHERE po_id=%d AND to_status='cancelled'", h.cfg.POHistoryTable(), acmeID))
	if acmeHistory != 1 {
		t.Errorf("sibling quote history 'cancelled' rows = %d, want 1", acmeHistory)
	}
}

func TestIntegration_RFQConvert_RejectsNonRFQStatus(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	_, poNumber, poCleanup := seedThrowawayPO(t, h, ctx) // status=draft, not an RFQ
	defer poCleanup()

	rec := httptest.NewRecorder()
	h.RFQConvert(rec, withIDStr(postForm("/rfq/{id}/convert", url.Values{}), poNumber))
	assertStatus(t, "RFQConvert on non-RFQ", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Only an RFQ can be converted to a PO.") {
		t.Errorf("expected rejection message, got: %s", rec.Body.String())
	}
}

func TestIntegration_RFQConvert_RejectsWhenBaseNumberTaken(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	quoteID, quoteNumber := seedRFQQuote(t, h, ctx, 1001, 0, 10, 2.50)
	defer cleanupPO(ctx, h, quoteID)
	base := rfqBaseNumber(quoteNumber)

	// Manufacture the collision: insert a placeholder PO at the bare base number
	// the convert would try to claim.
	collisionID, _, collisionCleanup := seedThrowawayPO(t, h, ctx)
	defer collisionCleanup()
	if _, err := h.execContext(ctx, fmt.Sprintf(
		"UPDATE %s SET number=@p1 WHERE ID=@p2", h.cfg.POTable()), base, collisionID); err != nil {
		t.Fatalf("force collision PO number: %v", err)
	}

	rec := httptest.NewRecorder()
	h.RFQConvert(rec, withIDStr(postForm("/rfq/{id}/convert", url.Values{}), quoteNumber))
	assertStatus(t, "RFQConvert with base number taken", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "already in use") {
		t.Errorf("expected \"already in use\" rejection, got: %s", rec.Body.String())
	}
}

// Read-only — no cleanup. Covers PartUnits + unitsForPart + scanUnitRow + unitRowSelect.
func TestIntegration_PartUnitsHandler(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// Success: 3013 (lot_serial) lists its seeded unit with part/lot join fields.
	req := withID(httptest.NewRequest(http.MethodGet, "/part/3013/units", nil), 3013)
	rec := httptest.NewRecorder()
	h.PartUnits(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PartUnits(3013): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"SN-3013-001", "ASM-1003", "8306"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartUnits(3013): body missing %q", want)
		}
	}

	// Tab not applicable: 3004 is not serial/lot_serial tracked.
	req2 := withID(httptest.NewRequest(http.MethodGet, "/part/3004/units", nil), 3004)
	rec2 := httptest.NewRecorder()
	h.PartUnits(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "does not apply") {
		t.Errorf("PartUnits(3004): expected tab-not-applicable error, got body: %s", rec2.Body.String())
	}
}

// Read-only — no cleanup. Covers PartUnitTrace + fetchUnitRow + genealogy wiring.
func TestIntegration_PartUnitTraceHandler(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	// Success: unit 8501 belongs to part 3013, has both lot 8306 and build 8203.
	req := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/8501", nil), 3013, 8501)
	rec := httptest.NewRecorder()
	h.PartUnitTrace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PartUnitTrace(3013,8501): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"SN-3013-001", "8306", "8203"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartUnitTrace(3013,8501): body missing %q", want)
		}
	}

	// Invalid unitID: non-numeric route param.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "3013")
	rctx.URLParams.Add("unitID", "abc")
	badReq := httptest.NewRequest(http.MethodGet, "/part/3013/units/abc", nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	badRec := httptest.NewRecorder()
	h.PartUnitTrace(badRec, badReq)
	if !strings.Contains(badRec.Body.String(), "Invalid unit id") {
		t.Errorf("PartUnitTrace(3013,abc): expected \"Invalid unit id\", got body: %s", badRec.Body.String())
	}

	// Unit belongs to a different part than the URL's {id}: 8502 is part 3007's unit,
	// requested under part 3013.
	wrongPartReq := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/8502", nil), 3013, 8502)
	wrongPartRec := httptest.NewRecorder()
	h.PartUnitTrace(wrongPartRec, wrongPartReq)
	if !strings.Contains(wrongPartRec.Body.String(), "Unit not found for this part") {
		t.Errorf("PartUnitTrace(3013,8502): expected \"Unit not found for this part\", got body: %s", wrongPartRec.Body.String())
	}

	// Nonexistent unit id.
	missingReq := withIDAndUnitID(httptest.NewRequest(http.MethodGet, "/part/3013/units/999999", nil), 3013, 999999)
	missingRec := httptest.NewRecorder()
	h.PartUnitTrace(missingRec, missingReq)
	if !strings.Contains(missingRec.Body.String(), "Unit not found for this part") {
		t.Errorf("PartUnitTrace(3013,999999): expected \"Unit not found for this part\", got body: %s", missingRec.Body.String())
	}
}

// Read-only, uses seeded form 6001 (steps 6101-6108, step 6104 archived). No cleanup needed.
func TestIntegration_FormDef_RendersStepsAndArchivedToggle(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.FormDef(rec, withID(httptest.NewRequest(http.MethodGet, "/forms/6001/def", nil), 6001))
	if rec.Code != http.StatusOK {
		t.Fatalf("FormDef(6001): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Show archived", "Insulation Resistance", "Output Voltage", "Current Draw"} {
		if !strings.Contains(body, want) {
			t.Errorf("FormDef(6001): body missing %q", want)
		}
	}
	// Step 6108's hide_formula is "{record.type}!=Re-Test"; with no record context the
	// {record.type} token stays unresolved, so the "!=" comparison (unresolved-token !=
	// "Re-Test") evaluates true and the step is hidden — unlike an "=" formula, where an
	// unresolved token can never match and the step stays shown. This asymmetry is
	// existing evaluateHide behavior (see TestEvaluateHide), not something this test
	// should try to change.
	if strings.Contains(body, "Retest Voltage Check") {
		t.Errorf("FormDef(6001): body unexpectedly contains \"Retest Voltage Check\" (step 6108 should be hidden — unresolved \"!=\" token)")
	}
}

// Seeds a throwaway part/form/form_row, forces a deterministic same-day form_row_history
// row via a raw UPDATE, then verifies FormDefHistory returns the PRE-change snapshot value.
func TestIntegration_FormDefHistory_ReturnsPreChangeSnapshot(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var partID int
	partNumber := "ITEST-FDH-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}

	var testID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter, spec_max) OUTPUT INSERTED.id VALUES (@p1, 0, 'Historical Step', '100')`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}

	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_row_id=@p1`, h.cfg.FormRowHistoryTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}()

	// Raw UPDATE triggers trg_form_row_history, snapshotting the OLD spec_max='100'.
	// trg_form_row_history requires changed_by (NOT NULL), populated from CONTEXT_INFO —
	// a tx + setAuditUser is required, same as the production SaveFormDef/ArchiveStep path.
	tx, err := h.beginTx(ctx)
	if err != nil {
		t.Fatalf("beginTx: %v", err)
	}
	h.setAuditUser(ctx, tx, "itest")
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET spec_max='150' WHERE id=@p1`, h.cfg.StepsTable()), testID); err != nil {
		tx.Rollback()
		t.Fatalf("update form_row: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit form_row update: %v", err)
	}

	// The trigger stamps changed_at with GETDATE() = UTC on Azure SQL. Ask for the day
	// that UTC "now" falls on *in the user's zone*, which is what a real user's browser
	// would request, and pin that zone on the request context (#847). Deriving the
	// expected day from time.Now().UTC() rather than the test host's local clock keeps
	// this deterministic no matter where the test runs.
	const testTZ = "America/Los_Angeles"
	loc, err := time.LoadLocation(testTZ)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", testTZ, err)
	}
	today := time.Now().UTC().In(loc).Format("2006-01-02")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/%d/def/history?at=%s", formID, today), nil)
	h.FormDefHistory(rec, userCtxTZ(withID(req, formID), testTZ))
	if rec.Code != http.StatusOK {
		t.Fatalf("FormDefHistory(at=today): status %d, want 200", rec.Code)
	}

	var resp struct {
		Steps []struct {
			ID      int    `json:"id"`
			SpecMax string `json:"spec_max"`
			Changed bool   `json:"changed"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(resp.Steps))
	}
	if resp.Steps[0].ID != testID {
		t.Errorf("Steps[0].ID = %d, want %d", resp.Steps[0].ID, testID)
	}
	if !resp.Steps[0].Changed {
		t.Errorf("Steps[0].Changed = false, want true")
	}
	if resp.Steps[0].SpecMax != "100" {
		t.Errorf("Steps[0].SpecMax = %q, want %q (pre-change value)", resp.Steps[0].SpecMax, "100")
	}

	// No history that day (yesterday, in the same zone) — falls back to the CURRENT form_row value.
	yesterday := time.Now().UTC().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/%d/def/history?at=%s", formID, yesterday), nil)
	h.FormDefHistory(rec2, userCtxTZ(withID(req2, formID), testTZ))
	var resp2 struct {
		Steps []struct {
			ID      int    `json:"id"`
			SpecMax string `json:"spec_max"`
			Changed bool   `json:"changed"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp2.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(resp2.Steps))
	}
	if resp2.Steps[0].Changed {
		t.Errorf("Steps[0].Changed = true, want false (no history yesterday)")
	}
	if resp2.Steps[0].SpecMax != "150" {
		t.Errorf("Steps[0].SpecMax = %q, want %q (current value)", resp2.Steps[0].SpecMax, "150")
	}
}

// Read-only, uses seeded form 6001. No cleanup needed.
func TestIntegration_EditFormDef_ShowsRawUnsubstitutedValues(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.EditFormDef(rec, withID(httptest.NewRequest(http.MethodGet, "/forms/6001/def/edit", nil), 6001))
	if rec.Code != http.StatusOK {
		t.Fatalf("EditFormDef(6001): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "query:recent_serial_numbers_for_form(@form_id={form.id})") {
		t.Errorf("EditFormDef(6001): body missing raw unresolved token for step 6107's spec_nom")
	}
	if !strings.Contains(body, "Show archived") {
		t.Errorf("EditFormDef(6001): body missing \"Show archived\"")
	}
}

// Seeds a throwaway part/form/two form_row steps; POSTs a change to stepA only (stepB's
// submitted values equal its original_* values, so it must be left untouched), plus a
// new row and a reordered step_order.
func TestIntegration_SaveFormDef_UpdatesStepSkipsUnchangedAndReordersWithNewRow(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var partID int
	partNumber := "ITEST-SFD-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}

	var stepAID, stepBID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter) OUTPUT INSERTED.id VALUES (@p1, 0, 'Old A')`,
		h.cfg.StepsTable()), formID,
	).Scan(&stepAID); err != nil {
		t.Fatalf("seed stepA: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter) OUTPUT INSERTED.id VALUES (@p1, 0, 'Keep B')`,
		h.cfg.StepsTable()), formID,
	).Scan(&stepBID); err != nil {
		t.Fatalf("seed stepB: %v", err)
	}
	// Sentinel updated_at for stepB — must survive untouched (proves the skip branch).
	// trg_form_row_history requires changed_by (NOT NULL) from CONTEXT_INFO — set it via
	// a tx, same as the production SaveFormDef/ArchiveStep path.
	sentinelTx, err := h.beginTx(ctx)
	if err != nil {
		t.Fatalf("beginTx: %v", err)
	}
	h.setAuditUser(ctx, sentinelTx, "itest")
	if _, err := sentinelTx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET updated_at='2020-01-01T00:00:00' WHERE id=@p1`, h.cfg.StepsTable()), stepBID); err != nil {
		sentinelTx.Rollback()
		t.Fatalf("set stepB sentinel updated_at: %v", err)
	}
	if err := sentinelTx.Commit(); err != nil {
		t.Fatalf("commit stepB sentinel update: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg.FormsTable()),
		fmt.Sprintf("%d,%d", stepAID, stepBID), formID); err != nil {
		t.Fatalf("set form test_order: %v", err)
	}

	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_row_id IN (SELECT id FROM %s WHERE form_id=@p1)`,
			h.cfg.FormRowHistoryTable(), h.cfg.StepsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.StepsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
	}()

	vals := url.Values{
		fmt.Sprintf("original_parameter_%d", stepAID): {"anything-A-orig"},
		fmt.Sprintf("parameter_%d", stepAID):           {"Updated Parameter"},
		fmt.Sprintf("original_parameter_%d", stepBID):  {"same"},
		fmt.Sprintf("parameter_%d", stepBID):            {"same"},
		"step_order":               {fmt.Sprintf("%d,new_0,%d", stepBID, stepAID)},
		"new_row[0][type]":         {"0"},
		"new_row[0][parameter]":    {"New Step"},
	}
	rec := httptest.NewRecorder()
	h.SaveFormDef(rec, adminCtx(withID(postForm(fmt.Sprintf("/forms/%d/def/edit", formID), vals), formID)))
	assertStatus(t, "SaveFormDef", rec, http.StatusSeeOther)

	var gotParamA string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT parameter FROM %s WHERE id=@p1`, h.cfg.StepsTable()), stepAID,
	).Scan(&gotParamA); err != nil {
		t.Fatalf("select stepA: %v", err)
	}
	if gotParamA != "Updated Parameter" {
		t.Errorf("stepA.parameter = %q, want %q", gotParamA, "Updated Parameter")
	}

	var gotParamB, gotUpdatedAtB string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT parameter, CONVERT(varchar, updated_at, 120) FROM %s WHERE id=@p1`, h.cfg.StepsTable()), stepBID,
	).Scan(&gotParamB, &gotUpdatedAtB); err != nil {
		t.Fatalf("select stepB: %v", err)
	}
	if gotParamB != "Keep B" {
		t.Errorf("stepB.parameter = %q, want unchanged %q", gotParamB, "Keep B")
	}
	if gotUpdatedAtB != "2020-01-01 00:00:00" {
		t.Errorf("stepB.updated_at = %q, want unchanged sentinel %q", gotUpdatedAtB, "2020-01-01 00:00:00")
	}

	var newStepID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE form_id=@p1 AND parameter='New Step'`, h.cfg.StepsTable()), formID,
	).Scan(&newStepID); err != nil {
		t.Fatalf("select new step: %v", err)
	}

	var gotOrder string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT test_order FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID,
	).Scan(&gotOrder); err != nil {
		t.Fatalf("select form test_order: %v", err)
	}
	wantOrder := fmt.Sprintf("%d,%d,%d", stepBID, newStepID, stepAID)
	if gotOrder != wantOrder {
		t.Errorf("form.test_order = %q, want %q", gotOrder, wantOrder)
	}
}

// Seeds a throwaway part/form/form_row (archived=0), toggles archived on then off.
func TestIntegration_ArchiveStep_TogglesArchivedFlag(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, formID, testID := seedThrowawayForm(t, h, ctx, "ITEST-AS")
	defer cleanupThrowawayForm(ctx, h, partID, formID, testID)

	rec := httptest.NewRecorder()
	h.ArchiveStep(rec, adminCtx(withIDAndTestID(
		postForm(fmt.Sprintf("/forms/%d/tests/%d/archive", formID, testID), url.Values{"archived": {"1"}}),
		formID, testID)))
	assertStatus(t, "ArchiveStep (archive)", rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != fmt.Sprintf("/forms/%d/def/edit", formID) {
		t.Errorf("Location = %q, want %q", got, fmt.Sprintf("/forms/%d/def/edit", formID))
	}

	var archived bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT archived FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID,
	).Scan(&archived); err != nil {
		t.Fatalf("select archived: %v", err)
	}
	if !archived {
		t.Errorf("archived = false after archive, want true")
	}

	rec2 := httptest.NewRecorder()
	h.ArchiveStep(rec2, adminCtx(withIDAndTestID(
		postForm(fmt.Sprintf("/forms/%d/tests/%d/archive", formID, testID), url.Values{"archived": {"0"}}),
		formID, testID)))
	assertStatus(t, "ArchiveStep (unarchive)", rec2, http.StatusSeeOther)

	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT archived FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID,
	).Scan(&archived); err != nil {
		t.Fatalf("select archived: %v", err)
	}
	if archived {
		t.Errorf("archived = true after unarchive, want false")
	}
}

// The key regression test #813 calls out: archiving a step must never alter a locked
// record's frozen test_order/form_revision snapshot.
func TestIntegration_ArchiveStep_DoesNotAlterLockedRecordSnapshot(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, formID, testID := seedThrowawayForm(t, h, ctx, "ITEST-ASL")
	recordID := seedLockedFormRecord(t, h, ctx, formID, testID)
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID)
		cleanupThrowawayForm(ctx, h, partID, formID, testID)
	}()

	var beforeOrder string
	var beforeRev sql.NullInt32
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT test_order, form_revision FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&beforeOrder, &beforeRev); err != nil {
		t.Fatalf("select before: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ArchiveStep(rec, adminCtx(withIDAndTestID(
		postForm(fmt.Sprintf("/forms/%d/tests/%d/archive", formID, testID), url.Values{"archived": {"1"}}),
		formID, testID)))
	assertStatus(t, "ArchiveStep", rec, http.StatusSeeOther)

	var afterOrder string
	var afterRev sql.NullInt32
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT test_order, form_revision FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&afterOrder, &afterRev); err != nil {
		t.Fatalf("select after: %v", err)
	}
	if afterOrder != beforeOrder {
		t.Errorf("locked record test_order changed: before=%q after=%q", beforeOrder, afterOrder)
	}
	if afterRev != beforeRev {
		t.Errorf("locked record form_revision changed: before=%v after=%v", beforeRev, afterRev)
	}

	var archived bool
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT archived FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID,
	).Scan(&archived); err != nil {
		t.Fatalf("select archived: %v", err)
	}
	if !archived {
		t.Errorf("archived = false, want true (sanity check the action ran)")
	}
}

// Same regression property as above, exercised via SaveFormDef instead of ArchiveStep.
func TestIntegration_SaveFormDef_DoesNotAlterLockedRecordSnapshot(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, formID, testID := seedThrowawayForm(t, h, ctx, "ITEST-SFDL")
	recordID := seedLockedFormRecord(t, h, ctx, formID, testID)
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID)
		cleanupThrowawayForm(ctx, h, partID, formID, testID)
	}()

	var beforeOrder string
	var beforeRev sql.NullInt32
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT test_order, form_revision FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&beforeOrder, &beforeRev); err != nil {
		t.Fatalf("select before: %v", err)
	}

	vals := url.Values{
		fmt.Sprintf("original_parameter_%d", testID): {"anything-orig"},
		fmt.Sprintf("parameter_%d", testID):           {"Updated Parameter"},
	}
	rec := httptest.NewRecorder()
	h.SaveFormDef(rec, adminCtx(withID(postForm(fmt.Sprintf("/forms/%d/def/edit", formID), vals), formID)))
	assertStatus(t, "SaveFormDef", rec, http.StatusSeeOther)

	var gotParam string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT parameter FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID,
	).Scan(&gotParam); err != nil {
		t.Fatalf("select step: %v", err)
	}
	if gotParam != "Updated Parameter" {
		t.Errorf("step.parameter = %q, want %q (sanity check the save happened)", gotParam, "Updated Parameter")
	}

	var afterOrder string
	var afterRev sql.NullInt32
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT test_order, form_revision FROM %s WHERE id=@p1`, h.cfg.RecordsTable()), recordID,
	).Scan(&afterOrder, &afterRev); err != nil {
		t.Fatalf("select after: %v", err)
	}
	if afterOrder != beforeOrder {
		t.Errorf("locked record test_order changed: before=%q after=%q", beforeOrder, afterOrder)
	}
	if afterRev != beforeRev {
		t.Errorf("locked record form_revision changed: before=%v after=%v", beforeRev, afterRev)
	}
}

// seedThrowawayForm creates a throwaway part + form + one data-row form_row (not
// archived), returning the ids. Shared by the ArchiveStep/SaveFormDef locked-snapshot
// tests above.
func seedThrowawayForm(t *testing.T, h *Handler, ctx context.Context, label string) (partID, formID, testID int) {
	t.Helper()
	partNumber := label + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, type, parameter, archived) OUTPUT INSERTED.id VALUES (@p1, 0, 'Step', 0)`,
		h.cfg.StepsTable()), formID,
	).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg.FormsTable()), strconv.Itoa(testID), formID); err != nil {
		t.Fatalf("set form test_order: %v", err)
	}
	return partID, formID, testID
}

// cleanupThrowawayForm deletes the rows created by seedThrowawayForm, best-effort.
func cleanupThrowawayForm(ctx context.Context, h *Handler, partID, formID, testID int) {
	smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_row_id=@p1`, h.cfg.FormRowHistoryTable()), testID)
	smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.StepsTable()), testID)
	smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID)
}

// seedLockedFormRecord inserts one locked form_record under formID with a frozen
// test_order/form_revision snapshot, returning the new record id.
func seedLockedFormRecord(t *testing.T, h *Handler, ctx context.Context, formID, testID int) int {
	t.Helper()
	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, record_date, serial_number, is_active, is_locked, test_order, form_revision)
		 OUTPUT INSERTED.id VALUES (@p1, '2026-07-01', '1', 1, 1, @p2, 1)`,
		h.cfg.RecordsTable()), formID, strconv.Itoa(testID),
	).Scan(&recordID); err != nil {
		t.Fatalf("seed locked form_record: %v", err)
	}
	return recordID
}

// Read-only — no cleanup. Covers lotsForPart + scanLotRow + lotRowSelect + PartLots.
func TestIntegration_LotsForPart(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	lots, err := h.lotsForPart(ctx, 3007)
	if err != nil {
		t.Fatalf("lotsForPart(3007): %v", err)
	}
	if len(lots) != 2 {
		t.Fatalf("lotsForPart(3007): len = %d, want 2", len(lots))
	}
	// Newest first: 8303 (2026-05-22) before 8301 (2026-05-15).
	if lots[0].ID != 8303 || lots[1].ID != 8301 {
		t.Fatalf("lotsForPart(3007) order = [%d,%d], want [8303,8301]", lots[0].ID, lots[1].ID)
	}
	for _, lr := range lots {
		if lr.PartID != 3007 || lr.PartNumber != "RAW-1002" {
			t.Errorf("lot %d: PartID=%d PartNumber=%q, want 3007/RAW-1002", lr.ID, lr.PartID, lr.PartNumber)
		}
	}
	if lots[1].VendorLot != "SS304-LOT-0088" {
		t.Errorf("lot 8301.VendorLot = %q, want %q", lots[1].VendorLot, "SS304-LOT-0088")
	}
	if lots[1].LotDescription != "PO 5003" {
		t.Errorf("lot 8301.LotDescription = %q, want %q", lots[1].LotDescription, "PO 5003")
	}
	if lots[0].VendorLot != "" {
		t.Errorf("lot 8303.VendorLot = %q, want \"\" (NULL)", lots[0].VendorLot)
	}
	if lots[0].LotDescription != "Cycle count - unlabeled found lot" {
		t.Errorf("lot 8303.LotDescription = %q, want %q", lots[0].LotDescription, "Cycle count - unlabeled found lot")
	}

	empty, err := h.lotsForPart(ctx, 3099999)
	if err != nil {
		t.Fatalf("lotsForPart(no lots): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("lotsForPart(no lots): len = %d, want 0", len(empty))
	}

	rec := httptest.NewRecorder()
	h.PartLots(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3007/lots", nil), 3007))
	if rec.Code != http.StatusOK {
		t.Fatalf("PartLots(3007): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"8301", "8303", "RAW-1002"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartLots(3007): body missing %q", want)
		}
	}

	rec2 := httptest.NewRecorder()
	h.PartLots(rec2, withID(httptest.NewRequest(http.MethodGet, "/part/3005/lots", nil), 3005))
	if !strings.Contains(rec2.Body.String(), "does not apply to") {
		t.Errorf("PartLots(3005, not lot-tracked): expected tab-not-applicable error, got body: %s", rec2.Body.String())
	}
}

// Read-only — no cleanup. Covers fetchLotRow directly.
func TestIntegration_FetchLotRow(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	lr, found, err := h.fetchLotRow(ctx, 8301)
	if err != nil {
		t.Fatalf("fetchLotRow(8301): %v", err)
	}
	if !found {
		t.Fatalf("fetchLotRow(8301): found = false, want true")
	}
	if lr.PartID != 3007 || lr.LotNumber != "8301" || !lr.IsActive {
		t.Errorf("fetchLotRow(8301) = %+v, want PartID=3007 LotNumber=8301 IsActive=true", lr)
	}

	_, found2, err := h.fetchLotRow(ctx, 999999999)
	if err != nil {
		t.Fatalf("fetchLotRow(nonexistent): %v", err)
	}
	if found2 {
		t.Errorf("fetchLotRow(nonexistent): found = true, want false")
	}
}

// The shared guard test — the ONE place lotBelongsToPart is exercised directly.
// PartLotTrace/LotEdit/LotUpdate and PartStockAdjust below reuse this guard's wiring
// without re-deriving its true/false logic.
func TestIntegration_LotBelongsToPart(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	var inactiveLotID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_id, lot_number, lot_description, is_active) OUTPUT INSERTED.id VALUES (@p1, 'ITEST-INACTIVE', 'itest inactive lot', 0)`,
		h.cfg.LotTable()), 3007,
	).Scan(&inactiveLotID); err != nil {
		t.Fatalf("seed inactive lot: %v", err)
	}
	defer smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.LotTable()), inactiveLotID)

	tx, err := h.beginTx(ctx)
	if err != nil {
		t.Fatalf("beginTx: %v", err)
	}
	defer tx.Rollback()

	if ok, err := h.lotBelongsToPart(ctx, tx, 8301, 3007); err != nil || !ok {
		t.Errorf("lotBelongsToPart(8301,3007) = %v,%v, want true,nil", ok, err)
	}
	if ok, err := h.lotBelongsToPart(ctx, tx, 8301, 3012); err != nil || ok {
		t.Errorf("lotBelongsToPart(8301,3012) = %v,%v, want false,nil (wrong part)", ok, err)
	}
	if ok, err := h.lotBelongsToPart(ctx, tx, 999999999, 3007); err != nil || ok {
		t.Errorf("lotBelongsToPart(nonexistent,3007) = %v,%v, want false,nil", ok, err)
	}
	if ok, err := h.lotBelongsToPart(ctx, tx, inactiveLotID, 3007); err != nil || ok {
		t.Errorf("lotBelongsToPart(inactive,3007) = %v,%v, want false,nil (inactive)", ok, err)
	}
}

// Covers PartLotTrace + genealogy wiring for lots + the lot-not-found/wrong-part guards.
func TestIntegration_PartLotTrace(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PartLotTrace(rec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3013/lots/8306", nil), 3013, 8306))
	if rec.Code != http.StatusOK {
		t.Fatalf("PartLotTrace(3013,8306): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"8302", "8303", "8301", "ASM-1002", "RAW-1002"} {
		if !strings.Contains(body, want) {
			t.Errorf("PartLotTrace(3013,8306): body missing %q", want)
		}
	}

	wrongPartRec := httptest.NewRecorder()
	h.PartLotTrace(wrongPartRec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3012/lots/8301", nil), 3012, 8301))
	if !strings.Contains(wrongPartRec.Body.String(), "Lot not found for this part") {
		t.Errorf("PartLotTrace(3012,8301, wrong part): expected \"Lot not found for this part\", got body: %s", wrongPartRec.Body.String())
	}

	missingRec := httptest.NewRecorder()
	h.PartLotTrace(missingRec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3007/lots/999999999", nil), 3007, 999999999))
	if !strings.Contains(missingRec.Body.String(), "Lot not found for this part") {
		t.Errorf("PartLotTrace(3007,999999999): expected \"Lot not found for this part\", got body: %s", missingRec.Body.String())
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "3007")
	rctx.URLParams.Add("lotID", "abc")
	badReq := httptest.NewRequest(http.MethodGet, "/part/3007/lots/abc", nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	badRec := httptest.NewRecorder()
	h.PartLotTrace(badRec, badReq)
	if !strings.Contains(badRec.Body.String(), "Invalid lot id") {
		t.Errorf("PartLotTrace(3007,abc): expected \"Invalid lot id\", got body: %s", badRec.Body.String())
	}

	tabRec := httptest.NewRecorder()
	h.PartLotTrace(tabRec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3005/lots/1", nil), 3005, 1))
	if !strings.Contains(tabRec.Body.String(), "does not apply to") {
		t.Errorf("PartLotTrace(3005, not lot-tracked): expected tab-not-applicable error, got body: %s", tabRec.Body.String())
	}
}

// Covers LotEdit (GET) and LotUpdate (POST) together — Update's happy path is verified
// by re-fetching the throwaway lot after the call.
func TestIntegration_LotEditAndUpdate(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	rec := httptest.NewRecorder()
	h.LotEdit(rec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3007/lots/8301/edit", nil), 3007, 8301))
	if rec.Code != http.StatusOK {
		t.Fatalf("LotEdit(3007,8301): status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"8301", "PO 5003", "SS304-LOT-0088"} {
		if !strings.Contains(body, want) {
			t.Errorf("LotEdit(3007,8301): body missing %q", want)
		}
	}

	wrongPartRec := httptest.NewRecorder()
	h.LotEdit(wrongPartRec, withIDAndLotID(httptest.NewRequest(http.MethodGet, "/part/3012/lots/8301/edit", nil), 3012, 8301))
	if !strings.Contains(wrongPartRec.Body.String(), "Lot not found for this part") {
		t.Errorf("LotEdit(3012,8301, wrong part): expected \"Lot not found for this part\", got body: %s", wrongPartRec.Body.String())
	}

	// LotUpdate happy path — a throwaway lot, not one of the shared seed lots.
	var throwawayLotID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_id, lot_number, lot_description, is_active) OUTPUT INSERTED.id VALUES (@p1, 'ITEST-LOTUPD', 'orig desc', 1)`,
		h.cfg.LotTable()), 3007,
	).Scan(&throwawayLotID); err != nil {
		t.Fatalf("seed throwaway lot: %v", err)
	}
	defer smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.LotTable()), throwawayLotID)

	updateRec := httptest.NewRecorder()
	h.LotUpdate(updateRec, withIDAndLotID(postForm(fmt.Sprintf("/part/3007/lots/%d", throwawayLotID), url.Values{
		"lot_description": {"new desc"}, "vendor_lot": {"NEWVENDOR123"},
	}), 3007, throwawayLotID))
	assert302(t, "LotUpdate", updateRec)

	var gotDesc, gotVendor string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT lot_description, vendor_lot_number FROM %s WHERE id=@p1`, h.cfg.LotTable()), throwawayLotID,
	).Scan(&gotDesc, &gotVendor); err != nil {
		t.Fatalf("select updated lot: %v", err)
	}
	if gotDesc != "new desc" || gotVendor != "NEWVENDOR123" {
		t.Errorf("lot after update: desc=%q vendor=%q, want %q/%q", gotDesc, gotVendor, "new desc", "NEWVENDOR123")
	}

	// Wrong-part guard: POST against the throwaway lot but under a different part id.
	wrongUpdateRec := httptest.NewRecorder()
	h.LotUpdate(wrongUpdateRec, withIDAndLotID(postForm(fmt.Sprintf("/part/3012/lots/%d", throwawayLotID), url.Values{
		"lot_description": {"hacked desc"}, "vendor_lot": {"HACKED"},
	}), 3012, throwawayLotID))
	if !strings.Contains(wrongUpdateRec.Body.String(), "Lot not found for this part") {
		t.Errorf("LotUpdate(wrong part): expected \"Lot not found for this part\", got body: %s", wrongUpdateRec.Body.String())
	}
	var afterDesc string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT lot_description FROM %s WHERE id=@p1`, h.cfg.LotTable()), throwawayLotID,
	).Scan(&afterDesc); err != nil {
		t.Fatalf("select lot after wrong-part update: %v", err)
	}
	if afterDesc != "new desc" {
		t.Errorf("lot_description after wrong-part update = %q, want unchanged %q (guard should block the write)", afterDesc, "new desc")
	}

	// Invalid lotID: non-numeric route param.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "3007")
	rctx.URLParams.Add("lotID", "abc")
	badReq := postForm("/part/3007/lots/abc", url.Values{}).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	badRec := httptest.NewRecorder()
	h.LotUpdate(badRec, badReq)
	if !strings.Contains(badRec.Body.String(), "Invalid lot id") {
		t.Errorf("LotUpdate(abc): expected \"Invalid lot id\", got body: %s", badRec.Body.String())
	}

	tabRec := httptest.NewRecorder()
	h.LotUpdate(tabRec, withIDAndLotID(postForm("/part/3005/lots/1", url.Values{}), 3005, 1))
	if !strings.Contains(tabRec.Body.String(), "does not apply to") {
		t.Errorf("LotUpdate(3005, not lot-tracked): expected tab-not-applicable error, got body: %s", tabRec.Body.String())
	}
}

// Read-only — no cleanup. Covers AllLots.
func TestIntegration_AllLots(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.AllLots(rec, httptest.NewRequest(http.MethodGet, "/lots", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("AllLots: status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"8301", "8302", "8303", "8306", "RAW-1002", "ASM-1003"} {
		if !strings.Contains(body, want) {
			t.Errorf("AllLots: body missing %q", want)
		}
	}
}

// Covers PartStockAdjust, reusing the lotBelongsToPart guard already proven above —
// only asserts the handler wires it correctly, not the guard's own true/false logic.
func TestIntegration_PartStockAdjust(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Non-lot-tracked happy path: part 3002 (BUY-1001) is never marked lot-tracked in
	// the seed, and BUY is a stockable category so ShowInventory()/requireTab("transactions")
	// pass regardless of pre-existing ledger activity.
	const nonLotTrackedPart = 3002
	var stockBefore float64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT stock_on_hand FROM %s WHERE id=@p1`, h.cfg.PartsTable()), nonLotTrackedPart,
	).Scan(&stockBefore); err != nil {
		t.Fatalf("select stock_on_hand before: %v", err)
	}
	rec := httptest.NewRecorder()
	h.PartStockAdjust(rec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", nonLotTrackedPart), url.Values{
		"qty": {"5"}, "reason": {"itest count correction"}, "txn_date": {"2026-07-01"},
	}), nonLotTrackedPart))
	assert302(t, "PartStockAdjust (non-lot-tracked)", rec)

	var txnID int
	var stockAfter float64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT stock_on_hand FROM %s WHERE id=@p1`, h.cfg.PartsTable()), nonLotTrackedPart,
	).Scan(&stockAfter); err != nil {
		t.Fatalf("select stock_on_hand after: %v", err)
	}
	if stockAfter != stockBefore+5 {
		t.Errorf("stock_on_hand after = %v, want %v (before %v + 5)", stockAfter, stockBefore+5, stockBefore)
	}
	var qty float64
	var note string
	var lotIDNull sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 id, qty, note, lot_id FROM %s WHERE part_id=@p1 AND txn_type='adjustment' ORDER BY id DESC`,
		h.cfg.InventoryTxnTable()), nonLotTrackedPart,
	).Scan(&txnID, &qty, &note, &lotIDNull); err != nil {
		t.Fatalf("select new ledger row: %v", err)
	}
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.InventoryTxnTable()), txnID)
		smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET stock_on_hand=@p1 WHERE id=@p2`, h.cfg.PartsTable()), stockBefore, nonLotTrackedPart)
	}()
	if qty != 5 || note != "itest count correction" || lotIDNull.Valid {
		t.Errorf("ledger row: qty=%v note=%q lot_id.Valid=%v, want 5/\"itest count correction\"/false", qty, note, lotIDNull.Valid)
	}

	// Missing/zero qty.
	zeroRec := httptest.NewRecorder()
	h.PartStockAdjust(zeroRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", nonLotTrackedPart), url.Values{
		"qty": {"0"}, "reason": {"x"},
	}), nonLotTrackedPart))
	if !strings.Contains(zeroRec.Body.String(), "Enter a non-zero quantity") {
		t.Errorf("PartStockAdjust(qty=0): expected \"Enter a non-zero quantity\", got body: %s", zeroRec.Body.String())
	}

	// Missing reason.
	noReasonRec := httptest.NewRecorder()
	h.PartStockAdjust(noReasonRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", nonLotTrackedPart), url.Values{
		"qty": {"5"},
	}), nonLotTrackedPart))
	if !strings.Contains(noReasonRec.Body.String(), "A reason is required") {
		t.Errorf("PartStockAdjust(no reason): expected \"A reason is required\", got body: %s", noReasonRec.Body.String())
	}

	// Lot-tracked, pick an existing active lot (3007, lot 8301).
	const lotTrackedPart = 3007
	var lotStockBefore float64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT stock_on_hand FROM %s WHERE id=@p1`, h.cfg.PartsTable()), lotTrackedPart,
	).Scan(&lotStockBefore); err != nil {
		t.Fatalf("select stock_on_hand before (lot-tracked): %v", err)
	}
	pickRec := httptest.NewRecorder()
	h.PartStockAdjust(pickRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", lotTrackedPart), url.Values{
		"qty": {"3"}, "reason": {"itest"}, "lot_id": {"8301"},
	}), lotTrackedPart))
	assert302(t, "PartStockAdjust (existing lot)", pickRec)

	var pickTxnID int
	var pickLotID int64
	var lotStockAfter float64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT stock_on_hand FROM %s WHERE id=@p1`, h.cfg.PartsTable()), lotTrackedPart,
	).Scan(&lotStockAfter); err != nil {
		t.Fatalf("select stock_on_hand after (lot-tracked): %v", err)
	}
	if lotStockAfter != lotStockBefore+3 {
		t.Errorf("stock_on_hand (lot-tracked) after = %v, want %v", lotStockAfter, lotStockBefore+3)
	}
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 id, lot_id FROM %s WHERE part_id=@p1 AND txn_type='adjustment' ORDER BY id DESC`,
		h.cfg.InventoryTxnTable()), lotTrackedPart,
	).Scan(&pickTxnID, &pickLotID); err != nil {
		t.Fatalf("select new ledger row (lot-tracked): %v", err)
	}
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.InventoryTxnTable()), pickTxnID)
		smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET stock_on_hand=@p1 WHERE id=@p2`, h.cfg.PartsTable()), lotStockBefore, lotTrackedPart)
	}()
	if pickLotID != 8301 {
		t.Errorf("ledger row lot_id = %d, want 8301", pickLotID)
	}

	// Lot-tracked, lot belongs to a DIFFERENT part (guard from TestIntegration_LotBelongsToPart).
	wrongLotRec := httptest.NewRecorder()
	h.PartStockAdjust(wrongLotRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", lotTrackedPart), url.Values{
		"qty": {"3"}, "reason": {"itest"}, "lot_id": {"8302"},
	}), lotTrackedPart))
	if !strings.Contains(wrongLotRec.Body.String(), "Selected lot is not an active lot of this part") {
		t.Errorf("PartStockAdjust(wrong-part lot): expected guard message, got body: %s", wrongLotRec.Body.String())
	}
	var afterWrongLotStock float64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT stock_on_hand FROM %s WHERE id=@p1`, h.cfg.PartsTable()), lotTrackedPart,
	).Scan(&afterWrongLotStock); err != nil {
		t.Fatalf("select stock_on_hand after wrong-lot attempt: %v", err)
	}
	if afterWrongLotStock != lotStockAfter {
		t.Errorf("stock_on_hand changed after blocked wrong-lot adjustment: before=%v after=%v", lotStockAfter, afterWrongLotStock)
	}

	// Both lot_id and new_lot_number set.
	bothRec := httptest.NewRecorder()
	h.PartStockAdjust(bothRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", lotTrackedPart), url.Values{
		"qty": {"1"}, "reason": {"itest"}, "lot_id": {"8301"}, "new_lot_number": {"ITEST-BOTH"},
	}), lotTrackedPart))
	if !strings.Contains(bothRec.Body.String(), "Choose an existing lot or enter a new lot number, not both") {
		t.Errorf("PartStockAdjust(both lot_id+new_lot_number): expected guard message, got body: %s", bothRec.Body.String())
	}

	// Neither lot_id nor new_lot_number set.
	neitherRec := httptest.NewRecorder()
	h.PartStockAdjust(neitherRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", lotTrackedPart), url.Values{
		"qty": {"1"}, "reason": {"itest"},
	}), lotTrackedPart))
	if !strings.Contains(neitherRec.Body.String(), "select a lot or enter a new lot number") {
		t.Errorf("PartStockAdjust(no lot fields): expected guard message, got body: %s", neitherRec.Body.String())
	}

	// new_lot_number creates a brand-new lot inline.
	newLotNumber := "ITEST-NEWLOT-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	newLotRec := httptest.NewRecorder()
	h.PartStockAdjust(newLotRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", lotTrackedPart), url.Values{
		"qty": {"2"}, "reason": {"itest new lot"}, "new_lot_number": {newLotNumber},
	}), lotTrackedPart))
	assert302(t, "PartStockAdjust (new lot)", newLotRec)

	var newLotID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id FROM %s WHERE part_id=@p1 AND lot_number=@p2`, h.cfg.LotTable()), lotTrackedPart, newLotNumber,
	).Scan(&newLotID); err != nil {
		t.Fatalf("select new lot: %v", err)
	}
	var newLotDesc string
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT lot_description FROM %s WHERE id=@p1`, h.cfg.LotTable()), newLotID,
	).Scan(&newLotDesc); err != nil {
		t.Fatalf("select new lot description: %v", err)
	}
	if newLotDesc != "Manual entry" {
		t.Errorf("new lot description = %q, want %q", newLotDesc, "Manual entry")
	}
	var newLotTxnID int
	var newLotTxnLotID int64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT TOP 1 id, lot_id FROM %s WHERE part_id=@p1 AND txn_type='adjustment' ORDER BY id DESC`,
		h.cfg.InventoryTxnTable()), lotTrackedPart,
	).Scan(&newLotTxnID, &newLotTxnLotID); err != nil {
		t.Fatalf("select new-lot ledger row: %v", err)
	}
	defer func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.InventoryTxnTable()), newLotTxnID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.LotTable()), newLotID)
		smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET stock_on_hand=stock_on_hand-2 WHERE id=@p1`, h.cfg.PartsTable()), lotTrackedPart)
	}()
	if int(newLotTxnLotID) != newLotID {
		t.Errorf("new-lot ledger row lot_id = %d, want %d", newLotTxnLotID, newLotID)
	}

	// Invalid lot_id (non-numeric).
	invalidLotRec := httptest.NewRecorder()
	h.PartStockAdjust(invalidLotRec, withID(postForm(fmt.Sprintf("/part/%d/adjust-stock", lotTrackedPart), url.Values{
		"qty": {"1"}, "reason": {"itest"}, "lot_id": {"abc"},
	}), lotTrackedPart))
	if !strings.Contains(invalidLotRec.Body.String(), "Invalid lot selection") {
		t.Errorf("PartStockAdjust(invalid lot_id): expected \"Invalid lot selection\", got body: %s", invalidLotRec.Body.String())
	}
}

// TestIntegration_ResolveAttachmentFileInput exercises resolveAttachmentFileInput's
// manual-link, import, move, collision, link-existing, replace-in-place, and
// part-not-found branches (#809).

// seedThrowawayPart inserts a throwaway ITEST-prefixed part row (raw SQL, no
// handler round-trip) and returns its id, its part_number, and a cleanup that
// hard-deletes it. Shared by the #809/#821 attachment tests below, which all
// need a real part row but not the full PartsCreate handler flow that
// smoke_post_test.go's seedPart drives.
func seedThrowawayPart(t *testing.T, h *Handler, ctx context.Context, issue string) (id int, partNumber string, cleanup func()) {
	t.Helper()
	partNumber = "ITEST-" + issue + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number, revision, title, release_status, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, 'A', 'Integration Test Part', 'U', 1)`,
		h.cfg.PartsTable()), partNumber,
	).Scan(&id); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	return id, partNumber, func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.PartsTable()), id)
	}
}

// seedThrowawayAttachment inserts a throwaway part_attachment row (via
// SCOPE_IDENTITY() — OUTPUT INSERTED is blocked on this trigger-bearing table)
// and returns its id and a cleanup that hard-deletes it. Shared by the
// #809/#821 attachment tests below.
func seedThrowawayAttachment(t *testing.T, h *Handler, ctx context.Context, partID int, fileName, category string) (id int, cleanup func()) {
	t.Helper()
	if err := h.DB().QueryRowContext(ctx, h.dia().InsertReturningID(
		h.cfg.AttachmentsTable(), "part_id, file_name, category, sort_order", "@p1,@p2,@p3,1", true,
	), partID, fileName, category).Scan(&id); err != nil {
		t.Fatalf("seed part_attachment: %v", err)
	}
	return id, func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.AttachmentsTable()), id)
	}
}

// tempDocControlRoot points h.cfg.DocControlRoot at a fresh t.TempDir() and
// returns the path, so file-writing #809/#821 attachment tests never touch
// the real doc-control tree. No restore is needed: t.TempDir() is unique per
// call and every subtest that needs a non-empty DocControlRoot calls this
// (or sets its own value, e.g. "") before relying on it.
func tempDocControlRoot(t *testing.T, h *Handler) string {
	t.Helper()
	dir := t.TempDir()
	h.cfg.DocControlRoot = dir
	return dir
}

func TestIntegration_ResolveAttachmentFileInput(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, partNumber, partCleanup := seedThrowawayPart(t, h, ctx, "809")
	defer partCleanup()
	partIDStr := strconv.Itoa(partID)
	wantName := buildAttachmentFileName(partNumber, "A", "Integration Test Part", "Datasheet", ".txt")

	t.Run("manual_filfilename_no_import", func(t *testing.T) {
		req := postForm("/x", url.Values{"FILFileName": {"http://example.com/foo.pdf"}})
		result := h.resolveAttachmentFileInput(ctx, req, "999999999", "A", "Datasheet", "", "")
		if result.FileName != urlutil.NormalizeLink("http://example.com/foo.pdf") {
			t.Errorf("FileName = %q, want normalized manual link", result.FileName)
		}
		if result.MoveSrc != "" || result.Collision != nil || result.ErrMsg != "" {
			t.Errorf("unexpected side fields: %+v", result)
		}
	})

	t.Run("doc_control_root_not_configured", func(t *testing.T) {
		orig := h.cfg.DocControlRoot
		h.cfg.DocControlRoot = ""
		defer func() { h.cfg.DocControlRoot = orig }()
		req := postForm("/x", url.Values{"source_path": {`C:\some\path.txt`}})
		result := h.resolveAttachmentFileInput(ctx, req, partIDStr, "A", "Datasheet", "", "")
		if result.ErrMsg != "DOC_CONTROL_ROOT is not configured; cannot import files." {
			t.Errorf("ErrMsg = %q", result.ErrMsg)
		}
	})

	t.Run("fresh_import_copy", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		srcFile := filepath.Join(t.TempDir(), "test.txt")
		if err := os.WriteFile(srcFile, []byte("hello world"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
		req := postForm("/x", url.Values{"source_path": {srcFile}})
		result := h.resolveAttachmentFileInput(ctx, req, partIDStr, "A", "Datasheet", "", "")
		if result.ErrMsg != "" {
			t.Fatalf("unexpected ErrMsg: %s", result.ErrMsg)
		}
		if result.FileName != "LOCAL:"+wantName {
			t.Errorf("FileName = %q, want %q", result.FileName, "LOCAL:"+wantName)
		}
		if result.MoveSrc != "" {
			t.Errorf("MoveSrc = %q, want empty", result.MoveSrc)
		}
		gotBytes, err := os.ReadFile(filepath.Join(docRoot, wantName))
		if err != nil {
			t.Fatalf("read copied file: %v", err)
		}
		if string(gotBytes) != "hello world" {
			t.Errorf("copied file contents = %q, want %q", gotBytes, "hello world")
		}
	})

	t.Run("move_mode", func(t *testing.T) {
		tempDocControlRoot(t, h)
		srcFile := filepath.Join(t.TempDir(), "test.txt")
		if err := os.WriteFile(srcFile, []byte("hello world"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
		req := postForm("/x", url.Values{"source_path": {srcFile}, "move_source": {"1"}})
		result := h.resolveAttachmentFileInput(ctx, req, partIDStr, "A", "Datasheet", "", "")
		if result.ErrMsg != "" {
			t.Fatalf("unexpected ErrMsg: %s", result.ErrMsg)
		}
		if result.MoveSrc != srcFile {
			t.Errorf("MoveSrc = %q, want %q", result.MoveSrc, srcFile)
		}
	})

	t.Run("import_collision", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		srcFile := filepath.Join(t.TempDir(), "test.txt")
		if err := os.WriteFile(srcFile, []byte("new bytes"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(docRoot, wantName), []byte("original bytes"), 0644); err != nil {
			t.Fatalf("pre-create target: %v", err)
		}
		req := postForm("/x", url.Values{"source_path": {srcFile}})
		result := h.resolveAttachmentFileInput(ctx, req, partIDStr, "A", "Datasheet", "", "")
		if result.Collision == nil {
			t.Fatalf("expected Collision, got nil (result=%+v)", result)
		}
		if result.Collision["Name"] != wantName || result.Collision["SourcePath"] != srcFile {
			t.Errorf("Collision = %+v, want Name=%q SourcePath=%q", result.Collision, wantName, srcFile)
		}
		if result.FileName != "" || result.MoveSrc != "" {
			t.Errorf("expected empty FileName/MoveSrc on collision, got %+v", result)
		}
		gotBytes, err := os.ReadFile(filepath.Join(docRoot, wantName))
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(gotBytes) != "original bytes" {
			t.Errorf("target file was modified: got %q, want %q", gotBytes, "original bytes")
		}
	})

	t.Run("link_existing_skips_copy", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		srcFile := filepath.Join(t.TempDir(), "test.txt")
		if err := os.WriteFile(srcFile, []byte("hello"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
		req := postForm("/x", url.Values{"source_path": {srcFile}, "link_existing": {"1"}})
		result := h.resolveAttachmentFileInput(ctx, req, partIDStr, "A", "Datasheet", "", "")
		if result.FileName != "LOCAL:"+wantName {
			t.Errorf("FileName = %q, want %q", result.FileName, "LOCAL:"+wantName)
		}
		if _, err := os.Stat(filepath.Join(docRoot, wantName)); !os.IsNotExist(err) {
			t.Errorf("expected no file created at %s, stat err = %v", wantName, err)
		}
	})

	t.Run("replace_name_uses_replace_local_file", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		srcFile := filepath.Join(t.TempDir(), "test.txt")
		if err := os.WriteFile(srcFile, []byte("new bytes"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(docRoot, wantName), []byte("old bytes"), 0644); err != nil {
			t.Fatalf("pre-create target: %v", err)
		}
		req := postForm("/x", url.Values{"source_path": {srcFile}})
		result := h.resolveAttachmentFileInput(ctx, req, partIDStr, "A", "Datasheet", "", wantName)
		if result.Collision != nil {
			t.Errorf("expected no Collision, got %+v", result.Collision)
		}
		if result.FileName != "LOCAL:"+wantName {
			t.Errorf("FileName = %q, want %q", result.FileName, "LOCAL:"+wantName)
		}
		gotBytes, err := os.ReadFile(filepath.Join(docRoot, wantName))
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(gotBytes) != "new bytes" {
			t.Errorf("target file contents = %q, want %q (should be swapped in place)", gotBytes, "new bytes")
		}
	})

	t.Run("part_not_found", func(t *testing.T) {
		tempDocControlRoot(t, h)
		req := postForm("/x", url.Values{"source_path": {`C:\some\path.txt`}})
		result := h.resolveAttachmentFileInput(ctx, req, "99999999999999999999", "A", "Datasheet", "", "")
		if !strings.HasPrefix(result.ErrMsg, "Error loading part: ") {
			t.Errorf("ErrMsg = %q, want prefix %q", result.ErrMsg, "Error loading part: ")
		}
	})
}

// TestIntegration_DeleteAttachmentFileIfUnshared exercises the shared-attachment
// consolidation-on-delete check for both the part_attachment and company_attachment
// tables (#809) — in particular the orphan-risk case: a file still referenced by
// another active row must survive deletion of this row's reference to it.
func TestIntegration_DeleteAttachmentFileIfUnshared(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	seedPart := func(t *testing.T) (partID int, cleanup func()) {
		id, _, cl := seedThrowawayPart(t, h, ctx, "809")
		return id, cl
	}
	seedAttachment := func(t *testing.T, partID int, fileName string) (attID int, cleanup func()) {
		return seedThrowawayAttachment(t, h, ctx, partID, fileName, "Test")
	}

	t.Run("not_shared_removes_file", func(t *testing.T) {
		docRoot := t.TempDir()
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const name = "unshared.txt"
		attID, cleanupAtt := seedAttachment(t, partID, "LOCAL:"+name)
		defer cleanupAtt()
		if err := os.WriteFile(filepath.Join(docRoot, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := h.deleteAttachmentFileIfUnshared(ctx, h.cfg.AttachmentsTable(), "id", "file_name", attID, "LOCAL:"+name, docRoot, name); err != nil {
			t.Fatalf("deleteAttachmentFileIfUnshared: %v", err)
		}
		if _, err := os.Stat(filepath.Join(docRoot, name)); !os.IsNotExist(err) {
			t.Errorf("expected file removed, stat err = %v", err)
		}
	})

	t.Run("shared_preserves_file", func(t *testing.T) {
		docRoot := t.TempDir()
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const name = "shared.txt"
		att1, cleanup1 := seedAttachment(t, partID, "LOCAL:"+name)
		defer cleanup1()
		_, cleanup2 := seedAttachment(t, partID, "LOCAL:"+name)
		defer cleanup2()
		if err := os.WriteFile(filepath.Join(docRoot, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := h.deleteAttachmentFileIfUnshared(ctx, h.cfg.AttachmentsTable(), "id", "file_name", att1, "LOCAL:"+name, docRoot, name); err != nil {
			t.Fatalf("deleteAttachmentFileIfUnshared: %v", err)
		}
		if _, err := os.Stat(filepath.Join(docRoot, name)); err != nil {
			t.Errorf("expected file preserved (still shared), stat err = %v", err)
		}
	})

	t.Run("inactive_sharer_does_not_count", func(t *testing.T) {
		docRoot := t.TempDir()
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const name = "inactive-sharer.txt"
		att1, cleanup1 := seedAttachment(t, partID, "LOCAL:"+name)
		defer cleanup1()
		att2, cleanup2 := seedAttachment(t, partID, "LOCAL:"+name)
		defer cleanup2()
		if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET is_active=0 WHERE id=@p1`, h.cfg.AttachmentsTable()), att2); err != nil {
			t.Fatalf("soft-delete second row: %v", err)
		}
		if err := os.WriteFile(filepath.Join(docRoot, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := h.deleteAttachmentFileIfUnshared(ctx, h.cfg.AttachmentsTable(), "id", "file_name", att1, "LOCAL:"+name, docRoot, name); err != nil {
			t.Fatalf("deleteAttachmentFileIfUnshared: %v", err)
		}
		if _, err := os.Stat(filepath.Join(docRoot, name)); !os.IsNotExist(err) {
			t.Errorf("expected file removed (inactive sharer shouldn't count), stat err = %v", err)
		}
	})

	t.Run("already_gone_file_not_error", func(t *testing.T) {
		docRoot := t.TempDir()
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const name = "never-existed.txt"
		attID, cleanupAtt := seedAttachment(t, partID, "LOCAL:"+name)
		defer cleanupAtt()
		if err := h.deleteAttachmentFileIfUnshared(ctx, h.cfg.AttachmentsTable(), "id", "file_name", attID, "LOCAL:"+name, docRoot, name); err != nil {
			t.Errorf("expected nil error for already-gone file, got %v", err)
		}
	})

	t.Run("table_agnostic_supplier_side", func(t *testing.T) {
		docRoot := t.TempDir()
		supplierName := "ITEST-809-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		var supplierID int
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (name, is_active) OUTPUT INSERTED.id VALUES (@p1,1)`, h.cfg.CompanyTable()), supplierName,
		).Scan(&supplierID); err != nil {
			t.Fatalf("seed company: %v", err)
		}
		const name = "supplier-file.txt"
		var attID int
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (supplier_id, file_path) OUTPUT INSERTED.supplier_attachment_id VALUES (@p1,@p2)`,
			h.cfg.CompanyAttachmentsTable()), supplierID, "LOCAL:"+name,
		).Scan(&attID); err != nil {
			t.Fatalf("seed company_attachment: %v", err)
		}
		defer func() {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE supplier_attachment_id=@p1`, h.cfg.CompanyAttachmentsTable()), attID)
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.CompanyTable()), supplierID)
		}()
		if err := os.WriteFile(filepath.Join(docRoot, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := h.deleteAttachmentFileIfUnshared(ctx, h.cfg.CompanyAttachmentsTable(), "supplier_attachment_id", "file_path", attID, "LOCAL:"+name, docRoot, name); err != nil {
			t.Fatalf("deleteAttachmentFileIfUnshared: %v", err)
		}
		if _, err := os.Stat(filepath.Join(docRoot, name)); !os.IsNotExist(err) {
			t.Errorf("expected file removed, stat err = %v", err)
		}
	})
}

// TestIntegration_SetPrimaryAttachment exercises setPrimaryAttachment's set/clear
// logic for both the part and supplier (company) primary-attachment pointer (#809).
func TestIntegration_SetPrimaryAttachment(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("set_and_clear_on_part", func(t *testing.T) {
		partID, _, partCleanup := seedThrowawayPart(t, h, ctx, "809")
		defer partCleanup()
		attID, attCleanup := seedThrowawayAttachment(t, h, ctx, partID, "LOCAL:primary-test.txt", "Test")
		defer attCleanup()

		if err := h.setPrimaryAttachment(ctx, h.cfg.PartsTable(), "id", "primary_attachment_id", partID, attID); err != nil {
			t.Fatalf("setPrimaryAttachment(set): %v", err)
		}
		var gotPrimary sql.NullInt64
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT primary_attachment_id FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID).Scan(&gotPrimary); err != nil {
			t.Fatalf("select primary_attachment_id: %v", err)
		}
		if !gotPrimary.Valid || int(gotPrimary.Int64) != attID {
			t.Errorf("primary_attachment_id = %+v, want %d", gotPrimary, attID)
		}

		if err := h.setPrimaryAttachment(ctx, h.cfg.PartsTable(), "id", "primary_attachment_id", partID, nil); err != nil {
			t.Fatalf("setPrimaryAttachment(clear): %v", err)
		}
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT primary_attachment_id FROM %s WHERE id=@p1`, h.cfg.PartsTable()), partID).Scan(&gotPrimary); err != nil {
			t.Fatalf("select primary_attachment_id after clear: %v", err)
		}
		if gotPrimary.Valid {
			t.Errorf("primary_attachment_id = %+v after clear, want NULL", gotPrimary)
		}
	})

	t.Run("set_on_supplier", func(t *testing.T) {
		supplierName := "ITEST-809-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		var supplierID int
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (name, is_active) OUTPUT INSERTED.id VALUES (@p1,1)`, h.cfg.CompanyTable()), supplierName,
		).Scan(&supplierID); err != nil {
			t.Fatalf("seed company: %v", err)
		}
		var attID int
		defer func() {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE supplier_attachment_id=@p1`, h.cfg.CompanyAttachmentsTable()), attID)
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.CompanyTable()), supplierID)
		}()
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (supplier_id, file_path) OUTPUT INSERTED.supplier_attachment_id VALUES (@p1,@p2)`,
			h.cfg.CompanyAttachmentsTable()), supplierID, "LOCAL:supplier-primary-test.txt",
		).Scan(&attID); err != nil {
			t.Fatalf("seed company_attachment: %v", err)
		}

		if err := h.setPrimaryAttachment(ctx, h.cfg.CompanyTable(), "id", "primary_attachment_id", supplierID, attID); err != nil {
			t.Fatalf("setPrimaryAttachment: %v", err)
		}
		var gotPrimary sql.NullInt64
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT primary_attachment_id FROM %s WHERE id=@p1`, h.cfg.CompanyTable()), supplierID).Scan(&gotPrimary); err != nil {
			t.Fatalf("select primary_attachment_id: %v", err)
		}
		if !gotPrimary.Valid || int(gotPrimary.Int64) != attID {
			t.Errorf("primary_attachment_id = %+v, want %d", gotPrimary, attID)
		}
	})

	t.Run("noop_on_nonexistent_parent", func(t *testing.T) {
		if err := h.setPrimaryAttachment(ctx, h.cfg.PartsTable(), "id", "primary_attachment_id", 999999999, nil); err != nil {
			t.Errorf("expected nil error for nonexistent parentID, got %v", err)
		}
	})
}

// TestIntegration_APIPartAttachmentName exercises the attachment-name preview
// endpoint's happy path and part-not-found guard (#821).
func TestIntegration_APIPartAttachmentName(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	partID, partNumber, partCleanup := seedThrowawayPart(t, h, ctx, "821")
	defer partCleanup()

	t.Run("happy_path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/part/%d/attachment-name?rev=B&category=Drawing&ext=.pdf", partID), nil)
		rec := httptest.NewRecorder()
		h.APIPartAttachmentName(rec, withID(req, partID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		want := buildAttachmentFileName(partNumber, "B", "Integration Test Part", "Drawing", ".pdf")
		if body.Name != want {
			t.Errorf("name = %q, want %q", body.Name, want)
		}
	})

	t.Run("part_not_found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/part/999999999/attachment-name", nil)
		rec := httptest.NewRecorder()
		h.APIPartAttachmentName(rec, withID(req, 999999999))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404. body: %s", rec.Code, rec.Body.String())
		}
	})
}

// TestIntegration_APIPartPasteAttachment exercises the clipboard-paste-as-new-
// attachment endpoint: happy path (file write + row insert), config guard,
// decode-error guard, part-not-found guard, and the non-numeric order_id
// leaving sort_order NULL (#821).
func TestIntegration_APIPartPasteAttachment(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	// seedPart wraps seedThrowawayPart with an extra cleanup step: the
	// APIPartPasteAttachment calls below create their own part_attachment row
	// via the handler (not via seedThrowawayAttachment), so it must be swept up
	// by part_id alongside the part itself.
	seedPart := func(t *testing.T) (partID int, partNumber string, cleanup func()) {
		id, num, partCleanup := seedThrowawayPart(t, h, ctx, "821")
		return id, num, func() {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), id)
			partCleanup()
		}
	}

	postPaste := func(partID int, jsonBody string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/part/%d/paste-attachment", partID), strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.APIPartPasteAttachment(rec, withID(req, partID))
		return rec
	}

	t.Run("happy_path", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, partNumber, cleanupPart := seedPart(t)
		defer cleanupPart()

		rec := postPaste(partID, fmt.Sprintf(`{"image_data":%q,"rev":"B","order_id":"3","comment":"note"}`, tinyPNG))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK {
			t.Fatalf("response = %s, want ok:true", rec.Body.String())
		}

		wantName := buildAttachmentFileName(partNumber, "B", "Integration Test Part", "Photo", ".png")
		if _, err := os.Stat(filepath.Join(h.cfg.DocControlRoot, wantName)); err != nil {
			t.Fatalf("expected file at %s, stat err = %v", wantName, err)
		}

		var fileName, category, comment sql.NullString
		var sortOrder sql.NullInt64
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT file_name, category, comment, sort_order FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), partID,
		).Scan(&fileName, &category, &comment, &sortOrder); err != nil {
			t.Fatalf("select attachment row: %v", err)
		}
		if fileName.String != "LOCAL:"+wantName {
			t.Errorf("file_name = %q, want %q", fileName.String, "LOCAL:"+wantName)
		}
		if category.String != "Photo" {
			t.Errorf("category = %q, want %q", category.String, "Photo")
		}
		if comment.String != "note" {
			t.Errorf("comment = %q, want %q", comment.String, "note")
		}
		if !sortOrder.Valid || sortOrder.Int64 != 3 {
			t.Errorf("sort_order = %+v, want 3", sortOrder)
		}
	})

	t.Run("doc_control_root_not_configured", func(t *testing.T) {
		orig := h.cfg.DocControlRoot
		h.cfg.DocControlRoot = ""
		defer func() { h.cfg.DocControlRoot = orig }()
		partID, _, cleanupPart := seedPart(t)
		defer cleanupPart()
		rec := postPaste(partID, fmt.Sprintf(`{"image_data":%q}`, tinyPNG))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed_image_data", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, _, cleanupPart := seedPart(t)
		defer cleanupPart()
		rec := postPaste(partID, `{"image_data":"not-a-data-url"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("part_not_found", func(t *testing.T) {
		tempDocControlRoot(t, h)
		rec := postPaste(999999999, fmt.Sprintf(`{"image_data":%q}`, tinyPNG))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("non_numeric_order_id", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, _, cleanupPart := seedPart(t)
		defer cleanupPart()
		rec := postPaste(partID, fmt.Sprintf(`{"image_data":%q,"order_id":"abc"}`, tinyPNG))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		var sortOrder sql.NullInt64
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT sort_order FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), partID,
		).Scan(&sortOrder); err != nil {
			t.Fatalf("select attachment row: %v", err)
		}
		if sortOrder.Valid {
			t.Errorf("sort_order = %+v, want NULL", sortOrder)
		}
	})
}

// TestIntegration_APIPartPasteAttachmentReplace exercises the clipboard-paste-
// replace endpoint: happy path (old file deleted, new file written, row
// updated), the shared-old-file soft-warning path, and its guard clauses (#821).
func TestIntegration_APIPartPasteAttachmentReplace(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	seedPart := func(t *testing.T) (partID int, cleanup func()) {
		id, _, partCleanup := seedThrowawayPart(t, h, ctx, "821")
		return id, func() {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), id)
			partCleanup()
		}
	}
	seedAttachment := func(t *testing.T, partID int, fileName string) int {
		id, _ := seedThrowawayAttachment(t, h, ctx, partID, fileName, "Photo")
		return id
	}

	postReplace := func(partID, attID int, jsonBody string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/api/part/%d/attachments/%d/paste-attachment", partID, attID), strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.APIPartPasteAttachmentReplace(rec, withIDAndAttID(req, partID, attID))
		return rec
	}

	t.Run("happy_path", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const oldName = "old-file.png"
		if _, err := writeIntoDocControl(docRoot, oldName, []byte("old bytes")); err != nil {
			t.Fatalf("seed old file: %v", err)
		}
		attID := seedAttachment(t, partID, "LOCAL:"+oldName)

		rec := postReplace(partID, attID, fmt.Sprintf(`{"image_data":%q,"rev":"C","order_id":"2","comment":"new note"}`, tinyPNG))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "warning") {
			t.Errorf("unexpected warning in response: %s", rec.Body.String())
		}

		var fileName, category, comment, rev sql.NullString
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT file_name, category, comment, part_revision FROM %s WHERE id=@p1`, h.cfg.AttachmentsTable()), attID,
		).Scan(&fileName, &category, &comment, &rev); err != nil {
			t.Fatalf("select attachment row: %v", err)
		}
		if fileName.String == "LOCAL:"+oldName {
			t.Errorf("file_name unchanged, want updated")
		}
		if category.String != "Photo" || comment.String != "new note" || rev.String != "C" {
			t.Errorf("row = category=%q comment=%q rev=%q, want Photo/new note/C", category.String, comment.String, rev.String)
		}

		if _, err := os.Stat(filepath.Join(docRoot, oldName)); !os.IsNotExist(err) {
			t.Errorf("expected old file removed, stat err = %v", err)
		}
		newName := strings.TrimPrefix(fileName.String, "LOCAL:")
		if _, err := os.Stat(filepath.Join(docRoot, newName)); err != nil {
			t.Errorf("expected new file at %s, stat err = %v", newName, err)
		}
	})

	// deleteAttachmentFileIfUnshared treats "still shared" as success (nil error,
	// file left in place) — the response's "warning" key only fires when that
	// call returns an actual error (e.g. a permission failure removing the file),
	// which sharing is not. So a shared old file is a plain {"ok":true} response;
	// the file being preserved on disk is the meaningful assertion here.
	t.Run("shared_old_file_preserved", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const sharedName = "shared-file.png"
		if _, err := writeIntoDocControl(docRoot, sharedName, []byte("shared bytes")); err != nil {
			t.Fatalf("seed shared file: %v", err)
		}
		attID := seedAttachment(t, partID, "LOCAL:"+sharedName)
		seedAttachment(t, partID, "LOCAL:"+sharedName) // second row sharing the same file

		rec := postReplace(partID, attID, fmt.Sprintf(`{"image_data":%q}`, tinyPNG))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "warning") {
			t.Errorf("unexpected warning in response: %s", rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(docRoot, sharedName)); err != nil {
			t.Errorf("expected shared old file preserved, stat err = %v", err)
		}
	})

	t.Run("invalid_attID", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/api/part/%d/attachments/abc/paste-attachment", partID),
			strings.NewReader(fmt.Sprintf(`{"image_data":%q}`, tinyPNG)))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.Itoa(partID))
		rctx.URLParams.Add("attID", "abc")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.APIPartPasteAttachmentReplace(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("doc_control_root_not_configured", func(t *testing.T) {
		orig := h.cfg.DocControlRoot
		h.cfg.DocControlRoot = ""
		defer func() { h.cfg.DocControlRoot = orig }()
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		rec := postReplace(partID, 1, fmt.Sprintf(`{"image_data":%q}`, tinyPNG))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("attachment_not_found", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		rec := postReplace(partID, 999999999, fmt.Sprintf(`{"image_data":%q}`, tinyPNG))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed_image_data", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		attID := seedAttachment(t, partID, "LOCAL:some-file.png")
		rec := postReplace(partID, attID, `{"image_data":"not-a-data-url"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})
}

// TestIntegration_APIPartGenerateThumbnail exercises real end-to-end PDF-to-PNG
// thumbnail generation (PDFium runs as embedded WASM, no external binary
// needed) against a minimal hand-built one-page PDF: happy path, the #839
// re-run-in-place behavior, and every guard clause (#821).
func TestIntegration_APIPartGenerateThumbnail(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Minimal valid one-page PDF (200x300pt MediaBox, no content stream). PDFium's
	// recovery parser rebuilds the object table by scanning for "N G obj" markers
	// when the (deliberately absent/invalid) xref can't be parsed directly.
	minimalPDF := []byte("%PDF-1.1\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 300]>>endobj\n" +
		"trailer<</Size 4/Root 1 0 R>>\nstartxref\n0\n%%EOF")

	seedPart := func(t *testing.T) (partID int, cleanup func()) {
		id, _, partCleanup := seedThrowawayPart(t, h, ctx, "821")
		return id, func() {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), id)
			partCleanup()
		}
	}
	seedAttachment := func(t *testing.T, partID int, fileName, category string) int {
		id, _ := seedThrowawayAttachment(t, h, ctx, partID, fileName, category)
		return id
	}

	postThumbnail := func(partID, attID int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/api/part/%d/attachments/%d/generate-thumbnail", partID, attID), nil)
		rec := httptest.NewRecorder()
		h.APIPartGenerateThumbnail(rec, withIDAndAttID(req, partID, attID))
		return rec
	}

	type generatedRow struct {
		id       int
		fileName string
	}
	checkGenerated := func(t *testing.T, docRoot string, partID int, wantMaxPx map[string]int) map[string]generatedRow {
		t.Helper()
		rows, err := h.DB().QueryContext(ctx, fmt.Sprintf(
			`SELECT id, category, file_name FROM %s WHERE part_id=@p1 AND category IN (@p2,@p3) AND is_active=%s`,
			h.cfg.AttachmentsTable(), h.dia().BoolLiteral(true)), partID, previewCategory, thumbnailCategory)
		if err != nil {
			t.Fatalf("query generated rows: %v", err)
		}
		defer rows.Close()
		out := map[string]generatedRow{}
		for rows.Next() {
			var id int
			var category, fileName string
			if err := rows.Scan(&id, &category, &fileName); err != nil {
				t.Fatalf("scan generated row: %v", err)
			}
			out[category] = generatedRow{id, fileName}
		}
		for _, cat := range []string{previewCategory, thumbnailCategory} {
			row, ok := out[cat]
			if !ok {
				t.Fatalf("no %s row created", cat)
			}
			name := strings.TrimPrefix(row.fileName, "LOCAL:")
			f, err := os.Open(filepath.Join(docRoot, name))
			if err != nil {
				t.Fatalf("open %s file: %v", cat, err)
			}
			img, err := png.Decode(f)
			f.Close()
			if err != nil {
				t.Fatalf("decode %s image: %v", cat, err)
			}
			b := img.Bounds()
			longEdge := b.Dx()
			if b.Dy() > longEdge {
				longEdge = b.Dy()
			}
			if longEdge > wantMaxPx[cat] {
				t.Errorf("%s long edge = %d, want <= %d", cat, longEdge, wantMaxPx[cat])
			}
		}
		return out
	}

	t.Run("happy_path_and_rerun_in_place", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		const pdfName = "source.pdf"
		if _, err := writeIntoDocControl(docRoot, pdfName, minimalPDF); err != nil {
			t.Fatalf("seed pdf file: %v", err)
		}
		attID := seedAttachment(t, partID, "LOCAL:"+pdfName, "Drawing")

		rec := postThumbnail(partID, attID)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		wantMaxPx := map[string]int{previewCategory: 800, thumbnailCategory: 250}
		first := checkGenerated(t, docRoot, partID, wantMaxPx)

		// Re-run in place (#839): same two row ids reused, files overwritten with the same names.
		rec = postThumbnail(partID, attID)
		if rec.Code != http.StatusOK {
			t.Fatalf("second run status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		second := checkGenerated(t, docRoot, partID, wantMaxPx)
		for _, cat := range []string{previewCategory, thumbnailCategory} {
			if second[cat].id != first[cat].id {
				t.Errorf("%s: row id changed on re-run: %d -> %d, want same id reused", cat, first[cat].id, second[cat].id)
			}
			if second[cat].fileName != first[cat].fileName {
				t.Errorf("%s: file_name changed on re-run: %q -> %q, want same name reused", cat, first[cat].fileName, second[cat].fileName)
			}
		}
	})

	t.Run("invalid_attID", func(t *testing.T) {
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/api/part/%d/attachments/abc/generate-thumbnail", partID), nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.Itoa(partID))
		rctx.URLParams.Add("attID", "abc")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.APIPartGenerateThumbnail(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("doc_control_root_not_configured", func(t *testing.T) {
		orig := h.cfg.DocControlRoot
		h.cfg.DocControlRoot = ""
		defer func() { h.cfg.DocControlRoot = orig }()
		rec := postThumbnail(1, 1)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("attachment_not_found", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		rec := postThumbnail(partID, 999999999)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("source_not_local_pdf", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()

		urlAttID := seedAttachment(t, partID, "https://example.com/spec.pdf", "Drawing")
		rec := postThumbnail(partID, urlAttID)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("URL source: status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}

		txtAttID := seedAttachment(t, partID, "LOCAL:notes.txt", "Drawing")
		rec = postThumbnail(partID, txtAttID)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("non-PDF local source: status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("local_pdf_missing_on_disk", func(t *testing.T) {
		tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		attID := seedAttachment(t, partID, "LOCAL:missing.pdf", "Drawing")
		rec := postThumbnail(partID, attID)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500. body: %s", rec.Code, rec.Body.String())
		}
	})
}

// TestIntegration_UpsertGeneratedAttachment exercises upsertGeneratedAttachment's
// find-or-create semantics directly, isolated from the PDF render pipeline (#821).
func TestIntegration_UpsertGeneratedAttachment(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	seedPart := func(t *testing.T) (partID int, cleanup func()) {
		id, _, partCleanup := seedThrowawayPart(t, h, ctx, "821")
		return id, func() {
			_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE part_id=@p1`, h.cfg.AttachmentsTable()), id)
			partCleanup()
		}
	}
	seedAttachment := func(t *testing.T, partID int, fileName, category string) int {
		id, _ := seedThrowawayAttachment(t, h, ctx, partID, fileName, category)
		return id
	}

	t.Run("insert_path", func(t *testing.T) {
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		partIDStr := strconv.Itoa(partID)

		if err := h.upsertGeneratedAttachment(ctx, partIDStr, "A", thumbnailCategory, "LOCAL:new.png"); err != nil {
			t.Fatalf("upsertGeneratedAttachment: %v", err)
		}
		var fileName string
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT file_name FROM %s WHERE part_id=@p1 AND category=@p2`, h.cfg.AttachmentsTable()), partID, thumbnailCategory,
		).Scan(&fileName); err != nil {
			t.Fatalf("select inserted row: %v", err)
		}
		if fileName != "LOCAL:new.png" {
			t.Errorf("file_name = %q, want %q", fileName, "LOCAL:new.png")
		}
	})

	t.Run("update_path_not_shared", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		partIDStr := strconv.Itoa(partID)
		const oldName = "old-thumb.png"
		if _, err := writeIntoDocControl(docRoot, oldName, []byte("old")); err != nil {
			t.Fatalf("seed old file: %v", err)
		}
		attID := seedAttachment(t, partID, "LOCAL:"+oldName, thumbnailCategory)

		if err := h.upsertGeneratedAttachment(ctx, partIDStr, "B", thumbnailCategory, "LOCAL:new-thumb.png"); err != nil {
			t.Fatalf("upsertGeneratedAttachment: %v", err)
		}
		var gotID int
		var fileName string
		if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
			`SELECT id, file_name FROM %s WHERE part_id=@p1 AND category=@p2`, h.cfg.AttachmentsTable()), partID, thumbnailCategory,
		).Scan(&gotID, &fileName); err != nil {
			t.Fatalf("select updated row: %v", err)
		}
		if gotID != attID {
			t.Errorf("row id changed: got %d, want %d (same row updated in place)", gotID, attID)
		}
		if fileName != "LOCAL:new-thumb.png" {
			t.Errorf("file_name = %q, want %q", fileName, "LOCAL:new-thumb.png")
		}
		if _, err := os.Stat(filepath.Join(docRoot, oldName)); !os.IsNotExist(err) {
			t.Errorf("expected old file removed, stat err = %v", err)
		}
	})

	t.Run("update_path_shared_old_file_preserved", func(t *testing.T) {
		docRoot := tempDocControlRoot(t, h)
		partID, cleanupPart := seedPart(t)
		defer cleanupPart()
		partIDStr := strconv.Itoa(partID)
		const sharedName = "shared-thumb.png"
		if _, err := writeIntoDocControl(docRoot, sharedName, []byte("shared")); err != nil {
			t.Fatalf("seed shared file: %v", err)
		}
		seedAttachment(t, partID, "LOCAL:"+sharedName, thumbnailCategory)
		seedAttachment(t, partID, "LOCAL:"+sharedName, "Photo") // unrelated row sharing the same filename

		if err := h.upsertGeneratedAttachment(ctx, partIDStr, "C", thumbnailCategory, "LOCAL:fresh-thumb.png"); err != nil {
			t.Fatalf("upsertGeneratedAttachment: %v", err)
		}
		if _, err := os.Stat(filepath.Join(docRoot, sharedName)); err != nil {
			t.Errorf("expected shared old file preserved, stat err = %v", err)
		}
	})
}

// TestIntegration_PasteResultImageWrite covers APIRecordPasteResultImage's
// actual write path (filename returned, file written with correct bytes) and
// its decode-error guard, which TestIntegration_PasteResultImageGuards
// explicitly does not reach — that test only exercises the not-found/locked
// guards that return before any filesystem write (#821).
func TestIntegration_PasteResultImageWrite(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	h.cfg.ImageRoot = t.TempDir()

	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	partID, partNumber, partCleanup := seedThrowawayPart(t, h, ctx, "821")
	defer partCleanup()

	var formID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (part_number_id, test_order, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, '', 0, 1)`, h.cfg.FormsTable()), partID,
	).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	defer func() {
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE form_id=@p1`, h.cfg.RecordsTable()), formID)
		_, _ = h.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg.FormsTable()), formID)
	}()

	const serial = "ITEST-821-UNLOCKED"
	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, serial_number, subject_part_number, subject_pn_description, is_locked, is_active)
		 OUTPUT INSERTED.id VALUES (@p1, @p2, '', '', 0, 1)`, h.cfg.RecordsTable()), formID, serial,
	).Scan(&recordID); err != nil {
		t.Fatalf("seed unlocked form_record: %v", err)
	}

	postPasteImage := func(jsonBody string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/api/record/%d/step/1/paste-image", recordID),
			strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.Itoa(recordID))
		rctx.URLParams.Add("tid", "1")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.APIRecordPasteResultImage(rec, req)
		return rec
	}

	t.Run("happy_path", func(t *testing.T) {
		rec := postPasteImage(fmt.Sprintf(`{"image_data":%q}`, tinyPNG))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			OK       bool   `json:"ok"`
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK {
			t.Fatalf("response = %s, want ok:true", rec.Body.String())
		}
		re := regexp.MustCompile(fmt.Sprintf(`^SN%s_rID%d_tID1_\d{8}_\d{6}\.png$`, serial, recordID))
		if !re.MatchString(resp.Filename) {
			t.Errorf("filename = %q, want to match %s", resp.Filename, re.String())
		}
		gotBytes, err := os.ReadFile(filepath.Join(h.cfg.ImageRoot, sanitizeFileNamePart(partNumber), resp.Filename))
		if err != nil {
			t.Fatalf("read written image: %v", err)
		}
		wantBytes, _ := base64.StdEncoding.DecodeString(strings.SplitN(tinyPNG, ",", 2)[1])
		if string(gotBytes) != string(wantBytes) {
			t.Errorf("written image bytes mismatch")
		}
	})

	t.Run("malformed_image_data", func(t *testing.T) {
		rec := postPasteImage(`{"image_data":"not-a-data-url"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400. body: %s", rec.Code, rec.Body.String())
		}
	})
}

// csvRowsContain reports whether rows contains a row exactly equal to want.
func csvRowsContain(rows [][]string, want []string) bool {
	for _, row := range rows {
		if len(row) != len(want) {
			continue
		}
		match := true
		for i := range row {
			if row[i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestIntegration_QuerySpendBySupplier verifies querySpendBySupplier's
// aggregation and descending sort against pinned seed po_line/purchase_order
// rows (#815).
func TestIntegration_QuerySpendBySupplier(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rows, err := h.querySpendBySupplier(context.Background(), reportDateRange{})
	if err != nil {
		t.Fatalf("querySpendBySupplier: %v", err)
	}
	byName := map[string]float64{}
	var order []string
	for _, row := range rows {
		byName[row.SupplierName] = row.TotalSpend
		order = append(order, row.SupplierName)
	}
	assertFloatEqual(t, "Acme Fasteners total spend", byName["Acme Fasteners"], 282.50)
	assertFloatEqual(t, "Precision Machining Co total spend", byName["Precision Machining Co"], 108.00)

	acmeIdx, precisionIdx := -1, -1
	for i, name := range order {
		if name == "Acme Fasteners" {
			acmeIdx = i
		}
		if name == "Precision Machining Co" {
			precisionIdx = i
		}
	}
	if acmeIdx == -1 || precisionIdx == -1 || acmeIdx > precisionIdx {
		t.Errorf("expected Acme Fasteners before Precision Machining Co in descending spend order, got %v", order)
	}
}

// TestIntegration_QuerySpendByPart verifies querySpendByPart's aggregation
// and descending sort against pinned seed po_line rows (#815).
func TestIntegration_QuerySpendByPart(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rows, err := h.querySpendByPart(context.Background(), reportDateRange{})
	if err != nil {
		t.Fatalf("querySpendByPart: %v", err)
	}
	byPart := map[string]float64{}
	var order []string
	for _, row := range rows {
		byPart[row.PartNumber] = row.TotalSpend
		order = append(order, row.PartNumber)
	}
	assertFloatEqual(t, "RAW-1002 total spend", byPart["RAW-1002"], 205.00)
	assertFloatEqual(t, "RAW-1001 total spend", byPart["RAW-1001"], 100.00)
	assertFloatEqual(t, "BUY-1001 total spend", byPart["BUY-1001"], 85.50)

	want := []string{"RAW-1002", "RAW-1001", "BUY-1001"}
	idx := map[string]int{}
	for i, name := range order {
		idx[name] = i
	}
	for i := 1; i < len(want); i++ {
		if idx[want[i-1]] >= idx[want[i]] {
			t.Errorf("expected %v in descending spend order, got %v", want, order)
			break
		}
	}
}

// TestIntegration_QueryOnTimeDelivery verifies queryOnTimeDelivery's on-time
// percentage and avg-days-late math against the one seed po_line row
// (5504, part of PO 5003/Acme Fasteners) that has lead_time_days,
// date_received, and its PO's date_ordered all set (#815). Requires ArxDev
// reseeded with the lead_time_days=50 addition to po_line 5504 in
// SQL/seed_test_data.sql — until reseeded this test fails against the old
// (lead_time_days IS NULL) row.
func TestIntegration_QueryOnTimeDelivery(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rows, err := h.queryOnTimeDelivery(context.Background(), reportDateRange{})
	if err != nil {
		t.Fatalf("queryOnTimeDelivery: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (rows=%+v)", len(rows), rows)
	}
	row := rows[0]
	if row.SupplierName != "Acme Fasteners" {
		t.Errorf("SupplierName = %q, want %q", row.SupplierName, "Acme Fasteners")
	}
	if row.TotalLines != 1 {
		t.Errorf("TotalLines = %d, want 1", row.TotalLines)
	}
	if row.OnTimeLines != 1 {
		t.Errorf("OnTimeLines = %d, want 1", row.OnTimeLines)
	}
	assertFloatEqual(t, "OnTimePct", row.OnTimePct, 100.0)
	assertFloatEqual(t, "AvgDaysLate", row.AvgDaysLate, -6.0)
}

// TestIntegration_QueryPOCycleTime verifies queryPOCycleTime's LEAD-paired
// stage-duration math against the one seed purchase_order_history pair
// (5801 draft -> 5804 open, PO 5002) that has both an entry and an exit (#815).
func TestIntegration_QueryPOCycleTime(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	rows, err := h.queryPOCycleTime(context.Background(), reportDateRange{})
	if err != nil {
		t.Fatalf("queryPOCycleTime: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (rows=%+v)", len(rows), rows)
	}
	row := rows[0]
	if row.Stage != "draft" {
		t.Errorf("Stage = %q, want %q", row.Stage, "draft")
	}
	if row.POCount != 1 {
		t.Errorf("POCount = %d, want 1", row.POCount)
	}
	assertFloatEqual(t, "AvgDays", row.AvgDays, 3.0)
}

// TestIntegration_QueryDataQualityParts verifies the three data-quality gap
// queries against pinned seed part rows (#815).
func TestIntegration_QueryDataQualityParts(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	containsPN := func(rows []dataQualityPartRow, pn string) bool {
		for _, r := range rows {
			if r.PartNumber == pn {
				return true
			}
		}
		return false
	}

	t.Run("no_attachments", func(t *testing.T) {
		rows, err := h.queryPartsNoAttachments(ctx)
		if err != nil {
			t.Fatalf("queryPartsNoAttachments: %v", err)
		}
		for _, want := range []string{"BUY-1002", "BUY-1003", "ASM-1001", "ASM-1002", "ASM-1003"} {
			if !containsPN(rows, want) {
				t.Errorf("expected %s present, rows=%+v", want, rows)
			}
		}
		for _, notWant := range []string{"BUY-1001", "BUY-1004"} {
			if containsPN(rows, notWant) {
				t.Errorf("expected %s absent, rows=%+v", notWant, rows)
			}
		}
	})

	t.Run("missing_default_supplier", func(t *testing.T) {
		rows, err := h.queryPartsMissingDefaultSupplier(ctx)
		if err != nil {
			t.Fatalf("queryPartsMissingDefaultSupplier: %v", err)
		}
		if len(rows) != 1 || rows[0].PartNumber != "BUY-1003" {
			t.Errorf("rows = %+v, want exactly one row for BUY-1003", rows)
		}
	})

	t.Run("stale_rollup", func(t *testing.T) {
		rows, err := h.queryPartsStaleRollup(ctx)
		if err != nil {
			t.Fatalf("queryPartsStaleRollup: %v", err)
		}
		if !containsPN(rows, "RAW-1002") {
			t.Errorf("expected RAW-1002 present, rows=%+v", rows)
		}
		for _, notWant := range []string{"ASM-1001", "ASM-1002"} {
			if containsPN(rows, notWant) {
				t.Errorf("expected %s absent (has a rollup), rows=%+v", notWant, rows)
			}
		}
	})
}

// TestIntegration_ReportsHandlers_EndToEnd exercises ReportsSpend, ReportsOnTime,
// ReportsCycleTime, and ReportsDataQuality's page-handler assembly (query +
// template render), asserting a clean 200 with no renderError output (#815).
func TestIntegration_ReportsHandlers_EndToEnd(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	cases := []struct {
		name   string
		fn     http.HandlerFunc
		target string
	}{
		{"ReportsSpend", h.ReportsSpend, "/reports/spend?range=custom"},
		{"ReportsOnTime", h.ReportsOnTime, "/reports/on-time?range=custom"},
		{"ReportsCycleTime", h.ReportsCycleTime, "/reports/cycle-time?range=custom"},
		{"ReportsDataQuality", h.ReportsDataQuality, "/reports/data-quality"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.target, nil)
			rec := httptest.NewRecorder()
			c.fn(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "Error loading") {
				t.Errorf("response body contains an error: %s", rec.Body.String())
			}
		})
	}
}

// TestIntegration_ReportsSpendBySupplierExportCSV verifies the spend-by-supplier
// CSV export's header and a pinned data row (#815).
func TestIntegration_ReportsSpendBySupplierExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/spend/export-suppliers.csv?range=custom", nil)
	rec := httptest.NewRecorder()
	h.ReportsSpendBySupplierExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 || !csvRowsContain(records[:1], []string{"Supplier", "Total Spend"}) {
		t.Fatalf("header row = %v, want [Supplier Total Spend]", records)
	}
	if !csvRowsContain(records[1:], []string{"Acme Fasteners", "282.50"}) {
		t.Errorf("rows = %v, want to contain [Acme Fasteners 282.50]", records[1:])
	}
}

// TestIntegration_ReportsSpendByPartExportCSV verifies the spend-by-part CSV
// export's header and a pinned data row (#815).
func TestIntegration_ReportsSpendByPartExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/spend/export-parts.csv?range=custom", nil)
	rec := httptest.NewRecorder()
	h.ReportsSpendByPartExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 || !csvRowsContain(records[:1], []string{"Part Number", "Title", "Total Spend"}) {
		t.Fatalf("header row = %v, want [Part Number Title Total Spend]", records)
	}
	if !csvRowsContain(records[1:], []string{"RAW-1002", "Stainless Steel Bar Stock", "205.00"}) {
		t.Errorf("rows = %v, want to contain RAW-1002/Stainless Steel Bar Stock/205.00", records[1:])
	}
}

// TestIntegration_ReportsOnTimeExportCSV verifies the on-time delivery CSV
// export's header and pinned data row (#815). Requires the same ArxDev reseed
// as TestIntegration_QueryOnTimeDelivery.
func TestIntegration_ReportsOnTimeExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/on-time/export.csv?range=custom", nil)
	rec := httptest.NewRecorder()
	h.ReportsOnTimeExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	wantHeader := []string{"Supplier", "Total Lines", "On-Time Lines", "On-Time %", "Avg Days Late"}
	if len(records) == 0 || !csvRowsContain(records[:1], wantHeader) {
		t.Fatalf("header row = %v, want %v", records, wantHeader)
	}
	if !csvRowsContain(records[1:], []string{"Acme Fasteners", "1", "1", "100.0", "-6.0"}) {
		t.Errorf("rows = %v, want to contain [Acme Fasteners 1 1 100.0 -6.0]", records[1:])
	}
}

// TestIntegration_ReportsCycleTimeExportCSV verifies the PO cycle time CSV
// export's header and pinned data row (#815).
func TestIntegration_ReportsCycleTimeExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/cycle-time/export.csv?range=custom", nil)
	rec := httptest.NewRecorder()
	h.ReportsCycleTimeExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 || !csvRowsContain(records[:1], []string{"Stage", "PO Count", "Avg Days"}) {
		t.Fatalf("header row = %v, want [Stage PO Count Avg Days]", records)
	}
	if !csvRowsContain(records[1:], []string{"draft", "1", "3.0"}) {
		t.Errorf("rows = %v, want to contain [draft 1 3.0]", records[1:])
	}
}

// TestIntegration_ReportsDataQualityNoAttachmentsExportCSV verifies the
// missing-attachments CSV export's header and a pinned data row (#815).
func TestIntegration_ReportsDataQualityNoAttachmentsExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/data-quality/export-no-attachments.csv", nil)
	rec := httptest.NewRecorder()
	h.ReportsDataQualityNoAttachmentsExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 || !csvRowsContain(records[:1], []string{"Part Number", "Title", "Category"}) {
		t.Fatalf("header row = %v, want [Part Number Title Category]", records)
	}
	found := false
	for _, row := range records[1:] {
		if len(row) > 0 && row[0] == "BUY-1002" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("rows = %v, want a row starting with BUY-1002", records[1:])
	}
}

// TestIntegration_ReportsDataQualityMissingSupplierExportCSV verifies the
// missing-default-supplier CSV export's header and its single data row (#815).
func TestIntegration_ReportsDataQualityMissingSupplierExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/data-quality/export-missing-supplier.csv", nil)
	rec := httptest.NewRecorder()
	h.ReportsDataQualityMissingSupplierExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 || !csvRowsContain(records[:1], []string{"Part Number", "Title", "Category"}) {
		t.Fatalf("header row = %v, want [Part Number Title Category]", records)
	}
	dataRows := records[1:]
	if len(dataRows) != 1 || len(dataRows[0]) == 0 || dataRows[0][0] != "BUY-1003" {
		t.Errorf("data rows = %v, want exactly one row starting with BUY-1003", dataRows)
	}
}

// TestIntegration_ReportsDataQualityStaleRollupExportCSV verifies the
// no/stale-rollup CSV export's header and a pinned data row (#815).
func TestIntegration_ReportsDataQualityStaleRollupExportCSV(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/reports/data-quality/export-stale-rollup.csv", nil)
	rec := httptest.NewRecorder()
	h.ReportsDataQualityStaleRollupExportCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 || !csvRowsContain(records[:1], []string{"Part Number", "Title", "Category"}) {
		t.Fatalf("header row = %v, want [Part Number Title Category]", records)
	}
	found := false
	for _, row := range records[1:] {
		if len(row) > 0 && row[0] == "RAW-1002" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("rows = %v, want a row starting with RAW-1002", records[1:])
	}
}
