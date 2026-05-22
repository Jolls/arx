-- Triggers that keep denormalized supplier counts current.
-- SUNumOfLNKs: total LNK rows where LNKSUID = supplier.id.
-- SUNumOfPOs:  total PO rows where supplier_id = supplier.id.
--
-- Run once to install. Safe to re-run (CREATE OR ALTER).
-- After installing, run the recalibration block at the bottom once
-- to fix any counts that drifted before triggers existed.
--
-- _Test table equivalents are created by _test.sql.

-- LNK → supplier.SUNumOfLNKs
CREATE OR ALTER TRIGGER dbo.trg_LNK_supplier_count
ON dbo.LNK
AFTER INSERT, UPDATE, DELETE
AS
BEGIN
    SET NOCOUNT ON;
    WITH affected (id) AS (
        SELECT LNKSUID FROM inserted WHERE LNKSUID IS NOT NULL
        UNION
        SELECT LNKSUID FROM deleted  WHERE LNKSUID IS NOT NULL
    )
    UPDATE s
    SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.LNK l WHERE l.LNKSUID = s.id)
    FROM   dbo.supplier s
    JOIN   affected a ON a.id = s.id;
END;
GO

-- PO → supplier.SUNumOfPOs
CREATE OR ALTER TRIGGER dbo.trg_PO_supplier_count
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
    FROM   dbo.supplier s
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
SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.LNK l WHERE l.LNKSUID    = s.id),
       s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.PO  p WHERE p.supplier_id = s.id)
FROM   dbo.supplier s;

UPDATE p
SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.FIL f WHERE f.FILPNID = p.PNID AND f.is_active = 1),
       p.PNPOLinks  = (SELECT COUNT(*) FROM dbo.POL pol WHERE pol.POLPNID = p.PNID)
FROM   dbo.PN p;
GO
