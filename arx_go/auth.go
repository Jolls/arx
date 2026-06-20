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

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const ctxUserKey contextKey = 1

// User holds the identity of the logged-in user.
type User struct {
	ID           int
	Username     string
	DisplayName  string
	CanApprovePO bool
}

// --- DB helpers ---

func (h *Handler) userByID(ctx context.Context, id int) (*User, error) {
	var u User
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, can_approve_po FROM %s WHERE id = @p1 AND is_active = 1`,
		h.cfg.UsersTable()), id,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.CanApprovePO)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (h *Handler) userByUsername(ctx context.Context, username string) (*User, string, error) {
	var u User
	var hash string
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, password_hash FROM %s WHERE username = @p1 AND is_active = 1`,
		h.cfg.UsersTable()), username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &hash)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	return &u, hash, err
}

func (h *Handler) userCount(ctx context.Context) (int, error) {
	var n int
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE is_active = 1`, h.cfg.UsersTable()),
	).Scan(&n)
	return n, err
}

// --- Session helpers ---

// currentUser returns the logged-in user stored on the request context by RequireAuth.
func (h *Handler) currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxUserKey).(*User)
	return u
}

// withUser fetches the session user from the DB and stashes it on r's context.
func (h *Handler) withUser(r *http.Request) (*http.Request, *User) {
	sess := h.session(r)
	id, ok := sess.Values["user_id"].(int)
	if !ok || id <= 0 {
		return r, nil
	}
	u, err := h.userByID(r.Context(), id)
	if err != nil || u == nil {
		return r, nil
	}
	return r.WithContext(context.WithValue(r.Context(), ctxUserKey, u)), u
}

// --- Login / logout handlers ---

// GET /login
func (h *Handler) LoginGet(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if _, u := h.withUser(r); u != nil {
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
		"TestMode":        h.cfg.TestMode,
		"AppVersion":      h.cfg.Version,
		"DefaultUsername": os.Getenv("USERNAME"),
	})
}

// POST /login
func (h *Handler) LoginPost(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
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
		if err := h.createUser(r.Context(), username, displayName, password); err != nil {
			http.Redirect(w, r, "/login?error=could+not+create+user", http.StatusSeeOther)
			return
		}
		u, _, _ := h.userByUsername(r.Context(), username)
		if u != nil {
			h.saveSessionUser(w, r, u)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Normal login.
	u, hash, err := h.userByUsername(r.Context(), username)
	if err != nil {
		http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u == nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		http.Redirect(w, r, "/login?error=invalid+username+or+password", http.StatusSeeOther)
		return
	}
	h.saveSessionUser(w, r, u)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// POST /logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sess := h.session(r)
	delete(sess.Values, "user_id")
	sess.Save(r, w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) saveSessionUser(w http.ResponseWriter, r *http.Request, u *User) {
	sess := h.session(r)
	sess.Values["user_id"] = u.ID
	sess.Save(r, w)
}

// --- User creation / management ---

func (h *Handler) createUser(ctx context.Context, username, displayName, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = h.execContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (username, display_name, password_hash) VALUES (@p1, @p2, @p3)`,
		h.cfg.UsersTable()), username, displayName, string(hash))
	return err
}

func (h *Handler) listUsers(ctx context.Context) ([]map[string]any, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, is_active, can_approve_po FROM %s ORDER BY username`,
		h.cfg.UsersTable()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int
		var username, displayName string
		var isActive, canApprovePO bool
		if err := rows.Scan(&id, &username, &displayName, &isActive, &canApprovePO); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"ID":           id,
			"Username":     username,
			"DisplayName":  displayName,
			"IsActive":     isActive,
			"CanApprovePO": canApprovePO,
		})
	}
	return out, rows.Err()
}

// POST /settings/users — create a new user.
func (h *Handler) SettingsUsersCreate(w http.ResponseWriter, r *http.Request) {
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
	if err := h.createUser(r.Context(), username, displayName, password); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+create+user", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/password — reset a user's password.
func (h *Handler) SettingsUsersResetPassword(w http.ResponseWriter, r *http.Request) {
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
		`UPDATE %s SET is_active = 1 - is_active, updated_at = GETDATE() WHERE id = @p1`,
		h.cfg.UsersTable()), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+update+user", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// POST /settings/users/{userID}/toggle-approve — toggle can_approve_po (PO approver, #267).
func (h *Handler) SettingsUsersToggleApprove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET can_approve_po = 1 - can_approve_po, updated_at = GETDATE() WHERE id = @p1`,
		h.cfg.UsersTable()), id); err != nil {
		http.Redirect(w, r, "/settings?tab=users&error=could+not+update+user", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?tab=users", http.StatusSeeOther)
}

// --- Login template ---

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, data map[string]any) {
	data["CSRFToken"] = h.csrfToken(w, r)
	tmpl, err := template.New("").ParseFS(h.tmplFS, "templates/pm/login.html")
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "login.html", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}
