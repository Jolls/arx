package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	ioFS "io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/sessions"

	"arx/arx_go/models"
	arxbase "arx/internal/config"
	arxdb "arx/internal/db"
	"arx/internal/urlutil"
)

// dbConn is an immutable snapshot of the active connection + its dialect,
// swapped atomically so concurrent requests never see a torn pointer or use
// a handle mid-close (#757).
type dbConn struct {
	db      *sql.DB
	dialect arxdb.Dialect
}

// runtimeState is the immutable snapshot of everything SettingsSave and the
// startup loaders change at runtime (#196). Readers Load() it once and never see
// a half-applied save; writers go through Handler.update. Never mutate a
// snapshot obtained from st()/cfg() in production code.
type runtimeState struct {
	cfg            *arxbase.Config
	conn           *dbConn // nil => not connected
	schemaMismatch string
	dbConnError    string
	companyLogo    string
	partCategories []models.Category
}

type Handler struct {
	state          atomic.Pointer[runtimeState]
	stateMu        sync.Mutex // serializes update()'s copy-and-swap
	store          *sessions.CookieStore
	tmplFS         ioFS.FS
	tmpls          map[string]*template.Template // parsed once by loadTemplates
	releaseNotes   string

	// connectDB opens a new DB connection; defaults to arxdb.Connect in New().
	// Overridable in tests so SettingsSave's failure path needs no real dial.
	connectDB func(engine, dsn string) (*sql.DB, arxdb.Dialect, error)

	routeMu    sync.Mutex
	routeStats map[int]*routeAccumulator

	userMu    sync.RWMutex
	userCache map[int]*userCacheEntry

	loginMu       sync.Mutex
	loginAttempts map[string]*loginAttempt

	digikeyToken digikeyTokenCache
}

// st returns the current runtime snapshot. A zero-value Handler (tests) yields
// an empty, config-less snapshot rather than nil.
func (h *Handler) st() *runtimeState {
	if s := h.state.Load(); s != nil {
		return s
	}
	return &runtimeState{}
}

// cfg returns the current config snapshot; treat it as read-only (use update).
func (h *Handler) cfg() *arxbase.Config { return h.st().cfg }

// update applies fn to a private copy of the state (and of the config, so fn may
// assign cfg fields) and atomically publishes it.
func (h *Handler) update(fn func(s *runtimeState)) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	next := *h.st()
	if next.cfg != nil {
		c := *next.cfg
		next.cfg = &c
	}
	fn(&next)
	h.state.Store(&next)
}

// database returns the live *sql.DB, or nil when not connected.
func (h *Handler) database() *sql.DB {
	if c := h.st().conn; c != nil {
		return c.db
	}
	return nil
}

// dbUnusable reports whether there's no DB connection at all, or the
// connection exists but doesn't actually work (h.dbConnError, #852) — the
// shared condition every auth-bypass-to-/settings check gates on.
func (h *Handler) dbUnusable() bool {
	return h.database() == nil || h.st().dbConnError != ""
}

// dia returns the live dialect, or a default SQL Server dialect when not
// connected (mirrors New()'s nil-dialect fallback so template/query building
// never nil-panics).
func (h *Handler) dia() arxdb.Dialect {
	if c := h.st().conn; c != nil && c.dialect != nil {
		return c.dialect
	}
	return arxdb.NewSQLServerDialect()
}

func New(db *sql.DB, dialect arxdb.Dialect, cfg *arxbase.Config, tmplFS ioFS.FS, releaseNotes []byte) *Handler {
	store := sessions.NewCookieStore([]byte(cfg.SessionSecret))
	// Harden the session/CSRF cookie: HttpOnly blocks JS access, SameSite=Lax
	// blunts cross-site POSTs. Secure is left off because the app is served over
	// plain HTTP on localhost. (#648)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	}
	if dialect == nil {
		dialect = arxdb.NewSQLServerDialect()
	}
	h := &Handler{
		store: store, tmplFS: tmplFS, releaseNotes: string(releaseNotes),
		routeStats:    make(map[int]*routeAccumulator),
		userCache:     make(map[int]*userCacheEntry),
		loginAttempts: make(map[string]*loginAttempt),
		connectDB:     arxdb.Connect,
	}
	h.state.Store(&runtimeState{cfg: cfg, conn: &dbConn{db: db, dialect: dialect}})
	return h
}

