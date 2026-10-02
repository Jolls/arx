-- Reports domain (#190, #246, #225): the Reports dashboard/report queries and the Settings ->
-- Utilities data-integrity checks. sqlc generates internal/dbq/reports.sql.go from this file;
-- the service is reports.go. Read-only throughout.

-- ── Dashboard cards ────────────────────────────────────────────────────────────

-- name: CountOpenPOs :one
SELECT COUNT(*)::int FROM purchase_order WHERE status = 'open';

-- name: CountPOsReceivedThisMonth :one
SELECT COUNT(DISTINCT po_id)::int FROM po_line
WHERE date_received >= date_trunc('month', sqlc.arg(today)::date)::date;

-- name: ListTopFailureModes :many
-- Failing steps across all forms, all-time, most-failed first.
SELECT f.id AS form_id, pn.part_number,
       COALESCE((SELECT r2.parameter FROM result r2
                 WHERE r2.form_row_id = res.form_row_id
                 ORDER BY r2.id DESC LIMIT 1), '')::text AS parameter,
       SUM(CASE WHEN res.pass_fail = FALSE THEN 1 ELSE 0 END)::int AS failure_count
FROM result res
JOIN form_record trec ON res.form_record_id = trec.id
JOIN form f ON trec.form_id = f.id
JOIN part pn ON f.part_number_id = pn.id
WHERE trec.is_active = TRUE AND res.pass_fail IS NOT NULL
GROUP BY f.id, pn.part_number, res.form_row_id
HAVING SUM(CASE WHEN res.pass_fail = FALSE THEN 1 ELSE 0 END) > 0
ORDER BY failure_count DESC LIMIT sqlc.arg(row_limit)::int;

-- name: ListRecordFailFlags :many
-- One row per active record: 1 when any of its results failed. The per-form yield is tallied in Go.
SELECT trec.form_id, pn.part_number,
       MAX(CASE WHEN res.pass_fail = FALSE THEN 1 ELSE 0 END)::int AS any_fail
FROM form_record trec
JOIN form f ON trec.form_id = f.id
JOIN part pn ON f.part_number_id = pn.id
LEFT JOIN result res ON res.form_record_id = trec.id
WHERE trec.is_active = TRUE
GROUP BY trec.id, trec.form_id, pn.part_number;

-- name: ListStaleWIPRecords :many
-- Unlocked records at least stale_days old, oldest first.
SELECT trec.id, trec.form_id, pn.part_number, trec.created_at
FROM form_record trec
JOIN form f ON trec.form_id = f.id
JOIN part pn ON f.part_number_id = pn.id
WHERE trec.is_active = TRUE AND trec.is_locked = FALSE
  AND trec.created_at <= CURRENT_TIMESTAMP - make_interval(days => sqlc.arg(stale_days)::int)
ORDER BY trec.created_at ASC LIMIT sqlc.arg(row_limit)::int;

-- name: ListPendingApprovalPOs :many
-- submitted_at is the PO's latest 'submitted' approval event; has_submitted is false (and
-- submitted_at the epoch) when there is none. sqlc can't type a nullable aggregate/lateral column.
SELECT po.number, COALESCE(h.changed_at, 'epoch'::timestamptz)::timestamptz AS submitted_at,
       (h.changed_at IS NOT NULL)::boolean AS has_submitted
FROM purchase_order po
LEFT JOIN LATERAL (SELECT x.changed_at FROM purchase_order_history x
                   WHERE x.po_id = po.id AND x.event_type = 'approval' AND x.action = 'submitted'
                   ORDER BY x.changed_at DESC LIMIT 1) h ON TRUE
WHERE po.approval_status = 'pending'
ORDER BY h.changed_at ASC LIMIT sqlc.arg(row_limit)::int;

-- name: ListBelowReorderParts :many
SELECT id AS part_id, part_number, stock_on_hand::numeric AS stock_on_hand, reorder_min::numeric AS reorder_min
FROM part
WHERE reorder_min IS NOT NULL AND stock_on_hand < reorder_min
ORDER BY (stock_on_hand - reorder_min) ASC LIMIT sqlc.arg(row_limit)::int;

