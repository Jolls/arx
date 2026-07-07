-- Migration: VBA ArchiveTest rows → test_definition_history
--
-- Background:
--   The VBA app archived test definition changes by copying rows within the Tests
--   table itself, setting archive_id = the source test's ID. The Go app instead
--   uses the trg_Tests_history trigger, which writes pre-change snapshots to the
--   separate test_definition_history table. This script migrates the old VBA data
--   into test_definition_history so all history is in one place.
--
-- Column mapping:
--   test_id    ← Tests.archive_id   (points back to the live test row)
--   changed_at ← Tests.updated_at   (best available timestamp; represents when
--                                    that version was last saved, not when the
--                                    archive copy was made)
--   changed_by ← 'VBA_MIGRATION'    (original Windows username was never stored)
--   pf_type    ← NULL               (column did not exist in the VBA era)
--   all other fields map directly by name
--
-- Steps:
--   1. Run pre-flight checks (section 1) and review results before proceeding.
--   2. Run against _Test tables (section 2). Verify rows in test_definition_history_Test.
--   3. Run against prod tables (section 3). Verify, then run cleanup (section 4).
--   4. After confirming prod history is correct, drop archive_id and revision columns
--      from Tests (and Tests_Test) — handled separately once UI is verified.
--
-- This script is idempotent within a single run but is NOT safe to run twice —
-- there is no duplicate guard. Run once per environment.
-- ============================================================================


-- ============================================================================
-- SECTION 1 — Pre-flight checks (run and review before migrating)
-- ============================================================================

-- 1a. How many archive rows exist?
SELECT COUNT(*) AS archive_row_count
FROM Tests
WHERE archive_id IS NOT NULL;

-- Same for _Test table:
SELECT COUNT(*) AS archive_row_count_test
FROM Tests_Test
WHERE archive_id IS NOT NULL;

-- 1b. Check for orphaned archive rows (archive_id points to a deleted live row).
--     These have nowhere to land — review before migrating.
SELECT id, archive_id, Parameter, updated_at
FROM Tests
WHERE archive_id IS NOT NULL
  AND archive_id NOT IN (SELECT id FROM Tests WHERE archive_id IS NULL);

-- 1c. Check for potential duplicates with trigger-captured data.
--     If the same test_id already has history rows on the same day as an archive
--     row, clicking that timeline dot will return duplicate step rows (pre-existing
--     handler bug). This query surfaces affected (test_id, day) pairs.
SELECT t.archive_id AS test_id,
       CAST(t.updated_at AS DATE) AS archive_day,
       COUNT(*) AS existing_history_rows
FROM Tests t
JOIN test_definition_history h
  ON h.test_id = t.archive_id
 AND CAST(h.changed_at AS DATE) = CAST(t.updated_at AS DATE)
WHERE t.archive_id IS NOT NULL
GROUP BY t.archive_id, CAST(t.updated_at AS DATE)
ORDER BY test_id, archive_day;


-- ============================================================================
-- SECTION 2 — Migrate _Test tables (run first)
-- ============================================================================

INSERT INTO test_definition_history_Test
  (test_id, changed_at, changed_by,
   type, Parameter, Specification, spec_units,
   spec_min, spec_max, spec_nom, default_result,
   hide_formula, pf_formula, pf_type,
   applicable_instrs, comment, category, sheet_name)
SELECT
  archive_id,
  COALESCE(updated_at, created_at, GETDATE()),
  'VBA_MIGRATION',
  type, Parameter, Specification, spec_units,
  spec_min, spec_max, spec_nom, default_result,
  hide_formula, pf_formula, NULL,
  applicable_instrs, comment, category, sheet_name
FROM Tests_Test
WHERE archive_id IS NOT NULL
ORDER BY archive_id, Revision;

-- Verify: row counts should match archive_row_count_test from 1a.
SELECT COUNT(*) AS migrated_rows FROM test_definition_history_Test WHERE changed_by = 'VBA_MIGRATION';

