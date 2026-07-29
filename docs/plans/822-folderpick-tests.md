# Issue #822: Test coverage for arxlib/folderpick

## Background
`arxlib/folderpick/folderpick.go` has zero test files. The exported functions (`BrowseFolderContext`, `BrowseFolder`, `BrowseFileContext`, `BrowseFile`) shell out to a real interactive PowerShell/WinForms dialog, so they cannot be meaningfully unit-tested end-to-end. The one deterministic, non-flaky behavior available without a live dialog: calling `BrowseFolderContext`/`BrowseFileContext` with an already-cancelled `context.Context` must return `""` quickly, without hanging or panicking (this exercises `exec.CommandContext`'s pre-start context check / `LookPath` failure path — both return non-nil `err` from `cmd.Output()`, which the function already maps to `""`).

`BrowseFolder`/`BrowseFile` (the no-context 5-minute-timeout wrappers) are not covered — they always invoke a real dialog with no way to inject cancellation, so they are excluded from this plan (see Open questions).

Confirmed file contents:
- `arxlib/folderpick/folderpick_windows.go` — `//go:build windows`, sets `cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}`.
- `arxlib/folderpick/folderpick_other.go` — `//go:build !windows`, `hideWindow` is a no-op.

Both variants share the same signature `func hideWindow(cmd *exec.Cmd)`, so a single build-tag-free test file can call it on any OS without referencing OS-specific fields of `syscall.SysProcAttr` (which differ per platform and would break the `!windows` build if referenced directly).

## New test file: arxlib/folderpick/folderpick_test.go
No build tag — must compile and pass on both Windows and `!windows` (per CLAUDE.md Cross-Platform Direction; `arxlib` has no extra system deps per the WSL build note).

Package: `folderpick` (same-package test).

Contents:

```go
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
```

## Steps
1. Create `arxlib/folderpick/folderpick_test.go` with the exact content above.
2. Run `go test ./...` from inside `arxlib/` (per CLAUDE.md go.work gotcha — root `go test ./...` fails since repo root isn't a module).
3. Confirm `go vet ./...` from `arxlib/` passes.
4. Confirm via `build.bat` (from `arx_go/`) that the full pre-existing `go test ./...` sweep still passes, since `build.bat` runs tests before building `Arx.exe`.

## Resolved decisions
- `BrowseFolder`/`BrowseFile` (no-context, 5-minute-timeout wrappers) are left untested — no way to inject cancellation without a real dialog.
- Cancellation/expired-deadline tests use a 500ms elapsed-time ceiling.
- No Windows-specific assertion on `cmd.SysProcAttr.HideWindow` — `TestHideWindow_NoPanic`'s cross-platform no-panic check is sufficient.

### Critical Files for Implementation
- arxlib/folderpick/folderpick.go
- arxlib/folderpick/folderpick_windows.go
- arxlib/folderpick/folderpick_other.go
- arxlib/folderpick/folderpick_test.go (new)
