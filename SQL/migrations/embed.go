// Package migrations embeds the Postgres migration files so the migrate runner
// (internal/migrate, #91) applies exactly the set it was built with.
package migrations

import "embed"

// FS holds every *.sql file in this directory, at its root.
//
//go:embed *.sql
var FS embed.FS
