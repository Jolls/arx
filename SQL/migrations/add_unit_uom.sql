-- Migration: add units-of-measure table and FK columns (#314)
-- Run once against the live database. Safe to run in test mode (targets real tables — re-run
-- _test.sql snapshot afterward to refresh _Test siblings).

-- 1. Create the unit reference table and seed it.
IF OBJECT_ID('dbo.unit', 'U') IS NULL
BEGIN
    CREATE TABLE unit (
        unit_id       INT           PRIMARY KEY IDENTITY,
        abbreviation  VARCHAR(20)   NOT NULL,
        display_name  VARCHAR(50)   NOT NULL,
        unit_type     VARCHAR(20)   NOT NULL,   -- count | volume | length | mass | package
    );

    INSERT INTO unit (abbreviation, display_name, unit_type) VALUES
        ('EA',    'Each',         'count'),
        ('PC',    'Piece',        'count'),
        ('mL',    'Milliliter',   'volume'),
        ('L',     'Liter',        'volume'),
        ('oz',    'Fluid Ounce',  'volume'),
        ('mm',    'Millimeter',   'length'),
        ('m',     'Meter',        'length'),
        ('IN',    'Inch',         'length'),
        ('FT',    'Foot',         'length'),
        ('g',     'Gram',         'mass'),
        ('kg',    'Kilogram',     'mass'),
        ('lb',    'Pound',        'mass'),
        ('PK',    'Pack',         'package'),
        ('REEL',  'Reel',         'package'),
        ('BOX',   'Box',          'package'),
        ('BTL',   'Bottle',       'package'),
        ('SPOOL', 'Spool',        'package');

    PRINT 'unit table created and seeded.';
END
ELSE
    PRINT 'unit table already exists — skipped.';

-- 2. Add PNUNID (base/inventory unit) to PN.
IF NOT EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.PN') AND name = 'PNUNID')
BEGIN
    ALTER TABLE dbo.PN ADD PNUNID INT NULL;
    ALTER TABLE dbo.PN ADD CONSTRAINT FK_PN_unit FOREIGN KEY (PNUNID) REFERENCES dbo.unit (unit_id);
    PRINT 'PN.PNUNID added.';
END
ELSE
    PRINT 'PN.PNUNID already exists — skipped.';

-- 3. Add unit_id (purchase unit) to supplier_part.
IF NOT EXISTS (SELECT 1 FROM sys.columns WHERE object_id = OBJECT_ID('dbo.supplier_part') AND name = 'unit_id')
BEGIN
    ALTER TABLE dbo.supplier_part ADD unit_id INT NULL;
    ALTER TABLE dbo.supplier_part ADD CONSTRAINT FK_supplier_part_unit FOREIGN KEY (unit_id) REFERENCES dbo.unit (unit_id);
    PRINT 'supplier_part.unit_id added.';
END
ELSE
    PRINT 'supplier_part.unit_id already exists — skipped.';

-- 4. Refresh _Test snapshot siblings (run _test.sql after this to pick up the new columns).
PRINT 'Done. Run SQL/_test.sql to refresh _Test snapshot tables with the new columns.';
