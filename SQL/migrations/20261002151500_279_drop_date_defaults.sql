-- Drop DEFAULT CURRENT_DATE from the five date-only business columns (#279, follow-up to #265).
-- The default records the database's zone date (UTC on Azure), which is a day off for an
-- evening Pacific write; every writer already supplies the user's local day explicitly, so the
-- default was only a silent wrong-day fallback. build.build_date and inventory_transaction.txn_date
-- are NOT NULL, so a write that omits them now fails loudly; part.created_date/modified_date and
-- price.effective_date are nullable and record NULL rather than a wrong date.
--
-- Metadata only: no row is read or rewritten. Not breaking (no schema_version bump): every
-- INSERT in the app and the load tool names these columns.
--
-- Idempotent (DROP DEFAULT on a column with no default is a no-op). Applied by the migrate
-- runner (arx_go/cmd/migrate, #91), one transaction per file.

-- +goose Up
-- +goose StatementBegin

ALTER TABLE build                 ALTER COLUMN build_date     DROP DEFAULT;
ALTER TABLE inventory_transaction ALTER COLUMN txn_date       DROP DEFAULT;
ALTER TABLE part                  ALTER COLUMN created_date   DROP DEFAULT;
ALTER TABLE part                  ALTER COLUMN modified_date  DROP DEFAULT;
ALTER TABLE price                 ALTER COLUMN effective_date DROP DEFAULT;

-- +goose StatementEnd
