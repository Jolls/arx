// Package db provides a SQL Server connection helper shared across Arx apps.
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/microsoft/go-mssqldb"
)

// Connect opens and verifies a SQL Server connection.
// DSN format: sqlserver://user:password@host?database=MyDB&encrypt=true
func Connect(dsn string) (*sql.DB, error) {
	database, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return database, nil
}
