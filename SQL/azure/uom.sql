-- uom: Reference list of units of measure.
-- Used as the base/inventory unit on part (part.uom_id) and the purchase unit on supplier_part (uom_id).
-- unit_type groups entries for UI display: count | volume | length | mass | package
-- "package" units (REEL, BOX, etc.) are purchase-only — not suitable as a base inventory unit.

IF OBJECT_ID('dbo.uom', 'U') IS NOT NULL DROP TABLE dbo.uom;

CREATE TABLE uom (
    uom_id        INT           PRIMARY KEY IDENTITY,
    abbreviation  VARCHAR(20)   NOT NULL,   -- Short form shown in dropdowns and tables: EA, mL, kg, REEL, …
    display_name  VARCHAR(50)   NOT NULL,   -- Long form: Each, Milliliter, Kilogram, Reel, …
    unit_type     VARCHAR(20)   NOT NULL,   -- count | volume | length | mass | package
);

INSERT INTO uom (abbreviation, display_name, unit_type) VALUES
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
