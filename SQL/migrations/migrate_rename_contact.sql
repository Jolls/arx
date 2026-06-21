-- migrate_rename_contact.sql
-- Commit 1 of db-table-rename effort: rename table CN → contact and all columns
-- to snake_case per go-forward convention. See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically.
--
-- No data is changed. The only schema change is naming.

-- ============================================================
-- STEP 1: RENAME TABLE
-- ============================================================

IF OBJECT_ID('dbo.CN', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.CN', 'contact';

-- ============================================================
-- STEP 2: RENAME COLUMNS
-- ============================================================

IF COL_LENGTH('dbo.contact', 'CNID') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNSUID') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNSUID', 'company_id', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNName') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNName', 'display_name', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNAddress') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNAddress', 'address', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNCity') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNCity', 'city', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNState') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNState', 'state', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNZipcode') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNZipcode', 'zipcode', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNCountry') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNCountry', 'country', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNPhone1') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNPhone1', 'phone_1', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNPhone2') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNPhone2', 'phone_2', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNFAX') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNFAX', 'fax', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNWeb') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNWeb', 'website', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNEmail') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNEmail', 'email', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNActive') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNActive', 'is_active', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNUserAccountLink') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNUserAccountLink', 'user_account_link', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNDateModified') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNDateModified', 'updated_at', 'COLUMN';

IF COL_LENGTH('dbo.contact', 'CNNotes') IS NOT NULL
    EXEC sp_rename 'dbo.contact.CNNotes', 'notes', 'COLUMN';

-- ============================================================
-- STEP 3: RENAME DEFAULT CONSTRAINT
-- ============================================================

IF OBJECT_ID('DF_CN_CNDateModified') IS NOT NULL
    EXEC sp_rename 'DF_CN_CNDateModified', 'DF_contact_updated_at';
