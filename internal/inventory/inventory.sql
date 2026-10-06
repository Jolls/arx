-- Inventory domain (#190, #222): the stock ledger, lots, builds, units and genealogy. sqlc
-- generates internal/dbq/inventory.sql.go from this file; the service is inventory.go.

-- ── Ledger ─────────────────────────────────────────────────────────────────────

-- name: CreateInventoryTxn :exec
INSERT INTO inventory_transaction (part_id, txn_type, qty, txn_date, username, reference, note, po_line_id, lot_id, build_id)
VALUES (sqlc.arg(part_id), sqlc.arg(txn_type), sqlc.arg(qty), sqlc.arg(txn_date), sqlc.arg(username),
        sqlc.narg(reference)::text, sqlc.narg(note)::text, sqlc.narg(po_line_id), sqlc.narg(lot_id), sqlc.narg(build_id));

-- name: AddPartStock :exec
-- The part's cached on-hand, moved by the same signed qty as its ledger row.
UPDATE part SET stock_on_hand = stock_on_hand + sqlc.arg(qty) WHERE id = sqlc.arg(id);

-- name: ListPartLedger :many
-- Oldest first; the caller accumulates the running balance.
SELECT it.txn_type, it.qty, it.txn_date, it.username,
       COALESCE(it.reference, '') AS reference, COALESCE(it.note, '') AS note,
       COALESCE(l.id, 0)::int AS lot_id, COALESCE(l.lot_number, '') AS lot_number
FROM inventory_transaction it
LEFT JOIN lot l ON l.id = it.lot_id
WHERE it.part_id = sqlc.arg(part_id) ORDER BY it.txn_date ASC, it.id ASC;

-- ── Lots ───────────────────────────────────────────────────────────────────────

-- name: CreateLot :one
-- source stays NULL (unclassified), as before.
INSERT INTO lot (part_id, lot_number, lot_description, vendor_lot_number, po_line_id, is_active)
VALUES (sqlc.arg(part_id), sqlc.arg(lot_number), sqlc.arg(lot_description), sqlc.narg(vendor_lot_number)::text,
        sqlc.narg(po_line_id), TRUE)
RETURNING id;

-- name: SetLotNumber :exec
UPDATE lot SET lot_number = sqlc.arg(lot_number) WHERE id = sqlc.arg(id);

-- name: ListActiveLots :many
SELECT id, lot_number, COALESCE(vendor_lot_number, '') AS vendor_lot
FROM lot WHERE part_id = sqlc.arg(part_id) AND is_active = TRUE
ORDER BY created_at DESC, id DESC;

-- name: CountActivePartLot :one
-- Whether the lot is an active lot of the part (the build/adjustment forged-selection guard).
SELECT COUNT(*) FROM lot WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id) AND is_active = TRUE;

-- name: CountPartLot :one
-- Ownership only: a test record may reference a since-retired lot.
SELECT COUNT(*) FROM lot WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: CountPartBuild :one
SELECT COUNT(*) FROM build WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: CreateGenealogyEdge :exec
-- Lot→lot edge; unit endpoints are written by the record/unit flows.
INSERT INTO genealogy (parent_lot_id, child_lot_id, qty_consumed)
VALUES (sqlc.arg(parent_lot_id), sqlc.arg(child_lot_id), sqlc.arg(qty_consumed));

-- name: ListLots :many
-- Newest first. A NULL part_id lists every part's lots; a NULL n means no limit.
SELECT l.id, l.lot_number, COALESCE(l.vendor_lot_number, '') AS vendor_lot, l.part_id,
       COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       l.lot_description, COALESCE(l.notes, '') AS notes, l.created_at, l.is_active
FROM lot l JOIN part p ON p.id = l.part_id
WHERE (sqlc.narg(part_id)::int IS NULL OR l.part_id = sqlc.narg(part_id)::int)
ORDER BY l.created_at DESC, l.id DESC
LIMIT sqlc.narg(n)::int;

