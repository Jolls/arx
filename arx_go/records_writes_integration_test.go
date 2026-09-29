//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Characterization tests for the records write paths (#249, #223), written against the pre-sqlc
// code. Fixtures are committed throwaway rows (recForm / recRecord / repInsert) removed LIFO on
// cleanup; rows a handler creates get their own cleanup registered after the fixtures.

func wScan(t *testing.T, h *Handler, q string, args []any, dest ...any) {
	t.Helper()
	if err := h.queryRowContext(context.Background(), q, args...).Scan(dest...); err != nil {
		t.Fatalf("%q: %v", q, err)
	}
}

func wInts(t *testing.T, h *Handler, q string, args ...any) []int {
	t.Helper()
	rows, err := h.queryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%q: %v", q, err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%q: %v", q, err)
	}
	return out
}

// wPost runs a POST handler as the seeded admin with one ("id") or two ("id", "testID") route ids.
func wPost(fn http.HandlerFunc, target string, vals url.Values, ids ...int) *httptest.ResponseRecorder {
	req := adminCtx(postForm(target, vals))
	switch len(ids) {
	case 1:
		req = withID(req, ids[0])
	case 2:
		req = withIDAndTestID(req, ids[0], ids[1])
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// wNewID parses the id out of a 303 to prefix+"N/...".
func wNewID(t *testing.T, rec *httptest.ResponseRecorder, prefix string) int {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303; body %s", rec.Code, rec.Body.String())
	}
	loc := strings.TrimPrefix(rec.Header().Get("Location"), prefix)
	id, err := strconv.Atoi(strings.SplitN(loc, "/", 2)[0])
	if err != nil {
		t.Fatalf("Location %q: %v", rec.Header().Get("Location"), err)
	}
	return id
}

// wCleanupRecord removes a record a handler created.
func wCleanupRecord(t *testing.T, h *Handler, id int) {
	t.Cleanup(func() {
		ctx := context.Background()
		smokeExec(ctx, h, `DELETE FROM record_event_results WHERE event_id IN (SELECT id FROM record_events WHERE form_record_id=$1)`, id)
		smokeExec(ctx, h, `DELETE FROM record_events WHERE form_record_id=$1`, id)
		smokeExec(ctx, h, `DELETE FROM result WHERE form_record_id=$1`, id)
		smokeExec(ctx, h, `DELETE FROM form_record WHERE id=$1`, id)
	})
}

// wCleanupForm removes the steps, history and events a handler added to formID (and the form).
func wCleanupForm(t *testing.T, h *Handler, formID int, alsoForm bool) {
	t.Cleanup(func() {
		ctx := context.Background()
		smokeExec(ctx, h, `DELETE FROM form_row_history WHERE form_row_id IN (SELECT id FROM form_row WHERE form_id=$1)`, formID)
		smokeExec(ctx, h, `DELETE FROM form_row WHERE form_id=$1`, formID)
		smokeExec(ctx, h, `DELETE FROM form_events WHERE form_id=$1`, formID)
		if alsoForm {
			smokeExec(ctx, h, `DELETE FROM form WHERE id=$1`, formID)
		}
	})
}

// wStep inserts a form_row with type/parameter plus the extra columns given (nil = NULL).
func wStep(t *testing.T, h *Handler, formID, typ int, param string, extra map[string]any) int {
	t.Helper()
	cols, args := []string{"form_id", "type", "parameter"}, []any{formID, typ, param}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cols, args = append(cols, k), append(args, extra[k])
	}
	ph := make([]string, len(cols))
	for i := range ph {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	return repInsert(t, h, fmt.Sprintf(`INSERT INTO form_row (%s) VALUES (%s) RETURNING id`, strings.Join(cols, ","), strings.Join(ph, ",")),
		args, `DELETE FROM form_row_history WHERE form_row_id=$1`, `DELETE FROM form_row WHERE id=$1`)
}

func wOrder(ids ...int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.Itoa(id)
	}
	return strings.Join(s, ",")
}

