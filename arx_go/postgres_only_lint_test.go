package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sqlServerSyntax matches T-SQL constructs that Postgres rejects. With the
// runtime Rewrite pass gone (#29), any of these in Go source is a live bug.
var sqlServerSyntax = regexp.MustCompile(`@p\d|GETDATE\(|OUTPUT INSERTED|SCOPE_IDENTITY|NEXT VALUE FOR|CONTEXT_INFO|\bTOP \(|TRY_CAST|DATEFROMPARTS|MERGE INTO|ISNULL\(|FETCH NEXT`)

// TestNoSQLServerSQLInGo fails on T-SQL syntax in Go source under arx_go/ and
// internal/, tests included (they run the same wrappers). This file is exempt:
// it names the patterns it forbids.
func TestNoSQLServerSQLInGo(t *testing.T) {
	var files []string
	for _, pat := range []string{"*.go", "*/*.go", "cmd/*/*.go", "../internal/*/*.go"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		if f == "postgres_only_lint_test.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if loc := sqlServerSyntax.FindString(line); loc != "" {
				t.Errorf("%s:%d: T-SQL %q", f, i+1, loc)
			}
		}
	}
}
