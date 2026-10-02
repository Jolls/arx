//go:build integration

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Characterization tests for the reports/utilities/misc handlers (#246, #248, #225): gaps the
// older integration tests left, written against the pre-sqlc code. Read-only against the seed
// rows (POs 5002–5009, forms 6001, records 7001–7014); everything written is removed on cleanup.

// repInsert runs an INSERT … RETURNING id and registers the cleanup statements (each takes the
// new id as $1) with t.Cleanup, so children registered later are removed before their parents.
func repInsert(t *testing.T, h *Handler, insert string, args []any, cleanup ...string) int {
	t.Helper()
	var id int
	if err := h.queryRowContext(context.Background(), insert, args...).Scan(&id); err != nil {
		t.Fatalf("seed %q: %v", insert, err)
	}
	t.Cleanup(func() {
		for _, q := range cleanup {
			smokeExec(context.Background(), h, q, id)
		}
	})
	return id
}

func repExec(t *testing.T, h *Handler, q string, args ...any) {
	t.Helper()
	if _, err := h.execContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func repCompany(t *testing.T, h *Handler) (int, string) {
	t.Helper()
	name := smokeUniq("RPT-CO")
	id := repInsert(t, h, `INSERT INTO company (name, supplier_code, is_active, is_supplier) VALUES ($1,'RP1',TRUE,TRUE) RETURNING id`,
		[]any{name},
		`UPDATE company SET primary_attachment_id=NULL WHERE id=$1`,
		`DELETE FROM company WHERE id=$1`)
	return id, name
}

func repPart(t *testing.T, h *Handler, category string) (int, string) {
	t.Helper()
	pn := smokeUniq("RPT-P")
	// modified_date has no column default (#279); the recent-activity feed lists parts that have one.
	id := repInsert(t, h, `INSERT INTO part (part_number, description, revision, category, modified_date) VALUES ($1,'rpt desc','A',$2,CURRENT_DATE) RETURNING id`,
		[]any{pn, category},
		`UPDATE part SET primary_attachment_id=NULL WHERE id=$1`,
		`DELETE FROM part_attachment WHERE part_id=$1`,
		`DELETE FROM part WHERE id=$1`)
	return id, pn
}

// repPO inserts a PO for the supplier; status/isActive are stored as given (NULL when nil).
func repPO(t *testing.T, h *Handler, co int, status *string, isActive *bool, dateOrdered *string) (int, string) {
	t.Helper()
	num := smokeUniq("RP")
	id := repInsert(t, h, `INSERT INTO purchase_order (number, supplier_id, supplier_name, status, is_active, date_ordered)
		VALUES ($1,$2,'rpt',$3,$4,$5::text::date) RETURNING id`,
		[]any{num, co, status, isActive, dateOrdered},
		`DELETE FROM po_line WHERE po_id=$1`,
		`DELETE FROM purchase_order_history WHERE po_id=$1`,
		`DELETE FROM purchase_order WHERE id=$1`)
	return id, num
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func repDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }

func TestIntegration_ReportsDateRangeBounds(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	// Seed orders: Acme 2026-01-10 (25.00 RAW-1001) and 2026-04-01 (205.00 RAW-1002);
	// Precision 2026-05-01 (60.00) and 2026-06-01 (24.00). Rows are looked up by name so
	// other data in the database can't matter.
	supplierTotal := func(rng reportDateRange, name string) (float64, bool) {
		rows, err := h.querySpendBySupplier(ctx, rng)
		if err != nil {
			t.Fatalf("querySpendBySupplier: %v", err)
		}
		for _, r := range rows {
			if r.SupplierName == name {
				return r.TotalSpend.InexactFloat64(), true
			}
		}
		return 0, false
	}
	const acme, prec = "Acme Fasteners", "Precision Machining Co"

	t.Run("spend_supplier_single_day_is_inclusive", func(t *testing.T) {
		rng := reportDateRange{From: repDay(2026, 4, 1), To: repDay(2026, 4, 1)}
		if v, ok := supplierTotal(rng, acme); !ok || v != 205 {
			t.Errorf("Acme = %v,%v want 205,true", v, ok)
		}
		if _, ok := supplierTotal(rng, prec); ok {
			t.Error("Precision must be outside 2026-04-01")
		}
	})
	t.Run("spend_supplier_gap_range_is_empty", func(t *testing.T) {
		rng := reportDateRange{From: repDay(2026, 4, 2), To: repDay(2026, 4, 30)}
		for _, n := range []string{acme, prec} {
			if _, ok := supplierTotal(rng, n); ok {
				t.Errorf("%s must be outside 2026-04-02..30", n)
			}
		}
	})
	t.Run("spend_supplier_from_only", func(t *testing.T) {
		rng := reportDateRange{From: repDay(2026, 5, 1)}
		if v, ok := supplierTotal(rng, prec); !ok || v != 84 {
			t.Errorf("Precision = %v,%v want 84,true (5002 60.00 + 5008 24.00)", v, ok)
		}
		if _, ok := supplierTotal(rng, acme); ok {
			t.Error("Acme has no dated order on/after 2026-05-01")
		}
	})
	t.Run("spend_supplier_to_only", func(t *testing.T) {
		rng := reportDateRange{To: repDay(2026, 1, 31)}
		if v, ok := supplierTotal(rng, acme); !ok || v != 25 {
			t.Errorf("Acme = %v,%v want 25,true", v, ok)
		}
		if _, ok := supplierTotal(rng, prec); ok {
			t.Error("Precision must be after 2026-01-31")
		}
	})
	t.Run("spend_part_to_only", func(t *testing.T) {
		rows, err := h.querySpendByPart(ctx, reportDateRange{To: repDay(2026, 1, 31)})
		if err != nil {
			t.Fatal(err)
		}
		var raw1001 float64
		for _, r := range rows {
			if r.PartNumber == "RAW-1001" {
				raw1001 = r.TotalSpend.InexactFloat64()
			}
			if r.PartNumber == "RAW-1002" {
				t.Errorf("RAW-1002 (ordered 2026-04-01) must be outside the range: %+v", r)
			}
		}
		if raw1001 != 25 {
			t.Errorf("RAW-1001 = %v want 25 (rows=%+v)", raw1001, rows)
		}
	})
	t.Run("on_time", func(t *testing.T) {
		find := func(rng reportDateRange) *onTimeSupplierRow {
			rows, err := h.queryOnTimeDelivery(ctx, rng)
			if err != nil {
				t.Fatal(err)
			}
			for i := range rows {
				if rows[i].SupplierName == acme {
					return &rows[i]
				}
			}
			return nil
		}
		in := find(reportDateRange{From: repDay(2026, 4, 1), To: repDay(2026, 4, 1)})
		if in == nil || in.TotalLines != 1 || in.OnTimeLines != 1 || in.OnTimePct != 100 {
			t.Fatalf("Acme in range = %+v, want 1/1/100%%", in)
		}
		assertFloatEqual(t, "AvgDaysLate", in.AvgDaysLate, -6)
		if out := find(reportDateRange{From: repDay(2026, 4, 2)}); out != nil {
			t.Errorf("Acme row should be excluded after 2026-04-01: %+v", out)
		}
	})
	t.Run("cycle_time", func(t *testing.T) {
		// 5002's status history: draft entered 2026-04-28, exited into open on 2026-05-01.
		// Other seeded POs enter draft earlier (5004: 2026-01-08) or never exit it (#312).
		find := func(rng reportDateRange) *cycleTimeStageRow {
			rows, err := h.queryPOCycleTime(ctx, rng)
			if err != nil {
				t.Fatal(err)
			}
			for i := range rows {
				if rows[i].Stage == "draft" {
					return &rows[i]
				}
			}
			return nil
		}
		in := find(reportDateRange{From: repDay(2026, 4, 20), To: repDay(2026, 4, 30)})
		if in == nil || in.POCount != 1 {
			t.Fatalf("draft in range = %+v, want 1 PO", in)
		}
		assertFloatEqual(t, "AvgDays", in.AvgDays, 3)
		if out := find(reportDateRange{From: repDay(2026, 5, 2)}); out != nil {
			t.Errorf("draft entered 2026-04-28 must be excluded from 2026-05-02..: %+v", out)
		}
		// Margin of two days: the seed's changed_at is midnight UTC, i.e. the evening before in PDT.
		if out := find(reportDateRange{To: repDay(2026, 1, 6)}); out != nil {
			t.Errorf("no draft entered by 2026-01-06; got: %+v", out)
		}
	})
}

func TestIntegration_DashboardPOCounts(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	co, _ := repCompany(t, h)

	openBefore, err := h.dashboardOpenPOCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recvBefore, err := h.dashboardPOsReceivedThisMonth(ctx)
	if err != nil {
		t.Fatal(err)
	}

	openPO, _ := repPO(t, h, co, strp("open"), boolp(true), nil)
	repPO(t, h, co, strp("draft"), boolp(true), nil) // not open: must not count
	repExec(t, h, `INSERT INTO po_line (po_id, line_number, qty, unit_cost, date_received) VALUES
		($1,1,1,1,CURRENT_DATE), ($1,2,1,1,CURRENT_DATE)`, openPO) // two lines, one PO
	lastMonthPO, _ := repPO(t, h, co, strp("open"), boolp(true), nil)
	repExec(t, h, `INSERT INTO po_line (po_id, line_number, qty, unit_cost, date_received)
		VALUES ($1,1,1,1,date_trunc('month', CURRENT_DATE)::date - 1)`, lastMonthPO) // last month: not counted

	openAfter, err := h.dashboardOpenPOCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if openAfter != openBefore+2 {
		t.Errorf("open PO count = %d, want %d (two 'open' POs added, the draft ignored)", openAfter, openBefore+2)
	}
	recvAfter, err := h.dashboardPOsReceivedThisMonth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recvAfter != recvBefore+1 {
		t.Errorf("received this month = %d, want %d (distinct POs, last month excluded)", recvAfter, recvBefore+1)
	}
}

func TestIntegration_DashboardFailureModesAndYield(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	modes, err := h.dashboardTopFailureModes(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var found *dashboardFailureModeItem
	for i := range modes {
		if modes[i].FormID == 6001 && modes[i].Parameter == "Current Draw" {
			found = &modes[i]
		}
		if i > 0 && modes[i].FailureCount > modes[i-1].FailureCount {
			t.Errorf("failure modes not ranked descending: %+v", modes)
		}
	}
	// Seed: results 7115 (record 7005) and 7123 (record 7007) fail the Current Draw step.
	if found == nil || found.PartNumber != "FORM-1001" || found.FailureCount != 2 {
		t.Errorf("Current Draw mode = %+v, want FORM-1001 x2 (modes=%+v)", found, modes)
	}
	if limited, err := h.dashboardTopFailureModes(ctx, 1); err != nil || len(limited) != 1 {
		t.Errorf("limit 1: %d rows, err %v", len(limited), err)
	}

	forms, err := h.dashboardLowestYieldForms(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var yf *dashboardYieldItem
	for i := range forms {
		if forms[i].FormID == 6001 {
			yf = &forms[i]
		}
	}
	// Seed: 13 active records on form 6001 (7004 is soft-deleted); 7005 and 7007 have a failing row.
	if yf == nil || yf.PartNumber != "FORM-1001" || yf.Total != 13 || yf.Passed != 11 {
		t.Errorf("form 6001 yield = %+v, want FORM-1001 13 total / 11 passed", yf)
	}
}

func TestIntegration_DashboardRecentActivity(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	co, _ := repCompany(t, h)
	partID, pn := repPart(t, h, "BUY")
	poID, num := repPO(t, h, co, strp("open"), boolp(true), nil)
	repExec(t, h, `INSERT INTO purchase_order_history (po_id, event_type, to_status, changed_by) VALUES ($1,'status','open','rpt')`, poID)

	items, err := h.dashboardRecentActivity(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || len(items) > 50 {
		t.Fatalf("len(items) = %d", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i].Timestamp.After(items[i-1].Timestamp) {
			t.Errorf("feed not newest-first at %d: %+v", i, items)
		}
	}
	var po, part *dashboardActivityItem
	for i := range items {
		switch items[i].Label {
		case "PO " + num:
			po = &items[i]
		case pn + " — rpt desc":
			part = &items[i]
		}
	}
	if po == nil || po.Detail != "→ open" || po.URL != fmt.Sprintf("/po/%d", poID) {
		t.Errorf("PO item = %+v", po)
	}
	if part == nil || part.Detail != "modified" || part.URL != fmt.Sprintf("/part/%d", partID) {
		t.Errorf("part item = %+v", part)
	}
	if capped, err := h.dashboardRecentActivity(ctx, 3); err != nil || len(capped) != 3 {
		t.Errorf("limit 3: %d rows, err %v", len(capped), err)
	}
}

func TestIntegration_ActiveFormOptions(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	addForm := func(partID int, active bool) {
		repInsert(t, h, `INSERT INTO form (part_number_id, test_order, is_locked, is_active) VALUES ($1,'',FALSE,$2) RETURNING id`,
			[]any{partID, active}, `DELETE FROM form WHERE id=$1`)
	}
	activePart, activePN := repPart(t, h, "FORM")
	addForm(activePart, true)
	archivedPart, archivedFormPN := repPart(t, h, "FORM")
	addForm(archivedPart, false)
	buyPart, buyPN := repPart(t, h, "BUY")
	addForm(buyPart, true)

	// part.description is nullable; the picker used to swallow the per-row Scan error (`continue`),
	// so a FORM part with a NULL description silently vanished from the list (#246).
	nullDescPN := smokeUniq("RPT-ND")
	nullDescPart := repInsert(t, h, `INSERT INTO part (part_number, description, revision, category) VALUES ($1,NULL,'A','FORM') RETURNING id`,
		[]any{nullDescPN}, `DELETE FROM part WHERE id=$1`)
	addForm(nullDescPart, true)

	forms, err := h.loadActiveFormOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	has := func(pn string) bool {
		for _, f := range forms {
			if f.PartNumber == pn {
				return true
			}
		}
		return false
	}
	if !has("FORM-1001") || !has(activePN) {
		t.Errorf("active FORM parts missing: %+v", forms)
	}
	if has(archivedFormPN) {
		t.Error("archived form must not be listed")
	}
	if !has(nullDescPN) {
		t.Error("a FORM part with a NULL description must still be listed")
	}
	if has(buyPN) {
		t.Error("form on a non-FORM part must not be listed")
	}
	for _, c := range []struct {
		name string
		fn   http.HandlerFunc
	}{{"ReportsYieldPicker", h.ReportsYieldPicker}, {"ReportsFailureModesPicker", h.ReportsFailureModesPicker}} {
		rec := httptest.NewRecorder()
		c.fn(rec, httptest.NewRequest(http.MethodGet, "/reports/picker", nil))
		assertStatus(t, c.name, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "FORM-1001") {
			t.Errorf("%s: body missing FORM-1001", c.name)
		}
	}
}

func TestIntegration_UtilitiesPOActiveDrift(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	co, _ := repCompany(t, h)
	driftID, driftNum := repPO(t, h, co, strp("open"), boolp(false), nil)
	_, okNum := repPO(t, h, co, strp("closed"), boolp(false), nil)

	c, err := h.checkPOActiveDrift(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var hit *utilRow
	for i := range c.Rows {
		if c.Rows[i].Label == okNum {
			t.Errorf("in-sync PO %s flagged: %+v", okNum, c.Rows[i])
		}
		if c.Rows[i].Label == driftNum {
			hit = &c.Rows[i]
		}
	}
	if hit == nil || hit.URL != fmt.Sprintf("/po/%d", driftID) ||
		hit.Detail != `status "open" implies is_active=true, but stored value is false` {
		t.Errorf("drift row = %+v", hit)
	}
	if c.Count != len(c.Rows) {
		t.Errorf("Count %d != len(Rows) %d", c.Count, len(c.Rows))
	}
}

// A legacy PO with NULL status/is_active (both columns are nullable) used to abort the whole
// drift check on the row scan (#246); NULL now reads as an empty status / false and the check completes.
func TestIntegration_UtilitiesPOActiveDriftNullStatus(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	co, _ := repCompany(t, h)
	_, num := repPO(t, h, co, nil, nil, nil)

	c, err := h.checkPOActiveDrift(context.Background())
	if err != nil {
		t.Fatalf("a NULL-status PO must not abort the check: %v", err)
	}
	for _, r := range c.Rows {
		if r.Label == num {
			t.Errorf("NULL status/is_active reads as ''/false, which is in sync; flagged: %+v", r)
		}
	}
}

func TestIntegration_UtilitiesSoftDeletedPointers(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	co, coName := repCompany(t, h)
	partID, pn := repPart(t, h, "BUY")

	partAtt := repInsert(t, h, `INSERT INTO part_attachment (part_id, file_name, category, is_active) VALUES ($1,'LOCAL:rpt-soft.pdf','Drawing',TRUE) RETURNING id`,
		[]any{partID},
		`UPDATE part SET primary_attachment_id=NULL WHERE primary_attachment_id=$1`,
		`DELETE FROM part_attachment WHERE id=$1`)
	repExec(t, h, `UPDATE part SET primary_attachment_id=$2 WHERE id=$1`, partID, partAtt)
	coAtt := repInsert(t, h, `INSERT INTO company_attachment (supplier_id, file_path, is_active) VALUES ($1,'LOCAL:rpt-soft.pdf',TRUE) RETURNING supplier_attachment_id`,
		[]any{co},
		`UPDATE company SET primary_attachment_id=NULL WHERE primary_attachment_id=$1`,
		`DELETE FROM company_attachment WHERE supplier_attachment_id=$1`)
	repExec(t, h, `UPDATE company SET primary_attachment_id=$2 WHERE id=$1`, co, coAtt)

	find := func() (part, comp *utilRow) {
		c, err := h.checkSoftDeletedAttachmentPointers(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i := range c.Rows {
			switch c.Rows[i].Label {
			case pn:
				part = &c.Rows[i]
			case coName:
				comp = &c.Rows[i]
			}
		}
		return
	}
	if p, c := find(); p != nil || c != nil {
		t.Fatalf("active attachments flagged: %+v %+v", p, c)
	}
	repExec(t, h, `UPDATE part_attachment SET is_active=FALSE WHERE id=$1`, partAtt)
	repExec(t, h, `UPDATE company_attachment SET is_active=FALSE WHERE supplier_attachment_id=$1`, coAtt)
	p, c := find()
	if p == nil || p.URL != fmt.Sprintf("/part/%d", partID) || p.Detail != "primary attachment is soft-deleted" {
		t.Errorf("part row = %+v", p)
	}
	if c == nil || c.URL != fmt.Sprintf("/supplier/%d", co) || c.Detail != "primary attachment is soft-deleted" {
		t.Errorf("company row = %+v", c)
	}
}

func TestIntegration_UtilitiesDeadLinks(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	docRoot, supRoot := t.TempDir(), t.TempDir()
	h.update(func(s *runtimeState) { s.cfg.DocControlRoot, s.cfg.SupplierFilesRoot = docRoot, supRoot })
	if err := os.WriteFile(filepath.Join(docRoot, "rpt-present.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(supRoot, "rpt-co-present.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	co, coName := repCompany(t, h)
	partID, pn := repPart(t, h, "BUY")
	addPart := func(file string, active bool) {
		repInsert(t, h, `INSERT INTO part_attachment (part_id, file_name, category, is_active) VALUES ($1,$2,'Drawing',$3) RETURNING id`,
			[]any{partID, file, active}, `DELETE FROM part_attachment WHERE id=$1`)
	}
	addPart(`LOCAL:rpt-missing.pdf`, true)
	addPart(`LOCAL:rpt-present.pdf`, true)
	addPart(`LOCAL:rpt-inactive-missing.pdf`, false)
	addPart(`https://example.com/x.pdf`, true)
	repInsert(t, h, `INSERT INTO company_attachment (supplier_id, file_path, is_active) VALUES ($1,'LOCAL:rpt-co-missing.pdf',TRUE) RETURNING supplier_attachment_id`,
		[]any{co}, `DELETE FROM company_attachment WHERE supplier_attachment_id=$1`)
	repInsert(t, h, `INSERT INTO company_attachment (supplier_id, file_path, is_active) VALUES ($1,'LOCAL:rpt-co-present.pdf',TRUE) RETURNING supplier_attachment_id`,
		[]any{co}, `DELETE FROM company_attachment WHERE supplier_attachment_id=$1`)

	c, err := h.checkDeadLinks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var partRows, coRows []utilRow
	for _, r := range c.Rows {
		switch r.Label {
		case pn:
			partRows = append(partRows, r)
		case coName:
			coRows = append(coRows, r)
		}
	}
	if len(partRows) != 1 || partRows[0].Detail != "missing on disk: LOCAL:rpt-missing.pdf" || partRows[0].URL != fmt.Sprintf("/part/%d", partID) {
		t.Errorf("part rows = %+v", partRows)
	}
	if len(coRows) != 1 || coRows[0].Detail != "missing on disk: LOCAL:rpt-co-missing.pdf" || coRows[0].URL != fmt.Sprintf("/supplier/%d", co) {
		t.Errorf("company rows = %+v", coRows)
	}
	if c.Count != len(c.Rows) {
		t.Errorf("Count %d != len(Rows) %d", c.Count, len(c.Rows))
	}
}

// The three part pointers are enforced FKs (#735), so the orphan check cannot find a row.
func TestIntegration_UtilitiesOrphanPointersEmpty(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	c, err := h.checkOrphanPointers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Count != 0 || len(c.Rows) != 0 {
		t.Errorf("orphan rows = %+v", c.Rows)
	}
}

func TestIntegration_FetchUnits(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	units, err := h.fetchUnits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range units {
		got = append(got, u.UnitType+"/"+u.Abbreviation+"/"+u.DisplayName)
	}
	// Seed uom rows 1–17, ordered by unit_type then abbreviation (database collation).
	if len(units) < 17 {
		t.Fatalf("units = %v", got)
	}
	for i := 1; i < len(units); i++ {
		if units[i].UnitType < units[i-1].UnitType {
			t.Errorf("not grouped by unit_type: %v", got)
			break
		}
	}
	first := units[0]
	if first.UnitType != "count" || first.ID == 0 {
		t.Errorf("first unit = %+v, want a count unit", first)
	}
	var ea *UnitOption
	for i := range units {
		if units[i].Abbreviation == "EA" {
			ea = &units[i]
		}
	}
	if ea == nil || ea.ID != 1 || ea.DisplayName != "Each" || ea.UnitType != "count" {
		t.Errorf("EA = %+v", ea)
	}
}

func TestIntegration_AppConfigRoundTrip(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	key := smokeUniq("rpt.appconfig")
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM app_config WHERE setting_key=$1`, key) })

	if _, err := h.appConfigGet(ctx, key); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("missing key err = %v, want sql.ErrNoRows", err)
	}
	if got := h.appConfigGetOr(ctx, key, "dflt"); got != "dflt" {
		t.Errorf("GetOr missing = %q", got)
	}
	if err := h.appConfigSet(ctx, key, "one"); err != nil {
		t.Fatal(err)
	}
	if v, err := h.appConfigGet(ctx, key); err != nil || v != "one" {
		t.Errorf("Get = %q,%v", v, err)
	}
	var t1, t2 time.Time
	if err := h.queryRowContext(ctx, `SELECT updated_at FROM app_config WHERE setting_key=$1`, key).Scan(&t1); err != nil {
		t.Fatal(err)
	}
	if err := h.appConfigSet(ctx, key, "two"); err != nil {
		t.Fatal(err)
	}
	if v := h.appConfigGetOr(ctx, key, "dflt"); v != "two" {
		t.Errorf("after upsert = %q", v)
	}
	if err := h.queryRowContext(ctx, `SELECT updated_at FROM app_config WHERE setting_key=$1`, key).Scan(&t2); err != nil {
		t.Fatal(err)
	}
	if !t2.After(t1) {
		t.Errorf("updated_at not bumped by upsert: %v -> %v", t1, t2)
	}
	if n := countRows(t, h, ctx, fmt.Sprintf("app_config WHERE setting_key='%s'", key)); n != 1 {
		t.Errorf("rows for key = %d, want 1 (upsert, not insert)", n)
	}
}

func TestIntegration_CheckSchemaVersionStates(t *testing.T) {
	ctx := context.Background()
	h, done := liveHandler(t)
	t.Cleanup(done)
	h.CheckSchemaVersion(ctx)
	if s := h.st(); s.schemaMismatch != "" || s.dbConnError != "" {
		t.Errorf("matching DB: mismatch=%q connErr=%q", s.schemaMismatch, s.dbConnError)
	}

	h2, done2 := liveHandler(t)
	t.Cleanup(done2)
	h2.CloseDB()
	h2.CheckSchemaVersion(ctx)
	if s := h2.st(); s.schemaMismatch != "" || !strings.HasPrefix(s.dbConnError, "could not read schema_version (") {
		t.Errorf("closed DB: mismatch=%q connErr=%q", s.schemaMismatch, s.dbConnError)
	}
}