// ── CreateRecord ────────────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_CreateRecordMaterializes(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	subjID, subjPN := repPart(t, h, "ASM")
	s4 := wStep(t, h, f.formID, 0, "Current", map[string]any{"specification": "{min}..{max}", "spec_min": "1", "spec_max": "3",
		"format": "F2", "hide_formula": "HIDE", "default_result": "x", "pf_type": "range"})
	s5 := wStep(t, h, f.formID, 0, "Retired", map[string]any{"archived": true})
	s6 := wStep(t, h, f.formID, 0, "Scope only", map[string]any{"instrument_types": "Scope"})
	s7 := wStep(t, h, f.formID, 0, "Meter too", map[string]any{"instrument_types": " meter , Probe"})
	order := wOrder(f.s1, f.s2, f.s3, s4, s5, s6, s7, 999999999)
	repExec(t, h, `UPDATE form SET test_order=$1 WHERE id=$2`, order, f.formID)

	rec := wPost(h.CreateRecord, "/forms/x/records/new", url.Values{
		"serial_number": {"SN-W1"}, "suggested_serial_number": {"1"}, "record_type": {"New Release"},
		"instrument_type": {"Meter"}, "bom_pnid": {strconv.Itoa(subjID)}, "record_date": {"2026-07-05T08:30"},
	}, f.formID)
	id := wNewID(t, rec, "/records/")
	wCleanupRecord(t, h, id)

	var partID, rev int
	var sn, pn, desc, rtype, itype, to, date string
	var notesNull, lotNull, buildNull, unitNull, active, locked, approved bool
	wScan(t, h, `SELECT part_id, serial_number, subject_part_number, subject_pn_description, record_type, instrument_type,
		test_order, to_char(record_date,'YYYY-MM-DD HH24:MI'), form_revision, notes IS NULL, lot_id IS NULL, build_id IS NULL,
		unit_id IS NULL, is_active, is_locked, is_approved FROM form_record WHERE id=$1`, []any{id},
		&partID, &sn, &pn, &desc, &rtype, &itype, &to, &date, &rev, &notesNull, &lotNull, &buildNull, &unitNull, &active, &locked, &approved)
	if partID != subjID || sn != "SN-W1" || pn != subjPN || desc != "rpt desc" || rtype != "New Release" || itype != "Meter" ||
		to != order || date != "2026-07-05 08:30" || rev != 2 || !notesNull || !lotNull || !buildNull || !unitNull || !active || locked || approved {
		t.Errorf("record = part %d sn %q pn %q desc %q type %q instr %q order %q date %q rev %d notes/lot/build/unit NULL %v/%v/%v/%v active %v locked %v approved %v",
			partID, sn, pn, desc, rtype, itype, to, date, rev, notesNull, lotNull, buildNull, unitNull, active, locked, approved)
	}

	// Heading kept; archived, unknown and other-instrument steps skipped; creation order.
	if got, want := fmt.Sprint(wInts(t, h, `SELECT form_row_id FROM result WHERE form_record_id=$1 ORDER BY id`, id)),
		fmt.Sprint([]int{f.s1, f.s2, f.s3, s4, s7}); got != want {
		t.Fatalf("materialized steps = %s, want %s", got, want)
	}
	var typ int
	var param, spec, min, format, hide, def, pf string
	var resNull, pfNull, commentNull bool
	wScan(t, h, `SELECT type, parameter, specification, spec_min, format, hide_formula, default_result, pf_type,
		result IS NULL, pass_fail IS NULL, comment IS NULL FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{id, s4},
		&typ, &param, &spec, &min, &format, &hide, &def, &pf, &resNull, &pfNull, &commentNull)
	if typ != 0 || param != "Current" || spec != "1..3" || min != "1" || format != "F2" || hide != "HIDE" || def != "x" || pf != "range" ||
		!resNull || !pfNull || !commentNull {
		t.Errorf("s4 snapshot = %d %q %q %q %q %q %q %q result/pf/comment NULL %v/%v/%v", typ, param, spec, min, format, hide, def, pf, resNull, pfNull, commentNull)
	}
	// A NULL definition column is materialized as '' (not NULL); the heading keeps its type.
	var s7MinNull bool
	wScan(t, h, `SELECT spec_min IS NULL FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{id, s7}, &s7MinNull)
	wScan(t, h, `SELECT type FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{id, f.s1}, &typ)
	if s7MinNull || typ != 1 {
		t.Errorf("s7 spec_min NULL = %v (want ''), heading type = %d", s7MinNull, typ)
	}

	// No / unknown BOM part: part_id NULL, subject fields ''.
	for _, bom := range []string{"", "2147483000"} {
		id2 := wNewID(t, wPost(h.CreateRecord, "/forms/x/records/new", url.Values{
			"serial_number": {"SN-W2"}, "suggested_serial_number": {"1"}, "bom_pnid": {bom},
		}, f.formID), "/records/")
		wCleanupRecord(t, h, id2)
		var partNull bool
		wScan(t, h, `SELECT part_id IS NULL, subject_part_number, subject_pn_description FROM form_record WHERE id=$1`, []any{id2}, &partNull, &pn, &desc)
		if !partNull || pn != "" || desc != "" {
			t.Errorf("bom %q: part NULL %v pn %q desc %q", bom, partNull, pn, desc)
		}
	}

	assertStatus(t, "CreateRecord missing form", wPost(h.CreateRecord, "/forms/x/records/new", url.Values{}, 2147483000), http.StatusNotFound)
}

// A form whose test_order or part description is NULL (pre-sqlc: scanned into a plain string, 500).
func TestIntegration_RecordsWrites_CreateRecordNullColumns(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	for _, q := range []string{`UPDATE form SET test_order=NULL WHERE id=$1`,
		`UPDATE part SET description=NULL WHERE id=(SELECT part_number_id FROM form WHERE id=$1)`} {
		f := recForm(t, h)
		repExec(t, h, q, f.formID)
		rec := wPost(h.CreateRecord, "/forms/x/records/new", url.Values{"serial_number": {"N1"}}, f.formID)
		t.Cleanup(func() {
			smokeExec(context.Background(), h, `DELETE FROM result WHERE form_record_id IN (SELECT id FROM form_record WHERE form_id=$1)`, f.formID)
			smokeExec(context.Background(), h, `DELETE FROM form_record WHERE form_id=$1`, f.formID)
		})
		id := wNewID(t, rec, "/records/")
		var to string
		wScan(t, h, `SELECT test_order FROM form_record WHERE id=$1`, []any{id}, &to)
		if want := map[bool]string{true: "", false: wOrder(f.s1, f.s2, f.s3)}[strings.Contains(q, "test_order")]; to != want {
			t.Errorf("%s: record test_order %q, want %q", q, to, want)
		}
	}
}

// ── SaveResults ─────────────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_SaveResults(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	s4 := wStep(t, h, f.formID, 0, "Fmt", map[string]any{"spec_max": "10", "format": "F2"})
	s5 := wStep(t, h, f.formID, 0, "Same", nil)
	s6 := wStep(t, h, f.formID, 0, "Cleared", nil)
	r := recRecord(t, h, f, nil, "77", "2026-07-01 10:00", "New Release", false, true, 2)
	// s2's frozen spec (0..1) differs from the live one (4..6): P/F follows the snapshot.
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, result, comment, spec_min, spec_max, pass_fail)
		VALUES ($1,$2,0,'Output Voltage','5.1','c','0','1',TRUE)`, r, f.s2)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, result, comment, updated_at)
		VALUES ($1,$2,0,'Same','same','same','2020-01-01')`, r, s5)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, result, pass_fail) VALUES ($1,$2,0,'Cleared','9',TRUE)`, r, s6)

	rec := wPost(h.SaveResults, "/records/x/edit", url.Values{
		fmt.Sprintf("result_%d", f.s2): {" 5.2 "}, fmt.Sprintf("comment_%d", f.s2): {"c"},
		fmt.Sprintf("result_%d", f.s3): {"0.5"}, fmt.Sprintf("comment_%d", f.s3): {"late"},
		fmt.Sprintf("result_%d", s4): {"3"},
		fmt.Sprintf("result_%d", s5): {"same"}, fmt.Sprintf("comment_%d", s5): {"same"},
		fmt.Sprintf("result_%d", s6): {""},
		"result_999999999":           {"x"},
		"record_type":                {" Re-Test "}, "instrument_type": {"Meter"}, "notes": {""}, "record_date": {"2026-08-01"},
	}, r)
	assertStatus(t, "SaveResults", rec, http.StatusSeeOther)

	var res, comment string
	var pf *bool
	wScan(t, h, `SELECT result, comment, pass_fail FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, f.s2}, &res, &comment, &pf)
	if res != "5.2" || comment != "c" || pf == nil || *pf {
		t.Errorf("s2 = %q %q %v, want 5.2 c FAIL (frozen 0..1)", res, comment, pf)
	}
	// Legacy path: no row yet → a snapshot row built from the live step.
	var param, spec, max, format string
	var typ int
	wScan(t, h, `SELECT result, comment, pass_fail, type, parameter, specification, spec_max, COALESCE(format,'<null>') FROM result
		WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, f.s3}, &res, &comment, &pf, &typ, &param, &spec, &max, &format)
	if res != "0.5" || comment != "late" || pf == nil || !*pf || typ != 0 || param != "Ripple" || spec != "under 1" || max != "1" {
		t.Errorf("s3 legacy row = %q %q %v %d %q %q %q", res, comment, pf, typ, param, spec, max)
	}
	wScan(t, h, `SELECT pass_fail, COALESCE(format,'<null>') FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, s4}, &pf, &format)
	// Pre-sqlc, SaveResults' own step query omitted format, so the legacy row stored ''.
	if pf == nil || !*pf || format != "F2" {
		t.Errorf("s4 legacy row pf %v format %q, want PASS F2", pf, format)
	}
	var upd string
	wScan(t, h, `SELECT to_char(updated_at,'YYYY-MM-DD') FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, s5}, &upd)
	if upd != "2020-01-01" {
		t.Errorf("unchanged s5 rewritten (updated_at %s)", upd)
	}
	wScan(t, h, `SELECT result, pass_fail FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, s6}, &res, &pf)
	if res != "" || pf != nil {
		t.Errorf("cleared s6 = %q %v, want '' NULL", res, pf)
	}
	var n int
	wScan(t, h, `SELECT COUNT(*) FROM result WHERE form_record_id=$1`, []any{r}, &n)
	if n != 5 {
		t.Errorf("result rows = %d, want 5 (unknown step ignored)", n)
	}

	var rtype, itype, date string
	var notesNull bool
	wScan(t, h, `SELECT record_type, instrument_type, notes IS NULL, to_char(record_date,'YYYY-MM-DD HH24:MI') FROM form_record WHERE id=$1`,
		[]any{r}, &rtype, &itype, &notesNull, &date)
	if rtype != "Re-Test" || itype != "Meter" || !notesNull || date != "2026-08-01 00:00" {
		t.Errorf("record = %q %q notes NULL %v date %q", rtype, itype, notesNull, date)
	}
	// No record_date posted: the date is kept.
	assertStatus(t, "SaveResults no date", wPost(h.SaveResults, "/records/x/edit", url.Values{"notes": {"hello"}}, r), http.StatusSeeOther)
	var notes string
	wScan(t, h, `SELECT notes, to_char(record_date,'YYYY-MM-DD HH24:MI'), record_type FROM form_record WHERE id=$1`, []any{r}, &notes, &date, &rtype)
	if notes != "hello" || date != "2026-08-01 00:00" || rtype != "" {
		t.Errorf("second save = notes %q date %q type %q", notes, date, rtype)
	}
}

// A record whose serial_number is NULL (pre-sqlc: scanned into a plain string, 500).
func TestIntegration_RecordsWrites_SaveResultsNullColumns(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	r := recRecord(t, h, f, nil, nil, "2026-07-01 10:00", "New Release", false, true, 2)
	assertStatus(t, "SaveResults NULL serial", wPost(h.SaveResults, "/records/x/edit", url.Values{"record_type": {"X"}}, r), http.StatusSeeOther)
	var rtype string
	var snNull bool
	wScan(t, h, `SELECT record_type, serial_number IS NULL FROM form_record WHERE id=$1`, []any{r}, &rtype, &snNull)
	if rtype != "X" || !snNull {
		t.Errorf("record_type %q, serial NULL %v", rtype, snNull)
	}
}

// ── ResyncRecord ────────────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_ResyncRecord(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	s4 := wStep(t, h, f.formID, 0, "Retired", map[string]any{"archived": true})
	s5 := wStep(t, h, f.formID, 0, "Scope only", map[string]any{"instrument_types": "Scope"})
	order := wOrder(f.s1, f.s2, f.s3, s4, s5)
	repExec(t, h, `UPDATE form SET test_order=$1 WHERE id=$2`, order, f.formID)
	r := recRecord(t, h, f, nil, "78", "2026-07-01 10:00", "New Release", false, true, 0)
	repExec(t, h, `UPDATE form_record SET test_order=$1, instrument_type='Meter' WHERE id=$2`, wOrder(f.s2), r)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, result, spec_max, pass_fail) VALUES ($1,$2,0,'Old','7','99',TRUE)`, r, f.s2)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, result) VALUES ($1,999999999,0,'Gone','g')`, r)

	assertStatus(t, "ResyncRecord", wPost(h.ResyncRecord, "/records/x/resync", nil, r), http.StatusSeeOther)

	var param, spec, min, nom, max, units, res string
	var pf *bool
	wScan(t, h, `SELECT parameter, specification, spec_min, spec_nom, spec_max, spec_units, result, pass_fail FROM result
		WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, f.s2}, &param, &spec, &min, &nom, &max, &units, &res, &pf)
	if param != "Output Voltage" || spec != "4 to 6" || min != "4" || nom != "5" || max != "6" || units != "V" || res != "7" || pf == nil || *pf {
		t.Errorf("s2 refreshed = %q %q %q %q %q %q %q %v", param, spec, min, nom, max, units, res, pf)
	}
	if got, want := fmt.Sprint(wInts(t, h, `SELECT form_row_id FROM result WHERE form_record_id=$1 ORDER BY form_row_id`, r)),
		fmt.Sprint([]int{f.s1, f.s2, f.s3, 999999999}); got != want {
		t.Errorf("rows after resync = %s, want %s (archived / other-instrument skipped, removed step kept)", got, want)
	}
	wScan(t, h, `SELECT parameter, result FROM result WHERE form_record_id=$1 AND form_row_id=999999999`, []any{r}, &param, &res)
	if param != "Gone" || res != "g" {
		t.Errorf("removed-step row touched: %q %q", param, res)
	}
	var to string
	var rev int
	wScan(t, h, `SELECT test_order, form_revision FROM form_record WHERE id=$1`, []any{r}, &to, &rev)
	if to != order || rev != 2 {
		t.Errorf("record test_order %q rev %d, want %q 2", to, rev, order)
	}

	// Locked: redirected, nothing changes.
	repExec(t, h, `UPDATE form_record SET is_locked=TRUE, test_order='' WHERE id=$1`, r)
	rec := wPost(h.ResyncRecord, "/records/x/resync", nil, r)
	assertStatus(t, "ResyncRecord locked", rec, http.StatusSeeOther)
	wScan(t, h, `SELECT test_order FROM form_record WHERE id=$1`, []any{r}, &to)
	if to != "" || rec.Header().Get("Location") != fmt.Sprintf("/records/%d", r) {
		t.Errorf("locked resync changed test_order to %q (Location %q)", to, rec.Header().Get("Location"))
	}
	assertStatus(t, "ResyncRecord missing", wPost(h.ResyncRecord, "/records/x/resync", nil, 2147483000), http.StatusNotFound)

	rNull := recRecord(t, h, f, nil, nil, "2026-07-01 10:00", "New Release", false, true, 0)
	// Pre-sqlc the NULL serial was scanned into a plain string and the resync 500'd.
	assertStatus(t, "ResyncRecord NULL serial", wPost(h.ResyncRecord, "/records/x/resync", nil, rNull), http.StatusSeeOther)
	wScan(t, h, `SELECT test_order FROM form_record WHERE id=$1`, []any{rNull}, &to)
	if to != order {
		t.Errorf("NULL-serial resync test_order %q, want %q", to, order)
	}
}

