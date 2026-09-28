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

-- name: GetPartBasic :one
-- The part header behind every part sub-tab page; thumb_file as in ListParts.
SELECT p.id, p.part_number, COALESCE(p.description, '') AS description, COALESCE(p.category, '') AS category,
       EXISTS(SELECT 1 FROM bom c WHERE c.parent_part_id = p.id) AS has_bom,
       p.primary_attachment_id, p.stock_on_hand, p.tracking_mode,
       COALESCE((SELECT MIN(a.file_name) FROM part_attachment a
                 WHERE a.part_id = p.id AND a.is_active = TRUE AND a.category = sqlc.arg(thumb_category)::text), '')::text AS thumb_file
FROM part p WHERE p.id = sqlc.arg(id);

-- name: GetPart :one
SELECT p.id, p.part_number, COALESCE(p.revision, '') AS revision, COALESCE(p.description, '') AS description,
       COALESCE(p.detail, '') AS detail, COALESCE(p.category, '') AS category,
       EXISTS(SELECT 1 FROM bom c WHERE c.parent_part_id = p.id) AS has_bom,
       p.release_status, COALESCE(p.is_active, FALSE) AS is_active,
       COALESCE(p.requested_by, '') AS requested_by, COALESCE(p.notes, '') AS notes,
       p.created_date, p.modified_date, p.primary_attachment_id,
       COALESCE(p.current_cost, 0) AS current_cost, COALESCE(p.last_rollup_cost, 0) AS last_rollup_cost,
       p.last_rollup_at, COALESCE(p.attachment_count, 0) AS attachment_count,
       COALESCE(p.po_line_count, 0) AS po_line_count,
       p.uom_id, COALESCE(u.abbreviation, '') AS unit_abbr, p.stock_on_hand, p.reorder_min, p.tracking_mode,
       COALESCE(p.user_field_1, '') AS user_field_1, COALESCE(p.user_field_2, '') AS user_field_2,
       COALESCE(p.user_field_3, '') AS user_field_3, COALESCE(p.user_field_4, '') AS user_field_4,
       COALESCE(p.user_field_5, '') AS user_field_5, COALESCE(p.user_field_6, '') AS user_field_6,
       COALESCE(p.user_field_7, '') AS user_field_7, COALESCE(p.user_field_8, '') AS user_field_8,
       COALESCE(p.user_field_9, '') AS user_field_9, COALESCE(p.user_field_10, '') AS user_field_10
FROM part p LEFT JOIN uom u ON u.uom_id = p.uom_id
WHERE p.id = $1;

-- name: CreatePart :one
-- A blank category is stored as NULL (uncategorized).
INSERT INTO part (part_number, revision, description, detail, category,
                  release_status, is_active, requested_by, notes, created_date, modified_date,
                  uom_id, current_cost, reorder_min,
                  user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
                  user_field_6, user_field_7, user_field_8, user_field_9, user_field_10, tracking_mode)
VALUES (sqlc.arg(part_number), sqlc.arg(revision)::text, sqlc.arg(description)::text, sqlc.arg(detail)::text,
        NULLIF(sqlc.arg(category)::text, ''), sqlc.arg(release_status), sqlc.arg(is_active)::boolean,
        sqlc.arg(requested_by)::text, sqlc.arg(notes)::text, sqlc.arg(now)::date, sqlc.arg(now)::date,
        sqlc.narg(uom_id), sqlc.arg(current_cost)::numeric, sqlc.narg(reorder_min),
        sqlc.arg(user_field_1)::text, sqlc.arg(user_field_2)::text, sqlc.arg(user_field_3)::text,
        sqlc.arg(user_field_4)::text, sqlc.arg(user_field_5)::text, sqlc.arg(user_field_6)::text,
        sqlc.arg(user_field_7)::text, sqlc.arg(user_field_8)::text, sqlc.arg(user_field_9)::text,
        sqlc.arg(user_field_10)::text, sqlc.arg(tracking_mode))
RETURNING id;

-- name: UpdatePart :exec
UPDATE part SET
  part_number = sqlc.arg(part_number), revision = sqlc.arg(revision)::text, description = sqlc.arg(description)::text,
  detail = sqlc.arg(detail)::text, category = NULLIF(sqlc.arg(category)::text, ''),
  release_status = sqlc.arg(release_status), is_active = sqlc.arg(is_active)::boolean,
  requested_by = sqlc.arg(requested_by)::text, notes = sqlc.arg(notes)::text, modified_date = sqlc.arg(now)::date,
  uom_id = sqlc.narg(uom_id), current_cost = sqlc.arg(current_cost)::numeric, reorder_min = sqlc.narg(reorder_min),
  user_field_1 = sqlc.arg(user_field_1)::text, user_field_2 = sqlc.arg(user_field_2)::text,
  user_field_3 = sqlc.arg(user_field_3)::text, user_field_4 = sqlc.arg(user_field_4)::text,
  user_field_5 = sqlc.arg(user_field_5)::text, user_field_6 = sqlc.arg(user_field_6)::text,
  user_field_7 = sqlc.arg(user_field_7)::text, user_field_8 = sqlc.arg(user_field_8)::text,
  user_field_9 = sqlc.arg(user_field_9)::text, user_field_10 = sqlc.arg(user_field_10)::text,
  tracking_mode = sqlc.arg(tracking_mode)
