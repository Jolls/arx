package main

import (
	"math"
	"testing"
	"time"
)

func TestTimelinePositions(t *testing.T) {
	d0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return d0.AddDate(0, 0, n) }
	cases := []struct {
		name  string
		times []time.Time
		now   time.Time
		want  []float64
	}{
		{"empty", nil, day(5), nil},
		{"single dot sits at the start", []time.Time{day(0)}, day(100), []float64{5}},
		// 2 gaps of 10 days: 2% base each, the other 86% split evenly.
		{"gaps within 20 days split proportionally", []time.Time{day(0), day(10)}, day(20), []float64{5, 50}},
		// 1-day gaps keep their length, the 400-day gap to now is capped at 20: 6% base + 84% over 22 days.
		{"burst keeps the base gap, long gap capped", []time.Time{day(0), day(1), day(2)}, day(402),
			[]float64{5, 5 + 2 + 84.0/22, 5 + 4 + 84.0*2/22}},
		// A 200-day gap between dots counts the same as a 20-day one: 4% base + 86% over 21 days.
		{"long gap between dots capped", []time.Time{day(0), day(200)}, day(201), []float64{5, 5 + 2 + 86.0*20/21}},
		// 50 gaps × 2% base exceeds the bar, so the dots fall back to even spacing (90/50 = 1.8% apart).
		{"too many dots space evenly", manyDays(50), day(50), evenly(50)},
	}
	for _, c := range cases {
		got := timelinePositions(c.times, c.now)
		if len(got) != len(c.want) {
			t.Fatalf("%s: len = %d, want %d", c.name, len(got), len(c.want))
		}
		for i := range got {
			if math.Abs(got[i]-c.want[i]) > 1e-9 {
				t.Errorf("%s: pct[%d] = %v, want %v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func manyDays(n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)
	}
	return out
}

func evenly(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 5 + 90*float64(i)/float64(n)
	}
	return out
}
