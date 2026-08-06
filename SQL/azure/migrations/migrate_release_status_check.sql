-- migrate_release_status_check.sql
-- Add CHECK (release_status IN ('U','A','D')) to part.release_status (issue #542,
-- tracked in #540). #542 made the column NOT NULL DEFAULT 'U' and had the app coalesce
-- blank -> 'U' on read/write (>=0.5.60 never writes '' or a value outside U/A/D). This
-- enforces that at the DB level so an older binary submitting the blank "-- Select --"
-- option can no longer insert ''. The VARCHAR(255) -> narrower type change is
-- intentionally deferred (see #213) to keep this a single additive constraint.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-0.5.60 binary saving a blank status errors on write
-- after this runs. Only run once every client is on >=0.5.60.
--
-- EVALUATE DATA FIRST — any row outside U/A/D would make ADD CONSTRAINT fail. Legacy ''
-- and NULL are normalized to 'U' below (the same coalescing the app already does); check
-- for any OTHER unexpected value first and fix by hand before running:
--     SELECT id, part_number, release_status
--     FROM dbo.part
--     WHERE release_status NOT IN ('U','A','D','');
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded, safe to re-run).
--
-- One of four v4 migrations (#540); run migrate_schema_v4.sql last to bump schema_version.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- Normalize the known legacy case (blank/NULL from older binaries) to 'U', matching the
-- app's read/write coalescing (#542). Any other out-of-range value is left alone so the
-- ADD CONSTRAINT below surfaces it for manual review rather than silently rewriting it.
UPDATE dbo.part SET release_status = 'U'
WHERE release_status IS NULL OR release_status = '';

IF OBJECT_ID('dbo.CK_part_number_release_status', 'C') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT CK_part_number_release_status
        CHECK (release_status IN ('U','A','D'));
