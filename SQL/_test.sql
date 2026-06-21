-- Populate/refresh ArxDev from ArxProd.
-- ArxDev is a separate database on the same server used when TEST_MODE=true.
-- Table names are identical to prod — only the DSN database changes.
-- Re-run whenever a new table is added to prod.
--
-- ============================================================
-- AZURE SQL DATABASE (PaaS)
-- ============================================================
-- Cross-database three-part names are not supported. Use the Azure portal instead:
--
--   1. Delete ArxDev if it exists (portal → ArxDev → Delete)
--   2. Open ArxProd → Overview → Copy
--   3. Name the copy "ArxDev", same server, any tier
--   4. After the copy completes, run the "Sequence restart" block below
--      against ArxDev to advance PO_Number_Seq past the snapshot max.
--
-- ============================================================
-- ON-PREMISES SQL SERVER
-- ============================================================
-- Run the full script below connected to the SQL Server instance
-- (not to ArxDev directly — USE ArxDev handles that).

-- ── Sequence restart (Azure: run this after portal Copy; on-prem: included below) ──────────────
--
--   DECLARE @next_po INT = (SELECT ISNULL(MAX(TRY_CAST(number AS INT)), 99999) + 1 FROM dbo.purchase_order);
--   IF EXISTS (SELECT 1 FROM sys.sequences WHERE name = 'PO_Number_Seq')
--       EXEC('ALTER SEQUENCE dbo.PO_Number_Seq RESTART WITH ' + @next_po);
--   ELSE
--       EXEC('CREATE SEQUENCE dbo.PO_Number_Seq AS INT START WITH ' + @next_po + ' INCREMENT BY 1 NO CYCLE NO CACHE');

-- ══════════════════════════════════════════════════════════════
-- ON-PREMISES FULL POPULATE SCRIPT
-- ══════════════════════════════════════════════════════════════

USE ArxDev;

