-- migrate_rename_test_record_family.sql
-- Traceability epic (#736) slice 1 (#737 testbed → #738): rename the test-record family,
-- pure renames, no behavior change:
--     test_record            -> form_record
--     test_result            -> result
--     test_definition        -> form_row
--     test_definition_history-> form_row_history   (column test_id -> form_row_id)
--     result.test_id                 -> form_row_id
--     record_event_results.test_id   -> form_row_id
-- and recreates the definition-history trigger against the new names, renames the
-- embedded DF_/FK_ constraint names to match, rewrites the two stored named_queries whose
-- SQL text referenced the old names (row data, not touched by sp_rename), and bumps
-- schema_version 4 -> 5.
--
-- The record_events.test_record_id column keeps its name (only its target table was
-- renamed; the inline FK follows the table object, so no action is needed for it).
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#738 binary queries these tables/columns by name.
-- The Go change (config.*Table() helpers, form_row_id column literals) and this migration
-- must ship together — an older binary breaks after the rename, and this build breaks
-- against un-renamed objects. schema_version 4 -> 5 gates the rollout via the mismatch
-- banner. Run once every client is on a #738 build.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on the old object existing / new not existing; safe to
-- re-run). Postgres equivalents follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Rename tables.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.test_record', 'U') IS NOT NULL AND OBJECT_ID('dbo.form_record', 'U') IS NULL
    EXEC sp_rename 'dbo.test_record', 'form_record';
IF OBJECT_ID('dbo.test_result', 'U') IS NOT NULL AND OBJECT_ID('dbo.result', 'U') IS NULL
    EXEC sp_rename 'dbo.test_result', 'result';
IF OBJECT_ID('dbo.test_definition_history', 'U') IS NOT NULL AND OBJECT_ID('dbo.form_row_history', 'U') IS NULL
    EXEC sp_rename 'dbo.test_definition_history', 'form_row_history';
IF OBJECT_ID('dbo.test_definition', 'U') IS NOT NULL AND OBJECT_ID('dbo.form_row', 'U') IS NULL
    EXEC sp_rename 'dbo.test_definition', 'form_row';
-- Postgres:
--   ALTER TABLE test_record             RENAME TO form_record;
--   ALTER TABLE test_result             RENAME TO result;
--   ALTER TABLE test_definition_history RENAME TO form_row_history;
--   ALTER TABLE test_definition         RENAME TO form_row;

-- ------------------------------------------------------------------
-- 2. Rename the test_id columns -> form_row_id (after the table renames above).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.result', 'test_id') IS NOT NULL AND COL_LENGTH('dbo.result', 'form_row_id') IS NULL
    EXEC sp_rename 'dbo.result.test_id', 'form_row_id', 'COLUMN';
IF COL_LENGTH('dbo.form_row_history', 'test_id') IS NOT NULL AND COL_LENGTH('dbo.form_row_history', 'form_row_id') IS NULL
    EXEC sp_rename 'dbo.form_row_history.test_id', 'form_row_id', 'COLUMN';
IF COL_LENGTH('dbo.record_event_results', 'test_id') IS NOT NULL AND COL_LENGTH('dbo.record_event_results', 'form_row_id') IS NULL
    EXEC sp_rename 'dbo.record_event_results.test_id', 'form_row_id', 'COLUMN';
-- Postgres:
--   ALTER TABLE result               RENAME COLUMN test_id TO form_row_id;
--   ALTER TABLE form_row_history      RENAME COLUMN test_id TO form_row_id;
--   ALTER TABLE record_event_results RENAME COLUMN test_id TO form_row_id;

-- ------------------------------------------------------------------
-- 3. Recreate the definition-history trigger against the new names. The old trigger's
--    body still names the pre-rename table/column, so it must be dropped and recreated
--    (sp_rename does not touch trigger bodies).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.trg_test_definition_history', 'TR') IS NOT NULL DROP TRIGGER dbo.trg_test_definition_history;
IF OBJECT_ID('dbo.trg_form_row_history', 'TR') IS NOT NULL DROP TRIGGER dbo.trg_form_row_history;
GO
CREATE TRIGGER dbo.trg_form_row_history
ON dbo.form_row
AFTER UPDATE
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.form_row_history
      (form_row_id, changed_at, changed_by,
       type, parameter, specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_type,
       instrument_types, format, comment, category, sheet_name)
    SELECT
      id, GETDATE(),
      REPLACE(CONVERT(VARCHAR(128), CONTEXT_INFO()), CHAR(0), ''),
      type, parameter, specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name
    FROM DELETED;
