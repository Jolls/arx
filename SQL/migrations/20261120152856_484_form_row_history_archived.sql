-- Record a step's archived flag in its history (#484): form_row_history.archived holds form_row.archived
-- from before each update, so the form definition history view can show a step as active or archived as
-- it was on the selected day. NULL = not recorded.
--
-- Backfill (an inference): only two app writes update form_row — UpdateStep (exactly the 15 snapshot
-- columns, skipped when nothing changed) and SetStepArchived (archived only). A history row whose
-- snapshot equals the step's next state (the next history row, or the live row for the newest) is
-- therefore an archive toggle — except rows from before archiving existed (#403, v0.5.35, 2026-06-23) and
-- rows imported from the VBA-era history (changed_by 'VBA_MIGRATION'), which other column updates or the
-- import itself could have written. Walking each step's rows newest → oldest from the live flag, flipping at
-- each toggle, gives the value before each update. Steps start active, so a step whose walk ends archived
-- has inconsistent history: it keeps NULL (the view falls back to the current flag). Only NULLs are filled.
-- archive_id / revision / granularity aren't in history and are out of scope.
--
-- Not breaking (no schema_version bump): additive nullable column; an older binary ignores it.
-- Idempotent (ADD COLUMN IF NOT EXISTS, CREATE OR REPLACE, backfill fills NULLs only).

-- +goose Up
-- +goose StatementBegin

ALTER TABLE form_row_history ADD COLUMN IF NOT EXISTS archived BOOLEAN;

CREATE OR REPLACE FUNCTION trg_form_row_history() RETURNS trigger AS $$
BEGIN
    INSERT INTO form_row_history
      (form_row_id, changed_at, changed_by,
       type, parameter, specification, spec_units,
       spec_min, spec_max, spec_nom, default_result,
       hide_formula, pf_type,
       instrument_types, format, comment, category, sheet_name, archived)
    SELECT
      id, CURRENT_TIMESTAMP,
      COALESCE(current_setting('arx.username', true), ''),
      type, parameter, specification, spec_units,
      spec_min, spec_max, spec_nom, default_result,
      hide_formula, pf_type,
      instrument_types, format, comment, category, sheet_name, archived
    FROM oldtab;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Each step's states in order: its history snapshots by (changed_at, id), then its live row last.
WITH s AS (
    SELECT id, form_row_id, changed_at AS k1, id AS k2,
           changed_at >= '2026-06-23' AND changed_by <> 'VBA_MIGRATION' AS can_toggle,
           type, parameter, specification, spec_units, spec_min, spec_max, spec_nom, default_result,
           hide_formula, pf_type, instrument_types, format, comment, category, sheet_name
    FROM form_row_history
    WHERE form_row_id IN (SELECT id FROM form_row)
    UNION ALL
    SELECT NULL, id, 'infinity', 0, FALSE,
           type, parameter, specification, spec_units, spec_min, spec_max, spec_nom, default_result,
           hide_formula, pf_type, instrument_types, format, comment, category, sheet_name
    FROM form_row
    WHERE id IN (SELECT form_row_id FROM form_row_history)
), r AS (
    SELECT s.*, ROW_NUMBER() OVER (PARTITION BY form_row_id ORDER BY k1, k2) AS rn FROM s
), w AS (
    -- toggle = the update after this snapshot changed none of the 15 columns (NULL-safe).
    SELECT a.id, a.form_row_id, a.rn, t.archived AS live,
           a.can_toggle AND ROW(a.type, a.parameter, a.specification, a.spec_units, a.spec_min, a.spec_max, a.spec_nom,
               a.default_result, a.hide_formula, a.pf_type, a.instrument_types, a.format, a.comment,
               a.category, a.sheet_name)
           IS NOT DISTINCT FROM
           ROW(b.type, b.parameter, b.specification, b.spec_units, b.spec_min, b.spec_max, b.spec_nom,
               b.default_result, b.hide_formula, b.pf_type, b.instrument_types, b.format, b.comment,
               b.category, b.sheet_name) AS toggle
    FROM r a
    JOIN r b ON b.form_row_id = a.form_row_id AND b.rn = a.rn + 1
    JOIN form_row t ON t.id = a.form_row_id
    WHERE a.id IS NOT NULL
), v AS (
    -- Value before each update: the live flag, flipped once per toggle at or after this row.
    SELECT id, form_row_id, rn,
           (live::int + SUM(toggle::int) OVER (PARTITION BY form_row_id ORDER BY rn DESC)) % 2 = 1 AS before
    FROM w
)
UPDATE form_row_history h
SET archived = v.before
FROM v
WHERE h.id = v.id
  AND h.archived IS NULL
  AND NOT EXISTS (SELECT 1 FROM v o WHERE o.form_row_id = v.form_row_id AND o.rn = 1 AND o.before);

-- +goose StatementEnd
