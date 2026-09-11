-- migrate_40_part_description.sql
-- Rename part.title -> part.description (issue #40): the column stores the part's
-- free-text name/description; "title" read as a document title, not a part attribute.
-- Pure rename, no behavior change. Renames the column, its default constraint, and
-- patches the one stored named_queries row whose SQL text selects the old column name
-- (sp_rename does not touch stored query text — see SQL/schema.md conventions and
-- migrate_rename_test_record_family.sql step 5 for the same pattern).
--
-- part.detail is a separate column and is NOT touched by this migration.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#40 binary queries this column by its old name.
-- The Go change (Config/model fields, part.description column literals) and this
-- migration must ship together — an older binary breaks after the rename, and this
-- build breaks against an un-renamed column. Bumps schema_version 9 -> 10, gating the
-- rollout via the mismatch banner. Run once every client is on a #40 build.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on the old name existing / new name not existing; safe
-- to re-run). Runs as a single batch (no `GO`) — the Azure portal's query editor sends
-- the whole script as one batch. sp_rename and the named_queries/app_config UPDATEs
-- are all runtime-resolved (EXEC calls / string literals, not direct column
-- references), so none of this needs dynamic-SQL wrapping the way a same-batch
-- SELECT/UPDATE against the new column name would.
--
-- Postgres equivalents follow each step in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- ------------------------------------------------------------------
-- 1. Rename the column.
-- ------------------------------------------------------------------
IF COL_LENGTH('dbo.part', 'title') IS NOT NULL AND COL_LENGTH('dbo.part', 'description') IS NULL
    EXEC sp_rename 'dbo.part.title', 'description', 'COLUMN';
-- Postgres: ALTER TABLE part RENAME COLUMN title TO description;

-- ------------------------------------------------------------------
-- 2. Rename the default constraint to match.
-- ------------------------------------------------------------------
IF OBJECT_ID('dbo.DF_part_number_title', 'D') IS NOT NULL AND OBJECT_ID('dbo.DF_part_description', 'D') IS NULL
    EXEC sp_rename 'dbo.DF_part_number_title', 'DF_part_description', 'OBJECT';
-- Postgres: default constraints aren't named objects; no action needed.

-- ------------------------------------------------------------------
-- 3. Patch the stored named_queries row whose SQL text selects the old column name
--    (row DATA — sp_rename above does not touch it). Idempotent (sets the row to its
--    final text; safe to re-run).
-- ------------------------------------------------------------------
UPDATE dbo.named_queries
   SET sql = 'SELECT part_number, description FROM bom JOIN part ON bom.component_part_id = part.id WHERE bom.parent_part_id = (SELECT id FROM part WHERE part_number = @pn) AND bom.line_number = @item'
 WHERE name = 'bom_pn_by_item';
-- Postgres: identical UPDATE (named_queries is dialect-agnostic row data).

-- ------------------------------------------------------------------
-- 4. Bump schema_version 9 -> 10 (gates the rollout; guarded, only advances 9 -> 10).
-- ------------------------------------------------------------------
UPDATE dbo.app_config
   SET setting_value = '10'
 WHERE setting_key = 'schema_version'
   AND setting_value = '9';
-- Postgres: identical UPDATE.
