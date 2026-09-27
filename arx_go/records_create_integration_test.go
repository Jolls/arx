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
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT serial_number FROM %s WHERE id=$1`,
		h.cfg().RecordsTable()), id).Scan(&sn); err != nil {
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

	rows, err := h.queryContext(ctx, fmt.Sprintf(`SELECT serial_number FROM %s WHERE form_id=$1`,
		h.cfg().RecordsTable()), formID)
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
