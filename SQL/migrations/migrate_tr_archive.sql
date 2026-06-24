-- Migration: add test_definition.archived (#403 — TR archive/retire)
-- Replaces the hide_formula='HIDE' workaround for retiring a test step.
-- Additive and rollback-safe. Idempotent — safe to re-run.
-- Run once on ArxProd and ArxDev.

IF COL_LENGTH('dbo.test_definition', 'archived') IS NULL
BEGIN
    ALTER TABLE dbo.test_definition
        ADD archived BIT NOT NULL CONSTRAINT DF_test_definition_archived DEFAULT 0;
    PRINT 'Added test_definition.archived';
END
ELSE
    PRINT 'test_definition.archived already exists — no change';
