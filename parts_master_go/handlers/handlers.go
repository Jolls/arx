package handlers

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
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/sessions"

	arxbase "arx/arxlib/config"
	"arx/arxlib/urlutil"
	"arx/parts_master_go/config"
)

type Handler struct {
	db                *sql.DB
	cfg               *config.Config
	store             *sessions.CookieStore
	tmplFS            ioFS.FS
	schemaMismatch    string
	releaseNotes      string
	AfterSettingsSave func(newDB *sql.DB) // called after a settings save; hands the new shared pool to TR
}

// DB returns the current database pool (shared with Test Records via arx_go).
func (h *Handler) DB() *sql.DB { return h.db }

// Config returns the active config (shared with Test Records via arx_go).
func (h *Handler) Config() *config.Config { return h.cfg }

func New(db *sql.DB, cfg *config.Config, tmplFS ioFS.FS, releaseNotes []byte) *Handler {
	store := sessions.NewCookieStore([]byte(cfg.SessionSecret))
	return &Handler{db: db, cfg: cfg, store: store, tmplFS: tmplFS, releaseNotes: string(releaseNotes)}
}

func (h *Handler) logSQL(query string, args ...any) {
	if !h.cfg.DebugMode {
		return
	}
	log.Printf("[SQL] %s | args=%v", strings.Join(strings.Fields(query), " "), args)
}

func (h *Handler) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	h.logSQL(query, args...)
	return h.db.QueryContext(ctx, query, args...)
}

func (h *Handler) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	h.logSQL(query, args...)
	return h.db.QueryRowContext(ctx, query, args...)
}

func (h *Handler) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	h.logSQL(query, args...)
	return h.db.ExecContext(ctx, query, args...)
}

type txLogger struct {
	*sql.Tx
	logFn func(string, ...any)
}

func (t *txLogger) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	t.logFn(query, args...)
	return t.Tx.ExecContext(ctx, query, args...)
}

func (t *txLogger) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	t.logFn(query, args...)
	return t.Tx.QueryRowContext(ctx, query, args...)
}

func (t *txLogger) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	t.logFn(query, args...)
	return t.Tx.QueryContext(ctx, query, args...)
}

func (t *txLogger) Commit() error   { t.logFn("COMMIT"); return t.Tx.Commit() }
func (t *txLogger) Rollback() error { t.logFn("ROLLBACK"); return t.Tx.Rollback() }

func (h *Handler) beginTx(ctx context.Context) (*txLogger, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txLogger{Tx: tx, logFn: h.logSQL}, nil
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
	_, err := h.execContext(ctx, `
		MERGE INTO `+h.cfg.AppConfigTable()+` AS t
		USING (SELECT @p1 AS k, @p2 AS v) AS s ON t.setting_key = s.k
		WHEN MATCHED THEN UPDATE SET t.setting_value = s.v, t.updated_at = GETDATE()
		WHEN NOT MATCHED THEN INSERT (setting_key, setting_value) VALUES (s.k, s.v)`,
		key, value)
	return err
}

// CloseDB closes the underlying database connection if one is open.
func (h *Handler) CloseDB() {
	if h.db != nil {
		h.db.Close()
	}
}

// RequireAuth is a middleware that redirects to /settings when no database
// connection is available. All application routes use this except /settings
// and /static/*.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.db == nil {
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// renderPrint parses a single standalone template (no layout wrapper) and executes it.
// Used for print views that ship their own full HTML document.
func (h *Handler) renderPrint(w http.ResponseWriter, page string, data any) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(h.tmplFS,
		"templates/"+page,
	)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, page, data); err != nil {
		http.Error(w, "template execute error: "+err.Error(), http.StatusInternalServerError)
	}
}

// render parses layout + partials + the named page template and executes "layout".
func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	if m, ok := data.(map[string]any); ok {
		m["AppVersion"] = h.cfg.Version
		m["SchemaMismatch"] = h.schemaMismatch
		m["TestRecordsURL"] = h.cfg.TestRecordsURL
	}
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(h.tmplFS,
		"templates/layout.html",
		"templates/partials.html",
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
	h.render(w, "not_found.html", map[string]any{
		"ActiveTab": "", "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) renderError(w http.ResponseWriter, msg string) {
	h.render(w, "error.html", map[string]any{
		"Error":    msg,
		"TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) session(r *http.Request) *sessions.Session {
	s, _ := h.store.Get(r, "arx-session")
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

// --- Template functions ----------------------------------------------------

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate":     formatDate,
		"formatFileSize": formatFileSize,
		"fileIcon":       urlutil.FileIcon,
		"isHTTPURL":      urlutil.IsHTTPURL,
		"isLocalDir":     urlutil.IsLocalDir,
		"isLocalFile":    urlutil.IsLocalFile,
		"localFileURL":         func(val string) string { return urlutil.LocalFileURL(val, "/local/") },
		"localDirURL":          func(val string) string { return urlutil.LocalDirURL(val, "/local-dir/") },
		"supplierLocalFileURL": func(val string) string { return urlutil.LocalFileURL(val, "/supplier-local/") },
		"supplierLocalDirURL":  func(val string) string { return urlutil.LocalDirURL(val, "/supplier-local-dir/") },
		"fileBaseName":   urlutil.FileBaseName,
		"attachLabel":    attachLabel,
		"isPDF":          urlutil.IsPDF,
		"deref":          func(f *float64) float64 { if f == nil { return 0 }; return *f },
		"derefInt":       func(i *int) int { if i == nil { return 0 }; return *i },
		"packSizeStr":    func(f *float64) string { if f == nil { return "—" }; return fmt.Sprintf("%g", *f) },
		"categoryCtx": func(opts []string, current, inputID string) map[string]any {
			return map[string]any{"Opts": opts, "Current": current, "InputID": inputID}
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
