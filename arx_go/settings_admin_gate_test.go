package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	arxbase "arx/internal/config"
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
		h.update(func(s *runtimeState) { s.dbConnError = "could not read schema_version (connection refused)" })
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

// requestAs builds a request through the real router's CSRF and auth layers: a
// signed session for u carrying a CSRF token, plus the matching form token on
// POSTs, so the request reaches the admin middleware rather than being refused
// earlier.
func requestAs(t *testing.T, h *Handler, method, path string, u *User) *http.Request {
	t.Helper()
	h.userCache[u.ID] = &userCacheEntry{user: u, expires: time.Now().Add(time.Minute)}

	seed := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = u.ID
	sess.Values["csrf_token"] = "test-token"
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, path, strings.NewReader("csrf_token=test-token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Host = "localhost:4568"
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

// Every admin-only endpoint must refuse a non-admin, with the response shape its
// callers expect: plain text for form posts and downloads, JSON where the Settings
// page parses the body (#106, #120). Driven through buildRouter so this also proves
// the routes are actually mounted behind the admin groups in main.go.
//
// Categories, part numbering and company logo are deliberately NOT in this list:
// they are shop data a technician may edit, not a security boundary (#106).
func TestAdminOnlyRoutes_RefuseNonAdmin(t *testing.T) {
	routes := []struct {
		method, path string
		json         bool
	}{
		{http.MethodPost, "/settings/digikey", false},
		{http.MethodGet, "/settings/backup", false},
		{http.MethodGet, "/settings/utilities", false},
		{http.MethodPost, "/settings/users", false},
		{http.MethodPost, "/settings/users/1/password", false},
		{http.MethodPost, "/settings/users/1/toggle-active", false},
		{http.MethodPost, "/settings/users/1/toggle-approve", false},
		{http.MethodPost, "/settings/users/1/toggle-approve-records", false},
		{http.MethodPost, "/settings/users/1/toggle-admin", false},
		{http.MethodPost, "/settings/named-queries/save", true},
		{http.MethodPost, "/settings/named-queries/test", true},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			h := testHandlerWithDB()
			r, _ := buildRouter(h)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, requestAs(t, h, rt.method, rt.path, &User{ID: 7, Username: "tester"}))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
			if !rt.json {
				if got := rec.Body.String(); got != "forbidden\n" {
					t.Errorf("body = %q, want %q", got, "forbidden\n")
				}
				return
			}
			// The settings page parses every response with r.json(), so the
			// rejection has to keep the JSON error shape.
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body["error"] == "" {
				t.Errorf("expected an error message, got %v", body)
			}
		})
	}
}

func TestRequireAdminMiddlewares(t *testing.T) {
	middlewares := []struct {
		name string
		wrap func(*Handler) func(http.Handler) http.Handler
	}{
		{"RequireAdmin", func(h *Handler) func(http.Handler) http.Handler { return h.RequireAdmin }},
		{"RequireAdminJSON", func(h *Handler) func(http.Handler) http.Handler { return h.RequireAdminJSON }},
	}
	callers := []struct {
		name string
		user *User
		want bool
	}{
		{"nil user", nil, false},
		{"non-admin user", &User{ID: 1}, false},
		{"admin user", &User{ID: 1, IsAdmin: true}, true},
	}

	for _, m := range middlewares {
		for _, c := range callers {
			t.Run(m.name+"/"+c.name, func(t *testing.T) {
				h := testHandler()
				req := httptest.NewRequest(http.MethodPost, "/x", nil)
				if c.user != nil {
					req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, c.user))
				}
				var reached bool
				rec := httptest.NewRecorder()
				m.wrap(h)(sentinel(&reached)).ServeHTTP(rec, req)

				if reached != c.want {
					t.Errorf("reached next = %v, want %v", reached, c.want)
				}
				if !c.want && rec.Code != http.StatusForbidden {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
				}
			})
		}
	}
}

// A signed-out caller on a live connection reaches Settings so they can fix a
// connection they can't sign in over (#401); a signed-in non-admin still can't
// repoint it.
func TestRequireConnectionEditor(t *testing.T) {
	t.Run("connected, signed out: allowed", func(t *testing.T) {
		h := testHandlerWithDB()
		var reached bool
		rec := httptest.NewRecorder()
		h.RequireConnectionEditor(sentinel(&reached)).ServeHTTP(
			rec, httptest.NewRequest(http.MethodPost, "/settings", nil))
		if !reached {
			t.Errorf("signed-out POST /settings did not reach the handler (status %d)", rec.Code)
		}
	})

	t.Run("connected, non-admin: refused", func(t *testing.T) {
		h := testHandlerWithDB()
		req := sessionFor(t, h, http.MethodPost, "/settings", &User{ID: 7, Username: "tester"})
		var reached bool
		rec := httptest.NewRecorder()
		h.RequireConnectionEditor(sentinel(&reached)).ServeHTTP(rec, req)
		if reached || rec.Code != http.StatusForbidden {
			t.Errorf("reached=%v status=%d, want refused with 403", reached, rec.Code)
		}
	})

	t.Run("connected, admin: allowed", func(t *testing.T) {
		h := testHandlerWithDB()
		req := sessionFor(t, h, http.MethodPost, "/settings", &User{ID: 8, Username: "admin", IsAdmin: true})
		var reached bool
		h.RequireConnectionEditor(sentinel(&reached)).ServeHTTP(httptest.NewRecorder(), req)
		if !reached {
			t.Error("admin did not reach POST /settings")
		}
	})
}

func TestAllowSignedOutConnection_GET(t *testing.T) {
	h := testHandlerWithDB()
	var reached bool
	rec := httptest.NewRecorder()
	h.AllowSignedOutConnection(sentinel(&reached)).ServeHTTP(
		rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if !reached || rec.Code == http.StatusSeeOther {
		t.Errorf("signed-out GET /settings reached=%v status=%d, want reached with no redirect", reached, rec.Code)
	}
}

// A signed-out save must not be able to change the connection target (#401): the
// form shows it read-only, so any difference is a stale or forged post.
func TestConnectionTargetChanged(t *testing.T) {
	cur := &arxbase.Config{DBServer: "db1", DBName: "ArxProd", DBUser: "u", TestDBServer: "", TestDBName: "ArxDev", TestDBUser: ""}
	cases := []struct {
		name                                             string
		server, db, user, testServer, testName, testUser string
		want                                             bool
	}{
		{"all same", "db1", "ArxProd", "u", "", "ArxDev", "", false},
		{"prod blanks keep", "", "", "", "", "", "", false},
		{"prod server changed", "evil", "ArxProd", "u", "", "ArxDev", "", true},
		{"prod name changed", "db1", "Other", "u", "", "ArxDev", "", true},
		{"prod user changed", "db1", "ArxProd", "x", "", "ArxDev", "", true},
		{"test server set", "db1", "ArxProd", "u", "evil", "ArxDev", "", true},
		{"test name changed", "db1", "ArxProd", "u", "", "Other", "", true},
		{"test user set", "db1", "ArxProd", "u", "", "ArxDev", "x", true},
	}
	for _, c := range cases {
		if got := connectionTargetChanged(cur, c.server, c.db, c.user, c.testServer, c.testName, c.testUser); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
