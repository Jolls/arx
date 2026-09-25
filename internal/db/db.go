// Package db provides a SQL Server connection helper for Arx.
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
)

// Connect opens and verifies a database connection for the given engine and
// returns the pool plus the matching Dialect. Both "sqlserver" and "postgres"
// are wired up; go-mssqldb is dropped at cutover (Phase 3).
func Connect(engine, dsn string) (*sql.DB, Dialect, error) {
	var driver string
	var dialect Dialect
	switch engine {
	case "sqlserver":
		driver, dialect = "sqlserver", NewSQLServerDialect()
	case "postgres":
		driver, dialect = "pgx", NewPostgresDialect()
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
