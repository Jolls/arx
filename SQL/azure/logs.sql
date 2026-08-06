-- logs: Audit log of SQL operations performed by the application.
-- sql_string captures the full SQL statement executed.
-- TODO: add NOT NULL to username and date_logged.
-- NOTE: this file previously contained a full-database backup script (SELECT * INTO _Test tables).
--       That script has been removed from here — if needed, save it as a separate maintenance script.

IF OBJECT_ID('dbo.logs', 'U') IS NOT NULL DROP TABLE logs;

CREATE TABLE logs (
  id           INT            PRIMARY KEY IDENTITY,
  username     VARCHAR(127),
  date_logged  DATETIME       CONSTRAINT DF_logs_date_logged DEFAULT GETDATE(),
  sql_string   VARCHAR(MAX)
);
