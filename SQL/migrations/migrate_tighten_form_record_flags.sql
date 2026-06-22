-- migrate_tighten_form_record_flags.sql
-- form.is_locked / form.is_active and test_record.is_locked / test_record.is_active were
-- nullable with no DEFAULT constraint in the live DB, though the DDL (SQL/TestRecords.sql)
-- declares them BIT NOT NULL DEFAULT 0 / 1. (Pre-existing drift from the old Forms/TestRecords
-- tables; the app always sets these on insert, so no data is actually NULL in practice.)
--
-- This reconciles live to the DDL: backfill any NULLs, add the DEFAULT constraints, set NOT NULL.
-- Run against BOTH ArxProd and ArxDev. Idempotent (safe to re-run).

-- 1) Backfill any NULLs to the documented defaults.
UPDATE dbo.form        SET is_locked = 0 WHERE is_locked IS NULL;
UPDATE dbo.form        SET is_active = 1 WHERE is_active IS NULL;
UPDATE dbo.test_record SET is_locked = 0 WHERE is_locked IS NULL;
UPDATE dbo.test_record SET is_active = 1 WHERE is_active IS NULL;

-- 2) Add DEFAULT constraints (guarded — names match the DDL).
IF NOT EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_form_is_locked')
    ALTER TABLE dbo.form ADD CONSTRAINT DF_form_is_locked DEFAULT 0 FOR is_locked;
IF NOT EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_form_is_active')
    ALTER TABLE dbo.form ADD CONSTRAINT DF_form_is_active DEFAULT 1 FOR is_active;
IF NOT EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_test_record_is_locked')
    ALTER TABLE dbo.test_record ADD CONSTRAINT DF_test_record_is_locked DEFAULT 0 FOR is_locked;
IF NOT EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_test_record_is_active')
    ALTER TABLE dbo.test_record ADD CONSTRAINT DF_test_record_is_active DEFAULT 1 FOR is_active;

-- 3) Enforce NOT NULL (no-op if already NOT NULL).
ALTER TABLE dbo.form        ALTER COLUMN is_locked BIT NOT NULL;
ALTER TABLE dbo.form        ALTER COLUMN is_active BIT NOT NULL;
ALTER TABLE dbo.test_record ALTER COLUMN is_locked BIT NOT NULL;
ALTER TABLE dbo.test_record ALTER COLUMN is_active BIT NOT NULL;