-- Verify: spot-check a few migrated rows.
SELECT TOP 10 * FROM test_definition_history_Test WHERE changed_by = 'VBA_MIGRATION' ORDER BY changed_at DESC;


-- ============================================================================
-- SECTION 3 — Migrate prod tables (run after section 2 is verified)
-- ============================================================================
-- Run the backups below first. They will error if the tables already exist
-- (DROP them first if re-running). Once migration is confirmed, drop them.

SELECT * INTO Tests_vba_archive_bak        FROM Tests                  WHERE archive_id IS NOT NULL;
SELECT * INTO test_definition_history_bak  FROM test_definition_history WHERE 1=1;

-- To restore if something goes wrong:
--   DELETE FROM test_definition_history WHERE changed_by = 'VBA_MIGRATION';
--   DROP TABLE Tests_vba_archive_bak;
--   DROP TABLE test_definition_history_bak;
--   (fix the issue, then re-run section 3)
--
-- To drop backups once migration is confirmed complete:
--   DROP TABLE Tests_vba_archive_bak;
--   DROP TABLE test_definition_history_bak;


INSERT INTO test_definition_history
  (test_id, changed_at, changed_by,
   type, Parameter, Specification, spec_units,
   spec_min, spec_max, spec_nom, default_result,
   hide_formula, pf_formula, pf_type,
   applicable_instrs, comment, category, sheet_name)
SELECT
  archive_id,
  COALESCE(updated_at, created_at, GETDATE()),
  'VBA_MIGRATION',
  type, Parameter, Specification, spec_units,
  spec_min, spec_max, spec_nom, default_result,
  hide_formula, pf_formula, NULL,
  applicable_instrs, comment, category, sheet_name
FROM Tests
WHERE archive_id IS NOT NULL
ORDER BY archive_id, Revision;

-- Verify: row counts should match archive_row_count from 1a.
SELECT COUNT(*) AS migrated_rows FROM test_definition_history WHERE changed_by = 'VBA_MIGRATION';

-- Verify: confirm timeline dots appear for expected forms.
-- Replace @form_id with a known form that had VBA edits.
 SELECT CAST(changed_at AS DATE) AS day, COUNT(DISTINCT test_id) AS changes
 FROM test_definition_history
 WHERE test_id IN (SELECT id FROM Tests WHERE form_id = @form_id)
 GROUP BY CAST(changed_at AS DATE)
 ORDER BY day;


-- ============================================================================
-- SECTION 3.5 — Refresh _Test tables from prod (run after section 3 is verified)
-- ============================================================================
-- Re-run SQL/_test.sql to wipe and recreate all _Test tables as a fresh
-- snapshot of prod. This replaces the section 2 test migration with the real
-- prod data and gives a clean baseline for future testing.
-- _test.sql already handles test_definition_history_Test and Tests_Test.


-- ============================================================================
-- SECTION 4 — Cleanup (run after prod is verified in the UI)
-- ============================================================================
-- TODO: Run this section once the timeline UI has been used against prod data
--       and the migrated history looks correct. Also drop backup tables and
--       update CLAUDE.md and SQL/test_definition.sql to remove archive_id/revision
--       references after column drops are done.
-- ============================================================================

-- Remove archive rows from Tests and Tests_Test.
-- These are rows where archive_id IS NOT NULL — they are the VBA copies,
-- not live test definitions. The live rows (archive_id IS NULL) are untouched.
--
-- DELETE FROM Tests_Test WHERE archive_id IS NOT NULL;
-- DELETE FROM Tests      WHERE archive_id IS NOT NULL;
--
-- Column drops (archive_id and revision) are a separate step — update this
-- file and run once the UI is confirmed clean.
-- ALTER TABLE Tests      DROP COLUMN archive_id;
-- ALTER TABLE Tests      DROP COLUMN revision;
-- ALTER TABLE Tests_Test DROP COLUMN archive_id;
-- ALTER TABLE Tests_Test DROP COLUMN revision;
