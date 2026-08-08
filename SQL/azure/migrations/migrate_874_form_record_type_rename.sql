-- migrate_874_form_record_type_rename.sql
-- #874: form_record.comments does not hold comments — it holds the record Type
-- ("New Release" / "Re-Test" / "Upgrade") shown in the UI. Pure rename, no behavior
-- change:
--     form_record.comments -> form_record.record_type
-- Scope is form_record only. record_events.comments and form_events.comments are
-- genuine free-text audit-trail comment columns and are NOT touched by this migration.
-- Bumps schema_version 8 -> 9.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#874 binary queries this column by its old name.
-- The Go change (models.TestRecord.RecordType, records.go/records_filters.go column
-- literals) and this migration must ship together — an older binary breaks after the
-- rename, and this build breaks against an un-renamed column. schema_version 8 -> 9
-- gates the rollout via the mismatch banner. Run once every client is on a #874 build.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on the old column existing / new not existing; safe to
-- re-run). Postgres equivalent follows in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Orphan/impact check — run against ArxProd BEFORE the rename below. ArxDev's seed
--    is clean, so this is here for the human running this against ArxProd to inspect:
--    any row means a site query/report/export outside this repo may reference the old
--    column name and needs updating by hand.
-- ------------------------------------------------------------------
-- SELECT COUNT(*) AS non_empty_comments_rows FROM dbo.form_record WHERE comments IS NOT NULL AND comments <> '';

-- ------------------------------------------------------------------
-- 2. Rename the column.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.form_record', 'comments') IS NOT NULL AND COL_LENGTH('dbo.form_record', 'record_type') IS NULL
    EXEC sp_rename 'dbo.form_record.comments', 'record_type', 'COLUMN';
-- Postgres: ALTER TABLE form_record RENAME COLUMN comments TO record_type;

-- ------------------------------------------------------------------
-- 3. Rewrite stored named_queries whose SQL text references form_record.comments.
--    This is row DATA (a query string in named_queries.sql), so sp_rename does NOT
--    touch it. The seeded named_queries.sql has no such reference (verified), but a
--    live DB may have accumulated custom saved queries that do — check ArxProd by hand
--    (SELECT * FROM dbo.named_queries WHERE sql LIKE '%form_record%comments%' OR
--    sql LIKE '%comments%form_record%') and update any that match before/after running
--    this migration.
-- ------------------------------------------------------------------

-- ------------------------------------------------------------------
-- 4. Bump schema_version 8 -> 9 (gates the rollout; guarded, only advances 8 -> 9).
-- ------------------------------------------------------------------
UPDATE dbo.app_config
   SET setting_value = '9'
 WHERE setting_key = 'schema_version'
   AND setting_value = '8';
