-- Test-record domain (#190, #248, #249; grows with #223). sqlc generates internal/dbq/records.sql.go
-- from this file; the service is records.go.

-- name: GetRecordHeader :one
-- A record's serial, lock state and its form's part number (the paste-image guard reads this).
SELECT COALESCE(r.serial_number, '') AS serial_number, r.is_locked, pn.part_number
FROM form_record r
JOIN form f ON r.form_id = f.id
JOIN part pn ON f.part_number_id = pn.id
WHERE r.id = sqlc.arg(id);

-- ── Forms ────────────────────────────────────────────────────────────────────

-- name: ListActiveForms :many
-- The records index: every active form whose part is an active FORM-category part.
SELECT f.id, f.part_number_id, f.is_locked, f.revision, pn.part_number,
       COALESCE(pn.description, '') AS description
FROM form f
JOIN part pn ON f.part_number_id = pn.id
WHERE pn.category = 'FORM' AND pn.is_active = TRUE AND f.is_active = TRUE
ORDER BY pn.part_number ASC;

-- name: GetFormHeader :one
-- A form with its part's number/description; every page that shows a form header reads this.
SELECT f.id, f.part_number_id, f.is_locked, COALESCE(f.test_order, '') AS test_order, f.revision,
       pn.part_number, COALESCE(pn.description, '') AS description,
       COALESCE(f.record_types, '') AS record_types, COALESCE(f.instrument_types, '') AS instrument_types
FROM form f
JOIN part pn ON f.part_number_id = pn.id
WHERE f.id = sqlc.arg(id);

-- name: ListFormSteps :many
-- Every form_row of a form with all rendering fields (definition view, edit page, live fallback
-- for un-materialized record rows). Order comes from form.test_order, not from this query.
SELECT id, form_id, COALESCE(parameter, '') AS parameter, COALESCE(specification, '') AS specification,
       COALESCE(default_result, '') AS default_result, COALESCE(hide_formula, '') AS hide_formula,
       COALESCE(type, 0)::int AS type, COALESCE(spec_min, '') AS spec_min, COALESCE(spec_max, '') AS spec_max,
       COALESCE(pf_type, '') AS pf_type, archived, archive_id, revision,
       COALESCE(category, '') AS category, COALESCE(sheet_name, '') AS sheet_name,
       COALESCE(spec_units, '') AS spec_units, COALESCE(spec_nom, '') AS spec_nom,
       COALESCE(instrument_types, '') AS instrument_types, COALESCE(format, '') AS format,
       COALESCE(comment, '') AS comment, created_at, updated_at
FROM form_row
WHERE form_id = sqlc.arg(form_id);

-- name: ListFormHistoryStamps :many
-- Change timestamps of a form's steps, oldest first (the definition page's timeline dots).
SELECT changed_at, form_row_id
FROM form_row_history
WHERE form_row_id IN (SELECT id FROM form_row WHERE form_id = sqlc.arg(form_id))
ORDER BY changed_at ASC;

-- name: ListFormStepsAt :many
-- A form's steps as they stood on one day: a step with history in [day_start, day_end) shows its
-- pre-change values (changed = true), any other its current ones.
SELECT t.id,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.type, 0)            ELSE COALESCE(t.type, 0)            END)::int  AS type,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.parameter, '')      ELSE COALESCE(t.parameter, '')      END)::text AS parameter,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_nom, '')       ELSE COALESCE(t.spec_nom, '')       END)::text AS spec_nom,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_min, '')       ELSE COALESCE(t.spec_min, '')       END)::text AS spec_min,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_max, '')       ELSE COALESCE(t.spec_max, '')       END)::text AS spec_max,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.spec_units, '')     ELSE COALESCE(t.spec_units, '')     END)::text AS spec_units,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.pf_type, '')        ELSE COALESCE(t.pf_type, '')        END)::text AS pf_type,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.default_result, '') ELSE COALESCE(t.default_result, '') END)::text AS default_result,
       (CASE WHEN h.form_row_id IS NOT NULL THEN COALESCE(h.hide_formula, '')   ELSE COALESCE(t.hide_formula, '')   END)::text AS hide_formula,
       (h.form_row_id IS NOT NULL)::bool AS changed
