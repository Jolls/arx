package main

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const ctxUserKey contextKey = 1
const ctxSQLStatsKey contextKey = 2

// User holds the identity of the logged-in user.
type User struct {
	ID                int
	Username          string
	DisplayName       string
	CanApprovePO      bool
	CanApproveRecords bool
	// IsAdmin gates the user-management endpoints (issue #750).
	IsAdmin bool
	// Per-user PO defaults (issue #463); 0 = unset, falls back to global config.
	DefaultPOContactID  int
	DefaultPOReceiverID int
	// Per-user UI accent theme (issue #537); "" = unset, falls back to "blue".
	AccentColor string
	// Per-user post-login landing page (issue #282); a same-origin relative path
	// (e.g. "/", "/pos", "/?f0=as"). "" = unset, falls back to "/".
	DefaultRoute string
	// Per-user IANA timezone (issue #847), e.g. "America/Los_Angeles"; used to
	// bucket UTC audit timestamps into the user's local calendar day. NOT NULL in
	// the DB with a default, but "" (or an unknown zone) falls back to
	// defaultTimezone.
	Timezone string
}

// --- DB helpers ---

func (h *Handler) userByID(ctx context.Context, id int) (*User, error) {
	var u User
	var defContact, defReceiver sql.NullInt64
	var accentColor, defaultRoute sql.NullString
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, can_approve_po, can_approve_records, is_admin, default_po_contact_id, default_po_receiver_id, accent_color, default_route, timezone FROM %s WHERE id = @p1 AND is_active = %s`,
		h.cfg.UsersTable(), h.dia().BoolLiteral(true)), id,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.CanApprovePO, &u.CanApproveRecords, &u.IsAdmin, &defContact, &defReceiver, &accentColor, &defaultRoute, &u.Timezone)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	u.DefaultPOContactID = int(defContact.Int64)
	u.DefaultPOReceiverID = int(defReceiver.Int64)
	u.AccentColor = accentColor.String
	u.DefaultRoute = defaultRoute.String
	return &u, err
}

// userCacheEntry holds a cached session user with a TTL-based expiry.
type userCacheEntry struct {
	user    *User
	expires time.Time
}

// cachedUserByID returns the session user from h.userCache when a fresh entry
// exists, otherwise fetches it via userByID and caches the result. Only
// successful (non-nil) lookups are cached, so an inactive or deleted user is
// never cached and is re-checked (and rejected) on every request.
func (h *Handler) cachedUserByID(ctx context.Context, id int) (*User, error) {
	h.userMu.RLock()
	entry, ok := h.userCache[id]
	h.userMu.RUnlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.user, nil
	}

	u, err := h.userByID(ctx, id)
	if err != nil || u == nil {
		return u, err
	}

	h.userMu.Lock()
	h.userCache[id] = &userCacheEntry{user: u, expires: time.Now().Add(userCacheTTL)}
	h.userMu.Unlock()
	return u, nil
}

// invalidateUserCache evicts a user's cached entry so the next request
// re-fetches current fields from the DB. Called after any UPDATE that changes
// a field cachedUserByID caches (is_active, can_approve_po, can_approve_records).
func (h *Handler) invalidateUserCache(id int) {
	h.userMu.Lock()
	delete(h.userCache, id)
	h.userMu.Unlock()
}

func (h *Handler) userByUsername(ctx context.Context, username string) (*User, string, error) {
	var u User
	var hash string
	var defaultRoute sql.NullString
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, password_hash, default_route FROM %s WHERE username = @p1 AND is_active = %s`,
		h.cfg.UsersTable(), h.dia().BoolLiteral(true)), username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &hash, &defaultRoute)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	u.DefaultRoute = defaultRoute.String
	return &u, hash, err
}

