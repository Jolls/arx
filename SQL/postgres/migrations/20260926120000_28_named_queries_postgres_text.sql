-- Replace the T-SQL text of the 10 canonical named_queries rows with their Postgres
-- translation (#28). Existing Postgres databases got named_queries copied as-is from
-- Azure, so their stored SQL still uses TRY_CAST / `+` concat / TOP / is_active = 1 and
-- fails at spec_nom auto-fill. Text matches SQL/postgres/seed_test_data.sql, including
-- max_subbatch_result's DateStyle-independent TO_DATE(NULLIF(@record_date, ''), ...).
-- Only rows with these canonical names are touched; user-authored queries are left alone.
--
-- Idempotent. Applied by the migrate runner (arx_go/cmd/migrate, #91), one transaction per file.

-- +goose Up
-- +goose StatementBegin

UPDATE named_queries nq
SET sql = v.sql, params = v.params, updated_at = CURRENT_TIMESTAMP
FROM (VALUES
  ('fil_category_for_pn',
   'SELECT comment FROM part_attachment WHERE part_id = (SELECT id FROM part WHERE part_number = @pn) AND is_active = TRUE',
   'pn'),
  ('parts_matching',
   'SELECT part_number FROM part WHERE part_number LIKE @pattern AND is_active = TRUE ORDER BY part_number DESC',
   'pattern'),
  ('pos_for_pn',
   'SELECT purchase_order.number FROM po_line LEFT JOIN purchase_order ON po_line.po_id = purchase_order.id WHERE po_line.part_number_snapshot LIKE @pn || ''%'' ORDER BY po_line.po_id DESC',
   'pn'),
  ('bom_pn_by_item',
   'SELECT part_number, description FROM bom JOIN part ON bom.component_part_id = part.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item',
   'pn, item'),
  ('pn_primary_attachment',
   'SELECT part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.part_number = @pn AND part_attachment.is_active = TRUE ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC LIMIT 1',
   'pn'),
  ('form_primary_attachment',
   'SELECT part_attachment.file_name, COALESCE(part_attachment.category, part_attachment.file_name) FROM part_attachment JOIN part ON part_attachment.part_id = part.id WHERE part.id = @pnid AND part_attachment.is_active = TRUE ORDER BY CASE WHEN part.primary_attachment_id IS NOT NULL AND part_attachment.id = part.primary_attachment_id THEN 0 ELSE 1 END, part_attachment.sort_order ASC LIMIT 1',
   'pnid'),
  ('recent_serial_numbers_for_form',
   'SELECT serial_number FROM form_record WHERE form_id = @form_id AND is_active = TRUE ORDER BY CASE WHEN serial_number ~ ''^[0-9]+$'' THEN CAST(serial_number AS INTEGER) END DESC NULLS LAST, record_date DESC LIMIT 20',
   'form_id'),
  ('max_subbatch_result',
   'SELECT MAX(CASE WHEN r.result ~ ''^[0-9]+$'' THEN CAST(r.result AS INTEGER) END) FROM result r JOIN form_record tr ON r.form_record_id = tr.id WHERE r.form_row_id = @form_row_id AND tr.is_active = TRUE AND CAST(tr.record_date AS DATE) <= TO_DATE(NULLIF(@record_date, ''''), ''MM/DD/YYYY'')',
   'form_row_id, record_date'),
  ('vendor_pns_for_pn',
   'SELECT vendor_part_number, description FROM po_line WHERE part_number_snapshot LIKE ''%'' || @pn || ''%'' ORDER BY po_id DESC',
   'pn'),
  ('revision_for_pn',
   'SELECT revision FROM part WHERE part_number = @pn AND is_active = TRUE',
   'pn')
) AS v(name, sql, params)
WHERE nq.name = v.name
  AND (nq.sql, nq.params) IS DISTINCT FROM (v.sql, v.params);
-- +goose StatementEnd
