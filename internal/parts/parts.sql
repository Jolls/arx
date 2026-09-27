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
