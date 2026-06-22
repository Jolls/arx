-- drop_legacy_test_tables.sql
-- One-time cleanup. Drops the vestigial *_Test legacy table copies (and the
-- Tests_vba_archive_bak / TestRecordHistory_Test backups) plus the *_Test sequence
-- (PO_Number_Seq_Test) that linger in the live databases from before TEST_MODE used a
-- separate ArxDev database.
--
-- Nothing in the app references these — the Go app only uses bare table names via
-- cfg.*Table(), and SQL/_test.sql repopulates ArxDev with bare names too. They are
-- pure clutter and still carry pre-rename names/casing.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (drops only what exists).
-- The script PRINTs every statement before running it so you can review the messages.
--
-- Targets: any table whose name ends in '_Test', plus 'Tests_vba_archive_bak'.
-- (No live/app table ends in '_Test', so the pattern is safe.)

SET NOCOUNT ON;

DECLARE @sql NVARCHAR(MAX) = N'';

-- ── 1) Drop any FK constraints touching the doomed tables (either side) ──────────
SELECT @sql = @sql
       + N'ALTER TABLE ' + QUOTENAME(SCHEMA_NAME(pt.schema_id)) + N'.' + QUOTENAME(pt.name)
       + N' DROP CONSTRAINT ' + QUOTENAME(fk.name) + N';' + CHAR(13) + CHAR(10)
FROM   sys.foreign_keys fk
JOIN   sys.tables pt ON pt.object_id = fk.parent_object_id
JOIN   sys.tables rt ON rt.object_id = fk.referenced_object_id
WHERE  pt.name LIKE '%[_]Test' OR pt.name = 'Tests_vba_archive_bak'
   OR  rt.name LIKE '%[_]Test' OR rt.name = 'Tests_vba_archive_bak';

IF @sql <> N''
BEGIN
    PRINT '-- Dropping foreign keys:';
    PRINT @sql;
    EXEC sp_executesql @sql;
END

-- ── 2) Drop the tables ──────────────────────────────────────────────────────────
SET @sql = N'';
SELECT @sql = @sql
       + N'DROP TABLE ' + QUOTENAME(SCHEMA_NAME(t.schema_id)) + N'.' + QUOTENAME(t.name) + N';' + CHAR(13) + CHAR(10)
FROM   sys.tables t
WHERE  t.name LIKE '%[_]Test' OR t.name = 'Tests_vba_archive_bak';

IF @sql <> N''
BEGIN
    PRINT '-- Dropping tables:';
    PRINT @sql;
    EXEC sp_executesql @sql;
    PRINT 'Legacy *_Test tables dropped.';
END
ELSE
    PRINT 'No legacy *_Test tables found — nothing to drop.';

-- ── 3) Drop legacy *_Test sequences (e.g. PO_Number_Seq_Test) ────────────────────
SET @sql = N'';
SELECT @sql = @sql
       + N'DROP SEQUENCE ' + QUOTENAME(SCHEMA_NAME(s.schema_id)) + N'.' + QUOTENAME(s.name) + N';' + CHAR(13) + CHAR(10)
FROM   sys.sequences s
WHERE  s.name LIKE '%[_]Test';

IF @sql <> N''
BEGIN
    PRINT '-- Dropping sequences:';
    PRINT @sql;
    EXEC sp_executesql @sql;
    PRINT 'Legacy *_Test sequences dropped.';
END
ELSE
    PRINT 'No legacy *_Test sequences found — nothing to drop.';
