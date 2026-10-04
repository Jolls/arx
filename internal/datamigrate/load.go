package datamigrate

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"arx/SQL/migrations"
)

// DSNEnv is the only source of the connection string: a DDL-capable login (TRUNCATE,
// dropping a constraint), the same variable the migrate command uses.
const DSNEnv = "ARX_MIGRATE_DSN"

const usage = `usage: migrate_data --csv-dir DIR --target-db NAME --confirm-truncate NAME [--dry-run] [--allow-prod]
                    [--server-zone UTC] [--desktop-zone America/Los_Angeles]`

// Run loads the CSV export into the database named by ARX_MIGRATE_DSN (args excludes the
// program name). The load is one transaction: any failure, or --dry-run, rolls back.
func Run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	flags := flag.NewFlagSet("migrate_data", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("csv-dir", "", "directory of exported <table>.csv files and manifest.csv")
	target := flags.String("target-db", "", "database name the load is meant for")
	confirm := flags.String("confirm-truncate", "", "repeat the database name to allow truncating it")
	dryRun := flags.Bool("dry-run", false, "run the whole load, then roll it back")
	allowProd := flags.Bool("allow-prod", false, "permit a database whose name contains arxprod (cutover only)")
	opt := Options{}
	flags.StringVar(&opt.ServerZone, "server-zone", "UTC", "zone of the source server's clock (server-default columns)")
	flags.StringVar(&opt.DesktopZone, "desktop-zone", "America/Los_Angeles", "zone of the desktop clocks (Go time.Now columns)")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return fmt.Errorf("%s", usage)
	}
	if *dir == "" || *target == "" {
		return fmt.Errorf("%s", usage)
	}
	if *confirm != *target {
		return fmt.Errorf("--confirm-truncate must repeat --target-db (%q): this load TRUNCATES every data table", *target)
	}
	if strings.Contains(strings.ToLower(*target), "arxprod") && !*allowProd {
		return errors.New("target looks like ArxProd; pass --allow-prod only for the real cutover")
	}
	dsn := getenv(DSNEnv)
	if dsn == "" {
		return fmt.Errorf("%s is not set: set it to a DSN for a DDL-capable login", DSNEnv)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil { // not wrapped: the error can echo the DSN, password included
		return fmt.Errorf("%s is not a valid DSN", DSNEnv)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var db string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&db); err != nil {
		return err
	}
	if db != *target {
		return fmt.Errorf("connected to database %q but --target-db is %q; nothing changed", db, *target)
	}
	fmt.Fprintf(out, "Target: %s\n", db)

	schema, err := LoadSchema(ctx, conn)
	if err != nil {
		return err
	}
	prepared, err := Prepare(*dir, schema, opt)
	if err != nil {
		return err
	}
	if orphans := FindOrphans(prepared.Tables, schema.FKs); len(orphans) > 0 {
		fmt.Fprintf(out, "%d orphaned rows (child FK value with no parent row); fix the source data, nothing changed:\n", len(orphans))
		for _, o := range orphans {
			fmt.Fprintln(out, "  "+o.String())
		}
		return fmt.Errorf("%d orphaned rows", len(orphans))
	}
	manifest, err := ReadManifest(filepath.Join(*dir, "manifest.csv"))
	if err != nil {
		return err
	}
	if err := CheckManifest(prepared.Tables, manifest); err != nil {
		return err
	}
	for _, s := range prepared.SkippedConfig {
		fmt.Fprintf(out, "app_config: not carried over: %s\n", s)
	}
	fmt.Fprintf(out, "app_config: carried over: %s\n", strings.Join(prepared.CarriedConfig, ", "))

	// setval is not transactional, so a rollback (or --dry-run) would leave the sequences moved.
	seqs, err := snapshotSequences(ctx, conn)
	if err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback(ctx)
			if err := restoreSequences(ctx, conn, seqs); err != nil {
				fmt.Fprintf(out, "WARNING: could not restore sequences after rollback: %v\n", err)
			}
		}
	}()
	if err := load(ctx, tx, prepared, schema, out); err != nil {
		return fmt.Errorf("load rolled back, target unchanged: %w", err)
	}
	if *dryRun {
		fmt.Fprintln(out, "Dry run OK: everything loaded and verified; rolling back (target unchanged).")
		return nil
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	fmt.Fprintln(out, "Committed.")
	return nil
}

