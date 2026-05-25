-- Migration: update named_queries rows after FIL.FILNotes → FIL.category column rename (#313)
-- Run on the live database. Safe to run multiple times — each UPDATE is idempotent.
--
-- Background: FIL.FILNotes was renamed to FIL.category in schema migration #313.
-- The named_queries rows that reference FILNotes were not updated at that time.
-- This script fixes them.

-- 1. Rename fil_notes_for_pn → fil_category_for_pn and fix its SQL.
UPDATE named_queries
SET name        = 'fil_category_for_pn',
    description = 'Attachment categories for a given part number',
    sql         = 'SELECT category FROM FIL WHERE FILPNID = (SELECT PNID FROM PN WHERE part_number = @pn) AND is_active = 1',
    updated_at  = GETDATE()
WHERE name = 'fil_notes_for_pn';
PRINT CONCAT('fil_notes_for_pn rows updated: ', @@ROWCOUNT);

-- 2. Fix pn_primary_attachment — replace FILNotes with category in COALESCE.
UPDATE named_queries
SET sql        = 'SELECT TOP 1 f.FILFileName, COALESCE(f.category, f.FILFileName) FROM FIL f JOIN PN pn ON f.FILPNID = pn.PNID WHERE pn.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN pn.PNFILIDPrimary > 0 AND f.FILID = pn.PNFILIDPrimary THEN 0 ELSE 1 END, f.order_id ASC',
    updated_at = GETDATE()
WHERE name = 'pn_primary_attachment'
  AND sql LIKE '%FILNotes%';
PRINT CONCAT('pn_primary_attachment rows updated: ', @@ROWCOUNT);

-- 3. Fix form_primary_attachment — replace FILNotes with category in COALESCE.
UPDATE named_queries
SET sql        = 'SELECT TOP 1 f.FILFileName, COALESCE(f.category, f.FILFileName) FROM FIL f JOIN PN pn ON f.FILPNID = pn.PNID WHERE pn.PNID = @pnid AND f.is_active = 1 ORDER BY CASE WHEN pn.PNFILIDPrimary > 0 AND f.FILID = pn.PNFILIDPrimary THEN 0 ELSE 1 END, f.order_id ASC',
    updated_at = GETDATE()
WHERE name = 'form_primary_attachment'
  AND sql LIKE '%FILNotes%';
PRINT CONCAT('form_primary_attachment rows updated: ', @@ROWCOUNT);

-- Verification: confirm no remaining FILNotes references in any active named query.
SELECT name, sql
FROM named_queries
WHERE sql LIKE '%FILNotes%' AND active = 1;
-- Expected: 0 rows.
