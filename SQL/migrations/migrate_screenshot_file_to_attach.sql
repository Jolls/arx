-- Migration: legacy "Screenshot/File" parameter convention -> pf_type = 'attach' (#587)
--
-- Background:
--   The VBA app gated picture-paste on a step's `parameter` field starting with the
--   literal text "Screenshot/File" (PictureFileCheck in archive/TestRecord/Functions.bas).
--   The Go paste-image feature instead gates on the dedicated `pf_type` column (already
--   used for filled/comment/range), adding a new value: 'attach'. This script flips
--   existing steps from the old parameter-prefix convention to the new pf_type so the
--   "Grab from clipboard" button shows up on forms that already relied on the legacy
--   convention, without any manual re-editing of every form.
--
-- Steps:
--   1. Run the pre-flight SELECT (section 1) and review the rows it will affect.
--   2. Run section 2 against ArxDev first; verify the form editor now shows "attach"
--      selected on the affected steps and the paste button appears on the edit page.
--   3. Run section 2 against prod when ready.
--   4. Section 3 (test_result snapshot rows) is optional — only needed if you want
--      historical/completed records to also read as "attach" (e.g. on the record show
--      page or exports). It does not recompute pass_fail; existing stored values are
--      left as-is.
--
-- Idempotent: the `pf_type <> 'attach'` guard means re-running after a partial or full
-- run is a no-op on rows already migrated. Safe to run more than once per environment.
-- ============================================================================

-- 1. Pre-flight: review which test_definition rows this will affect.
SELECT id, form_id, parameter, pf_type
FROM dbo.test_definition
WHERE parameter LIKE 'Screenshot/File%'
  AND (pf_type IS NULL OR pf_type <> 'attach');

-- 2. Live test definitions: flip pf_type to 'attach' for legacy Screenshot/File steps.
UPDATE dbo.test_definition
   SET pf_type = 'attach'
 WHERE parameter LIKE 'Screenshot/File%'
   AND (pf_type IS NULL OR pf_type <> 'attach');

-- 3. OPTIONAL: also migrate frozen test_result snapshot rows (#487) so completed
--    records that used the legacy convention read as "attach" historically. Does NOT
--    recompute pass_fail — existing stored PASS/FAIL/MISSING values are left untouched.
--    Skip this section if you'd rather leave historical snapshots as they were recorded.
-- UPDATE dbo.test_result
--    SET pf_type = 'attach'
--  WHERE parameter LIKE 'Screenshot/File%'
--    AND (pf_type IS NULL OR pf_type <> 'attach');
