package datamigrate

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// scratchDSNEnv names a throwaway Postgres database built with SQL/build_schema.sh,
// e.g. a docker container. The test TRUNCATES it, so the database name must start with
// "arxscratch"; ArxDev and ArxProd can never match.
const scratchDSNEnv = "ARX_DATAMIGRATE_SCRATCH_DSN"

func TestLoadFixtureIntoScratchDB(t *testing.T) {
	dsn := os.Getenv(scratchDSNEnv)
	if dsn == "" {
		t.Skipf("%s not set", scratchDSNEnv)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var db string
	conn.QueryRow(ctx, "SELECT current_database()").Scan(&db)
	if !strings.HasPrefix(strings.ToLower(db), "arxscratch") {
		t.Fatalf("refusing to truncate database %q: name must start with arxscratch", db)
	}

	args := []string{"--csv-dir", "testdata", "--target-db", db, "--confirm-truncate", db}
	env := func(k string) string {
		if k == DSNEnv {
			return dsn
		}
		return ""
	}
	// A marker the dry run must leave alone and the real load must replace.
	if _, err := conn.Exec(ctx, "INSERT INTO uom (uom_id, abbreviation, display_name, unit_type) VALUES (777, 'mk', 'marker', 'count') ON CONFLICT DO NOTHING"); err != nil {
		t.Fatal(err)
	}
	seqs := func() string {
		var s string
		if err := conn.QueryRow(ctx, "SELECT string_agg(sequencename || '=' || COALESCE(last_value::text, 'null'), ',' ORDER BY sequencename) FROM pg_sequences WHERE schemaname = current_schema()").Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	seqsBefore := seqs()
	var out bytes.Buffer
	if err := Run(ctx, append(args, "--dry-run"), env, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if after := seqs(); after != seqsBefore {
		t.Fatalf("dry run moved sequences:\nbefore %s\nafter  %s", seqsBefore, after)
	}
	var marker int
	conn.QueryRow(ctx, "SELECT count(*) FROM uom WHERE uom_id = 777").Scan(&marker)
	if marker != 1 {
		t.Fatal("dry run changed the database")
	}

	// Twice: the second run must truncate and reload, leaving no leftovers.
	for i := 0; i < 2; i++ {
		out.Reset()
		if err := Run(ctx, args, env, &out); err != nil {
			t.Fatalf("load %d: %v\n%s", i+1, err, out.String())
		}
	}

	q := func(sql string) string {
		var s string
		if err := conn.QueryRow(ctx, sql).Scan(&s); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return s
	}
	checks := []struct{ name, sql, want string }{
		{"marker from before the load is gone", "SELECT count(*)::text FROM uom WHERE uom_id = 777", "0"},
		{"one company, no leftovers", "SELECT count(*)::text FROM company", "1"},
		{"two parts", "SELECT count(*)::text FROM part", "2"},
		{"secret config not carried", "SELECT count(*)::text FROM app_config WHERE setting_key LIKE 'secret_%'", "0"},
		{"schema_version stays the target's", "SELECT setting_value FROM app_config WHERE setting_key = 'schema_version'", "12"},
		{"part categories imported from app_config", "SELECT string_agg(code, ',') FROM part_category", "BUY"},
		{"part '' category became NULL", "SELECT (category IS NULL)::text FROM part WHERE id = 2", "true"},
		{"deferred FK set", "SELECT price_id::text || '/' || primary_attachment_id::text FROM part WHERE id = 1", "1/1"},
		{"FK_part_category restored", "SELECT count(*)::text FROM pg_constraint WHERE conname = 'fk_part_category'", "1"},
		{"desktop clock read as Pacific", "SELECT to_char(date_modified AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI') FROM company", "2026-01-15 18:00"},
		{"server clock read as UTC", "SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI') FROM users", "2026-01-15 10:00"},
		{"named_queries got Postgres text", "SELECT (sql NOT LIKE '%TOP%')::text FROM named_queries", "true"},
		{"PO sequence past loaded max", "SELECT nextval('po_number_seq')::text", "4322"},
		{"identity sequence past loaded max", "SELECT nextval(pg_get_serial_sequence('part', 'id'))::text", "3"},
		{"numerics exact", "SELECT total_cost::text FROM purchase_order", "12.50000000"},
		{"CRLF inside quoted text intact", "SELECT notes FROM company", "line1\r\nline2, \"quoted\""},
	}
	for _, c := range checks {
		if got := q(c.sql); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
