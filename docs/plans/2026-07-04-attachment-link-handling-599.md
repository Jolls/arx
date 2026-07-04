# Plan — #599: Improve attachment link handling (`LOCAL:` / VBA-historical)

Issue: https://github.com/Jolls/arx-legacy/issues/599

## Context

Attachment links are stored as a raw heterogeneous string in `part_attachment.file_name`
and `company_attachment.file_path`. The **type is never stored** — it is inferred at render
time purely from the string prefix via `arxlib/urlutil` (`IsHTTPURL`, `IsLocalFile`,
`IsLocalDir`). Two problems follow:

1. **Only two shapes are recognized.** `http(s)://` and `LOCAL:` (relative to
   `DOC_CONTROL_ROOT`). Anything else — including the fully-qualified **absolute / UNC paths**
   (`\\server\share\file.pdf`, `C:\...`) that the old VBA app happily hyperlinked via its
   `Case Else` branch (`archive/PartsMaster/Files.bas:34`) — falls into the template
   `{{else}}` and renders as **dead, non-clickable text**.
2. **No auto-detection on input.** Values are stored verbatim, so the user must hand-type the
   `LOCAL:` prefix; a pasted raw path or a lowercase `local:` just becomes dead text.

### Decisions (confirmed with user)
- **Derived, no new column.** Type stays a pure function of the string — no schema change, no
  migration, nothing to keep in sync.
