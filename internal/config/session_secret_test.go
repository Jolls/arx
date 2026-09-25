package config

import (
	"os"
	"strings"
	"testing"
)

// isolateStores switches into a fresh temp dir (so config/local.json writes are
// isolated) and points os.UserConfigDir at a temp location (so the per-user
// secrets store writes are isolated too, on both Windows and Linux).
func isolateStores(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	cfgDir := t.TempDir()
	t.Setenv("AppData", cfgDir)         // Windows os.UserConfigDir
	t.Setenv("XDG_CONFIG_HOME", cfgDir) // Linux os.UserConfigDir
}

func TestResolveSessionSecret_EnvWins(t *testing.T) {
	isolateStores(t)
	env := strings.Repeat("e", minSessionSecretLen)
	t.Setenv("SESSION_SECRET", env)

	if got := resolveSessionSecret(&SecretsConfig{SessionSecret: "from-local"}); got != env {
		t.Fatalf("env should win: got %q", got)
	}
}

// A placeholder or short env value must not override the generated/persisted key (#168).
func TestResolveSessionSecret_WeakEnvIgnored(t *testing.T) {
	for name, env := range map[string]string{
		"placeholder": placeholderSessionSecret,
		"too short":   "short",
	} {
		t.Run(name, func(t *testing.T) {
			isolateStores(t)
			t.Setenv("SESSION_SECRET", env)

			if got := resolveSessionSecret(&SecretsConfig{SessionSecret: "from-local"}); got != "from-local" {
				t.Fatalf("weak env should be ignored: got %q", got)
			}
		})
	}
}

func TestResolveSessionSecret_UsesPersistedLocal(t *testing.T) {
	isolateStores(t)
	os.Unsetenv("SESSION_SECRET")

	if got := resolveSessionSecret(&SecretsConfig{SessionSecret: "from-local"}); got != "from-local" {
		t.Fatalf("stored secret should be used: got %q", got)
	}
}

func TestResolveSessionSecret_GeneratesAndPersists(t *testing.T) {
	isolateStores(t)
	os.Unsetenv("SESSION_SECRET")

	secrets := &SecretsConfig{}
	secret := resolveSessionSecret(secrets)

	if secret == "" || secret == "change-me-in-production" {
		t.Fatalf("expected a generated non-default secret, got %q", secret)
	}
	if secrets.SessionSecret != secret {
		t.Fatalf("secret not written back to the struct: %q vs %q", secrets.SessionSecret, secret)
	}

	// It must have been persisted so the key is stable across restarts.
	reloaded, err := LoadSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SessionSecret != secret {
		t.Fatalf("secret not persisted: reloaded %q, want %q", reloaded.SessionSecret, secret)
	}

	// A second resolve from the reloaded config returns the same key, not a new one.
	if got := resolveSessionSecret(reloaded); got != secret {
		t.Fatalf("second resolve regenerated the secret: got %q, want %q", got, secret)
	}
}

func TestResolveSessionSecret_NilSecretsDoesNotPanicOrPersist(t *testing.T) {
	isolateStores(t)
	os.Unsetenv("SESSION_SECRET")

	secret := resolveSessionSecret(nil)
	if secret == "" {
		t.Fatal("expected an ephemeral secret for nil secrets, got empty")
	}
	// Nothing should have been written when the secrets store was unavailable (nil).
	path, err := secretsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("nil secrets should not persist a secrets file (stat err=%v)", err)
	}
}

// TestMigrateSecretsFromSharedConfig verifies secrets left in the shared
// config/local.json by an older build are moved to the per-user store and
// scrubbed from the shared file.
func TestMigrateSecretsFromSharedConfig(t *testing.T) {
	isolateStores(t)
	os.Unsetenv("SESSION_SECRET")

	// Seed a shared config with both shared config and legacy secret keys.
	if err := os.MkdirAll("config", 0755); err != nil {
		t.Fatal(err)
	}
	shared := `{
  "db_server": "sql1",
  "db_name": "ArxProd",
  "db_user": "sa",
  "db_password": "sekret",
  "test_db_password": "devsekret",
  "session_secret": "old-shared-secret"
}`
	if err := os.WriteFile(localConfigPath, []byte(shared), 0600); err != nil {
		t.Fatal(err)
	}

	sc, err := LoadSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if sc.DBPassword != "sekret" || sc.TestDBPassword != "devsekret" || sc.SessionSecret != "old-shared-secret" {
		t.Fatalf("secrets not migrated: %+v", sc)
	}

	// The per-user file exists with the migrated secrets.
	path, _ := secretsPath()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("per-user secrets file not created: %v", err)
	}

	// The shared config no longer carries any secret keys, but keeps shared config.
	lc, err := LoadLocal()
	if err != nil {
		t.Fatal(err)
	}
	if lc.DBServer != "sql1" || lc.DBName != "ArxProd" || lc.DBUser != "sa" {
		t.Fatalf("shared config lost non-secret fields: %+v", lc)
	}
	raw, err := os.ReadFile(localConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"db_password", "test_db_password", "session_secret"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("shared config still contains secret key %q:\n%s", key, raw)
		}
	}

	// A second load reads straight from the per-user file (no re-migration needed).
	sc2, err := LoadSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if sc2.DBPassword != "sekret" {
		t.Fatalf("second load lost migrated secret: %+v", sc2)
	}
}
