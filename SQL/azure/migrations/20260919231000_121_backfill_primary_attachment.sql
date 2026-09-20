-- #121 — backfill primary_attachment_id for parts and suppliers that have active
-- attachments but no primary.
--
-- Sets the primary to the first active attachment (lowest sort_order, NULL treated as 0,
-- ties by id). Parts skip the generated 'PDF Preview' / 'Thumbnail' rows, matching the
-- app's auto-primary rule. Existing non-NULL primaries are never touched, so this is
-- idempotent. No schema change; does not touch app_config.schema_version.
--
-- Pinned to ArxDev. A human changes the USE line to ArxProd when running it there.
-- Single batch, no GO — the Azure portal query editor sends the whole script as one batch.

USE ArxDev;

-- Preview: rows this script will change.
SELECT 'part' AS parent, p.id AS parent_id
FROM dbo.part p
WHERE p.primary_attachment_id IS NULL
  AND EXISTS (SELECT 1 FROM dbo.part_attachment a
              WHERE a.part_id = p.id AND a.is_active = 1
                AND COALESCE(a.category, '') NOT IN ('PDF Preview', 'Thumbnail'))
UNION ALL
SELECT 'supplier', c.id
FROM dbo.company c
WHERE c.primary_attachment_id IS NULL
  AND EXISTS (SELECT 1 FROM dbo.company_attachment a
              WHERE a.supplier_id = c.id AND a.is_active = 1);

UPDATE dbo.part
SET primary_attachment_id = (
    SELECT TOP 1 a.id FROM dbo.part_attachment a
    WHERE a.part_id = dbo.part.id AND a.is_active = 1
      AND COALESCE(a.category, '') NOT IN ('PDF Preview', 'Thumbnail')
    ORDER BY COALESCE(a.sort_order, 0), a.id)
WHERE primary_attachment_id IS NULL
  AND EXISTS (SELECT 1 FROM dbo.part_attachment a
              WHERE a.part_id = dbo.part.id AND a.is_active = 1
                AND COALESCE(a.category, '') NOT IN ('PDF Preview', 'Thumbnail'));

UPDATE dbo.company
SET primary_attachment_id = (
    SELECT TOP 1 a.supplier_attachment_id FROM dbo.company_attachment a
    WHERE a.supplier_id = dbo.company.id AND a.is_active = 1
    ORDER BY COALESCE(a.sort_order, 0), a.supplier_attachment_id)
WHERE primary_attachment_id IS NULL
  AND EXISTS (SELECT 1 FROM dbo.company_attachment a
              WHERE a.supplier_id = dbo.company.id AND a.is_active = 1);

IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = 20260919231000)
    INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (20260919231000, 1);
