//go:build integration

package main

import (
	"context"
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

	h := New(database, cfg, nil, nil)

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
		"FILNotes":   {"integration-test-attachment"},
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
