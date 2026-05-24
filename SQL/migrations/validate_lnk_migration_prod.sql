-- validate_lnk_migration_prod.sql
-- Run AFTER migrate_lnk_to_supplier_part.sql.
-- Requires LNK_backup to exist (created by backup_lnk_prod.sql).
-- Compares supplier_part (migrated) against LNK_backup (pre-migration snapshot).

-- ============================================================
-- 1. COLUMN DIFF: LNK_backup (pre-migration) vs supplier_part (migrated)
--    NEW        = added by migration (renamed columns, mfg_part_id)
--    REMOVED    = dropped or renamed away
--    UNCHANGED  = same name in both (should be none)
-- ============================================================

SELECT
    COALESCE(bk.name, '—') AS [LNK_backup (before)],
    COALESCE(sp.name, '—') AS [supplier_part (after)],
    CASE
        WHEN bk.name IS NULL THEN 'NEW'
        WHEN sp.name IS NULL THEN 'REMOVED / RENAMED'
        ELSE                      'UNCHANGED'
    END AS [status]
FROM (
    SELECT name, column_id FROM sys.columns WHERE object_id = OBJECT_ID('dbo.LNK_backup')
) bk
FULL OUTER JOIN (
    SELECT name, column_id FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier_part')
) sp ON bk.name = sp.name
ORDER BY COALESCE(bk.column_id, sp.column_id);

-- ============================================================
-- 2. ROW COUNT: must match exactly — same source data, just renamed
-- ============================================================

SELECT
    (SELECT COUNT(*) FROM dbo.LNK_backup)   AS [LNK_backup rows (before)],
    (SELECT COUNT(*) FROM dbo.supplier_part) AS [supplier_part rows (after)],
    CASE
        WHEN (SELECT COUNT(*) FROM dbo.LNK_backup) = (SELECT COUNT(*) FROM dbo.supplier_part)
        THEN 'OK — counts match'
        ELSE 'MISMATCH — investigate before proceeding'
    END AS [result];

-- ============================================================
-- 3. NEW TABLES: mfg_part exists and is empty
-- ============================================================

SELECT
    CASE WHEN OBJECT_ID('dbo.mfg_part', 'U') IS NOT NULL THEN 'EXISTS' ELSE 'MISSING' END AS [mfg_part],
    (SELECT COUNT(*) FROM dbo.mfg_part) AS [row count (expect 0)];

-- ============================================================
-- 4. ROLE FLAGS: supplier has new columns with correct defaults
-- ============================================================

SELECT TOP 5
    id, name,
    is_supplier     AS [is_supplier (expect 1)],
    is_manufacturer AS [is_manufacturer (expect 0)]
FROM dbo.supplier
ORDER BY id;

-- ============================================================
-- 5. SPOT CHECK: sample rows look intact
-- ============================================================

SELECT TOP 5
    sp.id, sp.supplier_id, sp.part_id, sp.supplier_pn, sp.preference, sp.is_active,
    bk.LNKID, bk.LNKSUID, bk.LNKPNID, bk.LNKVendorPN, bk.LNKChoice, bk.LNKUse
FROM dbo.supplier_part sp
JOIN dbo.LNK_backup bk ON sp.id = bk.LNKID
ORDER BY sp.id;
