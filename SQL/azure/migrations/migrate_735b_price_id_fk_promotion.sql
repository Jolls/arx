-- migrate_735b_price_id_fk_promotion.sql
-- #213 follow-up (#735 Group B): part.price_id -> price.id.
-- Unlike Group A's already-NULL-based columns, price_id uses a DEFAULT 0 "no value" sentinel.
-- No Go code path reads or writes this column (confirmed by audit), so the 0->NULL conversion
-- is a pure data/schema change with no accompanying Go blast radius.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that single
-- line — nothing else in the script names a database. This is a script for a human to run,
-- not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each step guarded on its old/new state; safe to re-run). Postgres equivalents
-- follow each block in comments (for the #625 migration).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- 1. Convert the 0-sentinel to NULL (the semantic meaning of "no price" is unchanged; only
--    its DB representation moves from 0 to NULL).
UPDATE dbo.part SET price_id = NULL WHERE price_id = 0;
-- Postgres: UPDATE part SET price_id = NULL WHERE price_id = 0;

-- 2. Drop the DEFAULT 0 constraint. Looked up dynamically by column, not by the
--    DF_part_number_price_id name in SQL/azure/part.sql — a live DB may have auto-named it
--    at creation (e.g. DF__part__price_id__XXXXXXXX), matching the pattern in
--    migrate_rename_named_queries_active.sql.
DECLARE @df_price sysname;
SELECT @df_price = dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
WHERE dc.parent_object_id = OBJECT_ID('dbo.part') AND c.name = 'price_id';
IF @df_price IS NOT NULL
    EXEC('ALTER TABLE dbo.part DROP CONSTRAINT ' + @df_price);
-- Postgres: ALTER TABLE part ALTER COLUMN price_id DROP DEFAULT;

-- 3. Promote to a real FK. ORPHAN CHECK FIRST on a live DB — must return zero rows:
--      SELECT p.id, p.price_id FROM dbo.part p
--      LEFT JOIN dbo.price pr ON pr.id = p.price_id
--      WHERE p.price_id IS NOT NULL AND pr.id IS NULL;
IF OBJECT_ID('dbo.FK_part_price', 'F') IS NULL
    ALTER TABLE dbo.part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES dbo.price (id);
-- Postgres: ALTER TABLE part ADD CONSTRAINT FK_part_price FOREIGN KEY (price_id) REFERENCES price (id);
