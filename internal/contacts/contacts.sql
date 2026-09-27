-- name: ListContacts :many
SELECT cn.id, cn.company_id, cn.display_name,
       COALESCE(cn.email, '') AS email, COALESCE(cn.phone_1, '') AS phone_1,
       COALESCE(cn.city, '') AS city, COALESCE(cn.state, '') AS state,
       COALESCE(cn.country, '') AS country, COALESCE(cn.website, '') AS website,
       COALESCE(cn.is_active, FALSE) AS is_active, COALESCE(cn.notes, '') AS notes,
       cn.updated_at, COALESCE(su.name, '') AS supplier_name
FROM contact cn
LEFT JOIN company su ON cn.company_id = su.id
ORDER BY su.name, cn.display_name ASC;

-- name: GetContact :one
SELECT cn.id, cn.company_id, cn.display_name,
       COALESCE(cn.email, '') AS email,
       COALESCE(cn.phone_1, '') AS phone_1, COALESCE(cn.phone_2, '') AS phone_2,
       COALESCE(cn.fax, '') AS fax,
       COALESCE(cn.address, '') AS address, COALESCE(cn.city, '') AS city,
       COALESCE(cn.state, '') AS state, COALESCE(cn.zipcode, '') AS zipcode,
       COALESCE(cn.country, '') AS country,
       COALESCE(cn.website, '') AS website, COALESCE(cn.is_active, FALSE) AS is_active,
       COALESCE(cn.notes, '') AS notes, cn.updated_at,
       COALESCE(su.name, '') AS supplier_name
FROM contact cn
LEFT JOIN company su ON cn.company_id = su.id
WHERE cn.id = $1;

-- name: CreateContact :one
INSERT INTO contact (display_name, company_id, email, phone_1, phone_2, fax,
                     address, city, state, zipcode, country,
                     website, is_active, notes)
VALUES (sqlc.arg(display_name), sqlc.arg(company_id),
        sqlc.arg(email)::text, sqlc.arg(phone_1)::text, sqlc.arg(phone_2)::text, sqlc.arg(fax)::text,
        sqlc.arg(address)::text, sqlc.arg(city)::text, sqlc.arg(state)::text,
        sqlc.arg(zipcode)::text, sqlc.arg(country)::text,
        sqlc.arg(website)::text, sqlc.arg(is_active)::boolean, sqlc.arg(notes)::text)
RETURNING id;

-- name: UpdateContact :exec
UPDATE contact SET
  display_name = sqlc.arg(display_name), company_id = sqlc.arg(company_id),
  email = sqlc.arg(email)::text, phone_1 = sqlc.arg(phone_1)::text,
  phone_2 = sqlc.arg(phone_2)::text, fax = sqlc.arg(fax)::text,
  address = sqlc.arg(address)::text, city = sqlc.arg(city)::text, state = sqlc.arg(state)::text,
  zipcode = sqlc.arg(zipcode)::text, country = sqlc.arg(country)::text,
  website = sqlc.arg(website)::text, is_active = sqlc.arg(is_active)::boolean,
  notes = sqlc.arg(notes)::text, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

-- name: ListSiblingContacts :many
SELECT id, display_name FROM contact
WHERE company_id = sqlc.arg(company_id)::int AND id <> sqlc.arg(exclude_id)::int AND is_active = TRUE
ORDER BY display_name;

-- name: ListContactPOs :many
SELECT number,
       CASE WHEN supplier_contact_id = sqlc.arg(contact_id)::int THEN 'Supplier' ELSE 'Receiver' END AS role,
       COALESCE(supplier_name, '') AS supplier_name, COALESCE(status, '') AS status, date_ordered,
       COALESCE(total_cost, 0)::float8 AS total_cost
FROM purchase_order
WHERE supplier_contact_id = sqlc.arg(contact_id)::int OR receiver_contact_id = sqlc.arg(contact_id)::int
ORDER BY date_ordered DESC, id DESC;
