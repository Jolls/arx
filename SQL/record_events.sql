-- record_events: Audit trail for state changes on test_record (locked, unlocked, archived, activated, etc.).
-- test_record_id FKs to test_record.id.

IF OBJECT_ID('dbo.record_events', 'U') IS NOT NULL DROP TABLE record_events;

CREATE TABLE record_events (
  id             INT          PRIMARY KEY IDENTITY,
  test_record_id INT          NOT NULL REFERENCES dbo.test_record(id),  -- FK to test_record.id.
  event_type     VARCHAR(50)  NOT NULL,                                  -- 'locked', 'unlocked', 'archived', 'activated', etc.
  username       VARCHAR(255),                                           -- OS username at the time of the event.
  event_date     DATETIME     NOT NULL DEFAULT GETDATE(),                -- Timestamp of the event.
  comments       VARCHAR(MAX)                                            -- User-supplied comment (required on unlock).
);
