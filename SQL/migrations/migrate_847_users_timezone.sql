-- migrate_847_users_timezone.sql
-- Bug fix (#847): add `users.timezone`, a per-user IANA timezone identifier
-- (e.g. 'America/Los_Angeles'). `form_row_history.changed_at` is stamped by
-- trg_form_row_history with GETDATE(), which on Azure SQL is UTC. The form-definition
-- history view buckets those timestamps by calendar day; without a per-user zone it
-- used the raw UTC day, so after ~5pm Pacific every edit landed on tomorrow's date and
-- "what changed today" returned nothing. See SQL/users.sql and arx_go/records.go.
--
-- BACKFILL: none needed. SQL Server populates existing rows with the DEFAULT as part of
-- an `ADD <col> NOT NULL DEFAULT <constant>` (that is what makes the statement legal on a
-- non-empty table), so every existing user gets 'America/Los_Angeles' — the shop's current
-- timezone, i.e. today's de facto behavior for everyone. Users change it per-account in
-- Settings -> My Preferences.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. timezone is a new NOT
-- NULL column with a DEFAULT, so an INSERT from an old binary (which doesn't set it) still
-- succeeds. An old binary keeps running against the migrated DB, so the mismatch banner
-- must not fire.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded; safe to re-run). Runs as a single batch (no `GO`).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.users', 'timezone') IS NULL
    ALTER TABLE dbo.users ADD timezone VARCHAR(64) NOT NULL DEFAULT 'America/Los_Angeles';
-- Postgres: ALTER TABLE users ADD COLUMN IF NOT EXISTS timezone VARCHAR(64) NOT NULL DEFAULT 'America/Los_Angeles';