// ── DuplicateRecord ─────────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_DuplicateRecord(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	asmID, _ := repPart(t, h, "ASM")
	unitID := repInsert(t, h, `INSERT INTO unit (part_id, serial_number) VALUES ($1,$2) RETURNING id`, []any{asmID, smokeUniq("U")},
		`UPDATE form_record SET unit_id=NULL WHERE unit_id=$1`, `DELETE FROM unit WHERE id=$1`)
	src := recRecord(t, h, f, asmID, "55", "2026-07-01 10:00", "Re-Test", true, true, nil)
	repExec(t, h, `UPDATE form_record SET is_approved=TRUE, instrument_type='Meter', unit_id=$1, notes='src note' WHERE id=$2`, unitID, src)
	recResult(t, h, src, f.s1, 1, "Heading A", "", nil, "")
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max, spec_units,
		pf_type, format, hide_formula, default_result, result, comment, pass_fail)
		VALUES ($1,$2,0,'Output Voltage','4 to 6','4','5','6','V','range','F1','H','d','5.1','ok',TRUE)`, src, f.s2)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, result, pass_fail) VALUES ($1,$2,0,NULL,FALSE)`, src, f.s3)

	id := wNewID(t, wPost(h.DuplicateRecord, "/records/x/duplicate", nil, src), "/records/")
	wCleanupRecord(t, h, id)

	var same, fresh, locked, approved, active, notesNull, lotNull bool
	var rev, unit int
	wScan(t, h, `SELECT n.form_id = s.form_id AND n.part_id IS NOT DISTINCT FROM s.part_id AND n.serial_number IS NOT DISTINCT FROM s.serial_number
		  AND n.subject_part_number IS NOT DISTINCT FROM s.subject_part_number AND n.subject_pn_description IS NOT DISTINCT FROM s.subject_pn_description
		  AND n.record_type IS NOT DISTINCT FROM s.record_type AND n.instrument_type IS NOT DISTINCT FROM s.instrument_type
		  AND n.test_order IS NOT DISTINCT FROM s.test_order,
		  n.record_date > (now() - interval '1 day')::timestamp, n.is_locked, n.is_approved, n.is_active, n.form_revision, n.unit_id,
		  n.notes IS NULL, n.lot_id IS NULL
		FROM form_record n, form_record s WHERE n.id=$1 AND s.id=$2`, []any{id, src},
		&same, &fresh, &locked, &approved, &active, &rev, &unit, &notesNull, &lotNull)
	if !same || !fresh || locked || approved || !active || rev != 2 || unit != unitID || !notesNull || !lotNull {
		t.Errorf("duplicate = same %v fresh date %v locked %v approved %v active %v rev %d unit %d notes NULL %v lot NULL %v",
			same, fresh, locked, approved, active, rev, unit, notesNull, lotNull)
	}
	var copied, total int
	wScan(t, h, `SELECT COUNT(*) FROM result a JOIN result b ON b.form_record_id=$1 AND b.form_row_id=a.form_row_id
		WHERE a.form_record_id=$2 AND a.type=b.type AND a.parameter IS NOT DISTINCT FROM b.parameter
		  AND a.specification IS NOT DISTINCT FROM b.specification AND a.spec_min IS NOT DISTINCT FROM b.spec_min
		  AND a.spec_nom IS NOT DISTINCT FROM b.spec_nom AND a.spec_max IS NOT DISTINCT FROM b.spec_max
		  AND a.spec_units IS NOT DISTINCT FROM b.spec_units AND a.pf_type IS NOT DISTINCT FROM b.pf_type
		  AND a.format IS NOT DISTINCT FROM b.format AND a.hide_formula IS NOT DISTINCT FROM b.hide_formula
		  AND a.default_result IS NOT DISTINCT FROM b.default_result AND a.result IS NOT DISTINCT FROM b.result
		  AND a.comment IS NOT DISTINCT FROM b.comment AND a.pass_fail IS NOT DISTINCT FROM b.pass_fail`, []any{id, src}, &copied)
	wScan(t, h, `SELECT COUNT(*) FROM result WHERE form_record_id=$1`, []any{id}, &total)
	if copied != 3 || total != 3 {
		t.Errorf("copied results = %d identical of %d, want 3 of 3", copied, total)
	}

	assertStatus(t, "DuplicateRecord missing", wPost(h.DuplicateRecord, "/records/x/duplicate", nil, 2147483000), http.StatusNotFound)
	repExec(t, h, `UPDATE form_record SET record_type=NULL WHERE id=$1`, src)
	// Pre-sqlc the NULL record_type was scanned into a plain string and the duplicate 500'd; now it
	// is copied as NULL.
	id2 := wNewID(t, wPost(h.DuplicateRecord, "/records/x/duplicate", nil, src), "/records/")
	wCleanupRecord(t, h, id2)
	var typeNull bool
	wScan(t, h, `SELECT record_type IS NULL FROM form_record WHERE id=$1`, []any{id2}, &typeNull)
	if !typeNull {
		t.Error("duplicate of a NULL record_type isn't NULL")
	}
}