FROM form_row t
LEFT JOIN (
    SELECT form_row_id, type, parameter, spec_nom, spec_min, spec_max,
           spec_units, pf_type, default_result, hide_formula
    FROM form_row_history
    WHERE changed_at >= sqlc.arg(day_start)::timestamptz AND changed_at < sqlc.arg(day_end)::timestamptz
) h ON h.form_row_id = t.id
WHERE t.form_id = sqlc.arg(form_id);

-- name: ListFormPartOptions :many
-- FORM-category parts that don't already have an active form (new / duplicate form PN picker).
SELECT id, part_number, COALESCE(description, '') AS description
FROM part
WHERE category = 'FORM' AND is_active = TRUE
  AND NOT EXISTS (SELECT 1 FROM form WHERE part_number_id = part.id AND is_active = TRUE)
ORDER BY part_number;

-- name: ListSourceForms :many
-- Active forms to copy steps from (new-form page).
SELECT f.id, pn.part_number, COALESCE(pn.description, '') AS description
FROM form f
JOIN part pn ON f.part_number_id = pn.id
WHERE f.is_active = TRUE
ORDER BY pn.part_number;

-- name: CountFormSteps :one
SELECT COUNT(*)::int FROM form_row WHERE form_id = sqlc.arg(form_id);

-- name: GetFormStep :one
-- One step's report header fields; the caller checks form_id belongs to the form in the URL.
SELECT id, form_id, COALESCE(parameter, '') AS parameter, COALESCE(specification, '') AS specification,
       COALESCE(spec_units, '') AS spec_units, COALESCE(format, '') AS format
FROM form_row
WHERE id = sqlc.arg(id);

-- name: GetFormStepFormat :one
-- A step's display format, scoped to its form (a step of another form is no row).
SELECT COALESCE(format, '')::text AS format
FROM form_row
WHERE id = sqlc.arg(id) AND form_id = sqlc.arg(form_id);

-- ── Records ──────────────────────────────────────────────────────────────────

-- name: GetRecord :one
SELECT id, form_id, COALESCE(part_id, 0)::int AS part_id, COALESCE(serial_number, '') AS serial_number,
       COALESCE(subject_part_number, '') AS subject_part_number,
       COALESCE(subject_pn_description, '') AS subject_pn_description,
       record_date, COALESCE(record_type, '') AS record_type, COALESCE(notes, '') AS notes,
       COALESCE(instrument_type, '') AS instrument_type, is_locked, is_approved, is_active,
       COALESCE(test_order, '') AS test_order, lot_id, build_id, unit_id
FROM form_record
WHERE id = sqlc.arg(id);

-- name: ListFormRecords :many
-- Active records of a form for the records table: numeric serials descending (a non-numeric
-- serial sorts first), newest date first within a serial.
SELECT id, COALESCE(part_id, 0)::int AS part_id, COALESCE(serial_number, '') AS serial_number,
       COALESCE(subject_part_number, '') AS subject_part_number,
       COALESCE(subject_pn_description, '') AS subject_pn_description,
       record_date, COALESCE(record_type, '') AS record_type, is_locked, is_approved, form_revision
FROM form_record
WHERE form_id = sqlc.arg(form_id) AND is_active = TRUE
ORDER BY (CASE WHEN serial_number ~ '^[0-9]+$' THEN CAST(serial_number AS INTEGER) END) DESC, record_date DESC;

-- name: ListScopedRecords :many
-- Active records across forms for the Part/Lot/Unit records tables: exactly one of the three ids is
-- non-NULL (the service picks it from a fixed scope), the others don't filter.
SELECT r.id, COALESCE(r.part_id, 0)::int AS part_id, COALESCE(r.serial_number, '') AS serial_number,
       COALESCE(r.subject_part_number, '') AS subject_part_number,
       COALESCE(r.subject_pn_description, '') AS subject_pn_description,
       r.record_date, COALESCE(r.record_type, '') AS record_type, r.is_locked, r.is_approved,
       r.form_revision, r.form_id, fp.part_number AS form_part_number,
       COALESCE(fp.description, '') AS form_description
