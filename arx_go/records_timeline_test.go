package main

import (
	"math"
	"testing"
	"time"

	"arx/internal/records"
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

func TestBuildTimeline(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	at := func(day, hour int) time.Time { return time.Date(2026, 5, day, hour, 0, 0, 0, loc) }
	midnight := func(day int) time.Time { return at(day, 0) }
	edit := func(ts time.Time, step int) records.TimelineRow { return records.TimelineRow{At: ts, FormRowID: step} }
	lock := func(ts time.Time, user string) records.TimelineRow {
		return records.TimelineRow{At: ts, Username: user, IsLock: true}
	}
	cases := []struct {
		name string
		rows []records.TimelineRow
		rev  int
		want []historyPoint
	}{
		{"no rows", nil, 0, nil},
		{"edits only, never released: distinct steps per day", []records.TimelineRow{
			edit(at(1, 9), 1), edit(at(1, 10), 1), edit(at(1, 11), 2), edit(at(2, 9), 1)}, 0,
			[]historyPoint{
				{At: midnight(1), Query: "at=2026-05-01", Title: "2026-05-01 — 2 changes — Draft"},
				{At: midnight(2), Query: "at=2026-05-02", Title: "2026-05-02 — 1 change — Draft"}}},
		{"a lock day yields its edit dot to the lock marker", []records.TimelineRow{
			edit(at(1, 10), 1), edit(at(2, 9), 2), lock(at(2, 11), "admin"), edit(at(3, 9), 1)}, 1,
			[]historyPoint{
				{At: midnight(1), Title: "2026-05-01 — 1 change — Draft"},
				{At: at(2, 11), IsLock: true, Title: "Released Rev 1 — admin — 2026-05-02 11:00"},
				{At: midnight(3), Title: "2026-05-03 — 1 change — Rev 1 → draft of Rev 2"}}},
		{"revisions count back past releases missing from form_events", []records.TimelineRow{
			edit(at(1, 9), 1), lock(at(2, 10), "bob"), edit(at(3, 9), 1)}, 3,
			[]historyPoint{
				{At: midnight(1), Title: "2026-05-01 — 1 change — Rev 2 → draft of Rev 3"},
				{At: at(2, 10), IsLock: true, Title: "Released Rev 3 — bob — 2026-05-02 10:00"},
				{At: midnight(3), Title: "2026-05-03 — 1 change — Rev 3 → draft of Rev 4"}}},
		{"two locks on one day both show, the second since the first", []records.TimelineRow{
			lock(at(2, 9), "admin"), lock(at(2, 15), "admin")}, 2,
			[]historyPoint{
				{At: at(2, 9), IsLock: true, Query: "at=2026-05-02T16%3A00%3A00Z&since=",
					Title: "Released Rev 1 — admin — 2026-05-02 09:00"},
				{At: at(2, 15), IsLock: true, Query: "at=2026-05-02T22%3A00%3A00Z&since=2026-05-02T16%3A00%3A00Z",
					Title: "Released Rev 2 — admin — 2026-05-02 15:00"}}},
		{"unlock rows are audit-log only, not timeline points (#487)", []records.TimelineRow{
			{At: at(2, 9), Username: "admin", IsLock: true, EventID: 1, EventType: "locked"},
			{At: at(3, 9), Username: "bob", EventID: 2, EventType: "unlocked", Comments: "fix"},
			edit(at(4, 9), 1)}, 1,
			[]historyPoint{
				{At: at(2, 9), IsLock: true, Title: "Released Rev 1 — admin — 2026-05-02 09:00"},
				{At: midnight(4), Title: "2026-05-04 — 1 change — Rev 1 → draft of Rev 2"}}},
		{"lock day is the viewer's local day, not the UTC day", []records.TimelineRow{
			edit(time.Date(2026, 5, 28, 23, 30, 0, 0, time.UTC), 1),
			lock(time.Date(2026, 5, 29, 1, 0, 0, 0, time.UTC), "admin")}, 1,
			[]historyPoint{
				{At: time.Date(2026, 5, 29, 1, 0, 0, 0, time.UTC), IsLock: true, Title: "Released Rev 1 — admin — 2026-05-28 18:00"}}},
	}
	for _, c := range cases {
		got := buildTimeline(c.rows, loc, c.rev)
		if len(got) != len(c.want) {
			t.Errorf("%s: len = %d, want %d (%+v)", c.name, len(got), len(c.want), got)
			continue
		}
		for i, w := range c.want {
			g := got[i]
			// Query is checked only where the case spells it out.
			if !g.At.Equal(w.At) || g.IsLock != w.IsLock || (w.Query != "" && g.Query != w.Query) || g.Title != w.Title {
				t.Errorf("%s: point %d = {%v %v %q %q}, want {%v %v %q %q}", c.name, i,
					g.At, g.IsLock, g.Query, g.Title, w.At, w.IsLock, w.Query, w.Title)
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