-- name: ListRecentModifiedParts :many
SELECT id, part_number, COALESCE(description, '') AS description, modified_date
FROM part WHERE modified_date IS NOT NULL ORDER BY modified_date DESC LIMIT sqlc.arg(row_limit)::int;

-- name: ListRecentPOEvents :many
SELECT h.po_id, po.number, h.event_type, COALESCE(h.to_status, '') AS to_status,
       COALESCE(h.action, '') AS action, h.changed_at
FROM purchase_order_history h JOIN purchase_order po ON h.po_id = po.id
ORDER BY h.changed_at DESC LIMIT sqlc.arg(row_limit)::int;

-- ── Spend / on-time / cycle time ───────────────────────────────────────────────
-- date_from is inclusive and date_to exclusive; NULL leaves that side open.

-- name: SpendBySupplier :many
SELECT COALESCE(po.supplier_name, '') AS supplier_name,
       COALESCE(SUM(pol.qty * pol.unit_cost), 0)::numeric AS total_spend
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE (sqlc.narg(date_from)::date IS NULL OR po.date_ordered >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date   IS NULL OR po.date_ordered <  sqlc.narg(date_to)::date)
GROUP BY po.supplier_name
ORDER BY total_spend DESC;

-- name: SpendByPart :many
-- Lines with no part_id (freeform) are grouped by their part_number_snapshot so the spend stays counted.
SELECT COALESCE(p.part_number, pol.part_number_snapshot, '')::text AS part_number,
       COALESCE(p.description, '') AS description,
       COALESCE(SUM(pol.qty * pol.unit_cost), 0)::numeric AS total_spend
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
LEFT JOIN part p ON pol.part_id = p.id
WHERE (sqlc.narg(date_from)::date IS NULL OR po.date_ordered >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date   IS NULL OR po.date_ordered <  sqlc.narg(date_to)::date)
GROUP BY COALESCE(CAST(pol.part_id AS VARCHAR(20)), CONCAT('snap:', pol.part_number_snapshot)),
         p.part_number, p.description, pol.part_number_snapshot
ORDER BY total_spend DESC;

-- name: OnTimeDeliveryBySupplier :many
-- Only lines with a quoted lead time and a receipt date on a dated PO are evaluated.
SELECT COALESCE(po.supplier_name, '') AS supplier_name,
       COUNT(*)::int AS total_lines,
       SUM(CASE WHEN pol.date_received <= po.date_ordered + pol.lead_time_days THEN 1 ELSE 0 END)::int AS on_time_lines,
       COALESCE(AVG(CAST(pol.date_received - (po.date_ordered + pol.lead_time_days) AS DOUBLE PRECISION)), 0)::float8 AS avg_days_late
FROM po_line pol
JOIN purchase_order po ON pol.po_id = po.id
WHERE pol.lead_time_days IS NOT NULL
  AND pol.date_received IS NOT NULL
  AND po.date_ordered IS NOT NULL
  AND (sqlc.narg(date_from)::date IS NULL OR po.date_ordered >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date   IS NULL OR po.date_ordered <  sqlc.narg(date_to)::date)
GROUP BY po.supplier_name;

-- name: POCycleTimeByStage :many
-- The window filters entered_at in the outer query, after LEAD() has paired each stage with its
-- exit over the PO's full history (filtering inside the CTE would mis-pair stages near the edge).
WITH stage_durations AS (
    SELECT poh.po_id,
           poh.to_status AS stage,
           poh.changed_at AS entered_at,
           LEAD(poh.changed_at) OVER (PARTITION BY poh.po_id ORDER BY poh.changed_at, poh.id) AS exited_at
    FROM purchase_order_history poh
    WHERE poh.event_type = 'status'
      AND poh.to_status IN ('draft','open','sent','partially_received','closed')
)
SELECT stage::text AS stage,
       COUNT(*)::int AS po_count,
       COALESCE(AVG(CAST(EXTRACT(EPOCH FROM (date_trunc('hour', exited_at AT TIME ZONE 'UTC') - date_trunc('hour', entered_at AT TIME ZONE 'UTC'))) AS DOUBLE PRECISION) / 3600.0 / 24.0), 0)::float8 AS avg_days
