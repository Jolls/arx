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
	Version            string
	Port               string
	DBServer           string
	DBName             string
	DBUser             string
	DBPassword         string // from local.json only — never stored in .env
	SessionSecret      string
	DocControlRoot     string
	POFolderRoot       string
	SupplierFilesRoot  string
	TestRecordsURL     string
	TestMode           bool
	DebugMode          bool
	Settings           Settings
}

type Settings struct {
	PODefaults struct {
		ContactID  int
		ReceiverID int
	}
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
		Port:           getEnv("PM_PORT", getEnv("PORT", "4568")),
		DBServer:       os.Getenv("DB_SERVER"),
		DBName:         os.Getenv("DB_NAME"),
		DBUser:         os.Getenv("DB_USER"),
		SessionSecret:  getEnv("SESSION_SECRET", "change-me-in-production"),
		DocControlRoot:    os.Getenv("DOC_CONTROL_ROOT"),
		POFolderRoot:      os.Getenv("PO_FOLDER_ROOT"),
		SupplierFilesRoot: os.Getenv("SUPPLIER_FILES_ROOT"),
		TestRecordsURL:    getEnv("TR_URL", "http://localhost:4569"),
		TestMode:          os.Getenv("TEST_MODE") == "true",
		DebugMode:      os.Getenv("DEBUG_MODE") == "true",
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
		if local.PODefaultContactID != nil {
			cfg.Settings.PODefaults.ContactID = *local.PODefaultContactID
		}
		if local.PODefaultReceiverID != nil {
			cfg.Settings.PODefaults.ReceiverID = *local.PODefaultReceiverID
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

// BuildDSN constructs a sqlserver:// DSN from the config fields + a password.
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

// DSN returns a ready-to-use connection string using the stored DBPassword.
// Returns an empty string if password or server is not configured.
func (c *Config) DSN() string {
	if c.DBPassword == "" || c.DBServer == "" {
		return ""
	}
	return c.BuildDSN(c.DBPassword)
}

// ConnectionSummary returns a non-sensitive description of the DB target.
func (c *Config) ConnectionSummary() string {
	if c.DBServer == "" {
		return "(not configured)"
	}
	return fmt.Sprintf("%s / %s", c.DBServer, c.DBName)
}

// Table name helpers — each pair matches the Ruby model's self.table_name.
func (c *Config) PartsTable() string        { return pick(c.TestMode, "PN_Test", "PN") }
func (c *Config) AttachmentsTable() string  { return pick(c.TestMode, "FIL_Test", "FIL") }
func (c *Config) BOMTable() string          { return pick(c.TestMode, "PL_Test", "PL") }
func (c *Config) PriceTable() string        { return pick(c.TestMode, "price_Test", "price") }
func (c *Config) POTable() string           { return pick(c.TestMode, "PO_Test", "PO") }
func (c *Config) POLineTable() string       { return pick(c.TestMode, "POL_Test", "POL") }
func (c *Config) CompanyTable() string             { return pick(c.TestMode, "company_Test", "company") }
func (c *Config) SupplierPartTable() string        { return pick(c.TestMode, "supplier_part_Test", "supplier_part") }
func (c *Config) MfgPartTable() string             { return pick(c.TestMode, "mfg_part_Test", "mfg_part") }
func (c *Config) CompanyAttachmentsTable() string  {
	return pick(c.TestMode, "company_attachment_Test", "company_attachment")
}
func (c *Config) ContactTable() string      { return pick(c.TestMode, "CN_Test", "CN") }
func (c *Config) AppConfigTable() string    { return pick(c.TestMode, "app_config_Test", "app_config") }

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
