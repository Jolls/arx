-- migrate_supplier_to_company.sql
-- Renames supplier → company and supplier_attachment → company_attachment.
-- FK column names (supplier_id, mfg_id) are unchanged — they describe the role, not the table.
--
-- Also fixes deferred items from the LNK → supplier_part migration:
--   - trigger trg_LNK_supplier_count referenced dbo.LNK and LNKSUID (now broken)
--   - trigger trg_LNK_Test_supplier_count same issue
--   Both are dropped and recreated with correct names and column references.
--
-- Run order:
--   1. This file (both parts)
--   2. Refresh _Test tables via _test.sql if desired
--
-- Run against the PartsMaster database.

-- ============================================================
-- PART 1: TEST TABLES
-- No FK constraints on _Test tables — just rename and reinstall triggers.
-- ============================================================

IF OBJECT_ID('dbo.supplier_Test', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.supplier_Test', 'company_Test';

IF OBJECT_ID('dbo.supplier_attachment_Test', 'U') IS NOT NULL
    EXEC sp_rename 'dbo.supplier_attachment_Test', 'company_attachment_Test';

PRINT 'Test tables renamed.';

-- Drop old _Test trigger (body references dbo.LNK_Test / LNKSUID / dbo.supplier_Test — all stale)
IF OBJECT_ID('dbo.trg_LNK_Test_supplier_count', 'TR') IS NOT NULL
    DROP TRIGGER dbo.trg_LNK_Test_supplier_count;

-- Recreate with correct name and references
EXEC('
CREATE TRIGGER dbo.trg_supplier_part_Test_company_count
ON dbo.supplier_part_Test
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
    SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part_Test sp WHERE sp.supplier_id = s.id)
    FROM   dbo.company_Test s
    JOIN   affected a ON a.id = s.id
END
');

-- PO_Test trigger: update dbo.supplier_Test → dbo.company_Test reference
IF OBJECT_ID('dbo.trg_PO_Test_supplier_count', 'TR') IS NOT NULL
    DROP TRIGGER dbo.trg_PO_Test_supplier_count;

EXEC('
CREATE TRIGGER dbo.trg_PO_Test_company_count
ON dbo.PO_Test
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
    SET    s.SUNumOfPOs = (SELECT COUNT(*) FROM dbo.PO_Test p WHERE p.supplier_id = s.id)
    FROM   dbo.company_Test s
    JOIN   affected a ON a.id = s.id
END
');

PRINT 'Test triggers reinstalled.';

-- ============================================================
-- PART 2: PROD TABLES
-- ============================================================

BEGIN TRANSACTION;
BEGIN TRY

-- Rename tables.
-- sp_rename preserves FK references by object_id — no need to drop/recreate for the rename itself.
-- We rename FK constraint names afterward (cosmetic, but keeps schema readable).
EXEC sp_rename 'dbo.supplier',            'company';
EXEC sp_rename 'dbo.supplier_attachment', 'company_attachment';

-- Rename FK constraints that say "supplier" when meaning the company entity.
-- sp_rename 'OBJECT' renames the constraint without touching the underlying enforcement.
EXEC sp_rename N'dbo.FK_supplier_part_supplier',      N'FK_supplier_part_company',      N'OBJECT';
EXEC sp_rename N'dbo.FK_price_supplier',              N'FK_price_company',              N'OBJECT';
EXEC sp_rename N'dbo.FK_PO_supplier',                 N'FK_PO_company',                 N'OBJECT';
EXEC sp_rename N'dbo.FK_supplier_default_contact',    N'FK_company_default_contact',    N'OBJECT';
EXEC sp_rename N'dbo.FK_supplier_primary_attachment', N'FK_company_primary_attachment', N'OBJECT';
-- FK_mfg_part_mfg  — unchanged (already describes the mfg role, not the table name)
-- FK_PO_receiver   — unchanged (describes the receiver role)

COMMIT;
PRINT 'Prod tables and FK constraints renamed — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Prod rename failed: ' + ERROR_MESSAGE();
END CATCH;

-- ============================================================
-- REINSTALL PROD TRIGGERS (outside transaction — each EXEC is its own batch)
-- Fixes: stale dbo.LNK / LNKSUID / dbo.supplier references in trigger bodies.
-- ============================================================

-- Drop old trigger (body references dbo.LNK / LNKSUID / dbo.supplier — all stale)
IF OBJECT_ID('dbo.trg_LNK_supplier_count', 'TR') IS NOT NULL
    DROP TRIGGER dbo.trg_LNK_supplier_count;

EXEC('
CREATE TRIGGER dbo.trg_supplier_part_company_count
ON dbo.supplier_part
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
    SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part sp WHERE sp.supplier_id = s.id)
    FROM   dbo.company s
    JOIN   affected a ON a.id = s.id
END
');

-- PO trigger: update dbo.supplier → dbo.company reference
IF OBJECT_ID('dbo.trg_PO_supplier_count', 'TR') IS NOT NULL
    DROP TRIGGER dbo.trg_PO_supplier_count;

EXEC('
CREATE TRIGGER dbo.trg_PO_company_count
ON dbo.PO
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
    SET    s.SUNumOfPOs = (SELECT COUNT(*) FROM dbo.PO p WHERE p.supplier_id = s.id)
    FROM   dbo.company s
    JOIN   affected a ON a.id = s.id
END
');

PRINT 'Prod triggers reinstalled.';

-- One-time recalibration (safe to re-run)
UPDATE s
SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part sp WHERE sp.supplier_id = s.id),
       s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.PO             p  WHERE p.supplier_id  = s.id)
FROM   dbo.company s;

PRINT 'Counts recalibrated.';
