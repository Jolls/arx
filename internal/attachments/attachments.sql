-- ── part_attachment ──────────────────────────────────────────────────────────

-- name: ListPartAttachments :many
SELECT a.id, COALESCE(a.file_name, '') AS file_name, COALESCE(a.category, '') AS category,
       COALESCE(a.part_revision, '') AS part_revision, a.sort_order, COALESCE(a.comment, '') AS comment,
       a.supplier_part_id, a.mfg_part_id, COALESCE(sc.name, mc.name, '') AS vendor_name
FROM part_attachment a
LEFT JOIN supplier_part sp ON sp.id = a.supplier_part_id
LEFT JOIN company sc ON sc.id = sp.supplier_id
LEFT JOIN mfg_part mp ON mp.id = a.mfg_part_id
LEFT JOIN company mc ON mc.id = mp.mfg_id
WHERE a.part_id = $1 AND a.is_active = TRUE
ORDER BY a.sort_order, a.id;

-- name: GetPartAttachment :one
SELECT COALESCE(file_name, '') AS file_name, COALESCE(category, '') AS category,
       COALESCE(part_revision, '') AS part_revision, is_active
FROM part_attachment WHERE id = $1 AND part_id = $2;

-- name: CreatePartAttachment :exec
INSERT INTO part_attachment (part_id, file_name, part_revision, category, sort_order, comment,
                             supplier_part_id, mfg_part_id, hash)
VALUES (sqlc.arg(part_id), sqlc.arg(file_name)::text, sqlc.arg(part_revision)::text, sqlc.arg(category)::text,
        sqlc.narg(sort_order), sqlc.arg(comment)::text, sqlc.narg(supplier_part_id), sqlc.narg(mfg_part_id),
        sqlc.arg(hash)::text);

-- name: UpdatePartAttachment :exec
UPDATE part_attachment SET part_revision = sqlc.arg(part_revision)::text, category = sqlc.arg(category)::text,
  sort_order = sqlc.narg(sort_order), comment = sqlc.arg(comment)::text,
  supplier_part_id = sqlc.narg(supplier_part_id), mfg_part_id = sqlc.narg(mfg_part_id)
WHERE id = sqlc.arg(id);

-- name: UpdatePartAttachmentFile :exec
UPDATE part_attachment SET part_revision = sqlc.arg(part_revision)::text, category = sqlc.arg(category)::text,
  sort_order = sqlc.narg(sort_order), comment = sqlc.arg(comment)::text,
  supplier_part_id = sqlc.narg(supplier_part_id), mfg_part_id = sqlc.narg(mfg_part_id),
  file_name = sqlc.arg(file_name)::text, hash = sqlc.arg(hash)::text
WHERE id = sqlc.arg(id);

-- ReplacePartAttachmentPhoto repoints a row at a pasted image; its vendor scope is kept.
-- name: ReplacePartAttachmentPhoto :exec
UPDATE part_attachment SET part_revision = sqlc.arg(part_revision)::text, category = sqlc.arg(category)::text,
  sort_order = sqlc.narg(sort_order), comment = sqlc.arg(comment)::text,
  file_name = sqlc.arg(file_name)::text, hash = sqlc.arg(hash)::text
WHERE id = sqlc.arg(id);

-- name: SoftDeletePartAttachment :exec
UPDATE part_attachment SET is_active = FALSE WHERE id = $1 AND part_id = $2;

-- name: PartFileInUse :one
SELECT EXISTS (SELECT 1 FROM part_attachment
               WHERE file_name = sqlc.arg(file_name)::text AND is_active = TRUE AND id <> sqlc.arg(exclude_id));

-- EnsurePartPrimary points the part's primary at its first active attachment (lowest
-- sort_order, ties by id), but only when the current primary is NULL or no longer
-- active, so it is safe after every insert and soft-delete; with no active attachment
-- left the primary is cleared (#121). The generated preview/thumbnail rows never
-- become the primary.
-- name: EnsurePartPrimary :exec
UPDATE part SET primary_attachment_id = (
    SELECT a.id FROM part_attachment a
    WHERE a.part_id = part.id AND a.is_active = TRUE
      AND COALESCE(a.category, '') NOT IN (sqlc.arg(preview_category)::text, sqlc.arg(thumbnail_category)::text)
    ORDER BY COALESCE(a.sort_order, 0), a.id LIMIT 1)
WHERE part.id = sqlc.arg(part_id) AND (part.primary_attachment_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM part_attachment x WHERE x.id = part.primary_attachment_id AND x.is_active = TRUE));

-- name: SetPartPrimary :exec
UPDATE part SET primary_attachment_id = sqlc.narg(attachment_id) WHERE id = sqlc.arg(part_id);

-- name: FindDuplicatePartAttachment :one
SELECT a.id, p.id AS owner_id, p.part_number AS label
FROM part_attachment a JOIN part p ON p.id = a.part_id
WHERE a.is_active = TRUE AND a.hash = sqlc.arg(hash)::text AND a.id <> sqlc.arg(exclude_id)
ORDER BY a.id LIMIT 1;

