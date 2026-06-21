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
			fmt.Sprintf(`DELETE FROM %s WHERE PNID=@p1`, h.cfg.PartsTable()), pnID)
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
		fmt.Sprintf(`SELECT title FROM %s WHERE PNID=@p1`, h.cfg.PartsTable()), pnID,
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
		fmt.Sprintf(`SELECT PNFILLinks FROM %s WHERE PNID=@p1`, h.cfg.PartsTable()), pnID,
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
		fmt.Sprintf(`SELECT PNFILLinks FROM %s WHERE PNID=@p1`, h.cfg.PartsTable()), pnID,
	).Scan(&filLinks)
	if err != nil {
		t.Fatalf("SELECT PNFILLinks after attach delete: %v", err)
	}
	if filLinks != 0 {
		t.Errorf("PNFILLinks after attach delete = %d, want 0", filLinks)
	}
}
