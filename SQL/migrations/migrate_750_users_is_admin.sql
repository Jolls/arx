-- migrate_750_users_is_admin.sql
-- Security (#750, epic #719, finding C3): add `users.is_admin` and gate the five
-- user-admin endpoints on it. Before this, any logged-in user could self-grant
-- approval rights, reset any password, or deactivate others. See SQL/users.sql.
--
-- BACKFILL: existing active users are set is_admin = 1. They already had the (ungated)
-- ability to manage users, so granting it preserves current behavior and — critically —
-- avoids locking every existing deployment out of user management. New users created
-- after this default to is_admin = 0; only an admin can promote them.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. is_admin is a new
-- NOT NULL column with a DEFAULT, so an INSERT from an old binary (which doesn't set it)
-- still succeeds. An old binary keeps running against the migrated DB, so the mismatch
-- banner must not fire.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded; safe to re-run). Runs as a single batch (no `GO`): some
-- clients — e.g. the Azure portal's query editor — send the whole script as one batch and
-- don't split on `GO`, so the backfill UPDATE referencing the just-added column uses
-- dynamic SQL (`EXEC(N'...')`) to defer name resolution to runtime, after the column exists.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Add is_admin NOT NULL DEFAULT 0.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.users', 'is_admin') IS NULL
    ALTER TABLE dbo.users ADD is_admin BIT NOT NULL DEFAULT 0;
-- Postgres: ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;

-- ------------------------------------------------------------------
-- 2. Backfill: grant admin to existing active users (deferred via dynamic SQL — see header).
--    Guarded so a re-run is a no-op once any user is already an admin.
-- ------------------------------------------------------------------
EXEC(N'IF NOT EXISTS (SELECT 1 FROM dbo.users WHERE is_admin = 1)
    UPDATE dbo.users SET is_admin = 1 WHERE is_active = 1');
-- Postgres: UPDATE users SET is_admin = TRUE WHERE is_active = TRUE
--           AND NOT EXISTS (SELECT 1 FROM users WHERE is_admin = TRUE);
