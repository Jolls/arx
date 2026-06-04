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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/sessions"

	"arx/arxlib/urlutil"
	"arx/test_records_go/config"
	"arx/test_records_go/models"
)

type Handler struct {
	db             *sql.DB
	cfg            *config.Config
	store          *sessions.CookieStore
	tmplFS         ioFS.FS
	schemaMismatch string
	releaseNotes   string
}

func New(db *sql.DB, cfg *config.Config, tmplFS ioFS.FS, releaseNotes []byte) *Handler {
	store := sessions.NewCookieStore([]byte(cfg.SessionSecret))
	return &Handler{db: db, cfg: cfg, store: store, tmplFS: tmplFS, releaseNotes: string(releaseNotes)}
}

func (h *Handler) CloseDB() {
	if h.db != nil {
		h.db.Close()
	}
}

// SetDBAndConfig swaps in a fresh DB pool and config (called by App.Reload after settings save).
func (h *Handler) SetDBAndConfig(db *sql.DB, cfg *config.Config) {
	if h.db != nil {
		h.db.Close()
	}
	h.db = db
	h.cfg = cfg
}

// CheckSchemaVersion queries app_config for schema_version and stores a mismatch
// message if it doesn't match config.ExpectedSchemaVersion. Safe to call when db is nil.
func (h *Handler) CheckSchemaVersion(ctx context.Context) {
	if h.db == nil {
		return
	}
	var val string
	err := h.queryRowContext(ctx,
		`SELECT setting_value FROM `+h.cfg.AppConfigTable()+` WHERE setting_key = 'schema_version'`,
	).Scan(&val)
	if err != nil {
		h.schemaMismatch = fmt.Sprintf("could not read schema_version (%v)", err)
		return
	}
	if val != config.ExpectedSchemaVersion {
		h.schemaMismatch = fmt.Sprintf("DB schema v%s, app expects v%s", val, config.ExpectedSchemaVersion)
	} else {
		h.schemaMismatch = ""
	}
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

// RequireAuth redirects to /settings when no database connection is available.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.db == nil {
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	if m, ok := data.(map[string]any); ok {
		m["AppVersion"] = h.cfg.Version
		m["SchemaMismatch"] = h.schemaMismatch
		m["PartsMasterURL"] = h.cfg.PartsMasterURL
	}
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(h.tmplFS,
		"templates/layout.html",
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

func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	h.render(w, "not_found.html", map[string]any{"TestMode": h.cfg.TestMode})
}

func (h *Handler) session(r *http.Request) *sessions.Session {
	s, _ := h.store.Get(r, "arx-tr-session")
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

// --- Template functions ----------------------------------------------------

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("01/02/2006")
		},
		"formatDateInput": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02T15:04")
		},
		"formatDateTime": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("01/02 15:04")
		},
		"formatDateTimeFull": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("01/02/2006 3:04 PM")
		},
		"isQuerySpec": func(s string) bool { return strings.HasPrefix(s, "query:") },
		"isHTTPURL":    urlutil.IsHTTPURL,
		"isLocalFile":  urlutil.IsLocalFile,
		"localFileURL": func(val string) string { return urlutil.LocalFileURL(val, "/local/") },
		"fileBaseName": urlutil.FileBaseName,
		"fileIcon":     urlutil.FileIcon,
		"isPDF":        urlutil.IsPDF,
		"imageResult":  imageResult,
		"imageURL": func(partNumber, val string) string {
			return "/images/" + partNumber + "/" + val
		},
		// pfBadge mirrors the Ruby pf_badge helper: nil result → —, empty result → MISSING,
		// pass_fail truthy → PASS, falsy/nil → FAIL.
		"pfBadge": func(res *models.TestResult) template.HTML {
			if res == nil {
				return `<span class="badge bg-secondary">—</span>`
			}
			if res.Result == "" {
				return `<span class="badge bg-warning text-dark">MISSING</span>`
			}
			if res.PassFail != nil && *res.PassFail {
				return `<span class="badge bg-success">PASS</span>`
			}
			return `<span class="badge bg-danger">FAIL</span>`
		},
		// applyFormat applies an Excel number format string to a result value.
		// Handles: blank/General/@ (pass-through), "0"/"0.0"/"0.00"/"0.000"
		// (fixed decimal), "0%"/"0.0%" (percent). Non-numeric values fall through.
		"applyFormat": func(val, format string) string {
			if val == "" {
				return val
			}
			f := strings.TrimSpace(format)
			if f == "" || f == "General" || f == "@" {
				return val
			}
			isPercent := strings.HasSuffix(f, "%")
			numFmt := strings.TrimSuffix(f, "%")
			// Must be "0" or "0.NNN" pattern
			if numFmt != "0" && !strings.HasPrefix(numFmt, "0.") {
				return val
			}
			decimals := 0
			if idx := strings.Index(numFmt, "."); idx >= 0 {
				decimals = len(numFmt) - idx - 1
			}
			v, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return val
			}
			if isPercent {
				v *= 100
			}
			result := fmt.Sprintf("%.*f", decimals, v)
			if isPercent {
				result += "%"
			}
			return result
		},
	}
}

// imageResult returns true for VBA image filenames: SN-..._rID-..._tID-...
func imageResult(val string) bool {
	upper := strings.ToUpper(val)
	return strings.HasPrefix(upper, "SN-") &&
		strings.Contains(upper, "_RID-") &&
		strings.Contains(upper, "_TID-")
}
