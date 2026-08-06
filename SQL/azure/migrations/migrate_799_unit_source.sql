-- migrate_799_unit_source.sql
-- #799: allow a unit to be created without a test record (a pre-existing/legacy serial
-- that will never be tested). Drops CK_unit_provenance (which required lot_id OR
-- build_id) and adds `unit.source` (test|manual) so app code can tell a manually
-- back-filled unit apart from one minted at test time — see
-- docs/plans/799-create-unit-without-test-record.md.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. An older binary never
-- writes `source` (the DEFAULT 'test' covers its INSERTs) and never inserts a
-- both-provenance-NULL row, so the dropped CHECK only ever loosens what's accepted; the
-- mismatch banner must not fire (mirrors slice 3 / #740, slice 6 / #743).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres
-- equivalent follows in comments.
--
-- Runs as a single batch (no `GO`): the CHECK in step 3 references the column added in
-- step 2, and same-batch name resolution fails with "Invalid column name" against
-- pre-batch metadata — deferred via dynamic SQL (`EXEC(N'...')`), the migrate_743 pattern.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Drop the provenance CHECK — a manually-entered unit legitimately has neither a
--    lot nor a build.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.CK_unit_provenance', 'C') IS NOT NULL
    ALTER TABLE dbo.unit DROP CONSTRAINT CK_unit_provenance;
-- Postgres: ALTER TABLE unit DROP CONSTRAINT IF EXISTS ck_unit_provenance;

-- ------------------------------------------------------------------
-- 2. Add source NOT NULL DEFAULT 'test' — existing rows all came from the test path,
--    so the default IS the backfill; no separate UPDATE needed.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.unit', 'source') IS NULL
    ALTER TABLE dbo.unit ADD source VARCHAR(10) NOT NULL CONSTRAINT DF_unit_source DEFAULT 'test';
-- Postgres: ALTER TABLE unit ADD COLUMN IF NOT EXISTS source VARCHAR(10) NOT NULL DEFAULT 'test';

-- ------------------------------------------------------------------
-- 3. Add the CHECK constraint (deferred via dynamic SQL — see header note).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.CK_unit_source', 'C') IS NULL
    EXEC(N'ALTER TABLE dbo.unit ADD CONSTRAINT CK_unit_source CHECK (source IN (''test'', ''manual''))');
-- Postgres: ALTER TABLE unit ADD CONSTRAINT CK_unit_source CHECK (source IN ('test', 'manual'));
