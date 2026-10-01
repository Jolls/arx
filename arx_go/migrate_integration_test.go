//go:build integration

package main

import (
	"context"
	"testing"
)

// TestIntegration_MultiStatementExec pins what the goose migration format (#91)
// relies on: pgx runs an arg-less multi-statement Exec via the simple protocol,
// so a whole migration body can be one goose StatementBegin/End block.
func TestIntegration_MultiStatementExec(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	if _, err := h.execContext(context.Background(), "SELECT 1;\nSELECT 2;"); err != nil {
		t.Fatalf("multi-statement exec: %v", err)
	}
}
