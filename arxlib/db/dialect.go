package db

import "fmt"

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
	TopClause(ph string) string
	LimitClause(ph string) string
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

func (sqlServerDialect) TopClause(ph string) string { return "TOP (" + ph + ") " }
func (sqlServerDialect) LimitClause(string) string  { return "" }
