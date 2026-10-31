package main

import (
	"fmt"
	"strings"
	"testing"
)

// checkRoundTripGrowth returns an error when a page's critical-path round trips changed after
// child rows were added: the signature of an N+1 (or of a branch that stopped running).
func checkRoundTripGrowth(name string, before, after int) error {
	if before == after {
		return nil
	}
	return fmt.Errorf("%s: %d round trips before adding child rows, %d after: the count must not change with row count (#430)",
		name, before, after)
}

func TestCheckRoundTripGrowth(t *testing.T) {
	if err := checkRoundTripGrowth("page", 3, 3); err != nil {
		t.Errorf("(3,3): %v, want nil", err)
	}
	for _, after := range []int{4, 2} {
		err := checkRoundTripGrowth("page", 3, after)
		if err == nil || !strings.Contains(err.Error(), "3 round trips") || !strings.Contains(err.Error(), string(rune('0'+after))+" after") {
			t.Errorf("(3,%d): error %v, want one naming both counts", after, err)
		}
	}
}
