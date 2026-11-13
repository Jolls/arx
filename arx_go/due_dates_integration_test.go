//go:build integration

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"arx/internal/records"
)

// Seed (#447, relative to CURRENT_DATE): torque-wrench units of calibration form 6003 —
// 8506 TW-001 overdue (record 7021; a newer WIP 7026 must not clear it), 8507 TW-002 due in 10 days,
// 8508 TW-003 in date, 8509 TW-004 whose old overdue record 7024 is superseded by locked 7025.

// dueTx opens a transaction the test rolls back, so a subtest can edit seed rows freely.
func dueTx(t *testing.T, h *Handler) (*records.Service, *sql.Tx) {
	t.Helper()
	tx, err := h.database().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return records.New(tx), tx
}

func dueUnitIDs(t *testing.T, svc *records.Service) []int {
	t.Helper()
	rows, err := svc.ListDueUnits(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListDueUnits: %v", err)
	}
	var ids []int
	for _, r := range rows {
		ids = append(ids, r.UnitID)
	}
	return ids
}

func containsInt(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestIntegration_ListDueUnits_Seed(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	rows, err := h.records().ListDueUnits(t.Context(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, r := range rows {
		ids = append(ids, r.UnitID)
	}
	if !containsInt(ids, 8506) || !containsInt(ids, 8507) {
		t.Fatalf("due units = %v, want 8506 (overdue) and 8507 (due soon); ArxDev may need reseeding", ids)
	}
	for _, id := range []int{8508, 8509} {
		if containsInt(ids, id) {
			t.Errorf("unit %d listed, want it absent (in date / superseded)", id)
		}
	}
	// Soonest first: overdue 8506 (due -10d) ahead of 8507 (+10d).
	var i506, i507 int
	for i, r := range rows {
		switch r.UnitID {
		case 8506:
			i506 = i
			if r.RecordID != 7021 {
				t.Errorf("8506 current record = %d, want locked 7021 (draft 7026 must not count)", r.RecordID)
			}
		case 8507:
			i507 = i
		}
	}
	if i506 >= i507 {
		t.Errorf("order: 8506 at %d, 8507 at %d; want overdue first", i506, i507)
	}
}

func TestIntegration_ListDueUnits_Rules(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	for _, c := range []struct {
		name   string
		sql    string
		unit   int
		listed bool
	}{
		{"completing the newer record supersedes the overdue one", `UPDATE form_record SET is_locked = TRUE WHERE id = 7026`, 8506, false},
		{"inactive unit excluded", `UPDATE unit SET is_active = FALSE WHERE id = 8507`, 8507, false},
		{"inactive record excluded", `UPDATE form_record SET is_active = FALSE WHERE id = 7022`, 8507, false},
		{"cleared due date means untracked", `UPDATE form_record SET next_due_date = NULL WHERE id = 7022`, 8507, false},
		{"record without a unit excluded", `UPDATE form_record SET unit_id = NULL WHERE id = 7022`, 8507, false},
		{"inactive form excluded", `UPDATE form SET is_active = FALSE WHERE id = 6003`, 8507, false},
		{"due in 30 days is listed", `UPDATE form_record SET next_due_date = '` + records.Today().AddDate(0, 0, 30).Format("2006-01-02") + `' WHERE id = 7023`, 8508, true},
		{"due in 31 days is not", `UPDATE form_record SET next_due_date = '` + records.Today().AddDate(0, 0, 31).Format("2006-01-02") + `' WHERE id = 7023`, 8508, false},
		{"unlocking the newer record revives the old overdue one", `UPDATE form_record SET is_locked = FALSE WHERE id = 7025`, 8509, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, tx := dueTx(t, h)
			if _, err := tx.Exec(c.sql); err != nil {
				t.Fatal(err)
			}
			if got := containsInt(dueUnitIDs(t, svc), c.unit); got != c.listed {
				t.Errorf("unit %d listed = %v, want %v", c.unit, got, c.listed)
			}
		})
	}
}

func TestIntegration_ListUnitDue(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	due, err := h.records().ListUnitDue(t.Context(), 8506)
	if err != nil || len(due) != 1 || due[0].RecordID != 7021 || due[0].FormID != 6003 {
		t.Fatalf("ListUnitDue(8506) = %+v, %v; want one row from locked record 7021 on form 6003", due, err)
	}
	// 8508 is in date: still tracked on its own page, unlike the dashboard card.
	if due, err := h.records().ListUnitDue(t.Context(), 8508); err != nil || len(due) != 1 {
		t.Errorf("ListUnitDue(8508) = %+v, %v; want its one in-date due date", due, err)
	}
	// A unit with no recurring-form record has none.
	if due, err := h.records().ListUnitDue(t.Context(), 8503); err != nil || len(due) != 0 {
		t.Errorf("ListUnitDue(8503) = %+v, %v; want none", due, err)
	}
}

func TestIntegration_RecordNextDueSuperseded(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	for id, want := range map[int]bool{7021: false, 7024: true, 7025: false} {
		rec, err := h.records().GetRecord(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.NextDueDate == nil || rec.NextDueSuperseded != want {
			t.Errorf("record %d: NextDueDate=%v superseded=%v, want a date and superseded=%v", id, rec.NextDueDate, rec.NextDueSuperseded, want)
		}
	}
}

func TestIntegration_CreateRecordPrefillsNextDue(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	svc, tx := dueTx(t, h)
	ctx := context.Background()

	newRecord := func(formID, partID int) int {
		id, err := svc.InsertRecord(ctx, records.NewRecord{
			FormID: formID, PartID: &partID, SerialNumber: "PREFILL-1", SubjectPartNumber: "X", SubjectPnDescription: "X",
			RecordType: "Calibration", TestOrder: "6116", RecordDate: time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("InsertRecord: %v", err)
		}
		return id
	}

	rec, err := svc.GetRecord(ctx, newRecord(6003, 3015))
	if err != nil || rec.NextDueDate == nil || rec.NextDueDate.Format("2006-01-02") != "2027-01-15" {
		t.Errorf("recurring form: NextDueDate = %v (%v), want 2027-01-15 (record date + 365)", rec.NextDueDate, err)
	}
	if rec, err := svc.GetRecord(ctx, newRecord(6001, 3004)); err != nil || rec.NextDueDate != nil {
		t.Errorf("form without an interval: NextDueDate = %v (%v), want nil", rec.NextDueDate, err)
	}

	// Re-test: recomputed from today, not copied from the source record.
	dup, err := svc.DuplicateRecord(ctx, 7021)
	if err != nil {
		t.Fatal(err)
	}
	var want, got string
	if err := tx.QueryRow(`SELECT to_char(CURRENT_DATE + 365, 'YYYY-MM-DD'), to_char(next_due_date, 'YYYY-MM-DD') FROM form_record WHERE id = $1`, dup).Scan(&want, &got); err != nil || got != want {
		t.Errorf("duplicate: next_due_date = %q (%v), want %q", got, err, want)
	}
}

func TestIntegration_SaveRecordNextDue(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	svc, _ := dueTx(t, h)
	ctx := context.Background()

	save := func(due *time.Time) {
		t.Helper()
		if err := svc.UpdateRecordAfterSave(ctx, 7026, records.RecordSave{RecordType: "Calibration", NextDueDate: due}); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	save(&day)
	if rec, err := svc.GetRecord(ctx, 7026); err != nil || rec.NextDueDate == nil || !rec.NextDueDate.Equal(day) {
		t.Errorf("after save: NextDueDate = %v (%v), want %v", rec.NextDueDate, err, day)
	}
	save(nil)
	if rec, err := svc.GetRecord(ctx, 7026); err != nil || rec.NextDueDate != nil {
		t.Errorf("after clearing: NextDueDate = %v (%v), want nil", rec.NextDueDate, err)
	}
	// A locked record can't be claimed for a save, so its due date can't change (the handler stops here).
	if claimed, err := svc.ClaimRecord(ctx, 7021); err != nil || claimed {
		t.Errorf("ClaimRecord(locked 7021) = %v, %v; want false", claimed, err)
	}
}

func TestIntegration_DashboardDueOverdueCard(t *testing.T) {
	h, done := liveHandler(t)
	defer done()

	rec := httptest.NewRecorder()
	h.ReportsDashboardDueOverdue(rec, httptest.NewRequest(http.MethodGet, "/api/reports/dashboard/due-overdue", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, body)
	}
	// Seed dates are CURRENT_DATE (server) -/+ 10; the card counts from the local date, so allow a day's skew.
	for _, want := range []string{"TW-001", "d overdue", "TW-002", "badge text-bg-warning"} {
		if !strings.Contains(body, want) {
			t.Errorf("card missing %q:\n%s", want, body)
		}
	}
	for _, absent := range []string{"TW-003", "TW-004"} {
		if strings.Contains(body, absent) {
			t.Errorf("card lists %q, want it absent:\n%s", absent, body)
		}
	}
}
