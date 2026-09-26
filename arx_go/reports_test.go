package main

import (
	"net/url"
	"testing"
	"time"
)

func TestResolveSpendDateRange(t *testing.T) {
	now := time.Date(2026, 7, 10, 15, 4, 5, 0, time.UTC)

	tests := []struct {
		name        string
		query       url.Values
		wantPreset  string
		wantFromStr string
		wantToStr   string
	}{
		{
			name:        "this month",
			query:       url.Values{"range": {"this_month"}},
			wantPreset:  "this_month",
			wantFromStr: "2026-07-01",
			wantToStr:   "2026-07-31",
		},
		{
			name:        "this quarter Q3",
			query:       url.Values{"range": {"this_quarter"}},
			wantPreset:  "this_quarter",
			wantFromStr: "2026-07-01",
			wantToStr:   "2026-09-30",
		},
		{
			name:        "ytd",
			query:       url.Values{"range": {"ytd"}},
			wantPreset:  "ytd",
			wantFromStr: "2026-01-01",
			wantToStr:   "2026-07-10",
		},
		{
			name:        "custom valid",
			query:       url.Values{"range": {"custom"}, "from": {"2026-02-01"}, "to": {"2026-02-28"}},
			wantPreset:  "custom",
			wantFromStr: "2026-02-01",
			wantToStr:   "2026-02-28",
		},
		{
			name:        "custom malformed dates dropped",
			query:       url.Values{"range": {"custom"}, "from": {"not-a-date"}, "to": {"also-bad"}},
			wantPreset:  "custom",
			wantFromStr: "",
			wantToStr:   "",
		},
		{
			name:        "missing range defaults to this_month",
			query:       url.Values{},
			wantPreset:  "this_month",
			wantFromStr: "2026-07-01",
			wantToStr:   "2026-07-31",
		},
		{
			name:        "unrecognized range defaults to this_month",
			query:       url.Values{"range": {"bogus"}},
			wantPreset:  "this_month",
			wantFromStr: "2026-07-01",
			wantToStr:   "2026-07-31",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSpendDateRange(tt.query, now)
			if got.Preset != tt.wantPreset {
				t.Errorf("Preset = %q, want %q", got.Preset, tt.wantPreset)
			}
			if got.FromStr != tt.wantFromStr {
				t.Errorf("FromStr = %q, want %q", got.FromStr, tt.wantFromStr)
			}
			if got.ToStr != tt.wantToStr {
				t.Errorf("ToStr = %q, want %q", got.ToStr, tt.wantToStr)
			}
		})
	}
}

func TestResolveSpendDateRange_ThisQuarterBoundary(t *testing.T) {
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	got := resolveSpendDateRange(url.Values{"range": {"this_quarter"}}, now)
	if got.FromStr != "2026-01-01" || got.ToStr != "2026-03-31" {
		t.Errorf("got From=%q To=%q, want From=2026-01-01 To=2026-03-31", got.FromStr, got.ToStr)
	}
}

// TestResolveSpendDateRange_CustomUsesNowLocation: custom from/to dates are
// midnight in the user's zone (now's location), since the cycle-time report
// compares them against timestamptz changed_at (#192).
func TestResolveSpendDateRange_CustomUsesNowLocation(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	now := time.Date(2026, 7, 10, 15, 0, 0, 0, la)
	q, _ := url.ParseQuery("range=custom&from=2026-02-01&to=2026-02-28")
	got := resolveSpendDateRange(q, now)
	if want := time.Date(2026, 2, 1, 0, 0, 0, 0, la); !got.From.Equal(want) {
		t.Errorf("From = %v, want %v", got.From, want)
	}
}
