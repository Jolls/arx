package main

import (
	"math"
	"testing"
	"time"
)

func TestComputeYieldBuckets(t *testing.T) {
	d := func(s string) *time.Time {
		tm, _ := time.Parse("2006-01-02", s)
		return &tm
	}

	records := []yieldRecord{
		{RecordDate: d("2026-06-01"), AnyFail: false},
		{RecordDate: d("2026-06-15"), AnyFail: true},
		{RecordDate: d("2026-07-01"), AnyFail: false},
		{RecordDate: d("2026-07-02"), AnyFail: false},
		{RecordDate: d("2026-07-03"), AnyFail: true},
		{RecordDate: nil, AnyFail: false},
	}

	total, monthly := computeYieldBuckets(records, false)
	if total.Total != 6 || total.Passed != 4 || total.Failed != 2 {
		t.Fatalf("total = %+v, want Total=6 Passed=4 Failed=2", total)
	}
	if got, want := total.FPYPct(), 200.0/3.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("FPYPct() = %v, want %v", got, want)
	}
	if monthly != nil {
		t.Fatalf("monthly = %+v, want nil when not grouped", monthly)
	}

	total, monthly = computeYieldBuckets(records, true)
	if total.Total != 6 || total.Passed != 4 || total.Failed != 2 {
		t.Fatalf("grouped total = %+v, want Total=6 Passed=4 Failed=2", total)
	}
	if len(monthly) != 3 {
		t.Fatalf("len(monthly) = %d, want 3", len(monthly))
	}
	if monthly[0].Label != "2026-06" || monthly[0].Total != 2 || monthly[0].Passed != 1 || monthly[0].Failed != 1 {
		t.Fatalf("monthly[0] = %+v, want June bucket Total=2 Passed=1 Failed=1", monthly[0])
	}
	if monthly[1].Label != "2026-07" || monthly[1].Total != 3 || monthly[1].Passed != 2 || monthly[1].Failed != 1 {
		t.Fatalf("monthly[1] = %+v, want July bucket Total=3 Passed=2 Failed=1", monthly[1])
	}
	if monthly[2].Label != "Unknown" || monthly[2].Total != 1 || monthly[2].Passed != 1 || monthly[2].Failed != 0 {
		t.Fatalf("monthly[2] = %+v, want Unknown bucket Total=1 Passed=1 Failed=0", monthly[2])
	}
}

func TestYieldBucketFPYPct_ZeroTotal(t *testing.T) {
	b := yieldBucket{}
	if got := b.FPYPct(); got != 0 {
		t.Fatalf("FPYPct() on empty bucket = %v, want 0", got)
	}
}
