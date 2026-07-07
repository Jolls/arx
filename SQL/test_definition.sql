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
