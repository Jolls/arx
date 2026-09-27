package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// unreachable DSNs fail fast at connect, so a "connect:" error proves the
// confirmation gate was passed without touching a real database.
const dsnTail = "@127.0.0.1:1/%s?sslmode=disable&connect_timeout=2"

func dsnFor(db string) string {
	return "postgres://u:s3cret" + strings.Replace(dsnTail, "%s", db, 1)
}

func envOnly(dsn string) func(string) string {
	return func(k string) string {
		if k == DSNEnv {
			return dsn
		}
		return ""
	}
}

func TestRun_Usage(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	for _, args := range [][]string{nil, {"down"}, {"status", "extra"}, {"up", "-bogus"}} {
		var out bytes.Buffer
		err := Run(context.Background(), args, envOnly(dsnFor("ArxDev")), strings.NewReader(""), &out)
		if err == nil {
			t.Errorf("%q: want error", args)
		}
		if strings.Contains(out.String(), "Target:") {
			t.Errorf("%q: printed target before validating args: %q", args, out.String())
		}
	}
}

func TestRun_DSNOnlyFromEnv(t *testing.T) {
	var asked []string
	getenv := func(k string) string { asked = append(asked, k); return "" }
	err := Run(context.Background(), []string{"status"}, getenv, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), DSNEnv) {
		t.Fatalf("err = %v, want one naming %s", err, DSNEnv)
	}
	if len(asked) != 1 || asked[0] != DSNEnv {
		t.Errorf("getenv keys = %q, want only %s", asked, DSNEnv)
	}
}

// The runner must never fall back to app config or the secrets store (#91).
func TestRun_NoAppConfigImport(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "migrate.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "arx/internal/config" || strings.HasPrefix(p, "github.com/joho/godotenv") {
			t.Errorf("migrate.go imports %s", p)
		}
	}
}

func TestRun_BadDSN(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	var out bytes.Buffer
	err := Run(context.Background(), []string{"status"}, envOnly("postgres://u:s3cret@host:notaport/db"), strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("want error for unparseable DSN")
	}
	if strings.Contains(err.Error()+out.String(), "s3cret") {
		t.Errorf("password leaked: err=%q out=%q", err, out.String())
	}

	err = Run(context.Background(), []string{"status"}, envOnly("postgres://u:p@127.0.0.1:1/"), strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must name a database") {
		t.Errorf("err = %v, want 'must name a database'", err)
	}
}

func TestRun_PrintsTarget(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	var out bytes.Buffer
	_ = Run(context.Background(), []string{"status"}, envOnly(dsnFor("ArxProd")), strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "Target: 127.0.0.1:1/ArxProd (user u)") {
		t.Errorf("out = %q, want target line", out.String())
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("password leaked: %q", out.String())
	}
}

func TestRun_ConfirmationGate(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	cases := []struct {
		name, db, stdin string
		args            []string
		refused         bool
		prompted        bool
	}{
		{"EOF refuses", "ArxProd", "", []string{"up"}, true, true},
		{"wrong case refuses", "ArxProd", "arxprod\n", []string{"up"}, true, true},
		{"typed name passes", "ArxProd", "ArxProd\n", []string{"up"}, false, true},
		{"--yes skips prompt", "ArxProd", "", []string{"up", "--yes"}, false, false},
		{"ArxDev any case skips prompt", "arxdev", "", []string{"up"}, false, false},
		{"status never prompts", "ArxProd", "", []string{"status"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Run(context.Background(), c.args, envOnly(dsnFor(c.db)), strings.NewReader(c.stdin), &out)
			if c.refused {
				if !errors.Is(err, ErrNotConfirmed) {
					t.Errorf("err = %v, want ErrNotConfirmed", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), "connect:") {
				t.Errorf("err = %v, want a connect: error (gate passed)", err)
			}
			if got := strings.Contains(out.String(), "Type the database name"); got != c.prompted {
				t.Errorf("prompted = %v, want %v (out %q)", got, c.prompted, out.String())
			}
		})
	}
}

func TestNewProvider_ListsEmbeddedMigrations(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://u@127.0.0.1:1/x")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := NewProvider(db)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	files, err := filepath.Glob(filepath.Join("..", "..", "SQL", "postgres", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]bool{}
	for _, f := range files {
		v, err := strconv.ParseInt(strings.SplitN(filepath.Base(f), "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		want[v] = true
	}
	if len(want) < 4 {
		t.Fatalf("found %d migration files, want >= 4", len(want))
	}

	srcs := p.ListSources()
	if len(srcs) != len(want) {
		t.Fatalf("provider lists %d sources, directory has %d files", len(srcs), len(want))
	}
	for _, s := range srcs {
		if !want[s.Version] {
			t.Errorf("provider source %s (version %d) has no matching file", s.Path, s.Version)
		}
	}
}

func TestConfirm(t *testing.T) {
	for _, c := range []struct {
		in   string
		want error
	}{
		{"ArxProd\n", nil},
		{"ArxProd", nil},
		{" ArxProd \n", nil},
		{"x\n", ErrNotConfirmed},
		{"", ErrNotConfirmed},
	} {
		var out bytes.Buffer
		if err := confirm("ArxProd", strings.NewReader(c.in), &out); !errors.Is(err, c.want) {
			t.Errorf("confirm(%q) = %v, want %v", c.in, err, c.want)
		}
		if !strings.Contains(out.String(), "ArxProd") {
			t.Errorf("confirm(%q): prompt %q doesn't name the database", c.in, out.String())
		}
	}
}

