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

-- ── Record writes ────────────────────────────────────────────────────────────

-- name: LockSerialAllocation :exec
-- Serializes auto serial allocation for one form until the tx ends (#369, #33).
SELECT pg_advisory_xact_lock(sqlc.arg(namespace)::int, sqlc.arg(form_id)::int);

-- name: GetPartLabel :one
-- The part number / description denormalized onto a new record as its subject.
SELECT part_number, COALESCE(description, '') AS description
FROM part
WHERE id = sqlc.arg(id);

-- name: InsertRecord :one
INSERT INTO form_record (form_id, part_id, serial_number, subject_part_number, subject_pn_description,
                         record_type, instrument_type, test_order, record_date, created_at, is_active, is_locked,
                         form_revision)
VALUES (sqlc.arg(form_id), sqlc.narg(part_id), sqlc.arg(serial_number)::text, sqlc.arg(subject_part_number)::text,
        sqlc.arg(subject_pn_description)::text, sqlc.arg(record_type)::text, sqlc.arg(instrument_type)::text,
        sqlc.arg(test_order)::text, sqlc.arg(record_date)::timestamp, CURRENT_TIMESTAMP, TRUE, FALSE,
        sqlc.arg(form_revision)::int)
RETURNING id;

-- name: DuplicateRecord :one
-- A fresh WIP re-test of a record: same form, part, serial, subject, type, instrument and step order, dated now,
-- stamped with the form's current revision (#260), unit carried over (#745). No row = no such record.
INSERT INTO form_record (form_id, part_id, serial_number, subject_part_number, subject_pn_description,
                         record_type, instrument_type, test_order, record_date, created_at, is_active, is_locked,
                         is_approved, form_revision, unit_id)
SELECT s.form_id, s.part_id, s.serial_number, s.subject_part_number, s.subject_pn_description,
       s.record_type, COALESCE(s.instrument_type, ''), s.test_order, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, TRUE, FALSE,
       FALSE, f.revision, s.unit_id
FROM form_record s
JOIN form f ON f.id = s.form_id
WHERE s.id = sqlc.arg(id)
RETURNING id;

-- name: CopyRecordResults :exec
-- Copies every result row (snapshot and recorded value) of one record onto another.
INSERT INTO result (form_record_id, form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max,
                    spec_units, pf_type, format, hide_formula, default_result, result, comment, pass_fail, updated_at)
SELECT sqlc.arg(to_record_id), s.form_row_id, s.type, s.parameter, s.specification, s.spec_min, s.spec_nom,
       s.spec_max, s.spec_units, s.pf_type, s.format, s.hide_formula, s.default_result, s.result, s.comment,
       s.pass_fail, CURRENT_TIMESTAMP
FROM result s
WHERE s.form_record_id = sqlc.arg(from_record_id);

-- name: InsertResult :exec
-- One result row materialized from a (baked) step definition; result / comment / pass_fail NULL for a fresh row.
INSERT INTO result (form_record_id, form_row_id, type, parameter, specification, spec_min, spec_nom, spec_max,
                    spec_units, pf_type, format, hide_formula, default_result, result, comment, pass_fail, updated_at)
VALUES (sqlc.arg(record_id), sqlc.arg(step_id), sqlc.arg(type)::int, sqlc.arg(parameter)::text,
        sqlc.arg(specification)::text, sqlc.arg(spec_min)::text, sqlc.arg(spec_nom)::text, sqlc.arg(spec_max)::text,
        sqlc.arg(spec_units)::text, sqlc.arg(pf_type)::text, sqlc.arg(format)::text, sqlc.arg(hide_formula)::text,
        sqlc.arg(default_result)::text, sqlc.narg(result)::text, sqlc.narg(comment)::text, sqlc.narg(pass_fail)::bool,
        CURRENT_TIMESTAMP);

-- name: RefreshResult :exec
-- Re-pulls a step's live definition into an existing snapshot row (resync) with a recomputed pass_fail.
UPDATE result
SET type = sqlc.arg(type)::int, parameter = sqlc.arg(parameter)::text, specification = sqlc.arg(specification)::text,
    spec_min = sqlc.arg(spec_min)::text, spec_nom = sqlc.arg(spec_nom)::text, spec_max = sqlc.arg(spec_max)::text,
    spec_units = sqlc.arg(spec_units)::text, pf_type = sqlc.arg(pf_type)::text, format = sqlc.arg(format)::text,
    hide_formula = sqlc.arg(hide_formula)::text, default_result = sqlc.arg(default_result)::text,
    pass_fail = sqlc.narg(pass_fail)::bool, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

-- name: UpdateResultValue :exec
UPDATE result
SET result = sqlc.arg(result)::text, comment = sqlc.arg(comment)::text, pass_fail = sqlc.narg(pass_fail)::bool,
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

-- name: ClaimRecord :execrows
-- Takes a WIP record's row lock for the rest of the tx (#191); 0 rows = locked or missing.
UPDATE form_record SET updated_at = CURRENT_TIMESTAMP WHERE id = sqlc.arg(id) AND is_locked = FALSE;

-- name: UpdateRecordAfterSave :exec
-- The record-level fields of a results save; a NULL record_date keeps the stored one.
UPDATE form_record
SET record_date = COALESCE(sqlc.narg(record_date)::timestamp, record_date), record_type = sqlc.arg(record_type)::text,
    notes = sqlc.narg(notes)::text, instrument_type = sqlc.arg(instrument_type)::text,
    lot_id = sqlc.narg(lot_id)::int, build_id = sqlc.narg(build_id)::int, unit_id = sqlc.narg(unit_id)::int,
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

-- name: ResyncRecordHeader :exec
UPDATE form_record
SET test_order = sqlc.arg(test_order)::text, form_revision = sqlc.arg(form_revision)::int, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

-- name: CompleteRecord :execrows
-- WIP → Complete; takes the record's row lock. A non-NULL form_id scopes a bulk complete to that form.
UPDATE form_record SET is_locked = TRUE, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND is_locked = FALSE
  AND (sqlc.narg(form_id)::int IS NULL OR form_id = sqlc.narg(form_id)::int);

-- name: ApproveRecord :execrows
UPDATE form_record SET is_approved = TRUE, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND is_locked = TRUE AND is_approved = FALSE;

-- name: UnlockRecord :execrows
-- Locked → WIP, clearing approval. An approved record unlocks only for a reviewer (#249); the check sits in
-- the WHERE so an approval that lands while the unlock waits on the row lock still wins.
UPDATE form_record SET is_locked = FALSE, is_approved = FALSE, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND is_locked = TRUE AND (is_approved = FALSE OR sqlc.arg(may_unlock_approved)::bool);

-- name: InsertRecordEvent :one
INSERT INTO record_events (form_record_id, event_type, username, event_date, comments)
VALUES (sqlc.arg(record_id), sqlc.arg(event_type), sqlc.arg(username)::text, CURRENT_TIMESTAMP, sqlc.narg(comments)::text)
RETURNING id;

-- name: InsertEventResult :exec
INSERT INTO record_event_results (event_id, form_row_id, parameter, specification, spec_units, result, pass_fail, comment)
VALUES (sqlc.arg(event_id), sqlc.arg(form_row_id), sqlc.arg(parameter)::text, sqlc.arg(specification)::text,
        sqlc.arg(spec_units)::text, sqlc.arg(result)::text, sqlc.narg(pass_fail)::bool, sqlc.arg(comment)::text);

-- ── Form writes ──────────────────────────────────────────────────────────────

-- name: SetAuditUser :exec
-- Transaction-local acting username that trg_form_row_history reads via current_setting('arx.username').
SELECT set_config('arx.username', sqlc.arg(username)::text, true);

-- name: LockForm :execrows
-- Release: lock and bump the revision (#260).
UPDATE form SET is_locked = TRUE, revision = revision + 1 WHERE id = sqlc.arg(id) AND is_locked = FALSE;

-- name: UnlockForm :execrows
UPDATE form SET is_locked = FALSE WHERE id = sqlc.arg(id) AND is_locked = TRUE;

-- name: InsertFormEvent :exec
INSERT INTO form_events (form_id, event_type, username, event_date, comments)
VALUES (sqlc.arg(form_id), sqlc.arg(event_type), sqlc.arg(username)::text, CURRENT_TIMESTAMP, sqlc.narg(comments)::text);

-- name: UpdateStep :exec
UPDATE form_row
SET type = sqlc.arg(type)::int, parameter = sqlc.arg(parameter)::text, specification = sqlc.arg(specification)::text,
    spec_nom = sqlc.narg(spec_nom)::text, spec_min = sqlc.narg(spec_min)::text, spec_max = sqlc.narg(spec_max)::text,
    spec_units = sqlc.narg(spec_units)::text, pf_type = sqlc.narg(pf_type)::text,
    default_result = sqlc.narg(default_result)::text, hide_formula = sqlc.narg(hide_formula)::text,
    category = sqlc.narg(category)::text, sheet_name = sqlc.narg(sheet_name)::text,
    instrument_types = sqlc.narg(instrument_types)::text, format = sqlc.narg(format)::text,
    comment = sqlc.narg(comment)::text, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND form_id = sqlc.arg(form_id);

-- name: InsertStep :one
INSERT INTO form_row (form_id, type, parameter, specification, spec_nom, spec_min, spec_max, spec_units, pf_type,
                      default_result, hide_formula, category, sheet_name, instrument_types, format, comment,
                      created_at, updated_at)
VALUES (sqlc.arg(form_id), sqlc.arg(type)::int, sqlc.arg(parameter)::text, sqlc.narg(specification)::text,
        sqlc.narg(spec_nom)::text, sqlc.narg(spec_min)::text, sqlc.narg(spec_max)::text, sqlc.narg(spec_units)::text,
        sqlc.narg(pf_type)::text, sqlc.narg(default_result)::text, sqlc.narg(hide_formula)::text,
        sqlc.narg(category)::text, sqlc.narg(sheet_name)::text, sqlc.narg(instrument_types)::text,
        sqlc.narg(format)::text, sqlc.narg(comment)::text, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING id;

-- name: CopyStep :one
-- Copies one step of from_form_id into another form (no row when the step isn't one of from_form_id's). archived
-- is not copied (a copy starts active); a NULL type / parameter / specification becomes 0 / ''.
INSERT INTO form_row (form_id, type, parameter, specification, spec_nom, spec_min, spec_max, spec_units, pf_type,
                      default_result, hide_formula, category, sheet_name, instrument_types, format, comment,
                      archive_id, revision)
SELECT sqlc.arg(to_form_id), COALESCE(s.type, 0), COALESCE(s.parameter, ''), COALESCE(s.specification, ''),
       s.spec_nom, s.spec_min, s.spec_max, s.spec_units, s.pf_type, s.default_result, s.hide_formula, s.category,
       s.sheet_name, s.instrument_types, s.format, s.comment, s.archive_id, s.revision
FROM form_row s
WHERE s.id = sqlc.arg(step_id) AND s.form_id = sqlc.arg(from_form_id)
RETURNING id;

-- name: SetStepArchived :exec
UPDATE form_row SET archived = sqlc.arg(archived), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND form_id = sqlc.arg(form_id);

-- name: SetFormTestOrder :exec
UPDATE form SET test_order = sqlc.arg(test_order)::text WHERE id = sqlc.arg(id);

-- name: SetFormTypes :exec
UPDATE form SET record_types = sqlc.narg(record_types)::text, instrument_types = sqlc.narg(instrument_types)::text
WHERE id = sqlc.arg(id);

-- name: IsFormPart :one
-- Whether a part may own a new form: an active FORM-category part.
SELECT EXISTS (SELECT 1 FROM part WHERE id = sqlc.arg(id) AND category = 'FORM' AND is_active = TRUE)::bool;

-- name: InsertForm :one
-- A new active, unlocked form with an empty step order. Record / instrument types come from the source form
-- (NULL when there is none).
INSERT INTO form (part_number_id, is_active, is_locked, test_order, record_types, instrument_types)
VALUES (sqlc.arg(part_number_id), TRUE, FALSE, '',
        (SELECT s.record_types FROM form s WHERE s.id = sqlc.arg(source_id)),
        (SELECT s.instrument_types FROM form s WHERE s.id = sqlc.arg(source_id)))
RETURNING id;

-- ── Named queries (#250) ─────────────────────────────────────────────────────
-- The admin-authored SQL itself runs raw in arx_go's execQuery; only the named_queries table is here.

-- name: ListActiveNamedQueries :many
SELECT name, COALESCE(description, '') AS description, COALESCE(params, '') AS params, result_type
FROM named_queries
WHERE is_active = TRUE
ORDER BY name;

-- name: ListNamedQueries :many
SELECT id, name, COALESCE(description, '') AS description, sql, COALESCE(params, '') AS params, result_type,
       is_active, updated_at
FROM named_queries
ORDER BY name;

-- name: GetActiveNamedQuery :one
SELECT sql, result_type FROM named_queries WHERE name = sqlc.arg(name) AND is_active = TRUE;

-- name: InsertNamedQuery :one
INSERT INTO named_queries (name, description, sql, params, result_type, is_active)
VALUES (sqlc.arg(name), sqlc.arg(description)::text, sqlc.arg(sql), sqlc.arg(params)::text, sqlc.arg(result_type),
        sqlc.arg(is_active))
RETURNING id;

-- name: UpdateNamedQuery :execrows
UPDATE named_queries
SET name = sqlc.arg(name), description = sqlc.arg(description)::text, sql = sqlc.arg(sql),
    params = sqlc.arg(params)::text, result_type = sqlc.arg(result_type), is_active = sqlc.arg(is_active),
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id);

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
