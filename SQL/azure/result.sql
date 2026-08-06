-- result: One row per test step per test record.
-- form_record_id FKs to form_record.id. form_row_id FKs to form_row.id.
-- parameter/specification/spec_* are denormalized snapshots from form_row at record creation.
-- pass_fail: 1 = PASS, 0 = FAIL, NULL = not yet evaluated.

IF OBJECT_ID('dbo.result', 'U') IS NOT NULL DROP TABLE result;
-- FKs added after creation:
--   ALTER TABLE dbo.result ADD CONSTRAINT FK_result_form_record FOREIGN KEY (form_record_id) REFERENCES dbo.form_record (id);
--   ALTER TABLE dbo.result ADD CONSTRAINT FK_result_form_row    FOREIGN KEY (form_row_id) REFERENCES dbo.form_row (id);

CREATE TABLE result (
  id             INT          PRIMARY KEY IDENTITY,
  form_record_id INT          NOT NULL,             -- FK to form_record.id.
  form_row_id    INT          NOT NULL,             -- FK to form_row.id.
  pass_fail      BIT,                               -- 1 = PASS, 0 = FAIL, NULL = not evaluated.
  result         VARCHAR(255),
  comment        VARCHAR(255),
  parameter      VARCHAR(255),
  specification  VARCHAR(255),
  spec_units     VARCHAR(255),
  spec_min       VARCHAR(255),
  spec_nom       VARCHAR(255),
  spec_max       VARCHAR(255),
  pf_type        VARCHAR(255),                      -- snapshot of form_row.pf_type; saved records evaluate P/F against this, not the live def.
  format         VARCHAR(255),                      -- snapshot of form_row.format; controls how the recorded value renders on the frozen record.
  type           INT NOT NULL CONSTRAINT DF_result_type DEFAULT 0,  -- snapshot of form_row.type; 0 = data row, 1/2/3 = heading. Lets a record render headings without the live def.
  hide_formula   VARCHAR(255),                      -- snapshot of form_row.hide_formula; frozen visibility, evaluated against the record's own results.
  default_result VARCHAR(255),                      -- snapshot of form_row.default_result; used by the edit page for auto-calc/pre-fill. Never shown on the read-only view.
  updated_at     DATETIME
);