WHERE id = sqlc.arg(id);

-- name: ListBOMLines :many
-- A parent's BOM lines in line order, with each component's part data and
-- cost inputs; preferred_price is its lowest active price from its default supplier.
SELECT pl.id, pl.line_number, pl.qty, pl.component_part_id,
       pn.part_number, COALESCE(pn.description, '') AS description,
       COALESCE(pn.revision, '') AS revision, COALESCE(pn.category, '') AS category,
       COALESCE(pn.current_cost, 0) AS current_cost, COALESCE(pn.last_rollup_cost, 0) AS last_rollup_cost,
       COALESCE((SELECT MIN(p.price_ea) FROM price p
        WHERE p.part_id = pn.id AND p.is_active = TRUE AND p.supplier_id = pn.default_supplier_id), 0)::numeric AS preferred_price,
       EXISTS(SELECT 1 FROM bom c WHERE c.parent_part_id = pn.id) AS has_bom,
       COALESCE(pn.attachment_count, 0) AS attachment_count, COALESCE(pn.po_line_count, 0) AS po_line_count
FROM bom pl
JOIN part pn ON pl.component_part_id = pn.id
WHERE pl.parent_part_id = $1
ORDER BY pl.line_number;

-- name: ListWhereUsed :many
-- The BOM lines that use a part, with each parent's part data, by parent part number.
SELECT pl.line_number, pl.qty, pl.parent_part_id,
       pn.part_number, COALESCE(pn.description, '') AS description,
       COALESCE(pn.revision, '') AS revision, COALESCE(pn.category, '') AS category
FROM bom pl
JOIN part pn ON pl.parent_part_id = pn.id
WHERE pl.component_part_id = $1
ORDER BY pn.part_number;

-- name: GetPartRollup :one
SELECT COALESCE(last_rollup_cost, 0) AS last_rollup_cost, last_rollup_at FROM part WHERE id = $1;

-- name: GetPartByNumber :one
SELECT id, part_number, COALESCE(description, '') AS description FROM part WHERE part_number = $1;

-- name: DeleteBOMLine :exec
DELETE FROM bom WHERE id = sqlc.arg(id) AND parent_part_id = sqlc.arg(parent_part_id);

-- name: UpdateBOMLine :exec
UPDATE bom SET line_number = sqlc.arg(line_number), qty = sqlc.arg(qty), component_part_id = sqlc.arg(component_part_id)
WHERE id = sqlc.arg(id) AND parent_part_id = sqlc.arg(parent_part_id);

-- name: CreateBOMLine :exec
INSERT INTO bom (parent_part_id, component_part_id, line_number, qty)
VALUES (sqlc.arg(parent_part_id), sqlc.arg(component_part_id), sqlc.arg(line_number), sqlc.arg(qty));

-- name: CopyBOM :exec
INSERT INTO bom (parent_part_id, component_part_id, line_number, qty)
SELECT sqlc.arg(dst_id)::int, s.component_part_id, s.line_number, s.qty FROM bom s WHERE s.parent_part_id = sqlc.arg(src_id);

-- name: ListPartPrices :many
-- Every price row of a part (active or not), by supplier name, newest first, then pack size.
SELECT p.id, p.price_ea, p.price_pack, p.pack_size, COALESCE(p.is_active, FALSE) AS is_active,
       p.effective_date, p.supplier_id, COALESCE(s.name, '') AS supplier_name
FROM price p
LEFT JOIN company s ON p.supplier_id = s.id
WHERE p.part_id = $1
ORDER BY s.name, p.effective_date DESC, p.pack_size;

-- name: GetPartPrice :one
SELECT p.id, p.price_ea, p.price_pack, p.pack_size, COALESCE(p.is_active, FALSE) AS is_active,
       p.effective_date, p.supplier_id, COALESCE(s.name, '') AS supplier_name
FROM price p
LEFT JOIN company s ON p.supplier_id = s.id
WHERE p.id = sqlc.arg(id) AND p.part_id = sqlc.arg(part_id);

-- name: GetDefaultSupplier :one
SELECT default_supplier_id FROM part WHERE id = $1;

-- name: SetDefaultSupplier :exec
UPDATE part SET default_supplier_id = sqlc.arg(supplier_id) WHERE id = sqlc.arg(id);

-- name: CreatePrice :exec
INSERT INTO price (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
VALUES (sqlc.arg(part_id), sqlc.arg(supplier_id), sqlc.narg(pack_size), sqlc.narg(price_ea), sqlc.narg(price_pack),
        sqlc.arg(effective_date)::text::date, TRUE);

-- name: SetPriceActive :exec
UPDATE price SET is_active = sqlc.arg(is_active)::boolean WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: DeleteInactivePrice :exec
DELETE FROM price WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id) AND is_active = FALSE;

-- name: SetPartRollup :exec
UPDATE part SET last_rollup_cost = sqlc.arg(cost)::numeric, last_rollup_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id);

