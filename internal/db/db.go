// Package db provides a Postgres connection helper for Arx.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ReadOnlyStatementTimeoutMS bounds any statement run on a ReadOnly handle.
const ReadOnlyStatementTimeoutMS = 30000

// ReadOnlyDSN returns dsn with the runtime parameters that make every session
// default to read-only transactions and bound statement time. It overrides any
// value already present for those keys.
func ReadOnlyDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		// url.Error embeds the full URL (password included); keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	q := u.Query()
	q.Set("default_transaction_read_only", "on")
	q.Set("statement_timeout", strconv.Itoa(ReadOnlyStatementTimeoutMS))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OpenReadOnly returns a lazily-connecting handle for dsn whose sessions
// default to read-only transactions with a statement timeout, so the database
// itself refuses writes (including from functions called in a SELECT). No
// connection is made until first use. Used for admin-authored named queries.
func OpenReadOnly(dsn string) (*sql.DB, error) {
	roDSN, err := ReadOnlyDSN(dsn)
	if err != nil {
		return nil, err
	}
	database, err := sql.Open("pgx", roDSN)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	database.SetMaxOpenConns(2)
	return database, nil
}

// Connect opens and verifies a Postgres connection for dsn.
func Connect(dsn string) (*sql.DB, error) {
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return database, nil
}
