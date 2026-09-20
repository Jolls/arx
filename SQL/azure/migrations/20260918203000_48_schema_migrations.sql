-- #48 — schema_migrations ledger table; the first self-registering migration.
--
-- Additive: creates the table and registers itself. Does not touch app_config.schema_version.
-- Pre-#48 migrate_*.sql files are never registered — no row for one means "unknown".
--
-- Pinned to ArxDev. A human changes the USE line to ArxProd when running it there.
-- Single batch, no GO — the Azure portal query editor sends the whole script as one batch.
-- The registering INSERT is dynamic SQL because the table is created earlier in this same
-- batch, so a plain reference to it wouldn't resolve at compile time.

USE ArxDev;

IF OBJECT_ID('dbo.schema_migrations', 'U') IS NULL
    CREATE TABLE dbo.schema_migrations (
        id         INT      NOT NULL IDENTITY(1,1) PRIMARY KEY,
        version_id BIGINT   NOT NULL,
        is_applied BIT      NOT NULL,
        tstamp     DATETIME NULL DEFAULT CURRENT_TIMESTAMP
    );

EXEC(N'
IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = 20260918203000)
    INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (20260918203000, 1);
');
