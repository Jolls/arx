-- Drop prod _Test tables and the _Test sequence now that ArxDev is the test database.
-- Run ONLY after ArxDev is verified working with TEST_MODE=true.
-- Destructive — _Test tables are disposable snapshots, but confirm first.

IF OBJECT_ID('dbo.CN_Test',                      'U')  IS NOT NULL DROP TABLE dbo.CN_Test;
IF OBJECT_ID('dbo.FIL_Test',                     'U')  IS NOT NULL DROP TABLE dbo.FIL_Test;
IF OBJECT_ID('dbo.supplier_part_Test',           'U')  IS NOT NULL DROP TABLE dbo.supplier_part_Test;
IF OBJECT_ID('dbo.mfg_part_Test',                'U')  IS NOT NULL DROP TABLE dbo.mfg_part_Test;
IF OBJECT_ID('dbo.PL_Test',                      'U')  IS NOT NULL DROP TABLE dbo.PL_Test;
IF OBJECT_ID('dbo.price_Test',                   'U')  IS NOT NULL DROP TABLE dbo.price_Test;
IF OBJECT_ID('dbo.POL_Test',                     'U')  IS NOT NULL DROP TABLE dbo.POL_Test;
IF OBJECT_ID('dbo.PO_Test',                      'U')  IS NOT NULL DROP TABLE dbo.PO_Test;
IF OBJECT_ID('dbo.company_attachment_Test',      'U')  IS NOT NULL DROP TABLE dbo.company_attachment_Test;
IF OBJECT_ID('dbo.company_Test',                 'U')  IS NOT NULL DROP TABLE dbo.company_Test;
IF OBJECT_ID('dbo.PN_Test',                      'U')  IS NOT NULL DROP TABLE dbo.PN_Test;
IF OBJECT_ID('dbo.Forms_Test',                   'U')  IS NOT NULL DROP TABLE dbo.Forms_Test;
IF OBJECT_ID('dbo.form_events_Test',             'U')  IS NOT NULL DROP TABLE dbo.form_events_Test;
IF OBJECT_ID('dbo.record_events_Test',           'U')  IS NOT NULL DROP TABLE dbo.record_events_Test;
IF OBJECT_ID('dbo.TestRecords_Test',             'U')  IS NOT NULL DROP TABLE dbo.TestRecords_Test;
IF OBJECT_ID('dbo.TestResults_Test',             'U')  IS NOT NULL DROP TABLE dbo.TestResults_Test;
IF OBJECT_ID('dbo.test_definition_Test',         'U')  IS NOT NULL DROP TABLE dbo.test_definition_Test;
IF OBJECT_ID('dbo.test_definition_history_Test', 'U')  IS NOT NULL DROP TABLE dbo.test_definition_history_Test;
IF OBJECT_ID('dbo.app_config_Test',              'U')  IS NOT NULL DROP TABLE dbo.app_config_Test;
IF OBJECT_ID('dbo.unit_Test',                    'U')  IS NOT NULL DROP TABLE dbo.unit_Test;
IF OBJECT_ID('dbo.release_notes_Test',           'U')  IS NOT NULL DROP TABLE dbo.release_notes_Test;
IF OBJECT_ID('dbo.logs_Test',                    'U')  IS NOT NULL DROP TABLE dbo.logs_Test;
IF OBJECT_ID('dbo.PO_Number_Seq_Test',           'SO') IS NOT NULL DROP SEQUENCE dbo.PO_Number_Seq_Test;

PRINT 'Prod _Test objects dropped — ' + CONVERT(VARCHAR, GETDATE(), 120);
