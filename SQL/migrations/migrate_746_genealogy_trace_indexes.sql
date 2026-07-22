-- migrate_746_genealogy_trace_indexes.sql
-- Traceability epic (#736) slice 9 (#746): make the generalized lot+unit genealogy
-- walk's four filter indexes (added by slice 4, #741) COVERING, so the recursive
-- per-node trace query (arx_go/lot.go traceNeighbors) is a pure index seek with no
-- key lookup back to the clustered index for the far endpoint + qty_consumed.
--
-- PURELY a performance change: no new columns, no behavior change, no schema_version
-- bump (an older binary is unaffected by wider indexes).
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, a human changes
-- that single line — nothing else in the script names a database. Author-only; never
-- run by an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent: each block only rebuilds the index when qty_consumed is not already one
-- of its INCLUDE columns, so re-running is a no-op. Single batch, no GO (run whole).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Change this line to apply to ArxProd.

-- IX_gen_child_lot: filters "find parents of lot X" (ancestors). Cover parent endpoints + qty.
IF NOT EXISTS (
    SELECT 1 FROM sys.index_columns ic
    JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
    WHERE ic.object_id = OBJECT_ID('dbo.genealogy')
      AND ic.index_id = (SELECT index_id FROM sys.indexes WHERE name = 'IX_gen_child_lot' AND object_id = OBJECT_ID('dbo.genealogy'))
      AND c.name = 'qty_consumed' AND ic.is_included_column = 1
)
BEGIN
    DROP INDEX IX_gen_child_lot ON dbo.genealogy;
    CREATE INDEX IX_gen_child_lot ON dbo.genealogy (child_lot_id) INCLUDE (parent_lot_id, parent_unit_id, qty_consumed);
END

-- IX_gen_child_unit: filters "find parents of unit X" (ancestors).
IF NOT EXISTS (
    SELECT 1 FROM sys.index_columns ic
    JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
    WHERE ic.object_id = OBJECT_ID('dbo.genealogy')
      AND ic.index_id = (SELECT index_id FROM sys.indexes WHERE name = 'IX_gen_child_unit' AND object_id = OBJECT_ID('dbo.genealogy'))
      AND c.name = 'qty_consumed' AND ic.is_included_column = 1
)
BEGIN
    DROP INDEX IX_gen_child_unit ON dbo.genealogy;
    CREATE INDEX IX_gen_child_unit ON dbo.genealogy (child_unit_id) INCLUDE (parent_lot_id, parent_unit_id, qty_consumed);
END

-- IX_gen_parent_lot: filters "find children of lot X" (descendants). Cover child endpoints + qty.
IF NOT EXISTS (
    SELECT 1 FROM sys.index_columns ic
    JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
    WHERE ic.object_id = OBJECT_ID('dbo.genealogy')
      AND ic.index_id = (SELECT index_id FROM sys.indexes WHERE name = 'IX_gen_parent_lot' AND object_id = OBJECT_ID('dbo.genealogy'))
      AND c.name = 'qty_consumed' AND ic.is_included_column = 1
)
BEGIN
    DROP INDEX IX_gen_parent_lot ON dbo.genealogy;
    CREATE INDEX IX_gen_parent_lot ON dbo.genealogy (parent_lot_id) INCLUDE (child_lot_id, child_unit_id, qty_consumed);
END

-- IX_gen_parent_unit: filters "find children of unit X" (descendants).
IF NOT EXISTS (
    SELECT 1 FROM sys.index_columns ic
    JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
    WHERE ic.object_id = OBJECT_ID('dbo.genealogy')
      AND ic.index_id = (SELECT index_id FROM sys.indexes WHERE name = 'IX_gen_parent_unit' AND object_id = OBJECT_ID('dbo.genealogy'))
      AND c.name = 'qty_consumed' AND ic.is_included_column = 1
)
BEGIN
    DROP INDEX IX_gen_parent_unit ON dbo.genealogy;
    CREATE INDEX IX_gen_parent_unit ON dbo.genealogy (parent_unit_id) INCLUDE (child_lot_id, child_unit_id, qty_consumed);
END