BEGIN TRANSACTION;
BEGIN TRY

    -- Drop existing tables (reverse FK order)
    IF OBJECT_ID('dbo.contact',                  'U') IS NOT NULL DROP TABLE dbo.contact;
    IF OBJECT_ID('dbo.part_attachment',           'U') IS NOT NULL DROP TABLE dbo.part_attachment;
    IF OBJECT_ID('dbo.supplier_part',            'U') IS NOT NULL DROP TABLE dbo.supplier_part;
    IF OBJECT_ID('dbo.mfg_part',                 'U') IS NOT NULL DROP TABLE dbo.mfg_part;
    IF OBJECT_ID('dbo.bom',                      'U') IS NOT NULL DROP TABLE dbo.bom;
    IF OBJECT_ID('dbo.price',                    'U') IS NOT NULL DROP TABLE dbo.price;
    IF OBJECT_ID('dbo.inventory_transaction',    'U') IS NOT NULL DROP TABLE dbo.inventory_transaction;
    IF OBJECT_ID('dbo.po_line',                  'U') IS NOT NULL DROP TABLE dbo.po_line;
    IF OBJECT_ID('dbo.purchase_order_history',   'U') IS NOT NULL DROP TABLE dbo.purchase_order_history;
    IF OBJECT_ID('dbo.purchase_order',           'U') IS NOT NULL DROP TABLE dbo.purchase_order;
    IF OBJECT_ID('dbo.company_attachment',       'U') IS NOT NULL DROP TABLE dbo.company_attachment;
    IF OBJECT_ID('dbo.company',                  'U') IS NOT NULL DROP TABLE dbo.company;
    IF OBJECT_ID('dbo.part',                     'U') IS NOT NULL DROP TABLE dbo.part;
    IF OBJECT_ID('dbo.form',                     'U') IS NOT NULL DROP TABLE dbo.form;
    IF OBJECT_ID('dbo.form_events',              'U') IS NOT NULL DROP TABLE dbo.form_events;
    IF OBJECT_ID('dbo.record_events',            'U') IS NOT NULL DROP TABLE dbo.record_events;
    IF OBJECT_ID('dbo.test_record',              'U') IS NOT NULL DROP TABLE dbo.test_record;
    IF OBJECT_ID('dbo.test_result',              'U') IS NOT NULL DROP TABLE dbo.test_result;
    IF OBJECT_ID('dbo.test_definition',          'U') IS NOT NULL DROP TABLE dbo.test_definition;
    IF OBJECT_ID('dbo.test_definition_history',  'U') IS NOT NULL DROP TABLE dbo.test_definition_history;
    IF OBJECT_ID('dbo.named_queries',            'U') IS NOT NULL DROP TABLE dbo.named_queries;
    IF OBJECT_ID('dbo.app_config',               'U') IS NOT NULL DROP TABLE dbo.app_config;
    IF OBJECT_ID('dbo.unit',                     'U') IS NOT NULL DROP TABLE dbo.unit;
    IF OBJECT_ID('dbo.release_notes',            'U') IS NOT NULL DROP TABLE dbo.release_notes;
    IF OBJECT_ID('dbo.logs',                     'U') IS NOT NULL DROP TABLE dbo.logs;
    IF OBJECT_ID('dbo.users',                    'U') IS NOT NULL DROP TABLE dbo.users;

    -- Populate from prod via three-part names
    SELECT * INTO dbo.contact                  FROM ArxProd.dbo.contact;
    SELECT * INTO dbo.part_attachment          FROM ArxProd.dbo.part_attachment;
    SELECT * INTO dbo.supplier_part           FROM ArxProd.dbo.supplier_part;
    SELECT * INTO dbo.mfg_part                FROM ArxProd.dbo.mfg_part;
    SELECT * INTO dbo.bom                     FROM ArxProd.dbo.bom;
    SELECT * INTO dbo.price                   FROM ArxProd.dbo.price;
    SELECT * INTO dbo.po_line                 FROM ArxProd.dbo.po_line;
    SELECT * INTO dbo.purchase_order          FROM ArxProd.dbo.purchase_order;
    SELECT * INTO dbo.purchase_order_history  FROM ArxProd.dbo.purchase_order_history;
    SELECT * INTO dbo.company_attachment      FROM ArxProd.dbo.company_attachment;
    SELECT * INTO dbo.company                 FROM ArxProd.dbo.company;
    SELECT * INTO dbo.part                    FROM ArxProd.dbo.part;
    SELECT * INTO dbo.inventory_transaction   FROM ArxProd.dbo.inventory_transaction;
    SELECT * INTO dbo.form                    FROM ArxProd.dbo.form;
    SELECT * INTO dbo.form_events             FROM ArxProd.dbo.form_events;
    SELECT * INTO dbo.record_events           FROM ArxProd.dbo.record_events;
    SELECT * INTO dbo.test_record             FROM ArxProd.dbo.test_record;
    SELECT * INTO dbo.test_result             FROM ArxProd.dbo.test_result;
    SELECT * INTO dbo.test_definition         FROM ArxProd.dbo.test_definition;
    SELECT * INTO dbo.test_definition_history FROM ArxProd.dbo.test_definition_history;
    SELECT * INTO dbo.named_queries           FROM ArxProd.dbo.named_queries;
    SELECT * INTO dbo.app_config              FROM ArxProd.dbo.app_config;
    SELECT * INTO dbo.unit                    FROM ArxProd.dbo.unit;
    SELECT * INTO dbo.release_notes           FROM ArxProd.dbo.release_notes;
    SELECT * INTO dbo.logs                    FROM ArxProd.dbo.logs;
    SELECT * INTO dbo.users                   FROM ArxProd.dbo.users;

    -- Constraints not copied by SELECT * INTO
    ALTER TABLE dbo.purchase_order ADD CONSTRAINT UQ_purchase_order_number UNIQUE (number);
    ALTER TABLE dbo.app_config ADD CONSTRAINT DF_app_config_updated_at DEFAULT GETDATE() FOR updated_at;
    ALTER TABLE dbo.part_attachment ADD CONSTRAINT DF_part_attachment_is_active DEFAULT 1 FOR is_active;
    CREATE UNIQUE INDEX UQ_price_active_combo ON dbo.price (part_id, supplier_id, pack_size) WHERE is_active = 1;
    ALTER TABLE dbo.part ADD CONSTRAINT CK_part_number_category   CHECK (category IN ('', 'ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'RAW', 'SVC', 'TOOL'));

    -- Triggers not copied by SELECT * INTO
    EXEC('
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
    JOIN   affected a ON a.id = s.id
END
    ');
    EXEC('
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
    JOIN   affected a ON a.id = s.id
END
    ');
    EXEC('
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
    JOIN   affected a ON a.id = p.id
END
    ');
    EXEC('
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
    JOIN   affected a ON a.id = p.id
END
    ');

    -- Recalibrate snapshot counts
    UPDATE s
    SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part sp WHERE sp.supplier_id = s.id),
           s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.purchase_order p  WHERE p.supplier_id  = s.id)
    FROM   dbo.company s;

    UPDATE p
    SET    p.attachment_count = (SELECT COUNT(*) FROM dbo.part_attachment f WHERE f.part_id = p.id AND f.is_active = 1),
           p.po_line_count   = (SELECT COUNT(*) FROM dbo.po_line pol WHERE pol.part_id = p.id)
    FROM   dbo.part p;

    -- PO sequence: starts after current max so dev POs don't collide with snapshot data
    DECLARE @next_po INT = (SELECT ISNULL(MAX(TRY_CAST(number AS INT)), 99999) + 1 FROM dbo.purchase_order);
    IF EXISTS (SELECT 1 FROM sys.sequences WHERE name = 'PO_Number_Seq')
        EXEC('ALTER SEQUENCE dbo.PO_Number_Seq RESTART WITH ' + @next_po);
    ELSE
        EXEC('CREATE SEQUENCE dbo.PO_Number_Seq AS INT START WITH ' + @next_po + ' INCREMENT BY 1 NO CYCLE NO CACHE');

    COMMIT;
    PRINT 'ArxDev populated from ArxProd — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Populate failed: ' + ERROR_MESSAGE();
END CATCH;