-- name: SupplierPartOfPart :one
SELECT EXISTS (SELECT 1 FROM supplier_part WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id));

-- name: MfgPartOfPart :one
SELECT EXISTS (SELECT 1 FROM mfg_part WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id));

-- name: GetGeneratedAttachment :one
SELECT id, COALESCE(file_name, '') AS file_name FROM part_attachment
WHERE part_id = sqlc.arg(part_id) AND category = sqlc.arg(category)::text AND is_active = TRUE
ORDER BY id LIMIT 1;

-- name: CreateGeneratedAttachment :exec
INSERT INTO part_attachment (part_id, file_name, part_revision, category, hash)
VALUES (sqlc.arg(part_id), sqlc.arg(file_name)::text, sqlc.arg(part_revision)::text, sqlc.arg(category)::text,
        sqlc.arg(hash)::text);

-- name: UpdateGeneratedAttachment :exec
UPDATE part_attachment SET file_name = sqlc.arg(file_name)::text, part_revision = sqlc.arg(part_revision)::text,
  hash = sqlc.arg(hash)::text
WHERE id = sqlc.arg(id);

-- ── company_attachment ───────────────────────────────────────────────────────

-- name: ListCompanyAttachments :many
SELECT supplier_attachment_id, supplier_id, file_path, COALESCE(notes, '') AS notes, sort_order
FROM company_attachment WHERE supplier_id = $1 AND is_active = TRUE
ORDER BY sort_order, supplier_attachment_id;

-- name: GetCompanyAttachmentPath :one
SELECT file_path FROM company_attachment WHERE supplier_attachment_id = $1 AND supplier_id = $2;

-- name: CreateCompanyAttachment :exec
INSERT INTO company_attachment (supplier_id, file_path, notes, sort_order, hash)
VALUES (sqlc.arg(supplier_id), sqlc.arg(file_path), sqlc.arg(notes)::text, sqlc.narg(sort_order),
        sqlc.arg(hash)::text);

-- name: UpdateCompanyAttachment :exec
UPDATE company_attachment SET notes = sqlc.arg(notes)::text, sort_order = sqlc.narg(sort_order)
WHERE supplier_attachment_id = sqlc.arg(id) AND supplier_id = sqlc.arg(supplier_id);

-- name: UpdateCompanyAttachmentFile :exec
UPDATE company_attachment SET notes = sqlc.arg(notes)::text, sort_order = sqlc.narg(sort_order),
  file_path = sqlc.arg(file_path), hash = sqlc.arg(hash)::text
WHERE supplier_attachment_id = sqlc.arg(id) AND supplier_id = sqlc.arg(supplier_id);

-- name: SoftDeleteCompanyAttachment :exec
UPDATE company_attachment SET is_active = FALSE WHERE supplier_attachment_id = $1 AND supplier_id = $2;

-- name: CompanyFileInUse :one
SELECT EXISTS (SELECT 1 FROM company_attachment
               WHERE file_path = sqlc.arg(file_path) AND is_active = TRUE
                 AND supplier_attachment_id <> sqlc.arg(exclude_id));

-- EnsureCompanyPrimary is EnsurePartPrimary for a company (#121).
-- name: EnsureCompanyPrimary :exec
UPDATE company SET primary_attachment_id = (
    SELECT a.supplier_attachment_id FROM company_attachment a
    WHERE a.supplier_id = company.id AND a.is_active = TRUE
    ORDER BY COALESCE(a.sort_order, 0), a.supplier_attachment_id LIMIT 1)
WHERE company.id = sqlc.arg(supplier_id) AND (company.primary_attachment_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM company_attachment x
    WHERE x.supplier_attachment_id = company.primary_attachment_id AND x.is_active = TRUE));

-- name: SetCompanyPrimary :exec
UPDATE company SET primary_attachment_id = sqlc.narg(attachment_id) WHERE id = sqlc.arg(supplier_id);

-- name: FindDuplicateCompanyAttachment :one
SELECT a.supplier_attachment_id AS id, c.id AS owner_id, c.name AS label
FROM company_attachment a JOIN company c ON c.id = a.supplier_id
WHERE a.is_active = TRUE AND a.hash = sqlc.arg(hash)::text AND a.supplier_attachment_id <> sqlc.arg(exclude_id)
ORDER BY a.supplier_attachment_id LIMIT 1;

-- ── both ─────────────────────────────────────────────────────────────────────

-- WhereUsed lists every part and supplier with an active attachment of exactly this link.
-- name: WhereUsed :many
SELECT 'part'::text AS kind, p.id AS owner_id, COALESCE(p.part_number, '') AS code,
       COALESCE(p.description, '') AS label
FROM part_attachment fa JOIN part p ON p.id = fa.part_id
WHERE fa.is_active = TRUE AND fa.file_name = sqlc.arg(file)::text
UNION ALL
SELECT 'supplier'::text, c.id, '', COALESCE(c.name, '')
FROM company_attachment ca JOIN company c ON c.id = ca.supplier_id
WHERE ca.is_active = TRUE AND ca.file_path = sqlc.arg(file)::text
ORDER BY 1, 4;
