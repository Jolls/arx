-- attachment_category: options for the attachment Category dropdown (#194), edited
-- in Settings. part_attachment.category stays free text (no FK): "Custom..." lets
-- users type any value. Natural-key PK on the option text.

DROP TABLE IF EXISTS attachment_category CASCADE;

CREATE TABLE attachment_category (
  display_name VARCHAR(500) NOT NULL PRIMARY KEY,
  sort_order   INTEGER      NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
