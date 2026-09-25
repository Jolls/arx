package main

import (
	"sync"
	"testing"

	arxbase "arx/internal/config"
)

// TestRuntimeState_ConcurrentUpdate hammers update() against readers of every
// piece of runtime state. It only proves anything under `go test -race` (CI
// runs it), where the pre-#196 plain-field writes fail immediately.
func TestRuntimeState_ConcurrentUpdate(t *testing.T) {
	h := New(nil, nil, &arxbase.Config{}, templatesFS, nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = h.cfg().DBServer
				_ = h.cfg().TestMode
				_ = h.st().schemaMismatch
				_ = h.st().partCategories
				_ = h.companyLogoURL()
				_ = h.dbUnusable()
				_ = h.dia()
			}
		}()
	}

	for i := 0; i < 500; i++ {
		h.update(func(s *runtimeState) {
			s.cfg.DBServer = "srv"
			s.cfg.TestMode = i%2 == 0
			s.schemaMismatch = "x"
			s.companyLogo = "logo"
		})
	}
	close(stop)
	wg.Wait()

	if got := h.cfg().DBServer; got != "srv" {
		t.Errorf("DBServer = %q, want %q", got, "srv")
	}
}

// TestRuntimeState_UpdateDoesNotMutatePublishedSnapshot checks the copy-on-write
// contract: a snapshot a reader already holds never changes under it.
func TestRuntimeState_UpdateDoesNotMutatePublishedSnapshot(t *testing.T) {
	h := New(nil, nil, &arxbase.Config{}, templatesFS, nil)
	before := h.cfg()
	h.update(func(s *runtimeState) { s.cfg.DBServer = "changed" })
	if before.DBServer != "" {
		t.Errorf("held snapshot mutated: DBServer = %q", before.DBServer)
	}
	if h.cfg().DBServer != "changed" {
		t.Errorf("update not visible: DBServer = %q", h.cfg().DBServer)
	}
}
