-- Forms: Test form definitions. One form per part number / product type.
-- PNID links to PN.PNID in the PartsMaster database (cross-database; no FK constraint).
-- test_order is a comma-separated list of Tests.id values in display order.
-- locked prevents structural changes to the form (adding/removing/reordering tests).

IF OBJECT_ID('dbo.Forms', 'U') IS NOT NULL DROP TABLE Forms;

CREATE TABLE Forms (
  ID            INT          PRIMARY KEY IDENTITY,
  PNID          INT          NOT NULL,           -- Cross-database reference to PartsMaster PN.PNID. No FK constraint possible.
  test_order    VARCHAR(MAX),                    -- Comma-separated Tests.id values in display order.
  locked        BIT          NOT NULL CONSTRAINT DF_Forms_locked DEFAULT 0, -- 1 = locked from structural changes.
  active        BIT          NOT NULL CONSTRAINT DF_Forms_active DEFAULT 1, -- 0 = archived; hidden from UI.
  record_types  VARCHAR(500)                     -- comma-separated list of allowed record types (e.g. 'New Release,Re-Test,Upgrade'). NULL = free-text.
);

-- Migration (run once on live DB; _test.sql SELECT * INTO picks it up automatically):
-- ALTER TABLE Forms ADD record_types VARCHAR(500) NULL;
-- ALTER TABLE Forms_Test ADD record_types VARCHAR(500) NULL;


-- Tests: Individual test step / parameter definitions within a form.
-- form_id FKs to Forms.ID.
-- type encodes the row role: 0 = measurable test, 1/2/3 = heading level (mirrors VBA).
-- hide_formula = 'HIDE' excludes the row from display.
-- pf_formula is evaluated to determine pass/fail. spec_min/nom/max define the acceptance window.
-- NOTE: Parameter and Specification are capitalized in the live DB; the app normalizes
--       to lowercase via transform_keys(&:downcase).

-- TODO: rename Tests → test_definition and Tests_Test → test_definition_Test
--       to align with the test_definition_history naming convention.
--       Requires updating all Go handlers, config helpers, _test.sql, CLAUDE.md,
--       and running a DB rename (sp_rename or DROP/CREATE).
IF OBJECT_ID('dbo.Tests', 'U') IS NOT NULL DROP TABLE Tests;
-- FK added after creation: ALTER TABLE dbo.Tests ADD CONSTRAINT FK_Tests_Forms FOREIGN KEY (form_id) REFERENCES dbo.Forms (ID);

CREATE TABLE Tests (
  id                  INT          PRIMARY KEY IDENTITY,
  form_id             INT          NOT NULL,             -- FK to Forms.ID.
  archive_id          INT,                               -- TODO: document purpose.
  revision            INT,
  type                INT,                               -- 0 = test row; 1/2/3 = heading level.
  category            VARCHAR(255),
  sheet_name          VARCHAR(255),
  Parameter           VARCHAR(255),                      -- NOTE: capitalized in live DB.
  Specification       VARCHAR(255),                      -- NOTE: capitalized in live DB.
  spec_units          VARCHAR(255),
  spec_min            VARCHAR(255),
  spec_max            VARCHAR(255),
  spec_nom            VARCHAR(255),
  default_result      VARCHAR(255),
  hide_formula        VARCHAR(255),                      -- 'HIDE' excludes this row from display.
  pf_formula          VARCHAR(255),
  pf_type             VARCHAR(50),                       -- Go evaluator: 'range' (default/NULL = range check).
  applicable_instrs   VARCHAR(255),
  format              VARCHAR(255),
  comment             VARCHAR(500),
  created_at          DATETIME,
  updated_at          DATETIME
);


-- TestRecords: A single test session for one serial number against one form.
-- (serial_number, record_date) is the intended unique pair per instrument.
-- part_number_id FKs to PartsMaster PN.PNID (cross-database; no FK constraint).
-- serial_number_PN and serial_number_PNDesc are denormalized snapshots from PartsMaster.
-- test_order is a snapshot of Forms.test_order at record creation;
--   the app falls back to Forms.test_order when empty.
-- locked prevents further edits. active = 0 soft-deletes the record.

IF OBJECT_ID('dbo.TestRecords', 'U') IS NOT NULL DROP TABLE TestRecords;
-- FK added after creation: ALTER TABLE dbo.TestRecords ADD CONSTRAINT FK_TestRecords_Forms FOREIGN KEY (form_id) REFERENCES dbo.Forms (ID);

