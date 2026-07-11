// Package db provides a SQL Server connection helper shared across Arx apps.
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/microsoft/go-mssqldb"
)

// Connect opens and verifies a database connection for the given engine and
// returns the pool plus the matching Dialect. Only "sqlserver" is wired up in
// Phase 1; "postgres" is added in Phase 2.
func Connect(engine, dsn string) (*sql.DB, Dialect, error) {
	var driver string
	var dialect Dialect
	switch engine {
	case "sqlserver":
		driver, dialect = "sqlserver", NewSQLServerDialect()
	default:
		return nil, nil, fmt.Errorf("unsupported db engine %q", engine)
	}

	database, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("ping: %w", err)
	}
	return database, dialect, nil
}
