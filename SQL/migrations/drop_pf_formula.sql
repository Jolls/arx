-- Migration: drop pf_formula from test_definition and test_definition_history
-- pf_formula was deprecated in v0.3.52 (#199); the app has not read or written
-- it since then. The trigger already omits it in the reference DDL.
--
-- Run order:
--   1. Recreate trigger without pf_formula (in case live DB still has the old version)
--   2. Drop column from test_definition
--   3. Drop column from test_definition_history

-- Step 1: recreate trigger (idempotent — DROP + CREATE)
IF OBJECT_ID('dbo.trg_test_definition_history', 'TR') IS NOT NULL
    DROP TRIGGER dbo.trg_test_definition_history;
GO
CREATE TRIGGER dbo.trg_test_definition_history
ON dbo.test_definition
AFTER UPDATE
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.test_definition_history
      (test_id, changed_at, changed_by,
       type, Parameter, Specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_type,
       instrument_types, format, comment, category, sheet_name)
    SELECT
      id, GETDATE(), SYSTEM_USER,
      type, Parameter, Specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name
    FROM DELETED;
END
GO

-- Step 2: drop column from the live table
ALTER TABLE dbo.test_definition DROP COLUMN pf_formula;

-- Step 3: drop column from the history table (historical values are orphaned; acceptable)
ALTER TABLE dbo.test_definition_history DROP COLUMN pf_formula;
