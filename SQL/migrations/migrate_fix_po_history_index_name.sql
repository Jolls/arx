-- migrate_fix_po_history_index_name.sql
-- Corrective follow-up to migrate_rename_purchase_order.sql (commit 4).
--
-- That migration tried to rename the index IX_PO_history_po -> IX_purchase_order_history_po
-- but guarded the rename with `IF OBJECT_ID('dbo.purchase_order_history.IX_PO_history_po','I')
-- IS NOT NULL`. OBJECT_ID cannot resolve an index (indexes are not standalone schema objects),
-- so that expression is always NULL and the rename was silently skipped on both ArxProd and
-- ArxDev — leaving the legacy index name in place while the DDL (SQL/PO.sql) and the sibling FK
-- (FK_purchase_order_history_po) already use the new name.
--
-- This script performs the rename with a correct sys.indexes guard so live matches the DDL.
-- Run against BOTH ArxProd and ArxDev. Idempotent.

IF EXISTS (SELECT 1 FROM sys.indexes
           WHERE name = 'IX_PO_history_po'
             AND object_id = OBJECT_ID('dbo.purchase_order_history'))
   AND NOT EXISTS (SELECT 1 FROM sys.indexes
                   WHERE name = 'IX_purchase_order_history_po'
                     AND object_id = OBJECT_ID('dbo.purchase_order_history'))
    EXEC sp_rename 'dbo.purchase_order_history.IX_PO_history_po',
                   'IX_purchase_order_history_po', 'INDEX';
