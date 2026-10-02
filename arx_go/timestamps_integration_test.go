//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// auditTimestampColumns is every audit/event column #192 moves to a
// DB-assigned timestamptz, with whether it is NOT NULL and whether it carries a
// now() default (part.last_rollup_at is NULL until a rollup runs).
var auditTimestampColumns = []struct {
	table, column string
	notNull       bool
	hasDefault    bool
}{
	{"app_config", "updated_at", true, true},
	{"build", "created_at", true, true},
	{"company", "date_modified", false, true},
	{"contact", "updated_at", false, true},
	{"form_events", "event_date", true, true},
	{"form_record", "created_at", false, true},
	{"form_record", "updated_at", false, true},
	{"form_row", "created_at", false, true},
	{"form_row", "updated_at", false, true},
	{"form_row_history", "changed_at", true, true},
	{"inventory_transaction", "created_at", true, true},
	{"lot", "created_at", true, true},
	{"named_queries", "created_at", false, true},
	{"named_queries", "updated_at", false, true},
	{"part", "last_rollup_at", false, false},
	{"purchase_order", "date_modified", false, true},
	{"purchase_order_history", "changed_at", true, true},
	{"record_events", "event_date", true, true},
	{"result", "updated_at", false, true},
	{"schema_migrations", "tstamp", false, true},
	{"unit", "created_at", true, true},
	{"users", "created_at", true, true},
	{"users", "updated_at", true, true},
}

// columnInfo returns a column's data type, default expression ("" if none) and is_nullable.
func columnInfo(t *testing.T, h *Handler, table, column string) (dataType, def, nullable string) {
	t.Helper()
	if err := h.queryRowContext(context.Background(),
		`SELECT data_type, COALESCE(column_default,''), is_nullable FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name=$1 AND column_name=$2`, table, column,
	).Scan(&dataType, &def, &nullable); err != nil {
		t.Fatalf("%s.%s: %v", table, column, err)
	}
	return
}

// TestIntegration_AuditColumnsAreTimestamptz pins the #192 column types; the
// user-typed form_record.record_date stays a zoneless wall-clock TIMESTAMP.
func TestIntegration_AuditColumnsAreTimestamptz(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	for _, c := range auditTimestampColumns {
		dataType, def, nullable := columnInfo(t, h, c.table, c.column)
		if dataType != "timestamp with time zone" {
			t.Errorf("%s.%s type = %q, want timestamp with time zone", c.table, c.column, dataType)
		}
		if got := def != ""; got != c.hasDefault || (c.hasDefault && def != "now()") {
			t.Errorf("%s.%s default = %q, want now()=%v", c.table, c.column, def, c.hasDefault)
		}
		if wantNullable := map[bool]string{true: "NO", false: "YES"}[c.notNull]; nullable != wantNullable {
			t.Errorf("%s.%s is_nullable = %s, want %s", c.table, c.column, nullable, wantNullable)
		}
	}
	if dataType, _, _ := columnInfo(t, h, "form_record", "record_date"); dataType != "timestamp without time zone" {
		t.Errorf("form_record.record_date type = %q, want timestamp without time zone", dataType)
	}
}

// TestIntegration_RecordDateStaysWallClock pins that the user-typed record_date
// round-trips as its wall-clock value (seed record 7001).
func TestIntegration_RecordDateStaysWallClock(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	var got time.Time
	if err := h.queryRowContext(context.Background(),
		`SELECT record_date FROM form_record WHERE id=$1`, 7001).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if s := got.Format("2006-01-02 15:04"); s != "2026-06-01 00:00" {
		t.Errorf("record_date = %s, want 2026-06-01 00:00", s)
	}
}

