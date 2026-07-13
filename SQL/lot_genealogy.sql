-- lot_genealogy: self-referencing edge table linking a consumed parent lot to the
-- child lot it was built into (#676, part of the #568 lot control epic). A build
-- writes one row per lot-tracked component lot consumed, but only when the output
-- part is itself lot-tracked (so a child lot exists to point at). Mirrors bom's
-- parent/child shape one level down at the lot-instance level. Many-to-many: a
-- child lot can consume many parent lots; a parent lot can feed many child lots.
-- Recurse child_lot_id -> parent_lot_id to trace an output lot back to raw vendor lots.
-- Requires lot to exist first (FKs below).

IF OBJECT_ID('dbo.lot_genealogy', 'U') IS NOT NULL DROP TABLE dbo.lot_genealogy;

CREATE TABLE lot_genealogy (
  id             INT            PRIMARY KEY IDENTITY,
  parent_lot_id  INT            NOT NULL,                                       -- FK to lot.id (the consumed component lot).
  child_lot_id   INT            NOT NULL,                                       -- FK to lot.id (the produced output lot).
  qty_consumed   DECIMAL(15,5)  NOT NULL CONSTRAINT DF_lot_gen_qty DEFAULT 0    -- Quantity of the parent lot consumed into the child.
);

ALTER TABLE dbo.lot_genealogy ADD CONSTRAINT FK_lot_gen_parent FOREIGN KEY (parent_lot_id) REFERENCES dbo.lot (id);
ALTER TABLE dbo.lot_genealogy ADD CONSTRAINT FK_lot_gen_child  FOREIGN KEY (child_lot_id)  REFERENCES dbo.lot (id);
CREATE INDEX IX_lot_gen_child  ON dbo.lot_genealogy (child_lot_id);
CREATE INDEX IX_lot_gen_parent ON dbo.lot_genealogy (parent_lot_id);
