//go:build integration

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	"arx/internal/migrate"
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

// TestIntegration_MigrationsAllApplied: ArxDev (built by build_schema.sh, which
// baselines the ledger) shows every embedded migration applied.
func TestIntegration_MigrationsAllApplied(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()

	p, err := migrate.NewProvider(h.DB())
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := p.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v (baseline ArxDev's schema_migrations, see SQL/SCHEMA.md#migrations)", err)
	}
	for _, s := range statuses {
		if s.State != goose.StateApplied {
			t.Errorf("%s is %s; baseline ArxDev's schema_migrations (SQL/SCHEMA.md#migrations) or run migrate up", s.Source.Path, s.State)
		}
	}
}

// TestIntegration_FailedMigrationLeavesNoLedgerRow: a migration that fails
// mid-body rolls back as a whole and records no schema_migrations row.
func TestIntegration_FailedMigrationLeavesNoLedgerRow(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	const version = 99990101000000
	fsys := fstest.MapFS{"99990101000000_91_rollback_probe.sql": {Data: []byte(
		"-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\nSELECT 1/0;\n-- +goose StatementEnd\n")}}
	p, err := goose.NewProvider(goose.DialectPostgres, h.DB(), fsys, goose.WithTableName("schema_migrations"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Up(ctx)
	var pe *goose.PartialError
	if !errors.As(err, &pe) || !strings.Contains(pe.Err.Error(), "division by zero") {
		t.Fatalf("up err = %v, want a PartialError from the probe's division by zero", err)
	}

	var n int
	if err := h.queryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version_id = $1", version).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("failed migration left %d ledger row(s)", n)
	}
}
