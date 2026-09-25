package config

import (
	"os"
	"testing"
)

// clearConfigEnv unsets every env var Load() reads, so the developer's real
// shell environment never leaks into these tests. Uses os.Unsetenv (not
// t.Setenv("", "")) because godotenv.Load only fills vars that are genuinely
// absent from the process environment — a var set to "" still counts as
// present and blocks .env from supplying it.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PM_PORT", "PORT", "DB_SERVER", "DB_ENGINE", "DB_NAME",
		"TEST_DB_SERVER", "TEST_DB_ENGINE", "TEST_DB_NAME", "TEST_DB_USER",
		"DB_USER", "DOC_CONTROL_ROOT", "TEST_MODE", "DEBUG_MODE",
		"PO_FOLDER_ROOT", "SUPPLIER_FILES_ROOT", "IMAGE_ROOT",
		"DATABASE_DSN", "SESSION_SECRET",
	}
	orig := make(map[string]string, len(keys))
	origSet := make(map[string]bool, len(keys))
	for _, k := range keys {
		orig[k], origSet[k] = os.LookupEnv(k)
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			if origSet[k] {
				os.Setenv(k, orig[k])
			}
		}
	})
}

// writeLocalJSON writes config/local.json in the current (isolated) cwd.
// Called after isolateStores(t) has already Chdir'd into a temp dir. Always
// write this file (even "{}") so LoadLocal never falls into migrateLegacy.
func writeLocalJSON(t *testing.T, contents string) {
	t.Helper()
	if err := os.MkdirAll("config", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config/local.json", []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestLoad_Defaults confirms code defaults with every source isolated/empty.
func TestLoad_Defaults(t *testing.T) {
	isolateStores(t)
	clearConfigEnv(t)
	writeLocalJSON(t, "{}")

	cfg := Load("v0")

	if cfg.TestDBName != "ArxDev" {
		t.Errorf("TestDBName = %q, want %q (code default)", cfg.TestDBName, "ArxDev")
	}
	if cfg.TestMode != false {
		t.Errorf("TestMode = %v, want false", cfg.TestMode)
	}
	if cfg.DBServer != "" {
		t.Errorf("DBServer = %q, want empty", cfg.DBServer)
	}
	if cfg.DBName != "" {
		t.Errorf("DBName = %q, want empty", cfg.DBName)
	}
}

// TestLoad_EnvVarsApply confirms env vars alone reach the Config.
func TestLoad_EnvVarsApply(t *testing.T) {
	isolateStores(t)
	clearConfigEnv(t)
	writeLocalJSON(t, "{}")

	t.Setenv("DB_SERVER", "envserver")
	t.Setenv("DB_NAME", "envdb")
	t.Setenv("TEST_DB_NAME", "EnvTestDB")
	t.Setenv("TEST_MODE", "true")

	cfg := Load("v0")

	if cfg.DBServer != "envserver" {
		t.Errorf("DBServer = %q, want %q", cfg.DBServer, "envserver")
	}
	if cfg.DBName != "envdb" {
		t.Errorf("DBName = %q, want %q", cfg.DBName, "envdb")
	}
	if cfg.TestDBName != "EnvTestDB" {
		t.Errorf("TestDBName = %q, want %q (env overrides code default)", cfg.TestDBName, "EnvTestDB")
	}
	if cfg.TestMode != true {
		t.Errorf("TestMode = %v, want true", cfg.TestMode)
	}
}

// TestLoad_EnvVarWinsOverDotEnvFile verifies a real env var beats the same key
// supplied via .env, per the documented ".env -> env vars -> local.json" order
// (a set env var is left untouched by godotenv.Load, so os.Getenv reads the
// env var, not the .env value).
func TestLoad_EnvVarWinsOverDotEnvFile(t *testing.T) {
	isolateStores(t)
	clearConfigEnv(t)

	if err := os.WriteFile(".env", []byte("DB_SERVER=dotenvserver\nDB_NAME=dotenvdb\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_SERVER", "realenvserver")
	writeLocalJSON(t, "{}")

	cfg := Load("v0")

	if cfg.DBServer != "realenvserver" {
		t.Errorf("DBServer = %q, want %q (env var must beat .env for the same key)", cfg.DBServer, "realenvserver")
	}
	if cfg.DBName != "dotenvdb" {
		t.Errorf("DBName = %q, want %q (.env alone, no env var, must still apply)", cfg.DBName, "dotenvdb")
	}
}

// TestLoad_LocalJSONOverridesEnvAndDotEnv confirms local.json (highest
// precedence) overrides both env var and .env values, including the *bool
// nil-vs-explicit-false semantics for TestMode.
func TestLoad_LocalJSONOverridesEnvAndDotEnv(t *testing.T) {
	isolateStores(t)
	clearConfigEnv(t)

	if err := os.WriteFile(".env", []byte("DB_SERVER=dotenvserver\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_SERVER", "realenvserver")
	t.Setenv("DB_NAME", "envdb")
	t.Setenv("TEST_DB_NAME", "EnvTestDB")
	t.Setenv("TEST_MODE", "true")
	writeLocalJSON(t, `{"db_server":"localserver","db_name":"localdb","test_db_name":"LocalTestDB","test_mode":false}`)

	cfg := Load("v0")

	if cfg.DBServer != "localserver" {
		t.Errorf("DBServer = %q, want %q (local.json overrides env-supplied value)", cfg.DBServer, "localserver")
	}
	if cfg.DBName != "localdb" {
		t.Errorf("DBName = %q, want %q", cfg.DBName, "localdb")
	}
	if cfg.TestDBName != "LocalTestDB" {
		t.Errorf("TestDBName = %q, want %q", cfg.TestDBName, "LocalTestDB")
	}
	if cfg.TestMode != false {
		t.Errorf("TestMode = %v, want false (local.json's explicit false must override env's true)", cfg.TestMode)
	}
}

// TestLoad_LocalJSONAbsentFieldsDoNotOverride confirms fields absent from
// local.json don't clobber an env-supplied value with a zero value.
func TestLoad_LocalJSONAbsentFieldsDoNotOverride(t *testing.T) {
	isolateStores(t)
	clearConfigEnv(t)

	t.Setenv("DB_SERVER", "envserver")
	t.Setenv("TEST_MODE", "true")
	writeLocalJSON(t, `{"db_name":"localonlydb"}`)

	cfg := Load("v0")

	if cfg.DBServer != "envserver" {
		t.Errorf("DBServer = %q, want %q (local.json omitted this field, env value must survive)", cfg.DBServer, "envserver")
	}
	if cfg.DBName != "localonlydb" {
		t.Errorf("DBName = %q, want %q", cfg.DBName, "localonlydb")
	}
	if cfg.TestMode != true {
		t.Errorf("TestMode = %v, want true (local.json's TestMode is nil since the key is absent, env value must survive)", cfg.TestMode)
	}
}
