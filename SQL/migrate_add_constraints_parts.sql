-- migrate_add_constraints_parts.sql
-- Adds deferred NOT NULL, FK, and UNIQUE constraints to parts-master tables.
-- Run against the PartsMaster database.
--
-- WORKFLOW:
--   1. Run STEP 1 — every SELECT must return 0 rows before proceeding.
--   2. Fix any violations in application data.
--   3. Run STEP 2.

-- ============================================================
-- STEP 1: VERIFY — each query must return 0 rows
-- ============================================================

-- ---- LNK ----
SELECT 'LNK: NULL LNKSUID'        AS check_name, LNKID FROM dbo.LNK WHERE LNKSUID IS NULL;
SELECT 'LNK: orphan LNKSUID'      AS check_name, LNKID, LNKSUID FROM dbo.LNK WHERE LNKSUID IS NOT NULL AND LNKSUID NOT IN (SELECT id FROM dbo.supplier);
SELECT 'LNK: NULL LNKPNID'        AS check_name, LNKID FROM dbo.LNK WHERE LNKPNID IS NULL;
SELECT 'LNK: orphan LNKPNID'      AS check_name, LNKID, LNKPNID FROM dbo.LNK WHERE LNKPNID IS NOT NULL AND LNKPNID NOT IN (SELECT PNID FROM dbo.PN);

-- ---- PL ----
SELECT 'PL: NULL PLListID'        AS check_name, PLID FROM dbo.PL WHERE PLListID IS NULL;
SELECT 'PL: orphan PLListID'      AS check_name, PLID, PLListID FROM dbo.PL WHERE PLListID IS NOT NULL AND PLListID NOT IN (SELECT PNID FROM dbo.PN);
SELECT 'PL: NULL PLPartID'        AS check_name, PLID FROM dbo.PL WHERE PLPartID IS NULL;
SELECT 'PL: orphan PLPartID'      AS check_name, PLID, PLPartID FROM dbo.PL WHERE PLPartID IS NOT NULL AND PLPartID NOT IN (SELECT PNID FROM dbo.PN);
SELECT 'PL: NULL PLItem'          AS check_name, PLID FROM dbo.PL WHERE PLItem IS NULL;
SELECT 'PL: NULL PLQty'           AS check_name, PLID FROM dbo.PL WHERE PLQty IS NULL;
-- PLItem uniqueness is not enforced — users may assign duplicate item numbers within an assembly.

-- ---- POL ----
SELECT 'POL: NULL POLPOID'        AS check_name, POLID FROM dbo.POL WHERE POLPOID IS NULL;
SELECT 'POL: orphan POLPOID'      AS check_name, POLID, POLPOID FROM dbo.POL WHERE POLPOID IS NOT NULL AND POLPOID NOT IN (SELECT id FROM dbo.PO);
SELECT 'POL: orphan POLPNID'      AS check_name, POLID, POLPNID FROM dbo.POL WHERE POLPNID IS NOT NULL AND POLPNID NOT IN (SELECT PNID FROM dbo.PN);
SELECT 'POL: NULL POLItem'        AS check_name, POLID FROM dbo.POL WHERE POLItem IS NULL;
SELECT 'POL: NULL POLQty'         AS check_name, POLID FROM dbo.POL WHERE POLQty IS NULL;
SELECT 'POL: NULL POLCost'        AS check_name, POLID FROM dbo.POL WHERE POLCost IS NULL;

-- ---- PO ----
SELECT 'PO: NULL number'          AS check_name, id FROM dbo.PO WHERE number IS NULL;
SELECT 'PO: NULL supplier_id'     AS check_name, id FROM dbo.PO WHERE supplier_id IS NULL;
SELECT 'PO: orphan supplier_id'   AS check_name, id, supplier_id FROM dbo.PO WHERE supplier_id IS NOT NULL AND supplier_id NOT IN (SELECT id FROM dbo.supplier);
SELECT 'PO: orphan receiver_id'   AS check_name, id, receiver_id FROM dbo.PO WHERE receiver_id IS NOT NULL AND receiver_id NOT IN (SELECT id FROM dbo.supplier);

