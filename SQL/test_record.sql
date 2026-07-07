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
  is_locked              BIT          NOT NULL CONSTRAINT DF_test_record_is_locked DEFAULT 0, -- 1 = record is locked from further edits (Complete or Approved).
  is_approved            BIT          NOT NULL CONSTRAINT DF_test_record_is_approved DEFAULT 0, -- 1 = reviewer-approved; only a TR reviewer may unlock. Requires is_locked = 1.
  is_active              BIT          NOT NULL CONSTRAINT DF_test_record_is_active DEFAULT 1, -- 0 = soft-deleted; excluded from all views.
  created_at             DATETIME,
  updated_at             DATETIME,
  form_revision          INT                                                                  -- Snapshot of form.revision at record creation. NULL for pre-#260 records.
);

-- Migration (run once on live DB; also add the new column to the matching
-- INSERT in SQL/seed_test_data.sql — it inserts explicit column lists, so new
-- columns are NOT picked up automatically the way the old _test.sql clone was):
-- ALTER TABLE test_record ADD instrument_type VARCHAR(100) NULL;
