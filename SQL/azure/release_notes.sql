-- release_notes: Application version history and changelog.
-- show_users controls whether a release note is surfaced to users in the UI.
-- username is the author of the release note.
-- TODO: clarify username — is this the author, or a filter for which user sees this note?

IF OBJECT_ID('dbo.release_notes', 'U') IS NOT NULL DROP TABLE release_notes;

CREATE TABLE release_notes (
  id            INT            PRIMARY KEY IDENTITY,
  version       VARCHAR(8)     CONSTRAINT DF_release_notes_version      DEFAULT '0.0.0', -- Semantic version string. TODO: consider VARCHAR(16) for longer version numbers.
  notes         VARCHAR(MAX)   CONSTRAINT DF_release_notes_notes        DEFAULT '',
  date_changed  DATE           CONSTRAINT DF_release_notes_date_changed DEFAULT GETDATE(), -- TODO: change to DATETIME for more precision.
  show_users    BIT            CONSTRAINT DF_release_notes_show_users   DEFAULT 0,
  username      VARCHAR(64)    CONSTRAINT DF_release_notes_username     DEFAULT ''
);
