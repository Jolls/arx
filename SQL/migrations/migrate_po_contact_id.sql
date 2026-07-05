-- migrate_po_contact_id.sql
-- Contact-linked purchase orders (issue #597). Adds nullable FK columns
-- supplier_contact_id / receiver_contact_id → contact.id so a contact's detail
-- page can list its POs. The existing name-snapshot columns (supplier_contact /
-- receiver_contact) are retained and remain authoritative for print/display.
--
-- Additive and rollback-safe: both new columns are nullable and the old binary
-- never references them. Run against BOTH ArxProd and ArxDev.
--
-- Idempotent: safe to re-run (each step is guarded). Add the FK constraints only
-- AFTER the backfill so a partially-matched row can never block the ALTER.

-- ============================================================
-- STEP 1: ADD THE COLUMNS (nullable)
-- ============================================================

IF COL_LENGTH('dbo.purchase_order', 'supplier_contact_id') IS NULL
    ALTER TABLE dbo.purchase_order ADD supplier_contact_id INT NULL;

IF COL_LENGTH('dbo.purchase_order', 'receiver_contact_id') IS NULL
    ALTER TABLE dbo.purchase_order ADD receiver_contact_id INT NULL;
GO

-- ============================================================
-- STEP 2: BACKFILL FROM THE NAME SNAPSHOT
-- Best-effort match on company + display name. Only fills rows not already
-- linked. When several contacts share a name at the same company, the ACTIVE
-- one wins (is_active DESC), falling back to a soft-deleted one only if that's
-- all there is; ties break on lowest id so the result is deterministic.
-- Unmatched rows stay NULL (legacy, safe).
-- ============================================================

UPDATE po SET supplier_contact_id = c.id
FROM dbo.purchase_order po
CROSS APPLY (
    SELECT TOP 1 id
    FROM dbo.contact
    WHERE company_id = po.supplier_id
      AND display_name = po.supplier_contact
    ORDER BY is_active DESC, id
) c
WHERE po.supplier_contact_id IS NULL
  AND po.supplier_contact IS NOT NULL
  AND po.supplier_contact <> '';

UPDATE po SET receiver_contact_id = c.id
FROM dbo.purchase_order po
CROSS APPLY (
    SELECT TOP 1 id
    FROM dbo.contact
    WHERE company_id = po.receiver_id
      AND display_name = po.receiver_contact
    ORDER BY is_active DESC, id
) c
WHERE po.receiver_contact_id IS NULL
  AND po.receiver_contact IS NOT NULL
  AND po.receiver_contact <> '';
GO

-- ============================================================
-- STEP 3: ADD THE FOREIGN KEY CONSTRAINTS (after backfill)
-- ============================================================

IF OBJECT_ID('dbo.FK_purchase_order_supplier_contact', 'F') IS NULL
    ALTER TABLE dbo.purchase_order ADD CONSTRAINT FK_purchase_order_supplier_contact
        FOREIGN KEY (supplier_contact_id) REFERENCES dbo.contact (id);

IF OBJECT_ID('dbo.FK_purchase_order_receiver_contact', 'F') IS NULL
    ALTER TABLE dbo.purchase_order ADD CONSTRAINT FK_purchase_order_receiver_contact
        FOREIGN KEY (receiver_contact_id) REFERENCES dbo.contact (id);
GO
