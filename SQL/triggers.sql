-- Triggers that maintain denormalized counts on company and PN.
--   company.SUNumOfLNKs — active supplier_part rows for a supplier
--   company.SUNumOfPOs  — PO rows for a supplier
--   PN.PNFILLinks       — active FIL attachment rows for a part
--   PN.PNPOLinks        — POL line-item rows for a part
--
-- Each trigger recomputes a full COUNT(*) from live data (not increment/decrement),
-- so any drift is self-correcting on the next write to an affected row.
-- These columns are display-only; they are never used in WHERE clauses or business logic.
--
-- Run once to install. Safe to re-run (CREATE OR ALTER).
-- The recalibration block at the bottom corrects any counts that drifted before
-- triggers were installed; safe to re-run at any time if drift is suspected.
--
-- _Test table equivalents are created by _test.sql.

-- supplier_part → company.SUNumOfLNKs
CREATE OR ALTER TRIGGER dbo.trg_supplier_part_company_count
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
    JOIN   affected a ON a.id = s.id;
END;
GO

-- PO → company.SUNumOfPOs
CREATE OR ALTER TRIGGER dbo.trg_PO_company_count
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
    JOIN   affected a ON a.id = s.id;
END;
GO

-- FIL → PN.PNFILLinks
-- Counts only is_active=1 rows (soft-deleted rows are excluded).
CREATE OR ALTER TRIGGER dbo.trg_FIL_part_count
ON dbo.FIL
AFTER INSERT, UPDATE, DELETE
AS
BEGIN
    SET NOCOUNT ON;
    WITH affected (id) AS (
        SELECT FILPNID FROM inserted WHERE FILPNID IS NOT NULL
        UNION
        SELECT FILPNID FROM deleted  WHERE FILPNID IS NOT NULL
    )
    UPDATE p
    SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.FIL f WHERE f.FILPNID = p.PNID AND f.is_active = 1)
    FROM   dbo.PN p
    JOIN   affected a ON a.id = p.PNID;
END;
GO

-- POL → PN.PNPOLinks
CREATE OR ALTER TRIGGER dbo.trg_POL_part_count
ON dbo.POL
AFTER INSERT, UPDATE, DELETE
AS
BEGIN
    SET NOCOUNT ON;
    WITH affected (id) AS (
        SELECT POLPNID FROM inserted WHERE POLPNID IS NOT NULL
        UNION
        SELECT POLPNID FROM deleted  WHERE POLPNID IS NOT NULL
    )
    UPDATE p
    SET    p.PNPOLinks = (SELECT COUNT(*) FROM dbo.POL pol WHERE pol.POLPNID = p.PNID)
    FROM   dbo.PN p
    JOIN   affected a ON a.id = p.PNID;
END;
GO

-- One-time recalibration: corrects any counts that drifted before triggers existed.
-- Safe to re-run.
UPDATE s
SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part sp WHERE sp.supplier_id = s.id),
       s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.PO             p  WHERE p.supplier_id  = s.id)
FROM   dbo.company s;

UPDATE p
SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.FIL f WHERE f.FILPNID = p.PNID AND f.is_active = 1),
       p.PNPOLinks  = (SELECT COUNT(*) FROM dbo.POL pol WHERE pol.POLPNID = p.PNID)
FROM   dbo.PN p;
GO
