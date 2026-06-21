-- migrate_rename_test_records.sql
-- Commit 7 of db-table-rename effort: rename the test-records subsystem tables to the
-- go-forward convention — Forms → form, TestRecords → test_record, TestResults → test_result —
-- and modernize their legacy-cased columns (bare id PK, is_ boolean prefix, snake_case).
-- test_definition / test_definition_history tables keep their names but their capitalized
-- Parameter / Specification columns are lowercased.
-- See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically.
--
-- No data is changed.
--
-- Casing-only renames (ID→id, Parameter→parameter, Specification→specification,
-- serial_number_PN→serial_number_pn) use a case-sensitive (COLLATE ..._CS_AS) guard so the
-- step is skipped once the metadata casing has already been changed — SQL Server identifiers
-- are case-insensitive, so a plain COL_LENGTH guard would never skip a casing-only rename.
--
-- NOTE on constraint names: per-column DEFAULT constraints (DF_Forms_locked, DF_TestRecords_active,
-- etc.) are left under their legacy names — DEFAULT behavior binds to the column, not the name, so
-- this is harmless cosmetic drift (same decision as commit 6). The named FK constraints below are
-- renamed guarded; several were never created in the live DB (the DDL adds them via commented-out
-- ALTER statements), in which case the guarded rename simply no-ops.
--
-- IMPORTANT: trg_test_definition_history's body references the renamed Parameter/Specification
-- columns, so it is re-emitted below. sp_rename keeps it attached but does NOT rewrite its body.

-- ============================================================
-- STEP 1: RENAME TABLES
-- ============================================================

IF OBJECT_ID('dbo.Forms', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.Forms', 'form';

IF OBJECT_ID('dbo.TestRecords', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.TestRecords', 'test_record';

IF OBJECT_ID('dbo.TestResults', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.TestResults', 'test_result';

-- ============================================================
-- STEP 2: RENAME COLUMNS
-- ============================================================

-- form ---------------------------------------------------------
IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.form') AND name = 'ID' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.form.ID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.form', 'PNID') IS NOT NULL
    EXEC sp_rename 'dbo.form.PNID', 'part_number_id', 'COLUMN';

IF COL_LENGTH('dbo.form', 'locked') IS NOT NULL
    EXEC sp_rename 'dbo.form.locked', 'is_locked', 'COLUMN';

IF COL_LENGTH('dbo.form', 'active') IS NOT NULL
    EXEC sp_rename 'dbo.form.active', 'is_active', 'COLUMN';

-- test_record --------------------------------------------------
IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_record') AND name = 'ID' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_record.ID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.test_record', 'serial_number_PNDesc') IS NOT NULL
    EXEC sp_rename 'dbo.test_record.serial_number_PNDesc', 'serial_number_pn_desc', 'COLUMN';

IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_record') AND name = 'serial_number_PN' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_record.serial_number_PN', 'serial_number_pn', 'COLUMN';

IF COL_LENGTH('dbo.test_record', 'locked') IS NOT NULL
    EXEC sp_rename 'dbo.test_record.locked', 'is_locked', 'COLUMN';

IF COL_LENGTH('dbo.test_record', 'active') IS NOT NULL
    EXEC sp_rename 'dbo.test_record.active', 'is_active', 'COLUMN';

-- test_result --------------------------------------------------
IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_result') AND name = 'ID' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_result.ID', 'id', 'COLUMN';

-- test_definition (table name unchanged) -----------------------
IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_definition') AND name = 'Parameter' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_definition.Parameter', 'parameter', 'COLUMN';

IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_definition') AND name = 'Specification' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_definition.Specification', 'specification', 'COLUMN';

-- test_definition_history (table name unchanged) ---------------
IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_definition_history') AND name = 'Parameter' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_definition_history.Parameter', 'parameter', 'COLUMN';

IF EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.test_definition_history') AND name = 'Specification' COLLATE Latin1_General_CS_AS)
    EXEC sp_rename 'dbo.test_definition_history.Specification', 'specification', 'COLUMN';

-- ============================================================
-- STEP 3: RENAME NAMED FK CONSTRAINTS (guarded; several may not exist)
-- ============================================================

IF OBJECT_ID('FK_test_definition_Forms') IS NOT NULL
    EXEC sp_rename 'FK_test_definition_Forms', 'FK_test_definition_form';
IF OBJECT_ID('FK_TestRecords_Forms') IS NOT NULL
    EXEC sp_rename 'FK_TestRecords_Forms', 'FK_test_record_form';
IF OBJECT_ID('FK_TestResults_TestRecords') IS NOT NULL
    EXEC sp_rename 'FK_TestResults_TestRecords', 'FK_test_result_test_record';
IF OBJECT_ID('FK_TestResults_test_definition') IS NOT NULL
    EXEC sp_rename 'FK_TestResults_test_definition', 'FK_test_result_test_definition';

-- ============================================================
-- STEP 4: RE-EMIT TRIGGER BODY WITH LOWERCASED COLUMN NAMES
-- ============================================================
GO
CREATE OR ALTER TRIGGER dbo.trg_test_definition_history
ON dbo.test_definition
AFTER UPDATE
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.test_definition_history
      (test_id, changed_at, changed_by,
       type, parameter, specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_type,
       instrument_types, format, comment, category, sheet_name)
    SELECT
      id, GETDATE(),
      COALESCE(NULLIF(REPLACE(CONVERT(VARCHAR(128), CONTEXT_INFO()), CHAR(0), ''), ''), SYSTEM_USER),
      type, parameter, specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name
    FROM DELETED;
END
GO

-- ============================================================
-- STEP 5: UPDATE named_queries STORED SQL STRINGS
-- These embed the renamed table/column names directly. Idempotent.
-- ============================================================

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 20 serial_number FROM test_record WHERE form_id = @form_id AND is_active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC',
       updated_at = GETDATE()
WHERE  name = 'recent_serial_numbers_for_form';

UPDATE dbo.named_queries
SET    sql        = 'SELECT MAX(TRY_CAST(r.result AS INT)) FROM test_result r JOIN test_record tr ON r.record_id = tr.id WHERE r.test_id = @test_id AND tr.is_active = 1 AND CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)',
       updated_at = GETDATE()
WHERE  name = 'max_subbatch_result';
