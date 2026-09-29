//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// Characterization tests for the records read paths (#249, #223), written against the pre-sqlc
// code. Every row is a committed throwaway fixture removed on cleanup (LIFO via repInsert); the
// seeded forms/records are not touched.

type recFix struct {
	formPartID, formID, asmID, compID, lotID int
	formPN, asmPN, compPN, lotNumber         string
	s1, s2, s3                               int
	rA, rB, rC, rD                           int // WIP sn 10 / complete sn 9 / WIP sn ABC / inactive sn 99
}

// recForm inserts a FORM part, an active form (revision 2) and three steps (heading, two data
// rows), the form's test_order pointing at them.
func recForm(t *testing.T, h *Handler) recFix {
	t.Helper()
	var f recFix
	f.formPartID, f.formPN = repPart(t, h, "FORM")
	f.formID = repInsert(t, h,
		`INSERT INTO form (part_number_id, test_order, is_locked, is_active, record_types, instrument_types, revision)
		 VALUES ($1, '', FALSE, TRUE, 'New Release,Re-Test', '', 2) RETURNING id`,
		[]any{f.formPartID}, `DELETE FROM form WHERE id=$1`)
	step := func(typ int, param, spec, units, min, nom, max string) int {
		return repInsert(t, h,
			`INSERT INTO form_row (form_id, type, parameter, specification, spec_units, spec_min, spec_nom, spec_max)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			[]any{f.formID, typ, param, spec, units, min, nom, max},
			`DELETE FROM form_row_history WHERE form_row_id=$1`, `DELETE FROM form_row WHERE id=$1`)
	}
	f.s1 = step(1, "Heading A", "", "", "", "", "")
	f.s2 = step(0, "Output Voltage", "4 to 6", "V", "4", "5", "6")
	f.s3 = step(0, "Ripple", "under 1", "mV", "", "", "1")
	repExec(t, h, `UPDATE form SET test_order=$1 WHERE id=$2`,
		fmt.Sprintf("%d,%d,%d", f.s1, f.s2, f.s3), f.formID)
	return f
}

// recRecord inserts one form_record (nil-able columns as given) under formID.
func recRecord(t *testing.T, h *Handler, f recFix, partID any, sn any, date string, rtype string, locked, active bool, rev any) int {
	t.Helper()
	return repInsert(t, h,
		`INSERT INTO form_record (form_id, part_id, record_date, serial_number, subject_part_number, subject_pn_description,
		   test_order, record_type, is_locked, is_approved, is_active, form_revision)
		 VALUES ($1,$2,$3::timestamp,$4,$5,'asm desc',$6,$7,$8,FALSE,$9,$10) RETURNING id`,
		[]any{f.formID, partID, date, sn, f.asmPN, fmt.Sprintf("%d,%d,%d", f.s1, f.s2, f.s3), rtype, locked, active, rev},
		`DELETE FROM record_event_results WHERE event_id IN (SELECT id FROM record_events WHERE form_record_id=$1)`,
		`DELETE FROM record_events WHERE form_record_id=$1`,
		`DELETE FROM result WHERE form_record_id=$1`,
		`DELETE FROM form_record WHERE id=$1`)
}

func recResult(t *testing.T, h *Handler, recordID, stepID int, typ int, param, result string, pf any, comment string) {
	t.Helper()
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, result, pass_fail, comment)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, recordID, stepID, typ, param, result, pf, comment)
}

// recSetup builds the full fixture: an assembly (lot-tracked, with a BOM) under test, a lot, a form and
// four records (order in the list: C, A, B; D is inactive).
func recSetup(t *testing.T, h *Handler) recFix {
	t.Helper()
	f := recForm(t, h)
	f.asmID, f.asmPN = repPart(t, h, "ASM")
	repExec(t, h, `UPDATE part SET tracking_mode='lot' WHERE id=$1`, f.asmID)
	f.compID, f.compPN = repPart(t, h, "BUY")
	repInsert(t, h, `INSERT INTO bom (parent_part_id, component_part_id) VALUES ($1,$2) RETURNING id`,
		[]any{f.formPartID, f.compID}, `DELETE FROM bom WHERE id=$1`)
	repInsert(t, h, `INSERT INTO bom (parent_part_id, component_part_id) VALUES ($1,$2) RETURNING id`,
		[]any{f.asmID, f.compID}, `DELETE FROM bom WHERE id=$1`)
	f.lotNumber = smokeUniq("LOT")
	f.lotID = repInsert(t, h, `INSERT INTO lot (part_id, lot_number, lot_description) VALUES ($1,$2,'rec lot') RETURNING id`,
		[]any{f.asmID, f.lotNumber}, `UPDATE form_record SET lot_id=NULL WHERE lot_id=$1`, `DELETE FROM lot WHERE id=$1`)

	f.rA = recRecord(t, h, f, f.asmID, "10", "2026-07-01 10:00", "New Release", false, true, 2)
	f.rB = recRecord(t, h, f, f.asmID, "9", "2026-07-02 09:30", "Re-Test", true, true, nil)
	f.rC = recRecord(t, h, f, nil, "ABC", "2026-07-03 08:00", "New Release", false, true, 0)
	f.rD = recRecord(t, h, f, nil, "99", "2026-07-04 08:00", "Hidden Type", false, false, 2)
	repExec(t, h, `UPDATE form_record SET lot_id=$1 WHERE id=$2`, f.lotID, f.rA)

	recResult(t, h, f.rA, f.s1, 1, "Heading A", "", nil, "")
	recResult(t, h, f.rA, f.s2, 0, "Output Voltage", "5.1", true, "steady")
	recResult(t, h, f.rA, f.s3, 0, "Ripple", "2.2", false, "noisy")
	recResult(t, h, f.rB, f.s1, 1, "Heading A", "", nil, "")
	recResult(t, h, f.rB, f.s2, 0, "Output Voltage", "4.9", true, "")
	recResult(t, h, f.rB, f.s3, 0, "Ripple", "0.3", nil, "")
	return f
}

func recGet(h http.HandlerFunc, target string, ids ...int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	switch len(ids) {
	case 1:
		req = withID(req, ids[0])
	case 2:
		req = withIDAndTestID(req, ids[0], ids[1])
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

type recRow struct {
	ID        int    `json:"id"`
	PartID    int    `json:"pnId"`
	SN        string `json:"sn"`
	SNPN      string `json:"snPN"`
	SNDesc    string `json:"snDesc"`
	Date      string `json:"date"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	FormRev   string `json:"formRev"`
	FormID    int    `json:"formId"`
	FormLabel string `json:"formLabel"`
}

func recRows(t *testing.T, rec *httptest.ResponseRecorder) []recRow {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var out []recRow
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func recIDs(rows []recRow) []int {
	ids := make([]int, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

func TestIntegration_RecordsReads_RecordsRows(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	rows := recRows(t, recGet(h.RecordsRows, "/api/forms/x/records/rows", f.formID))
	// Non-numeric serial sorts first (NULL int, DESC), then 10, then 9; the inactive record is excluded.
	if got, want := fmt.Sprint(recIDs(rows)), fmt.Sprint([]int{f.rC, f.rA, f.rB}); got != want {
		t.Fatalf("record order = %s, want %s", got, want)
	}
	a, b, c := rows[1], rows[2], rows[0]
	if a.SN != "10" || a.Status != "wip" || a.Type != "New Release" || a.Date != "2026-07-01 10:00" ||
		a.SNPN != f.asmPN || a.SNDesc != "asm desc" || a.PartID != f.asmID || a.FormRev != "Rev 2" {
		t.Errorf("row A = %+v", a)
	}
	if b.Status != "complete" || b.FormRev != "" || b.Type != "Re-Test" {
		t.Errorf("row B = %+v (want complete, blank form rev)", b)
	}
	if c.PartID != 0 || c.FormRev != "Draft" {
		t.Errorf("row C = %+v (want part 0, Draft)", c)
	}
	repExec(t, h, `UPDATE form_record SET is_approved=TRUE WHERE id=$1`, f.rB)
	if got := recRows(t, recGet(h.RecordsRows, "/x", f.formID))[2].Status; got != "approved" {
		t.Errorf("approved record status = %q", got)
	}
}

// A record with NULL text columns currently 500s the whole list (Scan into a plain string).
func TestIntegration_RecordsReads_RecordsRowsNullColumns(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	repInsert(t, h, `INSERT INTO form_record (form_id, is_active) VALUES ($1, TRUE) RETURNING id`,
		[]any{f.formID}, `DELETE FROM form_record WHERE id=$1`)

	rec := recGet(h.RecordsRows, "/x", f.formID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (NULL columns must not sink the list)", rec.Code)
	}
	rows := recRows(t, rec)
	if len(rows) != 1 || rows[0].SN != "" || rows[0].Type != "" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestIntegration_RecordsReads_RecordsListPage(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	rec := recGet(h.RecordsList, "/forms/x/records?locked=3", f.formID)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{f.formPN, "Rev 2", `<option value="New Release">`, `<option value="Re-Test">`, "Marked 3 record(s) complete."} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "Hidden Type") {
		t.Errorf("inactive record's type leaked into the filter options")
	}
	if rec := recGet(h.RecordsList, "/x", 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("missing form: status %d, want 404", rec.Code)
	}
}

func TestIntegration_RecordsReads_FormsList(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	_, inactivePN := repPart(t, h, "FORM")

	body := recGet(h.FormsList, "/records").Body.String()
	if !strings.Contains(body, f.formPN) || !strings.Contains(body, "Rev 2") {
		t.Errorf("form list missing %s / Rev 2", f.formPN)
	}
	if strings.Contains(body, inactivePN) {
		t.Errorf("part without a form listed")
	}
}

func TestIntegration_RecordsReads_ScopedRecords(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	// Part scope: records whose part_id is the assembly, newest first, across forms.
	rows := recRows(t, recGet(h.PartRecordsRows, "/x", f.asmID))
	if got, want := fmt.Sprint(recIDs(rows)), fmt.Sprint([]int{f.rB, f.rA}); got != want {
		t.Fatalf("part-scoped ids = %s, want %s", got, want)
	}
	if rows[1].FormID != f.formID || rows[1].FormLabel != f.formPN+" — rpt desc" || rows[1].FormRev != "Rev 2" {
		t.Errorf("row = %+v", rows[1])
	}
	// Lot scope: only the record linked to the lot.
	req := withIDAndLotID(httptest.NewRequest(http.MethodGet, "/x", nil), f.asmID, f.lotID)
	rec := httptest.NewRecorder()
	h.LotRecordsRows(rec, req)
	if got := recIDs(recRows(t, rec)); len(got) != 1 || got[0] != f.rA {
		t.Errorf("lot-scoped ids = %v, want [%d]", got, f.rA)
	}
	// Type options (Part records page): distinct, sorted, non-empty, active only.
	body := recGet(h.PartRecords, "/part/x/records", f.asmID).Body.String()
	for _, want := range []string{`<option value="New Release">`, `<option value="Re-Test">`} {
		if !strings.Contains(body, want) {
			t.Errorf("part records page missing %q", want)
		}
	}
}

func TestIntegration_RecordsReads_RecordDetail(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	ev1 := repInsert(t, h, `INSERT INTO record_events (form_record_id, event_type, username, event_date, comments)
		VALUES ($1,'completed','evuser1','2026-07-02 10:00+00','first pass') RETURNING id`, []any{f.rB})
	repInsert(t, h, `INSERT INTO record_events (form_record_id, event_type, username, event_date, comments)
		VALUES ($1,'unlocked','evuser2','2026-07-02 11:00+00',NULL) RETURNING id`, []any{f.rB})
	ev3 := repInsert(t, h, `INSERT INTO record_events (form_record_id, event_type, username, event_date)
		VALUES ($1,'completed','evuser3','2026-07-02 12:00+00') RETURNING id`, []any{f.rB})
	repExec(t, h, `INSERT INTO record_event_results (event_id, form_row_id, parameter, result, pass_fail) VALUES ($1,$2,'Output Voltage','4.9',TRUE)`, ev1, f.s2)
	repExec(t, h, `INSERT INTO record_event_results (event_id, form_row_id, parameter, result, pass_fail) VALUES ($1,$2,'Output Voltage','5.5',TRUE)`, ev3, f.s2)

	// Middle record A: frozen rows, heading, results, prev/next (list order C, A, B), lot link.
	body := recGet(h.RecordDetail, "/records/x", f.rA).Body.String()
	for _, want := range []string{"Heading A", "Output Voltage", "5.1", "noisy", fmt.Sprintf("/records/%d", f.rC), fmt.Sprintf("/records/%d", f.rB), f.lotNumber, f.formPN} {
		if !strings.Contains(body, want) {
			t.Errorf("record A detail missing %q", want)
		}
	}
	// B has the audit trail: three events oldest first, and the snapshot diff.
	rec := recGet(h.RecordDetail, "/records/x", f.rB)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("record B: status %d", rec.Code)
	}
	for _, want := range []string{"Audit log (3)", "evuser1", "evuser2", "evuser3", "first pass", "5.5", "4.9"} {
		if !strings.Contains(body, want) {
			t.Errorf("record B detail missing %q", want)
		}
	}
	if i, j := strings.Index(body, "evuser1"), strings.Index(body, "evuser3"); i > j {
		t.Errorf("audit trail not oldest-first")
	}
	if rec := recGet(h.RecordDetail, "/x", 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("missing record: status %d, want 404", rec.Code)
	}
}

// A record with NULL text columns currently 500s the detail page.
func TestIntegration_RecordsReads_RecordDetailNullColumns(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	id := repInsert(t, h, `INSERT INTO form_record (form_id, is_active) VALUES ($1, TRUE) RETURNING id`,
		[]any{f.formID}, `DELETE FROM form_record WHERE id=$1`)
	if rec := recGet(h.RecordDetail, "/x", id); rec.Code != http.StatusOK {
		t.Errorf("status %d, want 200", rec.Code)
	}
}

func TestIntegration_RecordsReads_RecordPrintAndEdit(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	body := recGet(h.RecordPrint, "/records/x/print", f.rA).Body.String()
	for _, want := range []string{"Heading A", "Output Voltage", "5.1", f.formPN} {
		if !strings.Contains(body, want) {
			t.Errorf("print missing %q", want)
		}
	}
	if rec := recGet(h.RecordPrint, "/x", 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("print missing record: status %d", rec.Code)
	}

	// Edit page: lot-tracked assembly offers its active lot (selected) and is buildable.
	rec := recGet(h.EditRecord, "/records/x/edit", f.rA)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: status %d", rec.Code)
	}
	for _, want := range []string{"Output Voltage", "5.1", f.lotNumber, fmt.Sprintf(`value="%d" selected`, f.lotID), f.compPN} {
		if !strings.Contains(body, want) {
			t.Errorf("edit page missing %q", want)
		}
	}
	// A locked record redirects to the read-only view.
	rec = recGet(h.EditRecord, "/x", f.rB)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != fmt.Sprintf("/records/%d", f.rB) {
		t.Errorf("locked edit: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestIntegration_RecordsReads_NewRecord(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	body := recGet(h.NewRecord, "/forms/x/records/new", f.formID).Body.String()
	// BOM parts under the form's own PN; next serial = max numeric serial + 1 over ALL records
	// (the inactive sn 99 counts).
	for _, want := range []string{f.compPN, `name="serial_number" value="100"`} {
		if !strings.Contains(body, want) {
			t.Errorf("new record page missing %q", want)
		}
	}
	empty := recForm(t, h)
	if body := recGet(h.NewRecord, "/x", empty.formID).Body.String(); !strings.Contains(body, `name="serial_number" value="1"`) {
		t.Errorf("first serial should be 1")
	}
	if rec := recGet(h.NewRecord, "/x", 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("missing form: status %d", rec.Code)
	}
}

func TestIntegration_RecordsReads_TestReport(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	body := recGet(h.TestReport, "/forms/x/tests/y/report", f.formID, f.s2).Body.String()
	for _, want := range []string{"Output Voltage", "4 to 6", "V", f.formPN} {
		if !strings.Contains(body, want) {
			t.Errorf("report page missing %q", want)
		}
	}
	// A step of another form, or an unknown step, is a 404.
	other := recForm(t, h)
	if rec := recGet(h.TestReport, "/x", f.formID, other.s2); rec.Code != http.StatusNotFound {
		t.Errorf("foreign step: status %d, want 404", rec.Code)
	}
	if rec := recGet(h.TestReport, "/x", 999999999, f.s2); rec.Code != http.StatusNotFound {
		t.Errorf("missing form: status %d, want 404", rec.Code)
	}

	type reportRow struct {
		ID         int    `json:"id"`
		SN         string `json:"sn"`
		Date       string `json:"date"`
		ResultDate string `json:"resultDate"`
		Result     string `json:"result"`
		PF         string `json:"pf"`
		Comment    string `json:"comment"`
	}
	get := func(step int) []reportRow {
		rec := recGet(h.TestReportRows, "/x", f.formID, step)
		var out []reportRow
		if rec.Code != http.StatusOK {
			t.Fatalf("rows status %d: %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	s2 := get(f.s2)
	if len(s2) != 2 || s2[0].ID != f.rA || s2[1].ID != f.rB || s2[0].SN != "10" || s2[0].Result != "5.1" ||
		s2[0].PF != "PASS" || s2[0].Comment != "steady" || s2[0].Date != "2026-07-01 10:00" || s2[0].ResultDate == "" {
		t.Errorf("s2 rows = %+v", s2)
	}
	s3 := get(f.s3)
	if len(s3) != 2 || s3[0].PF != "FAIL" || s3[1].PF != "—" {
		t.Errorf("s3 rows = %+v", s3)
	}
	if rec := recGet(h.TestReportRows, "/x", f.formID, other.s2); rec.Code != http.StatusNotFound {
		t.Errorf("foreign step rows: status %d, want 404", rec.Code)
	}
}

func TestIntegration_RecordsReads_YieldAndFailureModesDateRange(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)

	total := regexp.MustCompile(`fs-3 fw-bold[a-z -]*">(\d+)</span>`)
	yield := func(q string) []string {
		body := recGet(h.RecordsYieldSummary, "/forms/x/yield"+q, f.formID).Body.String()
		var out []string
		for _, m := range total.FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}
	// Total/Passed/Failed. Records A (fail), B (pass), C (no results → pass); D inactive.
	if got := yield(""); len(got) < 3 || got[0] != "3" || got[1] != "2" || got[2] != "1" {
		t.Errorf("yield all = %v, want total 3 passed 2 failed 1", got)
	}
	if got := yield("?from=2026-07-02&to=2026-07-02"); len(got) < 3 || got[0] != "1" || got[1] != "1" || got[2] != "0" {
		t.Errorf("yield 07-02 = %v, want total 1 passed 1 failed 0", got)
	}
	if got := yield("?to=2026-07-01"); len(got) < 3 || got[0] != "1" || got[2] != "1" {
		t.Errorf("yield to 07-01 = %v, want total 1 failed 1", got)
	}
	if rec := recGet(h.RecordsYieldSummary, "/x", 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("missing form: status %d", rec.Code)
	}

	fm := func(q string) string {
		rec := recGet(h.RecordsFailureModes, "/forms/x/failure-modes"+q, f.formID)
		if rec.Code != http.StatusOK {
			t.Fatalf("failure modes%s: status %d", q, rec.Code)
		}
		return rec.Body.String()
	}
	all := fm("")
	// Ripple: 1 failure of 1 tested (B's is not evaluated); Output Voltage: 0 of 2. Ranked by failures.
	if i, j := strings.Index(all, "Ripple"), strings.Index(all, "Output Voltage"); i < 0 || j < 0 || i > j {
		t.Errorf("failure modes order/content wrong")
	}
	if !strings.Contains(all, ">100.0%<") {
		t.Errorf("failure modes missing Ripple's 100.0%% rate")
	}
	ranged := fm("?from=2026-07-02")
	if strings.Contains(ranged, "Ripple") || !strings.Contains(ranged, "Output Voltage") {
		t.Errorf("failure modes from 07-02 should keep only Output Voltage (B)")
	}
	if !strings.Contains(fm("?from=2030-01-01"), "No tested results in this date range.") {
		t.Errorf("empty range message missing")
	}
}

// A result whose form_row_id has no parameter text anywhere gives a NULL step label; the row is
// currently dropped silently (Scan into a plain string, error swallowed).
func TestIntegration_RecordsReads_FailureModesNullParameter(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	rid := recRecord(t, h, f, nil, "1", "2026-07-01 10:00", "x", false, true, 1)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, pass_fail) VALUES ($1,$2,0,FALSE)`, rid, f.s2)

	body := recGet(h.RecordsFailureModes, "/x", f.formID).Body.String()
	if strings.Contains(body, "No tested results in this date range.") {
		t.Errorf("row with NULL parameter was dropped")
	}
}

func TestIntegration_RecordsReads_FormPickers(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)
	_, freePN := repPart(t, h, "FORM")

	body := recGet(h.NewForm, "/forms/new").Body.String()
	if !strings.Contains(body, freePN) {
		t.Errorf("new form: FORM part without a form missing from the PN picker")
	}
	if !strings.Contains(body, f.formPN) {
		t.Errorf("new form: existing form missing from the copy-from list")
	}
	body = recGet(h.DuplicateForm, "/forms/x/duplicate", f.formID).Body.String()
	if !strings.Contains(body, "(3 steps)") || !strings.Contains(body, freePN) {
		t.Errorf("duplicate form page: steps/PN picker wrong")
	}
	if rec := recGet(h.DuplicateForm, "/x", 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("missing form: status %d", rec.Code)
	}
}

// Steps of a form, unchanged (no timestamps compared): both def pages and the header fields.
func TestIntegration_RecordsReads_FormDefPages(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)
	repExec(t, h, `UPDATE form_row SET archived=TRUE WHERE id=$1`, f.s3)

	body := recGet(h.FormDef, "/forms/x/def", f.formID).Body.String()
	for _, want := range []string{"Heading A", "Output Voltage", "Ripple", "Show archived", "Rev 2", f.formPN} {
		if !strings.Contains(body, want) {
			t.Errorf("form def missing %q", want)
		}
	}
	body = recGet(h.EditFormDef, "/forms/x/def/edit", f.formID).Body.String()
	for _, want := range []string{"Output Voltage", "Ripple", "New Release,Re-Test", "Show archived"} {
		if !strings.Contains(body, want) {
			t.Errorf("form def edit missing %q", want)
		}
	}
	for name, fn := range map[string]http.HandlerFunc{"FormDef": h.FormDef, "EditFormDef": h.EditFormDef, "FormDefHistory": h.FormDefHistory} {
		if rec := recGet(fn, "/x?at=2026-07-01", 999999999); rec.Code != http.StatusNotFound && name != "FormDefHistory" {
			t.Errorf("%s missing form: status %d, want 404", name, rec.Code)
		}
	}
}

func TestIntegration_RecordsReads_FormDefTimeline(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recSetup(t, h)
	// Two history rows on two days for one step, one for another on the first day.
	for _, c := range [][]any{{f.s2, "2026-07-01 12:00+00"}, {f.s3, "2026-07-01 13:00+00"}, {f.s2, "2026-07-05 12:00+00"}} {
		repExec(t, h, `INSERT INTO form_row_history (form_row_id, changed_at, changed_by) VALUES ($1,$2::timestamptz,'t')`, c...)
	}
	req := userCtxTZ(withID(httptest.NewRequest(http.MethodGet, "/forms/x/def", nil), f.formID), "UTC")
	rec := httptest.NewRecorder()
	h.FormDef(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`2026-07-01 — 2 changes`, `2026-07-05 — 1 change"`} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline missing %q", want)
		}
	}
}
