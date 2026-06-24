-- migrate_record_snapshot.sql — #487 record snapshot freeze
-- Adds pf_type and format snapshot columns to test_result so saved records render and
-- evaluate against the definition as it was when recorded, not the live definition.
-- Additive and idempotent; safe to run on ArxProd and ArxDev. No data change to existing rows.

IF COL_LENGTH('dbo.test_result', 'pf_type') IS NULL
BEGIN
    ALTER TABLE dbo.test_result ADD pf_type VARCHAR(255) NULL;
    PRINT 'Added test_result.pf_type';
END
ELSE
    PRINT 'test_result.pf_type already exists — no change';

IF COL_LENGTH('dbo.test_result', 'format') IS NULL
BEGIN
    ALTER TABLE dbo.test_result ADD format VARCHAR(255) NULL;
    PRINT 'Added test_result.format';
END
ELSE
    PRINT 'test_result.format already exists — no change';

-- type lets headings (type > 0) live in the snapshot so a saved record renders without the live
-- definition. DEFAULT 0 backfills existing rows as data rows (they are all data rows today).
IF COL_LENGTH('dbo.test_result', 'type') IS NULL
BEGIN
    ALTER TABLE dbo.test_result ADD type INT NOT NULL CONSTRAINT DF_test_result_type DEFAULT 0;
    PRINT 'Added test_result.type';
END
ELSE
    PRINT 'test_result.type already exists — no change';

IF COL_LENGTH('dbo.test_result', 'hide_formula') IS NULL
BEGIN
    ALTER TABLE dbo.test_result ADD hide_formula VARCHAR(255) NULL;
    PRINT 'Added test_result.hide_formula';
END
ELSE
    PRINT 'test_result.hide_formula already exists — no change';

IF COL_LENGTH('dbo.test_result', 'default_result') IS NULL
BEGIN
    ALTER TABLE dbo.test_result ADD default_result VARCHAR(255) NULL;
    PRINT 'Added test_result.default_result';
END
ELSE
    PRINT 'test_result.default_result already exists — no change';