func (h *Handler) logSQL(query string, args ...any) {
	if !h.cfg().DebugMode {
		return
	}
	log.Printf("[SQL] %s | args=%v", strings.Join(strings.Fields(query), " "), args)
}

const slowQueryThreshold = 100 * time.Millisecond

// userCacheTTL bounds staleness for cached session users not covered by an
// explicit invalidation call (e.g. a direct DB edit outside the toggle handlers).
const userCacheTTL = 60 * time.Second

// sqlStats accumulates per-request SQL round-trip count and total DB time.
// One goroutine handles a request and its queries run sequentially, so no lock.
type sqlStats struct {
	count int
	total time.Duration
}

// recordRoundTrip attributes one DB round trip to the request's sqlStats (if the
// profiling middleware seeded one) and flags queries slower than the threshold.
// A no-op when no sqlStats is in ctx (i.e. DebugMode off), which gates all of this.
func recordRoundTrip(ctx context.Context, query string, elapsed time.Duration) {
	st, ok := ctx.Value(ctxSQLStatsKey).(*sqlStats)
	if !ok {
		return
	}
	st.count++
	st.total += elapsed
	if elapsed >= slowQueryThreshold {
		log.Printf("[SLOW SQL] %s | %s", elapsed.Round(time.Millisecond),
			strings.Join(strings.Fields(query), " "))
	}
}

// timeQuery runs call, timing it into recordRoundTrip. Used for the single-value
// *sql.Row methods, which never return an error to check.
func timeQuery[T any](ctx context.Context, query string, call func() T) T {
	start := time.Now()
	result := call()
	recordRoundTrip(ctx, query, time.Since(start))
	return result
}

// timeQueryErr is timeQuery for the (result, error) DB methods.
func timeQueryErr[T any](ctx context.Context, query string, call func() (T, error)) (T, error) {
	start := time.Now()
	result, err := call()
	recordRoundTrip(ctx, query, time.Since(start))
	return result, err
}

// topLimit returns the dialect's TOP and LIMIT clauses for the same
// parameter placeholder ph, for the common TOP-N pagination pattern used
// across recent-item queries (recentPartPOs, recentPartTxns, recentSupplierPOs,
// topSupplierParts).
func (h *Handler) topLimit(ph string) (top, limit string) {
	d := h.dia()
	return d.TopClause(ph), d.LimitClause(ph)
}

func (h *Handler) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c := h.st().conn
	query = c.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQueryErr(ctx, query, func() (*sql.Rows, error) { return c.db.QueryContext(ctx, query, args...) })
}

func (h *Handler) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c := h.st().conn
	query = c.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQuery(ctx, query, func() *sql.Row { return c.db.QueryRowContext(ctx, query, args...) })
}

func (h *Handler) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c := h.st().conn
	query = c.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQueryErr(ctx, query, func() (sql.Result, error) { return c.db.ExecContext(ctx, query, args...) })
}

type txLogger struct {
	*sql.Tx
	logFn   func(string, ...any)
	rewrite func(string) string
}

func (t *txLogger) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	query = t.rewrite(query)
	t.logFn(query, args...)
	return timeQueryErr(ctx, query, func() (sql.Result, error) { return t.Tx.ExecContext(ctx, query, args...) })
}

func (t *txLogger) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	query = t.rewrite(query)
	t.logFn(query, args...)
	return timeQuery(ctx, query, func() *sql.Row { return t.Tx.QueryRowContext(ctx, query, args...) })
}

