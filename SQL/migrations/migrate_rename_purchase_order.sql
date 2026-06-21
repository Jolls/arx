-- migrate_rename_purchase_order.sql
-- Commit 4 of db-table-rename effort: rename tables PO → purchase_order and
-- PO_history → purchase_order_history per go-forward convention.
-- See docs/plans/db-table-rename-plan.md.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically.
--
-- Columns and PKs are already snake_case / bare `id` — there are NO column renames
-- in this commit. The only schema change is the table names plus tidying constraint
-- names that embed the old "PO" abbreviation.
--
-- The PO_Number_Seq sequence is NOT renamed (it is referenced as dbo.PO_Number_Seq
-- in Go query strings, which stay unchanged).
--
-- IMPORTANT: After the sp_rename steps, trg_PO_company_count's body is re-emitted
-- below via CREATE OR ALTER TRIGGER so it references the new table name.
-- sp_rename keeps the trigger attached but does NOT rewrite its body.

-- ============================================================
-- STEP 1: RENAME TABLES
-- ============================================================

IF OBJECT_ID('dbo.PO_history', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.PO_history', 'purchase_order_history';

IF OBJECT_ID('dbo.PO', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.PO', 'purchase_order';

-- ============================================================
-- STEP 2: RENAME CONSTRAINTS THAT EMBED THE OLD NAMES
-- (No column renames — columns are already snake_case.)
-- ============================================================

-- purchase_order
IF OBJECT_ID('UQ_PO_number') IS NOT NULL
    EXEC sp_rename 'UQ_PO_number', 'UQ_purchase_order_number';
IF OBJECT_ID('FK_PO_company') IS NOT NULL
    EXEC sp_rename 'FK_PO_company', 'FK_purchase_order_company';
IF OBJECT_ID('FK_PO_receiver') IS NOT NULL
    EXEC sp_rename 'FK_PO_receiver', 'FK_purchase_order_receiver';
IF OBJECT_ID('DF_PO_date_modified') IS NOT NULL
    EXEC sp_rename 'DF_PO_date_modified', 'DF_purchase_order_date_modified';
IF OBJECT_ID('DF_PO_internal_notes') IS NOT NULL
    EXEC sp_rename 'DF_PO_internal_notes', 'DF_purchase_order_internal_notes';
IF OBJECT_ID('DF_PO_is_active') IS NOT NULL
    EXEC sp_rename 'DF_PO_is_active', 'DF_purchase_order_is_active';
IF OBJECT_ID('DF_PO_status') IS NOT NULL
    EXEC sp_rename 'DF_PO_status', 'DF_purchase_order_status';
IF OBJECT_ID('CK_PO_status') IS NOT NULL
    EXEC sp_rename 'CK_PO_status', 'CK_purchase_order_status';
IF OBJECT_ID('DF_PO_approval_status') IS NOT NULL
    EXEC sp_rename 'DF_PO_approval_status', 'DF_purchase_order_approval_status';
IF OBJECT_ID('CK_PO_approval_status') IS NOT NULL
    EXEC sp_rename 'CK_PO_approval_status', 'CK_purchase_order_approval_status';

-- purchase_order_history
IF OBJECT_ID('CK_PO_history_event') IS NOT NULL
    EXEC sp_rename 'CK_PO_history_event', 'CK_purchase_order_history_event';
IF OBJECT_ID('DF_PO_history_by') IS NOT NULL
    EXEC sp_rename 'DF_PO_history_by', 'DF_purchase_order_history_by';
IF OBJECT_ID('DF_PO_history_at') IS NOT NULL
    EXEC sp_rename 'DF_PO_history_at', 'DF_purchase_order_history_at';
IF OBJECT_ID('FK_PO_history_PO') IS NOT NULL
    EXEC sp_rename 'FK_PO_history_PO', 'FK_purchase_order_history_po';
IF OBJECT_ID('dbo.purchase_order_history.IX_PO_history_po', 'I') IS NOT NULL
    EXEC sp_rename 'dbo.purchase_order_history.IX_PO_history_po', 'IX_purchase_order_history_po', 'INDEX';

-- ============================================================
-- STEP 3: RE-EMIT TRIGGER BODY WITH NEW TABLE NAME
-- sp_rename keeps the trigger attached but does NOT rewrite its body.
-- PN columns (PNID etc.) are renamed in commit 6, not here.
-- ============================================================

CREATE OR ALTER TRIGGER dbo.trg_PO_company_count
ON dbo.purchase_order
AFTER INSERT, UPDATE, DELETE
AS
BEGIN
    SET NOCOUNT ON;
    WITH affected (id) AS (
        SELECT supplier_id FROM inserted WHERE supplier_id IS NOT NULL
        UNION
        SELECT supplier_id FROM deleted  WHERE supplier_id IS NOT NULL
    )
    UPDATE s
    SET    s.SUNumOfPOs = (SELECT COUNT(*) FROM dbo.purchase_order p WHERE p.supplier_id = s.id)
    FROM   dbo.company s
    JOIN   affected a ON a.id = s.id;
END;
GO

-- ============================================================
-- STEP 4: UPDATE named_queries STORED SQL STRINGS
-- pos_for_pn references PO directly. POL is renamed in commit 5, so only the
-- PO token changes here. Idempotent: safe to re-run.
-- ============================================================

UPDATE dbo.named_queries
SET    sql        = 'SELECT purchase_order.number FROM POL LEFT JOIN purchase_order ON POL.POLPOID = purchase_order.id WHERE is_active = 1 AND POL.POLPNPartNumber LIKE @pn + ''%'' ORDER BY POL.POLPOID DESC',
       updated_at = GETDATE()
WHERE  name = 'pos_for_pn';