CREATE TABLE TestRecords (
  ID                   INT          PRIMARY KEY IDENTITY,
  form_id              INT          NOT NULL,             -- FK to Forms.ID.
  part_number_id       INT,                               -- FK to PartsMaster PN.PNID.
  record_date          DATETIME,                          -- TODO: add UNIQUE (serial_number, record_date).
  serial_number        VARCHAR(64),                       -- TODO: change to INT once all existing records are numeric
  serial_number_PN     VARCHAR(64),                       -- Denormalized PN at record creation.
  serial_number_PNDesc VARCHAR(64),                       -- Denormalized PN description at record creation.
  test_order           VARCHAR(MAX),                      -- Snapshot of Forms.test_order at record creation.
  comments             VARCHAR(MAX),
  locked               BIT          NOT NULL CONSTRAINT DF_TestRecords_locked DEFAULT 0, -- 1 = record is locked from further edits.
  active               BIT          NOT NULL CONSTRAINT DF_TestRecords_active DEFAULT 1, -- 0 = soft-deleted; excluded from all views.
  created_at           DATETIME,
  updated_at           DATETIME
);


-- TestRecordHistory: Audit trail for lock/unlock events on forms and test records.
-- history_type: 0 = unspecified, 1 = Form event, 2 = TestRecord event.
-- record_id FKs to Forms.ID or TestRecords.ID depending on history_type.
-- history_locked mirrors the locked status of the parent at the time of the event.

IF OBJECT_ID('dbo.TestRecordHistory', 'U') IS NOT NULL DROP TABLE TestRecordHistory;

CREATE TABLE TestRecordHistory (
  ID                INT          PRIMARY KEY IDENTITY,
  history_type      INT,                                  -- 0 = unspecified, 1 = Form, 2 = TestRecord.
  record_id         INT,                                  -- FK to Forms.ID or TestRecords.ID.
  username          VARCHAR(255),                         -- OS username at the time of the event.
  history_date      DATETIME,                             -- Timestamp of the event.
  history_comments  VARCHAR(MAX),                         -- User-supplied comment when unlocking.
  history_locked    BIT                                   -- Locked status at the time of the event.
);


-- TestResults: One row per test step per test record.
-- record_id FKs to TestRecords.ID. test_id FKs to Tests.id.
-- parameter/specification/spec_* are denormalized snapshots from Tests at record creation.
-- form_id is denormalized (derivable via TestRecords.form_id). TODO: evaluate removing.
-- pass_fail: 1 = PASS, 0 = FAIL, NULL = not yet evaluated.

IF OBJECT_ID('dbo.TestResults', 'U') IS NOT NULL DROP TABLE TestResults;
-- FKs added after creation:
--   ALTER TABLE dbo.TestResults ADD CONSTRAINT FK_TestResults_TestRecords FOREIGN KEY (record_id) REFERENCES dbo.TestRecords (ID);
--   ALTER TABLE dbo.TestResults ADD CONSTRAINT FK_TestResults_Tests       FOREIGN KEY (test_id)   REFERENCES dbo.Tests (id);

CREATE TABLE TestResults (
  ID             INT          PRIMARY KEY IDENTITY,
  record_id      INT          NOT NULL,             -- FK to TestRecords.ID.
  test_id        INT          NOT NULL,             -- FK to Tests.id.
  form_id        INT,                               -- Denormalized from TestRecords. TODO: evaluate removing.
  pass_fail      BIT,                               -- 1 = PASS, 0 = FAIL, NULL = not evaluated.
  result         VARCHAR(255),
  comment        VARCHAR(255),
  parameter      VARCHAR(255),
  specification  VARCHAR(255),
  spec_units     VARCHAR(255),
  spec_min       VARCHAR(255),
  spec_nom       VARCHAR(255),
  spec_max       VARCHAR(255),
  updated_at     DATETIME
);

