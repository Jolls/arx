-- migrate_users_default_route.sql
-- Per-user landing page preference (issue #282). Adds one nullable column so
-- each user can choose which page they land on after login (Settings → My
-- Preferences): any major tab, or a custom same-origin relative path (e.g. a
-- filtered list view like '/?f0=as'). Stored as the relative path; when NULL
-- the app falls back to '/'.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
-- Additive and rollback-safe: the column is nullable and a pre-#282 binary
-- never references it.

IF COL_LENGTH('dbo.users', 'default_route') IS NULL
    ALTER TABLE dbo.users ADD default_route VARCHAR(255) NULL;

-- Widen the column for databases that ran an earlier version of this script
-- (which created it as VARCHAR(20), too small for a custom filtered route).
-- Idempotent: altering to the same type is a no-op.
ALTER TABLE dbo.users ALTER COLUMN default_route VARCHAR(255) NULL;