func (t *txLogger) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	query = t.rewrite(query)
	t.logFn(query, args...)
	return timeQueryErr(ctx, query, func() (*sql.Rows, error) { return t.Tx.QueryContext(ctx, query, args...) })
}

func (t *txLogger) Commit() error   { t.logFn("COMMIT"); return t.Tx.Commit() }
func (t *txLogger) Rollback() error { t.logFn("ROLLBACK"); return t.Tx.Rollback() }

func (h *Handler) beginTx(ctx context.Context) (*txLogger, error) {
	c := h.st().conn
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txLogger{Tx: tx, logFn: h.logSQL, rewrite: c.dialect.Rewrite}, nil
}

// CheckSchemaVersion queries app_config for schema_version and stores a mismatch
// message if it doesn't match ExpectedSchemaVersion. Safe to call when db is nil.
func (h *Handler) CheckSchemaVersion(ctx context.Context) {
	if h.database() == nil {
		h.update(func(s *runtimeState) { s.schemaMismatch, s.dbConnError = "", "" })
		return
	}
	mismatch, connErr := arxbase.CheckSchemaVersion(ctx, h.queryRowContext, h.cfg().AppConfigTable())
	h.update(func(s *runtimeState) { s.schemaMismatch, s.dbConnError = mismatch, connErr })
}

// loadCompanyLogo caches the company logo data URI from app_config on the Handler
// so the hot render path stays DB-free. Safe to call when db is nil.
func (h *Handler) loadCompanyLogo(ctx context.Context) {
	logo := h.appConfigGetOr(ctx, "company_logo", "")
	h.update(func(s *runtimeState) { s.companyLogo = logo })
}

// loadDigiKeyCredentials loads the shop's DigiKey API client ID/secret from
// app_config onto cfg (issue #60 — shared across every user, unlike a DB
// password). Safe to call when db is nil.
func (h *Handler) loadDigiKeyCredentials(ctx context.Context) {
	id := h.appConfigGetOr(ctx, "digikey_client_id", "")
	secret := h.appConfigGetOr(ctx, digikeyClientSecretKey, "")
	h.update(func(s *runtimeState) {
		s.cfg.DigiKeyClientID = id
		s.cfg.DigiKeyClientSecret = secret
	})
}

// companyLogoURL returns the cached company logo as a template.URL. html/template's
// URL-context filter defangs any src/href value whose scheme isn't http(s)/mailto,
// which would otherwise strip our data: URI; template.URL marks it as pre-vetted.
func (h *Handler) companyLogoURL() template.URL {
	return template.URL(h.st().companyLogo)
}

// accentTheme is one preset accent color option offered in Settings (#537).
type accentTheme struct {
	Key   string // stored app_config value and the "theme-<key>" body class in app.css
	Label string // shown in the UI
}

// accentThemes are the preset accent color options offered in Settings, in
// display order.
var accentThemes = []accentTheme{
	{"blue", "Blue"},
	{"indigo", "Indigo"},
	{"teal", "Teal"},
	{"green", "Green"},
	{"slate", "Slate"},
}

func isValidAccentTheme(key string) bool {
	for _, t := range accentThemes {
		if t.Key == key {
			return true
		}
	}
	return false
}

// accentThemeClass returns the "theme-<name>" body class for the logged-in
// user's accent color preference (issue #537), falling back to "theme-blue"
// when logged out or the stored value is empty/unrecognized.
func (h *Handler) accentThemeClass(r *http.Request) string {
	u := h.currentUser(r)
	if u == nil || !isValidAccentTheme(u.AccentColor) {
		return "theme-blue"
	}
	return "theme-" + u.AccentColor
}

// defaultTimezone is the fallback IANA zone when a user is absent or carries an
// empty/unrecognized value (issue #847). Matches the migration's backfill.
const defaultTimezone = "America/Los_Angeles"

// timezoneOption is one preset timezone offered in Settings (#847).
type timezoneOption struct {
	Key   string // stored IANA identifier, e.g. "America/Los_Angeles"
	Label string // shown in the UI
}

