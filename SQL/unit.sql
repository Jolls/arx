-- unit: one serialized instance (Tier-3 identity) of a part (#740, traceability epic
-- #736 slice 3 — the keystone). The serial-number tier the model previously lacked: a
-- serial becomes a real row with FKs, not a parsed string like "LOT123.4". Replaces the
-- old trace_id / is_batch approach (see docs/plans/736-traceability-data-model.md §4.1).
--
-- Created LAZILY, one at a time, when an individual unit is tested (Q5) — a build of 20
-- does NOT pre-create 20 rows; unit-row count is the numerator of completeness, not the
-- denominator. Exists only for parts whose tracking_mode is serial / lot_serial.
--
-- lot_id is set for a serialized unit inside a lot (lot_serial); build_id is set for a
-- build-sourced unit. Both are individually nullable, but a unit with BOTH NULL is
-- orphaned and defeats traceability — so CK_unit_provenance enforces at least one is set
-- (the Q5 provenance invariant), backed by app-level assignment at creation. serial_number
-- is a STRING (non-numeric serials allowed), UNIQUE per part_id, editable until the unit's
-- first locked form_record.
--
-- Additive/unused until slice 8 wires create/render logic. Requires part, lot, and build
-- to exist first (FKs below).

IF OBJECT_ID('dbo.unit', 'U') IS NOT NULL DROP TABLE dbo.unit;

CREATE TABLE unit (
  id             INT           PRIMARY KEY IDENTITY,
  part_id        INT           NOT NULL,                                              -- FK to part.id (the part this unit is an instance of).
  lot_id         INT           NULL,                                                  -- FK to lot.id; set for a serialized unit inside a lot, NULL for serial-only parts.
  build_id       INT           NULL,                                                  -- FK to build.id; set for a build-sourced unit, NULL otherwise.
  serial_number  VARCHAR(255)  NOT NULL,                                              -- Serial entered at test time; STRING (non-numeric allowed), UNIQUE per part_id.
  created_at     DATETIME      NOT NULL CONSTRAINT DF_unit_created   DEFAULT GETDATE(),
  is_active      BIT           NOT NULL CONSTRAINT DF_unit_is_active DEFAULT 1,       -- Soft-delete / scrap flag.
  CONSTRAINT CK_unit_provenance CHECK (lot_id IS NOT NULL OR build_id IS NOT NULL),  -- Provenance invariant (Q5): every unit traces to a lot or a build.
  CONSTRAINT UQ_unit_serial     UNIQUE (part_id, serial_number)
);

ALTER TABLE dbo.unit ADD CONSTRAINT FK_unit_part  FOREIGN KEY (part_id)  REFERENCES dbo.part (id);
ALTER TABLE dbo.unit ADD CONSTRAINT FK_unit_lot   FOREIGN KEY (lot_id)   REFERENCES dbo.lot (id);
ALTER TABLE dbo.unit ADD CONSTRAINT FK_unit_build FOREIGN KEY (build_id) REFERENCES dbo.build (id);
CREATE INDEX IX_unit_part ON dbo.unit (part_id, is_active);