-- name: GetLot :one
SELECT l.id, l.lot_number, COALESCE(l.vendor_lot_number, '') AS vendor_lot, l.part_id,
       COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       l.lot_description, COALESCE(l.notes, '') AS notes, l.created_at, l.is_active
FROM lot l JOIN part p ON p.id = l.part_id
WHERE l.id = sqlc.arg(id);

-- name: CountPartLots :one
SELECT COUNT(*) FROM lot WHERE part_id = sqlc.arg(part_id);

-- name: UpdateLot :exec
UPDATE lot SET lot_description = sqlc.arg(lot_description), vendor_lot_number = sqlc.narg(vendor_lot_number)::text,
       notes = sqlc.narg(notes)::text
WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: AppendLotNote :exec
-- entry starts the notes when empty; otherwise separated_entry (blank line + entry) is appended.
-- Both are ::text — an untyped CONCAT argument is rejected by Postgres.
UPDATE lot SET notes = CASE WHEN COALESCE(notes, '') = '' THEN sqlc.arg(entry)::text
                            ELSE CONCAT(notes, sqlc.arg(separated_entry)::text) END
WHERE id = sqlc.arg(id);

-- ── Genealogy trace ────────────────────────────────────────────────────────────
-- One level of the walk. near_type ('lot'|'unit') and id name the near endpoint; the CASE arms
-- keep each edge column's equality index-usable (= NULL never matches the other one). The far
-- endpoint is a lot in the first branch and a unit in the second.

-- name: ListTraceAncestors :many
SELECT 'lot'::text AS node_type, l.id, l.lot_number AS number, COALESCE(l.vendor_lot_number, '') AS vendor_lot,
       COALESCE(l.notes, '') AS notes, (l.po_line_id IS NOT NULL)::boolean AS is_vendor_lot,
       p.id AS part_id, COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       g.qty_consumed AS qty
FROM genealogy g
JOIN lot l ON l.id = g.parent_lot_id
JOIN part p ON p.id = l.part_id
WHERE g.child_lot_id = CASE WHEN sqlc.arg(near_type)::text = 'lot' THEN sqlc.arg(id)::int END
   OR g.child_unit_id = CASE WHEN sqlc.arg(near_type)::text = 'unit' THEN sqlc.arg(id)::int END
UNION ALL
SELECT 'unit'::text, u.id, u.serial_number, ''::text, ''::text, FALSE,
       p.id, COALESCE(p.part_number, ''), COALESCE(p.description, ''), g.qty_consumed
FROM genealogy g
JOIN unit u ON u.id = g.parent_unit_id
JOIN part p ON p.id = u.part_id
WHERE g.child_lot_id = CASE WHEN sqlc.arg(near_type)::text = 'lot' THEN sqlc.arg(id)::int END
   OR g.child_unit_id = CASE WHEN sqlc.arg(near_type)::text = 'unit' THEN sqlc.arg(id)::int END
ORDER BY 1, 2;

-- name: ListLotIDsWithSources :many
-- Which of the given lots (comma-separated ids) have at least one source (parent) edge.
SELECT DISTINCT child_lot_id::int AS id FROM genealogy
WHERE child_lot_id = ANY(string_to_array(sqlc.arg(ids)::text, ',')::int[]);

-- name: ListTraceDescendants :many
SELECT 'lot'::text AS node_type, l.id, l.lot_number AS number, COALESCE(l.vendor_lot_number, '') AS vendor_lot,
       COALESCE(l.notes, '') AS notes, (l.po_line_id IS NOT NULL)::boolean AS is_vendor_lot,
       p.id AS part_id, COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       g.qty_consumed AS qty
FROM genealogy g
JOIN lot l ON l.id = g.child_lot_id
JOIN part p ON p.id = l.part_id
WHERE g.parent_lot_id = CASE WHEN sqlc.arg(near_type)::text = 'lot' THEN sqlc.arg(id)::int END
   OR g.parent_unit_id = CASE WHEN sqlc.arg(near_type)::text = 'unit' THEN sqlc.arg(id)::int END