// commonTimezones are the timezone choices offered in Settings, in display
// order. Deliberately a short curated list (US shop floors plus UTC), not the
// full IANA database — a closed list keeps validation trivial and the dropdown
// usable. Add entries here as needed.
var commonTimezones = []timezoneOption{
	{"America/Los_Angeles", "Pacific (Los Angeles)"},
	{"America/Denver", "Mountain (Denver)"},
	{"America/Phoenix", "Arizona (Phoenix, no DST)"},
	{"America/Chicago", "Central (Chicago)"},
	{"America/New_York", "Eastern (New York)"},
	{"America/Anchorage", "Alaska (Anchorage)"},
	{"Pacific/Honolulu", "Hawaii (Honolulu)"},
	{"UTC", "UTC"},
}

func isValidTimezone(key string) bool {
	for _, t := range commonTimezones {
		if t.Key == key {
			return true
		}
	}
	return false
}

// userLocation returns the *time.Location for the logged-in user's timezone
// preference (issue #847), falling back to defaultTimezone when logged out, unset,
// or unrecognized — and to time.UTC only if the tz database itself can't be loaded
// (should not happen: main.go blank-imports time/tzdata so the zone data is
// compiled into the binary).
func (h *Handler) userLocation(r *http.Request) *time.Location {
	tz := defaultTimezone
	if u := h.currentUser(r); u != nil && isValidTimezone(u.Timezone) {
		tz = u.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("warning: could not load timezone %q: %v", tz, err)
		return time.UTC
	}
	return loc
}

// appConfigGet reads a single key from app_config.
func (h *Handler) appConfigGet(ctx context.Context, key string) (string, error) {
	if h.database() == nil {
		return "", nil
	}
	var val string
	err := h.queryRowContext(ctx,
		`SELECT setting_value FROM `+h.cfg().AppConfigTable()+` WHERE setting_key = @p1`, key,
	).Scan(&val)
	return val, err
}

// appConfigGetOr reads a single key from app_config and returns def on any error or missing key.
func (h *Handler) appConfigGetOr(ctx context.Context, key, def string) string {
	val, err := h.appConfigGet(ctx, key)
	if err != nil {
		return def
	}
	return val
}

// appConfigSet upserts a key/value pair in app_config.
func (h *Handler) appConfigSet(ctx context.Context, key, value string) error {
	_, err := h.execContext(ctx, h.dia().UpsertAppConfig(h.cfg().AppConfigTable()), key, value)
	return err
}

// DB returns the underlying *sql.DB. Used in integration tests.
func (h *Handler) DB() *sql.DB { return h.database() }

// CloseDB closes the underlying database connection if one is open.
func (h *Handler) CloseDB() {
	if db := h.database(); db != nil {
		db.Close()
	}
}

// RequireAuth redirects to /settings when no DB is connected or the connection
// itself is unusable (#852), to /login when no user is logged in (including a
// plain schema-version mismatch on an otherwise-working connection), and
// otherwise stashes the user on the request context.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.dbUnusable() {
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		if h.st().schemaMismatch != "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		r2, u := h.withUser(w, r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r2)
	})
}

// RequireAuthOnceConnected gates a route that must stay reachable during
// first-run setup (no DB connected) but requires a logged-in user once a database
// is connected. Used for POST /settings so an unauthenticated caller can't
// rewrite the DB connection and exfiltrate the stored password after setup
// (#748). It also bypasses auth when the connection itself is unusable
// (h.dbConnError != "", #852) — login is impossible against a broken
// connection, so the admin needs a way back to Settings without one. A plain
// schema-version mismatch on an otherwise-working connection still requires
// login: that's the #748 protection, and login still works there.
func (h *Handler) RequireAuthOnceConnected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.dbUnusable() {
			next.ServeHTTP(w, r)
			return
		}
		r2, u := h.withUser(w, r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r2)
	})
}