FROM form_record r
JOIN form f ON f.id = r.form_id
JOIN part fp ON fp.id = f.part_number_id
WHERE r.is_active = TRUE
  AND (sqlc.narg(part_id)::int IS NULL OR r.part_id = sqlc.narg(part_id)::int)
  AND (sqlc.narg(lot_id)::int IS NULL OR r.lot_id = sqlc.narg(lot_id)::int)
  AND (sqlc.narg(unit_id)::int IS NULL OR r.unit_id = sqlc.narg(unit_id)::int)
ORDER BY r.record_date DESC;

-- name: ListRecordTypes :many
-- Distinct non-empty record types of the active records in one scope (filter datalists); exactly
-- one of the four ids is non-NULL.
SELECT DISTINCT COALESCE(record_type, '')::text AS record_type
FROM form_record
WHERE is_active = TRUE AND record_type <> ''
  AND (sqlc.narg(form_id)::int IS NULL OR form_id = sqlc.narg(form_id)::int)
  AND (sqlc.narg(part_id)::int IS NULL OR part_id = sqlc.narg(part_id)::int)
  AND (sqlc.narg(lot_id)::int IS NULL OR lot_id = sqlc.narg(lot_id)::int)
  AND (sqlc.narg(unit_id)::int IS NULL OR unit_id = sqlc.narg(unit_id)::int)
ORDER BY 1;

-- name: GetRecordNeighbors :one
-- Previous / next active record id (0 = none) in the records-table order; no row when the record
-- is not an active record of the form.
WITH ordered AS (
    SELECT id,
           LAG(id)  OVER (ORDER BY (CASE WHEN serial_number ~ '^[0-9]+$' THEN CAST(serial_number AS INTEGER) END) DESC, record_date DESC) AS prev_id,
           LEAD(id) OVER (ORDER BY (CASE WHEN serial_number ~ '^[0-9]+$' THEN CAST(serial_number AS INTEGER) END) DESC, record_date DESC) AS next_id
    FROM form_record
    WHERE form_id = sqlc.arg(form_id) AND is_active = TRUE
)
SELECT COALESCE(prev_id, 0)::int AS prev_id, COALESCE(next_id, 0)::int AS next_id
FROM ordered
WHERE id = sqlc.arg(record_id);

-- name: NextFormSerial :one
-- Suggested next serial: the largest all-digit serial of the form (any state) + 1, or 1.
SELECT COALESCE(MAX(CASE WHEN serial_number ~ '^[0-9]+$' THEN CAST(serial_number AS INTEGER) END) + 1, 1)::int AS next_serial
FROM form_record
WHERE form_id = sqlc.arg(form_id);

-- name: ListRecordResults :many
-- A record's materialized result rows (the frozen snapshot of each step).
SELECT id, form_record_id, form_row_id,
       COALESCE(parameter, '') AS parameter, COALESCE(specification, '') AS specification,
       COALESCE(result, '') AS result, pass_fail, COALESCE(comment, '') AS comment,
       COALESCE(spec_min, '') AS spec_min, COALESCE(spec_nom, '') AS spec_nom, COALESCE(spec_max, '') AS spec_max,
       COALESCE(spec_units, '') AS spec_units, COALESCE(pf_type, '') AS pf_type, COALESCE(format, '') AS format,
       COALESCE(type, 0)::int AS type, COALESCE(hide_formula, '') AS hide_formula,
       COALESCE(default_result, '') AS default_result, updated_at
FROM result
WHERE form_record_id = sqlc.arg(record_id);

-- name: ListRecordEvents :many
-- Lifecycle audit trail (complete / approve / unlock), oldest first.
SELECT id, form_record_id, event_type, COALESCE(username, '') AS username, event_date,
       COALESCE(comments, '') AS comments
FROM record_events
WHERE form_record_id = sqlc.arg(record_id)
ORDER BY event_date ASC, id ASC;

