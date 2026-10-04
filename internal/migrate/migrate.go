// Package migrate applies the embedded SQL/migrations files with goose
// (#91). It backs the console command arx_go/cmd/migrate:
//
//	go run ./arx_go/cmd/migrate status        # applied / pending per file
//	go run ./arx_go/cmd/migrate up [--yes]    # apply pending, in version order
//
// Credentials come only from ARX_MIGRATE_DSN, a DSN for a DDL-capable login; app
// config and the per-user secrets store are never read, and nothing is saved.
// Forward-only: there is no down. Each file runs in its own transaction together
// with its schema_migrations row. `up` against any database not named ArxDev asks
// for the database name to be typed, unless --yes is given.
package migrate

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"arx/SQL/migrations"
)

// DSNEnv is the only source of the runner's connection string.
const DSNEnv = "ARX_MIGRATE_DSN"

// ErrNotConfirmed is returned when the operator doesn't confirm a non-ArxDev target.
var ErrNotConfirmed = errors.New("not confirmed; nothing applied")

var errUsage = errors.New("usage: migrate status | migrate up [--yes]")

// Run executes `status` or `up` (args excludes the program name).
func Run(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || (args[0] != "status" && args[0] != "up") {
		return errUsage
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	yes := flags.Bool("yes", false, "apply to a database not named ArxDev without prompting")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w (%v)", errUsage, err)
	}
	if flags.NArg() > 0 {
		return errUsage
	}

	dsn := getenv(DSNEnv)
	if dsn == "" {
		return fmt.Errorf("%s is not set: set it to a DSN for a DDL-capable login (app config and secrets are never used)", DSNEnv)
	}
	// The parse error is not wrapped: it can echo the DSN, password included.
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("%s is not a valid Postgres connection string", DSNEnv)
	}
	if cfg.Database == "" {
		return fmt.Errorf("%s must name a database", DSNEnv)
	}
	fmt.Fprintf(out, "Target: %s:%d/%s (user %s)\n", cfg.Host, cfg.Port, cfg.Database, cfg.User)

	if args[0] == "up" && !strings.EqualFold(cfg.Database, "ArxDev") && !*yes {
		if err := confirm(cfg.Database, in, out); err != nil {
			return err
		}
	}

	// Migrations report through RAISE NOTICE; SELECT output is discarded.
	cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) { fmt.Fprintf(out, "NOTICE: %s\n", n.Message) }
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := checkLedger(ctx, db); err != nil {
		return err
	}
	p, err := NewProvider(db)
	if err != nil {
		return err
	}

	if args[0] == "status" {
		statuses, err := p.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			fmt.Fprintf(out, "%-8s %s\n", s.State, s.Source.Path)
		}
		return nil
	}

	res, err := p.Up(ctx)
	var pe *goose.PartialError
	if errors.As(err, &pe) {
		res = pe.Applied
	}
	for _, r := range res {
		fmt.Fprintln(out, r)
	}
	if err != nil {
		return err
	}
	if len(res) == 0 {
		fmt.Fprintln(out, "No pending migrations.")
	}
	return nil
}

// confirm requires the operator to type the target database's exact name.
func confirm(database string, in io.Reader, out io.Writer) error {
	fmt.Fprintf(out, "%q is not ArxDev. Type the database name to apply pending migrations to it: ", database)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if strings.TrimSpace(line) != database {
		return ErrNotConfirmed
	}
	return nil
}

// checkLedger refuses a database whose ledger isn't baselined: without the table
// goose would create its own (different column types), and without goose's
// version-0 row its up fails with "missing zero version".
func checkLedger(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version_id = 0").Scan(&n); err != nil {
		return fmt.Errorf("reading schema_migrations (load SQL/schema_migrations.sql first): %w", err)
	}
	if n == 0 {
		return errors.New("schema_migrations has no baseline row (version_id 0); baseline the ledger first, see SQL/SCHEMA.md#migrations")
	}
	return nil
}

// NewProvider returns a goose provider over the embedded migrations, recording
// into schema_migrations.
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithTableName("schema_migrations"))
}
