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