// TestIntegration_AuditTimestampsAreDBAssigned: audit timestamps come from the
// DB's now() (fixed at transaction start), not the desktop clock (#192). Each
// case rolls back, so nothing is written.
func TestIntegration_AuditTimestampsAreDBAssigned(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	req := userCtxTZ(httptest.NewRequest(http.MethodPost, "/", nil), "America/Los_Angeles")

	inTx := func(name string, fn func(tx *txLogger) []time.Time) {
		t.Helper()
		tx, err := h.beginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var dbNow time.Time
		if err := tx.QueryRowContext(ctx, `SELECT now()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		for i, got := range fn(tx) {
			if !got.Equal(dbNow) {
				t.Errorf("%s[%d]: timestamp %v, want DB now() %v", name, i, got, dbNow)
			}
		}
	}

	inTx("lot.created_at", func(tx *txLogger) []time.Time {
		id, err := h.createLot(ctx, tx, 3007, lotCreateArgs{Description: "itest-192"}, nil)
		if err != nil {
			t.Fatalf("createLot: %v", err)
		}
		var at time.Time
		if err := tx.QueryRowContext(ctx, `SELECT created_at FROM lot WHERE id=$1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return []time.Time{at}
	})

	inTx("inventory_transaction.created_at", func(tx *txLogger) []time.Time {
		if err := h.recordInventoryTxn(req, tx, 3007, "adjustment", 1, time.Now(), "itest-192", "", nil, nil, nil); err != nil {
			t.Fatalf("recordInventoryTxn: %v", err)
		}
		var at time.Time
		if err := tx.QueryRowContext(ctx,
			`SELECT created_at FROM inventory_transaction WHERE part_id=$1 ORDER BY id DESC LIMIT 1`, 3007).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return []time.Time{at}
	})

	inTx("purchase_order_history.changed_at / purchase_order.date_modified", func(tx *txLogger) []time.Time {
		if err := h.recordPOStatusChange(req, tx, 5002, "open", "sent"); err != nil {
			t.Fatalf("recordPOStatusChange: %v", err)
		}
		var changed, modified time.Time
		if err := tx.QueryRowContext(ctx,
			`SELECT changed_at FROM purchase_order_history WHERE po_id=$1 ORDER BY id DESC LIMIT 1`, 5002).Scan(&changed); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT date_modified FROM purchase_order WHERE ID=$1`, 5002).Scan(&modified); err != nil {
			t.Fatal(err)
		}
		return []time.Time{changed, modified}
	})
}

// TestIntegration_DateOnlyColumnsHaveNoDefault: the date-only business columns have no
// DEFAULT CURRENT_DATE, which would record the DB-zone day instead of the user's local
// day; every writer supplies the date explicitly (#279, #265).
func TestIntegration_DateOnlyColumnsHaveNoDefault(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	for _, c := range []struct{ table, column string }{
		{"build", "build_date"}, {"inventory_transaction", "txn_date"},
		{"part", "created_date"}, {"part", "modified_date"}, {"price", "effective_date"},
	} {
		if _, def, _ := columnInfo(t, h, c.table, c.column); def != "" {
			t.Errorf("%s.%s default = %q, want none", c.table, c.column, def)
		}
	}
}

// TestIntegration_TestReportRowsResultDateUsesUserZone: the report's resultDate shows
// result.updated_at (a timestamptz that pgx scans in the server's zone) in the viewing
// user's zone (#279). Report row ids are record ids; seeded records 7001-7003 (form 6001) have
// results pinned to 2020-01-01T00:00:00Z.
func TestIntegration_TestReportRowsResultDateUsesUserZone(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	for _, c := range []struct{ tz, want string }{
		{"UTC", "2020-01-01 00:00"},
		{"America/Los_Angeles", "2019-12-31 16:00"},
	} {
		req := userCtxTZ(withIDAndTestID(httptest.NewRequest(http.MethodGet, "/x", nil), 6001, 6102), c.tz)
		rec := httptest.NewRecorder()
		h.TestReportRows(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", c.tz, rec.Code, rec.Body.String())
		}
		var rows []struct {
			ID         int    `json:"id"`
			ResultDate string `json:"resultDate"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, r := range rows {
			if r.ID < 7001 || r.ID > 7003 {
				continue
			}
			seen++
			if r.ResultDate != c.want {
				t.Errorf("%s: result %d resultDate = %q, want %q", c.tz, r.ID, r.ResultDate, c.want)
			}
		}
		if seen == 0 {
			t.Errorf("%s: no seeded results in report rows: %s", c.tz, rec.Body.String())
		}
	}
}
