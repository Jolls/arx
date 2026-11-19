-- Evaluated spec bounds on result rows (#468): spec_min/spec_nom/spec_max may now hold "=" math and
-- query: directives, so each save stores what they evaluated to next to pass_fail. View, print and the
-- lock snapshot render these instead of re-running queries; editing a record re-evaluates them.
-- NULL = not evaluated yet (rows saved before this change, or never saved).
--
-- Not breaking (no schema_version bump): additive nullable columns; an older binary ignores them.
-- Idempotent (ADD COLUMN IF NOT EXISTS).

-- +goose Up
-- +goose StatementBegin

ALTER TABLE result ADD COLUMN IF NOT EXISTS eval_min VARCHAR(255);
ALTER TABLE result ADD COLUMN IF NOT EXISTS eval_nom VARCHAR(255);
ALTER TABLE result ADD COLUMN IF NOT EXISTS eval_max VARCHAR(255);

-- +goose StatementEnd
