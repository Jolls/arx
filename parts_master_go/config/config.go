package config

import (
	"log"
	"net/url"
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
	POFolderRoot      string
	SupplierFilesRoot string
	TestRecordsURL    string
	ImageRoot         string // used by Test Records; stored/displayed here so settings save round-trips it
	PODefaults        PODefaults
}

type PODefaults struct {
	ContactID  int
	ReceiverID int
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
			Port:           arxbase.GetEnv("PM_PORT", arxbase.GetEnv("PORT", "4568")),
			DBServer:       os.Getenv("DB_SERVER"),
			DBName:         os.Getenv("DB_NAME"),
			TestDBName:     arxbase.GetEnv("TEST_DB_NAME", "ArxDev"),
			DBUser:         os.Getenv("DB_USER"),
			SessionSecret:  arxbase.GetEnv("SESSION_SECRET", "change-me-in-production"),
			DocControlRoot: os.Getenv("DOC_CONTROL_ROOT"),
			TestMode:       os.Getenv("TEST_MODE") == "true",
			DebugMode:      os.Getenv("DEBUG_MODE") == "true",
		},
		POFolderRoot:      os.Getenv("PO_FOLDER_ROOT"),
		SupplierFilesRoot: os.Getenv("SUPPLIER_FILES_ROOT"),
		TestRecordsURL:    arxbase.GetEnv("TR_URL", "/records"),
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
func (c *Config) PartsTable() string              { return "PN" }
func (c *Config) AttachmentsTable() string        { return "FIL" }
func (c *Config) BOMTable() string                { return "PL" }
func (c *Config) PriceTable() string              { return "price" }
func (c *Config) POTable() string                 { return "PO" }
func (c *Config) POLineTable() string             { return "POL" }
func (c *Config) CompanyTable() string            { return "company" }
func (c *Config) SupplierPartTable() string       { return "supplier_part" }
func (c *Config) MfgPartTable() string            { return "mfg_part" }
func (c *Config) CompanyAttachmentsTable() string { return "company_attachment" }
func (c *Config) ContactTable() string            { return "CN" }
func (c *Config) UnitTable() string               { return "unit" }
func (c *Config) AppConfigTable() string          { return "app_config" }