FROM stage_durations
WHERE exited_at IS NOT NULL
  AND (sqlc.narg(entered_from)::timestamptz IS NULL OR entered_at >= sqlc.narg(entered_from)::timestamptz)
  AND (sqlc.narg(entered_to)::timestamptz   IS NULL OR entered_at <  sqlc.narg(entered_to)::timestamptz)
GROUP BY stage
ORDER BY CASE stage
    WHEN 'draft' THEN 1
    WHEN 'open' THEN 2
    WHEN 'sent' THEN 3
    WHEN 'partially_received' THEN 4
    WHEN 'closed' THEN 5
END;

-- ── Data-quality gaps ──────────────────────────────────────────────────────────

-- name: ListPartsNoAttachments :many
-- part.attachment_count is trigger-maintained, so it is read directly.
SELECT part_number, COALESCE(description, '') AS description, COALESCE(category, '') AS category
FROM part
WHERE is_active = TRUE AND category IN ('BUY','ASM','DWG') AND attachment_count = 0
ORDER BY part_number ASC;

-- name: ListPartsMissingDefaultSupplier :many
SELECT part_number, COALESCE(description, '') AS description, COALESCE(category, '') AS category
FROM part
WHERE is_active = TRUE AND category = 'BUY' AND default_supplier_id IS NULL
ORDER BY part_number ASC;

-- name: ListPartsStaleRollup :many
-- Never rolled up: NULL-only, no age threshold.
SELECT part_number, COALESCE(description, '') AS description, COALESCE(category, '') AS category
FROM part
WHERE is_active = TRUE AND (last_rollup_cost IS NULL OR last_rollup_at IS NULL)
ORDER BY part_number ASC;

-- name: ListActiveFormOptions :many
SELECT f.id, pn.part_number, COALESCE(pn.description, '') AS description
FROM form f
JOIN part pn ON f.part_number_id = pn.id
WHERE pn.category = 'FORM' AND pn.is_active = TRUE AND f.is_active = TRUE
ORDER BY pn.part_number ASC;

-- ── Utilities (Settings -> Utilities data-integrity checks) ─────────────────────

-- name: ListPartAttachmentLinks :many
SELECT COALESCE(f.file_name, '') AS link, p.id, p.part_number AS label
FROM part_attachment f JOIN part p ON f.part_id = p.id
WHERE f.is_active = TRUE;

-- name: ListCompanyAttachmentLinks :many
SELECT a.file_path AS link, c.id, c.name AS label
FROM company_attachment a JOIN company c ON a.supplier_id = c.id
WHERE a.is_active = TRUE;

-- name: ListOrphanDefaultSuppliers :many
-- The three orphan checks are enforced FKs today (#735), so they return nothing on a healthy schema.
SELECT p.id, p.part_number, p.default_supplier_id::int AS value
FROM part p
WHERE p.default_supplier_id > 0
  AND NOT EXISTS (SELECT 1 FROM company t WHERE t.id = p.default_supplier_id);

-- name: ListOrphanPrices :many
SELECT p.id, p.part_number, p.price_id::int AS value
FROM part p
WHERE p.price_id > 0
  AND NOT EXISTS (SELECT 1 FROM price t WHERE t.id = p.price_id);

-- name: ListOrphanPrimaryAttachments :many
SELECT p.id, p.part_number, p.primary_attachment_id::int AS value
FROM part p
WHERE p.primary_attachment_id > 0
  AND NOT EXISTS (SELECT 1 FROM part_attachment t WHERE t.id = p.primary_attachment_id);

-- name: ListPartsWithDeletedPrimary :many
SELECT p.id, p.part_number AS label
FROM part p JOIN part_attachment f ON f.id = p.primary_attachment_id
WHERE p.primary_attachment_id > 0 AND f.is_active = FALSE;

-- name: ListCompaniesWithDeletedPrimary :many
SELECT c.id, c.name AS label
FROM company c JOIN company_attachment a ON a.supplier_attachment_id = c.primary_attachment_id
WHERE c.primary_attachment_id > 0 AND a.is_active = FALSE;

-- name: ListPOActiveStates :many
-- status and is_active are nullable on legacy rows: NULL reads as '' / FALSE rather than
-- aborting the whole check.
SELECT id, number, COALESCE(status, '') AS status, COALESCE(is_active, FALSE) AS is_active
FROM purchase_order;