UNION ALL
SELECT 'unit'::text, u.id, u.serial_number, ''::text, ''::text, FALSE,
       p.id, COALESCE(p.part_number, ''), COALESCE(p.description, ''), g.qty_consumed
FROM genealogy g
JOIN unit u ON u.id = g.child_unit_id
JOIN part p ON p.id = u.part_id
WHERE g.parent_lot_id = CASE WHEN sqlc.arg(near_type)::text = 'lot' THEN sqlc.arg(id)::int END
   OR g.parent_unit_id = CASE WHEN sqlc.arg(near_type)::text = 'unit' THEN sqlc.arg(id)::int END
ORDER BY 1, 2;

-- ── Units ──────────────────────────────────────────────────────────────────────

-- name: ListPartUnits :many
SELECT u.id, u.serial_number, u.part_id, COALESCE(p.part_number, '') AS part_number,
       COALESCE(p.description, '') AS part_description, u.lot_id, COALESCE(l.lot_number, '') AS lot_number,
       u.build_id, u.is_active, u.created_at, u.source
FROM unit u
JOIN part p ON p.id = u.part_id
LEFT JOIN lot l ON l.id = u.lot_id
WHERE u.part_id = sqlc.arg(part_id) ORDER BY u.created_at DESC, u.id DESC;

-- name: ListRecentPartUnits :many
SELECT u.id, u.serial_number, u.part_id, COALESCE(p.part_number, '') AS part_number,
       COALESCE(p.description, '') AS part_description, u.lot_id, COALESCE(l.lot_number, '') AS lot_number,
       u.build_id, u.is_active, u.created_at, u.source
FROM unit u
JOIN part p ON p.id = u.part_id
LEFT JOIN lot l ON l.id = u.lot_id
WHERE u.part_id = sqlc.arg(part_id) ORDER BY u.created_at DESC, u.id DESC LIMIT sqlc.arg(n)::int;

-- name: GetUnit :one
SELECT u.id, u.serial_number, u.part_id, COALESCE(p.part_number, '') AS part_number,
       COALESCE(p.description, '') AS part_description, u.lot_id, COALESCE(l.lot_number, '') AS lot_number,
       u.build_id, u.is_active, u.created_at, u.source
FROM unit u
JOIN part p ON p.id = u.part_id
LEFT JOIN lot l ON l.id = u.lot_id
WHERE u.id = sqlc.arg(id);

-- name: CountPartUnits :one
SELECT COUNT(*) FROM unit WHERE part_id = sqlc.arg(part_id);

-- name: CountLockedUnitRecords :one
-- A unit's serial is frozen once any locked form_record points at it.
SELECT COUNT(*) FROM form_record WHERE unit_id = sqlc.arg(unit_id) AND is_locked = TRUE;

-- name: CreateManualUnit :one
INSERT INTO unit (part_id, serial_number, lot_id, build_id, source)
VALUES (sqlc.arg(part_id), sqlc.arg(serial_number), sqlc.narg(lot_id), sqlc.narg(build_id), 'manual')
RETURNING id;

-- name: SetUnitActive :exec
UPDATE unit SET is_active = sqlc.arg(is_active) WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: UpdateUnit :exec
UPDATE unit SET is_active = sqlc.arg(is_active), serial_number = sqlc.arg(serial_number)
WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id);

-- name: UpsertTestUnit :one
-- The no-op DO UPDATE makes RETURNING yield the existing id; provenance is set only on insert.
INSERT INTO unit (part_id, serial_number, build_id, lot_id, source)
VALUES (sqlc.arg(part_id), sqlc.arg(serial_number), sqlc.narg(build_id), sqlc.narg(lot_id), 'test')
ON CONFLICT (part_id, serial_number) DO UPDATE SET serial_number = EXCLUDED.serial_number
RETURNING id;

