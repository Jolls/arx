package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDeadSchemaNamesRemoved guards the dead-column cleanup (#31, architecture
// review item 2): no Go/template/JS source or Postgres reference DDL may name a
// dropped or renamed legacy column/table. Migrations are exempt (they must name
// the old columns to drop/rename them).
func TestDeadSchemaNamesRemoved(t *testing.T) {
	deadCol := regexp.MustCompile(`(?i)\b(suweb|sucontact1|sunotes|sunumoflnks|sunumofpos|susuppliercode|is_lot_tracked)\b`)
	deadTable := regexp.MustCompile(`(?i)\b(FROM|INTO|TABLE)\s+(logs|release_notes)\b`)

	check := func(path string, re *regexp.Regexp) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := re.FindString(line); m != "" {
				t.Errorf("%s:%d references removed schema name %q", path, i+1, m)
			}
		}
	}

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
			return err
		}
		switch filepath.Ext(path) {
		case ".go", ".html", ".js":
			check(path, deadCol)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sqlFiles, err := filepath.Glob(filepath.Join("..", "SQL", "postgres", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sqlFiles {
		check(f, deadCol)
		check(f, deadTable)
	}
	for _, f := range []string{"logs.sql", "release_notes.sql"} {
		if _, err := os.Stat(filepath.Join("..", "SQL", "postgres", f)); err == nil {
			t.Errorf("SQL/postgres/%s still exists", f)
		}
	}
}