func (h *Handler) userCount(ctx context.Context) (int, error) {
	var n int
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE is_active = %s`, h.cfg.UsersTable(), h.dia().BoolLiteral(true)),
	).Scan(&n)
	return n, err
}

// --- Session helpers ---

// currentUser returns the logged-in user stored on the request context by RequireAuth.
func (h *Handler) currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxUserKey).(*User)
	return u
}

// sessionIdleTimeout logs a session out after this long with no authenticated
// request, independent of the cookie's 30-day absolute lifetime (#757). This
// is a shop tool where a session may be left open for long stretches, so the
// window is generous.
const sessionIdleTimeout = 7 * 24 * time.Hour

// withUser fetches the session user from the DB and stashes it on r's context.
// It also enforces sessionIdleTimeout: a session whose last authenticated
// request predates the timeout is treated as logged out.
func (h *Handler) withUser(w http.ResponseWriter, r *http.Request) (*http.Request, *User) {
	sess := h.session(r)
	id, ok := sess.Values["user_id"].(int)
	if !ok || id <= 0 {
		return r, nil
	}
	now := time.Now().Unix()
	if last, ok := sess.Values["last_activity"].(int64); ok && now-last > int64(sessionIdleTimeout/time.Second) {
		delete(sess.Values, "user_id")
		sess.Save(r, w)
		return r, nil
	}
	u, err := h.cachedUserByID(r.Context(), id)
	if err != nil || u == nil {
		return r, nil
	}
	// Only re-stamp last_activity (and re-save the cookie) once it's gone stale
	// by more than a coarse granularity — every authenticated request re-signing
	// and re-writing the session cookie is unnecessary work when the idle
	// timeout only needs minute-level precision, not per-request precision.
	const activityStampGranularity = 5 * time.Minute
	if last, ok := sess.Values["last_activity"].(int64); !ok || now-last > int64(activityStampGranularity/time.Second) {
		sess.Values["last_activity"] = now
		sess.Save(r, w)
	}
	return r.WithContext(context.WithValue(r.Context(), ctxUserKey, u)), u
}

// --- Login throttling (#757) ---
// Keyed by lowercased username, not RemoteAddr: the app binds 127.0.0.1 only
// (main.go), so every request's RemoteAddr is loopback and per-IP keying would
// be meaningless. A short fixed cooldown (not an escalating hard lock) is the
// intended tradeoff for this localhost-only, single-shop threat model.
type loginAttempt struct {
	fails   int
	lockCap time.Time // requests before this are rejected
}

const loginMaxFails = 10
const loginLockout = 1 * time.Minute

// maxTrackedLogins caps loginAttempts so failed logins against arbitrary
// (attacker-supplied) usernames can't grow the map without bound. The map only
// holds transient throttle counters, so dropping them under a flood costs at
// most a re-accumulation window.
const maxTrackedLogins = 1024

func (h *Handler) loginBlocked(user string) bool {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	a, ok := h.loginAttempts[user]
	return ok && a.fails >= loginMaxFails && time.Now().Before(a.lockCap)
}

func (h *Handler) noteLoginFail(user string) {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	a, ok := h.loginAttempts[user]
	if ok && a.fails >= loginMaxFails && time.Now().After(a.lockCap) {
		// A prior lockout has fully elapsed — start the count over so a legit
		// user isn't stuck at one attempt per lockout window forever.
		delete(h.loginAttempts, user)
		ok = false
	}
	if !ok {
		if len(h.loginAttempts) >= maxTrackedLogins {
			h.loginAttempts = make(map[string]*loginAttempt)
		}
		a = &loginAttempt{}
		h.loginAttempts[user] = a
	}
	a.fails++
	if a.fails >= loginMaxFails {
		a.lockCap = time.Now().Add(loginLockout)
	}
}

func (h *Handler) noteLoginOK(user string) {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	delete(h.loginAttempts, user)
}

// --- Login / logout handlers ---

// GET /login
func (h *Handler) LoginGet(w http.ResponseWriter, r *http.Request) {
	if h.dbUnusable() {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if _, u := h.withUser(w, r); u != nil && h.schemaMismatch == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	n, err := h.userCount(r.Context())
	if err != nil {
		http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderLogin(w, r, map[string]any{
		"Bootstrap":       n == 0,
		"Error":           r.URL.Query().Get("error"),
		"Notice":          r.URL.Query().Get("notice"),
		"TestMode":        h.cfg.TestMode,
		"AppVersion":      h.cfg.Version,
		"DefaultUsername": os.Getenv("USERNAME"),
	})
}

// POST /login
func (h *Handler) LoginPost(w http.ResponseWriter, r *http.Request) {
	if h.dbUnusable() {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	n, err := h.userCount(r.Context())
	if err != nil {
		http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Bootstrap path: create first admin.
	if n == 0 {
		displayName := strings.TrimSpace(r.FormValue("display_name"))
		if username == "" || password == "" || displayName == "" {
			http.Redirect(w, r, "/login?error=all+fields+required", http.StatusSeeOther)
			return
		}
		if err := h.createUser(r.Context(), username, displayName, password, true); err != nil {
			http.Redirect(w, r, "/login?error=could+not+create+user", http.StatusSeeOther)
			return
		}
		u, _, _ := h.userByUsername(r.Context(), username)
		if u != nil {
			h.saveSessionUser(w, r, u)
		}
		http.Redirect(w, r, landingRoute(u), http.StatusSeeOther)
		return
	}

	// Normal login.
	loginKey := strings.ToLower(username)
	if h.loginBlocked(loginKey) {
		http.Redirect(w, r, "/login?error=too+many+attempts,+try+again+shortly", http.StatusSeeOther)
		return
	}
	u, hash, err := h.userByUsername(r.Context(), username)
	if err != nil {
		http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u == nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		h.noteLoginFail(loginKey)
		http.Redirect(w, r, "/login?error=invalid+username+or+password", http.StatusSeeOther)
		return
	}
	h.noteLoginOK(loginKey)
	h.saveSessionUser(w, r, u)
	http.Redirect(w, r, landingRoute(u), http.StatusSeeOther)
}

// landingRoute returns the path the app root ("/") redirects each user to,
// honoring their default_route preference (issue #282). Stored values are
// same-origin relative paths (sanitized on save); anything unset or unexpected
// falls back to the parts list. A value whose path is the root itself ("/" or
// "/?…") would redirect back to the dispatcher and loop, so it also falls back
// to "/parts". The "//" guard is defense-in-depth against a protocol-relative
// value.
func landingRoute(u *User) string {
	if u != nil {
		p := u.DefaultRoute
		if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") &&
			p != "/" && !strings.HasPrefix(p, "/?") {
			return p
		}
	}
	return "/parts"
}

// POST /logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sess := h.session(r)
	delete(sess.Values, "user_id")
	delete(sess.Values, "csrf_token")
	sess.Save(r, w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) saveSessionUser(w http.ResponseWriter, r *http.Request, u *User) {
	sess := h.session(r)
	sess.Values["user_id"] = u.ID
	sess.Values["last_activity"] = time.Now().Unix() // reset the idle clock on login (#757)
	delete(sess.Values, "csrf_token")                // rotate CSRF across the privilege change (#757)
	sess.Save(r, w)
}

// --- User creation / management ---

// createUser inserts a user. admin seeds is_admin — true only for the first-run
// bootstrap user (issue #750); users added later via Settings start non-admin.
func (h *Handler) createUser(ctx context.Context, username, displayName, password string, admin bool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = h.execContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (username, display_name, password_hash, is_admin) VALUES (@p1, @p2, @p3, @p4)`,
		h.cfg.UsersTable()), username, displayName, string(hash), admin)
	return err
}