// canEditConnection reports whether r's caller may change the database connection.
// Admins always may. So may anyone during first-run setup or while the connection
// is unusable: there may be no admin account yet, and demanding one while the DB is
// unreachable would lock everyone out of the only screen that can fix it
// (#106, extending #748/#852).
//
// Both the gate (RequireAdminOnceConnected) and the view (settingsData's
// CanEditConnection) call this, so the page can never offer a form the server will
// reject, and the rule has one definition rather than two that must be kept in step.
func (h *Handler) canEditConnection(r *http.Request) bool {
	return h.dbUnusable() || h.isAdmin(r)
}

// RequireAdminOnceConnected gates POST /settings and the folder-picker it drives.
// It composes RequireAuthOnceConnected rather than repeating it, so the first-run
// and broken-connection escape hatches have a single implementation: the wrapped
// middleware either passes straight through (bypass, no user on context) or resolves
// a real user, and canEditConnection reads correctly in both cases (#106).
func (h *Handler) RequireAdminOnceConnected(next http.Handler) http.Handler {
	return h.RequireAuthOnceConnected(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.canEditConnection(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireAdmin refuses non-admins with a plain-text 403. Must run after RequireAuth
// so the session user is on the context. Declared per route group in main.go so the
// route table shows which endpoints are admin-only — user management in particular
// gates on it so a non-admin can't self-grant rights, reset passwords, or deactivate
// others (#750, #120).
func (h *Handler) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.isAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminJSON is RequireAdmin for endpoints whose callers parse the response
// as JSON (the Settings page runs r.json() on every response), so the 403 carries
// a JSON error body instead of plain text (#103, #120).
func (h *Handler) RequireAdminJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.isAdmin(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Only an admin can do this."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fileServingPrefixes are routes that only ever serve static assets or local
// files from disk — they never touch the DB, so profileRequest skips them
// entirely instead of logging a guaranteed "0 round trips" line every time.
var fileServingPrefixes = []string{
	"/static/", "/local/", "/local-dir/", "/supplier-local/", "/supplier-local-dir/", "/images/",
}

func isFileServingPath(path string) bool {
	for _, prefix := range fileServingPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// routeAccumulator sums round trips and timing across the requests that make up
// one logical page view (the page load plus the background fetch/XHR calls it
// fires). path is the top-level navigation that started it.
type routeAccumulator struct {
	path  string
	count int
	total time.Duration
}

// logRouteSummary folds one request's stats into the calling session's current
// route accumulator and logs the running total. Modern browsers tag every
// request with Sec-Fetch-Dest: a real page navigation (typed URL, clicked link)
// is "document"; a fetch()/XHR call fired by the page's own JS is "empty". Only
// an explicit "empty" is folded into the current route — anything else,
// including a missing header (older browsers, non-browser clients), starts a
// fresh route. The Referer header can't make this distinction: navigating away
// from a page and that page's own background fetch both carry the same
// Referer, so matching on it alone never resets and the total grows forever.
// Skipped for sessions with no logged-in user yet.
func (h *Handler) logRouteSummary(r *http.Request, count int, total time.Duration) {
	sess := h.session(r)
	uid, ok := sess.Values["user_id"].(int)
	if !ok {
		return
	}

	h.routeMu.Lock()
	defer h.routeMu.Unlock()
	acc := h.routeStats[uid]
	if acc == nil || r.Header.Get("Sec-Fetch-Dest") != "empty" {
		acc = &routeAccumulator{path: r.URL.Path}
		h.routeStats[uid] = acc
	}
	acc.count += count
	acc.total += total
	log.Printf("[PERF SUMMARY] %s | %d round trips | %s total",
		acc.path, acc.count, acc.total.Round(time.Millisecond))
}

// profileRequest logs per-request SQL round-trip count and DB/total timing when
// DebugMode is on, plus a running per-route summary. See sqlStats/recordRoundTrip.
func (h *Handler) profileRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.cfg().DebugMode || isFileServingPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		st := &sqlStats{}
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxSQLStatsKey, st)))
		elapsed := time.Since(start)
		log.Printf("[PERF] %s %s | %d round trips | %s in DB | %s total",
			r.Method, r.URL.Path, st.count, st.total.Round(time.Millisecond), elapsed.Round(time.Millisecond))
		h.logRouteSummary(r, st.count, elapsed)
	})
}

