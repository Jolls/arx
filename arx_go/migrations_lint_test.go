package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	migrationName   = regexp.MustCompile(`^(\d{14})_\d+_[a-z0-9_]+\.sql$`)
	migrationGuard  = regexp.MustCompile(`(?i)FROM\s+schema_migrations\s+WHERE\s+version_id\s*=\s*(\d+)`)
	migrationInsert = regexp.MustCompile(`(?i)INSERT\s+INTO\s+schema_migrations\s*\(version_id,\s*is_applied\)\s*SELECT\s+(\d+),\s*TRUE`)
)

// TestMigrationsSelfRegister guards against a migration that changes the schema
// but never records itself in schema_migrations (#48, cf. #40).
func TestMigrationsSelfRegister(t *testing.T) {
	dir := filepath.Join("..", "SQL", "postgres", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("%s: name must be YYYYMMDDHHMMSS_<issue>_<description>.sql", name)
			continue
		}
		version := m[1]
		if prev, dup := seen[version]; dup {
			t.Errorf("%s: version %s already used by %s", name, version, prev)
		}
		seen[version] = name

		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for label, re := range map[string]*regexp.Regexp{"NOT EXISTS guard": migrationGuard, "INSERT": migrationInsert} {
			g := re.FindStringSubmatch(string(body))
			if g == nil {
				t.Errorf("%s: missing schema_migrations %s", name, label)
			} else if g[1] != version {
				t.Errorf("%s: %s registers version %s, filename says %s", name, label, g[1], version)
			}
		}
	}
}
