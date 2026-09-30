// Command migrate_data loads a CSV export of the Azure SQL ArxProd database into a
// Postgres database (#216). Run from the repo root with ARX_MIGRATE_DSN set:
//
//	go run ./arx_go/cmd/migrate_data --csv-dir DIR --target-db ArxDev --confirm-truncate ArxDev [--dry-run]
//
// See docs/216-data-migration/runbook.md and internal/datamigrate.
package main

import (
	"context"
	"fmt"
	"os"

	_ "time/tzdata" // zone names must resolve on machines with no Go toolchain

	"arx/internal/datamigrate"
)

func main() {
	if err := datamigrate.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate_data:", err)
		os.Exit(1)
	}
}
