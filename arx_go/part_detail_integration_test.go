//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"arx/arx_go/models"
	"arx/internal/urlutil"
)

const partDetailThumb = `LOCAL:itest\t.png`

// seedPartDetail seeds FULL (every column set, a uom, a BOM line, a primary
// attachment and a LOCAL thumbnail) and BARE (every nullable column NULL).
// Returns the two ids and FULL's primary attachment id.
func seedPartDetail(t *testing.T, h *Handler) (full, bare, primaryAtt int, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	pn, att, bom := "part", "part_attachment", "bom"
	base := smokeUniq("ITEST-PD")
	cleanup = func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE parent_part_id=$1`, bom), full)
		for _, id := range []int{full, bare} {
			smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET primary_attachment_id=NULL WHERE id=$1`, pn), id)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id=$1`, att), id)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, pn), id)
		}
	}
	scan := func(dst *int, q string, args ...any) {
		t.Helper()
		if err := h.queryRowContext(ctx, q, args...).Scan(dst); err != nil {
			cleanup()
			t.Fatalf("seed: %v", err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := h.execContext(ctx, q, args...); err != nil {
			cleanup()
			t.Fatalf("seed: %v", err)
		}
	}
	scan(&full, fmt.Sprintf(`INSERT INTO %s (part_number, revision, description, detail, category, release_status, is_active,
		requested_by, notes, created_date, modified_date, uom_id, current_cost, last_rollup_cost, last_rollup_at,
		stock_on_hand, reorder_min, tracking_mode,
		user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		user_field_6, user_field_7, user_field_8, user_field_9, user_field_10)
		VALUES ($1,'C','full desc','full detail','BUY','A',TRUE,'JJ','full notes','2026-01-02','2026-03-04',3,1.25,3.5,
		'2026-02-03T04:05:06Z',2.5,4,'lot','u1','u2','u3','u4','u5','u6','u7','u8','u9','u10') RETURNING id`, pn), base+"-A")
	scan(&bare, fmt.Sprintf(`INSERT INTO %s (part_number, revision, description, detail, category, is_active,
		requested_by, notes, created_date, modified_date, current_cost,
		user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		user_field_6, user_field_7, user_field_8, user_field_9, user_field_10)
		VALUES ($1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL) RETURNING id`, pn), base+"-B")
	exec(fmt.Sprintf(`INSERT INTO %s (parent_part_id, component_part_id, line_number, qty) VALUES ($1, 3002, 1, 2)`, bom), full)
	scan(&primaryAtt, fmt.Sprintf(`INSERT INTO %s (part_id, file_name, category, is_active) VALUES ($1,'https://example.com/d.pdf','Drawing',TRUE) RETURNING id`, att), full)
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, file_name, category, is_active) VALUES ($1,$2,$3,TRUE)`, att), full, partDetailThumb, thumbnailCategory)
	exec(fmt.Sprintf(`UPDATE %s SET primary_attachment_id=$2, attachment_count=7, po_line_count=4 WHERE id=$1`, pn), full, primaryAtt)
	exec(fmt.Sprintf(`UPDATE %s SET attachment_count=NULL, po_line_count=NULL WHERE id=$1`, pn), bare)
	return full, bare, primaryAtt, cleanup
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

// partEditFields keeps only the fields fetchPartFull fills for the edit form.
func partEditFields(p models.Part) models.Part {
	return models.Part{ID: p.ID, PartNumber: p.PartNumber, Revision: p.Revision, Description: p.Description,
		Detail: p.Detail, Category: p.Category, HasBOM: p.HasBOM, ReleaseStatus: p.ReleaseStatus,
		IsActive: p.IsActive, RequestedBy: p.RequestedBy, Notes: p.Notes, UnitID: p.UnitID,
		CurrentCost: p.CurrentCost, ReorderMin: p.ReorderMin, TrackingMode: p.TrackingMode,
		IsLotTracked: p.IsLotTracked, UserField1: p.UserField1, UserField2: p.UserField2,
		UserField3: p.UserField3, UserField4: p.UserField4, UserField5: p.UserField5,
		UserField6: p.UserField6, UserField7: p.UserField7, UserField8: p.UserField8,
		UserField9: p.UserField9, UserField10: p.UserField10}
}

// TestIntegration_FetchPartFull_Fields pins the edit-form fields: NULL text
// reads blank, NULL is_active reads inactive, NULL cost reads 0.
func TestIntegration_FetchPartFull_Fields(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()
	ctx := context.Background()

	want := map[int]models.Part{
		full: {ID: full, Revision: "C", Description: "full desc", Detail: "full detail", Category: "BUY",
			HasBOM: true, ReleaseStatus: "A", IsActive: true, RequestedBy: "JJ", Notes: "full notes",
			UnitID: intPtr(3), CurrentCost: 1.25, ReorderMin: floatPtr(4), TrackingMode: "lot", IsLotTracked: true,
			UserField1: "u1", UserField2: "u2", UserField3: "u3", UserField4: "u4", UserField5: "u5",
			UserField6: "u6", UserField7: "u7", UserField8: "u8", UserField9: "u9", UserField10: "u10"},
		bare: {ID: bare, ReleaseStatus: "U", TrackingMode: "none"},
	}
	for id, w := range want {
		p, err := h.fetchPartFull(ctx, strconv.Itoa(id))
		if err != nil {
			t.Fatalf("fetchPartFull %d: %v", id, err)
		}
		w.PartNumber = p.PartNumber
		if got := partEditFields(p); !reflect.DeepEqual(got, w) {
			t.Errorf("fetchPartFull %d:\n got %+v\nwant %+v", id, got, w)
		}
	}
	if _, err := h.fetchPartFull(ctx, "0"); err == nil {
		t.Error("fetchPartFull(0): want an error")
	}
}

