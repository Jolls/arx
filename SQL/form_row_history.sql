-- form_row_history: Audit trail for changes to form_row rows.
-- Populated automatically by trg_form_row_history (AFTER UPDATE trigger on form_row; see SQL/triggers.sql).
-- Each row is a snapshot of the old values captured at the moment of update.
--
-- User identity: changed_by reads the app user from CONTEXT_INFO() (the Go app calls
-- SET CONTEXT_INFO before each UPDATE). CONTEXT_INFO is a 128-byte VARBINARY padded with
-- 0x00; null bytes are stripped.

IF OBJECT_ID('dbo.form_row_history', 'U') IS NOT NULL DROP TABLE form_row_history;

CREATE TABLE form_row_history (
  id            INT          PRIMARY KEY IDENTITY,
  form_row_id   INT          NOT NULL,              -- FK to form_row.id
  changed_at    DATETIME     NOT NULL DEFAULT GETDATE(),
  changed_by    VARCHAR(128) NOT NULL DEFAULT SYSTEM_USER, -- set by trigger via CONTEXT_INFO()
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
-- EXEC sp_rename 'form_row.applicable_instrs',         'instrument_types', 'COLUMN';
-- EXEC sp_rename 'form_row_history.applicable_instrs', 'instrument_types', 'COLUMN';
-- ALTER TABLE dbo.form_row_history ADD format VARCHAR(255);           -- added v0.4
-- DROP TRIGGER dbo.trg_Tests_history;   -- legacy trigger from when table was named Tests;
--   referenced old column applicable_instrs, silently rolled back every UPDATE after rename.
