-- form_record: A single test session for one serial number against one form.
-- (serial_number, record_date) is the intended unique pair per instrument.
-- part_number_id FKs to part.id (logical reference; no FK constraint).
-- serial_number_pn and serial_number_pn_desc are denormalized snapshots from part.
-- test_order is a snapshot of form.test_order at record creation;
--   the app falls back to form.test_order when empty.
-- is_locked prevents further edits. is_active = 0 soft-deletes the record.
-- lot_id (#677) ties the record to the lot the tested unit itself belongs to — set only
--   when the tested part is lot-tracked (part.is_lot_tracked), referencing whichever
--   lot a prior receipt or build already produced. NULL otherwise. No write-back to
--   lot_genealogy from form_record itself.
-- build_id (#677) ties the record to the build event that produced the tested unit,
--   independent of lot_id — covers the case where the tested part is NOT itself
--   lot-tracked but its BOM had lot-tracked components: there is no output lot to
--   reference, but the build still recorded which component lots were consumed
--   (inventory_transaction.lot_id/build_id), so this is the only path back to them.
--   NULL when the unit wasn't produced by a build (e.g. purchased, non-assembled part).

IF OBJECT_ID('dbo.form_record', 'U') IS NOT NULL DROP TABLE form_record;
-- FK added after creation: ALTER TABLE dbo.form_record ADD CONSTRAINT FK_form_record_form FOREIGN KEY (form_id) REFERENCES dbo.form (id);
-- FK added after creation: ALTER TABLE dbo.form_record ADD CONSTRAINT FK_form_record_lot FOREIGN KEY (lot_id) REFERENCES dbo.lot (id);
-- FK added after creation: ALTER TABLE dbo.form_record ADD CONSTRAINT FK_form_record_build FOREIGN KEY (build_id) REFERENCES dbo.build (id);

CREATE TABLE form_record (
  id                     INT          PRIMARY KEY IDENTITY,
  form_id                INT          NOT NULL,             -- FK to form.id.
  part_number_id         INT,                               -- FK to part.id.
  record_date            DATETIME,                          -- TODO: add UNIQUE (serial_number, record_date).
  serial_number          VARCHAR(64),                       -- TODO: change to INT once all existing records are numeric
  serial_number_pn       VARCHAR(64),                       -- Denormalized PN at record creation.
  serial_number_pn_desc  VARCHAR(64),                       -- Denormalized PN description at record creation.
  test_order             VARCHAR(MAX),                      -- Snapshot of form.test_order at record creation.
  comments               VARCHAR(MAX),
  instrument_type        VARCHAR(100),                      -- Instrument type label (e.g. 'ModelA'). Matched against form_row.instrument_types to filter applicable steps.
  is_locked              BIT          NOT NULL CONSTRAINT DF_form_record_is_locked DEFAULT 0, -- 1 = record is locked from further edits (Complete or Approved).
  is_approved            BIT          NOT NULL CONSTRAINT DF_form_record_is_approved DEFAULT 0, -- 1 = reviewer-approved; only a TR reviewer may unlock. Requires is_locked = 1.
  is_active              BIT          NOT NULL CONSTRAINT DF_form_record_is_active DEFAULT 1, -- 0 = soft-deleted; excluded from all views.
  created_at             DATETIME,
  updated_at             DATETIME,
  form_revision          INT,                                                                 -- Snapshot of form.revision at record creation. NULL for pre-#260 records.
  lot_id                 INT,                                                                  -- FK to lot.id (#677). Lot the tested unit belongs to; NULL if part not lot-tracked.
  build_id               INT                                                                   -- FK to build.id (#677). Build that produced the tested unit; NULL if not build-produced.
);

-- Migration (run once on live DB; also add the new column to the matching
-- INSERT in SQL/seed_test_data.sql — it inserts explicit column lists, so new
-- columns are NOT picked up automatically the way the old _test.sql clone was):
-- ALTER TABLE form_record ADD instrument_type VARCHAR(100) NULL;
-- ALTER TABLE form_record ADD lot_id INT NULL;
-- ALTER TABLE form_record ADD CONSTRAINT FK_form_record_lot FOREIGN KEY (lot_id) REFERENCES lot (id);
-- ALTER TABLE form_record ADD build_id INT NULL;
-- ALTER TABLE form_record ADD CONSTRAINT FK_form_record_build FOREIGN KEY (build_id) REFERENCES build (id);
