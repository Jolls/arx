package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// SecretsConfig holds the per-user secrets that must NOT live in the shared,
// exe-adjacent config/local.json. When Arx.exe runs from a shared folder (e.g.
// OneDrive) every user shares that file, so a plaintext DB password would be
// readable by anyone with folder access and a shared session_secret would let
// any user forge another user's session cookie (#732). These are stored per-user
// under os.UserConfigDir() instead (%APPDATA%\Arx on Windows, ~/.config/arx on
// Linux).
type SecretsConfig struct {
	DBPassword     string `json:"db_password,omitempty"`
	TestDBPassword string `json:"test_db_password,omitempty"`
	SessionSecret  string `json:"session_secret,omitempty"`
}

// secretsPath returns the per-user secrets file location. It errors only when
// os.UserConfigDir cannot resolve a base directory (e.g. %AppData% unset).
func secretsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Arx", "local.json"), nil
}

// LoadSecrets reads the per-user secrets file. When the file does not exist it
// runs the one-time migration of secrets out of the shared config/local.json
// (see migrateFromSharedConfig). A nil return means the secrets store is
// unavailable (unresolvable path or unreadable file) — callers degrade
// gracefully rather than treat it as fatal.
func LoadSecrets() (*SecretsConfig, error) {
	path, err := secretsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		return migrateFromSharedConfig(), nil
	}
	var sc SecretsConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

// SaveSecrets writes the per-user secrets file (0600) under a 0700 parent dir.
func SaveSecrets(sc *SecretsConfig) error {
	path, err := secretsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// MigrateDigiKeySecrets returns any DigiKey client ID/secret left over in the
// per-user secrets store from before #60 moved them to the shared app_config
// table, and scrubs them from the store once found (so this runs at most
// once per machine). Returns empty strings when there is nothing to migrate.
// The caller (which has DB access, unlike this package) is responsible for
// writing the returned values into app_config.
func MigrateDigiKeySecrets() (clientID, clientSecret string) {
	path, err := secretsPath()
	if err != nil {
		return "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var legacy struct {
		DigiKeyClientID     string `json:"digikey_client_id"`
		DigiKeyClientSecret string `json:"digikey_client_secret"`
	}
	if json.Unmarshal(data, &legacy) != nil || legacy.DigiKeyClientID == "" {
		return "", ""
	}

	// Re-save the current (already-stripped) SecretsConfig to scrub the legacy
	// keys from disk now that they've been read out.
	var sc SecretsConfig
	if json.Unmarshal(data, &sc) == nil {
		if err := SaveSecrets(&sc); err != nil {
			log.Printf("warning: could not scrub migrated DigiKey credentials from secrets store: %v", err)
		}
	}
	return legacy.DigiKeyClientID, legacy.DigiKeyClientSecret
}

// migrateFromSharedConfig performs the one-time relocation of secrets out of the
// shared config/local.json into the per-user store on first run after upgrade. It
// reads the shared file once, and only scrubs the secrets from it after they are
// safely persisted to the per-user store — if the persist fails the shared file
// is left untouched so nothing is lost. Scrubbing re-saves the parsed LocalConfig:
// it no longer declares these keys, so re-marshaling drops them from disk, removing
// the plaintext leak (and forcing every other user of a shared exe to supply their
// own secrets into their own per-user file). Always returns a non-nil config;
// an empty one when there is nothing to migrate (no file written in that case).
func migrateFromSharedConfig() *SecretsConfig {
	data, err := os.ReadFile(localConfigPath)
	if err != nil {
		return &SecretsConfig{}
	}
	var shared LocalConfig
	if json.Unmarshal(data, &shared) != nil {
		return &SecretsConfig{}
	}
	var legacy struct {
		DBPassword     string `json:"db_password"`
		TestDBPassword string `json:"test_db_password"`
		SessionSecret  string `json:"session_secret"`
	}
	_ = json.Unmarshal(data, &legacy) // same bytes; shared unmarshal already validated
	if legacy.DBPassword == "" && legacy.TestDBPassword == "" && legacy.SessionSecret == "" {
		return &SecretsConfig{}
	}

	sc := &SecretsConfig{
		DBPassword:     legacy.DBPassword,
		TestDBPassword: legacy.TestDBPassword,
		SessionSecret:  legacy.SessionSecret,
	}
	if err := SaveSecrets(sc); err != nil {
		log.Printf("warning: could not persist migrated secrets; leaving shared config untouched: %v", err)
		return sc
	}
	// Persisted safely — now scrub the secrets from the shared file.
	if err := SaveLocal(&shared); err != nil {
		log.Printf("warning: could not scrub secrets from shared config: %v", err)
	}
	return sc
}
