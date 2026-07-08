-- Triggers that maintain denormalized counts on company and part, plus the
-- test_definition audit-history trigger.
--   company.SUNumOfLNKs            — active supplier_part rows for a supplier
--   company.SUNumOfPOs             — purchase_order rows for a supplier
--   part.attachment_count          — active part_attachment rows for a part
--   part.po_line_count             — po_line line-item rows for a part
--   test_definition_history        — snapshot of test_definition rows on UPDATE
--
-- The count triggers recompute a full COUNT(*) from live data (not increment/decrement),
-- so any drift is self-correcting on the next write to an affected row.
-- Those columns are display-only; they are never used in WHERE clauses or business logic.
--
-- Run once to install. Safe to re-run (CREATE OR ALTER).
-- The recalibration block at the bottom corrects any counts that drifted before
-- triggers were installed; safe to re-run at any time if drift is suspected.
--
-- These same triggers apply in ArxDev (identical schema, bare table names); the seed
-- script SQL/seed_test_data.sql assumes they already exist and lets them fire on its INSERTs.

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

-- purchase_order → company.SUNumOfPOs
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

-- part_attachment → part.attachment_count
-- Counts only is_active=1 rows (soft-deleted rows are excluded).
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

-- po_line → part.po_line_count
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

-- One-time recalibration: corrects any counts that drifted before triggers existed.
-- Safe to re-run.
UPDATE s
SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part sp WHERE sp.supplier_id = s.id),
       s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.purchase_order p  WHERE p.supplier_id  = s.id)
FROM   dbo.company s;

UPDATE p
SET    p.attachment_count = (SELECT COUNT(*) FROM dbo.part_attachment f WHERE f.part_id = p.id AND f.is_active = 1),
       p.po_line_count   = (SELECT COUNT(*) FROM dbo.po_line pol WHERE pol.part_id = p.id)
FROM   dbo.part p;
GO

-- test_definition → test_definition_history
-- Snapshot old values into test_definition_history on every test_definition UPDATE.
-- Uses DELETED pseudo-table which contains pre-update row values.
-- Set-based: handles bulk updates (multiple rows changed at once) correctly.
IF OBJECT_ID('dbo.trg_test_definition_history', 'TR') IS NOT NULL DROP TRIGGER trg_test_definition_history;
GO
CREATE TRIGGER dbo.trg_test_definition_history
ON dbo.test_definition
AFTER UPDATE
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.test_definition_history
      (test_id, changed_at, changed_by,
       type, parameter, specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_type,
       instrument_types, format, comment, category, sheet_name)
    SELECT
      id, GETDATE(),
      REPLACE(CONVERT(VARCHAR(128), CONTEXT_INFO()), CHAR(0), ''),
      type, parameter, specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name
    FROM DELETED;
END
GO
