-- migrate_schema_v4.sql
-- Seal the schema at version 4 (issue #540). RUN THIS LAST, only after all four of the
-- v4 non-backwards-compatible migrations have been applied to this database:
--     migrate_drop_has_bom.sql
--     migrate_drop_contact_user_account_link.sql
--     migrate_rename_named_queries_active.sql
--     migrate_release_status_check.sql
--
-- The app compares app_config.schema_version against config.ExpectedSchemaVersion (now "4")
-- and shows a mismatch banner otherwise. Bumping the version is what gates the rollout: a
-- pre-#540 binary against a v4 DB (and a #540 binary against a v3 DB) both surface the
-- banner instead of silently breaking. Run this only once every client is on a #540 build.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded: only advances 3 -> 4, safe to re-run).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

UPDATE dbo.app_config
   SET setting_value = '4'
 WHERE setting_key = 'schema_version'
   AND setting_value = '3';
