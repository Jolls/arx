-- name: ListPartCategories :many
SELECT code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
       is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible
FROM part_category
ORDER BY sort_order, code;

-- name: CountPartsByCategory :many
SELECT COALESCE(category, '') AS category, COUNT(*) AS part_count
FROM part
WHERE category IS NOT NULL
GROUP BY category;

-- name: ListPartCategoryCodes :many
SELECT code FROM part_category;

-- name: UpsertPartCategory :exec
INSERT INTO part_category (code, label, is_purchased, is_bom_visible, is_orders_visible, is_pricing_visible,
                           is_mfg_parts_visible, is_suppliers_visible, is_inventory_visible, sort_order)
VALUES (sqlc.arg(code), sqlc.arg(label), sqlc.arg(is_purchased), sqlc.arg(is_bom_visible),
        sqlc.arg(is_orders_visible), sqlc.arg(is_pricing_visible), sqlc.arg(is_mfg_parts_visible),
        sqlc.arg(is_suppliers_visible), sqlc.arg(is_inventory_visible), sqlc.arg(sort_order))
ON CONFLICT (code) DO UPDATE SET label=EXCLUDED.label, is_purchased=EXCLUDED.is_purchased,
  is_bom_visible=EXCLUDED.is_bom_visible, is_orders_visible=EXCLUDED.is_orders_visible,
  is_pricing_visible=EXCLUDED.is_pricing_visible, is_mfg_parts_visible=EXCLUDED.is_mfg_parts_visible,
  is_suppliers_visible=EXCLUDED.is_suppliers_visible, is_inventory_visible=EXCLUDED.is_inventory_visible,
  sort_order=EXCLUDED.sort_order, updated_at=now();

-- name: DeletePartCategory :exec
DELETE FROM part_category WHERE code = $1;

-- name: ListMfgParts :many
SELECT mp.id, mp.part_id, mp.mfg_id, mp.mfg_part_number, COALESCE(mp.description, '') AS description,
       mp.is_active, c.name AS mfg_name
FROM mfg_part mp
JOIN company c ON mp.mfg_id = c.id
WHERE mp.part_id = $1 AND mp.is_active = TRUE
ORDER BY c.name, mp.mfg_part_number;

-- name: GetMfgPart :one
SELECT id, part_id, mfg_id, mfg_part_number, COALESCE(description, '') AS description
FROM mfg_part
WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id) AND is_active = TRUE;

-- name: CreateMfgPart :exec
INSERT INTO mfg_part (part_id, mfg_id, mfg_part_number, description, is_active)
VALUES (sqlc.arg(part_id), sqlc.arg(mfg_id), sqlc.arg(mfg_part_number), sqlc.arg(description)::text, TRUE);

-- name: UpdateMfgPart :exec
UPDATE mfg_part SET mfg_id = sqlc.arg(mfg_id), mfg_part_number = sqlc.arg(mfg_part_number),
  description = sqlc.arg(description)::text
WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id) AND is_active = TRUE;

-- name: DeleteMfgPart :exec
UPDATE mfg_part SET is_active = FALSE WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: ListManufacturers :many
SELECT id, name FROM company
WHERE is_manufacturer = TRUE AND is_active = TRUE
ORDER BY name;

-- name: ListSupplierParts :many
SELECT sp.id, sp.supplier_id, sp.part_id, sp.preference, COALESCE(sp.supplier_pn, '') AS supplier_pn,
       COALESCE(sp.supplier_desc, '') AS supplier_desc, COALESCE(sp.lead_time, '') AS lead_time,
       sp.min_increment, sp.uom_id, c.name AS supplier_name,
       COALESCE(pu.abbreviation, bu.abbreviation, '') AS purchase_unit_abbr,
       (sp.uom_id IS NOT NULL)::boolean AS purchase_unit_is_explicit
FROM supplier_part sp
JOIN company c  ON sp.supplier_id = c.id
LEFT JOIN uom pu ON sp.uom_id   = pu.uom_id
LEFT JOIN part p ON sp.part_id  = p.id
LEFT JOIN uom bu ON p.uom_id    = bu.uom_id
WHERE sp.part_id = $1
ORDER BY c.name, sp.supplier_pn;

-- name: GetSupplierPart :one
SELECT sp.id, sp.supplier_id, sp.part_id, sp.preference, COALESCE(sp.supplier_pn, '') AS supplier_pn,
       COALESCE(sp.supplier_desc, '') AS supplier_desc, COALESCE(sp.lead_time, '') AS lead_time,
       sp.min_increment, sp.uom_id, c.name AS supplier_name
FROM supplier_part sp
JOIN company c ON sp.supplier_id = c.id
WHERE sp.id = sqlc.arg(id) AND sp.part_id = sqlc.arg(part_id);

