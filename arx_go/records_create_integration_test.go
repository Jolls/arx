//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Covers CreateRecord's per-form serial allocation (#33): accepted suggestions are
// re-derived under a per-form advisory lock; a typed override is stored as-is.

func createRecord(h *Handler, formID int, sn, suggested string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.CreateRecord(rec, adminCtx(withID(postForm(fmt.Sprintf("/forms/%d/records/new", formID), url.Values{
		"serial_number":           {sn},
		"suggested_serial_number": {suggested},
		"record_type":             {"Production"},
	}), formID)))
	return rec
}

func createdSerial(t *testing.T, h *Handler, ctx context.Context, rec *httptest.ResponseRecorder) string {
	t.Helper()
	loc := strings.TrimSuffix(strings.TrimPrefix(rec.Header().Get("Location"), "/records/"), "/edit")
	id, err := strconv.Atoi(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", rec.Header().Get("Location"), err)
	}
	var sn string
	if err := h.queryRowContext(ctx, `SELECT serial_number FROM form_record WHERE id=$1`, id).Scan(&sn); err != nil {
		t.Fatalf("select serial: %v", err)
	}
	return sn
}

func TestIntegration_CreateRecord_AutoSerial(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	_, formID, _, formCleanup := seedLockTestForm(t, h, ctx)
	defer formCleanup()

	for _, want := range []string{"1", "2"} {
		rec := createRecord(h, formID, "1", "1")
		assertStatus(t, "CreateRecord auto", rec, http.StatusSeeOther)
		if got := createdSerial(t, h, ctx, rec); got != want {
			t.Fatalf("auto serial: got %q, want %q", got, want)
		}
	}

	rec := createRecord(h, formID, "ABC-7", "3")
	assertStatus(t, "CreateRecord override", rec, http.StatusSeeOther)
	if got := createdSerial(t, h, ctx, rec); got != "ABC-7" {
		t.Fatalf("override serial: got %q, want ABC-7", got)
	}
}

func TestIntegration_CreateRecord_ConcurrentDistinctSerials(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	_, formID, _, formCleanup := seedLockTestForm(t, h, ctx)
	defer formCleanup()

	const n = 10
	start := make(chan struct{})
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = createRecord(h, formID, "1", "1").Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusSeeOther {
			t.Fatalf("create %d: status %d, want 303", i, c)
		}
	}

	rows, err := h.queryContext(ctx, `SELECT serial_number FROM form_record WHERE form_id=$1`, formID)
	if err != nil {
		t.Fatalf("select serials: %v", err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var sn string
		if err := rows.Scan(&sn); err != nil {
			t.Fatalf("scan: %v", err)
		}
		v, _ := strconv.Atoi(sn)
		got = append(got, v)
	}
	sort.Ints(got)
	if len(got) != n {
		t.Fatalf("got %d records, want %d: %v", len(got), n, got)
	}
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("serials not exactly 1..%d: %v", n, got)
		}
	}
}

// #450: a form's serial prefix/padding shape the suggestion; an accepted default also skips past the
// chosen UUT's existing unit serials; a typed SN outside the pattern is stored as-is. #451: the units
// endpoint lists the UUT's unit serials.
func TestIntegration_CreateRecord_SerialFormatAndUnitSkip(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	partID, formID, _, formCleanup := seedLockTestForm(t, h, ctx)
	defer formCleanup()
	if _, err := h.execContext(ctx, `UPDATE form SET serial_prefix='SK-', serial_pad=4 WHERE id=$1`, formID); err != nil {
		t.Fatalf("set serial format: %v", err)
	}
	if _, err := h.execContext(ctx, `INSERT INTO unit (part_id, serial_number, source) VALUES ($1, 'SK-0003', 'manual')`, partID); err != nil {
		t.Fatalf("seed unit: %v", err)
	}
	defer h.execContext(ctx, `DELETE FROM unit WHERE part_id=$1`, partID)

	for _, want := range []string{"SK-0001", "SK-0002"} {
		rec := createRecord(h, formID, "SK-0001", "SK-0001")
		assertStatus(t, "CreateRecord auto", rec, http.StatusSeeOther)
		if got := createdSerial(t, h, ctx, rec); got != want {
			t.Fatalf("auto serial: got %q, want %q", got, want)
		}
	}

	// With the UUT chosen, the accepted default skips unit SK-0003 (the form's own max is SK-0002).
	rec := httptest.NewRecorder()
	h.CreateRecord(rec, adminCtx(withID(postForm(fmt.Sprintf("/forms/%d/records/new", formID), url.Values{
		"serial_number": {"SK-0003"}, "suggested_serial_number": {"SK-0003"},
		"bom_pnid": {strconv.Itoa(partID)}, "record_type": {"Production"},
	}), formID)))
	assertStatus(t, "CreateRecord auto with UUT", rec, http.StatusSeeOther)
	if got := createdSerial(t, h, ctx, rec); got != "SK-0004" {
		t.Fatalf("auto serial with UUT: got %q, want SK-0004", got)
	}

	rec = createRecord(h, formID, "ABC-7", "SK-0005")
	assertStatus(t, "CreateRecord off-pattern override", rec, http.StatusSeeOther)
	if got := createdSerial(t, h, ctx, rec); got != "ABC-7" {
		t.Fatalf("override serial: got %q, want ABC-7", got)
	}

	// A digit run too long for the INTEGER cast is ignored by the sequence, not an error.
	rec = createRecord(h, formID, "SK-12345678901", "SK-0005")
	assertStatus(t, "CreateRecord overlong serial", rec, http.StatusSeeOther)

	rec = httptest.NewRecorder()
	h.NewRecordUnits(rec, adminCtx(withID(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/forms/%d/records/new/units?bom_pnid=%d", formID, partID), nil), formID)))
	assertStatus(t, "NewRecordUnits", rec, http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"next":"SK-0005","serials":["SK-0003"]}` {
		t.Fatalf("units = %s, want next SK-0005 (form records SK-0001, SK-0002, SK-0004) with serials [SK-0003]", got)
	}
}
