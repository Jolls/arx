-- Migration: rename Tests → test_definition, Tests_Test → test_definition_Test
-- Run on the live database before deploying the updated binary.
-- Safe to run multiple times — each step is guarded.

BEGIN TRANSACTION;
BEGIN TRY

    -- 1. Rename Tests_Test (no constraints to rename — SELECT INTO copies none).
    IF OBJECT_ID('dbo.Tests_Test', 'U') IS NOT NULL AND OBJECT_ID('dbo.test_definition_Test', 'U') IS NULL
    BEGIN
        EXEC sp_rename 'dbo.Tests_Test', 'test_definition_Test';
        PRINT 'Renamed Tests_Test → test_definition_Test';
    END

    -- 2. Rename Tests (production).
    IF OBJECT_ID('dbo.Tests', 'U') IS NOT NULL AND OBJECT_ID('dbo.test_definition', 'U') IS NULL
    BEGIN
        EXEC sp_rename 'dbo.Tests', 'test_definition';
        PRINT 'Renamed Tests → test_definition';
    END

    -- 3. Rename FK constraint on test_definition (was FK_Tests_Forms).
    IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = 'FK_Tests_Forms')
    BEGIN
        EXEC sp_rename 'dbo.FK_Tests_Forms', 'FK_test_definition_Forms', 'OBJECT';
        PRINT 'Renamed FK_Tests_Forms → FK_test_definition_Forms';
    END

    -- 4. Rename FK on TestResults referencing test_definition (was FK_TestResults_Tests).
    IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = 'FK_TestResults_Tests')
    BEGIN
        EXEC sp_rename 'dbo.FK_TestResults_Tests', 'FK_TestResults_test_definition', 'OBJECT';
        PRINT 'Renamed FK_TestResults_Tests → FK_TestResults_test_definition';
    END

    COMMIT;
    PRINT 'Migration complete — ' + CONVERT(VARCHAR, GETDATE(), 120);

END TRY
BEGIN CATCH
    ROLLBACK;
    PRINT 'Migration failed: ' + ERROR_MESSAGE();
END CATCH;
