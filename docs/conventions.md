# Application Conventions

Design decisions and runtime conventions that affect how the app handles files, folders, and external resources.

---

## Attachment URL conventions (`FILFileName` field)

The `FILFileName` column in `FIL` (and the equivalent field in `company_attachment`) stores one of three formats:

| Format | Example | Behavior |
|--------|---------|----------|
| `https://...` or `http://...` | `https://example.com/spec.pdf` | Opens in browser (external link) |
| `LOCAL:path\to\file` | `LOCAL:Engineering\spec.pdf` | Served via `GET /local/*` from `DOC_CONTROL_ROOT` |
| `LOCAL:path\to\folder\` (trailing slash or backslash) | `LOCAL:Engineering\drawings\` | Directory listing via `GET /local-dir/*` from `DOC_CONTROL_ROOT` |

**Invariant:** values stored in `FILFileName` are trimmed and use the uppercase prefix `LOCAL:`. The app never writes lowercase variants. Helper functions in `internal/urlutil` (`IsLocalFile`, `IsLocalDir`, `IsHTTPURL`, `LocalFileURL`, `LocalDirURL`) accept any case defensively but the stored data is always uppercase.

### Content hash / duplicate detection (#71)

Both `part_attachment` and `company_attachment` carry a `hash` column (`CHAR(64)`, lowercase-hex SHA-256), populated on every write:

| Attachment shape | Hash input |
|---|---|
| `LOCAL:file.pdf` (single file) | SHA-256 of the file's **bytes** |
| `LOCAL:folder\` (directory-style, trailing `/` or `\`) | SHA-256 of the **link string** |
| `http://` / `https://` | SHA-256 of the **link string** |
| Absolute path (`\\server\...`, `C:\...`, `file://`) | SHA-256 of the **link string** |
| `LOCAL:` file that is missing/unreadable | SHA-256 of the **link string** (fallback) |

The dup check is per-table, system-wide (compares against all `is_active = 1` rows in the same table, never across `part_attachment`/`company_attachment`), and excludes the row being edited. It's recomputed whenever the file/URL changes; a metadata-only edit leaves `hash` untouched. A match shows a dismissible warning before the row is saved — the user can "Add anyway" or cancel.

---

## Imported attachment naming convention

When a file is uploaded via the file picker on a part's Attachments tab, it
is copied into `DOC_CONTROL_ROOT` (as the bytes received in the upload — the
browser never exposes a source path) and renamed. The generated name is:

```
<PartNumber> <Rev> <Title> <Category>.<ext>
```

- Space-separated. Blank (or whitespace-only) parts are skipped so separators
  never double up. The part number is always present; rev, title, and category
  are optional.
- Title is truncated to 20 characters (`titleMaxLen` in `arx_go/attachments.go`).
- Filesystem-illegal characters (`<>:"/\|?*` and control chars) are replaced with
  `-`, and the result is always a bare filename, so it stays inside
  `DOC_CONTROL_ROOT`.
- Stored in `FILFileName` as `LOCAL:<name>` (no subfolder).
- If a file with the generated name already exists, the copy is rejected and the
  user is offered a **Link to existing file** action, which creates the
  attachment row pointing at the existing file without copying.

---

## Supplier attachment upload

A vendor's Attachments tab also accepts a direct file upload (beside the existing URL/path text
field), written into `SUPPLIER_FILES_ROOT` (no fallback to `DOC_CONTROL_ROOT` — the upload is
rejected with an error if `SUPPLIER_FILES_ROOT` isn't configured). Unlike part attachments, there's
no part-number/rev naming convention to apply: the uploaded file keeps its sanitized original name,
auto-suffixed with `" (2)"`, `" (3)"`, ... on a name collision (no "Link to existing file" prompt).

## Pasted-image attachment naming convention

The **Grab from clipboard** button on a part's Add Attachment form (enabled only
when Category = "Photo") uploads clipboard image bytes directly, without a
source file path. It uses the same naming convention as an imported file
(`<PartNumber> <Rev> <Title> Photo.<ext>`, via `buildAttachmentFileName`), but
since there is no user-typed title to dedupe on, repeated pastes for the same
part/rev auto-append `" (2)"`, `" (3)"`, ... before the extension instead of
surfacing the import collision prompt.

`internal/urlutil.IsImage` (extensions: `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`,
case-insensitive) is the canonical check for whether an attachment is a
displayable image — used to filter the part detail page's Photos thumbnail
grid and to show a hover-preview popup on the Attachments list page.

---

## Test-record image naming convention

A test-record step whose `pf_type = "attach"` shows a **Grab from clipboard**
button on its result cell (record edit page). Pasting an image writes it to
disk and returns its filename; the filename is stored as the step's plain
`result.result` value (no `LOCAL:` prefix — this is not a
`part_attachment`/`company_attachment` row, just a filename string) and
persisted on **Save**, alongside the rest of the record's edits.

Folder and filename:

```
IMAGE_ROOT\<form part_number>\SN<Serial>_rID<recordID>_tID<testID>_YYYYMMDD_HHMMSS.<ext>
```

- Served via `GET /images/*` (`arx_go/trfiles.go`) from `IMAGE_ROOT`.
- Folder is namespaced by the **form's** part number (unlike the flat
  `DOC_CONTROL_ROOT` used for part attachments); the folder is created if it
  doesn't exist yet.
- The filename keeps the legacy VBA field order (`SN`/`rID`/`tID`) but drops
  all dashes, using underscores as the only separator — including in the
  trailing write-time timestamp — so literal dashes never visually blend with
  `sanitizeFileNamePart`'s dash-for-illegal-char substitution.
- The timestamp is what makes repeat pastes on the same step unique; there is
  no `" (2)"`-style auto-suffix like the part-attachment Photo flow.
- **Legacy files** written by the old VBA app use the dashed shape
  `SN-..._rID-..._tID-...` and are not migrated — `imageResult()` in
  `render_tr.go` recognizes both the old dashed and new no-dash shapes, so
  both keep rendering.
- The old VBA convention gated picture-paste on a step's `parameter` field
  starting with `"Screenshot/File"`; the Go app instead uses the dedicated
  `pf_type = "attach"` value (see `SQL/azure/migrations/migrate_screenshot_file_to_attach.sql`
  for migrating existing forms off the legacy convention).

## PO folder convention

When a new PO is created, the app auto-creates a folder in `PO_FOLDER_ROOT` named:

```
<PO number> <company SUSupplierCode>
```

Folder lookup matches any directory whose name **starts with** the PO number (the company code suffix may vary). In test mode, `-testmode` is appended to the folder name.