-- named_queries: Library of named, parameterized SELECT queries used for spec_nom auto-fill.
-- Referenced in spec_nom as: query:name(@param={test_id})
-- No _Test variant — this is configuration data, not test data. Queries are read-only lookups.
-- sql must be a SELECT statement. Parameters use @name syntax (go-mssqldb named params).
-- params is a comma-separated list of expected parameter names (documentation only).
--
-- Column conventions for the SELECT statement:
--   1 column : the value stored in TestResults.result AND the label shown in the picker.
--   2 columns: col1 = stored value (unique key, e.g. PNPartNumber or PO.number)
--              col2 = human-readable label shown in the picker (e.g. PNTitle or POLDesc)
--   Additional columns beyond 2 are ignored.
--
-- result_type:
--   'list'   → picker shown to the user; may return many rows.
--   'single' → first row taken automatically; additional rows ignored.
--              SQL should use TOP 1 by convention but the app handles extras gracefully.
--   'multi'  → checkboxes; user may select any number; stored as comma-delimited string.

IF OBJECT_ID('dbo.named_queries', 'U') IS NOT NULL DROP TABLE named_queries;

CREATE TABLE named_queries (
  id          INT          PRIMARY KEY IDENTITY,
  name        VARCHAR(100) NOT NULL CONSTRAINT UQ_named_queries_name UNIQUE,  -- key used in spec_nom. Live constraint name: UQ__named_qu__72E12F1B32187951 (auto-named at creation).
  description VARCHAR(500),
  sql         VARCHAR(MAX) NOT NULL,          -- parameterized SELECT; @param_name syntax
  params      VARCHAR(255),                   -- comma-separated expected param names
  result_type VARCHAR(10)  NOT NULL CONSTRAINT DF_named_queries_result_type DEFAULT 'list', -- 'list' = picker; 'single' = take first row only
  active      BIT          NOT NULL CONSTRAINT DF_named_queries_active       DEFAULT 1,
  created_at  DATETIME,
  updated_at  DATETIME
);

-- Seed: initial named queries derived from existing spec_nom auto-fill patterns.
INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'fil_notes_for_pn',
  'File attachment notes for a given part number',
  'SELECT FILNotes FROM FIL WHERE FILPNID = (SELECT PNID FROM PN WHERE PNPartNumber = @pn) AND is_active = 1',
  'pn', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'parts_matching',
  'Part numbers matching a LIKE pattern (caller supplies wildcards)',
  'SELECT PNPartNumber FROM PN WHERE PNPartNumber LIKE @pattern AND PNActive = 1 ORDER BY PNPartNumber DESC',
  'pattern', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'pos_for_pn',
  'PO numbers where a line item part number prefix matches (active = not soft-deleted)',
  'SELECT PO.number FROM POL LEFT JOIN PO ON POL.POLPOID = PO.id WHERE is_active = 1 AND POL.POLPNPartNumber LIKE @pn + ''%'' ORDER BY POL.POLPOID DESC',
  'pn', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'bom_pn_by_item',
  'Part number at a specific BOM item position for a given parent assembly PN',
  'SELECT PN.PNPartNumber, PN.PNTitle FROM PL JOIN PN ON PL.PLPartID = PN.PNID WHERE PL.PLListID = (SELECT PNID FROM PN WHERE PNPartNumber = @pn) AND PL.PLItem = @item',
  'pn, item', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'pn_primary_attachment',
  'Primary attachment for any part number via PN.PNFILIDPrimary; falls back to lowest order_id if no primary set.',
  'SELECT TOP 1 f.FILFileName, COALESCE(f.FILNotes, f.FILFileName) FROM FIL f JOIN PN pn ON f.FILPNID = pn.PNID WHERE pn.PNPartNumber = @pn AND f.is_active = 1 ORDER BY CASE WHEN pn.PNFILIDPrimary > 0 AND f.FILID = pn.PNFILIDPrimary THEN 0 ELSE 1 END, f.order_id ASC',
  'pn', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:pn_primary_attachment(@pn={record.pn})
--                or: query:pn_primary_attachment(@pn={15})  (cross-step PN value)
--                or: query:pn_primary_attachment(@pn=924-00462-01)  (literal)
-- UPDATE existing row: UPDATE named_queries SET sql='...', description='...' WHERE name='pn_primary_attachment'

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'form_primary_attachment',
  'Primary attachment for the form''s own part number via PN.PNFILIDPrimary; falls back to lowest order_id if no primary set.',
  'SELECT TOP 1 f.FILFileName, COALESCE(f.FILNotes, f.FILFileName) FROM FIL f JOIN PN pn ON f.FILPNID = pn.PNID WHERE pn.PNID = @pnid AND f.is_active = 1 ORDER BY CASE WHEN pn.PNFILIDPrimary > 0 AND f.FILID = pn.PNFILIDPrimary THEN 0 ELSE 1 END, f.order_id ASC',
  'pnid', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:form_primary_attachment(@pnid={form.pnid})
