# #71 — Attachment content hash + duplicate detection

Add a `hash` column to `part_attachment` and `company_attachment`, populate it on every
write, and warn (dismissibly) when a new/edited attachment's hash already exists as an
**active** row in the same table.

Issue: [#71](https://github.com/Jolls/arx/issues/71)

---

## Resolved decisions (confirmed with user 2026-09-17 — all per the recommendation below)

### Q1. How does "warn, then let the user proceed" work in a server-rendered POST flow?

This app has no SPA layer; attachment forms are plain multipart POSTs that either redirect
(302) or re-render the page with an `Error` / `ImportCollision` banner.

**Option A (recommended) — post-back re-render with a hidden `confirm_duplicate` field.**
Exactly the shape of the existing `ImportCollision` banner in
`arx_go/templates/parts/part_attachments.html:20-68`:

1. POST arrives. `resolveAttachmentFileInput` runs as it does today — an uploaded file is
   already copied into `DOC_CONTROL_ROOT` and `in.FileName` is the final `LOCAL:<name>`.
2. Handler computes the hash and queries for an active match.
3. On a match, **nothing is inserted**; the page re-renders with a `DuplicateWarning`
   banner naming the existing attachment (linked) and two buttons:
   - **Add anyway** — a `<form>` that re-posts every field it already has, with
     `FILFileName=LOCAL:<name>` (so no re-upload is needed — the bytes are on disk) plus
     `confirm_duplicate=1`. The handler skips the check and inserts.
   - **Cancel** — see Q2.

   Zero new JS; no duplicated hash logic in the browser; identical to a pattern the user
   already knows from the import-collision prompt.

**Option B — AJAX preflight.** JS hashes the file with `crypto.subtle` (available:
`localhost` is a secure context), POSTs to a new `/api/attachment-hash-check`, renders a
banner, then submits with a hidden field. Rejected: it re-implements the
file-vs-string hash rule in JavaScript (the exact duplication `#558` deliberately removed
for filename generation), and the supplier attachments page currently has no JS at all.

> **Recommendation: Option A.**

### Q2. What happens to the just-copied file if the user cancels a duplicate warning?

Unlike the import-collision path (where nothing was written), by the time the duplicate is
detected an uploaded file has **already been copied** into `DOC_CONTROL_ROOT` /
`SUPPLIER_FILES_ROOT` under its generated name. Cancelling leaves an orphan file with no
row pointing at it.

- **(a) (recommended)** Cancel posts `discard_import=LOCAL:<name>` back to the same create
  route; the handler deletes that file via the existing
  `deleteAttachmentFileIfUnshared` (which no-ops if some other active row links it) and
  redirects. ~15 lines, no new route needed — a `discard_import` branch at the top of
  `PartAttachmentCreate` / `SupplierAttachmentCreate`.
- **(b)** Leave the orphan; Cancel is a plain link back to the attachments page. Zero code,
  but silently litters Doc Control every time someone backs out of a duplicate.

> **Recommendation: (a).** The banner only appears because the file is byte-identical to
> one already in Doc Control, so the copy is pure waste.

### Q3. Do multi-file batch uploads get the duplicate warning?

The issue says the batch check "compares only against hashes already committed to the
table, not against other files in the same in-progress batch" — but `importAttachmentBatch`
(`arx_go/attachments.go:414`) commits each file's row before recursing to the next, so
"already committed" *does* include earlier files of the same batch unless ids are tracked
through the recursion.

- **(a) (recommended)** Batch imports **record the hash but do not warn.** The mid-batch
  collision resume state machine (`batch_dir` / `batch_remaining` / `batch_categories` /
  `batch_total`, staged temp files, `allowLinkExisting` threading) is already the most
  intricate code in `attachments.go`; a second interrupt state doubles it.
- **(b)** Warn mid-batch too: thread an `insertedIDs []int` through `importAttachmentBatch`
  and add `DuplicateWarning` resume fields mirroring `ImportCollision`. Materially more
  code and a second resume path to test.

> **Recommendation: (a)** for this issue; (b) is a clean follow-up if wanted.

### Q4. Do the non-form write paths populate `hash` too?

The issue names only the four form handlers, but five other code paths create/repoint
attachment rows:

| Path | File |
|---|---|
| Clipboard paste (new) | `arx_go/api.go:313` `APIPartPasteAttachment` |
| Clipboard paste (replace) | `arx_go/api.go:385` `APIPartPasteAttachmentReplace` |
| PDF preview/thumbnail upsert | `arx_go/api.go:533` `upsertGeneratedAttachment` |
| DigiKey import (datasheet/photo) | `arx_go/sourcing.go:264` `applyDigiKeyImportExtras` |
| — | |

> **Recommendation: populate `hash` on all of them (bytes are already in hand — hash them
> directly, no re-read), but never *warn* there.** Those are machine-generated or
> explicitly-requested imports where an interrupt banner makes no sense, and a column that
> is only sometimes populated is useless for any future report. Cost is one extra column
> per INSERT/UPDATE.

### Q5. Where does the backfill command live?

No `cmd/` directory exists anywhere in the repo today; `arx_go` is a single `package main`.

> **Recommendation: follow the issue — `arx_go/cmd/backfill_attachment_hash/main.go`**, run
> as `cd arx_go && go run ./cmd/backfill_attachment_hash`. It compiles inside module
> `arx/arx_go`, is picked up by `go vet ./...` / `go test ./...`, and is untouched by
> `build.bat` (which builds only the root package with `-H windowsgui`). Alternative
> considered: a button on Settings → Utilities (`/settings/utilities` already exists and
> already resolves both attachment roots) — nicer for a non-developer, but the issue asks
> for a command and this is a one-time operation the developer runs.

### Q6. Should the hash string input be normalized?

For directory-style `LOCAL:` links and http(s)/UNC links we hash the **exact stored
string** as it is after `urlutil.NormalizeLink` — no case folding, no trailing-slash
normalization.

> **Recommendation: yes, hash the stored string verbatim.** Consequence to accept:
> `https://x.com/a.pdf` and `https://X.com/a.pdf` won't be flagged as duplicates. Anything
> smarter is guesswork about which URL components are case-significant.

### Q7. Index on `hash`?

> **Recommendation: no index.** Both tables are small (hundreds of rows), the lookup runs
> once per attachment save, and an index is speculative per CLAUDE.md §2. Add one if the
> tables ever grow.

**Not open:** `schema_version` is **not** bumped (nullable additive column, no `DEFAULT`;
old binaries' INSERTs still succeed — same call as `migrate_799` / `migrate_847`). No
FK-promotion orphan pre-check applies (this is a plain column add, not a constraint).

---

## Behavior spec

| Attachment shape | Hash input |
|---|---|
| `LOCAL:file.pdf` (single file) | SHA-256 of the file's **bytes**, read from `DOC_CONTROL_ROOT` (part) / `SUPPLIER_FILES_ROOT`→`DOC_CONTROL_ROOT` fallback (company) |
| `LOCAL:folder\` (trailing `/` or `\`) | SHA-256 of the **link string** |
| `http://` / `https://` | SHA-256 of the **link string** |
| `\\server\...`, `C:\...`, `file://` (abs path) | SHA-256 of the **link string** — the app never streams these (`urlutil.IsAbsPath`) |
| `LOCAL:` file that is missing/unreadable | SHA-256 of the **link string** (fallback, so the column is never NULL on a new write) |

- Stored as 64 lowercase hex chars.
- Dup check is per-table, system-wide, `is_active = 1` only, excluding the row being edited.
- Recomputed whenever the file/URL changes (`fileChanged` path); a metadata-only edit
  (category/rev/comment/vendor scope) leaves `hash` untouched.

---

## 1. Schema

### 1.1 `SQL/azure/part_attachment.sql`

Add to the `CREATE TABLE` body, after `comment`:

```sql
  hash           CHAR(64),       -- SHA-256 hex of the attachment (#71): file content for a
                                 -- single-file LOCAL: link, else the link string itself.
                                 -- NULL = not yet backfilled (see cmd/backfill_attachment_hash).
```

Add to the header comment block:

```
-- Prior migrations (historical reference):
--   #71  — hash column added (SQL/azure/migrations/migrate_71_attachment_hash.sql)
```

### 1.2 `SQL/azure/company_attachment.sql`

```sql
    is_active               BIT            NOT NULL CONSTRAINT DF_company_attachment_is_active DEFAULT 1,
    hash                    CHAR(64)       -- SHA-256 hex of the attachment (#71); NULL = not yet backfilled.
```

### 1.3 `SQL/postgres/part_attachment.sql` / `SQL/postgres/company_attachment.sql`

Same columns, `CHAR(64)` (no translation needed — see `SQL/postgres/README.md` rules table;
`CHAR(n)` is identical in both dialects).

### 1.4 New file `SQL/azure/migrations/migrate_71_attachment_hash.sql`

```sql
-- migrate_71_attachment_hash.sql
-- #71: add a SHA-256 hash to both attachment tables so duplicate uploads/links can be
-- detected. `hash` is CHAR(64) NULL holding a lowercase hex SHA-256 digest: the file's
-- content for a single-file LOCAL: link, or the link string itself for a directory-style
-- LOCAL: link, an http(s) URL, or an absolute/UNC path.
-- See docs/plans/71-attachment-content-hash.md.
--
-- BACKFILL: none here — SQL cannot read DOC_CONTROL_ROOT / SUPPLIER_FILES_ROOT. Existing
-- rows stay NULL until the one-off Go command is run against the same database:
--     cd arx_go && go run ./cmd/backfill_attachment_hash
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. `hash` is nullable with
-- no DEFAULT, so an older binary's INSERTs (which never mention it) still succeed and an
-- older binary keeps running against the migrated DB; the mismatch banner must not fire.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a human
-- to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (each ADD guarded on COL_LENGTH; safe to re-run). Runs as a single batch
-- (no `GO`). Nothing in this script references the newly added columns, so the
-- dynamic-SQL deferral of migrate_743/migrate_799 is not needed. Not an FK promotion, so
-- no orphan pre-check applies.

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.part_attachment', 'hash') IS NULL
    ALTER TABLE dbo.part_attachment ADD hash CHAR(64) NULL;
-- Postgres: ALTER TABLE part_attachment ADD COLUMN IF NOT EXISTS hash CHAR(64);

IF COL_LENGTH('dbo.company_attachment', 'hash') IS NULL
    ALTER TABLE dbo.company_attachment ADD hash CHAR(64) NULL;
-- Postgres: ALTER TABLE company_attachment ADD COLUMN IF NOT EXISTS hash CHAR(64);
```

### 1.5 `SQL/schema.md` — Table reference

Append to the `part_attachment` row (after the "Soft-delete only" sentence):

> `hash` (#71) `CHAR(64) NULL` = lowercase-hex SHA-256 identifying the attachment — the
> file's content for a single-file `LOCAL:` link, the link string itself for a
> directory-style `LOCAL:` link, an http(s) URL, or an absolute/UNC path. Recomputed
> whenever the file/URL changes; untouched by metadata-only edits. Used to warn on
> duplicate uploads/links against other **active** rows of the same table (`part_attachment`
> and `company_attachment` are checked independently). NULL = a pre-#71 row not yet
> backfilled by `arx_go/cmd/backfill_attachment_hash`. Run
> `SQL/azure/migrations/migrate_71_attachment_hash.sql`.

`company_attachment` has no row in the Table reference table today — **add one**:

| `company_attachment` | `supplier_attachment_id` | File/URL attachments on a company. `supplier_id` → `company.id` (logical, no FK). `file_path` holds a `LOCAL:` path or an http(s) URL; `LOCAL:` resolves against `SUPPLIER_FILES_ROOT` (falling back to `DOC_CONTROL_ROOT`), not `DOC_CONTROL_ROOT` like `part_attachment`. Soft-delete only (`is_active=0`). `hash` (#71) — same semantics as `part_attachment.hash`; checked only against other `company_attachment` rows. Run `SQL/azure/migrations/migrate_71_attachment_hash.sql`. |

### 1.6 `SQL/azure/seed_test_data.sql` + `SQL/postgres/seed_test_data.sql`

**No change required.** The four seeded `part_attachment` rows (8101-8104) are all
`https://` URLs; leaving `hash` NULL is the correct representation of "not yet backfilled"
and exercises the NULL-tolerant path. No `company_attachment` rows are seeded at all.

*Note for the implementer:* if an integration test wants a pre-existing hash to collide
against, seed it in the test itself (`seedThrowawayAttachment`, `integration_test.go:5093`)
rather than in `seed_test_data.sql` — that keeps the fixed-ID reference set stable.

---

## 2. Go — hashing + lookup helpers (`arx_go/attachments.go`)

New imports: `crypto/sha256`, `encoding/hex`.

```go
// hashBytes returns the lowercase-hex SHA-256 of data. Used where the bytes are
// already in memory (clipboard paste, DigiKey import) so the file isn't re-read.
func hashBytes(data []byte) string

// hashLinkString returns the lowercase-hex SHA-256 of an attachment link string.
// Used for directory-style LOCAL: links, http(s) URLs, absolute/UNC paths, and as
// the fallback when a LOCAL: file can't be read.
func hashLinkString(link string) string

// computeAttachmentHash returns the hash identifying one attachment link (#71):
// the file's content for a single-file LOCAL: link under root, or the link string
// itself for a directory-style LOCAL: link, an http(s) URL, an absolute path, or a
// LOCAL: file that is missing/unreadable. root is the filesystem root the table's
// LOCAL: links resolve against — DocControlRoot for part_attachment,
// companyAttachmentRoot() for company_attachment. Never returns "".
func computeAttachmentHash(root, link string) string
```

`computeAttachmentHash` body (streams, so a large PDF isn't loaded whole):

```go
if root == "" || !urlutil.IsLocalFile(link) || urlutil.IsLocalDir(link) {
    return hashLinkString(link)
}
rel := strings.ReplaceAll(urlutil.StripLocalPrefix(link), "\\", "/")
path, ok := safePath(root, rel)   // arx_go/files.go:30 — same guard deadLocalTarget uses
if !ok {
    return hashLinkString(link)
}
f, err := os.Open(path)
if err != nil {
    return hashLinkString(link)
}
defer f.Close()
sum := sha256.New()
if _, err := io.Copy(sum, f); err != nil {
    return hashLinkString(link)
}
return hex.EncodeToString(sum.Sum(nil))
```

Root resolution helper (mirrors `ServeSupplierFile` at `files.go:116` and `checkDeadLinks`
at `utilities.go:133`):

```go
// companyAttachmentRoot is the filesystem root company_attachment LOCAL: links
// resolve against: SUPPLIER_FILES_ROOT, falling back to DOC_CONTROL_ROOT.
func (h *Handler) companyAttachmentRoot() string
```

Duplicate lookup:

```go
// duplicateAttachment identifies the existing active attachment a pending one
// collides with, for the warning banner's link.
type duplicateAttachment struct {
    ID    int
    Label string // part number, or company name
    URL   string // "/part/<id>/attachments" or "/supplier/<id>/attachments"
}

// findDuplicatePartAttachment returns the oldest active part_attachment whose hash
// matches, excluding excludeID (0 = exclude nothing, i.e. the create path). Returns
// nil when hash is empty or nothing matches. Soft-deleted rows are never compared.
func (h *Handler) findDuplicatePartAttachment(ctx context.Context, hash string, excludeID int) (*duplicateAttachment, error)

// findDuplicateCompanyAttachment is the company_attachment equivalent. The two
// tables are checked independently — a match in one never flags against the other.
func (h *Handler) findDuplicateCompanyAttachment(ctx context.Context, hash string, excludeID int) (*duplicateAttachment, error)
```

Queries (dialect-portable `TOP`/`LIMIT` per `reports.go:186`):

```go
// part
fmt.Sprintf(`SELECT %sa.id, p.id, p.part_number
    FROM %s a JOIN %s p ON p.id = a.part_id
    WHERE a.is_active = %s AND a.hash = @p1 AND a.id <> @p2
    ORDER BY a.id`+h.dia().LimitClause("1"),
    h.dia().TopClause("1"), h.cfg.AttachmentsTable(), h.cfg.PartsTable(), h.dia().BoolLiteral(true))

// company
fmt.Sprintf(`SELECT %sa.supplier_attachment_id, c.id, c.name
    FROM %s a JOIN %s c ON c.id = a.supplier_id
    WHERE a.is_active = %s AND a.hash = @p1 AND a.supplier_attachment_id <> @p2
    ORDER BY a.supplier_attachment_id`+h.dia().LimitClause("1"),
    h.dia().TopClause("1"), h.cfg.CompanyAttachmentsTable(), h.cfg.CompanyTable(), h.dia().BoolLiteral(true))
```

Both must guard `if hash == "" { return nil, nil }` and pass `excludeID` (0 on create) so
`a.id <> 0` matches everything.

---

## 3. Go — part attachment handlers (`arx_go/parts.go`)

### 3.1 `insertAttachmentRow` — add the column

```go
func (h *Handler) insertAttachmentRow(ctx context.Context, partID, fileName, rev, category string, oID any, comment string, supplierPartID, mfgPartID any, hash string) error
```

```sql
INSERT INTO %s (part_id, file_name, part_revision, category, sort_order, comment, supplier_part_id, mfg_part_id, hash)
VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9)
```

Both call sites update: `PartAttachmentCreate` (`parts.go:1738`) and
`importAttachmentBatch` (`attachments.go:441`, which passes
`computeAttachmentHash(h.cfg.DocControlRoot, in.FileName)` and does **not** dup-check — Q3a).

### 3.2 `PartAttachmentCreate` (`parts.go:1658`)

**(a)** New branch at the very top, before the `batch_dir` branch — the Cancel action from a
duplicate warning (Q2a):

```go
if link := fv(r, "discard_import"); link != "" {
    if urlutil.IsLocalFile(link) && !urlutil.IsLocalDir(link) {
        // No row was ever inserted for this file; remove it unless some other
        // active row already links the same name.
        _ = h.deleteAttachmentFileIfUnshared(r.Context(), h.cfg.AttachmentsTable(), "id", "file_name",
            0, link, h.cfg.DocControlRoot, urlutil.StripLocalPrefix(link))
    }
    http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
    return
}
```

**(b)** Between the `in.Collision` check (`parts.go:1733`) and `insertAttachmentRow`:

```go
hash := computeAttachmentHash(h.cfg.DocControlRoot, in.FileName)
if fv(r, "confirm_duplicate") != "1" {
    dup, err := h.findDuplicatePartAttachment(r.Context(), hash, 0)
    if err != nil {
        h.renderError(w, r, "Error checking for duplicate attachments: "+err.Error())
        return
    }
    if dup != nil {
        h.renderPartAttachments(w, r, id, map[string]any{"DuplicateWarning": map[string]string{
            "FileName": in.FileName,
            "DupLabel": dup.Label,
            "DupURL":   dup.URL,
            "Category": category, "Rev": rev, "OrderID": fv(r, "order_id"),
            "Comment": comment, "VendorScope": fv(r, "vendor_scope"),
            // Imported="1" means we copied a file into Doc Control for this
            // submission, so Cancel must discard it.
            "Imported": boolStr(upload != nil && urlutil.IsLocalFile(in.FileName)),
        }})
        return
    }
}
```

(`boolStr` → inline `if ... { "1" } else { "" }`; no new helper needed.)

Then `h.insertAttachmentRow(..., hash)`.

### 3.3 `PartAttachmentUpdate` (`parts.go:1745`)

**(a)** Same `discard_import` guard is **not** needed here — Update's Cancel goes back to the
edit form; but the *duplicate-warning* Cancel on an edit must still discard a
just-copied file. Reuse the same branch shape at the top of `PartAttachmentUpdate`, then
redirect to `?edit={attID}#edit-form`.

**(b)** After `fileChanged := in.FileName != "" && in.FileName != oldFileName`
(`parts.go:1800`):

```go
var hash string
if fileChanged {
    hash = computeAttachmentHash(h.cfg.DocControlRoot, in.FileName)
    if fv(r, "confirm_duplicate") != "1" {
        dup, err := h.findDuplicatePartAttachment(r.Context(), hash, attIDInt)
        if err != nil { /* renderError */ }
        if dup != nil {
            // same DuplicateWarning map, plus "AttID": attID so the re-post targets
            // /part/{id}/attachments/{attID} and renderPartAttachments re-opens the
            // Edit form (it already falls back to ImportCollision["AttID"] when there
            // is no ?edit= param — extend that fallback to DuplicateWarning["AttID"]).
            return
        }
    }
}
```

**(c)** The `fileChanged` UPDATE gains `, hash=@p8` (renumbering the id placeholder to
`@p9`); the metadata-only UPDATE is unchanged.

### 3.4 `renderPartAttachments` (`parts.go:1599-1607`)

Extend the existing `editID` fallback so a duplicate warning raised from the Edit form keeps
that form open:

```go
if editID == "" {
    if ic, ok := extra["ImportCollision"].(map[string]string); ok {
        editID = ic["AttID"]
    }
}
if editID == "" {
    if dw, ok := extra["DuplicateWarning"].(map[string]string); ok {
        editID = dw["AttID"]
    }
}
```

---

## 4. Go — supplier attachment handlers (`arx_go/suppliers.go`)

### 4.1 `SupplierAttachmentCreate` (`suppliers.go:578`)

- `discard_import` branch at the top, using `h.companyAttachmentRoot()` and
  `deleteAttachmentFileIfUnshared(ctx, h.cfg.CompanyAttachmentsTable(), "supplier_attachment_id", "file_path", 0, link, h.companyAttachmentRoot(), ...)`.
- Before the INSERT:

```go
hash := computeAttachmentHash(h.companyAttachmentRoot(), filePath)
if strings.TrimSpace(r.FormValue("confirm_duplicate")) != "1" {
    dup, err := h.findDuplicateCompanyAttachment(r.Context(), hash, 0)
    ...
    if dup != nil {
        h.renderSupplierAttachments(w, r, id, map[string]any{"DuplicateWarning": ...})
        return
    }
}
```

> **Refactor note (required, minimal):** `SupplierAttachments` (`suppliers.go:487`) currently
> renders directly with no `extra map[string]any` seam, so there is no way to re-render the
> page with a banner. Split it exactly like `PartAttachments`/`renderPartAttachments`:
> `SupplierAttachments` becomes a one-liner delegating to a new
> `func (h *Handler) renderSupplierAttachments(w http.ResponseWriter, r *http.Request, id string, extra map[string]any)`
> that ends with `maps.Copy(data, extra)`. No other behavior change.

- INSERT gains `hash`:
  `INSERT INTO %s (supplier_id, file_path, notes, sort_order, hash) VALUES (@p1,@p2,@p3,@p4,@p5)`

### 4.2 `SupplierAttachmentUpdate` (`suppliers.go:631`)

- Same `fileChanged`-gated hash + dup check (excluding `attID`).
- `fileChanged` UPDATE gains `, hash=@pN`.

> **Pre-existing bug, do not fix here (mention only):** `SupplierAttachmentUpdate`'s
> `deleteAttachmentFileIfUnshared` call (`suppliers.go:683`) passes `h.cfg.DocControlRoot`,
> but supplier files live under `SUPPLIER_FILES_ROOT` (`saveSupplierUpload`,
> `ServeSupplierFile`). Out of scope for #71 — worth its own issue.

---

## 5. Go — non-warning write paths (Q4)

| Site | Change |
|---|---|
| `api.go:313` `APIPartPasteAttachment` | INSERT gains `hash` = `hashBytes(data)` (the decoded image bytes are already in hand) |
| `api.go:385` `APIPartPasteAttachmentReplace` | UPDATE gains `hash=@pN` = `hashBytes(data)` |
| `api.go:533` `upsertGeneratedAttachment` | Add a `hash string` param; both the INSERT and the UPDATE set it. `saveGeneratedAttachment` (`api.go:575`) passes `hashBytes(data)` |
| `sourcing.go:264` `applyDigiKeyImportExtras` | INSERT gains `hash` = `hashBytes(pf.data)` |

None of these show a warning banner.

---

## 6. Templates

### 6.1 `arx_go/templates/parts/part_attachments.html`

New block immediately after the `{{if .ImportCollision}}…{{end}}` block (line 68). Bootstrap
`alert alert-warning`, matching the collision banner's structure:

```html
{{if .DuplicateWarning}}
<div class="alert alert-warning">
    <p class="mb-2">This file or link is identical to an attachment that already exists on
        <a href="{{.DuplicateWarning.DupURL}}">{{.DuplicateWarning.DupLabel}}</a>.
        Nothing has been saved yet.</p>
    <form method="post"
          action="{{if .DuplicateWarning.AttID}}/part/{{.Part.ID}}/attachments/{{.DuplicateWarning.AttID}}{{else}}/part/{{.Part.ID}}/attachments{{end}}"
          class="d-inline">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="hidden" name="FILFileName" value="{{.DuplicateWarning.FileName}}">
        <input type="hidden" name="category" value="{{.DuplicateWarning.Category}}">
        <input type="hidden" name="FILPNRev" value="{{.DuplicateWarning.Rev}}">
        <input type="hidden" name="order_id" value="{{.DuplicateWarning.OrderID}}">
        <input type="hidden" name="comment" value="{{.DuplicateWarning.Comment}}">
        <input type="hidden" name="vendor_scope" value="{{.DuplicateWarning.VendorScope}}">
        <input type="hidden" name="confirm_duplicate" value="1">
        <button type="submit" class="btn btn-sm btn-warning">Add anyway</button>
    </form>
    <form method="post" action="/part/{{.Part.ID}}/attachments" class="d-inline">
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        {{if .DuplicateWarning.Imported}}
        <input type="hidden" name="discard_import" value="{{.DuplicateWarning.FileName}}">
        {{end}}
        <button type="submit" class="btn btn-sm btn-secondary">Cancel</button>
    </form>
</div>
{{end}}
```

Notes:
- No `enctype` needed — the re-post carries `FILFileName`, never a file, so
  `resolveAttachmentFileInput` takes its `upload == nil` manual-link branch and the
  already-copied file is reused as-is.
- The Cancel form posts to the **create** route even on an edit-triggered warning: its only
  job is discarding the orphan file, and `discard_import` returns early before any row work.
  When `Imported` is empty it posts nothing meaningful and just redirects back.

### 6.2 `arx_go/templates/suppliers/supplier_attachments.html`

Same banner, adapted: action `/supplier/{{.Supplier.ID}}/attachments[/{{.DuplicateWarning.AttID}}]`,
fields `file_path` / `notes` / `sort_order` instead of `FILFileName` / `comment` / `order_id`,
`{{.DuplicateWarning.DupLabel}}` linking to the other company's attachments page. Insert
after the `{{if .Error}}` line (line 11).

### 6.3 JS

**No changes to `arx_go/static/parts/paste_attachment.js` or the inline scripts in
`part_attachments.html`.** The whole flow is server-rendered.

---

## 7. Backfill command — `arx_go/cmd/backfill_attachment_hash/main.go`

New file, `package main`, ~90 lines. Deliberately standalone: it imports only
`arx/arxlib/config` and `arx/arxlib/db` (not `package main` in `arx_go`), so the hashing
rule is the one thing it must restate — keep it a literal copy of `computeAttachmentHash`'s
logic and cross-reference it in both comments.

```go
// Command backfill_attachment_hash fills part_attachment.hash and
// company_attachment.hash for rows written before #71. Run once per database,
// from the arx_go directory so config/local.json resolves:
//
//     cd arx_go
//     go run ./cmd/backfill_attachment_hash            # dry run: reports counts only
//     go run ./cmd/backfill_attachment_hash -apply     # writes
//
// Reads DOC_CONTROL_ROOT / SUPPLIER_FILES_ROOT the same way the app does, which is
// why this is a Go command and not part of the SQL migration. Set TEST_MODE=true
// (or test_engine/test_db_name in local.json) to target ArxDev.
```

Behavior:

1. `cfg := arxbase.Load("backfill")`; `db, _, err := arxdb.Connect(cfg.DBEngine(), cfg.DSN())`.
2. **Print the target** (`cfg.DBName()` / server / TestMode) and require `-apply` to write —
   the safety habit CLAUDE.md asks for on any ad-hoc DSN.
3. `SELECT id, file_name FROM part_attachment WHERE hash IS NULL` → compute →
   `UPDATE part_attachment SET hash=@p1 WHERE id=@p2`. Row-at-a-time; the tables are small.
4. Same for `company_attachment` (`supplier_attachment_id`, `file_path`,
   `SupplierFilesRoot` with `DocControlRoot` fallback).
5. Includes `is_active = 0` rows: they're excluded from *comparison*, but a re-activated row
   should not silently have a NULL hash.
6. Prints per-table `updated / skipped (unreadable file → string hash) / failed` counts.

Cross-platform: pure `os`/`crypto`, no cgo, no systray — compiles and runs on Linux.

---

## 8. Tests

### 8.1 Unit — `arx_go/attachments_test.go`

Table-driven over `computeAttachmentHash` against a `t.TempDir()` root:

| Case | Expectation |
|---|---|
| `LOCAL:a.txt`, file present | equals `sha256(contents)`, **not** `sha256("LOCAL:a.txt")` |
| `LOCAL:b.txt` with identical contents, different name | same hash as above (the point of the feature) |
| `LOCAL:folder\` | equals `sha256("LOCAL:folder\\")` even when the folder exists |
| `https://example.com/x.pdf` | equals `sha256(link)` |
| `\\server\share\x.pdf` | equals `sha256(link)` |
| `LOCAL:missing.txt` | falls back to `sha256(link)`, non-empty |
| `LOCAL:..\escape.txt` | `safePath` rejects → `sha256(link)`, non-empty |
| `root == ""` | `sha256(link)` |

Plus `hashBytes`/`hashLinkString` produce 64 lowercase hex chars.

### 8.2 Integration — `arx_go/integration_test.go` (build tag `integration`)

New `TestIntegration_AttachmentDuplicateHash`, following `TestIntegration_PartLifecycle`
(line 284) and using `seedThrowawayAttachment` (line 5093):

1. Create a throwaway part; `PartAttachmentCreate` with
   `FILFileName=http://example.test/dup-<nanos>` → 302, row has a non-NULL 64-char `hash`.
2. Create the *same* URL on a **second** throwaway part → **200** (re-render, not 302), body
   contains the warning text; `SELECT COUNT(*)` on the second part is 0.
3. Re-post with `confirm_duplicate=1` → 302, row exists, hashes equal.
4. Soft-delete the first row, repeat step 2 on a third part → **302** (soft-deleted rows are
   not compared).
5. Metadata-only `PartAttachmentUpdate` (change comment) leaves `hash` unchanged.
6. `SupplierAttachmentCreate` with the *same* URL on a company → **302**, not flagged
   (the two tables are checked independently).

Note in the plan for the implementer: this test touches `part_attachment`/`company_attachment`
so it must run against ArxDev with `ARX_TEST_DSN`, and `test_engine` in
`arx_go/config/local.json` must not be `postgres` (see MEMORY.md).

---

## 9. Docs / changelog

- `docs/conventions.md` — add a short subsection under "Attachment URL conventions"
  documenting the hash rule (the table in §Behavior spec above), since that file is the
  canonical `FILFileName` reference.
- `CHANGELOG.md` — one `### Added` line under the new version entry:
  `- Duplicate-attachment detection: attachments now carry a SHA-256 hash and the app warns before saving a file or link that already exists ([#71](https://github.com/Jolls/arx/issues/71))`
- `arx_go/RELEASE_NOTES.md` — one line under NEW FEATURES:
  `  Duplicate Attachment Warning` / `Arx now tells you when a file or link you're adding is already attached somewhere else.`

---

## 10. Verification (success criteria)

1. `cd arx_go && go build ./... && go vet ./... && go test ./...` clean (includes the new
   `cmd/` package and the unit tests in 8.1).
2. Human runs `SQL/azure/migrations/migrate_71_attachment_hash.sql` against **ArxDev**;
   re-running it is a no-op.
3. Human runs `cd arx_go && go run ./cmd/backfill_attachment_hash -apply`; the four seeded
   `part_attachment` rows come back with 64-char hashes.
4. Integration sweep: `go test -tags integration ./arx_go/...` with `ARX_TEST_DSN` pointed
   at ArxDev — new test plus the existing attachment tests pass.
5. Manual (user, `go run .` in `arx_go/`):
   - Add the same URL to two different parts → warning banner with a working link to the
     first part; **Add anyway** saves; **Cancel** returns cleanly.
   - Upload the same file to two different parts → warning; **Cancel** leaves **no** stray
     copy in `DOC_CONTROL_ROOT`.
   - Edit an attachment's comment only → saves silently, `hash` unchanged in the DB.
   - Multi-file batch upload → all files import with hashes, no warning interrupts (Q3a).
   - Same file on a vendor and on a part → **no** warning (tables independent).

## 11. Not in scope

- No index on `hash` (Q7).
- No dedup *report* or bulk merge/cleanup tool.
- No back-pressure on the batch path (Q3).
- The `SupplierAttachmentUpdate` wrong-root bug noted in §4.2 — pre-existing, file separately.