func (h *Handler) listUsers(ctx context.Context) ([]map[string]any, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, is_active, can_approve_po, can_approve_records, is_admin FROM %s ORDER BY username`,
		h.cfg.UsersTable()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int
		var username, displayName string
		var isActive, canApprovePO, canApproveRecords, isAdmin bool
		if err := rows.Scan(&id, &username, &displayName, &isActive, &canApprovePO, &canApproveRecords, &isAdmin); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"ID":                id,
			"Username":          username,
			"DisplayName":       displayName,
			"IsActive":          isActive,
			"CanApprovePO":      canApprovePO,
			"CanApproveRecords": canApproveRecords,
			"IsAdmin":           isAdmin,
		})
	}
	return out, rows.Err()
}

// requireAdmin writes a 403 and returns false unless the session user is an
// admin. Every user-management endpoint gates on it so a non-admin can't
// self-grant rights, reset passwords, or deactivate others (issue #750).
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if cu := h.currentUser(r); cu != nil && cu.IsAdmin {
		return true
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

// POST /settings/users — create a new user.
func (h *Handler) SettingsUsersCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")
	if username == "" || displayName == "" || password == "" {
		http.Redirect(w, r, "/settings?tab=users&error=all+fields+required", http.StatusSeeOther)
		return
	}
	if err := h.createUser(r.Context(), username, displayName, password, false); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+create+user", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/password — reset a user's password.
func (h *Handler) SettingsUsersResetPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	password := r.FormValue("password")
	if password == "" {
		http.Redirect(w, r, "/settings?tab=users&error=password+required", http.StatusSeeOther)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "hashing error", http.StatusInternalServerError)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET password_hash=@p1, updated_at=GETDATE() WHERE id=@p2`,
		h.cfg.UsersTable()), string(hash), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+reset+password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/toggle-active — toggle is_active.
