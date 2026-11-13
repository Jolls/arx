-- Recurring-record due dates (#447): form.recurrence_days is the form's default interval (NULL = not
-- recurring); form_record.next_due_date is that record's own due date, prefilled at creation from the
-- interval and editable until the record locks. A unit's current due date comes from its latest locked
-- record per form.
--
-- Not breaking (no schema_version bump): additive nullable columns; an older binary ignores them.
-- Idempotent (ADD COLUMN IF NOT EXISTS).

-- +goose Up
-- +goose StatementBegin

ALTER TABLE form ADD COLUMN IF NOT EXISTS recurrence_days INTEGER
  CONSTRAINT CK_form_recurrence_days CHECK (recurrence_days > 0);
ALTER TABLE form_record ADD COLUMN IF NOT EXISTS next_due_date DATE;

-- +goose StatementEnd
