-- bom: Bill of Materials table (renamed from PL in db-table-rename commit 2).
-- Each row is one component line in an assembly.
-- parent_part_id is the part.id of the parent assembly (the thing being built).
-- component_part_id is the part.id of the component (the thing being used).
-- line_number is the sequential line item number within the assembly.
-- qty is the quantity of the component required per assembly.

IF OBJECT_ID('dbo.bom', 'U') IS NOT NULL DROP TABLE bom;

CREATE TABLE bom (
  id                 INT            PRIMARY KEY IDENTITY,
  parent_part_id     INT            NOT NULL,           -- FK to part.id (parent assembly).
  component_part_id  INT            NOT NULL,           -- FK to part.id (component part).
  line_number        INT            NOT NULL DEFAULT 0, -- Line item number within the assembly.
  qty                DECIMAL(15,5)  NOT NULL DEFAULT 1  -- Quantity required.
);

ALTER TABLE dbo.bom ADD CONSTRAINT FK_bom_PN_parent    FOREIGN KEY (parent_part_id)    REFERENCES dbo.part (id);
ALTER TABLE dbo.bom ADD CONSTRAINT FK_bom_PN_component FOREIGN KEY (component_part_id) REFERENCES dbo.part (id);
-- No UNIQUE on (parent_part_id, line_number) — item numbers are user-assigned and not enforced unique.