-- ListBuildCostParts and ListActivePriceTiers take their ids comma-separated: sqlc's
-- database/sql output would pass an int[] param through lib/pq's pq.Array.
-- name: ListBuildCostParts :many
SELECT id, part_number, COALESCE(description, '') AS description, default_supplier_id
FROM part WHERE id = ANY(string_to_array(sqlc.arg(ids)::text, ',')::int[]);

-- name: ListActivePriceTiers :many
SELECT part_id, supplier_id, price_ea, pack_size
FROM price WHERE is_active = TRUE AND part_id = ANY(string_to_array(sqlc.arg(part_ids)::text, ',')::int[]);

-- name: ListPartOrders :many
-- Every PO line of a part with its PO header, newest order first.
SELECT po.number, COALESCE(po.supplier_name, '') AS supplier_name, po.date_ordered, po.date_closed,
       COALESCE(po.status, '') AS status, pol.line_number, pol.qty, pol.unit_cost,
       COALESCE(pol.description, '') AS description, COALESCE(pol.vendor_part_number, '') AS vendor_part_number
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE pol.part_id = sqlc.arg(part_id)::int
ORDER BY po.date_ordered DESC;

-- name: ListRecentPartPOs :many
SELECT po.number, COALESCE(po.supplier_name, '') AS supplier_name, COALESCE(po.status, '') AS status,
       po.date_ordered, pol.qty, pol.unit_cost
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE pol.part_id = sqlc.arg(part_id)::int
ORDER BY po.date_ordered DESC, po.id DESC LIMIT sqlc.arg(n)::int;

-- name: ListRecentPartTxns :many
SELECT txn_type, qty, txn_date
FROM inventory_transaction WHERE part_id = sqlc.arg(part_id) ORDER BY txn_date DESC, id DESC LIMIT sqlc.arg(n)::int;

-- name: GetPreferredSupplier :one
-- The part's preferred supplier and its most-preferred supplier_part row, if any. No row when
-- default_supplier_id is NULL.
SELECT c.id, COALESCE(c.name, '') AS name, sp.id AS supplier_part_id,
       COALESCE(sp.supplier_pn, '') AS supplier_pn, COALESCE(sp.supplier_desc, '') AS supplier_desc
FROM part p
JOIN company c ON c.id = p.default_supplier_id
LEFT JOIN supplier_part sp ON sp.part_id = p.id AND sp.supplier_id = c.id
WHERE p.id = $1
ORDER BY sp.preference, sp.id LIMIT 1;

-- name: ListPOPricePoints :many
SELECT po.number, COALESCE(po.supplier_name, '') AS supplier_name, po.date_ordered, pol.unit_cost
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE pol.part_id = sqlc.arg(part_id)::int AND po.date_ordered IS NOT NULL
ORDER BY po.date_ordered;

-- name: ListPriceListPoints :many
SELECT COALESCE(c.name, '') AS supplier_name, p.effective_date, p.price_ea, p.pack_size
FROM price p
LEFT JOIN company c ON p.supplier_id = c.id
WHERE p.part_id = $1 AND p.is_active = TRUE AND p.effective_date IS NOT NULL
ORDER BY p.effective_date;

-- name: PreferredSupplierMinPrice :one
-- The preferred supplier's cheapest active price (NULL prices sort last); no row when there is none.
SELECT price_ea FROM price WHERE part_id = sqlc.arg(part_id) AND is_active = TRUE
AND supplier_id = (SELECT default_supplier_id FROM part WHERE id = sqlc.arg(part_id))
ORDER BY price_ea LIMIT 1;

-- name: EnsureDefaultSupplier :exec
UPDATE part SET default_supplier_id = sqlc.arg(supplier_id)::int WHERE id = sqlc.arg(id) AND default_supplier_id IS NULL;

-- name: SearchParts :many
-- Autocomplete: part_number LIKE pattern, or description/detail LIKE pattern when by_desc.
SELECT id, part_number, COALESCE(revision, '') AS revision,
       COALESCE(description, '') AS description, COALESCE(detail, '') AS detail
FROM part
WHERE CASE WHEN sqlc.arg(by_desc)::bool
           THEN description LIKE sqlc.arg(pattern)::text OR detail LIKE sqlc.arg(pattern)::text
           ELSE part_number LIKE sqlc.arg(pattern)::text END
ORDER BY part_number
LIMIT sqlc.arg(n)::int;

-- name: GetSupplierPartDefaults :one
-- The pair's most-preferred supplier_part row plus its smallest-pack active price_ea; no row when
-- there is no link.
SELECT COALESCE(sp.supplier_pn, '') AS supplier_pn, sp.min_increment,
       (SELECT pr.price_ea FROM price pr
        WHERE pr.part_id = sp.part_id AND pr.supplier_id = sp.supplier_id AND pr.is_active = TRUE
        ORDER BY pr.pack_size LIMIT 1) AS price_ea
FROM supplier_part sp
WHERE sp.part_id = sqlc.arg(part_id) AND sp.supplier_id = sqlc.arg(supplier_id)
ORDER BY sp.preference
LIMIT 1;
