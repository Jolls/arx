-- migrate_drop_has_bom.sql
-- Drop part.has_bom (issue #555, tracked in #540). BOM presence is now computed
-- on demand via EXISTS(SELECT 1 FROM bom WHERE parent_part_id = ...) instead of a
-- denormalized flag — see hasOwnBOMExpr in arx_go/parts.go.
--
-- NON-BACKWARDS-COMPATIBLE: a pre-#555 binary still SELECTs has_bom by name in the
-- part fetch/detail paths. Only run once every client is on a build that includes
-- #555 (or later) — an older binary breaks after this column is gone.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change
-- that single line — nothing else in the script names a database. This is a script
-- for a human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded, safe to re-run).
--
-- One of four v4 migrations (#540); run migrate_schema_v4.sql last to bump schema_version.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

-- Drop the column's default constraint first. Its name is looked up dynamically rather
-- than hardcoded: on the live DB it is the legacy DF_PN_has_bom (named when the table was
-- PN, never renamed through the PN -> part_number -> part table renames), so a hardcoded
-- DF_part_number_has_bom would not match and DROP COLUMN would fail on the dependency.
DECLARE @df sysname;
SELECT @df = dc.name
FROM sys.default_constraints dc
JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
WHERE dc.parent_object_id = OBJECT_ID('dbo.part') AND c.name = 'has_bom';
IF @df IS NOT NULL
    EXEC('ALTER TABLE dbo.part DROP CONSTRAINT ' + @df);

IF COL_LENGTH('dbo.part', 'has_bom') IS NOT NULL
    ALTER TABLE dbo.part DROP COLUMN has_bom;
