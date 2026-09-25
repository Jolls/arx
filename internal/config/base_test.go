package config

import (
	"strings"
	"testing"
)

func TestActiveDBName(t *testing.T) {
	b := Base{DBName: "ArxProd", TestDBName: "ArxDev"}

	if got := b.ActiveDBName(); got != "ArxProd" {
		t.Errorf("TestMode=false: got %q, want ArxProd", got)
	}

	b.TestMode = true
	if got := b.ActiveDBName(); got != "ArxDev" {
		t.Errorf("TestMode=true: got %q, want ArxDev", got)
	}
}

func TestBuildDSN_DatabaseSwap(t *testing.T) {
	b := Base{
		DBServer:   "myserver",
		DBName:     "ArxProd",
		TestDBName: "ArxDev",
		DBUser:     "sa",
	}

	prod := b.BuildDSN("secret")
	if !strings.Contains(prod, "database=ArxProd") {
		t.Errorf("prod DSN missing ArxProd: %s", prod)
	}
	if strings.Contains(prod, "ArxDev") {
		t.Errorf("prod DSN should not contain ArxDev: %s", prod)
	}

	b.TestMode = true
	dev := b.BuildDSN("secret")
	if !strings.Contains(dev, "database=ArxDev") {
		t.Errorf("test DSN missing ArxDev: %s", dev)
	}
	if strings.Contains(dev, "ArxProd") {
		t.Errorf("test DSN should not contain ArxProd: %s", dev)
	}

	// Server and user must be unchanged between the two modes
	for _, dsn := range []string{prod, dev} {
		if !strings.Contains(dsn, "myserver") {
			t.Errorf("DSN missing server: %s", dsn)
		}
	}
}

func TestBuildDSN_Postgres(t *testing.T) {
	b := Base{
		Engine:     "postgres",
		DBServer:   "pghost:5432",
		DBName:     "ArxProd",
		TestDBName: "ArxDev",
		DBUser:     "arx",
	}

	prod := b.BuildDSN("secret")
	if !strings.HasPrefix(prod, "postgres://") {
		t.Errorf("postgres DSN should use the postgres:// scheme: %s", prod)
	}
	if !strings.Contains(prod, "arx:secret@pghost:5432") {
		t.Errorf("postgres DSN missing user/host: %s", prod)
	}
	if !strings.Contains(prod, "/ArxProd") || strings.Contains(prod, "ArxDev") {
		t.Errorf("prod DSN should target ArxProd: %s", prod)
	}

	b.TestMode = true
	dev := b.BuildDSN("secret")
	if !strings.Contains(dev, "/ArxDev") || strings.Contains(dev, "ArxProd") {
		t.Errorf("test DSN should target ArxDev: %s", dev)
	}
}

func TestBuildDSN_TestProfileSeparateServer(t *testing.T) {
	// Prod on SQL Server; test profile on a Postgres host with its own creds.
	b := Base{
		Engine:         "sqlserver",
		DBServer:       "sqlhost",
		DBName:         "ArxProd",
		DBUser:         "sa",
		TestEngine:     "postgres",
		TestDBServer:   "pghost:5432",
		TestDBName:     "ArxDev",
		TestDBUser:     "arxdev",
		TestDBPassword: "devpass",
	}

	prod := b.BuildDSN("prodpass")
	if !strings.HasPrefix(prod, "sqlserver://") || !strings.Contains(prod, "sa:prodpass@sqlhost") {
		t.Errorf("prod DSN should target the SQL Server profile: %s", prod)
	}

	b.TestMode = true
	if got := b.DBEngine(); got != "postgres" {
		t.Errorf("test-mode engine: got %q, want postgres", got)
	}
	dev := b.DSN() // uses the stored test password
	if !strings.HasPrefix(dev, "postgres://") {
		t.Errorf("test DSN should use the postgres:// scheme: %s", dev)
	}
	if !strings.Contains(dev, "arxdev:devpass@pghost:5432") {
		t.Errorf("test DSN should use the test host/creds: %s", dev)
	}
	if !strings.Contains(dev, "/ArxDev") || strings.Contains(dev, "sqlhost") {
		t.Errorf("test DSN should target the Postgres dev DB only: %s", dev)
	}
}

func TestBuildDSN_TestProfileInheritsProd(t *testing.T) {
	// Only TestDBName set: the historical same-server, name-only swap must still
	// work — server, engine, user, and password all inherit prod.
	b := Base{
		Engine:     "sqlserver",
		DBServer:   "sqlhost",
		DBName:     "ArxProd",
		DBUser:     "sa",
		DBPassword: "prodpass",
		TestDBName: "ArxDev",
		TestMode:   true,
	}

	dev := b.DSN()
	if !strings.Contains(dev, "sa:prodpass@sqlhost") {
		t.Errorf("blank test fields should inherit prod host/creds: %s", dev)
	}
	if !strings.Contains(dev, "database=ArxDev") {
		t.Errorf("test DSN should swap to ArxDev: %s", dev)
	}
	if b.DBEngine() != "sqlserver" {
		t.Errorf("blank TestEngine should inherit prod engine, got %q", b.DBEngine())
	}
}

func TestConnectionSummary(t *testing.T) {
	b := Base{DBServer: "myserver", DBName: "ArxProd", TestDBName: "ArxDev"}

	if got := b.ConnectionSummary(); got != "myserver / ArxProd" {
		t.Errorf("prod summary: got %q", got)
	}

	b.TestMode = true
	if got := b.ConnectionSummary(); got != "myserver / ArxDev (TEST)" {
		t.Errorf("test summary: got %q", got)
	}
}

func TestDBEngineDefaultsToSQLServer(t *testing.T) {
	b := &Base{}
	if got := b.DBEngine(); got != "sqlserver" {
		t.Fatalf("empty engine: got %q, want %q", got, "sqlserver")
	}
}

func TestDBEngineNormalizes(t *testing.T) {
	cases := map[string]string{
		"postgres":  "postgres",
		"POSTGRES":  "postgres",
		"sqlserver": "sqlserver",
		"nonsense":  "sqlserver",
	}
	for in, want := range cases {
		b := &Base{Engine: in}
		if got := b.DBEngine(); got != want {
			t.Errorf("engine %q: got %q, want %q", in, got, want)
		}
	}
}
