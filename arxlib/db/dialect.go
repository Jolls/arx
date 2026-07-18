package db

import (
	"fmt"
	"regexp"
	"strings"
)

// Dialect encapsulates the SQL constructs that differ between database engines.
// During the SQL Server -> Postgres migration (issue #625) a single SQL Server
// implementation exists; the Postgres one is added in Phase 2, and the whole
// interface collapses to Postgres-only at cutover.
type Dialect interface {
	Name() string
	Rewrite(query string) string
	TryCastInt(expr string) string
	MonthStartExpr() string
	UpsertAppConfig(table string) string
	InsertReturningID(table, columnList, valuesList string, hasTrigger bool) string
	// InsertSelectReturningID is InsertReturningID's INSERT ... SELECT sibling:
	// it inserts rows produced by selectBody (a full "SELECT ... FROM ... WHERE ..."
	// clause, already comma-formatted, using @pN placeholders and any inline
	// literals) into table's columnList and returns the new integer id to Scan.
	// hasTrigger selects the SCOPE_IDENTITY batch form, matching InsertReturningID.
	InsertSelectReturningID(table, columnList, selectBody string, hasTrigger bool) string
	TopClause(ph string) string
	LimitClause(ph string) string
	// SetAuditUser returns the statement, and its bound argument, that records
	// the acting username for the current transaction so the
	// trg_form_row_history trigger can attribute the snapshot. SQL Server
	// stashes it in CONTEXT_INFO (a varbinary, hence the []byte arg); Postgres
	// sets a transaction-local session GUC the trigger reads via
	// current_setting('arx.username').
	SetAuditUser(username string) (query string, arg any)
	// BoolLiteral renders a boolean constant for a BIT (SQL Server) / BOOLEAN
	// (Postgres) column, spliced into WHERE comparisons, SET assignments, INSERT
	// VALUES and CASE expressions. SQL Server BIT columns take integer literals
	// (1/0); Postgres BOOLEAN columns reject those and need TRUE/FALSE.
	BoolLiteral(v bool) string
	// ToggleBoolExpr renders the expression that flips a boolean column, for
	// `SET col = <toggle>`. SQL Server uses the arithmetic `1 - col` idiom on a
	// BIT column; Postgres uses `NOT col` on a BOOLEAN column.
	ToggleBoolExpr(column string) string
	// NextSequenceValueExpr draws the next value from a named sequence, used for
	// the PO-number sequence. SQL Server uses `NEXT VALUE FOR <seq>`; Postgres
	// uses `nextval('<seq>')`.
	NextSequenceValueExpr(seqName string) string
}

type sqlServerDialect struct{}

// NewSQLServerDialect returns the SQL Server dialect. Every method reproduces
// the SQL the app emits today, so introducing the seam changes no behavior.
func NewSQLServerDialect() Dialect { return sqlServerDialect{} }

func (sqlServerDialect) Name() string { return "sqlserver" }

// Rewrite is identity: SQL Server keeps @pN placeholders and GETDATE().
func (sqlServerDialect) Rewrite(query string) string { return query }

func (sqlServerDialect) TryCastInt(expr string) string {
	return fmt.Sprintf("TRY_CAST(%s AS INT)", expr)
}

func (sqlServerDialect) MonthStartExpr() string {
	return "DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)"
}

func (sqlServerDialect) UpsertAppConfig(table string) string {
	return "MERGE INTO " + table + " AS t\n" +
		"USING (SELECT @p1 AS k, @p2 AS v) AS s ON t.setting_key = s.k\n" +
		"WHEN MATCHED THEN UPDATE SET t.setting_value = s.v, t.updated_at = GETDATE()\n" +
		"WHEN NOT MATCHED THEN INSERT (setting_key, setting_value) VALUES (s.k, s.v);"
}

func (sqlServerDialect) InsertReturningID(table, columnList, valuesList string, hasTrigger bool) string {
	if hasTrigger {
		// OUTPUT INSERTED is blocked on tables with triggers; batch INSERT with
		// SCOPE_IDENTITY() so both run in the same scope.
		return fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s);\nSELECT CAST(SCOPE_IDENTITY() AS INT)",
			table, columnList, valuesList)
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) OUTPUT INSERTED.id VALUES (%s)",
		table, columnList, valuesList)
}

func (sqlServerDialect) InsertSelectReturningID(table, columnList, selectBody string, hasTrigger bool) string {
	if hasTrigger {
		// OUTPUT INSERTED is blocked on trigger tables; SCOPE_IDENTITY batch form.
		return fmt.Sprintf(
			"INSERT INTO %s (%s)\n%s;\nSELECT CAST(SCOPE_IDENTITY() AS INT)",
			table, columnList, selectBody)
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) OUTPUT INSERTED.id\n%s",
		table, columnList, selectBody)
}

func (sqlServerDialect) TopClause(ph string) string { return "TOP (" + ph + ") " }
func (sqlServerDialect) LimitClause(string) string  { return "" }

