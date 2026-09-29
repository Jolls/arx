// Package auth is the users data layer (#190, #251): typed access to the users table via the
// sqlc-generated queries in auth.sql. Password hashing and session handling stay in the handlers;
// this package only stores and returns hashes.
package auth

import (
	"context"
	"database/sql"
	"errors"

	"arx/internal/dbq"
)

// User is the session identity: the active-user columns the handlers cache. Unset preferences are
// the zero value ("" / 0).
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

// Listed is one row of the Settings user-management table.
type Listed struct {
	ID                int
	Username          string
	DisplayName       string
	IsActive          bool
	CanApprovePO      bool
	CanApproveRecords bool
	IsAdmin           bool
}

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// ByID returns the active user, or (nil, nil) when the id is unknown or the user is inactive.
func (s *Service) ByID(ctx context.Context, id int) (*User, error) {
	r, err := s.q.GetActiveUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &User{
		ID: r.ID, Username: r.Username, DisplayName: r.DisplayName,
		CanApprovePO: r.CanApprovePo, CanApproveRecords: r.CanApproveRecords, IsAdmin: r.IsAdmin,
		DefaultPOContactID: deref(r.DefaultPoContactID), DefaultPOReceiverID: deref(r.DefaultPoReceiverID),
		AccentColor: r.AccentColor, DefaultRoute: r.DefaultRoute, Timezone: r.Timezone,
	}, nil
}

// ByUsername returns the active user (only the login fields filled) and their password hash, or
// (nil, "", nil) when the username is unknown or the user is inactive. Matching is exact.
func (s *Service) ByUsername(ctx context.Context, username string) (*User, string, error) {
	r, err := s.q.GetActiveUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &User{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, DefaultRoute: r.DefaultRoute}, r.PasswordHash, nil
}

// CountActive returns the number of active users; 0 means first run (bootstrap admin).
func (s *Service) CountActive(ctx context.Context) (int, error) { return s.q.CountActiveUsers(ctx) }

// Create inserts a user with an already-hashed password.
func (s *Service) Create(ctx context.Context, username, displayName, passwordHash string, admin bool) error {
	return s.q.CreateUser(ctx, dbq.CreateUserParams{
		Username: username, DisplayName: displayName, PasswordHash: passwordHash, IsAdmin: admin,
	})
}

// List returns every user (active or not), ordered by username.
func (s *Service) List(ctx context.Context) ([]Listed, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	var out []Listed
	for _, r := range rows {
		out = append(out, Listed{
			ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, IsActive: r.IsActive,
			CanApprovePO: r.CanApprovePo, CanApproveRecords: r.CanApproveRecords, IsAdmin: r.IsAdmin,
		})
	}
	return out, nil
}

// SetPassword stores a new password hash. An unknown id updates nothing and is not an error.
func (s *Service) SetPassword(ctx context.Context, id int, hash string) error {
	return s.q.SetUserPassword(ctx, dbq.SetUserPasswordParams{PasswordHash: hash, ID: id})
}

// The toggles flip one flag and bump updated_at. An unknown id updates nothing and is not an error.
func (s *Service) ToggleActive(ctx context.Context, id int) error {
	return s.q.ToggleUserActive(ctx, id)
}
func (s *Service) ToggleApprovePO(ctx context.Context, id int) error {
	return s.q.ToggleUserApprovePO(ctx, id)
}
func (s *Service) ToggleApproveRecords(ctx context.Context, id int) error {
	return s.q.ToggleUserApproveRecords(ctx, id)
}
func (s *Service) ToggleAdmin(ctx context.Context, id int) error { return s.q.ToggleUserAdmin(ctx, id) }

// The preference setters leave updated_at alone.
func (s *Service) SetAccentColor(ctx context.Context, id int, color string) error {
	return s.q.SetUserAccentColor(ctx, dbq.SetUserAccentColorParams{AccentColor: color, ID: id})
}

func (s *Service) SetTimezone(ctx context.Context, id int, tz string) error {
	return s.q.SetUserTimezone(ctx, dbq.SetUserTimezoneParams{Timezone: tz, ID: id})
}

func (s *Service) SetDefaultRoute(ctx context.Context, id int, route string) error {
	return s.q.SetUserDefaultRoute(ctx, dbq.SetUserDefaultRouteParams{DefaultRoute: route, ID: id})
}

// SetPODefaults stores the per-user PO contact/receiver defaults; a value <= 0 is stored as NULL.
func (s *Service) SetPODefaults(ctx context.Context, id, contactID, receiverID int) error {
	arg := dbq.SetUserPODefaultsParams{ID: id}
	if contactID > 0 {
		arg.ContactID = &contactID
	}
	if receiverID > 0 {
		arg.ReceiverID = &receiverID
	}
	return s.q.SetUserPODefaults(ctx, arg)
}
