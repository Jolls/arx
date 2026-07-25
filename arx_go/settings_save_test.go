package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
)

// isolatedSettingsHandler returns a Handler whose config/local.json and
// per-user secrets file are isolated to a fresh temp dir, so SettingsSave
// tests never read or clobber the developer's real config/secrets.
func isolatedSettingsHandler(t *testing.T, cfg *arxbase.Config) *Handler {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg.SessionSecret = "test-secret"
	return New(nil, nil, cfg, templatesFS, nil)
}

// postSettings builds a POST /settings/save request with a URL-encoded body.
func postSettings(vals url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/settings/save", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// TestSettingsSave_ConnectFailure exercises the connection-failure path
// (issue #805 case 2): a stubbed connectDB returns an error, so SettingsSave
// must render the error, leave h.conn nil, persist the posted local.json
// overrides, and NOT persist any secrets (those are only written on success).
func TestSettingsSave_ConnectFailure(t *testing.T) {
	cfg := &arxbase.Config{}
	cfg.TestMode = true
	cfg.TestDBName = "ArxDev"
	h := isolatedSettingsHandler(t, cfg)
	h.connectDB = func(engine, dsn string) (*sql.DB, arxdb.Dialect, error) {
		return nil, nil, errors.New("connection refused")
	}

	vals := url.Values{
		"test_mode":        {"1"},
		"test_db_server":   {"127.0.0.1:1"},
		"test_db_name":     {"ArxDev"},
		"test_db_user":     {"sa"},
		"test_db_password": {"whatever"},
	}
	rec := httptest.NewRecorder()
	h.SettingsSave(rec, postSettings(vals))

	if rec.Code != http.StatusOK {
		t.Fatalf("SettingsSave(connect failure): status %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Connection failed:") {
		t.Errorf("SettingsSave(connect failure): body missing \"Connection failed:\", got: %s", rec.Body.String())
	}
	if h.database() != nil {
		t.Error("SettingsSave(connect failure): h.database() is non-nil, want still unconnected")
	}

	local, err := arxbase.LoadLocal()
	if err != nil {
		t.Fatalf("LoadLocal after save: %v", err)
	}
	if local.TestDBServer != "127.0.0.1:1" {
		t.Errorf("local.TestDBServer = %q, want %q (persisted despite connect failure)", local.TestDBServer, "127.0.0.1:1")
	}

	secrets, err := arxbase.LoadSecrets()
	if err != nil {
		t.Fatalf("LoadSecrets after save: %v", err)
	}
	if secrets.TestDBPassword != "" || secrets.DBPassword != "" {
		t.Errorf("secrets = %+v, want no passwords persisted on connect failure", secrets)
	}
}

// TestSettingsSave_FieldSemantics covers issue #805 case 5: a blank-clearable
// field (test_db_server) is cleared by a blank submit, while an "only if
// non-empty" field (test_db_name) is left untouched by the same blank submit.
func TestSettingsSave_FieldSemantics(t *testing.T) {
	cfg := &arxbase.Config{}
	h := isolatedSettingsHandler(t, cfg)

	if err := arxbase.SaveLocal(&arxbase.LocalConfig{
		TestDBServer: "oldsrv",
		TestDBName:   "ArxDev",
	}); err != nil {
		t.Fatalf("seed local.json: %v", err)
	}

	vals := url.Values{
		"test_db_server": {""},
		"test_db_name":   {""},
	}
	rec := httptest.NewRecorder()
	h.SettingsSave(rec, postSettings(vals))

	if rec.Code != http.StatusOK {
		t.Fatalf("SettingsSave(field semantics): status %d, want 200. body: %s", rec.Code, rec.Body.String())
	}

	local, err := arxbase.LoadLocal()
	if err != nil {
		t.Fatalf("LoadLocal after save: %v", err)
	}
	if local.TestDBServer != "" {
		t.Errorf("local.TestDBServer = %q, want \"\" (blank-clearable field was posted blank)", local.TestDBServer)
	}
	if local.TestDBName != "ArxDev" {
		t.Errorf("local.TestDBName = %q, want \"ArxDev\" (only-if-non-empty field must not be cleared by a blank submit)", local.TestDBName)
	}
}

// TestSelectConnectPassword_Precedence covers issue #805 case 4 directly
// against the extracted helper: prod mode uses posted-then-stored; test mode
// uses posted-test, then stored-test, then posted-prod, then stored-prod.
func TestSelectConnectPassword_Precedence(t *testing.T) {
	cases := []struct {
		name                                            string
		testMode                                        bool
		testPosted, storedTest, posted, storedDB, want   string
	}{
		{"prod: posted wins", false, "", "", "newpw", "oldpw", "newpw"},
		{"prod: falls back to stored", false, "", "", "", "oldpw", "oldpw"},
		{"test: fresh test password wins", true, "newtest", "oldtest", "prodposted", "proddb", "newtest"},
		{"test: falls back to stored test", true, "", "oldtest", "prodposted", "proddb", "oldtest"},
		{"test: falls back to posted prod", true, "", "", "prodposted", "proddb", "prodposted"},
		{"test: falls back to stored prod", true, "", "", "", "proddb", "proddb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := selectConnectPassword(c.testMode, c.testPosted, c.storedTest, c.posted, c.storedDB)
			if got != c.want {
				t.Errorf("selectConnectPassword(%v, %q, %q, %q, %q) = %q, want %q",
					c.testMode, c.testPosted, c.storedTest, c.posted, c.storedDB, got, c.want)
			}
		})
	}
}
