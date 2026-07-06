package main

import (
	"testing"

	"arx/arx_go/models"
)

func TestSuggestBaseNumber(t *testing.T) {
	cfg := models.BaseNumberConfig{Separator: "-", SegmentIndex: 1, Width: 5, Mode: "max_plus_one"}

	t.Run("max_plus_one with no existing parts", func(t *testing.T) {
		got := suggestBaseNumber(cfg, nil)
		if got != "00001" {
			t.Errorf("got %q, want 00001", got)
		}
	})

	t.Run("max_plus_one increments past the highest existing number", func(t *testing.T) {
		got := suggestBaseNumber(cfg, []string{"010-00003-01", "010-00007-01", "020-00002-01"})
		if got != "00008" {
			t.Errorf("got %q, want 00008", got)
		}
	})

	t.Run("max_plus_one ignores unparsable or short part numbers", func(t *testing.T) {
		got := suggestBaseNumber(cfg, []string{"010-00005-01", "not-a-number", "onlyonesegment"})
		if got != "00006" {
			t.Errorf("got %q, want 00006", got)
		}
	})

	t.Run("next_open_after fills a gap at or above floor", func(t *testing.T) {
		gapCfg := cfg
		gapCfg.Mode = "next_open_after"
		gapCfg.Floor = 300
		got := suggestBaseNumber(gapCfg, []string{"010-00300-01", "010-00301-01", "010-00305-01"})
		if got != "00302" {
			t.Errorf("got %q, want 00302", got)
		}
	})

	t.Run("next_open_after leaves smart numbers below floor untouched", func(t *testing.T) {
		gapCfg := cfg
		gapCfg.Mode = "next_open_after"
		gapCfg.Floor = 300
		got := suggestBaseNumber(gapCfg, []string{"010-00099-01", "010-00150-01"})
		if got != "00300" {
			t.Errorf("got %q, want 00300", got)
		}
	})
}
