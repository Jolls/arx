-- Snapshot script: copies all live tables into _Test shadow copies.
-- Run before any risky schema or data changes.
-- NOTE: _Test tables are data-only snapshots — no constraints or indexes are copied.
--       Do not treat these as a reliable restore point for production data.

BEGIN TRANSACTION;
BEGIN TRY

    -- Drop existing _Test tables
    IF OBJECT_ID('dbo.CN_Test',                  'U') IS NOT NULL DROP TABLE CN_Test;
    IF OBJECT_ID('dbo.FIL_Test',                 'U') IS NOT NULL DROP TABLE FIL_Test;
    IF OBJECT_ID('dbo.supplier_part_Test',        'U') IS NOT NULL DROP TABLE supplier_part_Test;
    IF OBJECT_ID('dbo.mfg_part_Test',             'U') IS NOT NULL DROP TABLE mfg_part_Test;
    IF OBJECT_ID('dbo.PL_Test',                  'U') IS NOT NULL DROP TABLE PL_Test;
    IF OBJECT_ID('dbo.PN_Test',                  'U') IS NOT NULL DROP TABLE PN_Test;
    IF OBJECT_ID('dbo.PO_Test',                  'U') IS NOT NULL DROP TABLE PO_Test;
    IF OBJECT_ID('dbo.POL_Test',                 'U') IS NOT NULL DROP TABLE POL_Test;
    IF OBJECT_ID('dbo.company_Test',             'U') IS NOT NULL DROP TABLE company_Test;
    IF OBJECT_ID('dbo.company_attachment_Test',  'U') IS NOT NULL DROP TABLE company_attachment_Test;
    IF OBJECT_ID('dbo.Forms_Test',               'U') IS NOT NULL DROP TABLE Forms_Test;
    IF OBJECT_ID('dbo.TestRecordHistory_Test',   'U') IS NOT NULL DROP TABLE TestRecordHistory_Test;
    IF OBJECT_ID('dbo.TestRecords_Test',         'U') IS NOT NULL DROP TABLE TestRecords_Test;
    IF OBJECT_ID('dbo.TestResults_Test',         'U') IS NOT NULL DROP TABLE TestResults_Test;
    IF OBJECT_ID('dbo.test_definition_Test',      'U') IS NOT NULL DROP TABLE test_definition_Test;
    IF OBJECT_ID('dbo.test_definition_history_Test', 'U') IS NOT NULL DROP TABLE test_definition_history_Test;
    -- named_queries intentionally excluded — no _Test variant (shared config, read-only lookups)
    IF OBJECT_ID('dbo.app_config_Test',          'U') IS NOT NULL DROP TABLE app_config_Test;
    IF OBJECT_ID('dbo.price_Test',               'U') IS NOT NULL DROP TABLE price_Test;
    IF OBJECT_ID('dbo.release_notes_Test',       'U') IS NOT NULL DROP TABLE release_notes_Test;
    IF OBJECT_ID('dbo.logs_Test',                'U') IS NOT NULL DROP TABLE logs_Test;

    -- Recreate from live
    SELECT * INTO CN_Test                FROM CN;
    SELECT * INTO FIL_Test               FROM FIL;
    SELECT * INTO supplier_part_Test     FROM supplier_part;
    SELECT * INTO mfg_part_Test          FROM mfg_part;
    SELECT * INTO PL_Test                FROM PL;
    SELECT * INTO PN_Test                FROM PN;
    SELECT * INTO PO_Test                FROM PO;
    SELECT * INTO POL_Test               FROM POL;
    SELECT * INTO company_Test               FROM company;
    SELECT * INTO company_attachment_Test    FROM company_attachment;
    SELECT * INTO Forms_Test             FROM Forms;
    SELECT * INTO TestRecordHistory_Test FROM TestRecordHistory;
    SELECT * INTO TestRecords_Test       FROM TestRecords;
    SELECT * INTO TestResults_Test       FROM TestResults;
    SELECT * INTO test_definition_Test        FROM test_definition;
    SELECT * INTO test_definition_history_Test FROM test_definition_history;
    SELECT * INTO app_config_Test        FROM app_config;
    SELECT * INTO price_Test             FROM price;
    SELECT * INTO release_notes_Test     FROM release_notes;
    SELECT * INTO logs_Test              FROM logs;

    -- Constraints not copied by SELECT * INTO — add manually
    ALTER TABLE dbo.PO_Test    ADD CONSTRAINT UQ_PO_Test_number                UNIQUE (number);
    -- No UNIQUE on PL_Test (PLListID, PLItem) — item numbers are user-assigned and not enforced unique.
    CREATE UNIQUE INDEX UQ_price_Test_active_combo ON dbo.price_Test (part_id, supplier_id, pack_size) WHERE is_active = 1;
    ALTER TABLE dbo.PN_Test    ADD CONSTRAINT CK_PN_Test_category              CHECK (category IN ('ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'RAW', 'SVC', 'TOOL'));

    -- Triggers not copied by SELECT * INTO — recreate on _Test tables.
    -- EXEC isolates each CREATE OR ALTER TRIGGER in its own batch (required by SQL Server).
    EXEC('
CREATE OR ALTER TRIGGER dbo.trg_supplier_part_Test_company_count
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
    EXEC('
CREATE OR ALTER TRIGGER dbo.trg_PO_Test_company_count
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
    EXEC('
CREATE OR ALTER TRIGGER dbo.trg_FIL_Test_part_count
ON dbo.FIL_Test
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
    SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.FIL_Test f WHERE f.FILPNID = p.PNID AND f.is_active = 1)
    FROM   dbo.PN_Test p
    JOIN   affected a ON a.id = p.PNID
END
    ');
    EXEC('
CREATE OR ALTER TRIGGER dbo.trg_POL_Test_part_count
ON dbo.POL_Test
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
    SET    p.PNPOLinks = (SELECT COUNT(*) FROM dbo.POL_Test pol WHERE pol.POLPNID = p.PNID)
    FROM   dbo.PN_Test p
    JOIN   affected a ON a.id = p.PNID
END
    ');

    -- Recalibrate copied snapshot counts (prod data may have drifted before triggers existed).
    UPDATE s
    SET    s.SUNumOfLNKs = (SELECT COUNT(*) FROM dbo.supplier_part_Test sp WHERE sp.supplier_id = s.id),
           s.SUNumOfPOs  = (SELECT COUNT(*) FROM dbo.PO_Test             p  WHERE p.supplier_id  = s.id)
    FROM   dbo.company_Test s;

    UPDATE p
    SET    p.PNFILLinks = (SELECT COUNT(*) FROM dbo.FIL_Test f WHERE f.FILPNID = p.PNID AND f.is_active = 1),
           p.PNPOLinks  = (SELECT COUNT(*) FROM dbo.POL_Test pol WHERE pol.POLPNID = p.PNID)
    FROM   dbo.PN_Test p;

    -- Test sequence: starts after the current max so test POs don't collide with snapshot data
    DECLARE @next_po INT = (SELECT ISNULL(MAX(TRY_CAST(number AS INT)), 0) + 1 FROM dbo.PO_Test);
    IF EXISTS (SELECT 1 FROM sys.sequences WHERE name = 'PO_Number_Seq_Test')
        EXEC('ALTER SEQUENCE dbo.PO_Number_Seq_Test RESTART WITH ' + @next_po);
    ELSE
        EXEC('CREATE SEQUENCE dbo.PO_Number_Seq_Test AS INT START WITH ' + @next_po + ' INCREMENT BY 1 NO CYCLE NO CACHE');

    COMMIT;
    PRINT 'Snapshot complete — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Snapshot failed: ' + ERROR_MESSAGE();
END CATCH;
