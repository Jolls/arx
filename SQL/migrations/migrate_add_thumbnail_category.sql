-- migrate_add_thumbnail_category.sql
-- PDF first-page images (issue #696). Adds the two generated attachment
-- categories — 'PDF Preview' (large render, shown in the Photos card) and
-- 'Thumbnail' (small render, shown as the /parts part-number hover tooltip) —
-- to the configured attachment_categories option list so they appear in the
-- Category dropdown.
--
-- Pinned to ArxDev. A human changes this to ArxProd when running against
-- production. Idempotent (guarded, safe to re-run) — each value is appended only
-- when not already present.

USE ArxDev;  -- change to ArxProd when running against production
GO

UPDATE app_config
SET setting_value = setting_value + ',PDF Preview'
WHERE setting_key = 'attachment_categories'
  AND ',' + setting_value + ',' NOT LIKE '%,PDF Preview,%';

UPDATE app_config
SET setting_value = setting_value + ',Thumbnail'
WHERE setting_key = 'attachment_categories'
  AND ',' + setting_value + ',' NOT LIKE '%,Thumbnail,%';
