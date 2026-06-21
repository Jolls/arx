-- migrate_inventory_core.sql
-- Inventory Core (issues #272 INV-1 + #274 INV-3). Adds the stock-movement ledger
-- and a cached on-hand balance per part.
--
-- Run against BOTH ArxProd and ArxDev. Idempotent (each step guarded, safe to re-run).
--
-- NOT rollback-safe: STEP 3 drops the legacy PN.PNQty column, which pre-0.5.28
-- binaries still read — so this migration also bumps app_config.schema_version to '3'
-- (STEP 4). The app warns when its ExpectedSchemaVersion and the DB value differ, so
-- an old binary pointed at a migrated DB (or a new binary at an un-migrated DB) is flagged.
-- Apply this together with deploying the v0.5.28 binary.
--
-- No data seed: PN.PNQty held no maintained data, so existing parts start at on-hand 0.
-- Going forward, on-hand is built from inventory_transaction rows (manual adjustments
-- now; PO receipts once #269 ships).

-- ============================================================
-- STEP 1: CACHED ON-HAND BALANCE ON PN
-- ============================================================

IF COL_LENGTH('dbo.PN', 'stock_on_hand') IS NULL
    ALTER TABLE dbo.PN ADD stock_on_hand DECIMAL(16,8) NOT NULL CONSTRAINT DF_PN_stock_on_hand DEFAULT 0;

-- ============================================================
-- STEP 2: STOCK-MOVEMENT LEDGER
-- ============================================================

IF OBJECT_ID('dbo.inventory_transaction', 'U') IS NULL
BEGIN
    CREATE TABLE dbo.inventory_transaction (
      id          INT          PRIMARY KEY IDENTITY,
      part_id     INT          NOT NULL,
      txn_type    VARCHAR(20)  NOT NULL CONSTRAINT CK_inv_txn_type CHECK (txn_type IN ('receipt','issue','adjustment','count')),
      qty         DECIMAL(16,8) NOT NULL,
      txn_date    DATE         NOT NULL CONSTRAINT DF_inv_txn_date DEFAULT GETDATE(),
      username    VARCHAR(128) NOT NULL CONSTRAINT DF_inv_txn_user DEFAULT '',
      reference   VARCHAR(255),
      note        VARCHAR(MAX),
      po_line_id  INT          NULL,
      created_at  DATETIME     NOT NULL CONSTRAINT DF_inv_txn_created DEFAULT GETDATE()
    );

    ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_PN  FOREIGN KEY (part_id)    REFERENCES dbo.PN (PNID);
    ALTER TABLE dbo.inventory_transaction ADD CONSTRAINT FK_inv_txn_POL FOREIGN KEY (po_line_id) REFERENCES dbo.POL (POLID);
    CREATE INDEX IX_inv_txn_part ON dbo.inventory_transaction (part_id, txn_date);
END

-- ============================================================
-- STEP 3: DROP THE LEGACY PN.PNQty COLUMN
-- Superseded by stock_on_hand. This is BREAKING for pre-0.5.28 binaries (their
-- part-detail read still SELECTs PNQty) — hence the schema_version bump in STEP 4.
-- Drops whatever default constraint is on the column (name varies by DB: the live
-- DB may still carry the DF__PN_Test__PNQty__* auto-name).
-- ============================================================

IF COL_LENGTH('dbo.PN', 'PNQty') IS NOT NULL
BEGIN
    DECLARE @dfPNQty NVARCHAR(128);
    SELECT @dfPNQty = dc.name
    FROM sys.default_constraints dc
    JOIN sys.columns col ON col.default_object_id = dc.object_id
    WHERE col.object_id = OBJECT_ID('dbo.PN') AND col.name = 'PNQty';
    IF @dfPNQty IS NOT NULL EXEC('ALTER TABLE dbo.PN DROP CONSTRAINT ' + @dfPNQty);
    ALTER TABLE dbo.PN DROP COLUMN PNQty;
END

-- ============================================================
-- STEP 4: BUMP SCHEMA VERSION (so older binaries flag the mismatch)
-- ============================================================

UPDATE dbo.app_config SET setting_value = '3', updated_at = GETDATE() WHERE setting_key = 'schema_version';
