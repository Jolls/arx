package config

import (
	"fmt"
	"log"
	"net/url"
	"os"

	"github.com/joho/godotenv"
)

// AppVersion is set at build time via -ldflags from the top entry in CHANGELOG.md.
// Falls back to "dev" when running with `go run`.
var AppVersion = "dev"

// ExpectedSchemaVersion is the app_config schema_version this build requires.
// Bump this whenever a migration changes the DB schema.
const ExpectedSchemaVersion = "2"

type Config struct {
	Version        string
	Port           string
	DBServer       string
	DBName         string
	DBUser         string
	DBPassword     string // from local.json only — never stored in .env
	SessionSecret  string
	ImageRoot      string
	DocControlRoot string
	PartsMasterURL string
	TestMode       bool
	DebugMode      bool
}

func Load() *Config {
	// Prefer a shared repo-root .env; fall back to an app-local one.
	if err := godotenv.Load("../.env"); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Println("no .env file found, reading from environment")
		}
	}

	cfg := &Config{
		Version:        AppVersion,
		Port:           getEnv("TR_PORT", getEnv("PORT", "4569")),
		TestMode:       os.Getenv("TEST_MODE") == "true",
		DBServer:       os.Getenv("DB_SERVER"),
		DBName:         os.Getenv("DB_NAME"),
		DBUser:         os.Getenv("DB_USER"),
		SessionSecret:  getEnv("SESSION_SECRET", "change-me-in-production"),
		ImageRoot:      os.Getenv("IMAGE_ROOT"),
		DocControlRoot: os.Getenv("DOC_CONTROL_ROOT"),
		PartsMasterURL: getEnv("PM_URL", "http://localhost:4568"),
		DebugMode:      os.Getenv("DEBUG_MODE") == "true",
	}

	// Apply local.json overrides (local values always win over .env).
	if local, err := LoadLocal(); err == nil && local != nil {
		if local.DBServer != "" {
			cfg.DBServer = local.DBServer
		}
		if local.DBName != "" {
			cfg.DBName = local.DBName
		}
		if local.DBUser != "" {
			cfg.DBUser = local.DBUser
		}
		cfg.DBPassword = local.DBPassword
		if local.ImageRoot != "" {
			cfg.ImageRoot = local.ImageRoot
		}
		if local.DocControlRoot != "" {
			cfg.DocControlRoot = local.DocControlRoot
		}
		if local.DebugMode {
			cfg.DebugMode = true
		}
		if local.TestMode != nil {
			cfg.TestMode = *local.TestMode
		}
	}

	return cfg
}

func (c *Config) BuildDSN(password string) string {
	u := &url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword(c.DBUser, password),
		Host:   c.DBServer,
		RawQuery: url.Values{
			"database": {c.DBName},
			"encrypt":  {"true"},
		}.Encode(),
	}
	return u.String()
}

func (c *Config) DSN() string {
	if c.DBPassword == "" || c.DBServer == "" {
		return ""
	}
	return c.BuildDSN(c.DBPassword)
}

func (c *Config) ConnectionSummary() string {
	if c.DBServer == "" {
		return "(not configured)"
	}
	return fmt.Sprintf("%s / %s", c.DBServer, c.DBName)
}

// Table name helpers — switches between prod and _Test variants.
func (c *Config) PartsTable() string          { return pick(c.TestMode, "PN_Test", "PN") }
func (c *Config) BOMTable() string            { return pick(c.TestMode, "PL_Test", "PL") } // read-only; PL is owned by parts_master_go
func (c *Config) FormsTable() string          { return pick(c.TestMode, "Forms_Test", "Forms") }
func (c *Config) RecordsTable() string        { return pick(c.TestMode, "TestRecords_Test", "TestRecords") }
func (c *Config) StepsTable() string          { return pick(c.TestMode, "test_definition_Test", "test_definition") }
func (c *Config) ResultsTable() string        { return pick(c.TestMode, "TestResults_Test", "TestResults") }
func (c *Config) NamedQueriesTable() string      { return "named_queries" } // no _Test variant — shared config
func (c *Config) TestDefinitionHistoryTable() string {
	return pick(c.TestMode, "test_definition_history_Test", "test_definition_history")
}
func (c *Config) AppConfigTable() string { return pick(c.TestMode, "app_config_Test", "app_config") }

func pick(test bool, testVal, prodVal string) string {
	if test {
		return testVal
	}
	return prodVal
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
