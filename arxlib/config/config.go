package config

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
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
const ExpectedSchemaVersion = "5"

// Config holds all configuration for the merged Arx application.
type Config struct {
	Base
	POFolderRoot      string
	SupplierFilesRoot string
	TestRecordsURL    string
	PartsMasterURL    string
	ImageRoot         string // used by Test Records; stored/displayed here so settings save round-trips it
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
			Engine:         os.Getenv("DB_ENGINE"),
			DBName:         os.Getenv("DB_NAME"),
			TestDBServer:   os.Getenv("TEST_DB_SERVER"),
			TestEngine:     os.Getenv("TEST_DB_ENGINE"),
			TestDBName:     GetEnv("TEST_DB_NAME", "ArxDev"),
			TestDBUser:     os.Getenv("TEST_DB_USER"),
			DBUser:         os.Getenv("DB_USER"),
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
	// local is nil only when local.json exists but is unreadable/corrupt; a
	// missing file yields an empty (non-nil) config.
	local, _ := LoadLocal()
	if local != nil {
		if local.DBServer != "" {
			cfg.DBServer = local.DBServer
		}
		if local.DBName != "" {
			cfg.DBName = local.DBName
		}
		if local.DBUser != "" {
			cfg.DBUser = local.DBUser
		}
		if local.Engine != nil {
			cfg.Engine = *local.Engine
		}
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
		if local.DebugMode {
			cfg.DebugMode = true
		}
		if local.TestMode != nil {
			cfg.TestMode = *local.TestMode
		}
		if local.TestDBServer != "" {
			cfg.TestDBServer = local.TestDBServer
		}
		if local.TestEngine != "" {
			cfg.TestEngine = local.TestEngine
		}
		if local.TestDBName != "" {
			cfg.TestDBName = local.TestDBName
		}
		if local.TestDBUser != "" {
			cfg.TestDBUser = local.TestDBUser
		}
	}

	// Expand a %USERPROFILE% token in the four folder roots so a shared
	// config/local.json (e.g. Arx.exe on a shared OneDrive folder) resolves
	// under whichever user's profile is running it, instead of the path the
	// last user to save Settings happened to have (#731).
	cfg.DocControlRoot = ExpandUserPath(cfg.DocControlRoot)
	cfg.POFolderRoot = ExpandUserPath(cfg.POFolderRoot)
	cfg.SupplierFilesRoot = ExpandUserPath(cfg.SupplierFilesRoot)
	cfg.ImageRoot = ExpandUserPath(cfg.ImageRoot)

	// Secrets (DB passwords, session secret) live in a per-user store, not the
	// shared config/local.json, so a shared exe does not leak them (#732). A nil
	// return means the store is unavailable — leave passwords empty (re-prompt).
	secrets, _ := LoadSecrets()
	if secrets != nil {
		cfg.DBPassword = secrets.DBPassword
		cfg.TestDBPassword = secrets.TestDBPassword
	}

	// Resolve the session secret used to sign session/CSRF cookies. Precedence:
	// explicit SESSION_SECRET env, then the persisted per-user secret, else
	// generate a strong random one and persist it. Never fall back to a shared
	// constant — a known key lets anyone forge a valid session cookie (#648;
	// regression of #352, lost in the two-app merge).
	cfg.SessionSecret = resolveSessionSecret(secrets)

	return cfg
}

// resolveSessionSecret returns the session-signing key, generating and persisting
// a random one to the per-user secrets store when neither the environment nor the
// store supplies it. It only writes when the store was readable (secrets != nil),
// so an unavailable store is never clobbered — in that case an ephemeral
// per-process key is used.
func resolveSessionSecret(secrets *SecretsConfig) string {
	if env := os.Getenv("SESSION_SECRET"); env != "" {
		return env
	}
	if secrets != nil && secrets.SessionSecret != "" {
		return secrets.SessionSecret
	}

	secret := randomSecret()
	if secrets != nil {
		secrets.SessionSecret = secret
		if err := SaveSecrets(secrets); err != nil {
			log.Printf("warning: could not persist generated session secret: %v", err)
		}
	} else {
		log.Println("warning: secrets store unavailable; using an ephemeral session secret (sessions will not survive a restart)")
	}
	return secret
}

// randomSecret returns 32 bytes of crypto/rand entropy, base64url-encoded.
// A crypto/rand failure is unrecoverable for a secure default, so it panics
// (fail closed) rather than returning a weak or empty key.
func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
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
func (c *Config) BuildTable() string              { return "build" }
func (c *Config) LotTable() string                { return "lot" }
func (c *Config) LotGenealogyTable() string       { return "lot_genealogy" }
func (c *Config) AppConfigTable() string          { return "app_config" }
func (c *Config) UsersTable() string              { return "users" }
func (c *Config) LinksTable() string              { return "LNK" }

// Test Records tables
func (c *Config) FormsTable() string              { return "form" }
func (c *Config) RecordsTable() string            { return "form_record" }
func (c *Config) StepsTable() string              { return "form_row" }
func (c *Config) ResultsTable() string            { return "result" }
func (c *Config) NamedQueriesTable() string       { return "named_queries" }
func (c *Config) FormRowHistoryTable() string     { return "form_row_history" }
func (c *Config) FormEventsTable() string          { return "form_events" }
func (c *Config) RecordEventsTable() string        { return "record_events" }
func (c *Config) RecordEventResultsTable() string  { return "record_event_results" }

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
