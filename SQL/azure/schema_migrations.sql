-- schema_migrations: ledger of applied migrations (#48).
-- Column shape follows goose's SQL Server ledger (id / version_id / is_applied / tstamp) so a
-- future runner (#91) can adopt it via a custom table name, not a data migration. tstamp is UTC
-- on Azure SQL. Deliberately NOT snake_case-convention (created_at) for that reason.
--
-- version_id is the YYYYMMDDHHMMSS prefix of the migration's filename. Every migration
-- from #48 on registers itself as its last step; the 28 earlier files never do, so a missing
-- row means "unknown", not "not applied".
--
-- Unlike every other DDL file this one does NOT drop the table first: re-running the
-- reference DDL must never wipe the ledger. It is also excluded from seed_test_data.sql.

IF OBJECT_ID('dbo.schema_migrations', 'U') IS NULL
    CREATE TABLE dbo.schema_migrations (
        id         INT      NOT NULL IDENTITY(1,1) PRIMARY KEY,
        version_id BIGINT   NOT NULL,
        is_applied BIT      NOT NULL,
        tstamp     DATETIME NULL DEFAULT CURRENT_TIMESTAMP
    );
