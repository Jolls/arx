-- migrate_users_accent_color.sql
-- Per-user UI accent theme (issue #537). Adds one nullable column to users so
-- each user can pick their own accent color preset (Settings → My
-- Preferences); when NULL the app falls back to the "blue" default.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
-- Additive and rollback-safe: the column is nullable and a pre-#537 binary
-- never references it.

IF COL_LENGTH('dbo.users', 'accent_color') IS NULL
    ALTER TABLE dbo.users ADD accent_color VARCHAR(20) NULL;

-- accent_color briefly shipped as a shop-wide app_config key before this
-- per-user column existed; the app no longer reads or writes that row, so
-- drop it. No-op (0 rows deleted) if it was never set.
DELETE FROM dbo.app_config WHERE setting_key = 'accent_color';
