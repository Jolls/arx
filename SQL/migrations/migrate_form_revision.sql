-- migrate_form_revision.sql — #260 formal form revision numbers
-- Adds form.revision (release counter, bumped on every unlock->lock) and
-- test_record.form_revision (snapshot of form.revision at record creation).
-- Additive and idempotent; safe to run on ArxProd and ArxDev.

IF COL_LENGTH('dbo.form', 'revision') IS NULL
BEGIN
    ALTER TABLE dbo.form ADD revision INT NOT NULL CONSTRAINT DF_form_revision DEFAULT 0;
    PRINT 'Added form.revision';

    -- Backfill: treat every already-released (locked) form as Rev 1.
    -- EXEC defers compilation until after the ALTER above has run; a bare UPDATE here
    -- fails to compile ("Invalid column name 'revision'") because the whole batch is
    -- parsed before the new column exists.
    EXEC('UPDATE dbo.form SET revision = 1 WHERE is_locked = 1');
    PRINT 'Backfilled form.revision = 1 for locked forms';
END
ELSE
    PRINT 'form.revision already exists — no change';

IF COL_LENGTH('dbo.test_record', 'form_revision') IS NULL
BEGIN
    ALTER TABLE dbo.test_record ADD form_revision INT NULL;
    PRINT 'Added test_record.form_revision';
END
ELSE
    PRINT 'test_record.form_revision already exists — no change';
