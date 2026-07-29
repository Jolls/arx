package folderpick

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestBrowseFolderContext_CanceledContext verifies that an already-cancelled
// context short-circuits the dialog launch and returns "" quickly, instead
// of hanging or panicking.
func TestBrowseFolderContext_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	got := BrowseFolderContext(ctx)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected empty string for canceled context, got %q", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("BrowseFolderContext took %v with an already-canceled context; expected near-instant return", elapsed)
	}
}

// TestBrowseFileContext_CanceledContext mirrors the folder-picker case above
// for the file picker.
func TestBrowseFileContext_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	got := BrowseFileContext(ctx)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected empty string for canceled context, got %q", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("BrowseFileContext took %v with an already-canceled context; expected near-instant return", elapsed)
	}
}

// TestBrowseFolderContext_ExpiredDeadline covers the timeout path
// (context.WithTimeout/WithDeadline) as a distinct cancellation reason from
// an explicit Cancel call above.
func TestBrowseFolderContext_ExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	start := time.Now()
	got := BrowseFolderContext(ctx)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected empty string for expired deadline, got %q", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("BrowseFolderContext took %v with an expired deadline; expected near-instant return", elapsed)
	}
}

// TestHideWindow_NoPanic exercises hideWindow on both the windows and
// !windows build variants without asserting on OS-specific SysProcAttr
// fields (those differ per platform and belong to the OS-tagged source
// files, not this shared test).
func TestHideWindow_NoPanic(t *testing.T) {
	cmd := exec.Command("echo")
	hideWindow(cmd)
}
