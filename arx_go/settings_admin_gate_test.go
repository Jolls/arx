package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sessionFor builds a request carrying a signed session cookie for u, with u
// seeded into the user cache so withUser resolves without dialing the DB.
// Mirrors TestRequireAuthOnceConnected_AllowsLoggedInUser's setup.
func sessionFor(t *testing.T, h *Handler, method, path string, u *User) *http.Request {
	t.Helper()
	h.userCache[u.ID] = &userCacheEntry{user: u, expires: time.Now().Add(time.Minute)}

	seed := httptest.NewRequest(method, path, nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = u.ID
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	req := httptest.NewRequest(method, path, nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

// Repointing the database connection is an admin action once the app has a
// usable one — otherwise any user could aim Arx at a database they control and
// harvest what the app writes there (#106).
func TestRequireAdminOnceConnected(t *testing.T) {
	t.Run("first run: no DB, anyone may set the connection up", func(t *testing.T) {
		h := testHandler() // database() == nil
		var reached bool
		rec := httptest.NewRecorder()
		h.RequireAdminOnceConnected(sentinel(&reached)).ServeHTTP(
			rec, httptest.NewRequest(http.MethodPost, "/settings", nil))
		if !reached {
			t.Error("first-run setup must stay reachable; there is no admin account yet")
		}
	})

	t.Run("broken connection: anyone may fix it", func(t *testing.T) {
		h := testHandlerWithDB()
		h.dbConnError = "could not read schema_version (connection refused)"
		var reached bool
		rec := httptest.NewRecorder()
		h.RequireAdminOnceConnected(sentinel(&reached)).ServeHTTP(
			rec, httptest.NewRequest(http.MethodPost, "/settings", nil))
		if !reached {
			t.Error("a broken connection must stay fixable without an admin login")
		}
	})

	t.Run("connected: non-admin is refused", func(t *testing.T) {
		h := testHandlerWithDB()
		req := sessionFor(t, h, http.MethodPost, "/settings", &User{ID: 7, Username: "tester"})
		var reached bool
		rec := httptest.NewRecorder()
		h.RequireAdminOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)
		if reached {
			t.Error("a non-admin reached POST /settings")
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("connected: admin is allowed", func(t *testing.T) {
		h := testHandlerWithDB()
		req := sessionFor(t, h, http.MethodPost, "/settings", &User{ID: 8, Username: "admin", IsAdmin: true})
		var reached bool
		rec := httptest.NewRecorder()
		h.RequireAdminOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)
		if !reached {
			t.Errorf("admin did not reach POST /settings (status %d)", rec.Code)
		}
	})
}

// The remaining shop-wide settings endpoints gate in-handler via requireAdmin.
// Categories, part numbering and company logo are deliberately NOT in this list:
// they are shop data a technician may edit, not a security boundary (#106).
func TestSettingsEndpointsRequireAdmin(t *testing.T) {
	endpoints := []struct {
		name    string
		method  string
		path    string
		handler func(*Handler) func(http.ResponseWriter, *http.Request)
	}{
		{"digikey credentials", http.MethodPost, "/settings/digikey",
			func(h *Handler) func(http.ResponseWriter, *http.Request) { return h.SettingsDigiKeySave }},
		{"data backup", http.MethodGet, "/settings/backup",
			func(h *Handler) func(http.ResponseWriter, *http.Request) { return h.SettingsBackup }},
		{"utilities report", http.MethodGet, "/settings/utilities",
			func(h *Handler) func(http.ResponseWriter, *http.Request) { return h.UtilitiesReport }},
	}

	for _, e := range endpoints {
		t.Run(e.name+"/non-admin is refused", func(t *testing.T) {
			h := testHandler()
			req := httptest.NewRequest(e.method, e.path, nil)
			req = req.WithContext(context.WithValue(req.Context(),
				ctxUserKey, &User{ID: 7, Username: "tester"}))
			rec := httptest.NewRecorder()
			e.handler(h)(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})

		t.Run(e.name+"/anonymous is refused", func(t *testing.T) {
			h := testHandler()
			rec := httptest.NewRecorder()
			e.handler(h)(rec, httptest.NewRequest(e.method, e.path, nil))
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}
