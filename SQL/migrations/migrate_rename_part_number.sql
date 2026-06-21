-- migrate_rename_part_number.sql
-- Commit 6 of db-table-rename effort: rename the hub table PN → part_number and
-- its legacy-prefixed columns to snake_case per go-forward convention.
-- See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically — the inbound
-- FKs from bom, part_attachment, po_line, supplier_part, mfg_part, price, and
-- inventory_transaction stay attached and keep pointing at the renamed PK.
--
-- No data is changed. Already-snake columns (part_number, category, has_bom,
-- revision, title, detail, release_status, user_field_1-10, price_id, stock_on_hand)
-- are left as-is.
--
-- IMPORTANT: both denormalized-count triggers (trg_FIL_part_count, trg_POL_part_count)
-- write to part_number columns, so their bodies are re-emitted below. sp_rename keeps
-- them attached but does NOT rewrite their bodies.
--
-- NOTE on DEFAULT constraint names: the live PN table was originally PN_Test, so its
-- DEFAULT constraints may carry auto-generated DF__PN_Test__* names rather than the
-- DF_PN_* names in the DDL. The renames below are guarded (IF OBJECT_ID ... IS NOT NULL),
-- so any that don't match are harmlessly skipped — DEFAULT behavior is unaffected either
-- way (constraints bind to the column, not the name). Only the structural CK/UQ/FK are
-- renamed here; the per-column DEFAULT names are left as legacy cosmetic drift.

-- ============================================================
-- STEP 1: RENAME TABLE
-- ============================================================

IF OBJECT_ID('dbo.PN', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.PN', 'part_number';

-- ============================================================
-- STEP 2: RENAME COLUMNS
-- ============================================================

IF COL_LENGTH('dbo.part_number', 'PNID') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNReqBy') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNReqBy', 'requested_by', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNNotes') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNNotes', 'notes', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNDate') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNDate', 'created_date', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNLastRollupCost') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNLastRollupCost', 'last_rollup_cost', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNLastRollupAt') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNLastRollupAt', 'last_rollup_at', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNFILLinks') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNFILLinks', 'attachment_count', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNFILIDPrimary') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNFILIDPrimary', 'primary_attachment_id', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNCurrentCost') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNCurrentCost', 'current_cost', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'active') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.active', 'is_active', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNPOLinks') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNPOLinks', 'po_line_count', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNDateModified') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNDateModified', 'modified_date', 'COLUMN';

IF COL_LENGTH('dbo.part_number', 'PNUNID') IS NOT NULL
    EXEC sp_rename 'dbo.part_number.PNUNID', 'unit_id', 'COLUMN';

-- ============================================================
-- STEP 3: RENAME STRUCTURAL CONSTRAINTS (guarded; legacy names may differ)
-- ============================================================

IF OBJECT_ID('UQ_PN_part_number') IS NOT NULL
    EXEC sp_rename 'UQ_PN_part_number', 'UQ_part_number_part_number';
IF OBJECT_ID('CK_PN_category') IS NOT NULL
    EXEC sp_rename 'CK_PN_category', 'CK_part_number_category';
IF OBJECT_ID('FK_PN_unit') IS NOT NULL
    EXEC sp_rename 'FK_PN_unit', 'FK_part_number_unit';

-- ============================================================
-- STEP 4: RE-EMIT TRIGGER BODIES WITH NEW TABLE/COLUMN NAMES
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
    SET    p.attachment_count = (SELECT COUNT(*) FROM dbo.part_attachment f WHERE f.part_id = p.id AND f.is_active = 1)
    FROM   dbo.part_number p
    JOIN   affected a ON a.id = p.id;
END;
GO

CREATE OR ALTER TRIGGER dbo.trg_POL_part_count
ON dbo.po_line
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
    SET    p.po_line_count = (SELECT COUNT(*) FROM dbo.po_line pol WHERE pol.part_id = p.id)
    FROM   dbo.part_number p
    JOIN   affected a ON a.id = p.id;
END;
GO

-- ============================================================
-- STEP 5: UPDATE named_queries STORED SQL STRINGS
-- These embed the PN table/column names directly. Idempotent.
-- ============================================================

UPDATE dbo.named_queries
SET    sql        = 'SELECT category FROM part_attachment WHERE part_id = (SELECT id FROM part_number WHERE part_number = @pn) AND is_active = 1',
       updated_at = GETDATE()
WHERE  name = 'fil_category_for_pn';

UPDATE dbo.named_queries
SET    sql        = 'SELECT part_number FROM part_number WHERE part_number LIKE @pattern AND is_active = 1 ORDER BY part_number DESC',
       updated_at = GETDATE()
WHERE  name = 'parts_matching';

UPDATE dbo.named_queries
SET    sql        = 'SELECT pn.part_number, pn.title FROM bom JOIN part_number pn ON bom.component_part_id = pn.id WHERE bom.parent_part_id = (SELECT id FROM part_number WHERE part_number = @pn) AND bom.line_number = @item',
       updated_at = GETDATE()
WHERE  name = 'bom_pn_by_item';

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part_number pn ON f.part_id = pn.id WHERE pn.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN pn.primary_attachment_id > 0 AND f.id = pn.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at = GETDATE()
WHERE  name = 'pn_primary_attachment';

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part_number pn ON f.part_id = pn.id WHERE pn.id = @pnid AND f.is_active = 1 ORDER BY CASE WHEN pn.primary_attachment_id > 0 AND f.id = pn.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at = GETDATE()
WHERE  name = 'form_primary_attachment';