func (h *Handler) SettingsUsersToggleActive(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if cu := h.currentUser(r); cu != nil && cu.ID == id {
		http.Redirect(w, r, "/settings?tab=users&error=cannot+deactivate+your+own+account", http.StatusSeeOther)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET is_active = %s, updated_at = GETDATE() WHERE id = @p1`,
		h.cfg.UsersTable(), h.dia().ToggleBoolExpr("is_active")), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+update+user", http.StatusSeeOther)
		return
	}
	h.invalidateUserCache(id)
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/toggle-approve — toggle can_approve_po (PO approver, #267).
func (h *Handler) SettingsUsersToggleApprove(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET can_approve_po = %s, updated_at = GETDATE() WHERE id = @p1`,
		h.cfg.UsersTable(), h.dia().ToggleBoolExpr("can_approve_po")), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+update+user", http.StatusSeeOther)
		return
	}
	h.invalidateUserCache(id)
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/toggle-approve-records — toggle can_approve_records (TR reviewer, #249).
func (h *Handler) SettingsUsersToggleApproveRecords(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET can_approve_records = %s, updated_at = GETDATE() WHERE id = @p1`,
		h.cfg.UsersTable(), h.dia().ToggleBoolExpr("can_approve_records")), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+update+user", http.StatusSeeOther)
		return
	}
	h.invalidateUserCache(id)
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/toggle-admin — toggle is_admin (issue #750).
// An admin can't remove their own admin rights, mirroring the self-deactivate
// guard, so the last admin can't accidentally lock everyone out of user admin.
func (h *Handler) SettingsUsersToggleAdmin(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if cu := h.currentUser(r); cu != nil && cu.ID == id {
		http.Redirect(w, r, "/settings?tab=users&error=cannot+remove+your+own+admin+rights", http.StatusSeeOther)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET is_admin = %s, updated_at = GETDATE() WHERE id = @p1`,
		h.cfg.UsersTable(), h.dia().ToggleBoolExpr("is_admin")), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+update+user", http.StatusSeeOther)
		return
	}
	h.invalidateUserCache(id)
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// --- Login template ---

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, data map[string]any) {
	data["CSRFToken"] = h.csrfToken(w, r)
	data["CompanyLogo"] = h.companyLogoURL()
	data["SchemaMismatch"] = h.schemaMismatch
	tmpl, err := template.New("").ParseFS(h.tmplFS, "templates/shared/login.html")
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "login.html", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}
