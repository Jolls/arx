-- Moves one-off (low-count) free-text part_attachment.category values into the new
-- comment column and consolidates their category to 'Other', so the category dropdown
-- stays a clean fixed list without losing the original free-text label (#585).
--
-- Excludes the 3 one-off values with >5 uses (Vendor CAD, Vendor Doc, Document) — those
-- are high-volume enough to warrant a deliberate consolidation into a real category
-- (like the #546 cleanup did), not a blanket move into comment/Other.
--
-- Idempotent: only touches rows whose category isn't already canonical and whose
-- comment is still empty, so re-running after a partial run is safe.

;WITH canonical AS (
    SELECT LTRIM(RTRIM(value)) AS cat
    FROM app_config
    CROSS APPLY STRING_SPLIT(setting_value, ',')
    WHERE setting_key = 'attachment_categories'
)
UPDATE part_attachment
SET comment = category,
    category = 'Other'
WHERE is_active = 1
  AND category NOT IN (SELECT cat FROM canonical)
  AND category NOT IN ('Vendor CAD', 'Vendor Doc', 'Document')
  AND (comment IS NULL OR comment = '');
