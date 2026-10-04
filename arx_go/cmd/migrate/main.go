// Command migrate applies SQL/migrations with goose (#91). Run from the
// repo root with ARX_MIGRATE_DSN set to a DSN for a DDL-capable login:
//
//	go run ./arx_go/cmd/migrate status        # applied / pending per file
//	go run ./arx_go/cmd/migrate up [--yes]    # apply pending, in version order
//
// A separate console command because Arx.exe is linked -H windowsgui. See
// internal/migrate for the rules.
package main

import (
	"context"
	"fmt"
	"os"

	"arx/internal/migrate"
)

func main() {
	if err := migrate.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}
