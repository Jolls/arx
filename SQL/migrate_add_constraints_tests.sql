-- migrate_add_constraints_tests.sql
-- Adds deferred NOT NULL and FK constraints to test-records tables.
-- Run against the TestRecords database.
--
-- Note: Forms.PNID references PN.PNID in the PartsMaster database (cross-database).
--       No FK constraint is possible; only NOT NULL is enforced here.
--
-- WORKFLOW:
--   1. Run STEP 1 — every SELECT must return 0 rows before proceeding.
--   2. Fix any violations.
--   3. Run STEP 2.

-- ============================================================
-- STEP 1: VERIFY — each query must return 0 rows
-- ============================================================

-- ---- Forms ----
SELECT 'Forms: NULL PNID'          AS check_name, ID FROM dbo.Forms WHERE PNID IS NULL;

-- ---- Tests ----
SELECT 'Tests: NULL form_id'       AS check_name, id FROM dbo.Tests WHERE form_id IS NULL;
SELECT 'Tests: orphan form_id'     AS check_name, id, form_id FROM dbo.Tests WHERE form_id IS NOT NULL AND form_id NOT IN (SELECT ID FROM dbo.Forms);

-- ---- TestRecords ----
SELECT 'TestRecords: NULL form_id'   AS check_name, ID FROM dbo.TestRecords WHERE form_id IS NULL;
SELECT 'TestRecords: orphan form_id' AS check_name, ID, form_id FROM dbo.TestRecords WHERE form_id IS NOT NULL AND form_id NOT IN (SELECT ID FROM dbo.Forms);

-- ---- TestResults ----
SELECT 'TestResults: NULL record_id'   AS check_name, ID FROM dbo.TestResults WHERE record_id IS NULL;
SELECT 'TestResults: orphan record_id' AS check_name, ID, record_id FROM dbo.TestResults WHERE record_id IS NOT NULL AND record_id NOT IN (SELECT ID FROM dbo.TestRecords);
SELECT 'TestResults: NULL test_id'     AS check_name, ID FROM dbo.TestResults WHERE test_id IS NULL;
SELECT 'TestResults: orphan test_id'   AS check_name, ID, test_id FROM dbo.TestResults WHERE test_id IS NOT NULL AND test_id NOT IN (SELECT id FROM dbo.Tests);


-- ============================================================
-- STEP 2: MIGRATE — run only after Step 1 returns 0 rows each
-- ============================================================

-- ---- Forms ----
-- No FK — PNID is a cross-database reference to PartsMaster.PN.PNID.
ALTER TABLE dbo.Forms ALTER COLUMN PNID INT NOT NULL;

-- ---- Tests ----
ALTER TABLE dbo.Tests ALTER COLUMN form_id INT NOT NULL;
ALTER TABLE dbo.Tests ADD CONSTRAINT FK_Tests_Forms FOREIGN KEY (form_id) REFERENCES dbo.Forms (ID);

-- ---- TestRecords ----
ALTER TABLE dbo.TestRecords ALTER COLUMN form_id INT NOT NULL;
ALTER TABLE dbo.TestRecords ADD CONSTRAINT FK_TestRecords_Forms FOREIGN KEY (form_id) REFERENCES dbo.Forms (ID);

-- ---- TestResults ----
ALTER TABLE dbo.TestResults ALTER COLUMN record_id INT NOT NULL;
ALTER TABLE dbo.TestResults ADD CONSTRAINT FK_TestResults_TestRecords FOREIGN KEY (record_id) REFERENCES dbo.TestRecords (ID);

ALTER TABLE dbo.TestResults ALTER COLUMN test_id INT NOT NULL;
ALTER TABLE dbo.TestResults ADD CONSTRAINT FK_TestResults_Tests FOREIGN KEY (test_id) REFERENCES dbo.Tests (id);
