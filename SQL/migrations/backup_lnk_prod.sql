-- backup_lnk_prod.sql
-- Run BEFORE migrate_lnk_to_supplier_part.sql.
-- Creates LNK_backup as a full copy of prod LNK (schema + data).
-- Used by validate_lnk_migration_prod.sql for post-migration comparison.
-- Drop manually once the migration is confirmed good.

-- Check current state without referencing LNK_backup directly
SELECT
    t.name                                                                          AS [table],
    t.create_date                                                                   AS [created],
    SUM(p.rows)                                                                     AS [row count]
FROM sys.tables t
JOIN sys.partitions p ON t.object_id = p.object_id AND p.index_id < 2
WHERE t.name IN ('LNK', 'LNK_backup')
GROUP BY t.name, t.create_date
ORDER BY t.name;

IF OBJECT_ID('dbo.LNK_backup', 'U') IS NOT NULL
BEGIN
    PRINT 'LNK_backup already exists — dropping and recreating.';
    DROP TABLE dbo.LNK_backup;
END;

SELECT * INTO dbo.LNK_backup FROM dbo.LNK;

DECLARE @n INT = (SELECT COUNT(*) FROM dbo.LNK_backup);
PRINT 'LNK_backup created: ' + CAST(@n AS VARCHAR) + ' rows — ' + CONVERT(VARCHAR, GETDATE(), 120);
