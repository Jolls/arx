-- migrate_743_part_tracking_mode.sql
-- Traceability epic (#736) slice 6 (#743): add `part.tracking_mode` (VARCHAR+CHECK:
-- none|lot|serial|lot_serial) and backfill it from `is_lot_tracked`. See
-- docs/plans/736-traceability-data-model.md §4.2 / §7 slice 6 / §9 freeze checklist and
-- SQL/part.sql.
--
-- ADDITIVE: `is_lot_tracked` stays and keeps driving reads; the read-swap happens in slice 8.
-- Backfill maps 0->'none', 1->'lot'. The 'serial'/'lot_serial' values have no existing data to
-- map to (Arx has no serial concept yet) — they get set per-part afterward, out of band.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. tracking_mode is a new
-- NOT NULL column with a DEFAULT, so an INSERT from an old binary (which doesn't set it)
-- still succeeds, and the CHECK only rejects values no old binary ever writes. An old binary
-- keeps running against the migrated DB, so the mismatch banner must not fire (mirrors slice 3
-- / #740 and slice 5 / #742).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalent
-- follows in comments (for the #625 migration).
--
-- Runs as a single batch (no `GO`): some clients — e.g. the Azure portal's query editor — send
-- the whole script to the server as one batch and don't split on `GO`, so a plain statement
-- referencing `tracking_mode` right after the column is added fails with "Invalid column name"
-- (batch compilation resolves column names against pre-batch metadata). Steps 2 and 3 use
-- dynamic SQL (`EXEC(N'...')`) to defer that resolution to runtime, after the column exists.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Add tracking_mode NOT NULL DEFAULT 'none'.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.part', 'tracking_mode') IS NULL
    ALTER TABLE dbo.part ADD tracking_mode VARCHAR(10) NOT NULL CONSTRAINT DF_part_number_tracking_mode DEFAULT 'none';
-- Postgres: ALTER TABLE part ADD COLUMN IF NOT EXISTS tracking_mode VARCHAR(10) NOT NULL DEFAULT 'none';

-- ------------------------------------------------------------------
-- 2. Add the CHECK constraint (deferred via dynamic SQL — see header note).
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.CK_part_number_tracking_mode', 'C') IS NULL
    EXEC(N'ALTER TABLE dbo.part ADD CONSTRAINT CK_part_number_tracking_mode CHECK (tracking_mode IN (''none'', ''lot'', ''serial'', ''lot_serial''))');
-- Postgres: ALTER TABLE part ADD CONSTRAINT CK_part_number_tracking_mode CHECK (tracking_mode IN ('none', 'lot', 'serial', 'lot_serial'));

-- ------------------------------------------------------------------
-- 3. Backfill from is_lot_tracked (0->'none' via DEFAULT above; 1->'lot' explicitly; deferred
--    via dynamic SQL — see header note).
-- ------------------------------------------------------------------
EXEC(N'UPDATE dbo.part SET tracking_mode = ''lot'' WHERE is_lot_tracked = 1 AND tracking_mode = ''none''');
-- Postgres: UPDATE part SET tracking_mode = 'lot' WHERE is_lot_tracked = TRUE AND tracking_mode = 'none';
