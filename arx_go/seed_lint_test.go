package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestSeedNamedQueriesFixedIDs guards #213: seed_test_data.sql's named_queries
// rows carry fixed ids, like every other seed block, at or below the table's
// setval floor, so the reference set is identical on every (re-)run.
func TestSeedNamedQueriesFixedIDs(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "SQL", "postgres", "seed_test_data.sql"))
	if err != nil {
		t.Fatal(err)
	}
	seed := string(b)

	m := regexp.MustCompile(`setval\(pg_get_serial_sequence\('named_queries', 'id'\), (\d+)\)`).FindStringSubmatch(seed)
	if m == nil {
		t.Fatal("no setval for named_queries in seed_test_data.sql")
	}
	floor, _ := strconv.Atoi(m[1])

	start := strings.Index(seed, "INSERT INTO named_queries (")
	if start < 0 {
		t.Fatal("no INSERT INTO named_queries in seed_test_data.sql")
	}
	lines := strings.Split(seed[start:], "\n")
	if !strings.HasPrefix(lines[0], "INSERT INTO named_queries (id, ") {
		t.Errorf("named_queries INSERT must list id first: %q", lines[0])
	}

	rowID := regexp.MustCompile(`^\((\d+), '`)
	seen := map[int]bool{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "(") { // row start; continuation lines start with '
			rm := rowID.FindStringSubmatch(line)
			if rm == nil {
				t.Errorf("named_queries seed row without a fixed id: %q", line)
			} else {
				id, _ := strconv.Atoi(rm[1])
				if id < 1 || id > floor {
					t.Errorf("named_queries seed id %d outside 1-%d (setval floor)", id, floor)
				}
				if seen[id] {
					t.Errorf("duplicate named_queries seed id %d", id)
				}
				seen[id] = true
			}
		}
		if strings.HasSuffix(line, ");") {
			break
		}
	}
	if len(seen) == 0 {
		t.Error("no named_queries seed rows with fixed ids found")
	}
}
