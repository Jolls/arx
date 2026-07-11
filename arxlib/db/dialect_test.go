package db

import (
	"strings"
	"testing"
)

func TestSQLServerDialectIsIdentityAndTSQL(t *testing.T) {
	d := NewSQLServerDialect()

	if d.Name() != "sqlserver" {
		t.Errorf("Name: got %q", d.Name())
	}
	// Tier-1 rewrite is identity on SQL Server.
	q := "SELECT id FROM part WHERE x = @p1 AND created = GETDATE()"
	if got := d.Rewrite(q); got != q {
		t.Errorf("Rewrite should be identity on SQL Server:\n got %q\nwant %q", got, q)
	}
	if got := d.TryCastInt("serial_number"); got != "TRY_CAST(serial_number AS INT)" {
		t.Errorf("TryCastInt: got %q", got)
	}
	if got := d.MonthStartExpr(); got != "DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)" {
		t.Errorf("MonthStartExpr: got %q", got)
	}
	if got := d.TopClause("@p2"); got != "TOP (@p2) " {
		t.Errorf("TopClause: got %q", got)
	}
	if got := d.LimitClause("@p2"); got != "" {
		t.Errorf("LimitClause should be empty on SQL Server: got %q", got)
	}
}

func TestSQLServerDialectInsertSelectReturningID(t *testing.T) {
	d := NewSQLServerDialect()

	// trigger table -> SCOPE_IDENTITY batch form
	gotT := d.InsertSelectReturningID("purchase_order", "a, b",
		"SELECT @p1, @p2 FROM po WHERE id = @p3", true)
	wantT := "INSERT INTO purchase_order (a, b)\nSELECT @p1, @p2 FROM po WHERE id = @p3;\nSELECT CAST(SCOPE_IDENTITY() AS INT)"
	if gotT != wantT {
		t.Errorf("trigger form:\n got %q\nwant %q", gotT, wantT)
	}

	// non-trigger table -> OUTPUT INSERTED.id form
	gotF := d.InsertSelectReturningID("part", "a, b", "SELECT x, y FROM src", false)
	wantF := "INSERT INTO part (a, b) OUTPUT INSERTED.id\nSELECT x, y FROM src"
	if gotF != wantF {
		t.Errorf("non-trigger form:\n got %q\nwant %q", gotF, wantF)
	}
}

func TestPostgresDialectHelpers(t *testing.T) {
	d := NewPostgresDialect()

	if d.Name() != "postgres" {
		t.Errorf("Name: got %q", d.Name())
	}
	if got := d.TryCastInt("serial_number"); got != "CASE WHEN serial_number ~ '^[0-9]+$' THEN CAST(serial_number AS INTEGER) END" {
		t.Errorf("TryCastInt: got %q", got)
	}
	if got := d.MonthStartExpr(); got != "date_trunc('month', CURRENT_DATE)::date" {
		t.Errorf("MonthStartExpr: got %q", got)
	}
	// Postgres has no TOP; the row cap rides on LIMIT instead.
	if got := d.TopClause("@p2"); got != "" {
		t.Errorf("TopClause should be empty on Postgres: got %q", got)
	}
	if got := d.LimitClause("@p2"); got != " LIMIT @p2" {
		t.Errorf("LimitClause: got %q", got)
	}
}

func TestPostgresRewrite(t *testing.T) {
	d := NewPostgresDialect()

	// @pN -> $N (including multi-digit), GETDATE() -> CURRENT_TIMESTAMP.
	in := "SELECT id FROM part WHERE a = @p1 AND b = @p12 AND created = GETDATE()"
	want := "SELECT id FROM part WHERE a = $1 AND b = $12 AND created = CURRENT_TIMESTAMP"
	if got := d.Rewrite(in); got != want {
		t.Errorf("Rewrite:\n got %q\nwant %q", got, want)
	}
	// Placeholder rewrite reaches the SQL the Tier-2 helpers emit with @pN.
	if got := d.Rewrite(d.UpsertAppConfig("app_config")); !strings.Contains(got, "VALUES ($1, $2)") {
		t.Errorf("UpsertAppConfig after Rewrite should carry $-placeholders: %q", got)
	}
}

func TestPostgresUpsertAppConfig(t *testing.T) {
	d := NewPostgresDialect()

	got := d.UpsertAppConfig("app_config")
	want := "INSERT INTO app_config (setting_key, setting_value) VALUES (@p1, @p2)\n" +
		"ON CONFLICT (setting_key) DO UPDATE SET setting_value = EXCLUDED.setting_value, updated_at = CURRENT_TIMESTAMP"
	if got != want {
		t.Errorf("UpsertAppConfig:\n got %q\nwant %q", got, want)
	}
}

func TestPostgresInsertReturningID(t *testing.T) {
	d := NewPostgresDialect()

	// RETURNING id works with or without triggers, so both flags yield the same SQL.
	want := "INSERT INTO part (a, b) VALUES (@p1, @p2) RETURNING id"
	if got := d.InsertReturningID("part", "a, b", "@p1, @p2", false); got != want {
		t.Errorf("non-trigger:\n got %q\nwant %q", got, want)
	}
	if got := d.InsertReturningID("part", "a, b", "@p1, @p2", true); got != want {
		t.Errorf("trigger form should match non-trigger:\n got %q\nwant %q", got, want)
	}
}

func TestPostgresInsertSelectReturningID(t *testing.T) {
	d := NewPostgresDialect()

	want := "INSERT INTO purchase_order (a, b)\nSELECT @p1, @p2 FROM po WHERE id = @p3\nRETURNING id"
	if got := d.InsertSelectReturningID("purchase_order", "a, b", "SELECT @p1, @p2 FROM po WHERE id = @p3", true); got != want {
		t.Errorf("InsertSelectReturningID:\n got %q\nwant %q", got, want)
	}
}
