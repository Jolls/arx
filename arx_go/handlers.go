package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	ioFS "io/fs"
	"log"
	"net/http"
	"path"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"

	"arx/arx_go/models"
	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
	"arx/arxlib/urlutil"
)

type Handler struct {
	db             *sql.DB
	dialect        arxdb.Dialect
	cfg            *arxbase.Config
	store          *sessions.CookieStore
	tmplFS         ioFS.FS
	schemaMismatch string
	releaseNotes   string
	companyLogo    string
	partCategories []models.Category

	routeMu    sync.Mutex
	routeStats map[int]*routeAccumulator

	userMu    sync.RWMutex
	userCache map[int]*userCacheEntry
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
	return &Handler{
		db: db, dialect: dialect, cfg: cfg, store: store, tmplFS: tmplFS, releaseNotes: string(releaseNotes),
		routeStats: make(map[int]*routeAccumulator),
		userCache:  make(map[int]*userCacheEntry),
	}
}

func (h *Handler) logSQL(query string, args ...any) {
	if !h.cfg.DebugMode {
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
	return h.dialect.TopClause(ph), h.dialect.LimitClause(ph)
}

func (h *Handler) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	query = h.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQueryErr(ctx, query, func() (*sql.Rows, error) { return h.db.QueryContext(ctx, query, args...) })
}

func (h *Handler) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	query = h.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQuery(ctx, query, func() *sql.Row { return h.db.QueryRowContext(ctx, query, args...) })
}

func (h *Handler) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	query = h.dialect.Rewrite(query)
	h.logSQL(query, args...)
	return timeQueryErr(ctx, query, func() (sql.Result, error) { return h.db.ExecContext(ctx, query, args...) })
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
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txLogger{Tx: tx, logFn: h.logSQL, rewrite: h.dialect.Rewrite}, nil
}

// CheckSchemaVersion queries app_config for schema_version and stores a mismatch
// message if it doesn't match ExpectedSchemaVersion. Safe to call when db is nil.
func (h *Handler) CheckSchemaVersion(ctx context.Context) {
	if h.db == nil {
		h.schemaMismatch = ""
		return
	}
	h.schemaMismatch = arxbase.CheckSchemaVersion(ctx, h.queryRowContext, h.cfg.AppConfigTable())
}

// loadCompanyLogo caches the company logo data URI from app_config on the Handler
// so the hot render path stays DB-free. Safe to call when db is nil.
func (h *Handler) loadCompanyLogo(ctx context.Context) {
	h.companyLogo = h.appConfigGetOr(ctx, "company_logo", "")
}

// companyLogoURL returns the cached company logo as a template.URL. html/template's
// URL-context filter defangs any src/href value whose scheme isn't http(s)/mailto,
// which would otherwise strip our data: URI; template.URL marks it as pre-vetted.
func (h *Handler) companyLogoURL() template.URL {
	return template.URL(h.companyLogo)
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

// appConfigGet reads a single key from app_config.
func (h *Handler) appConfigGet(ctx context.Context, key string) (string, error) {
	if h.db == nil {
		return "", nil
	}
	var val string
	err := h.queryRowContext(ctx,
		`SELECT setting_value FROM `+h.cfg.AppConfigTable()+` WHERE setting_key = @p1`, key,
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
	_, err := h.execContext(ctx, h.dialect.UpsertAppConfig(h.cfg.AppConfigTable()), key, value)
	return err
}

// DB returns the underlying *sql.DB. Used in integration tests.
func (h *Handler) DB() *sql.DB { return h.db }

// CloseDB closes the underlying database connection if one is open.
func (h *Handler) CloseDB() {
	if h.db != nil {
		h.db.Close()
	}
}

// RequireAuth redirects to /settings when no DB is connected, to /login when
// no user is logged in, and otherwise stashes the user on the request context.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.db == nil {
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		if h.schemaMismatch != "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		r2, u := h.withUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r2)
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
		if !h.cfg.DebugMode || isFileServingPath(r.URL.Path) {
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

// renderPrint parses a single standalone template (no layout wrapper) and executes it.
// Used for print views that ship their own full HTML document.
func (h *Handler) renderPrint(w http.ResponseWriter, page string, data any) {
	tmpl, err := template.New("").Funcs(pmTemplateFuncs()).ParseFS(h.tmplFS,
		"templates/"+page,
	)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, path.Base(page), data); err != nil {
		http.Error(w, "template execute error: "+err.Error(), http.StatusInternalServerError)
	}
}

// pmTabFavicons maps each Parts Master nav tab to its favicon, reusing the
// same icon shown in the nav bar so the browser tab matches the active section.
var pmTabFavicons = map[string]string{
	"parts":     "/static/shared/icons/parts.svg",
	"suppliers": "/static/shared/icons/vendors.svg",
	"pos":       "/static/shared/icons/pos.svg",
	"contacts":  "/static/shared/icons/contacts.svg",
	"records":   "/static/shared/icons/records.svg",
	"reports":   "/static/shared/icons/reports.svg",
}

// render parses layout + partials + the named page template and executes "layout".
func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	if m, ok := data.(map[string]any); ok {
		m["AppVersion"] = h.cfg.Version
		m["SchemaMismatch"] = h.schemaMismatch
		m["TestRecordsURL"] = h.cfg.TestRecordsURL
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
	tmpl, err := template.New("").Funcs(pmTemplateFuncs()).ParseFS(h.tmplFS,
		"templates/shared/layout.html",
		"templates/shared/partials.html",
		"templates/"+page,
	)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template execute error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	h.render(w, r, "shared/not_found.html", map[string]any{
		"ActiveTab": "", "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, msg string) {
	h.render(w, r, "shared/error.html", map[string]any{
		"Error":    msg,
		"TestMode": h.cfg.TestMode,
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
	return token != "" && r.FormValue("csrf_token") == token
}

// RequireCsrfOnPost is middleware that rejects any POST whose csrf_token form
// value does not match the session token.
func (h *Handler) RequireCsrfOnPost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !h.verifyCsrf(r) {
			http.Error(w, "Invalid form submission", http.StatusForbidden)
			return
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
		refPath := strings.SplitN(ref, "?", 2)[0]
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

// --- Template functions (Parts Master) ------------------------------------

func pmTemplateFuncs() template.FuncMap {
	return template.FuncMap{
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
		"categoryCtx": func(opts []string, current, inputID string) map[string]any {
			return map[string]any{"Opts": opts, "Current": current, "InputID": inputID}
		},
		"partCategoryCtx": func(cats any, selected string) map[string]any {
			return map[string]any{"Categories": cats, "Selected": selected}
		},
		"inList": func(list []string, val string) bool {
			for _, s := range list {
				if s == val {
					return true
				}
			}
			return false
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
			case reflect.Ptr, reflect.Interface:
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
	for _, p := range strings.Split(s, ",") {
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

// fetchUnits returns all rows from the unit table ordered by unit_type, abbreviation.
func (h *Handler) fetchUnits(ctx context.Context) ([]UnitOption, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT unit_id, abbreviation, display_name, unit_type FROM %s ORDER BY unit_type, abbreviation`,
		h.cfg.UnitTable(),
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
