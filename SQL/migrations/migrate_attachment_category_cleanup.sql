-- Normalizes free-text part_attachment.category values that have drifted from the
-- configured attachment_categories list (app_config), consolidating the
-- highest-volume variants (>=5 uses) into the canonical set:
-- Product Page, CAD, 3DP Job, CAD Export, Datasheet, Drawing, Certificate, ECO,
-- Photo, Compliance, Misc URL, Other
-- (#546)
--
-- One-off / low-count values (<5 uses) and blank categories are intentionally
-- left untouched.

UPDATE part_attachment SET category = 'Drawing'    WHERE category = 'DWG';
UPDATE part_attachment SET category = 'Product Page' WHERE category = 'Vendor Link';
UPDATE part_attachment SET category = 'Misc URL'   WHERE category = 'ONSHAPE LINK';
UPDATE part_attachment SET category = 'Datasheet'  WHERE category = 'Vendor Spec Sheet';
UPDATE part_attachment SET category = 'CAD'        WHERE category = 'Vendor CAD';
UPDATE part_attachment SET category = 'CAD Export' WHERE category = 'CAD EXPORT';
UPDATE part_attachment SET category = 'Other'      WHERE category = 'Vendor Document';
UPDATE part_attachment SET category = 'Drawing'    WHERE category = 'Vendor Drawing';
UPDATE part_attachment SET category = 'Drawing'    WHERE category = 'Vendor DWG';
UPDATE part_attachment SET category = 'Other'      WHERE category = 'Document';
UPDATE part_attachment SET category = 'Datasheet'  WHERE category = 'Vendor Spec';
UPDATE part_attachment SET category = '3DP Job'    WHERE category = '3DP Job File';
UPDATE part_attachment SET category = 'Other'      WHERE category = 'QUOTE';
UPDATE part_attachment SET category = 'Drawing'    WHERE category = ' Vendor Drawing';
UPDATE part_attachment SET category = 'Other'      WHERE category = 'Laser Cutter SVG';
UPDATE part_attachment SET category = 'Drawing'    WHERE category = 'SCD';
UPDATE part_attachment SET category = 'Other'      WHERE category = 'Laser Cutter Profile';
UPDATE part_attachment SET category = 'Datasheet'  WHERE category = 'VENDOR DATASHEET';
