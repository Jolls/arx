package config

import (
	"log"
	"os"

	arxbase "arx/arxlib/config"
	"github.com/joho/godotenv"
)

// AppVersion is set at build time via -ldflags from the top entry in CHANGELOG.md.
// Falls back to "dev" when running with `go run`.
var AppVersion = "dev"

// ExpectedSchemaVersion is the app_config schema_version this build requires.
// Bump this whenever a migration changes the DB schema.
const ExpectedSchemaVersion = "2"

type Config struct {
	arxbase.Base
	ImageRoot      string
	PartsMasterURL string
}

func Load() *Config {
	// Prefer a shared repo-root .env; fall back to an app-local one.
	if err := godotenv.Load("../.env"); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Println("no .env file found, reading from environment")
		}
	}

	cfg := &Config{
		Base: arxbase.Base{
			Version:        AppVersion,
			Port:           arxbase.GetEnv("TR_PORT", arxbase.GetEnv("PORT", "4569")),
			DBServer:       os.Getenv("DB_SERVER"),
			DBName:         os.Getenv("DB_NAME"),
			TestDBName:     arxbase.GetEnv("TEST_DB_NAME", "ArxDev"),
			DBUser:         os.Getenv("DB_USER"),
			SessionSecret:  arxbase.GetEnv("SESSION_SECRET", "change-me-in-production"),
			DocControlRoot: os.Getenv("DOC_CONTROL_ROOT"),
			TestMode:       os.Getenv("TEST_MODE") == "true",
			DebugMode:      os.Getenv("DEBUG_MODE") == "true",
		},
		ImageRoot:      os.Getenv("IMAGE_ROOT"),
		PartsMasterURL: arxbase.GetEnv("PM_URL", "/"),
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
		if local.TestDBName != "" {
			cfg.TestDBName = local.TestDBName
		}
	}

	return cfg
}

// Table name helpers — TEST_MODE swaps the whole DB via the DSN (see Base.ActiveDBName).
func (c *Config) PartsTable() string                 { return "PN" }
func (c *Config) BOMTable() string                   { return "PL" }
func (c *Config) FormsTable() string                 { return "Forms" }
func (c *Config) RecordsTable() string               { return "TestRecords" }
func (c *Config) StepsTable() string                 { return "test_definition" }
func (c *Config) ResultsTable() string               { return "TestResults" }
func (c *Config) NamedQueriesTable() string          { return "named_queries" }
func (c *Config) TestDefinitionHistoryTable() string { return "test_definition_history" }
func (c *Config) FormEventsTable() string            { return "form_events" }
func (c *Config) RecordEventsTable() string          { return "record_events" }
func (c *Config) AppConfigTable() string             { return "app_config" }