-- name: ListRecordEventResults :many
-- The per-completion result snapshots of a record, in event order (the handler diffs them).
SELECT rer.event_id, rer.form_row_id, COALESCE(rer.parameter, '') AS parameter,
       COALESCE(rer.specification, '') AS specification, COALESCE(rer.spec_units, '') AS spec_units,
       COALESCE(rer.result, '') AS result, rer.pass_fail, COALESCE(rer.comment, '') AS comment
FROM record_event_results rer
JOIN record_events re ON re.id = rer.event_id
WHERE re.form_record_id = sqlc.arg(record_id)
ORDER BY re.event_date ASC, re.id ASC, rer.id ASC;

-- name: GetPartTracking :one
-- The tested part's tracking mode and how many BOM lines it has as a parent (buildable if > 0).
SELECT p.tracking_mode, (SELECT COUNT(*) FROM bom b WHERE b.parent_part_id = p.id)::int AS bom_count
FROM part p
WHERE p.id = sqlc.arg(id);

-- name: ListBOMParts :many
-- Components under a part in the BOM (the new-record subject picker).
SELECT pn.id, pn.part_number, COALESCE(pn.description, '') AS description
FROM bom pl
JOIN part pn ON pl.component_part_id = pn.id
WHERE pl.parent_part_id = sqlc.arg(parent_part_id)
ORDER BY pn.description;

-- name: ListStepReportRows :many
-- Every active record's recorded result for one step, in the records-table order.
SELECT trec.id, COALESCE(trec.serial_number, '') AS serial_number,
       COALESCE(trec.subject_part_number, '') AS subject_part_number,
       COALESCE(trec.part_id, 0)::int AS part_id, trec.record_date,
       COALESCE(res.result, '') AS result, res.pass_fail, COALESCE(res.comment, '') AS comment,
       res.updated_at
FROM result res
JOIN form_record trec ON res.form_record_id = trec.id
WHERE res.form_row_id = sqlc.arg(step_id) AND trec.form_id = sqlc.arg(form_id) AND trec.is_active = TRUE
ORDER BY (CASE WHEN trec.serial_number ~ '^[0-9]+$' THEN CAST(trec.serial_number AS INTEGER) END) DESC, trec.record_date DESC;

-- ── Reports ──────────────────────────────────────────────────────────────────

-- name: ListYieldRecords :many
-- One row per active record of the form in the date range: its date and whether any result failed.
-- from_date / to_date are optional; to_date is inclusive of the whole day.
SELECT trec.record_date, COALESCE(BOOL_OR(res.pass_fail = FALSE), FALSE)::bool AS any_fail
FROM form_record trec
LEFT JOIN result res ON res.form_record_id = trec.id
WHERE trec.form_id = sqlc.arg(form_id) AND trec.is_active = TRUE
  AND (sqlc.narg(from_date)::timestamp IS NULL OR trec.record_date >= sqlc.narg(from_date)::timestamp)
  AND (sqlc.narg(to_date)::date IS NULL OR trec.record_date < (sqlc.narg(to_date)::date + 1))
GROUP BY trec.id, trec.record_date;

-- name: ListFailureModes :many
-- Each step's failure count and tested count over the form's active records in the date range,
-- most failures first. The label is the step's most recently written result-row parameter.
SELECT COALESCE((SELECT r2.parameter FROM result r2
                 WHERE r2.form_row_id = res.form_row_id
                 ORDER BY r2.id DESC LIMIT 1), '')::text AS parameter,
       COALESCE(SUM(CASE WHEN res.pass_fail = FALSE THEN 1 ELSE 0 END), 0)::int AS failure_count,
       COUNT(res.pass_fail)::int AS total_tested
FROM result res
JOIN form_record trec ON res.form_record_id = trec.id
WHERE trec.form_id = sqlc.arg(form_id) AND trec.is_active = TRUE AND res.pass_fail IS NOT NULL
  AND (sqlc.narg(from_date)::timestamp IS NULL OR trec.record_date >= sqlc.narg(from_date)::timestamp)
  AND (sqlc.narg(to_date)::date IS NULL OR trec.record_date < (sqlc.narg(to_date)::date + 1))
GROUP BY res.form_row_id
ORDER BY failure_count DESC, parameter ASC;
