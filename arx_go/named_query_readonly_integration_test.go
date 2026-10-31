//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
)

// execQuery must run on the read-only handle: a SELECT calling a write-capable
// function passes isSafeQuery but the database refuses it. A least-privilege app login
// (no USAGE on the sequence) is refused with "permission denied" before the read-only check.
func TestIntegration_ExecQuery_RefusesWritesAtDB(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	const q = "SELECT nextval(pg_get_serial_sequence('part', 'id'))"
	if !isSafeQuery(q) {
		t.Fatal("test query should pass isSafeQuery")
	}
	_, err := h.execQuery(context.Background(), q, "single", nil)
	if err == nil || !(strings.Contains(err.Error(), "read-only") || strings.Contains(err.Error(), "permission denied")) {
		t.Fatalf("execQuery err = %v, want read-only transaction or permission refusal", err)
	}
}