-- ---- price ----
SELECT 'price: orphan part_id'    AS check_name, id, part_id FROM dbo.price WHERE part_id IS NOT NULL AND part_id NOT IN (SELECT PNID FROM dbo.PN);
SELECT 'price: orphan supplier_id' AS check_name, id, supplier_id FROM dbo.price WHERE supplier_id IS NOT NULL AND supplier_id NOT IN (SELECT id FROM dbo.supplier);
SELECT 'price: duplicate (part_id, supplier_id, pack_size)' AS check_name, part_id, supplier_id, pack_size, COUNT(*) AS cnt FROM dbo.price GROUP BY part_id, supplier_id, pack_size HAVING COUNT(*) > 1;

-- ---- supplier ----
SELECT 'supplier: NULL name'       AS check_name, id FROM dbo.supplier WHERE name IS NULL;
-- Rows with default_contact = 0 will be changed to NULL (0 is a sentinel, not a valid CNID).
-- Review these rows before migrating:
SELECT 'supplier: default_contact=0 (will become NULL)' AS check_name, id, name FROM dbo.supplier WHERE default_contact = 0;
SELECT 'supplier: orphan default_contact'  AS check_name, id, default_contact FROM dbo.supplier WHERE default_contact != 0 AND default_contact NOT IN (SELECT CNID FROM dbo.CN);
SELECT 'supplier: orphan primary_attachment_id' AS check_name, id, primary_attachment_id FROM dbo.supplier WHERE primary_attachment_id IS NOT NULL AND primary_attachment_id NOT IN (SELECT supplier_attachment_id FROM dbo.supplier_attachment);

-- ---- CN ----
SELECT 'CN: NULL CNName'          AS check_name, CNID FROM dbo.CN WHERE CNName IS NULL;


-- ============================================================
-- STEP 2: MIGRATE — run only after Step 1 returns 0 rows each
-- ============================================================

-- ---- LNK ----
ALTER TABLE dbo.LNK ALTER COLUMN LNKSUID INT NOT NULL;
ALTER TABLE dbo.LNK ADD CONSTRAINT FK_LNK_supplier FOREIGN KEY (LNKSUID) REFERENCES dbo.supplier (id);

ALTER TABLE dbo.LNK ALTER COLUMN LNKPNID INT NOT NULL;
ALTER TABLE dbo.LNK ADD CONSTRAINT FK_LNK_PN FOREIGN KEY (LNKPNID) REFERENCES dbo.PN (PNID);

-- ---- PL ----
ALTER TABLE dbo.PL ALTER COLUMN PLListID INT NOT NULL;
ALTER TABLE dbo.PL ADD CONSTRAINT FK_PL_PN_List FOREIGN KEY (PLListID) REFERENCES dbo.PN (PNID);

ALTER TABLE dbo.PL ALTER COLUMN PLPartID INT NOT NULL;
ALTER TABLE dbo.PL ADD CONSTRAINT FK_PL_PN_Part FOREIGN KEY (PLPartID) REFERENCES dbo.PN (PNID);

-- PLItem: NOT NULL may already be set; ALTER COLUMN is a no-op if so.
ALTER TABLE dbo.PL ALTER COLUMN PLItem INT NOT NULL;

-- PLQty: change DEFAULT from 0 to 1 (drop auto-named constraint first).
-- If the constraint name differs on your DB, find it with:
--   SELECT name FROM sys.default_constraints WHERE parent_object_id = OBJECT_ID('dbo.PL') AND col_name(parent_object_id, parent_column_id) = 'PLQty';
DECLARE @df_pl_qty NVARCHAR(128);
SELECT @df_pl_qty = name FROM sys.default_constraints
    WHERE parent_object_id = OBJECT_ID('dbo.PL')
    AND col_name(parent_object_id, parent_column_id) = 'PLQty';
IF @df_pl_qty IS NOT NULL
    EXEC('ALTER TABLE dbo.PL DROP CONSTRAINT ' + @df_pl_qty);