-- UPDATE existing row: UPDATE named_queries SET sql='...', description='...' WHERE name='form_primary_attachment'

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'recent_serial_numbers_for_form',
  'Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.',
  'SELECT TOP 20 serial_number FROM TestRecords WHERE form_id = @form_id AND active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC',
  'form_id', 'list',
  GETDATE()
);
-- Usage in spec_nom: query:recent_serial_numbers_for_form(@form_id=2)          (literal form ID)
--                or: query:recent_serial_numbers_for_form(@form_id={form.id})   (current form)
-- INSERT into live DB:
-- INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES ('recent_serial_numbers_for_form','Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.','SELECT TOP 20 serial_number FROM TestRecords WHERE form_id = @form_id AND active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC','form_id','list',GETDATE());


INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'max_subbatch_result',
  'Highest integer result for a test step among active records on or before the given date. Prevents later batches from inflating the max when editing historical records.',
  'SELECT MAX(TRY_CAST(r.result AS INT)) FROM TestResults r JOIN TestRecords tr ON r.record_id = tr.ID WHERE r.test_id = @test_id AND tr.active = 1 AND CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)',
  'test_id, record_date', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:max_subbatch_result(@test_id=117,@record_date={record.date})


-- test_definition_history: Audit trail for changes to Tests rows.
-- Populated automatically by trg_Tests_history (AFTER UPDATE trigger on Tests).
-- Each row is a snapshot of the old values captured at the moment of update.
--
-- TODO (user login): changed_by currently stores SYSTEM_USER (the DB login, same for all apps).
-- Once user authentication is added to the Go app, use SET CONTEXT_INFO before each UPDATE
-- to pass the logged-in username, then read it in the trigger via CAST(CONTEXT_INFO() AS VARCHAR(128)).
-- VBA uses Environ("USERNAME") (Windows login) which Go cannot access from a web server context —
-- the server process runs as its own user, not the browser client's Windows user.

IF OBJECT_ID('dbo.test_definition_history', 'U') IS NOT NULL DROP TABLE test_definition_history;

CREATE TABLE test_definition_history (
  id            INT          PRIMARY KEY IDENTITY,
  test_id       INT          NOT NULL,              -- FK to Tests.id
  changed_at    DATETIME     NOT NULL DEFAULT GETDATE(),
  changed_by    VARCHAR(128) NOT NULL DEFAULT SYSTEM_USER, -- TODO: replace with app user via CONTEXT_INFO
  -- snapshot of values before the update
  type          INT,
  Parameter     VARCHAR(255),
  Specification VARCHAR(255),
  spec_units    VARCHAR(255),
  spec_min      VARCHAR(255),
  spec_max      VARCHAR(255),
  spec_nom      VARCHAR(255),
  default_result VARCHAR(255),
  hide_formula  VARCHAR(255),
  pf_formula    VARCHAR(255),
  pf_type       VARCHAR(50),
  applicable_instrs VARCHAR(255),
  comment       VARCHAR(500),
  category      VARCHAR(255),
  sheet_name    VARCHAR(255)
);

-- Trigger: snapshot old values into test_definition_history on every Tests UPDATE.
-- Uses DELETED pseudo-table which contains pre-update row values.
-- Set-based: handles bulk updates (multiple rows changed at once) correctly.
IF OBJECT_ID('dbo.trg_Tests_history', 'TR') IS NOT NULL DROP TRIGGER trg_Tests_history;
GO
CREATE TRIGGER dbo.trg_Tests_history
ON dbo.Tests
AFTER UPDATE
AS
BEGIN
    SET NOCOUNT ON;
    INSERT INTO dbo.test_definition_history
      (test_id, changed_at, changed_by,
       type, Parameter, Specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_formula, pf_type,
       applicable_instrs, comment, category, sheet_name)
    SELECT
      id, GETDATE(), SYSTEM_USER,
      type, Parameter, Specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_formula, pf_type,
      applicable_instrs, comment, category, sheet_name
    FROM DELETED;
END
GO


-- TestRecordPartsList: DEPRECATED. Originally stored part number data to remove a PartsMaster
-- dependency. Superseded by the live PartsMaster database join.