- **Third link kind = `abspath`** — UNC (`\\...`) and drive-letter (`C:\`, `Z:/`) absolute
  paths, plus `file://` URLs folded in. Distinct from `LOCAL:` (which is root-relative).
- **Auto-detect + normalize on input**, and **consolidate** the scattered classify/strip logic
  into one place.
- **Abspath UX = copy-path only.** The server does **not** stream arbitrary disk/UNC files (no
  new route, no new attack surface). The row shows the file base name plus a copy button.
- **Bare token → assume `LOCAL:`** (e.g. `spec.pdf`, `Engineering\spec.pdf` become `LOCAL:...`).

Final classifier enum: `url` · `localfile` · `localdir` · **`abspath`** · `unknown`.

### Scope note (TR is untouched)
Test-record templates (`templates/tr/*`) render step-result values, a different flow (bare
filename, no `LOCAL:`). They are **out of scope**. Do **not** touch `render_tr.go` or the tr
templates — `isAbsPath` is registered only in the pm FuncMap.

---

## Step 1 — `arxlib/urlutil/urlutil.go`: three new pure funcs

Append these to the file (after `IsLocalDir`, before `LocalFileURL` is fine). `len("LOCAL:") == 6`.

```go
// IsAbsPath reports whether val is an absolute local path: a UNC path (\\server\...),
// a drive-letter path (C:\... or C:/...), or a file:// URL. These render as copy-path
// only; the server never streams them.
func IsAbsPath(val string) bool {
	if strings.HasPrefix(val, "file://") || strings.HasPrefix(val, "\\\\") {
		return true
	}
	// drive letter: X:\ or X:/
	return len(val) >= 3 && val[1] == ':' && (val[2] == '\\' || val[2] == '/') &&
		((val[0] >= 'A' && val[0] <= 'Z') || (val[0] >= 'a' && val[0] <= 'z'))
}

// StripLocalPrefix returns val with a leading LOCAL: prefix (case-insensitive) removed.
// If val has no LOCAL: prefix it is returned unchanged.
func StripLocalPrefix(val string) string {
	if IsLocalFile(val) {
		return val[6:]
	}
	return val
}

// NormalizeLink canonicalizes a user-entered attachment link for storage:
//   - trims surrounding whitespace; empty stays empty
//   - http(s):// URLs and absolute paths (UNC/drive/file://) are stored verbatim
//   - a LOCAL: prefix (any case) is re-emitted with an uppercase LOCAL: and the original
//     remainder (trailing \ or / preserved so directory-ness survives)
//   - any other bare relative token is assumed doc-control and gets a LOCAL: prefix
func NormalizeLink(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if IsHTTPURL(v) || IsAbsPath(v) {
		return v
	}
	if IsLocalFile(v) {
		return "LOCAL:" + v[6:]
	}
	return "LOCAL:" + v
}
```

**Verify:** `cd arxlib && go build ./...`

## Step 2 — register `isAbsPath` for pm templates (one place only)

`arx_go/handlers.go`, in `pmTemplateFuncs()` (~L316, next to `"isHTTPURL"`):

```go
		"isAbsPath":      urlutil.IsAbsPath,
```

Do **not** add it to `render_tr.go`.

## Step 3 — shared copy-path partial

Add to `arx_go/templates/pm/partials.html` (a new `{{define}}` block, alongside the others).
`.` is the raw path string. `fileBaseName` and `isAbsPath` are already registered in the pm FuncMap.

```html
{{define "abspath_link"}}
<span class="d-inline-flex align-items-center gap-1">
    <span class="font-monospace text-truncate" style="font-size:0.9em; max-width:280px;" title="{{.}}">{{fileBaseName .}}</span>
    <button type="button" class="btn btn-sm btn-secondary py-0 px-1" title="Copy path: {{.}}"
            data-path="{{.}}" onclick="navigator.clipboard.writeText(this.getAttribute('data-path'))">
        <i class="bi bi-clipboard"></i>
    </button>
</span>
{{end}}
```

(`data-path` carries the value so no quote-escaping problems in `onclick`.)

## Step 4 — use the partial at every dead-text render site (6 sites)

At each site, insert an `{{else if isAbsPath $f}}{{template "abspath_link" $f}}` branch
**immediately before** the existing final `{{else}}`. Leave the existing `{{else}}` block
(the dead-text fallback) unchanged.

| File | Line | Existing final branch to keep after the new one |
|---|---|---|
| `templates/pm/part_attachments.html` | 73 | `{{else}}<span class="font-monospace" ...>{{$f}}</span>` |
| `templates/pm/supplier_attachments.html` | 42 | `{{else}}<span class="font-monospace" ...>{{$f}}</span>` |
| `templates/pm/part_detail.html` | 51 | `{{else}}<span class="part-status-value">{{$f}}</span>` |
| `templates/pm/part_detail.html` | 221 | `{{else}}{{$f}}{{end}}` (compact primary block) |
| `templates/pm/part_detail.html` | 237 | `{{else}}{{$f}}{{end}}` (compact primary block) |
| `templates/pm/supplier_detail.html` | 121 | `{{else}}{{$f}}{{end}}` (default attachment) |

Example (part_attachments.html — the `$f` variable is already in scope at each site):

```html
                        {{else if isLocalFile $f}}
                            ...unchanged...
                        {{else if isAbsPath $f}}
                            {{template "abspath_link" $f}}
                        {{else}}
                            <span class="font-monospace" style="font-size:0.9em;" title="{{$f}}">{{$f}}</span>
                        {{end}}
```

For the compact one-line sites (part_detail 221/237, supplier_detail 121) it is:
`... {{else if isAbsPath $f}}{{template "abspath_link" $f}}{{else}}{{$f}}{{end}}`.

Do **not** touch the icon-only `{{else}}<i class="bi bi-file-earmark">` fallbacks
(part_detail ~215/231) — the generic file icon is already fine for abspath.

## Step 5 — normalize on the manual-input write paths (3 sites)

**5a. `arx_go/attachments.go` `resolveAttachmentFileInput` (L173-175).** Only the manual
branch (`src == ""`). Change:

```go
	if src == "" {
		return attachmentFileInput{FileName: urlutil.NormalizeLink(fileName)}
	}
```

`NormalizeLink("")` returns `""`, so the edit-form "leave blank to keep current" behavior is
unchanged. The browse branch (below) already emits canonical `LOCAL:name` — untouched.

**5b. `arx_go/suppliers.go` `SupplierAttachmentCreate` (L494).** After the trim/empty check,
normalize before insert. Change:

```go
	filePath := strings.TrimSpace(r.FormValue("file_path"))
	if filePath == "" {
		http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
		return
	}
	filePath = urlutil.NormalizeLink(filePath)
```

**5c. `arx_go/suppliers.go` `SupplierAttachmentUpdate` (L539).** Change:

```go
	newFilePath := urlutil.NormalizeLink(strings.TrimSpace(r.FormValue("file_path")))
```

(The subsequent `newFilePath != "" && newFilePath != oldFilePath` compare still works.)

Confirm `arxlib/urlutil` is already imported in `suppliers.go`; add the import if `go build` complains.

## Step 6 — consolidate the hand-sliced `LOCAL:` strip logic (4 sites)

Pure refactor, no behavior change. Replace `oldFileName[len("LOCAL:"):]` /
`oldFilePath[len("LOCAL:"):]` with `urlutil.StripLocalPrefix(...)`:

- `arx_go/parts.go:1264` — `replaceName = urlutil.StripLocalPrefix(oldFileName)`
- `arx_go/api.go:361` — `..., urlutil.StripLocalPrefix(oldFileName))`
- `arx_go/suppliers.go:576` — `..., urlutil.StripLocalPrefix(oldFilePath))`

And `arx_go/pos.go` `localFilePath` (~L2446): replace the ad-hoc
`!strings.HasPrefix(strings.ToUpper(filename), "LOCAL:")` guard + later slice with
`urlutil.IsLocalFile` / `urlutil.StripLocalPrefix`. Read the whole function first; keep the
directory-reference early-return and the empty-root check exactly as they are.

## Step 7 — form help text

`arx_go/templates/pm/part_attachments.html:187` (the **Add** form placeholder). Change to:

```html
                           placeholder="https://…, a network/drive path (\\server\… or C:\…), or a doc-control file (spec.pdf) or folder (folder\)" required>
```

Leave the edit-form placeholder at L131-132 (`leave blank to keep current file`) alone.

---

## Tests — `arxlib/urlutil/urlutil_test.go`

Add table tests (this is the meaningful regression guard). Cover:

- `NormalizeLink`: `"https://x"`→verbatim; `"  https://x  "`→trimmed `"https://x"`;
  `""`→`""`; `"local:Eng\\a.pdf"`→`"LOCAL:Eng\\a.pdf"`; `"LOCAL:Eng\\"`→`"LOCAL:Eng\\"`
  (trailing `\` preserved); `"spec.pdf"`→`"LOCAL:spec.pdf"`;
  `"\\\\srv\\share\\a.pdf"`→verbatim; `"C:\\x\\a.pdf"`→verbatim; `"file:///c:/a.pdf"`→verbatim.
- `IsAbsPath`: true for `"\\\\srv\\s"`, `"C:\\x"`, `"z:/x"`, `"file://x"`; false for
  `"LOCAL:x"`, `"https://x"`, `"spec.pdf"`, `""`, `"C:"` (too short).
- `StripLocalPrefix`: `"LOCAL:a"`→`"a"`, `"local:a"`→`"a"`, `"https://x"`→`"https://x"`.

## Verification

- From `arxlib/`: `go test ./...` (new urlutil tests pass).
- From `arx_go/`: `go build ./...`, `go vet ./...`, `go test ./...` pass.
- Manual (user), part Attachments tab:
  1. Add `\\server\share\spec.pdf` → row shows base name + a **copy** button (not dead text); button copies the full path.
  2. Add `local:Engineering\drawing.pdf` (lowercase) → stored/rendered as `LOCAL:` doc-control link.
  3. Add bare `spec.pdf` → stored as `LOCAL:spec.pdf`.
  4. Existing `http(s)://` and `LOCAL:` file/dir rows render exactly as before (no regression).
- Repeat (1)/(4) on a supplier's Attachments tab.

## Out of scope
- No `link_type` DB column, no migration, no schema-doc changes.
- No server route to open/stream absolute paths (copy-path only).
- No changes to `render_tr.go` or `templates/tr/*` (test-record result values).