-- name: CreateSupplierPart :exec
INSERT INTO supplier_part (supplier_id, part_id, preference, supplier_pn, supplier_desc, lead_time, min_increment, uom_id)
VALUES (sqlc.arg(supplier_id), sqlc.arg(part_id), sqlc.narg(preference), sqlc.arg(supplier_pn)::text,
        sqlc.arg(supplier_desc)::text, sqlc.arg(lead_time)::text, sqlc.narg(min_increment), sqlc.narg(uom_id));

-- name: UpdateSupplierPart :exec
UPDATE supplier_part SET supplier_id = sqlc.arg(supplier_id), preference = sqlc.narg(preference),
  supplier_pn = sqlc.arg(supplier_pn)::text, supplier_desc = sqlc.arg(supplier_desc)::text,
  lead_time = sqlc.arg(lead_time)::text, min_increment = sqlc.narg(min_increment), uom_id = sqlc.narg(uom_id)
WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: DeleteSupplierPart :exec
DELETE FROM supplier_part WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: ListActivePrices :many
SELECT supplier_id, price_ea, price_pack, pack_size, effective_date
FROM price
WHERE part_id = $1 AND is_active = TRUE
ORDER BY supplier_id, pack_size;

-- ImportPrice skips (0 rows) a pack size that already has an active price.
-- name: ImportPrice :execrows
INSERT INTO price (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
VALUES (sqlc.arg(part_id), sqlc.arg(supplier_id), sqlc.arg(pack_size)::numeric, sqlc.arg(price_ea)::numeric,
        sqlc.arg(price_pack)::numeric, sqlc.arg(effective_date)::text::date, TRUE)
ON CONFLICT (part_id, supplier_id, pack_size) WHERE is_active DO NOTHING;

-- name: CreateImportedAttachment :exec
INSERT INTO part_attachment (part_id, file_name, part_revision, category, comment, hash)
VALUES (sqlc.arg(part_id), sqlc.arg(file_name)::text, '', sqlc.arg(category)::text, sqlc.arg(comment)::text, sqlc.arg(hash)::text);

-- CreateManufacturer returns no row when a company already has the name.
-- name: CreateManufacturer :one
INSERT INTO company (name, is_supplier, is_manufacturer) VALUES ($1, FALSE, TRUE)
ON CONFLICT (name) DO NOTHING
RETURNING id;

-- ImportMfgPart leaves an existing active (part, manufacturer, MPN) alone.
-- name: ImportMfgPart :exec
INSERT INTO mfg_part (part_id, mfg_id, mfg_part_number, is_active)
VALUES (sqlc.arg(part_id), sqlc.arg(mfg_id), sqlc.arg(mfg_part_number), TRUE)
ON CONFLICT (part_id, mfg_id, mfg_part_number) WHERE is_active DO NOTHING;

-- ListBOMComponents is one BOM level below a parent: each component's part data and whether it
-- has its own BOM (the RFQ planner, #99).
-- name: ListBOMComponents :many
SELECT pn.id, pl.qty, pn.part_number, COALESCE(pn.description, '') AS description,
       COALESCE(pn.revision, '') AS revision, COALESCE(pn.category, '') AS category,
       pn.stock_on_hand, pn.reorder_min, pn.default_supplier_id,
       EXISTS(SELECT 1 FROM bom c WHERE c.parent_part_id = pn.id) AS has_bom
FROM bom pl
JOIN part pn ON pl.component_part_id = pn.id
WHERE pl.parent_part_id = $1;

-- name: ListPartNumbers :many
SELECT part_number FROM part;

-- name: ListParts :many
-- The /parts grid and CSV export. thumb_file is the part's generated PDF
-- thumbnail (#696); MIN() is an arbitrary tie-break since the app enforces
-- one active Thumbnail row per part.
SELECT p.id, p.part_number, COALESCE(p.revision, '') AS revision,
       COALESCE(p.description, '') AS description, COALESCE(p.detail, '') AS detail,
       COALESCE(p.requested_by, '') AS requested_by, p.created_date,
       COALESCE(p.category, '') AS category, p.modified_date,
       COALESCE(p.is_active, TRUE) AS is_active,
       COALESCE(p.attachment_count, 0) AS attachment_count,
       COALESCE(p.po_line_count, 0) AS po_line_count,
       (p.reorder_min IS NOT NULL AND p.stock_on_hand < p.reorder_min) AS below_min,
       COALESCE((SELECT MIN(a.file_name) FROM part_attachment a
                 WHERE a.part_id = p.id AND a.is_active = TRUE AND a.category = sqlc.arg(thumb_category)::text), '')::text AS thumb_file
FROM part p ORDER BY p.part_number;
