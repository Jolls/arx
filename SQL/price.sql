-- price: Quantity-break pricing for parts by supplier.
-- Each row is one price point for a part/supplier combination at a given pack size.
-- part_id FKs to PN.PNID. supplier_id FKs to supplier.id.
-- price_ea is the per-unit cost. price_pack is the cost for the full pack_size quantity.
-- Multiple rows per part/supplier represent quantity break pricing.

IF OBJECT_ID('dbo.price', 'U') IS NOT NULL DROP TABLE price;

CREATE TABLE price (
  id           INT            PRIMARY KEY IDENTITY,
  part_id      INT            NOT NULL,    -- FK to PN.PNID.
  supplier_id  INT            NOT NULL,    -- FK to supplier.id.
  price_ea     DECIMAL(13,6)  CONSTRAINT DF_price_price_ea   DEFAULT 0, -- Per-unit price.
  price_pack   DECIMAL(13,6)  CONSTRAINT DF_price_price_pack DEFAULT 0, -- Total price for pack_size units.
  pack_size    DECIMAL(13,6)  CONSTRAINT DF_price_pack_size  DEFAULT 1, -- Units per pack. Decimal to support fractional quantities.
  is_active    BIT            CONSTRAINT DF_price_is_active  DEFAULT 1,
);

ALTER TABLE dbo.price ADD CONSTRAINT FK_price_PN              FOREIGN KEY (part_id)     REFERENCES dbo.PN (PNID);
ALTER TABLE dbo.price ADD CONSTRAINT FK_price_supplier        FOREIGN KEY (supplier_id) REFERENCES dbo.supplier (id);
ALTER TABLE dbo.price ADD CONSTRAINT UQ_price_part_supplier_pack UNIQUE (part_id, supplier_id, pack_size);
