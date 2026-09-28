-- name: ListSupplierRows :many
-- The /suppliers grid: every company with its default contact's name/country.
SELECT su.id, su.name, COALESCE(su.supplier_code, '') AS supplier_code,
       COALESCE(su.supplier_part_count, 0) AS supplier_part_count, COALESCE(su.po_count, 0) AS po_count,
       COALESCE(su.is_active, FALSE) AS is_active,
       COALESCE(cn.display_name, '') AS contact_name, COALESCE(cn.country, '') AS contact_country
FROM company su
LEFT JOIN contact cn ON su.default_contact = cn.id
ORDER BY su.name ASC;

-- name: GetSupplier :one
SELECT su.id, su.name, COALESCE(su.supplier_code, '') AS supplier_code, COALESCE(su.notes, '') AS notes,
       su.default_contact, COALESCE(su.is_active, FALSE) AS is_active,
       COALESCE(su.is_supplier, FALSE) AS is_supplier, COALESCE(su.is_manufacturer, FALSE) AS is_manufacturer,
       COALESCE(su.supplier_part_count, 0) AS supplier_part_count, COALESCE(su.po_count, 0) AS po_count,
       su.date_modified, su.primary_attachment_id,
       su.bulk_order_delimiter, su.bulk_order_pn_source,
       COALESCE(cn.display_name, '') AS contact_name, COALESCE(cn.phone_1, '') AS contact_phone,
       COALESCE(cn.email, '') AS contact_email, COALESCE(cn.city, '') AS contact_city
FROM company su
LEFT JOIN contact cn ON su.default_contact = cn.id
WHERE su.id = $1;

-- name: CreateSupplier :one
-- The bulk-order options keep their column defaults.
INSERT INTO company (name, supplier_code, default_contact, is_active, is_supplier, is_manufacturer, notes)
VALUES (sqlc.arg(name), sqlc.arg(supplier_code)::text, sqlc.narg(default_contact),
        sqlc.arg(is_active)::boolean, sqlc.arg(is_supplier)::boolean, sqlc.arg(is_manufacturer)::boolean,
        sqlc.arg(notes)::text)
RETURNING id;

-- name: UpdateSupplier :exec
UPDATE company SET name = sqlc.arg(name), supplier_code = sqlc.arg(supplier_code)::text,
       default_contact = sqlc.narg(default_contact),
       is_active = sqlc.arg(is_active)::boolean, is_supplier = sqlc.arg(is_supplier)::boolean,
       is_manufacturer = sqlc.arg(is_manufacturer)::boolean,
       notes = sqlc.arg(notes)::text, date_modified = CURRENT_TIMESTAMP,
       bulk_order_delimiter = sqlc.arg(bulk_order_delimiter), bulk_order_pn_source = sqlc.arg(bulk_order_pn_source)
WHERE id = sqlc.arg(id);

-- name: SearchSuppliers :many
-- Typeahead: active companies whose name is LIKE pattern, with the default contact's city.
SELECT su.id, su.name, COALESCE(cn.city, '') AS city
FROM company su
LEFT JOIN contact cn ON su.default_contact = cn.id
WHERE su.name LIKE sqlc.arg(pattern)::text AND su.is_active = TRUE
  AND (NOT sqlc.arg(supplier_only)::boolean OR su.is_supplier = TRUE)
ORDER BY su.name
LIMIT sqlc.arg(n)::int;

-- name: ListSupplierPOs :many
-- A supplier's POs (RFQ quotes included), most recent first; a NULL n means no limit.
SELECT number, COALESCE(status, '') AS status, date_ordered, COALESCE(total_cost, 0)::float8 AS total_cost
FROM purchase_order
WHERE supplier_id = sqlc.arg(supplier_id)::int
ORDER BY date_ordered DESC, id DESC
LIMIT sqlc.narg(n)::int;

-- name: ListTopSupplierParts :many
SELECT pn.id, pn.part_number, COALESCE(pn.description, '') AS description
FROM supplier_part sp
JOIN part pn ON sp.part_id = pn.id
WHERE sp.supplier_id = sqlc.arg(supplier_id)::int
ORDER BY pn.part_number
LIMIT sqlc.arg(n)::int;

-- name: ListSupplierLinkedParts :many
-- The supplier's Parts tab. effective_unit is the explicit purchase unit, else the part's base
-- unit; thumb_file as in parts.ListParts.
SELECT sp.id, sp.part_id, sp.preference, COALESCE(sp.supplier_pn, '') AS supplier_pn,
       COALESCE(sp.supplier_desc, '') AS supplier_desc, COALESCE(sp.lead_time, '') AS lead_time,
       sp.min_increment,
       pn.part_number, COALESCE(pn.description, '') AS description, COALESCE(pn.revision, '') AS revision,
       COALESCE(pn.category, '') AS category,
       sp.uom_id,
       COALESCE(pu.abbreviation, bu.abbreviation, '')::text AS effective_unit,
       (sp.uom_id IS NOT NULL)::boolean AS unit_is_explicit,
       COALESCE((SELECT MIN(file_name) FROM part_attachment a
                 WHERE a.part_id = pn.id AND a.is_active = TRUE AND a.category = sqlc.arg(thumb_category)::text), '')::text AS thumb_file
FROM supplier_part sp
JOIN part pn ON sp.part_id = pn.id
LEFT JOIN uom pu ON sp.uom_id = pu.uom_id
LEFT JOIN uom bu ON pn.uom_id = bu.uom_id
WHERE sp.supplier_id = sqlc.arg(supplier_id)::int
ORDER BY pn.part_number;

-- name: ListSupplierPOLinks :many
-- (part, PO number) pairs for a supplier's non-RFQ PO lines, PO number descending.
SELECT pol.part_id, po.number
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE po.supplier_id = sqlc.arg(supplier_id)::int AND po.rfq_group_id IS NULL
ORDER BY po.number DESC;
