package settings

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every table in the reference DDL must be in the backup (#267), or a restore silently loses it;
// users and app_config are exported separately and schema_migrations is the migration ledger.
func TestBackupCoversEveryTable(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "SQL", "postgres", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no DDL files found (err %v)", err)
	}
	create := regexp.MustCompile(`(?im)^CREATE TABLE (?:IF NOT EXISTS )?([a-z_]+)`)
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "seed") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range create.FindAllStringSubmatch(string(b), -1) {
			tbl := m[1]
			if tbl == BackupUsers || tbl == BackupAppConfig || tbl == "schema_migrations" || slices.Contains(BackupTables, tbl) {
				continue
			}
			t.Errorf("table %q (%s) is not in the backup: add it to BackupTables", tbl, filepath.Base(f))
		}
	}
}

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
