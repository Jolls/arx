-- form: Test form definitions. One form per part number / product type.
-- part_number_id links to part.id (logical reference; no FK constraint).
-- test_order is a comma-separated list of test_definition.id values in display order.
-- is_locked prevents structural changes to the form (adding/removing/reordering tests).

IF OBJECT_ID('dbo.form', 'U') IS NOT NULL DROP TABLE form;

CREATE TABLE form (
  id            INT          PRIMARY KEY IDENTITY,
  part_number_id INT         NOT NULL,           -- Logical reference to part.id. No FK constraint.
  test_order    VARCHAR(MAX),                    -- Comma-separated test_definition.id values in display order.
  is_locked     BIT          NOT NULL CONSTRAINT DF_form_is_locked DEFAULT 0, -- 1 = locked from structural changes.
  is_active     BIT          NOT NULL CONSTRAINT DF_form_is_active DEFAULT 1, -- 0 = archived; hidden from UI.
  record_types      VARCHAR(500),                -- comma-separated list of allowed record types (e.g. 'New Release,Re-Test,Upgrade'). NULL = free-text.
  instrument_types  VARCHAR(500)                 -- comma-separated instrument types valid for this form (e.g. 'ModelA,ModelB'). Drives the Instrument Type dropdown on records. NULL = free-text.
);

-- Migration (run once on live DB; _test.sql SELECT * INTO picks it up automatically):
-- ALTER TABLE form ADD record_types VARCHAR(500) NULL;
-- ALTER TABLE form ADD instrument_types VARCHAR(500) NULL;


-- test_definition: Individual test step / parameter definitions within a form.
-- form_id FKs to form.id.
-- type encodes the row role: 0 = measurable test, 1/2/3 = heading level (mirrors VBA).
-- hide_formula = 'HIDE' excludes the row from display.
-- pf_formula is evaluated to determine pass/fail. spec_min/nom/max define the acceptance window.

IF OBJECT_ID('dbo.test_definition', 'U') IS NOT NULL DROP TABLE test_definition;
-- FK added after creation: ALTER TABLE dbo.test_definition ADD CONSTRAINT FK_test_definition_form FOREIGN KEY (form_id) REFERENCES dbo.form (id);

CREATE TABLE test_definition (
  id                  INT          PRIMARY KEY IDENTITY,
  form_id             INT          NOT NULL,             -- FK to form.id.
  archive_id          INT,                               -- TODO: document purpose.
  revision            INT,
  type                INT,                               -- 0 = test row; 1/2/3 = heading level.
  category            VARCHAR(255),
  sheet_name          VARCHAR(255),
  parameter           VARCHAR(255),
  specification       VARCHAR(255),
  spec_units          VARCHAR(255),
  spec_min            VARCHAR(255),
  spec_max            VARCHAR(255),
  spec_nom            VARCHAR(255),
  default_result      VARCHAR(255),
  hide_formula        VARCHAR(255),                      -- 'HIDE' excludes this row from display.
  archived            BIT          NOT NULL CONSTRAINT DF_test_definition_archived DEFAULT 0, -- 1 = retired step; hidden from new records and the live def view, still rendered on historical records that have a result for it.
  pf_type             VARCHAR(50),                       -- Go evaluator: 'range' (default/NULL = range check).
  instrument_types    VARCHAR(255),                      -- Comma-separated instrument type names this step applies to. NULL/empty = applies to all. Matched against test_record.instrument_type.
  format              VARCHAR(255),
  comment             VARCHAR(500),
  created_at          DATETIME,
  updated_at          DATETIME
);


-- test_record: A single test session for one serial number against one form.
-- (serial_number, record_date) is the intended unique pair per instrument.
-- part_number_id FKs to part.id (logical reference; no FK constraint).
-- serial_number_pn and serial_number_pn_desc are denormalized snapshots from part.
-- test_order is a snapshot of form.test_order at record creation;
--   the app falls back to form.test_order when empty.
-- is_locked prevents further edits. is_active = 0 soft-deletes the record.

IF OBJECT_ID('dbo.test_record', 'U') IS NOT NULL DROP TABLE test_record;
-- FK added after creation: ALTER TABLE dbo.test_record ADD CONSTRAINT FK_test_record_form FOREIGN KEY (form_id) REFERENCES dbo.form (id);

