-- part_category: configurable part categories (#194), edited in Settings → Part
-- Categories. part.category is an FK to code, so a code used by any part can't be
-- deleted. Natural-key PK (deliberate deviation from the bare-id convention: the
-- code is what part stores). The app caches the rows at startup and on save.

DROP TABLE IF EXISTS part_category CASCADE;

CREATE TABLE part_category (
  code                 VARCHAR(10)  NOT NULL PRIMARY KEY,   -- = part.category (FK_part_category)
  label                VARCHAR(255) NOT NULL DEFAULT '',
  is_purchased         BOOLEAN      NOT NULL DEFAULT FALSE,  -- bought, not built: "Create RFQs" orders it (#99)
  is_bom_visible       BOOLEAN      NOT NULL DEFAULT FALSE,  -- the flags below show/hide part subtabs
  is_orders_visible    BOOLEAN      NOT NULL DEFAULT FALSE,
  is_pricing_visible   BOOLEAN      NOT NULL DEFAULT FALSE,
  is_mfg_parts_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  is_suppliers_visible BOOLEAN      NOT NULL DEFAULT FALSE,
  is_inventory_visible BOOLEAN      NOT NULL DEFAULT FALSE,  -- also: consumed from stock by builds
  sort_order           INTEGER      NOT NULL DEFAULT 0,
  created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
