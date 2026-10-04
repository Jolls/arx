package main

import (
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	arxbase "arx/internal/config"
)

// testHandler builds a Handler with no DB and no templates — enough to exercise
// the auth and CSRF middleware, which return before any rendering.
func testHandler() *Handler {
	cfg := &arxbase.Config{}
	cfg.SessionSecret = "test-secret"
	return New(nil, cfg, nil, nil)
}

// nopDriver is a database/sql driver that is never actually dialed; it exists
// only so testHandlerWithDB can construct a non-nil *sql.DB.
type nopDriver struct{}

func (nopDriver) Open(name string) (driver.Conn, error) { return nil, nil }

func init() {
	sql.Register("nop-driver", nopDriver{})
}

// testHandlerWithDB builds a Handler with a non-nil (never-dialed) *sql.DB, so
// the h.database() == nil short-circuit in RequireAuth doesn't mask other checks.
func testHandlerWithDB() *Handler {
	db, err := sql.Open("nop-driver", "")
	if err != nil {
		panic(err)
	}
	cfg := &arxbase.Config{}
	cfg.SessionSecret = "test-secret"
	cfg.Port = "4568"
	return New(db, cfg, nil, nil)
}

func TestBuildRouter_RejectsForeignHost(t *testing.T) {
	r, _ := buildRouter(testHandlerWithDB())
	for _, path := range []string{"/login", "/settings"} {
		for host, want := range map[string]bool{
			"evil.example.com":      false,
			"evil.example.com:4568": false,
			"localhost":             false,
			"localhost:9999":        false,
			"localhost:4568":        true,
			"127.0.0.1:4568":        true,
			"[::1]:4568":            true,
		} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = host
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if got := rec.Code != http.StatusMisdirectedRequest; got != want {
				t.Errorf("GET %s Host %q: status = %d, allowed = %v, want allowed = %v", path, host, rec.Code, got, want)
			}
		}
	}
}

// TestBuildRouter_WildcardRoutesPreserveChiMatchingSemantics confirms the
// static-prefix wildcard file-serving routes still require their literal
// trailing slash. Go's "{rest...}" wildcard matches a bare "/local" request
// (no trailing slash at all) by itself, unlike chi's old "/local/*", which
// required the slash — buildRouter guards against that gap (#319); this
// confirms the guard is wired up for every affected route.
func TestBuildRouter_WildcardRoutesPreserveChiMatchingSemantics(t *testing.T) {
	h := testHandlerWithDB()
	h.userCache[7] = &userCacheEntry{user: &User{ID: 7}, expires: time.Now().Add(time.Minute)}

	seed := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}
	cookies := seedRec.Result().Cookies()

	r, _ := buildRouter(h)
	dial := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "localhost:4568"
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, path := range []string{"/local", "/local-dir", "/supplier-local", "/supplier-local-dir", "/images", "/static"} {
		if got := dial(path); got != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d (bare prefix must not match the wildcard route)", path, got, http.StatusNotFound)
		}
	}

	for _, path := range []string{"/local/", "/local-dir/", "/supplier-local/", "/supplier-local-dir/", "/images/"} {
		if got := dial(path); got == http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want anything but %d (trailing slash must still match the wildcard route)", path, got, http.StatusNotFound)
		}
	}
	if got := dial("/static/"); got != http.StatusOK {
		t.Errorf("GET /static/: status = %d, want %d", got, http.StatusOK)
	}
}

// TestBuildRouter_WildcardRoutesHaveBarePrefixSibling confirms every
// "{rest...}" route in the table has a matching bare-prefix (no trailing
// slash) sibling registered for the same method — the guard handleWildcard
// adds against net/http's implicit 307-redirect-to-slash (#319). A
// "{rest...}" route added by hand instead of through handleWildcard, without
// copying that sibling, would 307 instead of reaching a handler for the bare
// path the way chi's old "/x/*" did (#328).
func TestBuildRouter_WildcardRoutesHaveBarePrefixSibling(t *testing.T) {
	_, routes := buildRouter(testHandlerWithDB())

	registered := make(map[string]bool, len(routes))
	for _, rt := range routes {
		registered[rt.method+" "+rt.pattern] = true
	}

	for _, rt := range routes {
		if !strings.HasSuffix(rt.pattern, "/{rest...}") {
			continue
		}
		sibling := strings.TrimSuffix(rt.pattern, "/{rest...}")
		if !registered[rt.method+" "+sibling] {
			t.Errorf("%s %s has no bare-prefix sibling %q registered", rt.method, rt.pattern, sibling)
		}
	}
}

