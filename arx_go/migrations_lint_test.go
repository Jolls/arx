package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var migrationName = regexp.MustCompile(`^(\d{14})_\d+_[a-z0-9_]+\.sql$`)

// migrationForbidden is what the goose runner (#91) now owns: the per-file
// transaction, the ledger row, and the target check.
var migrationForbidden = []struct {
	label string
	re    *regexp.Regexp
}{
	{"BEGIN/COMMIT/ROLLBACK (the runner wraps each file in a transaction)", regexp.MustCompile(`(?im)^\s*(BEGIN|COMMIT|ROLLBACK)\s*;`)},
	{"INSERT INTO schema_migrations (the runner records the version)", regexp.MustCompile(`(?i)INSERT\s+INTO\s+schema_migrations`)},
	{"current_database() guard (the runner confirms the target)", regexp.MustCompile(`(?i)current_database\s*\(`)},
}

const migrationDir = "../SQL/migrations"

// migrationProblems checks a migration body against the goose format: header
// comments, then one Up section that is a single StatementBegin/End block.
func migrationProblems(body string) []string {
	var lines []string
	annotations := 0
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "-- +goose") {
			annotations++
		} else if strings.HasPrefix(l, "--") {
			continue
		}
		lines = append(lines, l)
	}
	var problems []string
	if len(lines) < 3 || lines[0] != "-- +goose Up" || lines[1] != "-- +goose StatementBegin" ||
		lines[len(lines)-1] != "-- +goose StatementEnd" || annotations != 3 {
		problems = append(problems, "must be: header comments, -- +goose Up, -- +goose StatementBegin, body, -- +goose StatementEnd (no other +goose annotations)")
	}
	for _, f := range migrationForbidden {
		if f.re.MatchString(body) {
			problems = append(problems, "contains "+f.label)
		}
	}
	return problems
}

// manifestProblems compares checksums.txt (sha256sum --text format) with the
// migration files, so a committed migration can't be edited silently (#40, #91).
func manifestProblems(manifest string, files map[string][]byte) []string {
	var problems []string
	want := map[string]string{}
	for _, l := range strings.Split(manifest, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			problems = append(problems, fmt.Sprintf("checksums.txt: malformed line %q", l))
			continue
		}
		want[strings.TrimPrefix(f[1], "*")] = f[0]
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		got := hex.EncodeToString(sum[:])
		w, ok := want[n]
		delete(want, n)
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: no entry in checksums.txt; add:\n%s  %s", n, got, n))
		case w != got:
			problems = append(problems, fmt.Sprintf("%s: sha256 %s, checksums.txt says %s; committed migrations are immutable, revert and add a new migration", n, got, w))
		}
	}
	for n := range want {
		problems = append(problems, fmt.Sprintf("checksums.txt lists %s, which does not exist", n))
	}
	return problems
}

// TestMigrationsGooseFormat enforces the goose migration format (#91): the
// runner owns each file's transaction and its schema_migrations row.
func TestMigrationsGooseFormat(t *testing.T) {
	entries, err := os.ReadDir(migrationDir)
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

		body, err := os.ReadFile(filepath.Join(migrationDir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range migrationProblems(string(body)) {
			t.Errorf("%s: %s", name, p)
		}
	}
}

func TestMigrationChecksums(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join(migrationDir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(migrationDir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(p)] = b
	}
	for _, p := range manifestProblems(string(manifest), files) {
		t.Error(p)
	}
}

func TestMigrationProblems(t *testing.T) {
	const valid = "-- header\n-- +goose Up\n-- +goose StatementBegin\nDO $$ BEGIN\n  PERFORM 1;\nEND $$;\nSELECT 1;\n-- +goose StatementEnd\n"
	if p := migrationProblems(valid); len(p) != 0 {
		t.Errorf("valid body: %q", p)
	}
	for name, body := range map[string]string{
		"missing Up":            "-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n",
		"SQL before Up":         "SELECT 1;\n-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n",
		"missing StatementEnd":  "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n",
		"Down section":          "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n-- +goose Down\nSELECT 1;\n",
		"NO TRANSACTION":        "-- +goose NO TRANSACTION\n-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n",
		"BEGIN;":                "-- +goose Up\n-- +goose StatementBegin\nBEGIN;\nSELECT 1;\n-- +goose StatementEnd\n",
		"COMMIT;":               "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\nCOMMIT;\n-- +goose StatementEnd\n",
		"self-register INSERT":  "-- +goose Up\n-- +goose StatementBegin\nINSERT INTO schema_migrations (version_id, is_applied) SELECT 1, TRUE;\n-- +goose StatementEnd\n",
		"current_database guard": "-- +goose Up\n-- +goose StatementBegin\nDO $$ BEGIN IF current_database() <> 'x' THEN RAISE EXCEPTION 'no'; END IF; END $$;\n-- +goose StatementEnd\n",
	} {
		if len(migrationProblems(body)) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
}

func TestManifestProblems(t *testing.T) {
	files := map[string][]byte{"a.sql": []byte("SELECT 1;\n")}
	sum := sha256.Sum256(files["a.sql"])
	h := hex.EncodeToString(sum[:])

	for name, c := range map[string]struct {
		manifest string
		files    map[string][]byte
		want     string
	}{
		"match":         {h + "  a.sql\n", files, ""},
		"binary marker": {h + " *a.sql\n", files, ""},
		"changed byte":  {h + "  a.sql\n", map[string][]byte{"a.sql": []byte("SELECT 2;\n")}, "immutable"},
		"missing entry": {"", files, "no entry"},
		"stale entry":   {h + "  a.sql\n" + h + "  gone.sql\n", files, "does not exist"},
		"malformed":     {h + "  a.sql extra\n", files, "malformed"},
	} {
		p := manifestProblems(c.manifest, c.files)
		if c.want == "" {
			if len(p) != 0 {
				t.Errorf("%s: %q", name, p)
			}
		} else if !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: %q, want one containing %q", name, p, c.want)
		}
	}
}
