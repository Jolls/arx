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
--   DECLARE @next_po INT = (SELECT ISNULL(MAX(TRY_CAST(number AS INT)), 99999) + 1 FROM dbo.PO);
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
    IF OBJECT_ID('dbo.CN',                      'U') IS NOT NULL DROP TABLE dbo.CN;
    IF OBJECT_ID('dbo.FIL',                      'U') IS NOT NULL DROP TABLE dbo.FIL;
    IF OBJECT_ID('dbo.supplier_part',            'U') IS NOT NULL DROP TABLE dbo.supplier_part;
    IF OBJECT_ID('dbo.mfg_part',                 'U') IS NOT NULL DROP TABLE dbo.mfg_part;
    IF OBJECT_ID('dbo.PL',                       'U') IS NOT NULL DROP TABLE dbo.PL;
    IF OBJECT_ID('dbo.price',                    'U') IS NOT NULL DROP TABLE dbo.price;
    IF OBJECT_ID('dbo.POL',                      'U') IS NOT NULL DROP TABLE dbo.POL;
    IF OBJECT_ID('dbo.PO',                       'U') IS NOT NULL DROP TABLE dbo.PO;
    IF OBJECT_ID('dbo.company_attachment',       'U') IS NOT NULL DROP TABLE dbo.company_attachment;
    IF OBJECT_ID('dbo.company',                  'U') IS NOT NULL DROP TABLE dbo.company;
    IF OBJECT_ID('dbo.PN',                       'U') IS NOT NULL DROP TABLE dbo.PN;
    IF OBJECT_ID('dbo.Forms',                    'U') IS NOT NULL DROP TABLE dbo.Forms;
    IF OBJECT_ID('dbo.form_events',              'U') IS NOT NULL DROP TABLE dbo.form_events;
    IF OBJECT_ID('dbo.record_events',            'U') IS NOT NULL DROP TABLE dbo.record_events;
    IF OBJECT_ID('dbo.TestRecords',              'U') IS NOT NULL DROP TABLE dbo.TestRecords;
    IF OBJECT_ID('dbo.TestResults',              'U') IS NOT NULL DROP TABLE dbo.TestResults;
    IF OBJECT_ID('dbo.test_definition',          'U') IS NOT NULL DROP TABLE dbo.test_definition;
    IF OBJECT_ID('dbo.test_definition_history',  'U') IS NOT NULL DROP TABLE dbo.test_definition_history;
    IF OBJECT_ID('dbo.named_queries',            'U') IS NOT NULL DROP TABLE dbo.named_queries;
    IF OBJECT_ID('dbo.app_config',               'U') IS NOT NULL DROP TABLE dbo.app_config;
    IF OBJECT_ID('dbo.unit',                     'U') IS NOT NULL DROP TABLE dbo.unit;
    IF OBJECT_ID('dbo.release_notes',            'U') IS NOT NULL DROP TABLE dbo.release_notes;
    IF OBJECT_ID('dbo.logs',                     'U') IS NOT NULL DROP TABLE dbo.logs;

    -- Populate from prod via three-part names
    SELECT * INTO dbo.CN                      FROM ArxProd.dbo.CN;
    SELECT * INTO dbo.FIL                     FROM ArxProd.dbo.FIL;
    SELECT * INTO dbo.supplier_part           FROM ArxProd.dbo.supplier_part;
    SELECT * INTO dbo.mfg_part                FROM ArxProd.dbo.mfg_part;
    SELECT * INTO dbo.PL                      FROM ArxProd.dbo.PL;
    SELECT * INTO dbo.price                   FROM ArxProd.dbo.price;
    SELECT * INTO dbo.POL                     FROM ArxProd.dbo.POL;
    SELECT * INTO dbo.PO                      FROM ArxProd.dbo.PO;
    SELECT * INTO dbo.company_attachment      FROM ArxProd.dbo.company_attachment;
    SELECT * INTO dbo.company                 FROM ArxProd.dbo.company;
    SELECT * INTO dbo.PN                      FROM ArxProd.dbo.PN;
    SELECT * INTO dbo.Forms                   FROM ArxProd.dbo.Forms;
    SELECT * INTO dbo.form_events             FROM ArxProd.dbo.form_events;
    SELECT * INTO dbo.record_events           FROM ArxProd.dbo.record_events;
    SELECT * INTO dbo.TestRecords             FROM ArxProd.dbo.TestRecords;
    SELECT * INTO dbo.TestResults             FROM ArxProd.dbo.TestResults;
    SELECT * INTO dbo.test_definition         FROM ArxProd.dbo.test_definition;
    SELECT * INTO dbo.test_definition_history FROM ArxProd.dbo.test_definition_history;
    SELECT * INTO dbo.named_queries           FROM ArxProd.dbo.named_queries;
    SELECT * INTO dbo.app_config              FROM ArxProd.dbo.app_config;
    SELECT * INTO dbo.unit                    FROM ArxProd.dbo.unit;
    SELECT * INTO dbo.release_notes           FROM ArxProd.dbo.release_notes;
    SELECT * INTO dbo.logs                    FROM ArxProd.dbo.logs;

    -- Constraints not copied by SELECT * INTO
    ALTER TABLE dbo.PO         ADD CONSTRAINT UQ_PO_number             UNIQUE (number);
    ALTER TABLE dbo.app_config ADD CONSTRAINT DF_app_config_updated_at DEFAULT GETDATE() FOR updated_at;
    ALTER TABLE dbo.FIL        ADD CONSTRAINT DF_FIL_is_active          DEFAULT 1 FOR is_active;
    CREATE UNIQUE INDEX UQ_price_active_combo ON dbo.price (part_id, supplier_id, pack_size) WHERE is_active = 1;
    ALTER TABLE dbo.PN         ADD CONSTRAINT CK_PN_category            CHECK (category IN ('ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'RAW', 'SVC', 'TOOL'));

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
    EXEC('
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
    JOIN   affected a ON a.id = p.PNID
END
    ');
    EXEC('
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
    JOIN   affected a ON a.id = p.PNID
END
    ');

    -- Recalibrate snapshot counts
    UPDATE s
    SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part sp WHERE sp.supplier_id = s.id),
           s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.PO             p  WHERE p.supplier_id  = s.id)
    FROM   dbo.company s;

    UPDATE p
    SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.FIL f WHERE f.FILPNID = p.PNID AND f.is_active = 1),
           p.PNPOLinks  = (SELECT COUNT(*) FROM dbo.POL pol WHERE pol.POLPNID = p.PNID)
    FROM   dbo.PN p;

    -- PO sequence: starts after current max so dev POs don't collide with snapshot data
    DECLARE @next_po INT = (SELECT ISNULL(MAX(TRY_CAST(number AS INT)), 99999) + 1 FROM dbo.PO);
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