type seqState struct {
	name  string
	last  *int64 // nil = never called
	start int64
}

func snapshotSequences(ctx context.Context, conn *pgx.Conn) ([]seqState, error) {
	rows, err := conn.Query(ctx, "SELECT sequencename, last_value, start_value FROM pg_sequences WHERE schemaname = current_schema()")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []seqState
	for rows.Next() {
		var s seqState
		if err := rows.Scan(&s.name, &s.last, &s.start); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func restoreSequences(ctx context.Context, conn *pgx.Conn, seqs []seqState) error {
	for _, s := range seqs {
		target, called := s.start, false
		if s.last != nil {
			target, called = *s.last, true
		}
		if _, err := conn.Exec(ctx, "SELECT setval($1::regclass, $2, $3)", pgx.Identifier{s.name}.Sanitize(), target, called); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// LoadSchema reads the target's base tables, identity columns and single-column FKs.
func LoadSchema(ctx context.Context, conn *pgx.Conn) (*Schema, error) {
	s := &Schema{Columns: map[string][]Column{}}
	rows, err := conn.Query(ctx, `
		SELECT c.table_name, c.column_name, c.data_type, c.is_identity = 'YES'
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t, c, typ string
		var identity bool
		if err := rows.Scan(&t, &c, &typ, &identity); err != nil {
			return nil, err
		}
		s.Columns[t] = append(s.Columns[t], Column{c, typ})
		if identity {
			s.Identity = append(s.Identity, tableColumn{t, c})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = conn.Query(ctx, `
		SELECT c.conrelid::regclass::text, a.attname, c.confrelid::regclass::text, fa.attname, array_length(c.conkey, 1)
		FROM pg_constraint c
		JOIN pg_attribute a  ON a.attrelid = c.conrelid  AND a.attnum = c.conkey[1]
		JOIN pg_attribute fa ON fa.attrelid = c.confrelid AND fa.attnum = c.confkey[1]
		WHERE c.contype = 'f' AND c.connamespace = current_schema()::regnamespace`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fk FK
		var n int
		if err := rows.Scan(&fk.Child, &fk.ChildCol, &fk.Parent, &fk.ParentCol, &n); err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, fmt.Errorf("multi-column FK on %s is not supported by the orphan check", fk.Child)
		}
		s.FKs = append(s.FKs, fk)
	}
	return s, rows.Err()
}

func load(ctx context.Context, tx pgx.Tx, p *Prepared, s *Schema, out io.Writer) error {
	var names []string
	for _, t := range p.Tables {
		if t.Name != "app_config" {
			names = append(names, pgx.Identifier{t.Name}.Sanitize())
		}
	}
	names = append(names, "part_category", "attachment_category")
	if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(names, ", ")+" RESTART IDENTITY"); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	// The app_config row that must survive is the target's schema_version.
	if _, err := tx.Exec(ctx, "DELETE FROM app_config WHERE setting_key <> 'schema_version'"); err != nil {
		return err
	}
	// Part categories come from app_config (migration 194, run below); until then part.category
	// has no parent rows.
	if _, err := tx.Exec(ctx, "ALTER TABLE part DROP CONSTRAINT IF EXISTS fk_part_category"); err != nil {
		return err
	}

	deferred := map[string]map[string]bool{}
	for _, d := range deferredColumns {
		if deferred[d.table] == nil {
			deferred[d.table] = map[string]bool{}
		}
		deferred[d.table][d.column] = true
	}
	for _, t := range p.Tables {
		n, err := copyTable(ctx, tx, t, deferred[t.Name])
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		fmt.Fprintf(out, "  loaded %-24s %6d rows\n", t.Name, n)
	}
	if err := setDeferred(ctx, tx, p.Tables); err != nil {
		return fmt.Errorf("deferred FK columns: %w", err)
	}

	// The same statements the migrate runner applies, so the data ends up exactly as a
	// migrated database would: categories from app_config (194), Postgres text for the
	// canonical named_queries (28). Both are idempotent.
	for _, suffix := range []string{"_194_category_tables.sql", "_28_named_queries_postgres_text.sql"} {
		body, err := migrationSQL(suffix)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, body); err != nil {
			return fmt.Errorf("migration %s: %w", suffix, err)
		}
	}
	if err := resetSequences(ctx, tx, s, out); err != nil {
		return err
	}
	return verify(ctx, tx, p.Tables, out)
}

func copyTable(ctx context.Context, tx pgx.Tx, t *Table, deferred map[string]bool) (int64, error) {
	if len(t.Rows) == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for _, row := range t.Rows {
		rec := row
		if len(deferred) > 0 {
			rec = append([]string(nil), row...)
			for i, c := range t.Cols {
				if deferred[c] {
					rec[i] = NullMarker
				}
			}
		}
		w.Write(rec)
	}
	w.Flush()
	quoted := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		quoted[i] = pgx.Identifier{c}.Sanitize()
	}
	sql := fmt.Sprintf(`COPY %s (%s) FROM STDIN WITH (FORMAT csv, NULL '\N')`,
		pgx.Identifier{t.Name}.Sanitize(), strings.Join(quoted, ", "))
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, &buf, sql)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// setDeferred fills the cyclic-FK columns that were loaded NULL.
func setDeferred(ctx context.Context, tx pgx.Tx, tables []*Table) error {
	by := map[string]*Table{}
	for _, t := range tables {
		by[t.Name] = t
	}
	batch := &pgx.Batch{}
	for _, d := range deferredColumns {
		t := by[d.table]
		ci, idi := t.colIndex(d.column), t.colIndex("id")
		if ci < 0 || idi < 0 {
			continue
		}
		q := fmt.Sprintf("UPDATE %s SET %s = $1::int WHERE id = $2::int", pgx.Identifier{d.table}.Sanitize(), pgx.Identifier{d.column}.Sanitize())
		for _, row := range t.Rows {
			if row[ci] != NullMarker {
				batch.Queue(q, row[ci], row[idi])
			}
		}
	}
	res := tx.SendBatch(ctx, batch)
	defer res.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := res.Exec(); err != nil {
			return err
		}
	}
	return res.Close()
}

// resetSequences moves every identity sequence and po_number_seq past the loaded data.
func resetSequences(ctx context.Context, tx pgx.Tx, s *Schema, out io.Writer) error {
	for _, id := range s.Identity {
		if skipTables[id.table] || derivedTables[id.table] {
			continue
		}
		t, c := pgx.Identifier{id.table}.Sanitize(), pgx.Identifier{id.column}.Sanitize()
		var v int64
		err := tx.QueryRow(ctx, fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence($1, $2), GREATEST(COALESCE(MAX(%[2]s), 0), 1), COALESCE(MAX(%[2]s), 0) >= 1) FROM %[1]s`, t, c),
			t, id.column).Scan(&v)
		if err != nil {
			return fmt.Errorf("sequence for %s.%s: %w", id.table, id.column, err)
		}
	}
	var v int64
	err := tx.QueryRow(ctx, `
		SELECT setval('po_number_seq', GREATEST(COALESCE(MAX(substring(number from '^[0-9]+')::bigint), 0), 1),
		              COALESCE(MAX(substring(number from '^[0-9]+')::bigint), 0) >= 1)
		FROM purchase_order`).Scan(&v)
	if err != nil {
		return fmt.Errorf("po_number_seq: %w", err)
	}
	fmt.Fprintf(out, "  po_number_seq now %d (next PO number is %d)\n", v, v+1)
	return nil
}

// verify compares the loaded tables with the CSV and, through the manifest (checked
// against the CSV before the load), with the source server.
func verify(ctx context.Context, tx pgx.Tx, tables []*Table, out io.Writer) error {
	var bad []string
	for _, t := range tables {
		if t.Name == "app_config" {
			continue // carries target-owned rows; counted via the manifest check instead
		}
		sums := t.Sums()
		var cols []string
		sel := "SELECT count(*)"
		for _, c := range t.Cols {
			if _, ok := sums[c]; ok {
				cols = append(cols, c)
				sel += fmt.Sprintf(", COALESCE(SUM(%s), 0)::text", pgx.Identifier{c}.Sanitize())
			}
		}
		var n int
		dbSums := make([]string, len(cols))
		dest := []any{&n}
		for i := range dbSums {
			dest = append(dest, &dbSums[i])
		}
		if err := tx.QueryRow(ctx, sel+" FROM "+pgx.Identifier{t.Name}.Sanitize()).Scan(dest...); err != nil {
			return err
		}
		if n != len(t.Rows) {
			bad = append(bad, fmt.Sprintf("%s: %d rows in database, %d in CSV", t.Name, n, len(t.Rows)))
		}
		for i, c := range cols {
			got, _ := new(big.Rat).SetString(dbSums[i])
			if got == nil || got.Cmp(sums[c]) != 0 {
				bad = append(bad, fmt.Sprintf("%s.%s: database sum %s, CSV sum %s", t.Name, c, dbSums[i], sums[c].FloatString(8)))
			}
		}
		fmt.Fprintf(out, "  verified %-23s %6d rows, %d numeric sums\n", t.Name, n, len(cols))
	}
	if len(bad) > 0 {
		return problemsError("verification failed", bad)
	}
	return nil
}

// migrationSQL returns the embedded migration file whose name ends in suffix.
func migrationSQL(suffix string) (string, error) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			b, err := migrations.FS.ReadFile(e.Name())
			return string(b), err
		}
	}
	return "", fmt.Errorf("embedded migration %s not found", suffix)
}

