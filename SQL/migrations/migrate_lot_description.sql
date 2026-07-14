-- migrate_lot_description.sql
-- Add lot.lot_description to carry human-readable provenance ("PO <number>" /
-- "Build #<id>" / "Manual entry"), persisted instead of derived via joins each read
-- (issue #687). Existing rows are backfilled using the same po_line/purchase_order/
-- build join logic the app used to derive it on the fly.
--
-- SAFETY: this script is pinned to ArxDev via the USE below. To apply it to ArxProd,
-- remove (or change) that single USE line — nothing else in the script names a database.
--
-- Idempotent (guarded, safe to re-run). Additive: lot_description is NOT NULL with a
-- '' default, so a pre-#687 binary that doesn't set it still inserts successfully.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.lot', 'lot_description') IS NULL
    ALTER TABLE dbo.lot ADD lot_description VARCHAR(255) NOT NULL CONSTRAINT DF_lot_desc DEFAULT '';

UPDATE l
SET l.lot_description = CASE
    WHEN po.number IS NOT NULL THEN 'PO ' + po.number
    WHEN b.id IS NOT NULL THEN 'Build #' + CAST(b.id AS VARCHAR(20))
    ELSE l.lot_description
END
FROM dbo.lot l
LEFT JOIN dbo.po_line pl ON pl.id = l.po_line_id
LEFT JOIN dbo.purchase_order po ON po.id = pl.po_id
LEFT JOIN dbo.build b ON b.output_lot_id = l.id
WHERE l.lot_description = '';
