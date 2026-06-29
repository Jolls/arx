-- migrate_record_event_results.sql
-- Per-lock result history (issue #251). Adds the result-snapshot table written on each
-- Complete/lock transition and read by the record detail view's audit log.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (guarded, safe to re-run).
--
-- Purely additive (new table only) — backward-compatible: a pre-0.5.48 binary never
-- references this table, so no schema_version bump is needed. Apply it before (or with)
-- deploying the v0.5.48 binary; the binary creates no tables itself.

IF OBJECT_ID('dbo.record_event_results', 'U') IS NULL
BEGIN
    CREATE TABLE dbo.record_event_results (
      id            INT          PRIMARY KEY IDENTITY,
      event_id      INT          NOT NULL,    -- the 'completed' record_events row this snapshot belongs to.
      test_id       INT          NOT NULL,    -- FK to test_definition.id (which step).
      parameter     VARCHAR(255),             -- resolved parameter snapshot, so the row renders without a join.
      specification VARCHAR(255),             -- resolved spec snapshot (tokens already baked in), frozen at completion.
      spec_units    VARCHAR(255),
      result        VARCHAR(255),
      pass_fail     BIT,                       -- 1 = PASS, 0 = FAIL, NULL = not evaluated.
      comment       VARCHAR(255)
    );

    ALTER TABLE dbo.record_event_results
        ADD CONSTRAINT FK_record_event_results_event FOREIGN KEY (event_id) REFERENCES dbo.record_events (id);
END

-- Frozen spec columns: added if an earlier version of this migration created the table without them.
IF COL_LENGTH('dbo.record_event_results', 'specification') IS NULL
    ALTER TABLE dbo.record_event_results ADD specification VARCHAR(255);
IF COL_LENGTH('dbo.record_event_results', 'spec_units') IS NULL
    ALTER TABLE dbo.record_event_results ADD spec_units VARCHAR(255);
