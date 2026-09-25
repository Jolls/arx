//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	arxbase "arx/internal/config"
	arxdb "arx/internal/db"
)

// arxDevProfile parses ARX_TEST_DSN (e.g.
// sqlserver://user:pass@server?database=ArxDev&...) into its component
// fields. TestMain already confirmed ARX_TEST_DSN points at seeded test data
// (see checkArxDevSentinel in integration_test.go) before any test runs.
func arxDevProfile(t *testing.T) (server, user, password, database string) {
	t.Helper()
	dsn := os.Getenv("ARX_TEST_DSN")
	if dsn == "" {
		t.Skip("set ARX_TEST_DSN to run integration tests")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse ARX_TEST_DSN: %v", err)
	}
	server = u.Host
	if u.User != nil {
		user = u.User.Username()
		password, _ = u.User.Password()
	}
	database = u.Query().Get("database")
	return server, user, password, database
}

// isolatedIntegrationSettingsHandler builds a Handler pointed at a live ArxDev
// connection, isolating config/local.json and the per-user secrets file to a
// temp dir so a successful SettingsSave swap never writes the real ArxDev
// password into the developer's actual secrets store.
func isolatedIntegrationSettingsHandler(t *testing.T) *Handler {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dsn := os.Getenv("ARX_TEST_DSN")
	cfg := arxbase.Load("dev")
	cfg.SessionSecret = "test-secret"

	database, dialect, err := arxdb.Connect(cfg.DBEngine(), dsn)
	if err != nil {
		t.Fatalf("initial db.Connect: %v", err)
	}
	h := New(database, dialect, cfg, templatesFS, nil)
	return h
}

// TestIntegration_SettingsSave_TestModeSwap covers issue #805 case 1: posting
// a valid test-mode ArxDev connection profile swaps the live connection,
// persists the posted secret/local fields, and re-runs the post-connect side
// effects (schema check, part categories) without panicking.
func TestIntegration_SettingsSave_TestModeSwap(t *testing.T) {
	server, user, password, database := arxDevProfile(t)
	h := isolatedIntegrationSettingsHandler(t)
	h.cfg.TestMode = true
	h.cfg.TestDBServer = server
	h.cfg.TestDBUser = user
	h.cfg.TestDBName = database

	oldConn := h.conn.Load()

	vals := url.Values{
		"test_mode":        {"1"},
		"test_db_server":   {server},
		"test_db_user":     {user},
		"test_db_name":     {database},
		"test_db_password": {password},
	}
	rec := httptest.NewRecorder()
	h.SettingsSave(rec, postSettings(vals))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("SettingsSave(test-mode swap): status %d, want %d. body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("SettingsSave(test-mode swap): redirect Location = %q, want \"/\"", loc)
	}

	newConn := h.conn.Load()
	if newConn == oldConn {
		t.Error("SettingsSave(test-mode swap): h.conn was not swapped to a new pointer")
	}
	if err := h.database().PingContext(context.Background()); err != nil {
		t.Errorf("SettingsSave(test-mode swap): new connection does not ping: %v", err)
	}
	if err := checkArxDevSentinel(context.Background(), h); err != nil {
		t.Fatalf("SettingsSave(test-mode swap): swapped connection: %v", err)
	}

	secrets, err := arxbase.LoadSecrets()
	if err != nil {
		t.Fatalf("LoadSecrets after swap: %v", err)
	}
	if secrets.TestDBPassword != password {
		t.Errorf("secrets.TestDBPassword = %q, want the posted ArxDev password", secrets.TestDBPassword)
	}

	local, err := arxbase.LoadLocal()
	if err != nil {
		t.Fatalf("LoadLocal after swap: %v", err)
	}
	if local.TestDBServer != server || local.TestDBUser != user || local.TestDBName != database {
		t.Errorf("local test profile = %+v, want server=%q user=%q name=%q", local, server, user, database)
	}

	if h.partCategories == nil {
		t.Error("SettingsSave(test-mode swap): partCategories not populated — loadPartCategories side effect did not run")
	}
}

// TestIntegration_SettingsSave_TestModeToggleForcesRelogin covers issue #805
// case 3: flipping test_mode on a request that swaps the DB clears the
// session's user_id/csrf_token and redirects to /login, since auth is
// per-database (#631).
func TestIntegration_SettingsSave_TestModeToggleForcesRelogin(t *testing.T) {
	server, user, password, database := arxDevProfile(t)
	h := isolatedIntegrationSettingsHandler(t)
	h.cfg.TestMode = false
	h.cfg.DBServer = server
	h.cfg.DBUser = user
	h.cfg.DBName = database
	h.cfg.DBPassword = password

	seed := httptest.NewRequest(http.MethodPost, "/settings/save", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	sess.Values["csrf_token"] = "some-token"
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save seed session: %v", err)
	}

	vals := url.Values{
		"test_mode":    {"1"},
		"test_db_name": {database},
	}
	req := postSettings(vals)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	// Stash the session user on the context the way RequireAuthOnceConnected's
	// withUser does: SettingsSave only reuses the stored password for an
	// authenticated caller, and this case posts no password (#852).
	req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, &User{ID: 7, Username: "admin"}))
	rec := httptest.NewRecorder()
	h.SettingsSave(rec, req)

	if err := checkArxDevSentinel(context.Background(), h); err != nil {
		t.Fatalf("SettingsSave(relogin): swapped connection: %v", err)
	}

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("SettingsSave(relogin): status %d, want %d. body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Errorf("SettingsSave(relogin): redirect Location = %q, want it to start with \"/login\"", loc)
	}
	if !strings.Contains(loc, "notice=") {
		t.Errorf("SettingsSave(relogin): redirect Location = %q, want a notice= query param", loc)
	}

	replay := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		replay.AddCookie(c)
	}
	sess2 := h.session(replay)
	if _, ok := sess2.Values["user_id"]; ok {
		t.Error("SettingsSave(relogin): user_id still present in session after test-mode DB swap")
	}
	if _, ok := sess2.Values["csrf_token"]; ok {
		t.Error("SettingsSave(relogin): csrf_token still present in session after test-mode DB swap")
	}
}
