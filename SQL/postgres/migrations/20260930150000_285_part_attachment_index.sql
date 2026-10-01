-- Index part_attachment for the per-part thumbnail subquery in ListParts / GetPartBasic (#285).
-- Without it the /parts grid seq-scans part_attachment once per part (~580 ms at 2,278 parts).
-- Partial on is_active: every thumbnail/attachment lookup filters to active rows.
--
-- Idempotent. Applied by the migrate runner (arx_go/cmd/migrate, #91), one transaction per file.

-- +goose Up
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS IX_part_attachment_part ON part_attachment (part_id, category) WHERE is_active;

-- +goose StatementEnd
