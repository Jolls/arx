-- test_definition_history: Audit trail for changes to Tests rows.
-- Populated automatically by trg_test_definition_history (AFTER UPDATE trigger on Tests; see SQL/triggers.sql).
-- Each row is a snapshot of the old values captured at the moment of update.
--
-- User identity: changed_by reads the app user from CONTEXT_INFO() (the Go app calls
-- SET CONTEXT_INFO before each UPDATE). CONTEXT_INFO is a 128-byte VARBINARY padded with
-- 0x00; null bytes are stripped.

IF OBJECT_ID('dbo.test_definition_history', 'U') IS NOT NULL DROP TABLE test_definition_history;

CREATE TABLE test_definition_history (
  id            INT          PRIMARY KEY IDENTITY,
  test_id       INT          NOT NULL,              -- FK to test_definition.id
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
-- EXEC sp_rename 'test_definition.applicable_instrs',         'instrument_types', 'COLUMN';
-- EXEC sp_rename 'test_definition_history.applicable_instrs', 'instrument_types', 'COLUMN';
-- ALTER TABLE dbo.test_definition_history ADD format VARCHAR(255);           -- added v0.4
-- DROP TRIGGER dbo.trg_Tests_history;   -- legacy trigger from when table was named Tests;
--   referenced old column applicable_instrs, silently rolled back every UPDATE after rename.
