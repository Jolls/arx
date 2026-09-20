package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	arxbase "arx/arxlib/config"
)

// failingDriver's Open always fails with an error shaped like a real driver
// error, carrying the server, login and database name.
type failingDriver struct{}

func (failingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unable to open tcp connection with host 'sql-prod.internal:1433': login error for user 'arxsvc' on database 'ArxSecret'")
}

func init() {
	sql.Register("failing-driver", failingDriver{})
}

// TestLoginPost_DBErrorDoesNotLeakConnectionDetails covers #110: a driver
// error on the pre-auth path must reach the log but not the response body.
func TestLoginPost_DBErrorDoesNotLeakConnectionDetails(t *testing.T) {
	db, err := sql.Open("failing-driver", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &arxbase.Config{}
	cfg.SessionSecret = "test-secret"
	h := New(db, nil, cfg, nil, nil)

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		logBuf.Reset()
		req := httptest.NewRequest(method, "/login", strings.NewReader("username=alice&password=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			h.LoginGet(rec, req)
		} else {
			h.LoginPost(rec, req)
		}

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500", method, rec.Code)
		}
		for _, secret := range []string{"sql-prod", "arxsvc", "ArxSecret"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s: body leaks %q: %q", method, secret, rec.Body.String())
			}
		}
		if !strings.Contains(logBuf.String(), "sql-prod") {
			t.Errorf("%s: log missing driver error detail: %q", method, logBuf.String())
		}
	}
}

// TestCachedUserByID_ReturnsFreshEntryWithoutDB seeds the cache directly (no DB
// call reachable) and confirms cachedUserByID returns the cached user instead
// of calling userByID.
func TestCachedUserByID_ReturnsFreshEntryWithoutDB(t *testing.T) {
	h := testHandler() // db == nil; userByID would panic/fail if called
	want := &User{ID: 1, Username: "alice"}
	h.userCache[1] = &userCacheEntry{user: want, expires: time.Now().Add(time.Minute)}

	got, err := h.cachedUserByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want the cached pointer %+v", got, want)
	}
}

func TestInvalidateUserCache_RemovesEntry(t *testing.T) {
	h := testHandler()
	h.userCache[3] = &userCacheEntry{user: &User{ID: 3}, expires: time.Now().Add(time.Minute)}

	h.invalidateUserCache(3)

	if _, ok := h.userCache[3]; ok {
		t.Error("entry still present after invalidateUserCache")
	}
}

func TestLoginPost_RedirectsToSettingsWhenNoDB(t *testing.T) {
	h := testHandler() // db == nil
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	rec := httptest.NewRecorder()
	h.LoginPost(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("Location = %q, want /settings", loc)
	}
}

// TestLoginPost_BadFormData confirms r.ParseForm()'s error path is reached
// (and returns 400) before any DB work, using a malformed percent-escape that
// ParseForm reliably rejects (verified experimentally: a malformed multipart
// body does NOT error ParseForm, but a bad %-escape in a urlencoded body does).
func TestLoginPost_BadFormData(t *testing.T) {
	h := testHandlerWithDB()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.LoginPost(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "bad form data") {
		t.Errorf("body = %q, want it to mention bad form data", rec.Body.String())
	}
}

func TestLogout_ClearsSessionAndRedirects(t *testing.T) {
	h := testHandler()

	// Seed a session cookie carrying user_id.
	seed := httptest.NewRequest(http.MethodPost, "/logout", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}

	// Replay the post-logout cookie and confirm user_id was actually deleted,
	// not just that a redirect happened.
	checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		checkReq.AddCookie(c)
	}
	checkSess := h.session(checkReq)
	if _, ok := checkSess.Values["user_id"]; ok {
		t.Error("user_id still present in session after Logout")
	}
}

func TestRequireAdmin_RejectsNonAdminAndNilUser(t *testing.T) {
	cases := []struct {
		name string
		user *User
		want bool
	}{
		{"nil user", nil, false},
		{"non-admin user", &User{ID: 1, IsAdmin: false}, false},
		{"admin user", &User{ID: 1, IsAdmin: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := testHandler()
			req := httptest.NewRequest(http.MethodPost, "/settings/users", nil)
			if c.user != nil {
				req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, c.user))
			}
			rec := httptest.NewRecorder()
			got := h.requireAdmin(rec, req)

			if got != c.want {
				t.Errorf("requireAdmin() = %v, want %v", got, c.want)
			}
			if !c.want && rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestSettingsUsers_ForbiddenForNonAdmin confirms every SettingsUsers* handler
// rejects an anonymous caller via requireAdmin before touching the DB (db is
// nil here — a real query would panic/error loudly, making a false pass
// detectable). auth.go currently defines six SettingsUsers* handlers, not
// five as issue #758's text says (see plan's Open Questions) — all six are
// covered.
func TestSettingsUsers_ForbiddenForNonAdmin(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"Create":               nil,
		"ResetPassword":        nil,
		"ToggleActive":         nil,
		"ToggleApprove":        nil,
		"ToggleApproveRecords": nil,
		"ToggleAdmin":          nil,
	}
	h := testHandler() // db == nil
	handlers["Create"] = h.SettingsUsersCreate
	handlers["ResetPassword"] = h.SettingsUsersResetPassword
	handlers["ToggleActive"] = h.SettingsUsersToggleActive
	handlers["ToggleApprove"] = h.SettingsUsersToggleApprove
	handlers["ToggleApproveRecords"] = h.SettingsUsersToggleApproveRecords
	handlers["ToggleAdmin"] = h.SettingsUsersToggleAdmin

	for name, fn := range handlers {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/settings/users", nil)
			rec := httptest.NewRecorder()
			fn(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}