CREATE TABLE test_record (
  id                     INT          PRIMARY KEY IDENTITY,
  form_id                INT          NOT NULL,             -- FK to form.id.
  part_number_id         INT,                               -- FK to part.id.
  record_date            DATETIME,                          -- TODO: add UNIQUE (serial_number, record_date).
  serial_number          VARCHAR(64),                       -- TODO: change to INT once all existing records are numeric
  serial_number_pn       VARCHAR(64),                       -- Denormalized PN at record creation.
  serial_number_pn_desc  VARCHAR(64),                       -- Denormalized PN description at record creation.
  test_order             VARCHAR(MAX),                      -- Snapshot of form.test_order at record creation.
  comments               VARCHAR(MAX),
  instrument_type        VARCHAR(100),                      -- Instrument type label (e.g. 'ModelA'). Matched against test_definition.instrument_types to filter applicable steps.
  is_locked              BIT          NOT NULL CONSTRAINT DF_test_record_is_locked DEFAULT 0, -- 1 = record is locked from further edits.
  is_active              BIT          NOT NULL CONSTRAINT DF_test_record_is_active DEFAULT 1, -- 0 = soft-deleted; excluded from all views.
  created_at             DATETIME,
  updated_at             DATETIME
);

-- Migration (run once on live DB; _test.sql SELECT * INTO picks it up automatically):
-- ALTER TABLE test_record ADD instrument_type VARCHAR(100) NULL;


-- form_events: Audit trail for state changes on form (locked, unlocked, archived, activated, etc.).
-- form_id FKs to form.id.
-- event_type is a short string identifying the action taken.

IF OBJECT_ID('dbo.form_events', 'U') IS NOT NULL DROP TABLE form_events;

CREATE TABLE form_events (
  id          INT          PRIMARY KEY IDENTITY,
  form_id     INT          NOT NULL REFERENCES dbo.form(id),  -- FK to form.id.
  event_type  VARCHAR(50)  NOT NULL,                           -- 'locked', 'unlocked', 'archived', 'activated', etc.
  username    VARCHAR(255),                                    -- OS username at the time of the event.
  event_date  DATETIME     NOT NULL DEFAULT GETDATE(),         -- Timestamp of the event.
  comments    VARCHAR(MAX)                                     -- User-supplied comment (required on unlock).
);

-- Migration (run once on live DB — replaces TestRecordHistory):
-- See SQL/TestRecords.sql history or issue #298 for the full migration script.


-- record_events: Audit trail for state changes on test_record (locked, unlocked, archived, activated, etc.).
-- test_record_id FKs to test_record.id.

IF OBJECT_ID('dbo.record_events', 'U') IS NOT NULL DROP TABLE record_events;

CREATE TABLE record_events (
  id             INT          PRIMARY KEY IDENTITY,
  test_record_id INT          NOT NULL REFERENCES dbo.test_record(id),  -- FK to test_record.id.
  event_type     VARCHAR(50)  NOT NULL,                                  -- 'locked', 'unlocked', 'archived', 'activated', etc.
  username       VARCHAR(255),                                           -- OS username at the time of the event.
  event_date     DATETIME     NOT NULL DEFAULT GETDATE(),                -- Timestamp of the event.
  comments       VARCHAR(MAX)                                            -- User-supplied comment (required on unlock).
);


-- test_result: One row per test step per test record.
-- record_id FKs to test_record.id. test_id FKs to test_definition.id.
-- parameter/specification/spec_* are denormalized snapshots from test_definition at record creation.
-- pass_fail: 1 = PASS, 0 = FAIL, NULL = not yet evaluated.

IF OBJECT_ID('dbo.test_result', 'U') IS NOT NULL DROP TABLE test_result;
-- FKs added after creation:
--   ALTER TABLE dbo.test_result ADD CONSTRAINT FK_test_result_test_record   FOREIGN KEY (record_id) REFERENCES dbo.test_record (id);
--   ALTER TABLE dbo.test_result ADD CONSTRAINT FK_test_result_test_definition FOREIGN KEY (test_id)   REFERENCES dbo.test_definition (id);

