package main

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseDB_NoDBIsNoop(t *testing.T) {
	testHandler().CloseDB() // must not panic
}

func TestCloseDB_ClosesConnection(t *testing.T) {
	h := testHandlerWithDB()
	db := h.DB()
	h.CloseDB()
	if err := db.Ping(); err == nil {
		t.Fatal("expected error pinging a closed DB")
	}
}

func TestHeadlessEnabled(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "": false, "0": false, "true": false} {
		t.Setenv("ARX_HEADLESS", v)
		if got := headlessEnabled(); got != want {
			t.Errorf("ARX_HEADLESS=%q: got %v, want %v", v, got, want)
		}
	}
}

// serveOn returns a fake startServer that serves on addr and reports a listener stop via onStopped.
func serveOn(addr string) func(func()) (*http.Server, string) {
	return func(onStopped func()) (*http.Server, string) {
		server := &http.Server{Addr: addr, Handler: http.NotFoundHandler()}
		go func() {
			if err := server.ListenAndServe(); err != nil {
				onStopped()
			}
		}()
		return server, ""
	}
}

func TestRunHeadless_ContextCancelShutsDownAndClosesDB(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var closed atomic.Int32
	done := make(chan int, 1)
	go func() { done <- runHeadless(ctx, serveOn(addr), func() { closed.Add(1) }) }()

	for range 100 { // wait for the listener
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runHeadless did not return after cancel")
	}
	if closed.Load() != 1 {
		t.Errorf("closeDB called %d times, want 1", closed.Load())
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Error("listener still accepting after shutdown")
	}
}

func TestRunHeadless_ListenerFailureReturnsNonZero(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var closed atomic.Int32
	code := runHeadless(context.Background(), serveOn(ln.Addr().String()), func() { closed.Add(1) })
	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	if closed.Load() != 1 {
		t.Errorf("closeDB called %d times, want 1", closed.Load())
	}
}
