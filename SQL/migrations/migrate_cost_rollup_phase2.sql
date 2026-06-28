-- migrate_cost_rollup_phase2.sql
-- BOM cost rollup Phase 2 (issues #465, re-scope #484). Two changes:
--   1. part.default_supplier_id — the preferred supplier whose cheapest active price
--      feeds the rollup as a part's leaf cost (falls back to current_cost when NULL).
--   2. 'OPS' category — operation/labor lines (current_cost = hourly rate, BOM qty = hours).
--
-- Run against BOTH ArxProd and ArxDev.
--
-- Idempotent: each step is guarded, so a partially-applied run can be re-executed.
-- Additive and rollback-safe: the column is nullable and 'OPS' only widens the CHECK,
-- so a 0.5.x binary never references either. Does NOT bump app_config.schema_version.

-- ============================================================
-- STEP 1: PREFERRED SUPPLIER COLUMN
-- ============================================================

IF COL_LENGTH('dbo.part', 'default_supplier_id') IS NULL
    ALTER TABLE dbo.part ADD default_supplier_id INT NULL;
GO

-- ============================================================
-- STEP 2: WIDEN CATEGORY CHECK TO ALLOW 'OPS'
-- ============================================================

IF EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = 'CK_part_number_category')
    ALTER TABLE dbo.part DROP CONSTRAINT CK_part_number_category;
GO

ALTER TABLE dbo.part ADD CONSTRAINT CK_part_number_category
    CHECK (category IN ('', 'ASM', 'BUY', 'DWG', 'DOC', 'FORM', 'MFG', 'OPS', 'RAW', 'SVC', 'TOOL'));
GO

-- ============================================================
-- STEP 3: AUTO-PIN PARTS THAT HAVE EXACTLY ONE ACTIVE-PRICE SUPPLIER
-- ============================================================
-- Parts whose active price rows reference a single distinct supplier get that supplier
-- pinned automatically. Parts with 2+ suppliers are left NULL for manual review (STEP 4).

UPDATE p
SET    default_supplier_id = s.supplier_id
FROM   dbo.part p
JOIN  (SELECT part_id, MIN(supplier_id) AS supplier_id
       FROM   dbo.price
       WHERE  is_active = 1
       GROUP  BY part_id
       HAVING COUNT(DISTINCT supplier_id) = 1) s
  ON   s.part_id = p.id
WHERE  p.default_supplier_id IS NULL;
GO

-- ============================================================
-- STEP 4: REPORT — parts with 2+ active suppliers needing manual review
-- ============================================================
-- Review these and set part.default_supplier_id by hand (or via the Pricing tab's
-- "Set preferred" control in the app).

SELECT pr.part_id,
       p.part_number,
       COUNT(DISTINCT pr.supplier_id) AS active_supplier_count
FROM   dbo.price pr
JOIN   dbo.part  p ON p.id = pr.part_id
WHERE  pr.is_active = 1
GROUP  BY pr.part_id, p.part_number
HAVING COUNT(DISTINCT pr.supplier_id) > 1
ORDER  BY p.part_number;
GO
