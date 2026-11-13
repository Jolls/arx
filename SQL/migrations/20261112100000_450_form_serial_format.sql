-- Per-form serial-number format (#450): form.serial_prefix + form.serial_pad drive the suggested
-- serial on a new test record (prefix + zero-padded sequence, e.g. SK-0042). Empty prefix and
-- pad 0 keep the old bare-integer suggestion.
--
-- Not breaking (no schema_version bump): additive columns with defaults; an older binary ignores
-- them. Idempotent (ADD COLUMN IF NOT EXISTS).

-- +goose Up
-- +goose StatementBegin

ALTER TABLE form ADD COLUMN IF NOT EXISTS serial_prefix VARCHAR(50) NOT NULL DEFAULT '';
ALTER TABLE form ADD COLUMN IF NOT EXISTS serial_pad    INTEGER     NOT NULL DEFAULT 0
  CONSTRAINT CK_form_serial_pad CHECK (serial_pad BETWEEN 0 AND 20);

-- +goose StatementEnd
