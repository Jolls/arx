-- users: accounts, admin toggles and per-user preferences (#190, #251, #247). Sessions and CSRF live
-- in the signed cookie, not the DB, so this is the whole auth data layer. Password hashing stays in
-- the handler; these queries only store and return hashes.

-- name: GetActiveUserByID :one
SELECT id, username, display_name, can_approve_po, can_approve_records, is_admin,
       default_po_contact_id, default_po_receiver_id,
       COALESCE(accent_color, '') AS accent_color, COALESCE(default_route, '') AS default_route, timezone
FROM users WHERE id = sqlc.arg(id)::int AND is_active = TRUE;

-- name: GetActiveUserByUsername :one
SELECT id, username, display_name, password_hash, COALESCE(default_route, '') AS default_route
FROM users WHERE username = sqlc.arg(username)::text AND is_active = TRUE;

-- name: CountActiveUsers :one
SELECT COUNT(*)::int FROM users WHERE is_active = TRUE;

-- name: CreateUser :exec
INSERT INTO users (username, display_name, password_hash, is_admin)
VALUES (sqlc.arg(username), sqlc.arg(display_name), sqlc.arg(password_hash), sqlc.arg(is_admin)::bool);

-- name: ListUsers :many
SELECT id, username, display_name, is_active, can_approve_po, can_approve_records, is_admin
FROM users ORDER BY username;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id)::int;

-- name: ToggleUserActive :exec
UPDATE users SET is_active = NOT is_active, updated_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id)::int;

-- name: ToggleUserApprovePO :exec
UPDATE users SET can_approve_po = NOT can_approve_po, updated_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id)::int;

-- name: ToggleUserApproveRecords :exec
UPDATE users SET can_approve_records = NOT can_approve_records, updated_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id)::int;

-- name: ToggleUserAdmin :exec
UPDATE users SET is_admin = NOT is_admin, updated_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id)::int;

-- The per-user preference updates below deliberately leave updated_at alone (as before the conversion).

-- name: SetUserAccentColor :exec
UPDATE users SET accent_color = sqlc.arg(accent_color)::text WHERE id = sqlc.arg(id)::int;

-- name: SetUserTimezone :exec
UPDATE users SET timezone = sqlc.arg(timezone)::text WHERE id = sqlc.arg(id)::int;

-- name: SetUserDefaultRoute :exec
UPDATE users SET default_route = sqlc.arg(default_route)::text WHERE id = sqlc.arg(id)::int;

-- name: SetUserPODefaults :exec
UPDATE users SET default_po_contact_id = sqlc.narg(contact_id)::int, default_po_receiver_id = sqlc.narg(receiver_id)::int
WHERE id = sqlc.arg(id)::int;
