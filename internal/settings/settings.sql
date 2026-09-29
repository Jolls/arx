-- Settings page reads and the attachment-category list (#190, #247). The backup's table read is not
-- here: its table name is dynamic, so Service.QueryTable runs an allowlist-checked raw query.

-- name: ListContactOptions :many
-- company_id <= 0 means every company.
SELECT id, display_name FROM contact
WHERE is_active = TRUE AND (sqlc.arg(company_id)::int <= 0 OR company_id = sqlc.arg(company_id)::int)
ORDER BY display_name;

-- name: ListSupplierOptions :many
SELECT id, name FROM company WHERE is_active = TRUE ORDER BY name;

-- name: ListAttachmentCategories :many
SELECT display_name FROM attachment_category ORDER BY sort_order, display_name;

-- name: DeleteAttachmentCategories :exec
DELETE FROM attachment_category;

-- name: InsertAttachmentCategory :exec
INSERT INTO attachment_category (display_name, sort_order) VALUES (sqlc.arg(display_name), sqlc.arg(sort_order)::int);
