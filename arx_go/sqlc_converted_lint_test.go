package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSQLCConvertedFilesHaveNoRawSQL guards the sqlc conversion (#190): no
// non-test file in package main may call cfg.*Table() or run raw SQL; queries
// live in internal/<domain>/<domain>.sql. Deny by default: a new file is
// checked without being listed.
func TestSQLCConvertedFilesHaveNoRawSQL(t *testing.T) {
	raw := regexp.MustCompile(`Table\(\)|\bh\.(queryContext|queryRowContext|execContext)\(|\.(QueryContext|QueryRowContext|ExecContext)\(`)
	// handlers.go defines the logging DB wrappers and the handlerDB/txLogger DBTX adapters.
	exemptFiles := map[string]bool{"handlers.go": true}
	// execQuery runs admin-authored named-query SQL, which can't be a static sqlc query (#250).
	allowed := map[string]string{"named_query.go": "rows, err := h.queryContext(ctx, sqlText, args...)"}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files found (err %v)", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || exemptFiles[f] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if a, ok := allowed[f]; ok && strings.TrimSpace(line) == a {
				continue
			}
			if m := raw.FindString(line); m != "" {
				t.Errorf("%s:%d: raw SQL call %q; add a query to internal/<domain>/<domain>.sql instead", f, i+1, m)
			}
		}
	}
}
