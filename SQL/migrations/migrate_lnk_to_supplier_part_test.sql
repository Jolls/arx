-- migrate_lnk_to_supplier_part_test.sql
-- Run this FIRST to validate against the _Test tables before touching prod.
-- Mirrors migrate_lnk_to_supplier_part.sql but operates on _Test tables only.
--
-- sp_rename prints "object reference" warnings — expected, not errors.
-- Run against the PartsMaster database.

BEGIN TRANSACTION;
BEGIN TRY

-- ============================================================
-- STEP 1: CREATE mfg_part_Test
-- ============================================================

IF OBJECT_ID('dbo.mfg_part_Test', 'U') IS NULL
BEGIN
    CREATE TABLE dbo.mfg_part_Test (
        id               INT           PRIMARY KEY IDENTITY,
        part_id          INT           NOT NULL,
        mfg_id           INT           NOT NULL,
        mfg_part_number  VARCHAR(100)  NOT NULL,
        description      VARCHAR(250)  NULL,
    );
END;

-- ============================================================
-- STEP 2: ADD ROLE FLAGS TO supplier_Test
-- ============================================================

IF NOT EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier_Test') AND name = 'is_supplier')
    ALTER TABLE dbo.supplier_Test ADD is_supplier BIT NOT NULL DEFAULT 1;

IF NOT EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier_Test') AND name = 'is_manufacturer')
    ALTER TABLE dbo.supplier_Test ADD is_manufacturer BIT NOT NULL DEFAULT 0;

-- ============================================================
-- STEP 3: DROP DEFAULT CONSTRAINTS ON LNK_Test
-- LNK_Test has auto-named constraints — find and drop dynamically.
-- ============================================================

DECLARE @sql NVARCHAR(256), @constraint NVARCHAR(128), @col NVARCHAR(128);
DECLARE cur CURSOR FOR
    SELECT c.name, dc.name
    FROM sys.default_constraints dc
    JOIN sys.columns c ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
    WHERE dc.parent_object_id = OBJECT_ID('dbo.LNK_Test')
      AND c.name IN ('LNKCurrentCost', 'LNKUse', 'LNKChoice');
OPEN cur;
FETCH NEXT FROM cur INTO @col, @constraint;
WHILE @@FETCH_STATUS = 0
BEGIN
    SET @sql = N'ALTER TABLE dbo.LNK_Test DROP CONSTRAINT ' + QUOTENAME(@constraint);
    EXEC sp_executesql @sql;
    FETCH NEXT FROM cur INTO @col, @constraint;
END;
CLOSE cur; DEALLOCATE cur;

-- ============================================================
-- STEP 4: DROP OBSOLETE COLUMNS FROM LNK_Test
-- ============================================================

ALTER TABLE dbo.LNK_Test DROP COLUMN LNKMFRID, LNKMFRPNID, LNKUNID, LNKToPNID, LNKAtQty, LNKCurrentCost, LNKRFQDate;

-- ============================================================
-- STEP 5: RENAME TABLE LNK_Test → supplier_part_Test
-- ============================================================

EXEC sp_rename 'dbo.LNK_Test', 'supplier_part_Test';

-- ============================================================
-- STEP 6: RENAME COLUMNS ON supplier_part_Test
-- ============================================================

EXEC sp_rename 'dbo.supplier_part_Test.LNKID',          'id',            'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKSUID',         'supplier_id',   'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKPNID',         'part_id',       'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKVendorPN',     'supplier_pn',   'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKVendorDesc',   'supplier_desc', 'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKLeadtime',     'lead_time',     'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKChoice',       'preference',    'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKUse',          'is_active',     'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKMinIncrement', 'min_increment', 'COLUMN';
EXEC sp_rename 'dbo.supplier_part_Test.LNKSetupCost',    'setup_cost',    'COLUMN';

-- ============================================================
-- STEP 7: ADD DEFAULT CONSTRAINTS ON supplier_part_Test
-- ============================================================

ALTER TABLE dbo.supplier_part_Test ADD DEFAULT 1 FOR is_active;
ALTER TABLE dbo.supplier_part_Test ADD DEFAULT 1 FOR preference;

-- ============================================================
-- STEP 8: ADD mfg_part_id COLUMN TO supplier_part_Test
-- ============================================================

ALTER TABLE dbo.supplier_part_Test ADD mfg_part_id INT NULL;

COMMIT;
PRINT 'Test migration complete: LNK_Test → supplier_part_Test — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Test migration failed: ' + ERROR_MESSAGE();
END CATCH;