// TestIntegration_FetchPartBasic_Fields pins the sub-tab header part.
func TestIntegration_FetchPartBasic_Fields(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, bare, primaryAtt, cleanup := seedPartDetail(t, h)
	defer cleanup()
	ctx := context.Background()

	want := map[int]models.Part{
		full: {ID: full, Description: "full desc", Category: "BUY", HasBOM: true, PrimaryAttachmentID: intPtr(primaryAtt),
			StockOnHand: 2.5, TrackingMode: "lot", IsLotTracked: true,
			ThumbnailURL: urlutil.LocalFileURL(partDetailThumb, "/local/")},
		bare: {ID: bare, TrackingMode: "none"},
	}
	for id, w := range want {
		p, err := h.fetchPartBasic(ctx, strconv.Itoa(id))
		if err != nil {
			t.Fatalf("fetchPartBasic %d: %v", id, err)
		}
		w.PartNumber = p.PartNumber
		if !reflect.DeepEqual(p, w) {
			t.Errorf("fetchPartBasic %d:\n got %+v\nwant %+v", id, p, w)
		}
	}
}

// TestIntegration_PartDetail_Renders pins the detail-only fields (unit
// abbreviation, rollup, stock, modified date) as the page renders them.
func TestIntegration_PartDetail_Renders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	full, bare, _, cleanup := seedPartDetail(t, h)
	defer cleanup()

	for id, wants := range map[int][]string{
		full: {"<strong>Unit:</strong><span>mL</span>", "<strong>Detail:</strong><span>full detail</span>",
			"<strong>Current Cost:</strong><span>$1.2500</span>", "<strong>Last Rollup:</strong><span>$3.5000</span>",
			`<strong>Stock on hand:</strong><span>2.50 <span class="badge`, "2026-03-04", "full notes"},
		bare: {"<strong>Unit:</strong><span>—</span>"},
	} {
		rec := httptest.NewRecorder()
		h.PartDetail(rec, withID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/part/%d/details", id), nil), id))
		if rec.Code != http.StatusOK {
			t.Fatalf("PartDetail %d: status %d", id, rec.Code)
		}
		for _, s := range wants {
			if !strings.Contains(rec.Body.String(), s) {
				t.Errorf("PartDetail %d: body missing %q", id, s)
			}
		}
	}
}

// TestIntegration_PartCreateUpdate_Columns pins every column PartsCreate and
// PartUpdate write, including the blank-field NULL/zero/Under Review rules.
func TestIntegration_PartCreateUpdate_Columns(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()
	pn := "part"
	today := time.Now().Format("2006-01-02")

	cols := func(id int) map[string]any {
		t.Helper()
		var raw string
		if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT row_to_json(p)::text FROM %s p WHERE id=$1`, pn), id).Scan(&raw); err != nil {
			t.Fatalf("read part %d: %v", id, err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	check := func(step string, id int, want map[string]any) {
		t.Helper()
		got := cols(id)
		for k, v := range want {
			if !reflect.DeepEqual(got[k], v) {
				t.Errorf("%s: %s = %#v, want %#v", step, k, got[k], v)
			}
		}
	}

	number := smokeUniq("ITEST-PCU")
	vals := url.Values{
		"part_number": {number}, "revision": {"B"}, "description": {"d"}, "detail": {"dt"}, "category": {"BUY"},
		"release_status": {"D"}, "PNReqBy": {"JJ"}, "PNNotes": {"n"}, "PNUNID": {"3"}, "current_cost": {"1.5"},
		"reorder_min": {"2.25"}, "tracking_mode": {"serial"},
	}
	for i := 1; i <= 10; i++ {
		vals.Set(fmt.Sprintf("user_field_%d", i), fmt.Sprintf("v%d", i))
	}
	rec := httptest.NewRecorder()
	h.PartsCreate(rec, postForm("/parts", vals))
	id := locID(t, rec, "/part/")
	defer smokeExec(ctx, h, fmt.Sprintf("DELETE FROM %s WHERE id=$1", pn), id)

	want := map[string]any{
		"part_number": number, "revision": "B", "description": "d", "detail": "dt", "category": "BUY",
		"release_status": "D", "is_active": false, "requested_by": "JJ", "notes": "n",
		"created_date": today, "modified_date": today, "uom_id": 3.0, "current_cost": 1.5, "reorder_min": 2.25,
		"tracking_mode": "serial",
	}
	for i := 1; i <= 10; i++ {
		want[fmt.Sprintf("user_field_%d", i)] = fmt.Sprintf("v%d", i)
	}
	check("create", id, want)

	// Blanks: NULL category/unit/reorder_min, zero cost, bad status → U (active),
	// bad tracking mode → none, blank text stays ''.
	for _, k := range []string{"revision", "category", "PNUNID", "current_cost", "reorder_min", "PNNotes", "user_field_3"} {
		vals.Set(k, "")
	}
	vals.Set("release_status", "X")
	vals.Set("tracking_mode", "bogus")
	smokeExec(ctx, h, fmt.Sprintf("UPDATE %s SET modified_date='2020-01-01', created_date='2020-01-01' WHERE id=$1", pn), id)
	rec = httptest.NewRecorder()
	h.PartUpdate(rec, withID(postForm(fmt.Sprintf("/part/%d", id), vals), id))
	assert302(t, "PartUpdate", rec)
	want["revision"], want["category"], want["uom_id"], want["current_cost"], want["reorder_min"] = "", nil, nil, 0.0, nil
	want["notes"], want["user_field_3"] = "", ""
	want["release_status"], want["is_active"], want["tracking_mode"] = "U", true, "none"
	want["created_date"] = "2020-01-01"
	check("update", id, want)
}
