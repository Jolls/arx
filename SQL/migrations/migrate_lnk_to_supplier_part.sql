-- migrate_lnk_to_supplier_part.sql
-- Supersedes: migrate_lnk_mfr.sql (deleted)
-- Run AFTER migrate_lnk_to_supplier_part_test.sql has been validated.
--
-- Renames LNK → supplier_part and modernizes all column names.
-- Also creates mfg_part and adds role flags to supplier.
--
-- Strategy: rename in place (sp_rename) — no data movement.
-- sp_rename prints "object reference" warnings — expected, not errors.
--
-- AFTER RUNNING THIS: update _test.sql and triggers.sql to reference
-- supplier_part instead of LNK. Trigger rename deferred.
--
-- Run against the PartsMaster database.

BEGIN TRANSACTION;
BEGIN TRY

-- ============================================================
-- STEP 1: CREATE mfg_part
-- ============================================================

IF OBJECT_ID('dbo.mfg_part', 'U') IS NULL
BEGIN
    CREATE TABLE dbo.mfg_part (
        id               INT           PRIMARY KEY IDENTITY,
        part_id          INT           NOT NULL,
        mfg_id           INT           NOT NULL,
        mfg_part_number  VARCHAR(100)  NOT NULL,
        description      VARCHAR(250)  NULL,
    );
    ALTER TABLE dbo.mfg_part ADD CONSTRAINT FK_mfg_part_pn  FOREIGN KEY (part_id) REFERENCES dbo.PN (PNID);
    ALTER TABLE dbo.mfg_part ADD CONSTRAINT FK_mfg_part_mfg FOREIGN KEY (mfg_id)  REFERENCES dbo.supplier (id);
    CREATE UNIQUE INDEX UQ_mfg_part_combo ON dbo.mfg_part (part_id, mfg_id, mfg_part_number);
END;

-- ============================================================
-- STEP 2: ADD ROLE FLAGS TO supplier
-- Existing rows default to is_supplier=1, is_manufacturer=0.
-- ============================================================

IF NOT EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier') AND name = 'is_supplier')
    ALTER TABLE dbo.supplier ADD is_supplier BIT NOT NULL CONSTRAINT DF_supplier_is_supplier DEFAULT 1;

IF NOT EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier') AND name = 'is_manufacturer')
    ALTER TABLE dbo.supplier ADD is_manufacturer BIT NOT NULL CONSTRAINT DF_supplier_is_manufacturer DEFAULT 0;

-- ============================================================
-- STEP 3: DROP FK CONSTRAINTS ON LNK
-- Must happen before column drops and rename.
-- ============================================================

IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = 'FK_LNK_supplier' AND parent_object_id = OBJECT_ID('dbo.LNK'))
    ALTER TABLE dbo.LNK DROP CONSTRAINT FK_LNK_supplier;

IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = 'FK_LNK_PN' AND parent_object_id = OBJECT_ID('dbo.LNK'))
    ALTER TABLE dbo.LNK DROP CONSTRAINT FK_LNK_PN;

-- Only present if migrate_lnk_mfr.sql was previously run
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = 'FK_LNK_manufacturer' AND parent_object_id = OBJECT_ID('dbo.LNK'))
    ALTER TABLE dbo.LNK DROP CONSTRAINT FK_LNK_manufacturer;

-- ============================================================
-- STEP 4: DROP DEFAULT CONSTRAINTS ON LNK
-- Must drop before dropping or renaming the columns they're on.
-- ============================================================

IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_LNK_LNKCurrentCost')
    ALTER TABLE dbo.LNK DROP CONSTRAINT DF_LNK_LNKCurrentCost;

IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_LNK_LNKUse')
    ALTER TABLE dbo.LNK DROP CONSTRAINT DF_LNK_LNKUse;

IF EXISTS (SELECT 1 FROM sys.default_constraints WHERE name = 'DF_LNK_LNKChoice')
    ALTER TABLE dbo.LNK DROP CONSTRAINT DF_LNK_LNKChoice;

-- ============================================================
-- STEP 5: DROP OBSOLETE COLUMNS
-- LNKMFRID / LNKMFRPNID  — superseded by mfg_part table
-- LNKUNID                 — deferred (#314)
-- LNKToPNID               — deferred (#278)
-- LNKAtQty / LNKCurrentCost — pricing lives in price table (#304)
-- LNKRFQDate              — deferred to full RFQ workflow (#270)
-- ============================================================

ALTER TABLE dbo.LNK DROP COLUMN LNKMFRID, LNKMFRPNID, LNKUNID, LNKToPNID, LNKAtQty, LNKCurrentCost, LNKRFQDate;

-- ============================================================
-- STEP 6: RENAME TABLE LNK → supplier_part
-- ============================================================

EXEC sp_rename 'dbo.LNK', 'supplier_part';

-- ============================================================
-- STEP 7: RENAME COLUMNS
-- ============================================================

EXEC sp_rename 'dbo.supplier_part.LNKID',          'id',            'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKSUID',         'supplier_id',   'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKPNID',         'part_id',       'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKVendorPN',     'supplier_pn',   'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKVendorDesc',   'supplier_desc', 'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKLeadtime',     'lead_time',     'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKChoice',       'preference',    'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKUse',          'is_active',     'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKMinIncrement', 'min_increment', 'COLUMN';
EXEC sp_rename 'dbo.supplier_part.LNKSetupCost',    'setup_cost',    'COLUMN';

-- ============================================================
-- STEP 8: RECREATE DEFAULT CONSTRAINTS WITH NEW NAMES
-- ============================================================

ALTER TABLE dbo.supplier_part ADD CONSTRAINT DF_supplier_part_is_active  DEFAULT 1 FOR is_active;
ALTER TABLE dbo.supplier_part ADD CONSTRAINT DF_supplier_part_preference DEFAULT 1 FOR preference;

-- ============================================================
-- STEP 9: ADD mfg_part_id COLUMN
-- ============================================================

ALTER TABLE dbo.supplier_part ADD mfg_part_id INT NULL;

-- ============================================================
-- STEP 10: ADD FK CONSTRAINTS WITH NEW NAMES
-- ============================================================

ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_supplier FOREIGN KEY (supplier_id) REFERENCES dbo.supplier (id);
ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_pn       FOREIGN KEY (part_id)     REFERENCES dbo.PN (PNID);
ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_mfg_part FOREIGN KEY (mfg_part_id) REFERENCES dbo.mfg_part (id);

COMMIT;
PRINT 'Migration complete: LNK → supplier_part — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Migration failed: ' + ERROR_MESSAGE();
END CATCH;
