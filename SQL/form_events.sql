-- form_events: Audit trail for state changes on form (locked, unlocked, archived, activated, etc.).
-- form_id FKs to form.id.
-- event_type is a short string identifying the action taken.

IF OBJECT_ID('dbo.form_events', 'U') IS NOT NULL DROP TABLE form_events;

CREATE TABLE form_events (
  id          INT          PRIMARY KEY IDENTITY,
  form_id     INT          NOT NULL REFERENCES dbo.form(id),  -- FK to form.id.
  event_type  VARCHAR(50)  NOT NULL,                           -- 'locked', 'unlocked', 'archived', 'activated', etc.
  username    VARCHAR(255),                                    -- OS username at the time of the event.
  event_date  DATETIME     NOT NULL DEFAULT GETDATE(),         -- Timestamp of the event.
  comments    VARCHAR(MAX)                                     -- User-supplied comment (required on unlock).
);

-- Migration (run once on live DB — replaces TestRecordHistory): see issue #298 for the full migration script.
