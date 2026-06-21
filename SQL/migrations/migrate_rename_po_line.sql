-- migrate_rename_po_line.sql
-- Commit 5 of db-table-rename effort: rename table POL → po_line and all columns
-- to snake_case per go-forward convention. See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically.
--
-- No data is changed. The only schema change is naming.
--
-- IMPORTANT: After the sp_rename steps, trg_POL_part_count's body is re-emitted
-- below via CREATE OR ALTER TRIGGER so it references the new table/column names.
-- sp_rename keeps the trigger attached but does NOT rewrite its body.

-- ============================================================
-- STEP 1: RENAME TABLE
-- ============================================================

IF OBJECT_ID('dbo.POL', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.POL', 'po_line';

-- ============================================================
-- STEP 2: RENAME COLUMNS
-- ============================================================

IF COL_LENGTH('dbo.po_line', 'POLID') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLID', 'id', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLPOID') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLPOID', 'po_id', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLPNID') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLPNID', 'part_id', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLPNPartNumber') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLPNPartNumber', 'part_number_snapshot', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLRev') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLRev', 'revision_snapshot', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLItem') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLItem', 'line_number', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLDesc') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLDesc', 'description', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLQty') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLQty', 'qty', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'POLCost') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.POLCost', 'unit_cost', 'COLUMN';

IF COL_LENGTH('dbo.po_line', 'VendorPN') IS NOT NULL
    EXEC sp_rename 'dbo.po_line.VendorPN', 'vendor_part_number', 'COLUMN';

-- ============================================================
-- STEP 3: RENAME CONSTRAINTS THAT EMBED THE OLD NAMES
-- ============================================================

IF OBJECT_ID('FK_POL_PO') IS NOT NULL
    EXEC sp_rename 'FK_POL_PO', 'FK_po_line_po';

IF OBJECT_ID('FK_POL_PN') IS NOT NULL
    EXEC sp_rename 'FK_POL_PN', 'FK_po_line_part';

-- inventory_transaction's FK to the (now renamed) line table
IF OBJECT_ID('FK_inv_txn_POL') IS NOT NULL
    EXEC sp_rename 'FK_inv_txn_POL', 'FK_inv_txn_po_line';

-- ============================================================
-- STEP 4: RE-EMIT TRIGGER BODY WITH NEW TABLE/COLUMN NAMES
-- sp_rename keeps the trigger attached but does NOT rewrite its body.
-- Counts ALL rows (no is_active filter, unlike trg_FIL_part_count).
-- PN columns (PNPOLinks, PNID) are renamed in commit 6, not here.
-- ============================================================

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
    SET    p.PNPOLinks = (SELECT COUNT(*) FROM dbo.po_line pol WHERE pol.part_id = p.PNID)
    FROM   dbo.PN p
    JOIN   affected a ON a.id = p.PNID;
END;
GO

-- ============================================================
-- STEP 5: UPDATE named_queries STORED SQL STRINGS
-- pos_for_pn references POL columns directly. Idempotent: safe to re-run.
-- ============================================================

UPDATE dbo.named_queries
SET    sql        = 'SELECT purchase_order.number FROM po_line LEFT JOIN purchase_order ON po_line.po_id = purchase_order.id WHERE is_active = 1 AND po_line.part_number_snapshot LIKE @pn + ''%'' ORDER BY po_line.po_id DESC',
       updated_at = GETDATE()
WHERE  name = 'pos_for_pn';
