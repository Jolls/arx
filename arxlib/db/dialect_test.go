package db

import "testing"

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
