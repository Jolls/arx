package settings

import (
	"context"
	"regexp"
	"testing"
)

// QueryTable interpolates the table name into SQL, so anything off the allowlist must be refused
// before the db is touched (the nil db here would panic if it were reached).
func TestQueryTableRejectsUnlistedNames(t *testing.T) {
	s := New(nil)
	for _, name := range []string{"", "pg_shadow", "part; DROP TABLE part", "part --", "PART", "schema_migrations", "users "} {
		if rows, err := s.QueryTable(context.Background(), name); err == nil {
			rows.Close()
			t.Errorf("QueryTable(%q) accepted a name that is not a backup table", name)
		}
	}
}

func TestBackupTablesAreBareIdentifiers(t *testing.T) {
	ident := regexp.MustCompile(`^[a-z][a-z_]*$`)
	seen := map[string]bool{}
	for _, tbl := range append([]string{BackupUsers, BackupAppConfig}, BackupTables...) {
		if !ident.MatchString(tbl) {
			t.Errorf("backup table %q is not a bare lowercase identifier", tbl)
		}
		if seen[tbl] {
			t.Errorf("backup table %q listed twice", tbl)
		}
		seen[tbl] = true
	}
}
