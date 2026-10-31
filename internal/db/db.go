// Package db provides a Postgres connection helper for Arx.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
// Its pool is not observed (SetObserver covers pools built by Connect only).
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

const (
	maxPoolConns      = 12 // covers the widest page fan-out (#426)
	connMaxIdleTime   = 15 * time.Minute
	pingIdleThreshold = 30 * time.Second
)

// Event is a database round trip that is not a query: invisible to the per-query count
// but paid by the user (#430).
type Event int

const (
	EventConnect Event = iota + 1 // new connection opened
	EventPrepare                  // statement prepared on a connection (first use of that SQL there)
	EventPing                     // idle liveness ping before reusing a connection
)

// SetObserver registers fn to be told, with the originating request's ctx, of each Event on
// pools built by Connect. nil clears it. Safe for concurrent use.
func SetObserver(fn func(context.Context, Event)) {
	if fn == nil {
		observer.Store(nil)
		return
	}
	observer.Store(&fn)
}

var observer atomic.Pointer[func(context.Context, Event)]

func notify(ctx context.Context, e Event) {
	if fn := observer.Load(); fn != nil {
		(*fn)(ctx, e)
	}
}

// eventTracer reports connection opens and statement preparations; pings come from shouldPing.
type eventTracer struct{}

func (eventTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (eventTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (eventTracer) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	notify(ctx, EventConnect)
	return ctx
}
func (eventTracer) TraceConnectEnd(context.Context, pgx.TraceConnectEndData) {}
func (eventTracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, _ pgx.TracePrepareStartData) context.Context {
	return ctx
}
func (eventTracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, d pgx.TracePrepareEndData) {
	if !d.AlreadyPrepared {
		notify(ctx, EventPrepare)
	}
}

// shouldPing replaces pgx's default of pinging any connection idle over 1 s, which
// costs a round trip on nearly every page (#426). A connection that dies inside the
// threshold is noticed on its first query instead.
func shouldPing(ctx context.Context, p stdlib.ShouldPingParams) bool {
	if p.IdleDuration <= pingIdleThreshold {
		return false
	}
	notify(ctx, EventPing)
	return true
}

// openPool builds the main pool for dsn without connecting.
func openPool(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	cfg.Tracer = eventTracer{}
	database := stdlib.OpenDB(*cfg, stdlib.OptionShouldPing(shouldPing))
	database.SetMaxOpenConns(maxPoolConns)
	database.SetMaxIdleConns(maxPoolConns)
	database.SetConnMaxIdleTime(connMaxIdleTime)
	return database, nil
}

// Connect opens and verifies a Postgres connection for dsn.
func Connect(dsn string) (*sql.DB, error) {
	database, err := openPool(dsn)
	if err != nil {
		return nil, err
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return database, nil
}
