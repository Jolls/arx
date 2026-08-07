-- migrate_735c_primary_attachment_id_fk_promotion.sql
-- #213 follow-up (#735 Group C): part.primary_attachment_id -> part_attachment.id.
-- Converts the DEFAULT 0 "no value" sentinel to NULL, matching the precedent already set by
-- company.primary_attachment_id -> company_attachment.id (NULL-based, FK-enforced).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalents
-- follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- 1. Convert the 0-sentinel to NULL.
UPDATE dbo.part SET primary_attachment_id = NULL WHERE primary_attachment_id = 0;
-- Postgres: UPDATE part SET primary_attachment_id = NULL WHERE primary_attachment_id = 0;

-- 2. Drop the DEFAULT 0 constraint. Looked up dynamically by column, not by the
--    DF_part_number_primary_attachment_id name in SQL/azure/part.sql — a live DB may have
--    auto-named it at creation, matching the pattern in
--    migrate_rename_named_queries_active.sql.
DECLARE @df_primary_att sysname;
SELECT @df_primary_att = dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
WHERE dc.parent_object_id = OBJECT_ID('dbo.part') AND c.name = 'primary_attachment_id';
IF @df_primary_att IS NOT NULL
    EXEC('ALTER TABLE dbo.part DROP CONSTRAINT ' + @df_primary_att);
-- Postgres: ALTER TABLE part ALTER COLUMN primary_attachment_id DROP DEFAULT;

-- 3. Promote to a real FK. ORPHAN CHECK FIRST on a live DB — must return zero rows:
--      SELECT p.id, p.primary_attachment_id FROM dbo.part p
--      LEFT JOIN dbo.part_attachment fa ON fa.id = p.primary_attachment_id
--      WHERE p.primary_attachment_id IS NOT NULL AND fa.id IS NULL;
IF OBJECT_ID('dbo.FK_part_primary_attachment', 'F') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.part_attachment (id);
-- Postgres: ALTER TABLE part ADD CONSTRAINT FK_part_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES part_attachment (id);

-- 4. Patch stored named_queries text: 'pn_primary_attachment' and 'form_primary_attachment' both
--    embed `part.primary_attachment_id > 0` in their CASE expression. Functionally this still
--    works after the NULL conversion (NULL > 0 is never true, same as the old 0-sentinel
--    exclusion), but update the text anyway so it doesn't read as stale/misleading — mirrors the
--    named_queries content-update pattern in migrate_794_named_queries_drop_alias.sql.
UPDATE dbo.named_queries
SET sql = 'SELECT TOP 1 part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.part_number = @pn AND part_attachment.is_active = 1 ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC'
WHERE name = 'pn_primary_attachment';
UPDATE dbo.named_queries
SET sql = 'SELECT TOP 1 part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.id = @pnid AND part_attachment.is_active = 1 ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC'
WHERE name = 'form_primary_attachment';
-- Postgres: named_queries is dialect-agnostic row data — identical UPDATEs apply.
