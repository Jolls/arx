-- migrate_rename_part_attachment.sql
-- Commit 3 of db-table-rename effort: rename table FIL → part_attachment and columns
-- to snake_case per go-forward convention. See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically.
--
-- No data is changed. The only schema change is naming.
--
-- IMPORTANT: After running the sp_rename steps, the trigger body is re-emitted below
-- via CREATE OR ALTER TRIGGER so it references the new table/column names.
-- sp_rename keeps the trigger attached but does NOT rewrite its body.

-- ============================================================
-- STEP 1: RENAME TABLE
-- ============================================================

IF OBJECT_ID('dbo.FIL', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.FIL', 'part_attachment';

-- ============================================================
-- STEP 2: RENAME COLUMNS
-- ============================================================

IF COL_LENGTH('dbo.part_attachment', 'FILID') IS NOT NULL
    EXEC sp_rename 'dbo.part_attachment.FILID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.part_attachment', 'FILPNID') IS NOT NULL
    EXEC sp_rename 'dbo.part_attachment.FILPNID', 'part_id', 'COLUMN';

IF COL_LENGTH('dbo.part_attachment', 'FILFileName') IS NOT NULL
    EXEC sp_rename 'dbo.part_attachment.FILFileName', 'file_name', 'COLUMN';

IF COL_LENGTH('dbo.part_attachment', 'FILPNRev') IS NOT NULL
    EXEC sp_rename 'dbo.part_attachment.FILPNRev', 'part_revision', 'COLUMN';

IF COL_LENGTH('dbo.part_attachment', 'order_id') IS NOT NULL
    EXEC sp_rename 'dbo.part_attachment.order_id', 'sort_order', 'COLUMN';

-- category and is_active are unchanged

-- ============================================================
-- STEP 3: RENAME CONSTRAINTS THAT EMBED THE OLD NAMES
-- ============================================================

IF OBJECT_ID('DF_FIL_order_id') IS NOT NULL
    EXEC sp_rename 'DF_FIL_order_id', 'DF_part_attachment_sort_order';

IF OBJECT_ID('DF_FIL_is_active') IS NOT NULL
    EXEC sp_rename 'DF_FIL_is_active', 'DF_part_attachment_is_active';

IF OBJECT_ID('FK_FIL_FILPNID') IS NOT NULL
    EXEC sp_rename 'FK_FIL_FILPNID', 'FK_part_attachment_part_id';

-- ============================================================
-- STEP 4: RE-EMIT TRIGGER BODY WITH NEW TABLE/COLUMN NAMES
-- sp_rename keeps the trigger attached but does NOT rewrite its body.
-- PN columns (PNFILLinks, PNID) are renamed in commit 6, not here.
-- ============================================================

CREATE OR ALTER TRIGGER dbo.trg_FIL_part_count
ON dbo.part_attachment
AFTER INSERT, UPDATE, DELETE
AS
BEGIN
    SET NOCOUNT ON;
    WITH affected (id) AS (
        SELECT part_id FROM inserted WHERE part_id IS NOT NULL
        UNION
        SELECT part_id FROM deleted  WHERE part_id IS NOT NULL
    )
    UPDATE p
    SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.part_attachment f WHERE f.part_id = p.PNID AND f.is_active = 1)
    FROM   dbo.PN p
    JOIN   affected a ON a.id = p.PNID;
END;
GO

-- ============================================================
-- STEP 5: UPDATE named_queries STORED SQL STRINGS
-- These stored SQL strings reference FIL table/columns directly
-- and must be updated so they work against the renamed schema.
-- PN columns (PNFILIDPrimary, PNID) are renamed in commit 6, not here.
-- Idempotent: safe to re-run (UPDATE WHERE name=...).
-- ============================================================

UPDATE dbo.named_queries
SET    sql = 'SELECT category FROM part_attachment WHERE part_id = (SELECT PNID FROM PN WHERE part_number = @pn) AND is_active = 1',
       updated_at = GETDATE()
WHERE  name = 'fil_category_for_pn';

UPDATE dbo.named_queries
SET    description = 'Primary attachment for any part number via PN.PNFILIDPrimary; falls back to lowest sort_order if no primary set.',
       sql         = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN PN pn ON f.part_id = pn.PNID WHERE pn.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN pn.PNFILIDPrimary > 0 AND f.id = pn.PNFILIDPrimary THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at  = GETDATE()
WHERE  name = 'pn_primary_attachment';

UPDATE dbo.named_queries
SET    description = 'Primary attachment for the form''s own part number via PN.PNFILIDPrimary; falls back to lowest sort_order if no primary set.',
       sql         = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN PN pn ON f.part_id = pn.PNID WHERE pn.PNID = @pnid AND f.is_active = 1 ORDER BY CASE WHEN pn.PNFILIDPrimary > 0 AND f.id = pn.PNFILIDPrimary THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at  = GETDATE()
WHERE  name = 'form_primary_attachment';
