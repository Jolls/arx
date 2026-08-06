-- #872 / #870 — free-text notes on lots and on test records.
--
-- lot.notes         batch-level, persistent; edited in full on the lot edit page and
--                   appended to (server-side, append-only) from a record's edit page.
-- form_record.notes session-level remark for the whole test session; freezes with the
--                   record when it is locked/approved. Distinct from form_record.comments,
--                   which despite its name holds the record Type dropdown value (see #874).
--
-- Pinned to ArxDev. A human changes the USE line to ArxProd when running it there.
-- Single batch, no GO — the Azure portal query editor sends the whole script as one batch.
-- No dynamic SQL needed: nothing later in this script references the new columns.

USE ArxDev;

IF COL_LENGTH('dbo.lot', 'notes') IS NULL
    ALTER TABLE dbo.lot ADD notes VARCHAR(MAX) NULL;

IF COL_LENGTH('dbo.form_record', 'notes') IS NULL
    ALTER TABLE dbo.form_record ADD notes VARCHAR(MAX) NULL;
