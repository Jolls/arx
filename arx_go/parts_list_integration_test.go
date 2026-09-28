//go:build integration

package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
)

// seedPartsList seeds three parts whose part numbers sort FULL < BARE < OFF:
// FULL has every field set, is below its reorder min and has two active LOCAL
// thumbnails (plus a lower-named inactive thumbnail and a non-thumbnail that
// must be ignored); BARE has every nullable column NULL and an http thumbnail;
// OFF is inactive. Returns the three part numbers and the ids.
func seedPartsList(t *testing.T, h *Handler) (full, bare, off string, ids [3]int, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	pn, att := h.cfg().PartsTable(), h.cfg().AttachmentsTable()
	base := smokeUniq("ITEST-PL")
	full, bare, off = base+"-A", base+"-B", base+"-C"
	cleanup = func() {
		for _, id := range ids {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id=$1`, att), id)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, pn), id)
		}
	}
	insert := func(i int, q string, args ...any) {
		t.Helper()
		if err := h.queryRowContext(ctx, fmt.Sprintf(q, pn), args...).Scan(&ids[i]); err != nil {
			cleanup()
			t.Fatalf("seed part %d: %v", i, err)
		}
	}
	insert(0, `INSERT INTO %s (part_number, revision, description, detail, requested_by, created_date, category, modified_date, is_active, stock_on_hand, reorder_min)
		VALUES ($1,'B','full desc','full detail','JJ','2026-01-02','BUY','2026-03-04',TRUE,1,5) RETURNING id`, full)
	insert(1, `INSERT INTO %s (part_number, revision, description, detail, requested_by, created_date, category, modified_date, is_active)
		VALUES ($1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL) RETURNING id`, bare)
	insert(2, `INSERT INTO %s (part_number, created_date, category, modified_date, is_active, stock_on_hand, reorder_min)
		VALUES ($1,'2026-05-06','RAW','2026-05-07',FALSE,9,5) RETURNING id`, off)

	attach := func(partID int, fileName, category string, active bool) {
		t.Helper()
		if _, err := h.execContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (part_id, file_name, category, is_active) VALUES ($1,$2,$3,$4)`, att),
			partID, fileName, category, active); err != nil {
			cleanup()
			t.Fatalf("seed attachment %s: %v", fileName, err)
		}
	}
	attach(ids[0], `LOCAL:itest\c.png`, thumbnailCategory, true)
	attach(ids[0], `LOCAL:itest\b.png`, thumbnailCategory, true)
	attach(ids[0], `LOCAL:itest\a.png`, thumbnailCategory, false)
	attach(ids[0], `LOCAL:itest\0.pdf`, "Drawing", true)
	attach(ids[1], `https://example.com/t.png`, thumbnailCategory, true)

	// Pin the trigger-maintained counts so the test doesn't depend on them.
	for i, counts := range [][2]any{{7, 4}, {nil, nil}, {0, 0}} {
		if _, err := h.execContext(ctx, fmt.Sprintf(
			`UPDATE %s SET attachment_count=$2, po_line_count=$3 WHERE id=$1`, pn), ids[i], counts[0], counts[1]); err != nil {
			cleanup()
			t.Fatalf("pin counts %d: %v", i, err)
		}
	}
	return full, bare, off, ids, cleanup
}

// TestIntegration_PartsRows_Fields pins the /parts grid JSON: NULL columns
// read blank/zero, NULL is_active reads active, below-min needs a reorder min,
// and the thumbnail is the lowest active LOCAL thumbnail only.
func TestIntegration_PartsRows_Fields(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, bare, off, ids, cleanup := seedPartsList(t, h)
	defer cleanup()

	type row struct {
		ID          int    `json:"id"`
		PN          string `json:"pn"`
		Rev         string `json:"rev"`
		Description string `json:"description"`
		Detail      string `json:"detail"`
		ReqBy       string `json:"reqBy"`
		Date        string `json:"date"`
		Cat         string `json:"cat"`
		Modified    string `json:"modified"`
		Active      bool   `json:"active"`
		Attach      int    `json:"attach"`
		POLines     int    `json:"poLines"`
		BelowMin    bool   `json:"belowMin"`
		Thumb       string `json:"thumb"`
	}
	w := httptest.NewRecorder()
	h.PartsRows(w, httptest.NewRequest("GET", "/parts/rows", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var rows []row
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []row{
		{ID: ids[0], PN: full, Rev: "B", Description: "full desc", Detail: "full detail", ReqBy: "JJ",
			Date: "2026-01-02", Cat: "BUY", Modified: "2026-03-04", Active: true, Attach: 7, POLines: 4,
			BelowMin: true, Thumb: "/local/itest/b.png"},
		{ID: ids[1], PN: bare, Active: true},
		{ID: ids[2], PN: off, Date: "2026-05-06", Cat: "RAW", Modified: "2026-05-07"},
	}
	var got []row
	for _, r := range rows {
		if r.PN == full || r.PN == bare || r.PN == off {
			got = append(got, r)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n %+v\nwant\n %+v", got, want)
	}
}

// TestIntegration_PartsExportCSV_Fields pins the parts CSV export's header and
// per-part records (NULL columns blank, NULL is_active exported as true).
func TestIntegration_PartsExportCSV_Fields(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, bare, off, _, cleanup := seedPartsList(t, h)
	defer cleanup()

	w := httptest.NewRecorder()
	h.PartsExportCSV(w, httptest.NewRequest("GET", "/parts/export.csv", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q", ct)
	}
	recs, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	wantHeader := []string{"Part Number", "Revision", "Description", "Detail", "Requested By", "Created Date", "Category", "Modified Date", "Active"}
	if len(recs) == 0 || !reflect.DeepEqual(recs[0], wantHeader) {
		t.Fatalf("header = %v", recs[:min(1, len(recs))])
	}
	want := [][]string{
		{full, "B", "full desc", "full detail", "JJ", "2026-01-02", "BUY", "2026-03-04", "true"},
		{bare, "", "", "", "", "", "", "", "true"},
		{off, "", "", "", "", "2026-05-06", "RAW", "2026-05-07", "false"},
	}
	var got [][]string
	for _, r := range recs[1:] {
		if r[0] == full || r[0] == bare || r[0] == off {
			got = append(got, r)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records =\n %v\nwant\n %v", got, want)
	}
}
