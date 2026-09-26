package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	arxbase "arx/internal/config"
)

// integrationDSN returns the connection string the -tags integration suite
// runs against: ARX_TEST_DSN when set, otherwise (only with
// ARX_TEST_FROM_CONFIG=1) the test-mode profile from config/local.json plus
// the per-user secrets store, so the password never has to be typed or echoed
// (#207). Returns "" when neither is requested, so the suite skips.
func integrationDSN() (string, error) {
	if dsn := os.Getenv("ARX_TEST_DSN"); dsn != "" {
		return dsn, nil
	}
	if os.Getenv("ARX_TEST_FROM_CONFIG") != "1" {
		return "", nil
	}
	cfg := arxbase.Load("dev")
	cfg.TestMode = true
	dsn := cfg.DSN()
	if dsn == "" {
		return "", errors.New("ARX_TEST_FROM_CONFIG=1 but the test-mode connection has no server or password configured")
	}
	return dsn, nil
}

// integrationEngine derives the db engine from dsn's scheme (not from
// test_engine in local.json, which may disagree) and refuses any target whose
// database is not ArxDev, before a connection is attempted.
// Errors never include dsn itself, since it carries the password.
func integrationEngine(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", errors.New("test DSN is not a valid URL")
	}
	var engine, database string
	switch u.Scheme {
	case "sqlserver":
		engine, database = "sqlserver", u.Query().Get("database")
	case "postgres", "postgresql":
		engine, database = "postgres", strings.TrimPrefix(u.Path, "/")
	default:
		return "", fmt.Errorf("test DSN scheme %q is not sqlserver:// or postgres://", u.Scheme)
	}
	// Two independent checks: an explicit ArxProd refusal, then an allowlist of
	// exactly ArxDev (any case).
	if strings.Contains(strings.ToLower(database), "arxprod") {
		return "", fmt.Errorf("refusing to run integration tests against %q", database)
	}
	if !strings.EqualFold(database, "arxdev") {
		return "", fmt.Errorf("integration tests only run against ArxDev, not %q", database)
	}
	return engine, nil
}

func TestIntegrationEngine(t *testing.T) {
	cases := []struct {
		dsn, want string
		wantErr   bool
	}{
		{"sqlserver://u:p@host?database=ArxDev&encrypt=true", "sqlserver", false},
		{"postgres://u:p@host:5432/arxdev?sslmode=require", "postgres", false},
		{"postgresql://u:p@host/ARXDEV", "postgres", false},
		{"postgres://u:p@host/arx", "", true},
		{"sqlserver://u:p@host?database=ArxDev2&encrypt=true", "", true},
		{"sqlserver://u:p@host?database=ArxProd&encrypt=true", "", true},
		{"postgres://u:p@host/arxprod", "", true},
		{"sqlserver://u:p@host?encrypt=true", "", true},
		{"server=host;database=ArxDev", "", true},
		{"mysql://u:p@host/arxdev", "", true},
	}
	for _, c := range cases {
		got, err := integrationEngine(c.dsn)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("integrationEngine(%q) = %q, %v; want %q, err=%v", c.dsn, got, err, c.want, c.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), ":p@") {
			t.Errorf("integrationEngine(%q) error leaks the password: %v", c.dsn, err)
		}
	}
}
