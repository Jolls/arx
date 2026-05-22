-- PL: Parts List — the BOM (Bill of Materials) table.
-- Each row is one component line in an assembly.
-- PLListID is the PNID of the parent assembly (the thing being built).
-- PLPartID is the PNID of the component (the thing being used).
-- PLItem is the sequential line item number within the assembly.
-- PLQty is the quantity of the component required per assembly.

IF OBJECT_ID('dbo.PL', 'U') IS NOT NULL DROP TABLE PL;

CREATE TABLE PL (
  PLID      INT            PRIMARY KEY IDENTITY,
  PLListID  INT            NOT NULL,           -- FK to PN.PNID (parent assembly).
  PLPartID  INT            NOT NULL,           -- FK to PN.PNID (component part).
  PLItem    INT            NOT NULL DEFAULT 0, -- Line item number within the assembly.
  PLQty     DECIMAL(15,5)  NOT NULL DEFAULT 1  -- Quantity required.
);

ALTER TABLE dbo.PL ADD CONSTRAINT FK_PL_PN_List FOREIGN KEY (PLListID) REFERENCES dbo.PN (PNID);
ALTER TABLE dbo.PL ADD CONSTRAINT FK_PL_PN_Part FOREIGN KEY (PLPartID) REFERENCES dbo.PN (PNID);
-- No UNIQUE on (PLListID, PLItem) — item numbers are user-assigned and not enforced unique.
