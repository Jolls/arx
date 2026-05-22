// Package db re-exports arx/arxlib/db for backwards compatibility with
// internal import paths. New code should import arx/arxlib/db directly.
package db

import (
	"database/sql"

	arxdb "arx/arxlib/db"
)

// Connect opens and verifies a SQL Server connection.
func Connect(dsn string) (*sql.DB, error) {
	return arxdb.Connect(dsn)
}
