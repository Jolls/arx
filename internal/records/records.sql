-- Test-record domain (#190, #248; grows with #223). sqlc generates internal/dbq/records.sql.go
-- from this file; the service is records.go.

-- name: GetRecordHeader :one
-- A record's serial, lock state and its form's part number (the paste-image guard reads this).
SELECT COALESCE(r.serial_number, '') AS serial_number, r.is_locked, pn.part_number
FROM form_record r
JOIN form f ON r.form_id = f.id
JOIN part pn ON f.part_number_id = pn.id
WHERE r.id = sqlc.arg(id);