func (sqlServerDialect) SetAuditUser(username string) (string, any) {
	return "SET CONTEXT_INFO @p1", []byte(username)
}

func (sqlServerDialect) BoolLiteral(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func (sqlServerDialect) ToggleBoolExpr(column string) string { return "1 - " + column }

// NextSequenceValueExpr returns the T-SQL sequence draw. The PO-number sequence
// is the fixed schema object dbo.PO_Number_Seq, so the returned expression is a
// verbatim literal (byte-for-byte identical to the prior inline SQL); the
// logical seqName the caller passes is only meaningful to the Postgres dialect.
func (sqlServerDialect) NextSequenceValueExpr(string) string {
	return "NEXT VALUE FOR dbo.PO_Number_Seq"
}

// pgPlaceholder matches the @pN parameter markers the app emits (go-mssqldb's
// numbering convention) so the Postgres dialect can rewrite them to $N.
var pgPlaceholder = regexp.MustCompile(`@p(\d+)`)

type postgresDialect struct{}

// NewPostgresDialect returns the Postgres dialect (issue #625, Phase 2). It is
// the counterpart to NewSQLServerDialect: the same Handler call sites route
// through these helpers, which emit Postgres-native SQL.
func NewPostgresDialect() Dialect { return postgresDialect{} }

func (postgresDialect) Name() string { return "postgres" }

// Rewrite applies the two Tier-1 textual transforms for Postgres: @pN -> $N
// placeholders and GETDATE() -> CURRENT_TIMESTAMP. Both are also applied to the
// SQL produced by the Tier-2 helpers below (every query passes through here in
// the Handler wrappers), so those helpers may keep emitting @pN/GETDATE().
func (postgresDialect) Rewrite(query string) string {
	query = strings.ReplaceAll(query, "GETDATE()", "CURRENT_TIMESTAMP")
	return pgPlaceholder.ReplaceAllString(query, "$$${1}")
}

// TryCastInt is Postgres's TRY_CAST(expr AS INT) equivalent: Postgres CAST
// raises on a non-numeric value, so guard with a digits-only regex and return
// NULL when it would not parse. Matches TRY_CAST's NULL-on-failure semantics for
// the serial-number ordering use. (A value exceeding INTEGER range would still
// raise, as it also does under a 32-bit SQL Server INT; serial numbers stay well
// inside that range.)
func (postgresDialect) TryCastInt(expr string) string {
	return fmt.Sprintf("CASE WHEN %s ~ '^[0-9]+$' THEN CAST(%s AS INTEGER) END", expr, expr)
}

// MonthStartExpr returns the first day of the current month as a date, matching
// the SQL Server DATEFROMPARTS form used in the received-this-month comparison.
func (postgresDialect) MonthStartExpr() string {
	return "date_trunc('month', CURRENT_DATE)::date"
}

func (postgresDialect) UpsertAppConfig(table string) string {
	return "INSERT INTO " + table + " (setting_key, setting_value) VALUES (@p1, @p2)\n" +
		"ON CONFLICT (setting_key) DO UPDATE SET setting_value = EXCLUDED.setting_value, updated_at = CURRENT_TIMESTAMP"
}

// InsertReturningID uses RETURNING id, which works on Postgres regardless of
// triggers, so the hasTrigger branch the SQL Server form needs is unnecessary.
func (postgresDialect) InsertReturningID(table, columnList, valuesList string, _ bool) string {
	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		table, columnList, valuesList)
}

func (postgresDialect) InsertSelectReturningID(table, columnList, selectBody string, _ bool) string {
	return fmt.Sprintf(
		"INSERT INTO %s (%s)\n%s\nRETURNING id",
		table, columnList, selectBody)
}

// Postgres paginates with LIMIT (no TOP clause), so TopClause is empty and
// LimitClause carries the row cap. The leading space lets it append directly
// after an ORDER BY clause with no separator in the query template.
func (postgresDialect) TopClause(string) string      { return "" }
func (postgresDialect) LimitClause(ph string) string { return " LIMIT " + ph }

// SetAuditUser sets a transaction-local session GUC (is_local = true, matching
// CONTEXT_INFO's scope within the audit transaction) that
// trg_form_row_history reads via current_setting('arx.username'). The
// @p1 placeholder is rewritten to $1 by Rewrite in the tx wrapper.
func (postgresDialect) SetAuditUser(username string) (string, any) {
	return "SELECT set_config('arx.username', @p1, true)", username
}

func (postgresDialect) BoolLiteral(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}

func (postgresDialect) ToggleBoolExpr(column string) string { return "NOT " + column }

// NextSequenceValueExpr returns the Postgres sequence draw for the bare,
// lowercase sequence name created by SQL/postgres/purchase_order.sql.
func (postgresDialect) NextSequenceValueExpr(seqName string) string {
	return fmt.Sprintf("nextval('%s')", seqName)
}
