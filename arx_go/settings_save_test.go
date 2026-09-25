package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	arxbase "arx/internal/config"
	arxdb "arx/internal/db"
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
	h := New(nil, nil, cfg, templatesFS, nil)
	if err := h.loadTemplates(); err != nil {
		panic(err)
	}
	return h
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
		name                                          string
		testMode, allowStored                         bool
		testPosted, storedTest, posted, storedDB, want string
	}{
		{"prod: posted wins", false, true, "", "", "newpw", "oldpw", "newpw"},
		{"prod: falls back to stored", false, true, "", "", "", "oldpw", "oldpw"},
		{"test: fresh test password wins", true, true, "newtest", "oldtest", "prodposted", "proddb", "newtest"},
		{"test: falls back to stored test", true, true, "", "oldtest", "prodposted", "proddb", "oldtest"},
		{"test: falls back to posted prod", true, true, "", "", "prodposted", "proddb", "prodposted"},
		{"test: falls back to stored prod", true, true, "", "", "", "proddb", "proddb"},
		// allowStored=false (anonymous caller, #852): stored secrets are ignored.
		{"prod: anonymous ignores stored", false, false, "", "", "", "oldpw", ""},
		{"prod: anonymous still uses posted", false, false, "", "", "newpw", "oldpw", "newpw"},
		{"test: anonymous ignores both stored", true, false, "", "oldtest", "", "proddb", ""},
		{"test: anonymous still uses posted test", true, false, "newtest", "oldtest", "", "proddb", "newtest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := selectConnectPassword(c.testMode, c.allowStored, c.testPosted, c.storedTest, c.posted, c.storedDB)
			if got != c.want {
				t.Errorf("selectConnectPassword(%v, %v, %q, %q, %q, %q) = %q, want %q",
					c.testMode, c.allowStored, c.testPosted, c.storedTest, c.posted, c.storedDB, got, c.want)
			}
		})
	}
}

// TestSettingsSave_AnonymousNoStoredPasswordReconnect covers the #852
// credential-exfiltration case: the auth bypass for a broken connection means
// an anonymous caller can POST /settings, so a blank db_password must NOT fall
// back to the stored secret. Otherwise the caller could repoint db_server at a
// host they control and have the app hand over the real password.
func TestSettingsSave_AnonymousNoStoredPasswordReconnect(t *testing.T) {
	cfg := &arxbase.Config{}
	cfg.DBPassword = "stored-secret"
	h := isolatedSettingsHandler(t, cfg)
	h.update(func(s *runtimeState) { s.dbConnError = "could not read schema_version (connection refused)" })

	var dialed []string
	h.connectDB = func(engine, dsn string) (*sql.DB, arxdb.Dialect, error) {
		dialed = append(dialed, dsn)
		return nil, nil, errors.New("should not be dialed")
	}

	vals := url.Values{
		"db_server":   {"attacker.example.com"},
		"db_name":     {"ArxDev"},
		"db_user":     {"sa"},
		"db_password": {""},
	}
	rec := httptest.NewRecorder()
	h.SettingsSave(rec, postSettings(vals)) // no ctxUserKey on the context: anonymous

	if len(dialed) != 0 {
		t.Errorf("connectDB called with %v; an anonymous caller must not trigger a reconnect using the stored password", dialed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "enter your database password") {
		t.Errorf("body missing the \"enter your database password\" prompt, got: %s", rec.Body.String())
	}
}

// TestSettingsSave_AuthenticatedReusesStoredPassword is the counterpart: a
// logged-in admin keeps today's blank-means-reuse-stored-password convenience.
func TestSettingsSave_AuthenticatedReusesStoredPassword(t *testing.T) {
	cfg := &arxbase.Config{}
	cfg.DBPassword = "stored-secret"
	h := isolatedSettingsHandler(t, cfg)

	var dialed []string
	h.connectDB = func(engine, dsn string) (*sql.DB, arxdb.Dialect, error) {
		dialed = append(dialed, dsn)
		return nil, nil, errors.New("connection refused")
	}

	vals := url.Values{
		"db_server":   {"127.0.0.1:1"},
		"db_name":     {"ArxDev"},
		"db_user":     {"sa"},
		"db_password": {""},
	}
	req := postSettings(vals)
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, &User{ID: 8001, Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	h.SettingsSave(rec, req)

	if len(dialed) != 1 {
		t.Fatalf("connectDB called %d times, want 1: a logged-in admin's blank password must still reuse the stored one", len(dialed))
	}
	if !strings.Contains(dialed[0], "stored-secret") {
		t.Errorf("DSN %q does not carry the stored password", dialed[0])
	}
}
