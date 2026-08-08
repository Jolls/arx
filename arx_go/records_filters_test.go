package main

import (
	"net/url"
	"testing"
	"time"

	arxdb "arx/arxlib/db"
)

func TestParseRecordFilters_DefaultsToWIP(t *testing.T) {
	for _, in := range []string{"", "status=", "status=bogus"} {
		q, _ := url.ParseQuery(in)
		if got := parseRecordFilters(q).Status; got != "wip" {
			t.Errorf("parseRecordFilters(%q).Status = %q, want \"wip\"", in, got)
		}
	}
}

func TestParseRecordFilters_KeepsValidStatus(t *testing.T) {
	for _, s := range []string{"wip", "complete", "approved", "all"} {
		q := url.Values{"status": {s}}
		if got := parseRecordFilters(q).Status; got != s {
			t.Errorf("status %q parsed to %q", s, got)
		}
	}
}

func TestParseRecordFilters_DropsUnparseableDates(t *testing.T) {
	q := url.Values{"from": {"not-a-date"}, "to": {"2026-13-99"}}
	f := parseRecordFilters(q)
	if !f.From.IsZero() || f.FromStr != "" {
		t.Errorf("bad from kept: From=%v FromStr=%q", f.From, f.FromStr)
	}
	if !f.To.IsZero() || f.ToStr != "" {
		t.Errorf("bad to kept: To=%v ToStr=%q", f.To, f.ToStr)
	}
}

func TestParseRecordFilters_ParsesGoodDates(t *testing.T) {
	q := url.Values{"from": {"2026-01-02"}, "to": {"2026-03-04"}}
	f := parseRecordFilters(q)
	if f.From.IsZero() || f.FromStr != "2026-01-02" {
		t.Errorf("from not parsed: %v / %q", f.From, f.FromStr)
	}
	if f.To.IsZero() || f.ToStr != "2026-03-04" {
		t.Errorf("to not parsed: %v / %q", f.To, f.ToStr)
	}
}

func TestWhereClauses_WIPNoExtras(t *testing.T) {
	f := parseRecordFilters(url.Values{}) // status=wip, nothing else
	sql, args := f.whereClauses(arxdb.NewSQLServerDialect(), 2)
	if sql != " AND is_locked = 0" {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
}

func TestWhereClauses_StatusVariants(t *testing.T) {
	cases := map[string]string{
		"complete": " AND is_locked = 1 AND is_approved = 0",
		"approved": " AND is_approved = 1",
		"all":      "",
	}
	for status, want := range cases {
		f := parseRecordFilters(url.Values{"status": {status}})
		sql, _ := f.whereClauses(arxdb.NewSQLServerDialect(), 2)
		if sql != want {
			t.Errorf("status %q: sql = %q, want %q", status, sql, want)
		}
	}
}

func TestWhereClauses_AllFiltersNumberedFromStart(t *testing.T) {
	q := url.Values{
		"status": {"all"},
		"type":   {"Re-Test"},
		"from":   {"2026-01-02"},
		"to":     {"2026-03-04"},
	}
	f := parseRecordFilters(q)
	sql, args := f.whereClauses(arxdb.NewSQLServerDialect(), 2)
	want := " AND record_type = @p2 AND record_date >= @p3 AND record_date < DATEADD(day, 1, @p4)"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Fatalf("args len = %d, want 3", len(args))
	}
	if args[0] != "Re-Test" {
		t.Errorf("args[0] = %v, want Re-Test", args[0])
	}
	if _, ok := args[1].(time.Time); !ok {
		t.Errorf("args[1] = %T, want time.Time", args[1])
	}
}

func TestStatusWIP(t *testing.T) {
	if !parseRecordFilters(url.Values{}).StatusWIP() {
		t.Error("default should report StatusWIP() true")
	}
	if parseRecordFilters(url.Values{"status": {"all"}}).StatusWIP() {
		t.Error("status=all should report StatusWIP() false")
	}
}
