package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	arxdb "arx/internal/db"
)

// statsQueries simulates n queries, each 1 ms, against ctx's sqlStats.
func statsQueries(ctx context.Context, n int) {
	for range n {
		recordRoundTrip(ctx, "q", time.Millisecond)
	}
}

func queryBranch(n int) func(context.Context) error {
	return func(ctx context.Context) error {
		statsQueries(ctx, n)
		return nil
	}
}

func TestRecordRoundTrip_CountsQueries(t *testing.T) {
	st := &sqlStats{}
	statsQueries(context.WithValue(context.Background(), ctxSQLStatsKey, st), 1)
	if st.count != 1 || st.queries != 1 {
		t.Errorf("count=%d queries=%d, want 1, 1", st.count, st.queries)
	}
}

func TestParallel_CountsCriticalPath(t *testing.T) {
	st := &sqlStats{}
	ctx := context.WithValue(context.Background(), ctxSQLStatsKey, st)
	statsQueries(ctx, 1)
	if err := parallel(ctx, queryBranch(1), queryBranch(3), queryBranch(2)); err != nil {
		t.Fatal(err)
	}
	if st.count != 4 || st.queries != 7 || st.total != 7*time.Millisecond {
		t.Errorf("count=%d queries=%d total=%s, want 4, 7, 7ms", st.count, st.queries, st.total)
	}
}

func TestParallel_Nested(t *testing.T) {
	st := &sqlStats{}
	ctx := context.WithValue(context.Background(), ctxSQLStatsKey, st)
	err := parallel(ctx,
		func(ctx context.Context) error {
			statsQueries(ctx, 1)
			err := parallel(ctx, queryBranch(2), queryBranch(1))
			statsQueries(ctx, 1)
			return err
		},
		queryBranch(3),
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.count != 4 || st.queries != 8 {
		t.Errorf("count=%d queries=%d, want 4, 8", st.count, st.queries)
	}
}

func TestParallel_NoStatsInContext(t *testing.T) {
	err := parallel(context.Background(), func(ctx context.Context) error {
		if ctx.Value(ctxSQLStatsKey) != nil {
			t.Error("branch sees stats the parent never had")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParallel_RunsConcurrently(t *testing.T) {
	var barrier sync.WaitGroup
	barrier.Add(3)
	branch := func(context.Context) error {
		barrier.Done()
		done := make(chan struct{})
		go func() { barrier.Wait(); close(done) }()
		select {
		case <-done:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("branches did not overlap")
		}
	}
	// A sequential run never releases the barrier: the WaitGroup would still count 2 after the
	// first branch times out, and its error stops the rest.
	if err := parallel(context.Background(), branch, branch, branch); err != nil {
		t.Fatal(err)
	}
}

func TestParallel_ErrorDoesNotCancelSiblings(t *testing.T) {
	errBoom := errors.New("boom")
	started := make(chan struct{})
	finished := false
	var siblingCtxErr error
	err := parallel(context.Background(),
		func(context.Context) error {
			select {
			case <-started:
			case <-time.After(time.Second):
			}
			return errBoom
		},
		func(ctx context.Context) error {
			close(started)
			time.Sleep(50 * time.Millisecond)
			siblingCtxErr = ctx.Err()
			finished = true
			return nil
		},
	)
	if !errors.Is(err, errBoom) {
		t.Errorf("err = %v, want errBoom", err)
	}
	if !finished {
		t.Error("sibling did not run to completion")
	}
	if siblingCtxErr != nil {
		t.Errorf("sibling ctx err = %v, want nil", siblingCtxErr)
	}
}

func TestParallel_ParentCancelReachesBranches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()
	err := parallel(ctx, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// A panic in a branch goroutine would kill the process; parallel must hand it back to the
// caller, where net/http recovers it per connection.
func TestParallel_RepanicsOnCaller(t *testing.T) {
	finished := false
	defer func() {
		if p := recover(); p != "kaboom" {
			t.Errorf("recovered %v, want kaboom", p)
		}
		if !finished {
			t.Error("sibling did not finish before the re-panic")
		}
	}()
	_ = parallel(context.Background(),
		func(context.Context) error { panic("kaboom") },
		func(context.Context) error {
			time.Sleep(20 * time.Millisecond)
			finished = true
			return nil
		},
	)
	t.Error("parallel returned instead of panicking")
}

func TestRecordHiddenEvent_CountsByKind(t *testing.T) {
	st := &sqlStats{}
	ctx := context.WithValue(context.Background(), ctxSQLStatsKey, st)
	recordHiddenEvent(ctx, arxdb.EventConnect)
	for range 2 {
		recordHiddenEvent(ctx, arxdb.EventPrepare)
	}
	for range 3 {
		recordHiddenEvent(ctx, arxdb.EventPing)
	}
	if st.opens != 1 || st.prepares != 2 || st.pings != 3 || st.count != 0 || st.queries != 0 {
		t.Errorf("opens=%d prepares=%d pings=%d count=%d queries=%d, want 1, 2, 3, 0, 0",
			st.opens, st.prepares, st.pings, st.count, st.queries)
	}
	recordHiddenEvent(context.Background(), arxdb.EventPing) // no stats in ctx: must not panic
}

func TestParallel_FoldsHiddenEvents(t *testing.T) {
	st := &sqlStats{}
	ctx := context.WithValue(context.Background(), ctxSQLStatsKey, st)
	err := parallel(ctx,
		func(ctx context.Context) error {
			statsQueries(ctx, 1)
			recordHiddenEvent(ctx, arxdb.EventConnect)
			return nil
		},
		func(ctx context.Context) error {
			statsQueries(ctx, 3)
			recordHiddenEvent(ctx, arxdb.EventPrepare)
			recordHiddenEvent(ctx, arxdb.EventPrepare)
			recordHiddenEvent(ctx, arxdb.EventPing)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.count != 3 || st.queries != 4 || st.opens != 1 || st.prepares != 2 || st.pings != 1 {
		t.Errorf("count=%d queries=%d opens=%d prepares=%d pings=%d, want 3, 4, 1, 2, 1",
			st.count, st.queries, st.opens, st.prepares, st.pings)
	}
}

func TestSQLStats_HiddenSummary(t *testing.T) {
	st := sqlStats{opens: 1, prepares: 2, pings: 3}
	if got, want := st.hiddenSummary(), "1 opens, 2 prepares, 3 pings"; got != want {
		t.Errorf("hiddenSummary() = %q, want %q", got, want)
	}
}
