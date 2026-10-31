package db

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestOpenPool_Limits(t *testing.T) {
	database, err := openPool("postgres://u:p@127.0.0.1:1/x?sslmode=require") // never dialed
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := database.Stats().MaxOpenConnections; got != 12 {
		t.Errorf("MaxOpenConnections = %d, want 12", got)
	}
}

func TestShouldPing_Threshold(t *testing.T) {
	for _, c := range []struct {
		idle time.Duration
		want bool
	}{{29 * time.Second, false}, {30 * time.Second, false}, {31 * time.Second, true}} {
		if got := shouldPing(context.Background(), stdlib.ShouldPingParams{IdleDuration: c.idle}); got != c.want {
			t.Errorf("shouldPing(idle %s) = %v, want %v", c.idle, got, c.want)
		}
	}
}

func TestConnect_Errors(t *testing.T) {
	_, err := Connect("postgres://u:SECRETPW@127.0.0.1:1/x?sslmode=require")
	if err == nil {
		t.Fatal("unreachable server: want error")
	}
	if !strings.HasPrefix(err.Error(), "ping: ") {
		t.Errorf("unreachable server: error %q lacks ping prefix", err)
	}
	if strings.Contains(err.Error(), "SECRETPW") {
		t.Error("unreachable server: error leaks the password")
	}

	_, err = Connect("postgres://u:SECRETPW@h:badport/x")
	if err == nil {
		t.Fatal("bad dsn: want error")
	}
	if strings.Contains(err.Error(), "SECRETPW") {
		t.Error("bad dsn: error leaks the password")
	}
}

type ctxKey struct{}

// captureEvents installs an observer that records every event and the ctx it arrived with.
func captureEvents(t *testing.T) (events *[]Event, ctxs *[]context.Context) {
	t.Helper()
	events, ctxs = new([]Event), new([]context.Context)
	SetObserver(func(ctx context.Context, e Event) {
		*events = append(*events, e)
		*ctxs = append(*ctxs, ctx)
	})
	t.Cleanup(func() { SetObserver(nil) })
	return events, ctxs
}

func TestEventTracer_ReportsConnectAndPrepare(t *testing.T) {
	events, ctxs := captureEvents(t)
	ctx := context.WithValue(context.Background(), ctxKey{}, "req")
	tr := eventTracer{}

	if got := tr.TraceConnectStart(ctx, pgx.TraceConnectStartData{}); got != ctx {
		t.Error("TraceConnectStart must return the ctx it was given")
	}
	tr.TracePrepareEnd(ctx, nil, pgx.TracePrepareEndData{AlreadyPrepared: false})
	tr.TracePrepareEnd(ctx, nil, pgx.TracePrepareEndData{AlreadyPrepared: true})

	want := []Event{EventConnect, EventPrepare}
	if len(*events) != len(want) || (*events)[0] != want[0] || (*events)[1] != want[1] {
		t.Fatalf("events = %v, want %v", *events, want)
	}
	for _, c := range *ctxs {
		if c.Value(ctxKey{}) != "req" {
			t.Error("observer did not receive the originating ctx")
		}
	}
}

func TestShouldPing_ReportsPing(t *testing.T) {
	events, _ := captureEvents(t)
	for _, idle := range []time.Duration{29 * time.Second, 30 * time.Second} {
		shouldPing(context.Background(), stdlib.ShouldPingParams{IdleDuration: idle})
	}
	if len(*events) != 0 {
		t.Fatalf("no ping at or under the threshold, got %v", *events)
	}
	if !shouldPing(context.Background(), stdlib.ShouldPingParams{IdleDuration: 31 * time.Second}) {
		t.Fatal("31 s idle must ping")
	}
	if len(*events) != 1 || (*events)[0] != EventPing {
		t.Errorf("events = %v, want one EventPing", *events)
	}
}

func TestSetObserver_NilStopsReports(t *testing.T) {
	var n atomic.Int32
	SetObserver(func(context.Context, Event) { n.Add(1) })
	t.Cleanup(func() { SetObserver(nil) })

	eventTracer{}.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
	if n.Load() != 1 {
		t.Fatalf("observer count = %d after one event, want 1", n.Load())
	}
	SetObserver(nil)
	eventTracer{}.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
	shouldPing(context.Background(), stdlib.ShouldPingParams{IdleDuration: 31 * time.Second})
	if n.Load() != 1 {
		t.Errorf("observer count = %d after SetObserver(nil), want 1", n.Load())
	}
}
