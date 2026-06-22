-- migrate_rename_part.sql
-- Commit 8 of db-table-rename effort: rename the hub table part_number → part.
-- This is a TABLE-NAME-ONLY rename — all columns were already modernized in commit 6
-- (see migrate_rename_part_number.sql). The human-readable part_number COLUMN keeps its name.
-- See docs/plans/db-table-rename-plan.md and the commit-8 decision note.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
-- sp_rename preserves all indexes, constraints, and FKs automatically — the inbound FKs from
-- bom, part_attachment, po_line, supplier_part, mfg_part, price, and inventory_transaction
-- stay attached and keep pointing at the renamed table.
--
-- No data is changed.
--
-- Constraint names keep their legacy *_part_number_* form (UQ_part_number_part_number,
-- CK_part_number_category, FK_part_number_unit, DF_part_number_*). Constraint names bind to the
-- object, not the table name, so this is harmless cosmetic drift (same decision as commit 6).
--
-- IMPORTANT: both denormalized-count triggers (trg_FIL_part_count, trg_POL_part_count) target
-- the hub table in their bodies, so they are re-emitted below. sp_rename keeps them attached but
-- does NOT rewrite their bodies.

-- ============================================================
-- STEP 1: RENAME TABLE
-- ============================================================

IF OBJECT_ID('dbo.part_number', 'U') IS NOT NULL AND OBJECT_ID('dbo.part', 'U') IS NULL
    EXEC sp_rename 'dbo.part_number', 'part';

-- ============================================================
-- STEP 2: RE-EMIT TRIGGER BODIES TARGETING dbo.part
-- ============================================================
GO
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
    FROM   dbo.part p
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
    FROM   dbo.part p
    JOIN   affected a ON a.id = p.id;
END;
GO

-- ============================================================
-- STEP 3: UPDATE named_queries STORED SQL STRINGS
-- These embed the hub table name directly (the part_number COLUMN is unchanged). Idempotent.
-- ============================================================

UPDATE dbo.named_queries
SET    sql        = 'SELECT category FROM part_attachment WHERE part_id = (SELECT id FROM part WHERE part_number = @pn) AND is_active = 1',
       updated_at = GETDATE()
WHERE  name = 'fil_category_for_pn';

UPDATE dbo.named_queries
SET    sql        = 'SELECT part_number FROM part WHERE part_number LIKE @pattern AND is_active = 1 ORDER BY part_number DESC',
       updated_at = GETDATE()
WHERE  name = 'parts_matching';

UPDATE dbo.named_queries
SET    sql        = 'SELECT pn.part_number, pn.title FROM bom JOIN part pn ON bom.component_part_id = pn.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item',
       updated_at = GETDATE()
WHERE  name = 'bom_pn_by_item';

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part pn ON f.part_id = pn.id WHERE pn.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN pn.primary_attachment_id > 0 AND f.id = pn.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at = GETDATE()
WHERE  name = 'pn_primary_attachment';

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part pn ON f.part_id = pn.id WHERE pn.id = @pnid AND f.is_active = 1 ORDER BY CASE WHEN pn.primary_attachment_id > 0 AND f.id = pn.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at = GETDATE()
WHERE  name = 'form_primary_attachment';
