-- Migration: rename PNType → category, add has_bom BIT; rename 5 PN columns to snake_case
-- Run on the live database before deploying the updated binary.
-- Safe to run multiple times — each step is guarded.
-- PN_Test is migrated first as a dry run — verify its output before PN runs.

BEGIN TRANSACTION;
BEGIN TRY

    -- ── PN_Test (dry run — no constraints to rename, throwaway data) ──────────

    -- 1. Check for unexpected values in PN_Test first.
    SELECT DISTINCT PNType, COUNT(*) AS cnt
    FROM PN_Test
    GROUP BY PNType
    ORDER BY cnt DESC;

    -- 2. Rename PN_Test.PNType → category.
    IF COL_LENGTH('dbo.PN_Test', 'PNType') IS NOT NULL AND COL_LENGTH('dbo.PN_Test', 'category') IS NULL
    BEGIN
        EXEC sp_rename 'dbo.PN_Test.PNType', 'category', 'COLUMN';
        PRINT 'Renamed PN_Test.PNType → category';
    END

    -- 3. Migrate legacy values in PN_Test.
    UPDATE PN_Test SET category = 'BUY' WHERE category = 'PS';
    UPDATE PN_Test SET category = 'ASM' WHERE category = 'PL';
    UPDATE PN_Test SET category = 'ASM' WHERE category = 'CAT';
    UPDATE PN_Test SET category = 'BUY' WHERE category = '' OR category IS NULL;

    -- 4. Add has_bom to PN_Test.
    IF COL_LENGTH('dbo.PN_Test', 'has_bom') IS NULL
    BEGIN
        ALTER TABLE PN_Test ADD has_bom BIT NOT NULL DEFAULT 0;
        PRINT 'Added has_bom column to PN_Test';
    END

    -- 5. Set has_bom on PN_Test.
    UPDATE PN_Test SET has_bom = 1 WHERE category IN ('ASM', 'FORM') AND has_bom = 0;
    PRINT CONCAT('PN_Test: set has_bom=1 on ', @@ROWCOUNT, ' parts');

    -- 5a. Rename 5 PN_Test columns to snake_case (no constraints to rename — SELECT INTO copies none).
    IF COL_LENGTH('dbo.PN_Test', 'PNPartNumber') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN_Test.PNPartNumber', 'part_number', 'COLUMN';
        PRINT 'Renamed PN_Test.PNPartNumber → part_number';
    END
    IF COL_LENGTH('dbo.PN_Test', 'PNTitle') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN_Test.PNTitle', 'title', 'COLUMN';
        PRINT 'Renamed PN_Test.PNTitle → title';
    END
    IF COL_LENGTH('dbo.PN_Test', 'PNDetail') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN_Test.PNDetail', 'detail', 'COLUMN';
        PRINT 'Renamed PN_Test.PNDetail → detail';
    END
    IF COL_LENGTH('dbo.PN_Test', 'PNStatus') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN_Test.PNStatus', 'release_status', 'COLUMN';
        PRINT 'Renamed PN_Test.PNStatus → release_status';
    END
    IF COL_LENGTH('dbo.PN_Test', 'PNActive') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN_Test.PNActive', 'active', 'COLUMN';
        PRINT 'Renamed PN_Test.PNActive → active';
    END

    -- ── PN (production) ───────────────────────────────────────────────────────

    -- 6. Check for unexpected category values in PN before migrating.
    --    Review any rows returned here before proceeding.
    SELECT DISTINCT PNType, COUNT(*) AS cnt
    FROM PN
    GROUP BY PNType
    ORDER BY cnt DESC;

    -- 7. Rename PN.PNType → category.
    IF COL_LENGTH('dbo.PN', 'PNType') IS NOT NULL AND COL_LENGTH('dbo.PN', 'category') IS NULL
    BEGIN
        EXEC sp_rename 'dbo.PN.PNType', 'category', 'COLUMN';
        PRINT 'Renamed PN.PNType → category';
    END

    -- 8. Rename the default constraint (name may differ on live DB — check sys.default_constraints first).
    IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_PN_PNType')
    BEGIN
        EXEC sp_rename 'dbo.DF_PN_PNType', 'DF_PN_category', 'OBJECT';
        PRINT 'Renamed constraint DF_PN_PNType → DF_PN_category';
    END

    -- 9. Migrate legacy P&V category values to new names.
    UPDATE PN SET category = 'BUY' WHERE category = 'PS';   -- Purchased Spec → Buy
    UPDATE PN SET category = 'ASM' WHERE category = 'PL';   -- Parts List → Assembly
    UPDATE PN SET category = 'ASM' WHERE category = 'CAT';  -- Catalog (old BOM type) → Assembly
    UPDATE PN SET category = 'BUY' WHERE category = '' OR category IS NULL;
    -- DWG, DOC, FORM stay as-is (values unchanged).
    PRINT CONCAT('PN: migrated values. Remaining distinct categories: ', (SELECT COUNT(DISTINCT category) FROM PN));

    -- 10. Add has_bom column.
    IF COL_LENGTH('dbo.PN', 'has_bom') IS NULL
    BEGIN
        ALTER TABLE PN ADD has_bom BIT NOT NULL DEFAULT 0;
        PRINT 'Added has_bom column to PN';
    END

    -- 11. Set has_bom = 1 for parts that were previously BOM types (ASM and FORM).
    UPDATE PN SET has_bom = 1 WHERE category IN ('ASM', 'FORM') AND has_bom = 0;
    PRINT CONCAT('PN: set has_bom=1 on ', @@ROWCOUNT, ' parts');

    -- 12. Update default from '' to 'BUY' — drop old default constraint and add new one.
    --     The constraint name may still be the auto-generated PN_Test-era name on live DB.
    DECLARE @dfName SYSNAME;
    SELECT @dfName = dc.name
    FROM sys.default_constraints dc
    JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
    JOIN sys.tables t ON t.object_id = dc.parent_object_id
    WHERE t.name = 'PN' AND c.name = 'category';
    IF @dfName IS NOT NULL
        EXEC('ALTER TABLE dbo.PN DROP CONSTRAINT ' + @dfName);
    ALTER TABLE dbo.PN ADD CONSTRAINT DF_PN_category DEFAULT 'BUY' FOR category;
    PRINT 'Updated PN.category default to BUY';

    -- 13. Add CHECK constraint on category (only if no invalid values remain).
    IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = 'CK_PN_category')
    BEGIN
        ALTER TABLE PN ADD CONSTRAINT CK_PN_category
            CHECK (category IN ('ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'RAW', 'SVC', 'TOOL'));
        PRINT 'Added CK_PN_category constraint';
    END

    -- 14. Rename 5 PN columns to snake_case.
    IF COL_LENGTH('dbo.PN', 'PNPartNumber') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN.PNPartNumber', 'part_number', 'COLUMN';
        PRINT 'Renamed PN.PNPartNumber → part_number';
    END
    -- Rename UNIQUE constraint on PNPartNumber if it exists under the old name.
    IF EXISTS (SELECT 1 FROM sys.key_constraints WHERE name = 'UQ_PN_PNPartNumber')
        EXEC sp_rename 'dbo.UQ_PN_PNPartNumber', 'UQ_PN_part_number', 'OBJECT';

    IF COL_LENGTH('dbo.PN', 'PNTitle') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN.PNTitle', 'title', 'COLUMN';
        PRINT 'Renamed PN.PNTitle → title';
    END
    IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_PN_PNTitle')
        EXEC sp_rename 'dbo.DF_PN_PNTitle', 'DF_PN_title', 'OBJECT';

    IF COL_LENGTH('dbo.PN', 'PNDetail') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN.PNDetail', 'detail', 'COLUMN';
        PRINT 'Renamed PN.PNDetail → detail';
    END
    IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_PN_PNDetail')
        EXEC sp_rename 'dbo.DF_PN_PNDetail', 'DF_PN_detail', 'OBJECT';

    IF COL_LENGTH('dbo.PN', 'PNStatus') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN.PNStatus', 'release_status', 'COLUMN';
        PRINT 'Renamed PN.PNStatus → release_status';
    END
    IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_PN_PNStatus')
        EXEC sp_rename 'dbo.DF_PN_PNStatus', 'DF_PN_release_status', 'OBJECT';

    IF COL_LENGTH('dbo.PN', 'PNActive') IS NOT NULL
    BEGIN
        EXEC sp_rename 'dbo.PN.PNActive', 'active', 'COLUMN';
        PRINT 'Renamed PN.PNActive → active';
    END
    -- PNActive default constraint may have an auto-generated name from PN_Test era — find and rename it.
    DECLARE @dfActive SYSNAME;
    SELECT @dfActive = dc.name
    FROM sys.default_constraints dc
    JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
    JOIN sys.tables t ON t.object_id = dc.parent_object_id
    WHERE t.name = 'PN' AND c.name = 'active' AND dc.name <> 'DF_PN_active';
    IF @dfActive IS NOT NULL
        EXEC sp_rename @dfActive, 'DF_PN_active', 'OBJECT';

    -- 15. Update named_queries SQL strings to use new column names.
    UPDATE named_queries SET sql = REPLACE(REPLACE(sql, 'PNPartNumber', 'part_number'), 'PNActive', 'active')
    WHERE name IN ('fil_notes_for_pn', 'parts_matching', 'bom_pn_by_item', 'pn_primary_attachment');
    UPDATE named_queries SET sql = REPLACE(sql, 'PNTitle', 'title')
    WHERE name = 'bom_pn_by_item';
    PRINT CONCAT('Updated ', @@ROWCOUNT, ' named_queries rows');

    -- 16. Bump schema version to 2.
    UPDATE app_config      SET setting_value = '2' WHERE setting_key = 'schema_version';
    UPDATE app_config_Test SET setting_value = '2' WHERE setting_key = 'schema_version';
    PRINT 'Schema version → 2';

    COMMIT;
    PRINT 'Migration complete — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Migration failed: ' + ERROR_MESSAGE();
END CATCH;
