-- migrate_rename_named_queries_active.sql
-- Rename named_queries.active -> is_active (issue #573, tracked in #540) for naming
-- consistency with every other table's is_active convention. Also renames the column's
-- default constraint DF_named_queries_active -> DF_named_queries_is_active.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#573/#540 binary queries `active` by name in
-- listNamedQueries/runNamedQuery and the Settings named-query editor. The Go change and
-- this migration must ship together — an older binary breaks after the rename, and this
-- build breaks against an un-renamed column. Run once every client is on a build
-- including this change.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded, safe to re-run).
--
-- One of four v4 migrations (#540); run migrate_schema_v4.sql last to bump schema_version.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.named_queries', 'active') IS NOT NULL
   AND COL_LENGTH('dbo.named_queries', 'is_active') IS NULL
    EXEC sp_rename 'dbo.named_queries.active', 'is_active', 'COLUMN';

-- Normalize the column's default constraint to DF_named_queries_is_active. Its current
-- name is looked up dynamically (by column, after the rename above): the live DB
-- auto-named it at creation (e.g. DF__named_qu__activ__XXXXXXXX, matching the auto-named
-- UQ__named_qu__... unique constraint), so a hardcoded old name would not match.
DECLARE @df sysname;
SELECT @df = dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
WHERE dc.parent_object_id = OBJECT_ID('dbo.named_queries') AND c.name = 'is_active';
IF @df IS NOT NULL AND @df <> 'DF_named_queries_is_active'
    EXEC sp_rename @df, 'DF_named_queries_is_active';

-- Postgres equivalent (for the future ArxProd -> Postgres migration, #625; the default
-- is an inline DEFAULT TRUE with no named constraint, so only the column is renamed):
--     ALTER TABLE named_queries RENAME COLUMN active TO is_active;
