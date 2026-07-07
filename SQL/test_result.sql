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
