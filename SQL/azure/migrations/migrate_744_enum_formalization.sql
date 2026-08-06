-- migrate_744_enum_formalization.sql
-- Traceability epic (#736) slice 7 (#744): formalize three inferred value sets as VARCHAR+CHECK
-- "enum" columns, and promote the last in-scope logical reference to a real FK. See
-- docs/plans/736-traceability-data-model.md §4.1 / §4.3 / §4.4 / §6 Q10 / §7 slice 7 and
-- SQL/lot.sql, SQL/form.sql, SQL/form_row.sql.
--
--     lot.source          VARCHAR(10)  NULL  CHECK (purchase|build|adjust)
--     form.form_type       VARCHAR(20)  NOT NULL DEFAULT 'test'  CHECK (inspection|test|calibration|checklist|batch record)
--     form_row.granularity VARCHAR(10)  NOT NULL DEFAULT 'unit'  CHECK (lot|unit)
--     form.part_number_id -> part.id    real FK (was a logical reference; §4.4)
--
-- ADDITIVE / inert: nothing reads these columns until the epic's slice-8 code. The values the app
-- branches on land now (VARCHAR+CHECK, §4.3 — not a native enum, not a lookup table) so the schema
-- is stable before the code that uses it.
--
-- Backfills:
--   * lot.source is inferred from existing structure — po_line_id set => 'purchase'; else an owning
--     build (build.output_lot_id = lot.id) => 'build'; else 'adjust' (manual / cycle-count). Left
--     NULLABLE with no DEFAULT: unlike part.tracking_mode's 'none', a lot has no neutral resting
--     source, so a lot created by a pre-slice-8 binary between this migration and slice 8 records
--     NULL ("not yet classified") rather than a mislabel; slice-8 code sets it going forward.
--   * form.form_type backfills every existing form to 'test' (this is a test-records system; DEFAULT
--     'test' also covers new forms from a pre-slice-8 binary). Q10: 'batch record' is a legal
--     form_type value, and is the ONE record_types token that conceptually moves here. This migration
--     does NOT rewrite record_types data (no seed row carries it; a live site with a 'batch record'
--     entry in form.record_types moves it to form_type by hand — see the SELECT at the bottom).
--   * form_row.granularity backfills to 'unit' via the DEFAULT: every existing form line is a
--     per-unit test (records are per-serial with full per-parameter results).
--
-- part_number_id has always been a logical reference (the FORM's own PN); promoting it to a real FK
-- requires that no form row points at a missing part. The seed is clean; on a live DB a human runs
-- the orphan check below FIRST and reconciles any hits before the ADD CONSTRAINT (which fails loudly
-- on an orphan). Kept ON DELETE NO ACTION (the default), matching #677/#742.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. All three columns are new (NULL,
-- or NOT NULL with a DEFAULT), so an INSERT from a pre-#744 binary still succeeds; each CHECK rejects
-- only values no old binary writes, and the new FK rejects only a form pointing at a missing part,
-- which the old binary never creates. An old binary keeps running against the migrated DB, so the
-- mismatch banner must not fire (mirrors slices 3/5/6 — #740/#742/#743).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single line —
-- nothing else in the script names a database. This is a script for a human to run, not for an agent
-- (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalents follow
-- each block in comments (for the #625 migration).
--
-- Runs as a single batch (no `GO`): some clients — e.g. the Azure portal's query editor — send the
-- whole script as one batch and don't split on `GO`, so a plain statement referencing a just-added
-- column fails with "Invalid column name" (batch compilation resolves names against pre-batch
-- metadata). The backfills below use dynamic SQL (`EXEC(N'...')`) to defer that resolution to
-- runtime, after the column exists.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. lot.source (NULLABLE) + CHECK.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.lot', 'source') IS NULL
    ALTER TABLE dbo.lot ADD source VARCHAR(10) NULL;
-- Postgres: ALTER TABLE lot ADD COLUMN IF NOT EXISTS source VARCHAR(10) NULL;

IF OBJECT_ID('dbo.CK_lot_source', 'C') IS NULL
    EXEC(N'ALTER TABLE dbo.lot ADD CONSTRAINT CK_lot_source CHECK (source IN (''purchase'', ''build'', ''adjust''))');
-- Postgres: ALTER TABLE lot ADD CONSTRAINT CK_lot_source CHECK (source IN ('purchase', 'build', 'adjust'));

-- Backfill by inference (deferred via dynamic SQL — see header note). Only touch rows not yet
-- classified so a re-run / a slice-8-set value is left alone.
EXEC(N'
    UPDATE dbo.lot SET source = ''purchase'' WHERE source IS NULL AND po_line_id IS NOT NULL;
    UPDATE dbo.lot SET source = ''build''    WHERE source IS NULL AND EXISTS (SELECT 1 FROM dbo.build b WHERE b.output_lot_id = dbo.lot.id);
    UPDATE dbo.lot SET source = ''adjust''   WHERE source IS NULL;');
-- Postgres:
--   UPDATE lot SET source = 'purchase' WHERE source IS NULL AND po_line_id IS NOT NULL;
--   UPDATE lot SET source = 'build'    WHERE source IS NULL AND EXISTS (SELECT 1 FROM build b WHERE b.output_lot_id = lot.id);
--   UPDATE lot SET source = 'adjust'   WHERE source IS NULL;

-- ------------------------------------------------------------------
-- 2. form.form_type NOT NULL DEFAULT 'test' + CHECK.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.form', 'form_type') IS NULL
    ALTER TABLE dbo.form ADD form_type VARCHAR(20) NOT NULL CONSTRAINT DF_form_type DEFAULT 'test';
-- Postgres: ALTER TABLE form ADD COLUMN IF NOT EXISTS form_type VARCHAR(20) NOT NULL DEFAULT 'test';

IF OBJECT_ID('dbo.CK_form_type', 'C') IS NULL
    EXEC(N'ALTER TABLE dbo.form ADD CONSTRAINT CK_form_type CHECK (form_type IN (''inspection'', ''test'', ''calibration'', ''checklist'', ''batch record''))');
-- Postgres: ALTER TABLE form ADD CONSTRAINT CK_form_type CHECK (form_type IN ('inspection', 'test', 'calibration', 'checklist', 'batch record'));

-- ------------------------------------------------------------------
-- 3. form_row.granularity NOT NULL DEFAULT 'unit' + CHECK (existing rows are per-unit tests).
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.form_row', 'granularity') IS NULL
    ALTER TABLE dbo.form_row ADD granularity VARCHAR(10) NOT NULL CONSTRAINT DF_form_row_granularity DEFAULT 'unit';
-- Postgres: ALTER TABLE form_row ADD COLUMN IF NOT EXISTS granularity VARCHAR(10) NOT NULL DEFAULT 'unit';

IF OBJECT_ID('dbo.CK_form_row_granularity', 'C') IS NULL
    EXEC(N'ALTER TABLE dbo.form_row ADD CONSTRAINT CK_form_row_granularity CHECK (granularity IN (''lot'', ''unit''))');
-- Postgres: ALTER TABLE form_row ADD CONSTRAINT CK_form_row_granularity CHECK (granularity IN ('lot', 'unit'));

-- ------------------------------------------------------------------
-- 4. Promote form.part_number_id to a real FK (§4.4). ORPHAN CHECK FIRST on a live DB — this must
--    return zero rows before the ADD CONSTRAINT below, or reconcile the offending forms:
--        SELECT f.id, f.part_number_id FROM dbo.form f
--        LEFT JOIN dbo.part p ON p.id = f.part_number_id
--        WHERE p.id IS NULL;
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.FK_form_part', 'F') IS NULL
    ALTER TABLE dbo.form ADD CONSTRAINT FK_form_part FOREIGN KEY (part_number_id) REFERENCES dbo.part (id);
-- Postgres: ALTER TABLE form ADD CONSTRAINT FK_form_part FOREIGN KEY (part_number_id) REFERENCES part (id);

-- ------------------------------------------------------------------
-- 5. (INFORMATIONAL — no automated change) Q10 'batch record' move. Any live site whose
--    form.record_types carries a 'batch record' token moves it to form_type by hand; the seed has
--    none. Find them with:
--        SELECT id, record_types FROM dbo.form WHERE ',' + record_types + ',' LIKE '%,batch record,%';
-- ------------------------------------------------------------------
