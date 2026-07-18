-- named_queries: Library of named, parameterized SELECT queries used for spec_nom auto-fill.
-- Referenced in spec_nom as: query:name(@param={test_id})
-- No _Test variant — this is configuration data, not test data. Queries are read-only lookups.
-- sql must be a SELECT statement. Parameters use @name syntax (go-mssqldb named params).
-- params is a comma-separated list of expected parameter names (documentation only).
--
-- Column conventions for the SELECT statement:
--   1 column : the value stored in result.result AND the label shown in the picker.
--   2 columns: col1 = stored value (unique key, e.g. PNPartNumber or PO.number)
--              col2 = human-readable label shown in the picker (e.g. PNTitle or POLDesc)
--   Additional columns beyond 2 are ignored.
--
-- result_type:
--   'list'   → picker shown to the user; may return many rows.
--   'single' → first row taken automatically; additional rows ignored.
--              SQL should use TOP 1 by convention but the app handles extras gracefully.
--   'multi'  → checkboxes; user may select any number; stored as comma-delimited string.

IF OBJECT_ID('dbo.named_queries', 'U') IS NOT NULL DROP TABLE named_queries;

CREATE TABLE named_queries (
  id          INT          PRIMARY KEY IDENTITY,
  name        VARCHAR(100) NOT NULL CONSTRAINT UQ_named_queries_name UNIQUE,  -- key used in spec_nom. Live constraint name: UQ__named_qu__72E12F1B32187951 (auto-named at creation).
  description VARCHAR(500),
  sql         VARCHAR(MAX) NOT NULL,          -- parameterized SELECT; @param_name syntax
  params      VARCHAR(255),                   -- comma-separated expected param names
  result_type VARCHAR(10)  NOT NULL CONSTRAINT DF_named_queries_result_type DEFAULT 'list', -- 'list' = picker; 'single' = take first row only
  is_active   BIT          NOT NULL CONSTRAINT DF_named_queries_is_active    DEFAULT 1,
  created_at  DATETIME,
  updated_at  DATETIME
);

-- Seed: initial named queries derived from existing spec_nom auto-fill patterns.
INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'fil_category_for_pn',
  'Comments of active Attachments for a given part number',
  'SELECT comment FROM part_attachment WHERE part_id = (SELECT id FROM part WHERE part_number = @pn) AND is_active = 1',
  'pn', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'parts_matching',
  'Part numbers matching a LIKE pattern (caller supplies wildcards)',
  'SELECT part_number FROM part WHERE part_number LIKE @pattern AND is_active = 1 ORDER BY part_number DESC',
  'pattern', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'pos_for_pn',
  'PO numbers where a line item part number prefix matches (active = not soft-deleted)',
  'SELECT purchase_order.number FROM po_line LEFT JOIN purchase_order ON po_line.po_id = purchase_order.id WHERE po_line.part_number_snapshot LIKE @pn + ''%'' ORDER BY po_line.po_id DESC',
  'pn', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'bom_pn_by_item',
  'Part number at a specific BOM item position for a given parent assembly PN',
  'SELECT p.part_number, p.title FROM bom JOIN part p ON bom.component_part_id = p.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item',
  'pn, item', 'list',
  GETDATE()
);

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'pn_primary_attachment',
  'Primary attachment for any part number via part.primary_attachment_id; falls back to lowest sort_order if no primary set.',
  'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part p ON f.part_id = p.id WHERE p.part_number = @pn AND f.is_active = 1 ORDER BY CASE WHEN p.primary_attachment_id > 0 AND f.id = p.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
  'pn', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:pn_primary_attachment(@pn={record.pn})
--                or: query:pn_primary_attachment(@pn={15})  (cross-step PN value)
--                or: query:pn_primary_attachment(@pn=924-00462-01)  (literal)
-- UPDATE existing row: UPDATE named_queries SET sql='...', description='...' WHERE name='pn_primary_attachment'

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'form_primary_attachment',
  'Primary attachment for the form''s own part number via part.primary_attachment_id; falls back to lowest sort_order if no primary set.',
  'SELECT TOP 1 f.file_name, COALESCE(f.category, f.file_name) FROM part_attachment f JOIN part p ON f.part_id = p.id WHERE p.id = @pnid AND f.is_active = 1 ORDER BY CASE WHEN p.primary_attachment_id > 0 AND f.id = p.primary_attachment_id THEN 0 ELSE 1 END, f.sort_order ASC',
  'pnid', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:form_primary_attachment(@pnid={form.pnid})
-- UPDATE existing row: UPDATE named_queries SET sql='...', description='...' WHERE name='form_primary_attachment'

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'recent_serial_numbers_for_form',
  'Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.',
  'SELECT TOP 20 serial_number FROM form_record WHERE form_id = @form_id AND is_active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC',
  'form_id', 'multi',
  GETDATE()
);
-- Usage in spec_nom: query:recent_serial_numbers_for_form(@form_id=2)          (literal form ID)
--                or: query:recent_serial_numbers_for_form(@form_id={form.id})   (current form)
-- INSERT into live DB:
-- INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES ('recent_serial_numbers_for_form','Most recent 20 serial numbers tested against a given form (active records only, newest first). Use a literal form_id to reference a different form than the current one.','SELECT TOP 20 serial_number FROM form_record WHERE form_id = @form_id AND is_active = 1 ORDER BY TRY_CAST(serial_number AS INT) DESC, record_date DESC','form_id','multi',GETDATE());


INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'max_subbatch_result',
  'Highest integer result for a test step among active records on or before the given date. Prevents later batches from inflating the max when editing historical records.',
  'SELECT MAX(TRY_CAST(r.result AS INT)) FROM result r JOIN form_record tr ON r.form_record_id = tr.id WHERE r.form_row_id = @form_row_id AND tr.is_active = 1 AND CAST(tr.record_date AS DATE) <= CONVERT(DATE, @record_date, 101)',
  'form_row_id, record_date', 'single',
  GETDATE()
);
-- Usage in spec_nom: query:max_subbatch_result(@form_row_id=117,@record_date={record.date})

INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES (
  'vendor_pns_for_pn',
  'Vendor part numbers and line item description from PO lines whose part number contains the search term (wildcard both sides).',
  'SELECT vendor_part_number, description FROM po_line WHERE part_number_snapshot LIKE ''%'' + @pn + ''%'' ORDER BY po_id DESC',
  'pn', 'list',
  GETDATE()
);
-- Usage in spec_nom: query:vendor_pns_for_pn(@pn={record.pn})
-- INSERT into live DB:
-- INSERT INTO named_queries (name, description, sql, params, result_type, created_at) VALUES ('vendor_pns_for_pn','Vendor part numbers and line item description from PO lines whose part number contains the search term (wildcard both sides).','SELECT vendor_part_number, description FROM po_line WHERE part_number_snapshot LIKE ''%'' + @pn + ''%'' ORDER BY po_id DESC','pn','list',GETDATE());