// renderPrint executes a pre-parsed standalone template (no layout wrapper).
// Used for print views that ship their own full HTML document.
func (h *Handler) renderPrint(w http.ResponseWriter, page string, data any) {
	tmpl := h.tmpl(w, "print:"+page)
	if tmpl == nil {
		return
	}
	if err := tmpl.ExecuteTemplate(w, path.Base(page), data); err != nil {
		serverError(w, "template execute error", err)
	}
}

// pmTabFavicons maps each nav tab to its favicon, reusing the same icon
// shown in the nav bar so the browser tab matches the active section.
var pmTabFavicons = map[string]string{
	"parts":     "/static/shared/icons/parts.svg",
	"suppliers": "/static/shared/icons/vendors.svg",
	"pos":       "/static/shared/icons/pos.svg",
	"contacts":  "/static/shared/icons/contacts.svg",
	"records":   "/static/shared/icons/records.svg",
	"reports":   "/static/shared/icons/reports.svg",
}

// render executes "layout" from the pre-parsed layout + partials + page template.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	if m, ok := data.(map[string]any); ok {
		s := h.st()
		m["AppVersion"] = s.cfg.Version
		m["SchemaMismatch"] = s.schemaMismatch
		m["DBConnError"] = s.dbConnError
		m["CurrentUser"] = h.currentUser(r)
		m["CSRFToken"] = h.csrfToken(w, r)
		m["CompanyLogo"] = h.companyLogoURL()
		m["AccentThemeClass"] = h.accentThemeClass(r)
		m["Title"] = "Arx Parts Master"
		m["Favicon"] = "/static/shared/favicon.png"
		m["FaviconType"] = "image/png"
		if tab, _ := m["ActiveTab"].(string); tab != "" {
			if icon, ok := pmTabFavicons[tab]; ok {
				m["Favicon"] = icon
				m["FaviconType"] = "image/svg+xml"
			}
		}
	}
	tmpl := h.tmpl(w, "layout:"+page)
	if tmpl == nil {
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		serverError(w, "template execute error", err)
	}
}

func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	h.render(w, r, "shared/not_found.html", map[string]any{
		"ActiveTab": "", "TestMode": h.cfg().TestMode,
	})
}

// sameOriginRefererPath returns the path+query of the request's Referer if it
// points back at this same host, or "" otherwise (guards against an
// open-redirect-style "Back" link from a spoofed/external Referer header).
func sameOriginRefererPath(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host {
		return ""
	}
	return ref.RequestURI()
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, msg string) {
	h.render(w, r, "shared/error.html", map[string]any{
		"Error":    msg,
		"TestMode": h.cfg().TestMode,
		"BackURL":  sameOriginRefererPath(r),
	})
}

func (h *Handler) session(r *http.Request) *sessions.Session {
	s, _ := h.store.Get(r, arxbase.SessionCookieName)
	return s
}

// --- CSRF ------------------------------------------------------------------

func (h *Handler) csrfToken(w http.ResponseWriter, r *http.Request) string {
	sess := h.session(r)
	if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
		return token
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	sess.Values["csrf_token"] = token
	sess.Save(r, w)
	return token
}

func (h *Handler) verifyCsrf(r *http.Request) bool {
	sess := h.session(r)
	token, _ := sess.Values["csrf_token"].(string)
	got := r.FormValue("csrf_token")
	if token == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// RequireLocalHost rejects requests whose Host header isn't this app's own
// loopback address. The listener is bound to 127.0.0.1, but DNS rebinding lets
// a remote web page reach it under its own hostname (#167).
func (h *Handler) RequireLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "localhost:" + h.cfg().Port, "127.0.0.1:" + h.cfg().Port, "[::1]:" + h.cfg().Port:
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
		}
	})
}

