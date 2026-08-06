-- migrate_794_named_queries_drop_alias.sql
-- Drop unnecessary single-letter table aliases (p., f.) from three named_queries rows
-- (#794): bom_pn_by_item, pn_primary_attachment, form_primary_attachment. Content-only
-- change (no schema change) — updates the stored `sql` text to match SQL/named_queries.sql.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (unconditional UPDATE by name; safe to re-run).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

UPDATE named_queries
SET sql = 'SELECT part_number, title FROM bom JOIN part ON bom.component_part_id = part.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item'
WHERE name = 'bom_pn_by_item';

UPDATE named_queries
SET sql = 'SELECT TOP 1 part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.part_number = @pn AND part_attachment.is_active = 1 ORDER BY CASE WHEN part.primary_attachment_id > 0 AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC'
WHERE name = 'pn_primary_attachment';

UPDATE named_queries
SET sql = 'SELECT TOP 1 part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.id = @pnid AND part_attachment.is_active = 1 ORDER BY CASE WHEN part.primary_attachment_id > 0 AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC'
WHERE name = 'form_primary_attachment';
