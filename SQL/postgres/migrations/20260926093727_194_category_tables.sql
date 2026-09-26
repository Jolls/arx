-- Part/attachment categories move from JSON/CSV in app_config to real tables (#194,
-- architecture review 2026-09-24 item 8). BREAKING: schema_version 11 -> 12.
--   * part_category (code PK) + FK_part_category from part.category; the fixed
--     CK_part_number_category CHECK is dropped and '' categories become NULL.
--   * attachment_category (display_name PK); part_attachment.category stays free text.
--   * imports the saved app_config rows (falling back to the defaults), adds any code
--     parts already use, then deletes the app_config keys.
-- Grant the app login SELECT, INSERT, UPDATE, DELETE on both new tables
-- (SQL/SCHEMA.md "Database privileges").
--
-- Pinned to ArxDev: the guard below aborts on any other database. A human edits the guard
-- to run it elsewhere (never default to ArxProd). Idempotent; runs in one transaction:
--   psql -v ON_ERROR_STOP=1 -d ArxDev -f 20260926093727_194_category_tables.sql

BEGIN;

DO $$ BEGIN
  IF lower(current_database()) <> 'arxdev' THEN
    RAISE EXCEPTION 'Pinned to ArxDev (connected to %); edit this guard to run elsewhere', current_database();
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS part_category (
  code                 VARCHAR(10)  NOT NULL PRIMARY KEY,
  label                VARCHAR(255) NOT NULL DEFAULT '',
  is_purchased         BOOLEAN      NOT NULL DEFAULT FALSE,
  is_bom_visible       BOOLEAN      NOT NULL DEFAULT FALSE,
  is_orders_visible    BOOLEAN      NOT NULL DEFAULT FALSE,
  is_pricing_visible   BOOLEAN      NOT NULL DEFAULT FALSE,
  is_mfg_parts_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  is_suppliers_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  is_inventory_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  sort_order           INTEGER      NOT NULL DEFAULT 0,
  created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS attachment_category (
  display_name VARCHAR(500) NOT NULL PRIMARY KEY,
  sort_order   INTEGER      NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Import the Settings-saved part categories (JSON keys code/label/purchased/bom/orders/
-- pricing/mfgParts/suppliers/inventory; a missing key = false). Codes longer than 10
-- characters can't fit part.category and are skipped with a NOTICE.
DO $$
DECLARE skipped text;
BEGIN
  FOR skipped IN
    SELECT e->>'code' FROM app_config ac, jsonb_array_elements(ac.setting_value::jsonb) e
    WHERE ac.setting_key = 'part_categories' AND length(btrim(COALESCE(e->>'code',''))) > 10
  LOOP
    RAISE NOTICE 'skipping part category code longer than 10 characters: %', skipped;
  END LOOP;

  INSERT INTO part_category (code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
                             is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible, sort_order)
  SELECT DISTINCT ON (upper(btrim(e->>'code'))) upper(btrim(e->>'code')), left(COALESCE(btrim(e->>'label'),''), 255),
         COALESCE((e->>'purchased')::boolean, FALSE), COALESCE((e->>'bom')::boolean, FALSE),
         COALESCE((e->>'orders')::boolean, FALSE), COALESCE((e->>'pricing')::boolean, FALSE),
         COALESCE((e->>'mfgParts')::boolean, FALSE), COALESCE((e->>'suppliers')::boolean, FALSE),
         COALESCE((e->>'inventory')::boolean, FALSE), (ord - 1)::int
  FROM app_config ac, jsonb_array_elements(ac.setting_value::jsonb) WITH ORDINALITY t(e, ord)
  WHERE ac.setting_key = 'part_categories' AND length(btrim(COALESCE(e->>'code',''))) BETWEEN 1 AND 10
  ORDER BY upper(btrim(e->>'code')), ord
  ON CONFLICT (code) DO NOTHING;
EXCEPTION WHEN invalid_text_representation OR invalid_parameter_value THEN
  RAISE NOTICE 'app_config.part_categories is not a valid JSON array; using defaults';
END $$;

-- Defaults (= models.DefaultCategories()) when nothing was saved.
INSERT INTO part_category (code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
                           is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible, sort_order)
SELECT * FROM (VALUES
  ('ASM', 'Assembly', FALSE, TRUE, TRUE, TRUE, TRUE, TRUE, TRUE, 0),
  ('BUY', 'Purchased', TRUE, FALSE, TRUE, TRUE, TRUE, TRUE, TRUE, 1),
  ('DWG', 'Drawing', FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, 2),
  ('DOC', 'Document', FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, 3),
  ('FORM', 'Test Form', FALSE, TRUE, FALSE, FALSE, FALSE, FALSE, FALSE, 4),
  ('MFG', 'Manufactured', FALSE, TRUE, TRUE, TRUE, TRUE, TRUE, TRUE, 5),
  ('OPS', 'Operation / Labor', FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, 6),
  ('RAW', 'Raw Material', TRUE, FALSE, TRUE, TRUE, TRUE, TRUE, TRUE, 7),
  ('SVC', 'Service', TRUE, FALSE, TRUE, TRUE, TRUE, TRUE, FALSE, 8),
  ('TOOL', 'Tooling', TRUE, FALSE, TRUE, TRUE, TRUE, TRUE, FALSE, 9)
) v
WHERE NOT EXISTS (SELECT 1 FROM part_category);

-- Codes parts already use that the saved list lacked: permissive tabs, as the app
-- showed them before (models.DefaultCategoryTabs).
INSERT INTO part_category (code, label, is_orders_visible, is_pricing_visible, is_mfg_parts_visible,
                           is_suppliers_visible, is_inventory_visible, sort_order)
SELECT c, c, TRUE, TRUE, TRUE, TRUE, TRUE,
       (SELECT COALESCE(MAX(sort_order), -1) FROM part_category) + ROW_NUMBER() OVER (ORDER BY c)
FROM (SELECT DISTINCT category AS c FROM part WHERE category IS NOT NULL AND category <> '') d
WHERE NOT EXISTS (SELECT 1 FROM part_category pc WHERE pc.code = d.c);

-- Attachment categories from the saved comma list (first occurrence wins), else defaults.
INSERT INTO attachment_category (display_name, sort_order)
SELECT DISTINCT ON (btrim(x)) btrim(x), (ord - 1)::int
FROM app_config ac, unnest(string_to_array(ac.setting_value, ',')) WITH ORDINALITY t(x, ord)
WHERE ac.setting_key = 'attachment_categories' AND btrim(x) <> ''
ORDER BY btrim(x), ord
ON CONFLICT (display_name) DO NOTHING;

INSERT INTO attachment_category (display_name, sort_order)
SELECT * FROM (VALUES
  ('Vendor Link', 0), ('Drawing', 1), ('CAD', 2), ('Datasheet', 3), ('Vendor Document', 4),
  ('Fabrication', 5), ('Schematic', 6), ('Quote', 7), ('BOM', 8), ('SOP', 9),
  ('Certificate', 10), ('Photo', 11), ('PDF Preview', 12), ('Thumbnail', 13)
) v
WHERE NOT EXISTS (SELECT 1 FROM attachment_category);

UPDATE part SET category = NULL WHERE category = '';

ALTER TABLE part DROP CONSTRAINT IF EXISTS ck_part_number_category;

-- Orphan check before the FK (expect 0 rows after the inserts above).
SELECT p.id, p.category FROM part p
LEFT JOIN part_category pc ON pc.code = p.category
WHERE p.category IS NOT NULL AND pc.code IS NULL;

ALTER TABLE part DROP CONSTRAINT IF EXISTS fk_part_category;
ALTER TABLE part ADD CONSTRAINT FK_part_category FOREIGN KEY (category) REFERENCES part_category (code);

DELETE FROM app_config WHERE setting_key IN ('part_categories', 'attachment_categories');

UPDATE app_config SET setting_value = '12' WHERE setting_key = 'schema_version' AND setting_value = '11';

INSERT INTO schema_migrations (version_id, is_applied)
SELECT 20260926093727, TRUE
WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version_id = 20260926093727);

COMMIT;
