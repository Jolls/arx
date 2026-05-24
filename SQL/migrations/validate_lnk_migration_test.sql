-- validate_lnk_migration_test.sql
-- Run after migrate_lnk_to_supplier_part_test.sql, before touching prod.
-- Compares supplier_part_Test (migrated) against LNK (prod, still unmigrated)
-- to confirm the test migration did exactly what was intended.

-- ============================================================
-- 1. COLUMN DIFF: LNK (prod) vs supplier_part_Test (migrated)
--    NEW        = added by migration (renamed columns, mfg_part_id)
--    REMOVED    = dropped or renamed away
--    UNCHANGED  = same name in both (shouldn't happen — all LNK columns changed)
-- ============================================================

SELECT
    COALESCE(lnk.name, '—')  AS [LNK (prod)],
    COALESCE(sp.name,  '—')  AS [supplier_part_Test],
    CASE
        WHEN lnk.name IS NULL THEN 'NEW'
        WHEN sp.name  IS NULL THEN 'REMOVED / RENAMED'
        ELSE                       'UNCHANGED'
    END AS [status]
FROM (
    SELECT name, column_id FROM sys.columns WHERE object_id = OBJECT_ID('dbo.LNK')
) lnk
FULL OUTER JOIN (
    SELECT name, column_id FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier_part_Test')
) sp ON lnk.name = sp.name
ORDER BY COALESCE(lnk.column_id, sp.column_id);

-- ============================================================
-- 2. ROW COUNT: data should be fully preserved
-- NOTE: a mismatch here is expected if _test.sql was not refreshed
-- immediately before this migration. The migration uses only sp_rename
-- and ALTER TABLE — no DML — so it cannot change row counts. Any
-- difference reflects snapshot drift, not migration data loss.
-- ============================================================

SELECT
    (SELECT COUNT(*) FROM dbo.LNK)               AS [LNK prod rows],
    (SELECT COUNT(*) FROM dbo.supplier_part_Test) AS [supplier_part_Test rows],
    CASE
        WHEN (SELECT COUNT(*) FROM dbo.LNK) = (SELECT COUNT(*) FROM dbo.supplier_part_Test)
        THEN 'OK — counts match'
        ELSE 'MISMATCH'
    END AS [result];

-- ============================================================
-- 3. NEW TABLES: mfg_part_Test exists and is empty
-- ============================================================

SELECT
    CASE WHEN OBJECT_ID('dbo.mfg_part_Test', 'U') IS NOT NULL THEN 'EXISTS' ELSE 'MISSING' END AS [mfg_part_Test],
    (SELECT COUNT(*) FROM dbo.mfg_part_Test) AS [row count (expect 0)];

-- ============================================================
-- 4. ROLE FLAGS: supplier_Test has new columns with correct defaults
-- ============================================================

SELECT TOP 5
    id, name,
    is_supplier     AS [is_supplier (expect 1)],
    is_manufacturer AS [is_manufacturer (expect 0)]
FROM dbo.supplier_Test
ORDER BY id;