-- name: GetUnitProvenance :one
SELECT lot_id, build_id FROM unit WHERE id = sqlc.arg(id);

-- ── Builds ─────────────────────────────────────────────────────────────────────

-- name: ListPartBuilds :many
SELECT id, qty, build_date FROM build WHERE part_id = sqlc.arg(part_id) ORDER BY build_date DESC, id DESC;

-- name: GetBuild :one
SELECT id, qty, build_date FROM build WHERE id = sqlc.arg(id);

-- name: ListBuildHistory :many
-- tested_count: serialized units minted for the build; manual back-filled units (#799) were never tested.
SELECT b.id, b.qty, b.build_date, b.username, COALESCE(b.note, '') AS note,
       (SELECT COUNT(*) FROM unit u WHERE u.build_id = b.id AND u.source <> 'manual')::int AS tested_count
FROM build b WHERE b.part_id = sqlc.arg(part_id) ORDER BY b.build_date DESC, b.id DESC;

-- name: ListBuildComponents :many
SELECT b.component_part_id, COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS description,
       COALESCE(p.category, '') AS category, b.qty, p.stock_on_hand, p.tracking_mode
FROM bom b JOIN part p ON b.component_part_id = p.id
WHERE b.parent_part_id = sqlc.arg(parent_part_id)
ORDER BY b.line_number;

-- name: ListBuildLines :many
-- Ascending component id: the build locks each component's part row in this order, so two builds that share
-- components can't lock them in opposite orders and deadlock (#268).
SELECT b.component_part_id, COALESCE(p.part_number, '') AS part_number, b.qty,
       COALESCE(p.category, '') AS category, p.tracking_mode
FROM bom b JOIN part p ON b.component_part_id = p.id
WHERE b.parent_part_id = sqlc.arg(parent_part_id)
ORDER BY b.component_part_id, b.line_number;

-- name: CreateBuild :one
INSERT INTO build (part_id, qty, build_date, username, note)
VALUES (sqlc.arg(part_id), sqlc.arg(qty), sqlc.arg(build_date), sqlc.arg(username), sqlc.narg(note)::text)
RETURNING id;

-- name: SetBuildOutputLot :exec
UPDATE build SET output_lot_id = sqlc.arg(output_lot_id) WHERE id = sqlc.arg(id);

-- name: LockRecord :one
-- #191: row lock on the test record a build was launched from, taken before any part row is locked.
SELECT id FROM form_record WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LinkRecordToBuild :execrows
-- #191: one conditional write, so a lock landing mid-build is honored.
UPDATE form_record SET lot_id = sqlc.narg(lot_id), build_id = sqlc.arg(build_id), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id) AND is_locked = FALSE;

-- name: ListAllUnits :many
-- Cross-part list (#362), newest first.
SELECT u.id, u.serial_number, u.part_id, COALESCE(p.part_number, '') AS part_number,
       COALESCE(p.description, '') AS part_description, u.lot_id, COALESCE(l.lot_number, '') AS lot_number,
       u.build_id, u.is_active, u.created_at, u.source
FROM unit u
JOIN part p ON p.id = u.part_id
LEFT JOIN lot l ON l.id = u.lot_id
ORDER BY u.created_at DESC, u.id DESC;

-- name: ListAllBuilds :many
-- Cross-part list (#362), newest first; output lot is NULL for builds of non-lot-tracked parts.
SELECT b.id, b.part_id, COALESCE(p.part_number, '') AS part_number, COALESCE(p.description, '') AS part_description,
       b.qty, b.build_date, b.output_lot_id, COALESCE(l.lot_number, '') AS lot_number, COALESCE(b.note, '') AS note
FROM build b
JOIN part p ON p.id = b.part_id
LEFT JOIN lot l ON l.id = b.output_lot_id
ORDER BY b.build_date DESC, b.id DESC;