CREATE TABLE test_result (
  id             INT          PRIMARY KEY IDENTITY,
  record_id      INT          NOT NULL,             -- FK to test_record.id.
  test_id        INT          NOT NULL,             -- FK to test_definition.id.
  pass_fail      BIT,                               -- 1 = PASS, 0 = FAIL, NULL = not evaluated.
  result         VARCHAR(255),
  comment        VARCHAR(255),
  parameter      VARCHAR(255),
  specification  VARCHAR(255),
  spec_units     VARCHAR(255),
  spec_min       VARCHAR(255),
  spec_nom       VARCHAR(255),
  spec_max       VARCHAR(255),
  pf_type        VARCHAR(255),                      -- snapshot of test_definition.pf_type; saved records evaluate P/F against this, not the live def.
  format         VARCHAR(255),                      -- snapshot of test_definition.format; controls how the recorded value renders on the frozen record.
  type           INT NOT NULL CONSTRAINT DF_test_result_type DEFAULT 0,  -- snapshot of test_definition.type; 0 = data row, 1/2/3 = heading. Lets a record render headings without the live def.
  hide_formula   VARCHAR(255),                      -- snapshot of test_definition.hide_formula; frozen visibility, evaluated against the record's own results.
  default_result VARCHAR(255),                      -- snapshot of test_definition.default_result; used by the edit page for auto-calc/pre-fill. Never shown on the read-only view.
  updated_at     DATETIME
);

-- named_queries: Library of named, parameterized SELECT queries used for spec_nom auto-fill.
-- Referenced in spec_nom as: query:name(@param={test_id})
-- No _Test variant — this is configuration data, not test data. Queries are read-only lookups.
-- sql must be a SELECT statement. Parameters use @name syntax (go-mssqldb named params).
-- params is a comma-separated list of expected parameter names (documentation only).
--
-- Column conventions for the SELECT statement:
--   1 column : the value stored in test_result.result AND the label shown in the picker.
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
  'fil_category_for_pn',
  'Attachment categories for a given part number',
  'SELECT category FROM part_attachment WHERE part_id = (SELECT id FROM part WHERE part_number = @pn) AND is_active = 1',
  'pn', 'list',
  GETDATE()
);
-- Migration (run once on live DB): see SQL/migrations/migrate_filnotes_to_category_named_queries.sql

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'parts_matching',
  'Part numbers matching a LIKE pattern (caller supplies wildcards)',
  'SELECT part_number FROM part WHERE part_number LIKE @pattern AND is_active = 1 ORDER BY part_number DESC',
  'pattern', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'pos_for_pn',
  'PO numbers where a line item part number prefix matches (active = not soft-deleted)',
  'SELECT purchase_order.number FROM po_line LEFT JOIN purchase_order ON po_line.po_id = purchase_order.id WHERE is_active = 1 AND po_line.part_number_snapshot LIKE @pn + ''%'' ORDER BY po_line.po_id DESC',
  'pn', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'bom_pn_by_item',
  'Part number at a specific BOM item position for a given parent assembly PN',
  'SELECT p.part_number, p.title FROM bom JOIN part p ON bom.component_part_id = p.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item',
  'pn, item', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'pn_primary_attachment',
  'Primary attachment for any part number via part.primary_attachment_id; falls back to lowest sort_order if no primary set.',
  'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part p ON f.part_id = p.id WHERE p.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN p.primary_attachment_id > 0 AND f.id = p.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
  'pn', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:pn_primary_attachment(@pn={record.pn})
--                or: query:pn_primary_attachment(@pn={15})  (cross-step PN value)
--                or: query:pn_primary_attachment(@pn=924-00462-01)  (literal)
-- UPDATE existing row: UPDATE named_queries SET sql='...', description='...' WHERE name='pn_primary_attachment'

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'form_primary_attachment',
  'Primary attachment for the form''s own part number via part.primary_attachment_id; falls back to lowest sort_order if no primary set.',
  'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part p ON f.part_id = p.id WHERE p.id = @pnid AND f.is_active = 1 ORDER BY CASE WHEN p.primary_attachment_id > 0 AND f.id = p.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
  'pnid', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:form_primary_attachment(@pnid={form.pnid})
