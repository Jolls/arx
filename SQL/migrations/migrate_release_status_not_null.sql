-- migrate_release_status_not_null.sql
-- Part release_status: default to 'Under Review' and make NOT NULL (issue #542).
-- Follow-up to #532/#541, where is_active now derives from release_status. A blank
-- ('' or NULL) status is ambiguous — this backfills it to 'U' and enforces NOT NULL.
--
-- Run against BOTH the production database AND ArxDev. The DF_part_number_release_status
-- DEFAULT 'U' constraint already exists, so no default is added here.

-- ============================================================
-- STEP 1: BACKFILL BLANK / NULL STATUS TO 'U' (Under Review)
-- ============================================================

UPDATE dbo.part SET release_status = 'U' WHERE release_status IS NULL OR release_status = '';

-- ============================================================
-- STEP 2: ENFORCE NOT NULL
-- ============================================================

ALTER TABLE dbo.part ALTER COLUMN release_status VARCHAR(255) NOT NULL;
