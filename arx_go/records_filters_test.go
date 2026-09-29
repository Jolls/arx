package main

import (
	"net/url"
	"testing"
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

func TestStatusWIP(t *testing.T) {
	if !parseRecordFilters(url.Values{}).StatusWIP() {
		t.Error("default should report StatusWIP() true")
	}
	if parseRecordFilters(url.Values{"status": {"all"}}).StatusWIP() {
		t.Error("status=all should report StatusWIP() false")
	}
}