-- UPDATE existing row: UPDATE named_queries SET sql='...', description='...' WHERE name='form_primary_attachment'

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'recent_serial_numbers_for_form',
  'Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.',
  'SELECT TOP 20 serial_number FROM test_record WHERE form_id = @form_id AND is_active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC',
  'form_id', 'list',
  GETDATE()
);
-- Usage in spec_nom: query:recent_serial_numbers_for_form(@form_id=2)          (literal form ID)
--                or: query:recent_serial_numbers_for_form(@form_id={form.id})   (current form)
-- INSERT into live DB:
-- INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES ('recent_serial_numbers_for_form','Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.','SELECT TOP 20 serial_number FROM test_record WHERE form_id = @form_id AND is_active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC','form_id','list',GETDATE());


INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'max_subbatch_result',
  'Highest integer result for a test step among active records on or before the given date. Prevents later batches from inflating the max when editing historical records.',
  'SELECT MAX(TRY_CAST(r.result AS INT)) FROM test_result r JOIN test_record tr ON r.record_id = tr.id WHERE r.test_id = @test_id AND tr.is_active = 1 AND CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)',
  'test_id, record_date', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:max_subbatch_result(@test_id=117,@record_date={record.date})


-- test_definition_history: Audit trail for changes to Tests rows.
-- Populated automatically by trg_test_definition_history (AFTER UPDATE trigger on Tests).
-- Each row is a snapshot of the old values captured at the moment of update.
--
-- User identity: changed_by reads the app user from CONTEXT_INFO() when set (Go app calls
-- SET CONTEXT_INFO before each UPDATE), otherwise falls back to SYSTEM_USER (the shared DB
-- login). CONTEXT_INFO is a 128-byte VARBINARY padded with 0x00; null bytes are stripped.
-- Old binaries that don't SET CONTEXT_INFO will record SYSTEM_USER on rollback.

IF OBJECT_ID('dbo.test_definition_history', 'U') IS NOT NULL DROP TABLE test_definition_history;

CREATE TABLE test_definition_history (
  id            INT          PRIMARY KEY IDENTITY,
  test_id       INT          NOT NULL,              -- FK to test_definition.id
  changed_at    DATETIME     NOT NULL DEFAULT GETDATE(),
  changed_by    VARCHAR(128) NOT NULL DEFAULT SYSTEM_USER, -- set by trigger via CONTEXT_INFO(); falls back to SYSTEM_USER
  -- snapshot of values before the update
  type          INT,
  parameter     VARCHAR(255),
  specification VARCHAR(255),
  spec_units    VARCHAR(255),
  spec_min      VARCHAR(255),
  spec_max      VARCHAR(255),
  spec_nom      VARCHAR(255),
  default_result VARCHAR(255),
  hide_formula  VARCHAR(255),
  pf_type       VARCHAR(50),
  instrument_types VARCHAR(255),
  format        VARCHAR(255),
  comment       VARCHAR(500),
  category      VARCHAR(255),
  sheet_name    VARCHAR(255)
);

-- Migrations (run once on live DB):
-- EXEC sp_rename 'test_definition.applicable_instrs',         'instrument_types', 'COLUMN';
-- EXEC sp_rename 'test_definition_history.applicable_instrs', 'instrument_types', 'COLUMN';
-- ALTER TABLE dbo.test_definition_history ADD format VARCHAR(255);           -- added v0.4
-- DROP TRIGGER dbo.trg_Tests_history;   -- legacy trigger from when table was named Tests;
--   referenced old column applicable_instrs, silently rolled back every UPDATE after rename.

-- Trigger: snapshot old values into test_definition_history on every Tests UPDATE.
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
      COALESCE(NULLIF(REPLACE(CONVERT(VARCHAR(128), CONTEXT_INFO()), CHAR(0), ''), ''), SYSTEM_USER),
      type, parameter, specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name
    FROM DELETED;
END
GO


-- TestRecordPartsList: DEPRECATED. Originally stored part number data to remove a PartsMaster
-- dependency. Superseded by the live PartsMaster database join.