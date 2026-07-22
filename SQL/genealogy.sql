-- genealogy: edge table recording provenance — which parent lot/unit was consumed
-- into which child lot/unit (traceability epic #736 slice 4, #741; widened from the
-- former lot_genealogy, #676/#568). Each endpoint is a lot OR a unit: a build writes
-- one edge per consumed component, and the exactly-one-parent / exactly-one-child
-- CHECKs (Q11) guarantee every edge names precisely one parent FK and one child FK.
-- Existing lot->lot rows stay valid (unit columns NULL). Many-to-many: a child can
-- consume many parents; a parent can feed many children. Recurse child_*_id ->
-- parent_*_id to trace an output back to its raw vendor lots / serialized children.
-- Requires lot and unit to exist first (FKs below).

IF OBJECT_ID('dbo.genealogy', 'U') IS NOT NULL DROP TABLE dbo.genealogy;

CREATE TABLE genealogy (
  id              INT            PRIMARY KEY IDENTITY,
  parent_lot_id   INT            NULL,                                        -- FK to lot.id  (consumed component lot).
  parent_unit_id  INT            NULL,                                        -- FK to unit.id (consumed component unit).
  child_lot_id    INT            NULL,                                        -- FK to lot.id  (produced output lot).
  child_unit_id   INT            NULL,                                        -- FK to unit.id (produced output unit).
  qty_consumed    DECIMAL(15,5)  NOT NULL CONSTRAINT DF_gen_qty DEFAULT 0,    -- Quantity of the parent consumed into the child.
  CONSTRAINT CK_gen_one_parent CHECK (
    (CASE WHEN parent_lot_id  IS NULL THEN 0 ELSE 1 END) +
    (CASE WHEN parent_unit_id IS NULL THEN 0 ELSE 1 END) = 1),
  CONSTRAINT CK_gen_one_child CHECK (
    (CASE WHEN child_lot_id  IS NULL THEN 0 ELSE 1 END) +
    (CASE WHEN child_unit_id IS NULL THEN 0 ELSE 1 END) = 1)
);

ALTER TABLE dbo.genealogy ADD CONSTRAINT FK_gen_parent_lot  FOREIGN KEY (parent_lot_id)  REFERENCES dbo.lot (id);
ALTER TABLE dbo.genealogy ADD CONSTRAINT FK_gen_parent_unit FOREIGN KEY (parent_unit_id) REFERENCES dbo.unit (id);
ALTER TABLE dbo.genealogy ADD CONSTRAINT FK_gen_child_lot   FOREIGN KEY (child_lot_id)   REFERENCES dbo.lot (id);
ALTER TABLE dbo.genealogy ADD CONSTRAINT FK_gen_child_unit  FOREIGN KEY (child_unit_id)  REFERENCES dbo.unit (id);
-- Covering indexes (#746): INCLUDE the far endpoints + qty_consumed so the recursive
-- lot+unit genealogy walk (arx_go/lot.go traceNeighbors) seeks without a key lookup.
CREATE INDEX IX_gen_parent_lot  ON dbo.genealogy (parent_lot_id)  INCLUDE (child_lot_id, child_unit_id, qty_consumed);
CREATE INDEX IX_gen_parent_unit ON dbo.genealogy (parent_unit_id) INCLUDE (child_lot_id, child_unit_id, qty_consumed);
CREATE INDEX IX_gen_child_lot   ON dbo.genealogy (child_lot_id)   INCLUDE (parent_lot_id, parent_unit_id, qty_consumed);
CREATE INDEX IX_gen_child_unit  ON dbo.genealogy (child_unit_id)  INCLUDE (parent_lot_id, parent_unit_id, qty_consumed);
