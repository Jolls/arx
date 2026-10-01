package main

import (
	"context"
	"errors"
	"testing"
)

func TestCombineInUse(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name            string
		shared          bool
		ownUsed         bool
		ownErr          error
		otherUsed       bool
		wantUsed        bool
		wantErr         error
		wantOtherCalled bool
	}{
		{name: "not shared uses own only", otherUsed: true},
		{name: "shared, own used", shared: true, ownUsed: true, wantUsed: true},
		{name: "shared, other table counts", shared: true, otherUsed: true, wantUsed: true, wantOtherCalled: true},
		{name: "shared, neither used", shared: true, wantOtherCalled: true},
		{name: "shared, own error short-circuits", shared: true, ownErr: boom, otherUsed: true, wantErr: boom},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			otherCalled, otherExclude := false, -1
			own := func(context.Context, string, int) (bool, error) { return c.ownUsed, c.ownErr }
			other := func(_ context.Context, _ string, ex int) (bool, error) {
				otherCalled, otherExclude = true, ex
				return c.otherUsed, nil
			}
			used, err := combineInUse(own, other, c.shared)(context.Background(), "LOCAL:a", 7)
			if used != c.wantUsed || err != c.wantErr || otherCalled != c.wantOtherCalled {
				t.Errorf("got used=%v err=%v otherCalled=%v, want %v/%v/%v", used, err, otherCalled, c.wantUsed, c.wantErr, c.wantOtherCalled)
			}
			// The caller's excludeID is an id in its own table, so the other table gets 0.
			if otherCalled && otherExclude != 0 {
				t.Errorf("other excludeID = %d, want 0", otherExclude)
			}
		})
	}
}