END
GO
-- Postgres: see SQL/postgres/triggers.sql (function+trigger renamed to trg_form_row_history).

-- ------------------------------------------------------------------
-- 4. Rename the embedded DF_/FK_ constraint names to match the new table names.
--    Cosmetic (constraints keep functioning under their old names), guarded on the
--    old-named object existing so this is safe if a live DB auto-named them differently.
-- ------------------------------------------------------------------
IF OBJECT_ID('DF_test_record_is_locked', 'D')   IS NOT NULL EXEC sp_rename 'DF_test_record_is_locked',   'DF_form_record_is_locked';
IF OBJECT_ID('DF_test_record_is_approved', 'D') IS NOT NULL EXEC sp_rename 'DF_test_record_is_approved', 'DF_form_record_is_approved';
IF OBJECT_ID('DF_test_record_is_active', 'D')   IS NOT NULL EXEC sp_rename 'DF_test_record_is_active',   'DF_form_record_is_active';
IF OBJECT_ID('FK_test_record_form', 'F')        IS NOT NULL EXEC sp_rename 'FK_test_record_form',        'FK_form_record_form';
IF OBJECT_ID('FK_test_record_lot', 'F')         IS NOT NULL EXEC sp_rename 'FK_test_record_lot',         'FK_form_record_lot';
IF OBJECT_ID('FK_test_record_build', 'F')       IS NOT NULL EXEC sp_rename 'FK_test_record_build',       'FK_form_record_build';
IF OBJECT_ID('DF_test_result_type', 'D')        IS NOT NULL EXEC sp_rename 'DF_test_result_type',        'DF_result_type';
IF OBJECT_ID('FK_test_result_test_record', 'F') IS NOT NULL EXEC sp_rename 'FK_test_result_test_record', 'FK_result_form_record';
IF OBJECT_ID('FK_test_result_test_definition', 'F') IS NOT NULL EXEC sp_rename 'FK_test_result_test_definition', 'FK_result_form_row';
IF OBJECT_ID('DF_test_definition_archived', 'D') IS NOT NULL EXEC sp_rename 'DF_test_definition_archived', 'DF_form_row_archived';
IF OBJECT_ID('FK_test_definition_form', 'F')    IS NOT NULL EXEC sp_rename 'FK_test_definition_form',     'FK_form_row_form';
GO

-- ------------------------------------------------------------------
-- 5. Rewrite stored named_queries whose SQL text references the old table/column names.
--    These queries are row DATA (a query string in named_queries.sql), so the sp_rename
--    of the table objects above does NOT touch them — without this, spec_nom auto-fill
--    breaks after the rename ("Invalid object name 'test_record'"). Idempotent (sets the
--    row to its final text; safe to re-run). NOTE: any *custom* named_queries a site added
--    that reference these tables/columns must be updated by hand — this covers only the
--    two canonical seeded rows.
-- ------------------------------------------------------------------
UPDATE dbo.named_queries
   SET sql = 'SELECT TOP 20 serial_number FROM form_record WHERE form_id = @form_id AND is_active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC'
 WHERE name = 'recent_serial_numbers_for_form';
UPDATE dbo.named_queries
   SET sql = 'SELECT MAX(TRY_CAST(r.result AS INT)) FROM result r JOIN form_record tr ON r.record_id = tr.id WHERE r.form_row_id = @test_id AND tr.is_active = 1 AND CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)'
 WHERE name = 'max_subbatch_result';
-- Postgres: identical UPDATEs (named_queries is dialect-agnostic row data).

-- ------------------------------------------------------------------
-- 6. Bump schema_version 4 -> 5 (gates the rollout; guarded, only advances 4 -> 5).
-- ------------------------------------------------------------------
UPDATE dbo.app_config
   SET setting_value = '5'
 WHERE setting_key = 'schema_version'
   AND setting_value = '4';
