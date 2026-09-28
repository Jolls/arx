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

-- name: ListPORows :many
-- The /pos grid, number descending.
SELECT number, COALESCE(status, '') AS status, supplier_id, rfq_group_id, COALESCE(supplier_name, '') AS supplier_name,
       date_ordered, date_closed, COALESCE(orderer, '') AS orderer, COALESCE(total_cost, 0) AS total_cost
FROM purchase_order
ORDER BY number DESC;

-- name: ListPOExportRows :many
-- One row per PO line (a PO without lines once, line columns NULL), for the CSV export.
SELECT p.number, COALESCE(p.status, '') AS status, COALESCE(p.supplier_name, '') AS supplier_name,
       p.date_ordered, p.date_closed, COALESCE(p.orderer, '') AS orderer, COALESCE(p.total_cost, 0) AS total_cost,
       l.line_number, COALESCE(l.part_number_snapshot, '') AS part_number, COALESCE(l.description, '') AS description,
       COALESCE(l.qty, 0) AS qty, COALESCE(l.unit_cost, 0) AS unit_cost,
       COALESCE(l.vendor_part_number, '') AS vendor_part_number
FROM purchase_order p
LEFT JOIN po_line l ON l.po_id = p.id
ORDER BY p.number DESC, l.line_number;

-- name: GetPO :one
SELECT id, number, COALESCE(status, '') AS status, COALESCE(approval_status, '') AS approval_status,
       COALESCE(is_active, FALSE) AS is_active, COALESCE(orderer, '') AS orderer, COALESCE(account_id, '') AS account_id,
       date_ordered, date_requested, date_closed, date_printed, date_modified,
       supplier_id, COALESCE(supplier_name, '') AS supplier_name, COALESCE(supplier_contact, '') AS supplier_contact,
       supplier_contact_id, COALESCE(supplier_email, '') AS supplier_email,
       COALESCE(supplier_address, '') AS supplier_address, COALESCE(supplier_city, '') AS supplier_city,
       COALESCE(supplier_state, '') AS supplier_state, COALESCE(supplier_zipcode, '') AS supplier_zipcode,
       COALESCE(supplier_country, '') AS supplier_country, COALESCE(supplier_phone_number, '') AS supplier_phone_number,
       COALESCE(supplier_fax_number, '') AS supplier_fax_number,
       receiver_id, COALESCE(receiver_name, '') AS receiver_name, COALESCE(receiver_contact, '') AS receiver_contact,
       receiver_contact_id, COALESCE(receiver_email, '') AS receiver_email,
       COALESCE(receiver_address, '') AS receiver_address, COALESCE(receiver_city, '') AS receiver_city,
       COALESCE(receiver_state, '') AS receiver_state, COALESCE(receiver_zipcode, '') AS receiver_zipcode,
       COALESCE(receiver_country, '') AS receiver_country, COALESCE(receiver_phone, '') AS receiver_phone,
       COALESCE(receiver_fax, '') AS receiver_fax,
       tax1, shipping_cost, misc_cost, total_cost,
       COALESCE(notes, '') AS notes, COALESCE(internal_notes, '') AS internal_notes, rfq_group_id
FROM purchase_order
WHERE number = $1;

-- name: GetPOSupplierID :one
SELECT supplier_id FROM purchase_order WHERE number = $1;

-- name: ListPOLines :many
-- A PO's lines by line number, with the part's tracking mode and primary attachment.
SELECT pol.id, pol.line_number, COALESCE(pol.part_number_snapshot, '') AS part_number_snapshot,
       COALESCE(pol.revision_snapshot, '') AS revision_snapshot, COALESCE(pol.description, '') AS description,
       pol.qty, pol.unit_cost, COALESCE(pol.vendor_part_number, '') AS vendor_part_number, pol.part_id,
       pol.lead_time_days, pol.received_qty, pol.date_received,
       COALESCE(p.tracking_mode, '') AS tracking_mode,
       fil.id AS att_id, COALESCE(fil.file_name, '') AS att_file_name, COALESCE(fil.category, '') AS att_category
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
LEFT JOIN part p ON pol.part_id = p.id
LEFT JOIN part_attachment fil ON p.primary_attachment_id = fil.id
WHERE po.number = $1
ORDER BY pol.line_number;

-- name: ListPOReceipts :many
-- The receipt ledger rows recorded against a PO's lines, newest first.
SELECT it.txn_date, pol.part_id, COALESCE(pol.part_number_snapshot, '') AS part_number, it.qty, it.username
FROM inventory_transaction it
JOIN po_line pol ON it.po_line_id = pol.id
WHERE pol.po_id = $1 AND it.txn_type = 'receipt'
ORDER BY it.txn_date DESC, it.id DESC;

-- name: ListPOHistory :many
-- A PO's status + approval timeline, newest first.
SELECT event_type, COALESCE(from_status, '') AS from_status, COALESCE(to_status, '') AS to_status,
       COALESCE(action, '') AS action, COALESCE(note, '') AS note, changed_by, changed_at
FROM purchase_order_history
WHERE po_id = $1
ORDER BY changed_at DESC, id DESC;

-- name: ListSuggestedLinks :many
-- Catalog lines on a PO whose vendor part number has no supplier_part row for the PO's supplier.
SELECT pol.part_id::int AS part_id, COALESCE(pol.part_number_snapshot, '') AS part_number, pol.vendor_part_number::text AS vendor_part_number
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE po.number = $1
  AND pol.part_id IS NOT NULL
  AND pol.vendor_part_number IS NOT NULL AND pol.vendor_part_number <> ''
  AND po.supplier_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM supplier_part sp
    WHERE sp.part_id = pol.part_id
      AND sp.supplier_id = po.supplier_id
      AND sp.supplier_pn = pol.vendor_part_number
  );

-- name: ListSuggestedPrices :many
-- Distinct priced catalog lines on a PO whose cost no active price of the PO's supplier covers at
-- the same unit cost and a pack size at or below the line qty.
SELECT DISTINCT pol.part_id::int AS part_id, COALESCE(pol.part_number_snapshot, '') AS part_number, pol.unit_cost, pol.qty
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE po.number = $1
  AND pol.part_id IS NOT NULL
  AND pol.unit_cost > 0
  AND po.supplier_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM price pr
    WHERE pr.part_id = pol.part_id
      AND pr.supplier_id = po.supplier_id
      AND pr.is_active = TRUE
      AND pr.price_ea = pol.unit_cost
      AND pr.pack_size <= pol.qty
  );

-- name: ListRFQGroupLines :many
-- One row per (quote, line) in an RFQ group, quotes in id order; a quote without lines once,
-- with a NULL po_line id.
SELECT po.number, COALESCE(po.supplier_name, '') AS supplier_name, po.supplier_id, COALESCE(po.status, '') AS status,
       COALESCE(po.total_cost, 0) AS total_cost,
       pol.id AS pol_id, COALESCE(pol.part_number_snapshot, '') AS part_number,
       COALESCE(pol.revision_snapshot, '') AS revision, COALESCE(pol.description, '') AS description,
       COALESCE(pol.qty, 0) AS qty, COALESCE(pol.unit_cost, 0) AS unit_cost, pol.lead_time_days
FROM purchase_order po
LEFT JOIN po_line pol ON pol.po_id = po.id
WHERE po.rfq_group_id = sqlc.arg(group_id)::int
ORDER BY po.id, pol.line_number;
