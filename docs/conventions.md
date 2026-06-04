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

**Invariant:** values stored in `FILFileName` are trimmed and use the uppercase prefix `LOCAL:`. The app never writes lowercase variants. Helper functions in `arxlib/urlutil` (`IsLocalFile`, `IsLocalDir`, `IsHTTPURL`, `LocalFileURL`, `LocalDirURL`) accept any case defensively but the stored data is always uppercase.

---

## PO folder convention

When a new PO is created, the app auto-creates a folder in `PO_FOLDER_ROOT` named:

```
<PO number> <company SUSupplierCode>
```

Folder lookup matches any directory whose name **starts with** the PO number (the company code suffix may vary). In test mode, `-testmode` is appended to the folder name.
