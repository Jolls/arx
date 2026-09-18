-- migrate_71_attachment_hash.sql
-- #71: add a SHA-256 hash to both attachment tables so duplicate uploads/links can be
-- detected. `hash` is CHAR(64) NULL holding a lowercase hex SHA-256 digest: the file's
-- content for a single-file LOCAL: link, or the link string itself for a directory-style
-- LOCAL: link, an http(s) URL, or an absolute/UNC path.
-- See docs/plans/71-attachment-content-hash.md.
--
-- BACKFILL: none here — SQL cannot read DOC_CONTROL_ROOT / SUPPLIER_FILES_ROOT. Existing
-- rows stay NULL until the one-off Go command is run against the same database:
--     cd arx_go && go run ./cmd/backfill_attachment_hash -apply
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. `hash` is nullable with
-- no DEFAULT, so an older binary's INSERTs (which never mention it) still succeed and an
-- older binary keeps running against the migrated DB; the mismatch banner must not fire.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, a human changes that
-- single line — nothing else in this script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each ADD guarded on COL_LENGTH; safe to re-run). Runs as a single batch
-- (no `GO`). Nothing in this script references the newly added columns, so the
-- dynamic-SQL deferral used by migrate_743/migrate_799 is not needed. Not an FK
-- promotion, so no orphan pre-check applies.

USE ArxDev;   -- SAFETY: pinned to ArxDev. A human changes this line to apply to ArxProd.

IF COL_LENGTH('dbo.part_attachment', 'hash') IS NULL
    ALTER TABLE dbo.part_attachment ADD hash CHAR(64) NULL;
-- Postgres: ALTER TABLE part_attachment ADD COLUMN IF NOT EXISTS hash CHAR(64);

IF COL_LENGTH('dbo.company_attachment', 'hash') IS NULL
    ALTER TABLE dbo.company_attachment ADD hash CHAR(64) NULL;
-- Postgres: ALTER TABLE company_attachment ADD COLUMN IF NOT EXISTS hash CHAR(64);
