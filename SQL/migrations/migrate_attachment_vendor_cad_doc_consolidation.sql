-- Consolidates the 2 remaining high-volume one-off part_attachment.category values
-- (excluded from migrate_attachment_oneoff_categories_to_comment.sql for deliberate
-- handling) into their corresponding canonical categories (#585):
-- Vendor CAD (88 uses) -> CAD
-- Vendor Doc (79 uses) -> Document (newly added to the canonical attachment_categories list)

UPDATE part_attachment SET category = 'CAD'      WHERE category = 'Vendor CAD' AND is_active = 1;
UPDATE part_attachment SET category = 'Document' WHERE category = 'Vendor Doc' AND is_active = 1;
