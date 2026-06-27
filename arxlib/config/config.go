package config

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"

	"github.com/joho/godotenv"
)

// ExpectedSchemaVersion is the app_config schema_version this build requires.
// Bump this ONLY for non-backward-compatible schema changes — ones where the previous
// binary can no longer run against the migrated DB (dropped/renamed columns or tables,
// type changes, repurposed columns). Additive changes (new nullable or defaulted columns,
// new tables) are backward-compatible and must NOT bump this — the old binary ignores them.
const ExpectedSchemaVersion = "3"

// Config holds all configuration for the merged Arx application.
type Config struct {
	Base
	POFolderRoot      string
	SupplierFilesRoot string
	TestRecordsURL    string
	PartsMasterURL    string
	ImageRoot         string // used by Test Records; stored/displayed here so settings save round-trips it
	PODefaults        PODefaults
}

// PODefaults holds the default contact and receiver IDs for new purchase orders.
type PODefaults struct {
	ContactID  int
	ReceiverID int
}

// Load reads configuration from .env files, environment variables, and
// config/local.json. Later sources win over earlier ones.
// version is injected via -ldflags at build time (e.g. arx/arx_go.AppVersion).
func Load(version string) *Config {
	// Prefer a shared repo-root .env; fall back to an app-local one.
	if err := godotenv.Load("../.env"); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Println("no .env file found, reading from environment")
		}
	}

	cfg := &Config{
		Base: Base{
			Version:        version,
			Port:           GetEnv("PM_PORT", GetEnv("PORT", "4568")),
			DBServer:       os.Getenv("DB_SERVER"),
			DBName:         os.Getenv("DB_NAME"),
			TestDBName:     GetEnv("TEST_DB_NAME", "ArxDev"),
			DBUser:         os.Getenv("DB_USER"),
			SessionSecret:  GetEnv("SESSION_SECRET", "change-me-in-production"),
			DocControlRoot: os.Getenv("DOC_CONTROL_ROOT"),
			TestMode:       os.Getenv("TEST_MODE") == "true",
			DebugMode:      os.Getenv("DEBUG_MODE") == "true",
		},
		POFolderRoot:      os.Getenv("PO_FOLDER_ROOT"),
		SupplierFilesRoot: os.Getenv("SUPPLIER_FILES_ROOT"),
		TestRecordsURL:    GetEnv("TR_URL", "/records"),
		PartsMasterURL:    GetEnv("PM_URL", "/"),
		ImageRoot:         os.Getenv("IMAGE_ROOT"),
	}

	// Backward compat: parse DATABASE_DSN if new individual fields are not set.
	if cfg.DBServer == "" {
		if rawDSN := os.Getenv("DATABASE_DSN"); rawDSN != "" {
			if u, err := url.Parse(rawDSN); err == nil {
				cfg.DBServer = u.Host
				cfg.DBName = u.Query().Get("database")
				if u.User != nil {
					cfg.DBUser = u.User.Username()
					// Password deliberately NOT extracted — must come from local.json
				}
			}
		}
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
		if local.DocControlRoot != "" {
			cfg.DocControlRoot = local.DocControlRoot
		}
		if local.POFolderRoot != "" {
			cfg.POFolderRoot = local.POFolderRoot
		}
		if local.SupplierFilesRoot != "" {
			cfg.SupplierFilesRoot = local.SupplierFilesRoot
		}
		if local.ImageRoot != "" {
			cfg.ImageRoot = local.ImageRoot
		}
		if local.PODefaultContactID != nil {
			cfg.PODefaults.ContactID = *local.PODefaultContactID
		}
		if local.PODefaultReceiverID != nil {
			cfg.PODefaults.ReceiverID = *local.PODefaultReceiverID
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
// Parts Master tables
func (c *Config) PartsTable() string              { return "part" }
func (c *Config) AttachmentsTable() string        { return "part_attachment" }
func (c *Config) BOMTable() string                { return "bom" }
func (c *Config) PriceTable() string              { return "price" }
func (c *Config) POTable() string                 { return "purchase_order" }
func (c *Config) POLineTable() string             { return "po_line" }
func (c *Config) POHistoryTable() string          { return "purchase_order_history" }
func (c *Config) CompanyTable() string            { return "company" }
func (c *Config) SupplierPartTable() string       { return "supplier_part" }
func (c *Config) MfgPartTable() string            { return "mfg_part" }
func (c *Config) CompanyAttachmentsTable() string { return "company_attachment" }
func (c *Config) ContactTable() string            { return "contact" }
func (c *Config) UnitTable() string               { return "unit" }
func (c *Config) InventoryTxnTable() string       { return "inventory_transaction" }
func (c *Config) AppConfigTable() string          { return "app_config" }
func (c *Config) UsersTable() string              { return "users" }
func (c *Config) LinksTable() string              { return "LNK" }

// Test Records tables
func (c *Config) FormsTable() string                 { return "form" }
func (c *Config) RecordsTable() string               { return "test_record" }
func (c *Config) StepsTable() string                 { return "test_definition" }
func (c *Config) ResultsTable() string               { return "test_result" }
func (c *Config) NamedQueriesTable() string          { return "named_queries" }
func (c *Config) TestDefinitionHistoryTable() string { return "test_definition_history" }
func (c *Config) FormEventsTable() string            { return "form_events" }
func (c *Config) RecordEventsTable() string          { return "record_events" }

// CheckSchemaVersion queries app_config for schema_version and returns "" when it
// matches ExpectedSchemaVersion, or a non-empty mismatch/error message otherwise.
// The queryRow argument is the handler's h.queryRowContext wrapper so SQL logging is preserved.
func CheckSchemaVersion(ctx context.Context,
	queryRow func(context.Context, string, ...any) *sql.Row,
	appConfigTable string) string {
	var val string
	err := queryRow(ctx,
		`SELECT setting_value FROM `+appConfigTable+` WHERE setting_key = 'schema_version'`,
	).Scan(&val)
	if err != nil {
		return fmt.Sprintf("could not read schema_version (%v)", err)
	}
	if val != ExpectedSchemaVersion {
		return fmt.Sprintf("DB schema v%s, app expects v%s", val, ExpectedSchemaVersion)
	}
	return ""
}
