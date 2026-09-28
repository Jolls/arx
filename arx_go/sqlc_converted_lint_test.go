package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestSQLCConvertedFilesHaveNoRawSQL guards the sqlc conversion (#190): a
// handler file whose domain moved to internal/<domain> must not slide back to
// cfg.*Table() or raw query calls. Append each file as its domain converts.
func TestSQLCConvertedFilesHaveNoRawSQL(t *testing.T) {
	convertedFiles := []string{"contacts.go", "categories.go", "mfg_parts.go", "sourcing.go", "attachments.go"}
	raw := regexp.MustCompile(`Table\(\)|\bh\.(queryContext|queryRowContext|execContext)\(|\.(QueryContext|QueryRowContext|ExecContext)\(`)
	for _, f := range convertedFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := raw.FindString(line); m != "" {
				t.Errorf("%s:%d: raw SQL call %q in a sqlc-converted file", f, i+1, m)
			}
		}
	}
}