// TestBuildRouter_MethodMismatchIsNotFalseNotFound confirms withNotFound
// tells a true 404 apart from a path that's registered under a different
// method: net/http's ServeMux reports pattern=="" for both (#319), so a
// naive check would render the app's custom 404 page for a request like
// "GET /logout" (POST-only) instead of the 405 a client expects.
func TestBuildRouter_MethodMismatchIsNotFalseNotFound(t *testing.T) {
	r, _ := buildRouter(testHandlerWithDB())
	dial := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "localhost:4568"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := dial(http.MethodGet, "/logout")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /logout: status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "POST") {
		t.Errorf("GET /logout: Allow header = %q, want it to mention POST", allow)
	}

	rec = dial(http.MethodGet, "/this-path-does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /this-path-does-not-exist: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "404 page not found") {
		t.Errorf("GET /this-path-does-not-exist: got net/http's default 404 body, want the app's custom not_found page")
	}
}

// sentinel reports whether the wrapped next-handler was reached.
func sentinel(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireAuth_RedirectsWhenNoDB(t *testing.T) {
	h := testHandler() // db == nil

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; expected redirect to /settings instead")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("Location = %q, want /settings", loc)
	}
}

func TestRequireAuth_RedirectsOnSchemaMismatch(t *testing.T) {
	h := testHandlerWithDB()
	h.update(func(s *runtimeState) { s.schemaMismatch = "expected v5, found v4" })

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; expected redirect to /login instead")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRequireAuth_RedirectsToSettingsOnConnError(t *testing.T) {
	h := testHandlerWithDB()
	h.update(func(s *runtimeState) { s.dbConnError = "could not read schema_version (connection refused)" })

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; expected redirect to /settings instead")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("Location = %q, want /settings", loc)
	}
}

func TestRequireAuthOnceConnected_AllowsOnConnError(t *testing.T) {
	h := testHandlerWithDB()
	h.update(func(s *runtimeState) { s.dbConnError = "could not read schema_version (connection refused)" })

	var reached bool
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	rec := httptest.NewRecorder()
	h.RequireAuthOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Error("next handler did not run; /settings must be reachable without login when the connection itself is broken")
	}
}

func TestRequireAuthOnceConnected_StillRedirectsOnSchemaMismatchAlone(t *testing.T) {
	h := testHandlerWithDB()
	h.update(func(s *runtimeState) { s.schemaMismatch = "DB schema v4, app expects v5" }) // working connection, just old schema

	var reached bool
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	rec := httptest.NewRecorder()
	h.RequireAuthOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; a plain schema-version mismatch must still require login (#748 protection)")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRequireAuthOnceConnected_AllowsFirstRun(t *testing.T) {
	h := testHandler() // db == nil

	var reached bool
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	rec := httptest.NewRecorder()
	h.RequireAuthOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Error("next handler did not run; first-run POST /settings must be reachable while db == nil")
	}
}