// RequireCsrfOnPost is middleware that rejects any POST whose csrf_token form
// value does not match the session token.
func (h *Handler) RequireCsrfOnPost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Parse the body explicitly (instead of leaving it to verifyCsrf's
			// r.FormValue call) so a body that exceeds the main.go
			// MaxBytesReader cap surfaces as *http.MaxBytesError instead of
			// being silently swallowed and misreported as a bad CSRF token
			// (#88). 32<<20 matches net/http's own unexported defaultMaxMemory,
			// which is what FormValue passes to ParseMultipartForm internally
			// — this preserves today's in-memory/on-disk multipart threshold.
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					http.Error(w, "Uploaded file is too large (max 100 MB)", http.StatusRequestEntityTooLarge)
					return
				}
			}
			if !h.verifyCsrf(r) {
				http.Error(w, "Invalid form submission", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- Nav context (breadcrumb back-link) ------------------------------------

var listPaths = map[string]bool{
	"/": true, "/pos": true, "/suppliers": true, "/contacts": true,
}

func resourceBase(path string) string {
	path = strings.TrimRight(path, "/")
	parts := strings.SplitN(path, "/", 4)
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "/")
	}
	return path
}

func (h *Handler) setNavContext(w http.ResponseWriter, r *http.Request, url, label string) {
	sess := h.session(r)

	if ref := r.Referer(); ref != "" {
		refPath, _, _ := strings.Cut(ref, "?")
		if idx := strings.Index(refPath, "://"); idx >= 0 {
			refPath = refPath[idx+3:]
			if sl := strings.Index(refPath, "/"); sl >= 0 {
				refPath = refPath[sl:]
			}
		}
		refPath = strings.TrimRight(refPath, "/")
		if refPath == "" {
			refPath = "/"
		}

		if resourceBase(refPath) != resourceBase(url) {
			if listPaths[refPath] {
				delete(sess.Values, "nav_back_url")
				delete(sess.Values, "nav_back_label")
			} else {
				sess.Values["nav_back_url"] = sess.Values["nav_current_url"]
				sess.Values["nav_back_label"] = sess.Values["nav_current_label"]
			}
		}
	}

	sess.Values["nav_current_url"] = url
	sess.Values["nav_current_label"] = label
	sess.Save(r, w)
}

func navBack(sess *sessions.Session) (url, label string) {
	if u, ok := sess.Values["nav_back_url"].(string); ok {
		url = u
	}
	if l, ok := sess.Values["nav_back_label"].(string); ok {
		label = l
	}
	return
}

// --- Template functions (core) ---------------------------------------------

func coreTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// dict builds a map from alternating string-key/value pairs so a
		// {{template}} call can pass more than one value to a named template
		// (a Go html/template block only ever receives a single ".").
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: keys must be strings")
				}
				m[key] = pairs[i+1]
			}
			return m, nil
		},
		"formatDate":           formatDate,
		"formatFileSize":       formatFileSize,
		"fileIcon":             urlutil.FileIcon,
		"isHTTPURL":            urlutil.IsHTTPURL,
		"isAbsPath":            urlutil.IsAbsPath,
		"isLocalDir":           urlutil.IsLocalDir,
		"isLocalFile":          urlutil.IsLocalFile,
		"localFileURL":         func(val string) string { return urlutil.LocalFileURL(val, "/local/") },
		"localDirURL":          func(val string) string { return urlutil.LocalDirURL(val, "/local-dir/") },
		"supplierLocalFileURL": func(val string) string { return urlutil.LocalFileURL(val, "/supplier-local/") },
		"supplierLocalDirURL":  func(val string) string { return urlutil.LocalDirURL(val, "/supplier-local-dir/") },
		"fileBaseName":         urlutil.FileBaseName,
		"displayLink":          urlutil.StripLocalPrefix,
		"attachLabel":          attachLabel,
		"isPDF":                urlutil.IsPDF,
		"isImage":              urlutil.IsImage,
		"deref": func(f *float64) float64 {
			if f == nil {
				return 0
			}
			return *f
		},
		"derefInt": func(i *int) int {
			if i == nil {
				return 0
			}
			return *i
		},
		"packSizeStr": func(f *float64) string {
			if f == nil {
				return "—"
			}
			return fmt.Sprintf("%g", *f)
		},
		"supplierPrefLabel": func(i *int) string {
			if i == nil {
				return ""
			}
			switch *i {
			case 1:
				return "Primary"
			case 2:
				return "Alternate"
			case 3:
				return "Backup"
			default:
				return strconv.Itoa(*i)
			}
		},
		"categoryCtx": func(opts []string, current, inputID string) map[string]any {
			return map[string]any{"Opts": opts, "Current": current, "InputID": inputID}
		},
		"partCategoryCtx": func(cats any, selected string) map[string]any {
			return map[string]any{"Categories": cats, "Selected": selected}
		},
		"inList": func(list []string, val string) bool {
			return slices.Contains(list, val)
		},
		"not": func(v any) bool {
			if v == nil {
				return true
			}
			val := reflect.ValueOf(v)
			switch val.Kind() {
			case reflect.Bool:
				return !val.Bool()
			case reflect.String:
				return val.String() == ""
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				return val.Int() == 0
			case reflect.Slice, reflect.Map, reflect.Array:
				return val.Len() == 0
			case reflect.Pointer, reflect.Interface:
				return val.IsNil()
			default:
				return false
			}
		},
		"formatDateInput": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02")
		},
		"formatDateTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Format("2006-01-02 15:04")
		},
		"fmtFloat": func(f float64) string {
			if f == 0 {
				return ""
			}
			return strconv.FormatFloat(f, 'f', -1, 64)
		},
		"fmtOptFloat": func(f *float64) string {
			if f == nil || *f == 0 {
				return ""
			}
			return strconv.FormatFloat(*f, 'f', -1, 64)
		},
		"mul": func(a, b float64) float64 { return a * b },
		"cityLine": func(city, state, zip string) string {
			var parts []string
			if city != "" {
				parts = append(parts, city)
			}
			if state != "" {
				parts = append(parts, state)
			}
			result := strings.Join(parts, ", ")
			if zip != "" {
				result += "  " + zip
			}
			return result
		},
	}
}

func formatDate(t *time.Time) string {
	if t == nil {
		return "N/A"
	}
	return t.Format("2006-01-02")
}

func formatFileSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1_048_576:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1_048_576)
	}
}

// splitCSV splits a comma-separated string into trimmed, non-empty tokens.
func splitCSV(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func attachLabel(filename, category string) string {
	if category != "" {
		return category
	}
	return urlutil.FileBaseName(filename)
}

// ── Units of measure ─────────────────────────────────────────────────────────

// UnitOption is a row from the unit table, used to populate dropdowns.
type UnitOption struct {
	ID           int
	Abbreviation string
	DisplayName  string
	UnitType     string
}

// fetchUnits returns all rows from the uom table ordered by unit_type, abbreviation.
func (h *Handler) fetchUnits(ctx context.Context) ([]UnitOption, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT uom_id, abbreviation, display_name, unit_type FROM %s ORDER BY unit_type, abbreviation`,
		h.cfg().UomTable(),
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var units []UnitOption
	for rows.Next() {
		var u UnitOption
		var abbr, name, utype sql.NullString
		if err := rows.Scan(&u.ID, &abbr, &name, &utype); err != nil {
			return nil, err
		}
		u.Abbreviation = abbr.String
		u.DisplayName = name.String
		u.UnitType = utype.String
		units = append(units, u)
	}
	return units, rows.Err()
}
