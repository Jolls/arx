-- record_event_results: Per-lock result snapshot (#251). One row per data result, captured
-- when a record is completed, linked to the 'completed' record_events row. Rows are written
-- in frozen-row display order so the snapshot renders by id. Never updated after insert.

IF OBJECT_ID('dbo.record_event_results', 'U') IS NOT NULL DROP TABLE record_event_results;

CREATE TABLE record_event_results (
  id            INT          PRIMARY KEY IDENTITY,
  event_id      INT          NOT NULL REFERENCES dbo.record_events(id),  -- the 'completed' event this snapshot belongs to.
  form_row_id   INT          NOT NULL,    -- FK to form_row.id (which step).
  parameter     VARCHAR(255),             -- resolved parameter snapshot, so the row renders without a join.
  specification VARCHAR(255),             -- resolved spec snapshot (tokens already baked in), frozen at completion.
  spec_units    VARCHAR(255),
  result        VARCHAR(255),
  pass_fail     BIT,                       -- 1 = PASS, 0 = FAIL, NULL = not evaluated.
  comment       VARCHAR(255)
);