func TestRequireAuthOnceConnected_RedirectsWhenConnectedAndAnonymous(t *testing.T) {
	h := testHandlerWithDB() // db != nil, no session user

	var reached bool
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	rec := httptest.NewRecorder()
	h.RequireAuthOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; expected redirect to /login (the password-exfil path must be blocked)")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRequireAuthOnceConnected_AllowsLoggedInUser(t *testing.T) {
	h := testHandlerWithDB() // db != nil
	// Seed the user cache so withUser resolves without dialing the DB.
	h.userCache[7] = &userCacheEntry{user: &User{ID: 7}, expires: time.Now().Add(time.Minute)}

	// Build a signed session cookie carrying user_id=7.
	seed := httptest.NewRequest(http.MethodPost, "/settings", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	var reached bool
	req := httptest.NewRequest(http.MethodPost, "/settings", nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.RequireAuthOnceConnected(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Errorf("next handler did not run; a logged-in user must reach POST /settings (status %d)", rec.Code)
	}
}

func TestRequireCsrfOnPost_RejectsBadPost(t *testing.T) {
	h := testHandler()

	cases := []struct {
		name string
		body string
	}{
		{"no token", ""},
		{"wrong token", "csrf_token=not-the-real-token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var reached bool
			req := httptest.NewRequest(http.MethodPost, "/part/1", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.RequireCsrfOnPost(sentinel(&reached)).ServeHTTP(rec, req)

			if reached {
				t.Error("next handler ran; expected 403")
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

func TestRequireCsrfOnPost_AllowsGet(t *testing.T) {
	h := testHandler()

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireCsrfOnPost(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Error("GET was blocked; CSRF check should only apply to POST")
	}
}

func TestRequireCsrfOnPost_AllowsValidPost(t *testing.T) {
	h := testHandler()

	// Seed a token into the session and capture the resulting cookie.
	seedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRec := httptest.NewRecorder()
	token := h.csrfToken(seedRec, seedReq)
	cookies := seedRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("csrfToken did not set a session cookie")
	}

	// Replay the session cookie on a POST carrying the matching token.
	var reached bool
	body := "csrf_token=" + url.QueryEscape(token)
	req := httptest.NewRequest(http.MethodPost, "/part/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.RequireCsrfOnPost(sentinel(&reached)).ServeHTTP(rec, req)

	if !reached {
		t.Errorf("valid POST was blocked; status = %d", rec.Code)
	}
}

// routeAllowlist lists the buildRouter routes that are intentionally reachable
// without auth (login/logout/static/whats-new). Everything else registered on
// the router must redirect an anonymous caller to /login.
var routeAllowlist = map[string]bool{
	"GET /login":     true,
	"POST /login":    true,
	"POST /logout":   true,
	"GET /whats-new": true,
	// The bare-prefix 404 sibling of the public /static/{rest...} route
	// (#319) — same no-auth tier as its wildcard counterpart.
	"GET /static": true,
}

// staticRoutePrefix is served directly (no auth) — skipped by exact pattern
// match rather than listing every method explicitly.
const staticRoutePrefix = "/static/{rest...}"

// substituteRouteParams replaces route params ({id}, {userID}, the "{$}"
// exact-root marker, the trailing "{rest...}" wildcard, ...) with a concrete
// placeholder value so the pattern can be dialed as a real request path. The
// concrete value never matters here: an anonymous request is rejected by
// RequireAuth before any handler (or its param parsing) runs.
func substituteRouteParams(pattern string) string {
	var out strings.Builder
	i := 0
	for i < len(pattern) {
		switch pattern[i] {
		case '{':
			end := strings.IndexByte(pattern[i:], '}')
			if end == -1 {
				out.WriteByte(pattern[i])
				i++
				continue
			}
			name := pattern[i+1 : i+end]
			switch {
			case name == "$":
				// matches only the exact path up to here; nothing to add.
			case strings.Contains(strings.ToLower(name), "id"):
				out.WriteString("1")
			default:
				out.WriteString("x")
			}
			i += end + 1
		default:
			out.WriteByte(pattern[i])
			i++
		}
	}
	return out.String()
}

func TestBuildRouter_AllAppRoutesRequireAuth(t *testing.T) {
	h := testHandlerWithDB()
	r, routes := buildRouter(h)

	// Seed a valid CSRF cookie/token once, reused for every POST route dialed
	// below, so the global RequireCsrfOnPost middleware doesn't reject the
	// request before RequireAuth gets a chance to run.
	seedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRec := httptest.NewRecorder()
	token := h.csrfToken(seedRec, seedReq)
	csrfCookies := seedRec.Result().Cookies()

	visited := 0
	for _, rt := range routes {
		method, route := rt.method, rt.pattern
		key := method + " " + route
		if routeAllowlist[key] || route == staticRoutePrefix {
			continue
		}
		visited++
		path := substituteRouteParams(route)

		var req *http.Request
		if method == http.MethodPost {
			body := "csrf_token=" + url.QueryEscape(token)
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for _, c := range csrfCookies {
				req.AddCookie(c)
			}
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Host = "localhost:4568"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s %s: status = %d, want %d (redirect to /login)", method, route, rec.Code, http.StatusSeeOther)
		} else if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("%s %s: Location = %q, want /login", method, route, loc)
		}
	}
	if visited < 150 {
		t.Errorf("only visited %d non-allowlisted routes, want >= 150 (route table may not have been walked)", visited)
	}
}

func TestRequireAuth_RedirectsWhenAnonymous(t *testing.T) {
	h := testHandlerWithDB() // db != nil, schemaMismatch empty, no session cookie

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(sentinel(&reached)).ServeHTTP(rec, req)

	if reached {
		t.Error("next handler ran; expected redirect to /login")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRequireAuth_ProceedsWhenLoggedIn(t *testing.T) {
	h := testHandlerWithDB()
	h.userCache[7] = &userCacheEntry{user: &User{ID: 7}, expires: time.Now().Add(time.Minute)}

	seed := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	sess.Values["last_activity"] = time.Now().Unix()
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	var reachedID int
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if u := h.currentUser(r); u != nil {
			reachedID = u.ID
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.RequireAuth(next).ServeHTTP(rec, req)

	if !reached {
		t.Fatalf("next handler did not run; status = %d", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if reachedID != 7 {
		t.Errorf("context user ID = %d, want 7", reachedID)
	}
}

func TestWithUser_IdleTimeoutLogsOut(t *testing.T) {
	h := testHandlerWithDB()
	h.userCache[7] = &userCacheEntry{user: &User{ID: 7}, expires: time.Now().Add(time.Minute)}

	seed := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	sess.Values["last_activity"] = time.Now().Add(-(sessionIdleTimeout + time.Hour)).Unix()
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	r2, u := h.withUser(rec, req)

	if u != nil {
		t.Errorf("user = %+v, want nil (idle timeout should log out)", u)
	}
	if r2 != req {
		t.Error("request was replaced; expected the original request back on idle logout")
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("no cookie written; expected the idle-logout session save to clear user_id")
	}

	// Decode the cookie the idle-logout path wrote and confirm user_id was deleted.
	replay := httptest.NewRequest(http.MethodGet, "/part/1", nil)
	for _, c := range rec.Result().Cookies() {
		replay.AddCookie(c)
	}
	sess2 := h.session(replay)
	if _, ok := sess2.Values["user_id"]; ok {
		t.Error("user_id still present in the saved session after idle logout")
	}
}

func TestWithUser_ActiveSessionProceeds(t *testing.T) {
	h := testHandlerWithDB()
	h.userCache[7] = &userCacheEntry{user: &User{ID: 7}, expires: time.Now().Add(time.Minute)}

	t.Run("fresh activity does not re-stamp", func(t *testing.T) {
		seed := httptest.NewRequest(http.MethodGet, "/part/1", nil)
		seedRec := httptest.NewRecorder()
		sess := h.session(seed)
		sess.Values["user_id"] = 7
		sess.Values["last_activity"] = time.Now().Add(-10 * time.Second).Unix()
		if err := sess.Save(seed, seedRec); err != nil {
			t.Fatalf("save session: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
		for _, c := range seedRec.Result().Cookies() {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		r2, u := h.withUser(rec, req)

		if u == nil || u.ID != 7 {
			t.Fatalf("user = %+v, want ID 7", u)
		}
		if h.currentUser(r2) == nil {
			t.Error("currentUser(r2) is nil; expected the user stashed on context")
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Error("cookie written for a fresh session; expected no re-stamp within activityStampGranularity")
		}
	})

	t.Run("stale activity re-stamps", func(t *testing.T) {
		seed := httptest.NewRequest(http.MethodGet, "/part/1", nil)
		seedRec := httptest.NewRecorder()
		sess := h.session(seed)
		sess.Values["user_id"] = 7
		staleActivity := time.Now().Add(-6 * time.Minute).Unix()
		sess.Values["last_activity"] = staleActivity
		if err := sess.Save(seed, seedRec); err != nil {
			t.Fatalf("save session: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/part/1", nil)
		for _, c := range seedRec.Result().Cookies() {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		r2, u := h.withUser(rec, req)

		if u == nil || u.ID != 7 {
			t.Fatalf("user = %+v, want ID 7", u)
		}
		if h.currentUser(r2) == nil {
			t.Error("currentUser(r2) is nil; expected the user stashed on context")
		}
		if len(rec.Result().Cookies()) == 0 {
			t.Fatal("no cookie written; expected a re-stamp past activityStampGranularity")
		}

		replay := httptest.NewRequest(http.MethodGet, "/part/1", nil)
		for _, c := range rec.Result().Cookies() {
			replay.AddCookie(c)
		}
		sess2 := h.session(replay)
		newActivity, _ := sess2.Values["last_activity"].(int64)
		if newActivity <= staleActivity {
			t.Errorf("last_activity = %d, want advanced past %d", newActivity, staleActivity)
		}
	})
}

func TestLoginThrottle_LockoutLifecycle(t *testing.T) {
	h := testHandler()

	if h.loginBlocked("bob") {
		t.Fatal("bob blocked before any failures")
	}
	for range loginMaxFails - 1 {
		h.noteLoginFail("bob")
	}
	if h.loginBlocked("bob") {
		t.Error("bob blocked before reaching loginMaxFails")
	}
	h.noteLoginFail("bob")
	if !h.loginBlocked("bob") {
		t.Error("bob not blocked after loginMaxFails failures")
	}

	h.noteLoginOK("bob")
	if h.loginBlocked("bob") {
		t.Error("bob still blocked after a successful login")
	}
	if len(h.loginAttempts) != 0 {
		t.Errorf("loginAttempts has %d entries after noteLoginOK, want 0", len(h.loginAttempts))
	}
}

func TestLoginThrottle_LockoutExpires(t *testing.T) {
	h := testHandler()

	for range loginMaxFails {
		h.noteLoginFail("bob")
	}
	if !h.loginBlocked("bob") {
		t.Fatal("bob not blocked after loginMaxFails failures")
	}

	h.loginMu.Lock()
	h.loginAttempts["bob"].lockCap = time.Now().Add(-time.Second)
	h.loginMu.Unlock()

	if h.loginBlocked("bob") {
		t.Error("bob still blocked after lockCap elapsed")
	}

	h.noteLoginFail("bob")
	h.loginMu.Lock()
	fails := h.loginAttempts["bob"].fails
	h.loginMu.Unlock()
	if fails != 1 {
		t.Errorf("fails = %d after one failure past an elapsed lockout, want 1 (counter should restart)", fails)
	}
}
