-- form: Test form definitions. One form per part number / product type.
-- part_number_id -> part.id (the FORM's own PN; real FK since #744).
-- test_order is a comma-separated list of form_row.id values in display order.
-- is_locked prevents structural changes to the form (adding/removing/reordering tests).

IF OBJECT_ID('dbo.form', 'U') IS NOT NULL DROP TABLE form;

CREATE TABLE form (
  id            INT          PRIMARY KEY IDENTITY,
  part_number_id INT         NOT NULL,           -- FK to part.id (the FORM's own PN). Real FK since #744.
  test_order    VARCHAR(MAX),                    -- Comma-separated form_row.id values in display order.
  is_locked     BIT          NOT NULL CONSTRAINT DF_form_is_locked DEFAULT 0, -- 1 = locked from structural changes.
  is_active     BIT          NOT NULL CONSTRAINT DF_form_is_active DEFAULT 1, -- 0 = archived; hidden from UI.
  form_type     VARCHAR(20)  NOT NULL CONSTRAINT DF_form_type DEFAULT 'test'  -- Kind of quality document (#744): inspection|test|calibration|checklist|batch record. Orthogonal to record_types (do NOT merge). 'batch record' lives here, not in record_types.
                             CONSTRAINT CK_form_type CHECK (form_type IN ('inspection', 'test', 'calibration', 'checklist', 'batch record')),
  record_types      VARCHAR(500),                -- comma-separated list of allowed record types (e.g. 'New Release,Re-Test,Upgrade'). NULL = free-text. Orthogonal to form_type.
  instrument_types  VARCHAR(500),                -- comma-separated instrument types valid for this form (e.g. 'ModelA,ModelB'). Drives the Instrument Type dropdown on records. NULL = free-text.
  revision      INT          NOT NULL CONSTRAINT DF_form_revision DEFAULT 0 -- number of times this form has been released (locked). 0 = never released ("Draft"); bumped on every unlock->lock transition (#260).
);

ALTER TABLE dbo.form ADD CONSTRAINT FK_form_part FOREIGN KEY (part_number_id) REFERENCES dbo.part (id); -- #744: was a logical reference.

-- Migration (run once on live DB; also add the new column to the matching
-- INSERT in SQL/seed_test_data.sql — it inserts explicit column lists, so new
-- columns are NOT picked up automatically the way the old _test.sql clone was):
-- ALTER TABLE form ADD record_types VARCHAR(500) NULL;
-- ALTER TABLE form ADD instrument_types VARCHAR(500) NULL;
-- form.revision / form_record.form_revision (#260): see migrations/migrate_form_revision.sql