ALTER TABLE dbo.PL ALTER COLUMN PLQty DECIMAL(15,5) NOT NULL;
ALTER TABLE dbo.PL ADD CONSTRAINT DF_PL_PLQty DEFAULT 1 FOR PLQty;

-- No UNIQUE on (PLListID, PLItem) — item numbers are user-assigned and not enforced unique.

-- ---- POL ----
ALTER TABLE dbo.POL ALTER COLUMN POLPOID INT NOT NULL;
ALTER TABLE dbo.POL ADD CONSTRAINT FK_POL_PO FOREIGN KEY (POLPOID) REFERENCES dbo.PO (id);

-- POLPNID is nullable (PO line items may not map to a catalog part).
ALTER TABLE dbo.POL ADD CONSTRAINT FK_POL_PN FOREIGN KEY (POLPNID) REFERENCES dbo.PN (PNID);

ALTER TABLE dbo.POL ALTER COLUMN POLItem INT NOT NULL;
ALTER TABLE dbo.POL ALTER COLUMN POLQty DECIMAL(11,2) NOT NULL;

UPDATE dbo.POL SET POLCost = 0 WHERE POLCost IS NULL;
ALTER TABLE dbo.POL ALTER COLUMN POLCost DECIMAL(16,8) NOT NULL;
ALTER TABLE dbo.POL ADD CONSTRAINT DF_POL_POLCost DEFAULT 0 FOR POLCost;

-- ---- PO ----
ALTER TABLE dbo.PO ALTER COLUMN number VARCHAR(32) NOT NULL;

ALTER TABLE dbo.PO ALTER COLUMN supplier_id INT NOT NULL;
ALTER TABLE dbo.PO ADD CONSTRAINT FK_PO_supplier FOREIGN KEY (supplier_id) REFERENCES dbo.supplier (id);

-- receiver_id is nullable (not all POs have a separate ship-to).
ALTER TABLE dbo.PO ADD CONSTRAINT FK_PO_receiver FOREIGN KEY (receiver_id) REFERENCES dbo.supplier (id);

-- ---- price ----
ALTER TABLE dbo.price ADD CONSTRAINT FK_price_PN FOREIGN KEY (part_id) REFERENCES dbo.PN (PNID);
ALTER TABLE dbo.price ADD CONSTRAINT FK_price_supplier FOREIGN KEY (supplier_id) REFERENCES dbo.supplier (id);
ALTER TABLE dbo.price ADD CONSTRAINT UQ_price_part_supplier_pack UNIQUE (part_id, supplier_id, pack_size);

-- ---- supplier ----
ALTER TABLE dbo.supplier ALTER COLUMN name VARCHAR(127) NOT NULL;

-- default_contact: 0 is a sentinel for "no contact"; convert to NULL, make column nullable, add FK.
UPDATE dbo.supplier SET default_contact = NULL WHERE default_contact = 0;
ALTER TABLE dbo.supplier ALTER COLUMN default_contact INT NULL;
-- Drop the NOT NULL DEFAULT 0 default constraint before adding FK.
DECLARE @df_su_dc NVARCHAR(128);
SELECT @df_su_dc = name FROM sys.default_constraints
    WHERE parent_object_id = OBJECT_ID('dbo.supplier')
    AND col_name(parent_object_id, parent_column_id) = 'default_contact';
IF @df_su_dc IS NOT NULL
    EXEC('ALTER TABLE dbo.supplier DROP CONSTRAINT ' + @df_su_dc);
ALTER TABLE dbo.supplier ADD CONSTRAINT FK_supplier_default_contact FOREIGN KEY (default_contact) REFERENCES dbo.CN (CNID);

ALTER TABLE dbo.supplier ADD CONSTRAINT FK_supplier_primary_attachment FOREIGN KEY (primary_attachment_id) REFERENCES dbo.supplier_attachment (supplier_attachment_id);

-- ---- CN ----
ALTER TABLE dbo.CN ALTER COLUMN CNName VARCHAR(127) NOT NULL;