// ── Completion snapshot ─────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_CompletionSnapshotOrder(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	s9 := wStep(t, h, f.formID, 0, "Extra", nil)
	r := recRecord(t, h, f, nil, "80", "2026-07-01 10:00", "New Release", false, true, 2)
	repExec(t, h, `UPDATE form_record SET test_order=$1 WHERE id=$2`, wOrder(f.s3, f.s2), r)
	recResult(t, h, r, f.s1, 1, "Heading A", "", nil, "")
	recResult(t, h, r, f.s2, 0, "Output Voltage", "5", true, "")
	recResult(t, h, r, f.s3, 0, "Ripple", "0.2", true, "")
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type) VALUES ($1,$2,0)`, r, s9)

	assertStatus(t, "LockRecord", wPost(h.LockRecord, "/records/x/lock", nil, r), http.StatusSeeOther)

	// test_order first, then ids not in it ascending; headings excluded; NULL text stored as ''.
	if got, want := fmt.Sprint(wInts(t, h, `SELECT rer.form_row_id FROM record_event_results rer JOIN record_events re ON re.id=rer.event_id
		WHERE re.form_record_id=$1 ORDER BY rer.id`, r)), fmt.Sprint([]int{f.s3, f.s2, s9}); got != want {
		t.Errorf("snapshot order = %s, want %s", got, want)
	}
	var param, result, comment, user, evType string
	wScan(t, h, `SELECT rer.parameter, rer.result, rer.comment, re.username, re.event_type FROM record_event_results rer
		JOIN record_events re ON re.id=rer.event_id WHERE re.form_record_id=$1 AND rer.form_row_id=$2`, []any{r, s9},
		&param, &result, &comment, &user, &evType)
	if param != "" || result != "" || comment != "" || user != "admin" || evType != "completed" {
		t.Errorf("s9 snapshot = %q %q %q by %q (%s)", param, result, comment, user, evType)
	}
}

// ── Form lifecycle ──────────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_LockUnlockForm(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	wCleanupForm(t, h, f.formID, false)

	state := func() (locked bool, rev int, events string) {
		wScan(t, h, `SELECT f.is_locked, f.revision, COALESCE((SELECT string_agg(event_type || ':' || COALESCE(username,'') || ':' || COALESCE(comments,'<null>'), ',' ORDER BY id)
			FROM form_events WHERE form_id=f.id),'') FROM form f WHERE f.id=$1`, []any{f.formID}, &locked, &rev, &events)
		return
	}
	for i := 0; i < 2; i++ { // the second lock is a no-op
		rec := wPost(h.LockForm, "/forms/x/lock", nil, f.formID)
		assertStatus(t, "LockForm", rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/forms/%d/def", f.formID) {
			t.Errorf("Location %q", loc)
		}
	}
	if l, rev, ev := state(); !l || rev != 3 || ev != "locked:admin:<null>" {
		t.Errorf("after lock: locked %v rev %d events %q", l, rev, ev)
	}
	assertStatus(t, "UnlockForm no comment", wPost(h.UnlockForm, "/forms/x/unlock", url.Values{"comment": {"  "}}, f.formID), http.StatusBadRequest)
	for i := 0; i < 2; i++ {
		assertStatus(t, "UnlockForm", wPost(h.UnlockForm, "/forms/x/unlock", url.Values{"comment": {" why "}}, f.formID), http.StatusSeeOther)
	}
	if l, rev, ev := state(); l || rev != 3 || ev != "locked:admin:<null>,unlocked:admin:why" {
		t.Errorf("after unlock: locked %v rev %d events %q", l, rev, ev)
	}
}

// ── CreateForm / CreateDuplicate ────────────────────────────────────────────

// wSourceForm is recForm plus an all-NULL step, a step carrying every copied column, and a step
// outside test_order; the form's instrument_types is NULL.
func wSourceForm(t *testing.T, h *Handler) (recFix, []int) {
	f := recForm(t, h)
	sN := wStep(t, h, f.formID, 0, "Nulls", nil)
	sA := wStep(t, h, f.formID, 2, "Full", map[string]any{"archived": true, "archive_id": 7, "revision": 3, "category": "Cat",
		"sheet_name": "Sh", "comment": "cm", "instrument_types": "Scope", "format": "F3", "pf_type": "range", "default_result": "d",
		"hide_formula": "HIDE", "specification": "sp", "spec_nom": "n", "spec_min": "a", "spec_max": "b", "spec_units": "u"})
	wStep(t, h, f.formID, 0, "Not in order", nil)
	order := []int{f.s1, f.s2, sN, sA, f.s3}
	repExec(t, h, `UPDATE form SET test_order=$1, instrument_types=NULL WHERE id=$2`, wOrder(order...), f.formID)
	return f, order
}

// wCheckCopied asserts newFormID holds a copy of the source steps in order.
func wCheckCopied(t *testing.T, h *Handler, newFormID int, src []int) {
	t.Helper()
	var to string
	wScan(t, h, `SELECT test_order FROM form WHERE id=$1`, []any{newFormID}, &to)
	ids := wInts(t, h, `SELECT id FROM form_row WHERE form_id=$1 ORDER BY id`, newFormID)
	if to != wOrder(ids...) || len(ids) != len(src) {
		t.Fatalf("new test_order %q, steps %v (want %d, in id order)", to, ids, len(src))
	}
	for i, old := range src {
		var same, archived, specNull bool
		wScan(t, h, `SELECT COALESCE(a.type,0) = b.type AND COALESCE(a.parameter,'') = b.parameter
			  AND COALESCE(a.specification,'') = b.specification AND a.spec_nom IS NOT DISTINCT FROM b.spec_nom
			  AND a.spec_min IS NOT DISTINCT FROM b.spec_min AND a.spec_max IS NOT DISTINCT FROM b.spec_max
			  AND a.spec_units IS NOT DISTINCT FROM b.spec_units AND a.pf_type IS NOT DISTINCT FROM b.pf_type
			  AND a.default_result IS NOT DISTINCT FROM b.default_result AND a.hide_formula IS NOT DISTINCT FROM b.hide_formula
			  AND a.category IS NOT DISTINCT FROM b.category AND a.sheet_name IS NOT DISTINCT FROM b.sheet_name
			  AND a.instrument_types IS NOT DISTINCT FROM b.instrument_types AND a.format IS NOT DISTINCT FROM b.format
			  AND a.comment IS NOT DISTINCT FROM b.comment AND a.archive_id IS NOT DISTINCT FROM b.archive_id
			  AND a.revision IS NOT DISTINCT FROM b.revision,
			  b.archived, b.specification IS NULL
			FROM form_row a, form_row b WHERE a.id=$1 AND b.id=$2`, []any{old, ids[i]}, &same, &archived, &specNull)
		// archived isn't copied (every copy starts active); a NULL parameter/specification becomes ''.
		if !same || archived || specNull {
			t.Errorf("step %d (from %d): same %v archived %v specification NULL %v", ids[i], old, same, archived, specNull)
		}
	}
}

func TestIntegration_RecordsWrites_CreateForm(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	src, order := wSourceForm(t, h)

	p1, _ := repPart(t, h, "FORM")
	id := wNewID(t, wPost(h.CreateForm, "/forms/new", url.Values{"pnid": {strconv.Itoa(p1)}, "source_id": {strconv.Itoa(src.formID)}}), "/forms/")
	wCleanupForm(t, h, id, true)
	var pn, rev int
	var rtypes string
	var itypesNull, active, locked bool
	wScan(t, h, `SELECT part_number_id, revision, record_types, instrument_types IS NULL, is_active, is_locked FROM form WHERE id=$1`,
		[]any{id}, &pn, &rev, &rtypes, &itypesNull, &active, &locked)
	if pn != p1 || rev != 0 || rtypes != "New Release,Re-Test" || !itypesNull || !active || locked {
		t.Errorf("copied form = pn %d rev %d types %q instr NULL %v active %v locked %v", pn, rev, rtypes, itypesNull, active, locked)
	}
	wCheckCopied(t, h, id, order)

	// Blank form: no steps, empty order, NULL types.
	p2, _ := repPart(t, h, "FORM")
	id2 := wNewID(t, wPost(h.CreateForm, "/forms/new", url.Values{"pnid": {strconv.Itoa(p2)}}), "/forms/")
	wCleanupForm(t, h, id2, true)
	var to string
	var rtNull bool
	wScan(t, h, `SELECT test_order, record_types IS NULL, instrument_types IS NULL FROM form WHERE id=$1`, []any{id2}, &to, &rtNull, &itypesNull)
	if to != "" || !rtNull || !itypesNull || len(wInts(t, h, `SELECT id FROM form_row WHERE form_id=$1`, id2)) != 0 {
		t.Errorf("blank form = order %q types NULL %v/%v", to, rtNull, itypesNull)
	}

	asm, _ := repPart(t, h, "ASM")
	inactive, _ := repPart(t, h, "FORM")
	repExec(t, h, `UPDATE part SET is_active=FALSE WHERE id=$1`, inactive)
	for _, bad := range []string{"", "0", strconv.Itoa(asm), strconv.Itoa(inactive)} {
		assertStatus(t, "CreateForm pnid "+bad, wPost(h.CreateForm, "/forms/new", url.Values{"pnid": {bad}}), http.StatusBadRequest)
	}

	// A missing source fails the copy and rolls the new form back.
	p3, _ := repPart(t, h, "FORM")
	t.Cleanup(func() { smokeExec(context.Background(), h, `DELETE FROM form WHERE part_number_id=$1`, p3) })
	assertStatus(t, "CreateForm missing source", wPost(h.CreateForm, "/forms/new",
		url.Values{"pnid": {strconv.Itoa(p3)}, "source_id": {"2147483000"}}), http.StatusInternalServerError)
	var n int
	wScan(t, h, `SELECT COUNT(*) FROM form WHERE part_number_id=$1`, []any{p3}, &n)
	if n != 0 {
		t.Errorf("forms for p3 = %d, want 0 (rolled back)", n)
	}
}

func TestIntegration_RecordsWrites_CreateDuplicate(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	src, order := wSourceForm(t, h)

	p1, _ := repPart(t, h, "FORM")
	id := wNewID(t, wPost(h.CreateDuplicate, "/forms/x/duplicate", url.Values{"pnid": {strconv.Itoa(p1)}}, src.formID), "/forms/")
	wCleanupForm(t, h, id, true)
	var rtypes string
	var itypesNull bool
	wScan(t, h, `SELECT record_types, instrument_types IS NULL FROM form WHERE id=$1`, []any{id}, &rtypes, &itypesNull)
	if rtypes != "New Release,Re-Test" || !itypesNull {
		t.Errorf("duplicate types %q instr NULL %v", rtypes, itypesNull)
	}
	wCheckCopied(t, h, id, order)

	asm, _ := repPart(t, h, "ASM")
	assertStatus(t, "CreateDuplicate bad pn", wPost(h.CreateDuplicate, "/forms/x/duplicate", url.Values{"pnid": {strconv.Itoa(asm)}}, src.formID), http.StatusBadRequest)
	p2, _ := repPart(t, h, "FORM")
	t.Cleanup(func() { smokeExec(context.Background(), h, `DELETE FROM form WHERE part_number_id=$1`, p2) })
	assertStatus(t, "CreateDuplicate missing source", wPost(h.CreateDuplicate, "/forms/x/duplicate",
		url.Values{"pnid": {strconv.Itoa(p2)}}, 2147483000), http.StatusInternalServerError)
	var n int
	wScan(t, h, `SELECT COUNT(*) FROM form WHERE part_number_id=$1`, []any{p2}, &n)
	if n != 0 {
		t.Errorf("forms for p2 = %d, want 0 (rolled back)", n)
	}
}

// ── SaveFormDef ─────────────────────────────────────────────────────────────

func TestIntegration_RecordsWrites_SaveFormDef(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	wCleanupForm(t, h, f.formID, false)

	s2 := strconv.Itoa(f.s2)
	vals := url.Values{
		"original_parameter_" + s2: {"x"}, "parameter_" + s2: {"Output Voltage 2"}, "type_" + s2: {"0"},
		"specification_" + s2: {"4 to 6"}, "spec_min_" + s2: {"4"}, "spec_max_" + s2: {"6"}, "spec_units_" + s2: {"V"},
		"spec_nom_" + s2:   {""},
		"new_row[a][type]": {"1"}, "new_row[a][parameter]": {""},
		"new_row[b][type]": {"0"}, "new_row[b][parameter]": {"  "},
		"new_row[c][type]": {"0"}, "new_row[c][parameter]": {"P"}, "new_row[c][spec_max]": {"9"},
		"step_order":   {fmt.Sprintf("%d,new_a,new_b,%d,%d", f.s3, f.s1, f.s2)},
		"record_types": {"A,B"}, "original_record_types": {"New Release,Re-Test"},
		"instrument_types": {""}, "original_instrument_types": {""},
	}
	assertStatus(t, "SaveFormDef", wPost(h.SaveFormDef, "/forms/x/def/edit", vals, f.formID), http.StatusSeeOther)

	var param, spec, min string
	var nomNull, commentNull bool
	wScan(t, h, `SELECT parameter, specification, spec_min, spec_nom IS NULL, comment IS NULL FROM form_row WHERE id=$1`, []any{f.s2},
		&param, &spec, &min, &nomNull, &commentNull)
	if param != "Output Voltage 2" || spec != "4 to 6" || min != "4" || !nomNull || !commentNull {
		t.Errorf("s2 = %q %q %q nom NULL %v comment NULL %v", param, spec, min, nomNull, commentNull)
	}
	var by, before string
	wScan(t, h, `SELECT changed_by, parameter FROM form_row_history WHERE form_row_id=$1`, []any{f.s2}, &by, &before)
	if by != "admin" || before != "Output Voltage" {
		t.Errorf("history = by %q pre-change %q", by, before)
	}

	var heading, pRow int
	var hParam string
	wScan(t, h, `SELECT id, parameter FROM form_row WHERE form_id=$1 AND type=1 AND id NOT IN ($2)`, []any{f.formID, f.s1}, &heading, &hParam)
	var pSpecNull, pMinNull bool
	var pMax string
	wScan(t, h, `SELECT id, specification IS NULL, spec_min IS NULL, spec_max FROM form_row WHERE form_id=$1 AND parameter='P'`,
		[]any{f.formID}, &pRow, &pSpecNull, &pMinNull, &pMax)
	if hParam != "" || !pSpecNull || !pMinNull || pMax != "9" {
		t.Errorf("new rows: heading param %q, P spec NULL %v min NULL %v max %q", hParam, pSpecNull, pMinNull, pMax)
	}
	var n int
	wScan(t, h, `SELECT COUNT(*) FROM form_row WHERE form_id=$1`, []any{f.formID}, &n)
	if n != 5 {
		t.Errorf("steps = %d, want 5 (blank data row skipped)", n)
	}
	var to, rtypes string
	var itNull bool
	wScan(t, h, `SELECT test_order, record_types, instrument_types IS NULL FROM form WHERE id=$1`, []any{f.formID}, &to, &rtypes, &itNull)
	if want := wOrder(f.s3, heading, f.s1, f.s2, pRow); to != want || rtypes != "A,B" || !itNull {
		t.Errorf("form = order %q (want %q) types %q instr NULL %v", to, want, rtypes, itNull)
	}
}

// ── Named-query CRUD (#250) ─────────────────────────────────────────────────

func TestIntegration_RecordsWrites_NamedQueryCRUD(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	name := smokeUniq("ZZNQ-A")
	save := func(vals url.Values) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h.SettingsNamedQueryRowSave(rec, adminCtx(postForm("/settings/named-queries/row", vals)))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return rec.Code, out
	}

	code, out := save(url.Values{"name": {" " + name + " "}, "sql": {"SELECT 1"}, "result_type": {"bogus"}, "active": {"1"}})
	id := int(out["id"].(float64))
	t.Cleanup(func() { smokeExec(context.Background(), h, `DELETE FROM named_queries WHERE id=$1`, id) })
	var desc, params, rt string
	var active bool
	wScan(t, h, `SELECT description, params, result_type, is_active FROM named_queries WHERE id=$1`, []any{id}, &desc, &params, &rt, &active)
	if code != http.StatusOK || desc != "" || params != "" || rt != "list" || !active {
		t.Errorf("insert = %d desc %q params %q type %q active %v", code, desc, params, rt, active)
	}

	code, _ = save(url.Values{"id": {strconv.Itoa(id)}, "name": {name}, "sql": {"SELECT 2"}, "result_type": {"single"}, "description": {"d2"}})
	wScan(t, h, `SELECT description, result_type, is_active FROM named_queries WHERE id=$1`, []any{id}, &desc, &rt, &active)
	if code != http.StatusOK || desc != "d2" || rt != "single" || active {
		t.Errorf("update = %d desc %q type %q active %v", code, desc, rt, active)
	}
	if code, out = save(url.Values{"id": {"2147483000"}, "name": {name + "x"}, "sql": {"SELECT 1"}}); code != http.StatusNotFound {
		t.Errorf("update missing = %d %v", code, out)
	}
	if code, out = save(url.Values{"name": {name}, "sql": {"SELECT 1"}}); code != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(out["error"]), "already exists") {
		t.Errorf("duplicate name = %d %v", code, out)
	}
	if code, _ = save(url.Values{"name": {name + "y"}, "sql": {"DELETE FROM part"}}); code != http.StatusBadRequest {
		t.Errorf("unsafe sql = %d", code)
	}

	nameB := smokeUniq("ZZNQ-B")
	repInsert(t, h, `INSERT INTO named_queries (name, sql, description, params) VALUES ($1,'SELECT 3',NULL,NULL) RETURNING id`,
		[]any{nameB}, `DELETE FROM named_queries WHERE id=$1`)
	full, err := h.loadNamedQueriesFull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var gotA, gotB *NamedQueryRow
	for i := range full {
		switch full[i].Name {
		case name:
			gotA = &full[i]
		case nameB:
			gotB = &full[i]
		}
	}
	if gotA == nil || gotA.Active || gotA.SQL != "SELECT 2" || gotA.Updated == "" || gotB == nil || gotB.Description != "" || gotB.Params != "" || !gotB.Active {
		t.Errorf("full list A %+v B %+v", gotA, gotB)
	}
	list, err := h.listNamedQueries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sawA, sawB bool
	for _, q := range list {
		sawA = sawA || q.Name == name
		sawB = sawB || q.Name == nameB
	}
	if sawA || !sawB {
		t.Errorf("active list: inactive A present %v, B present %v", sawA, sawB)
	}
}

// ── Transaction / race fixes (review of #249) ───────────────────────────────

// A record completed while a resync is running wins: the resync writes nothing.
func TestIntegration_RecordsWrites_ResyncLockRaceWritesNothing(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	f := recForm(t, h)
	r := recRecord(t, h, f, nil, "81", "2026-07-01 10:00", "New Release", false, true, 0)
	repExec(t, h, `UPDATE form_record SET test_order=$1 WHERE id=$2`, wOrder(f.s2), r)
	repExec(t, h, `INSERT INTO result (form_record_id, form_row_id, type, parameter) VALUES ($1,$2,0,'Old')`, r, f.s2)

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx, []string{`UPDATE form_record SET is_locked=TRUE WHERE id=$1`}, [][]any{{r}},
		func() { rec = wPost(h.ResyncRecord, "/records/x/resync", nil, r) })

	assertStatus(t, "ResyncRecord", rec, http.StatusSeeOther)
	var param, to string
	var n int
	wScan(t, h, `SELECT parameter FROM result WHERE form_record_id=$1 AND form_row_id=$2`, []any{r, f.s2}, &param)
	wScan(t, h, `SELECT test_order, (SELECT COUNT(*) FROM result WHERE form_record_id=$1) FROM form_record WHERE id=$1`, []any{r}, &to, &n)
	if param != "Old" || to != wOrder(f.s2) || n != 1 {
		t.Errorf("locked mid-resync: parameter %q, test_order %q, rows %d (want Old, %q, 1)", param, to, n, wOrder(f.s2))
	}
}

// A reviewer approving while a non-reviewer's unlock is running wins: the approved record stays locked.
func TestIntegration_RecordsWrites_UnlockApproveRaceKeepsApproval(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	f := recForm(t, h)
	r := recRecord(t, h, f, nil, "82", "2026-07-01 10:00", "New Release", true, true, 0)

	raceHandler(t, h, ctx, []string{`UPDATE form_record SET is_approved=TRUE WHERE id=$1`}, [][]any{{r}}, func() {
		rec := httptest.NewRecorder()
		h.UnlockRecord(rec, nonReviewerCtx(withID(postForm("/records/x/unlock", url.Values{"comment": {"why"}}), r)))
	})

	var locked, approved bool
	var events int
	wScan(t, h, `SELECT is_locked, is_approved, (SELECT COUNT(*) FROM record_events WHERE form_record_id=$1 AND event_type='unlocked')
		FROM form_record WHERE id=$1`, []any{r}, &locked, &approved, &events)
	if !locked || !approved || events != 0 {
		t.Errorf("after racing unlock: locked %v approved %v unlocked events %d (want true true 0)", locked, approved, events)
	}
}

// A failed audit-event insert rolls back the state change it records. A username longer than the events'
// VARCHAR(255) makes the insert fail.
func TestIntegration_RecordsWrites_LifecycleEventFailureRollsBack(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	wCleanupForm(t, h, f.formID, false)
	longUser := func(req *http.Request) *http.Request {
		return req.WithContext(context.WithValue(req.Context(), ctxUserKey,
			&User{ID: 8001, Username: strings.Repeat("u", 300), IsAdmin: true, CanApproveRecords: true}))
	}
	run := func(fn http.HandlerFunc, target string, vals url.Values, id int) {
		t.Helper()
		rec := httptest.NewRecorder()
		fn(rec, longUser(withID(postForm(target, vals), id)))
		assertStatus(t, target, rec, http.StatusInternalServerError)
	}
	state := func(q string, id int) string {
		t.Helper()
		var s string
		wScan(t, h, q, []any{id}, &s)
		return s
	}
	const recState = `SELECT is_locked::text || ',' || is_approved::text FROM form_record WHERE id=$1`
	const formState = `SELECT is_locked::text || ',' || revision::text FROM form WHERE id=$1`

	complete := recRecord(t, h, f, nil, "83", "2026-07-01 10:00", "New Release", true, true, 0)
	run(h.ApproveRecord, "/records/x/approve", nil, complete)
	if got := state(recState, complete); got != "true,false" {
		t.Errorf("approve with failed event: record %s, want still complete (true,false)", got)
	}
	run(h.UnlockRecord, "/records/x/unlock", url.Values{"comment": {"why"}}, complete)
	if got := state(recState, complete); got != "true,false" {
		t.Errorf("unlock with failed event: record %s, want still locked (true,false)", got)
	}

	run(h.LockForm, "/forms/x/lock", nil, f.formID)
	if got := state(formState, f.formID); got != "false,2" {
		t.Errorf("lock form with failed event: form %s, want unchanged (false,2)", got)
	}
	repExec(t, h, `UPDATE form SET is_locked=TRUE WHERE id=$1`, f.formID)
	run(h.UnlockForm, "/forms/x/unlock", url.Values{"comment": {"why"}}, f.formID)
	if got := state(formState, f.formID); got != "true,2" {
		t.Errorf("unlock form with failed event: form %s, want still locked (true,2)", got)
	}
}

// A form-definition save that fails on the form-level update keeps none of its step changes.
func TestIntegration_RecordsWrites_SaveFormDefIsAtomic(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	wCleanupForm(t, h, f.formID, false)
	s2 := strconv.Itoa(f.s2)
	rec := wPost(h.SaveFormDef, "/forms/x/def/edit", url.Values{
		"original_parameter_" + s2: {"Output Voltage"}, "parameter_" + s2: {"Changed"},
		"new_row[a][type]": {"0"}, "new_row[a][parameter]": {"Added"},
		// record_types is VARCHAR(500): the form-level update fails after the step writes.
		"record_types": {strings.Repeat("x", 600)}, "original_record_types": {"New Release,Re-Test"},
	}, f.formID)
	assertStatus(t, "SaveFormDef", rec, http.StatusInternalServerError)
	var param string
	var steps, history int
	wScan(t, h, `SELECT parameter, (SELECT COUNT(*) FROM form_row WHERE form_id=$2),
		(SELECT COUNT(*) FROM form_row_history WHERE form_row_id=$1) FROM form_row WHERE id=$1`, []any{f.s2, f.formID},
		&param, &steps, &history)
	if param != "Output Voltage" || steps != 3 || history != 0 {
		t.Errorf("after failed save: s2 %q, steps %d, history rows %d (want unchanged, 3, 0)", param, steps, history)
	}
}