// ReadManifest reads manifest.csv (table,metric,value): metric is "rows" or "sum:<column>",
// computed on the source server by the export script.
func ReadManifest(path string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w (the export script writes manifest.csv next to the table CSVs)", err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("manifest.csv: %w", err)
	}
	m := map[string]map[string]string{}
	for i, rec := range recs {
		if i == 0 && len(rec) > 0 {
			rec[0] = strings.TrimPrefix(rec[0], "\xef\xbb\xbf")
		}
		if i == 0 || len(rec) != 3 {
			continue
		}
		if m[rec[0]] == nil {
			m[rec[0]] = map[string]string{}
		}
		m[rec[0]][rec[1]] = rec[2]
	}
	return m, nil
}

// CheckManifest compares the parsed CSVs with the source server's own counts and sums,
// which catches a truncated or mangled export before anything is loaded.
func CheckManifest(tables []*Table, m map[string]map[string]string) error {
	var bad []string
	loaded := map[string]bool{}
	for _, t := range tables {
		loaded[t.Name] = true
	}
	for name := range m { // a source table with no target table would otherwise be dropped silently
		if !loaded[name] && !skipTables[name] {
			bad = append(bad, fmt.Sprintf("%s: in manifest.csv but the target has no such table (add it to skipTables in spec.go if it is meant to be dropped)", name))
		}
	}
	for _, t := range tables {
		want := m[t.Name]
		if want == nil {
			bad = append(bad, t.Name+": not in manifest.csv")
			continue
		}
		if want["rows"] != fmt.Sprint(t.SourceRows) {
			bad = append(bad, fmt.Sprintf("%s: manifest says %s rows, CSV has %d", t.Name, want["rows"], t.SourceRows))
		}
		if t.Name == "app_config" {
			continue // sums are over numeric columns, which app_config has none of
		}
		for c, sum := range t.Sums() {
			src, ok := new(big.Rat).SetString(want["sum:"+c])
			if !ok {
				bad = append(bad, fmt.Sprintf("%s.%s: no sum in manifest.csv", t.Name, c))
			} else if src.Cmp(sum) != 0 {
				bad = append(bad, fmt.Sprintf("%s.%s: manifest sum %s, CSV sum %s", t.Name, c, want["sum:"+c], sum.FloatString(8)))
			}
		}
	}
	if len(bad) > 0 {
		return problemsError("CSV export does not match the source manifest", bad)
	}
	return nil
}
