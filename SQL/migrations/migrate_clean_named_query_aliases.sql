-- migrate_clean_named_query_aliases.sql
-- Cosmetic cleanup: three named_queries aliased the part table as 'pn' (a leftover from when
-- the table was named PN), e.g. `JOIN part pn`. Renamed the alias to 'p' for readability.
-- No behavior change — same tables/columns, same results.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent.

UPDATE dbo.named_queries
SET    sql        = 'SELECT p.part_number, p.title FROM bom JOIN part p ON bom.component_part_id = p.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item',
       updated_at = GETDATE()
WHERE  name = 'bom_pn_by_item';

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part p ON f.part_id = p.id WHERE p.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN p.primary_attachment_id > 0 AND f.id = p.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at = GETDATE()
WHERE  name = 'pn_primary_attachment';

UPDATE dbo.named_queries
SET    sql        = 'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part p ON f.part_id = p.id WHERE p.id = @pnid AND f.is_active = 1 ORDER BY CASE WHEN p.primary_attachment_id > 0 AND f.id = p.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
       updated_at = GETDATE()
WHERE  name = 'form_primary_attachment';
