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
