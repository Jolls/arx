# Company logo in the UI and PO PDFs (#538)

> Per-feature design doc. Implements [#538](https://github.com/Jolls/arx-legacy/issues/538): surface the shop's
> own company logo in the app UI and on printed POs.

## Context

The PO print document ([po_print.html](../../arx_go/templates/pm/po_print.html)) and the app UI carry no
company identity — the header shows only the product name ("Arx") and a favicon-sized icon, and the PO
print sheet has no letterhead. Issue #538 asks to surface the shop's own **company logo** in the UI and,
specifically, on the PO PDFs sent to suppliers.

**Key facts that shaped the approach:**
- There is **no PDF library**. "PO PDFs" are the browser's print-to-PDF of the standalone HTML template
  `po_print.html` (`POPrint` handler → `renderPrint`). A logo is just an `<img>` in that template.
- There is **no buyer-company profile** — the `company` table is actually the suppliers/vendors table
  (`CompanyTable()`; `POPrint` reads `SUSupplierCode` from it). So the logo is an app-wide branding
  asset, not per-record data.

**Decisions taken:**
- **Storage:** a dedicated logo **upload in Settings**, stored in the DB as a base64 **data URI** in
  `app_config` key `company_logo` (same key/value store as `attachment_categories`). Chosen over a
  bundled static file (no rebuild to change) and over a `company_attachment` file (self-contained in the
  DB, no `DOC_CONTROL_ROOT` dependency, and — because it's an inline data URI — the logo still renders in
  a PO PDF that was **saved offline**).
- **Placement:** top header bar (both `pm/` and `tr/` layouts), the login page, and the PO print sheet.

## Approach

Store the logo as a data URI string in `app_config`, cache it on the `Handler` (mirroring the existing
cached `releaseNotes`/`schemaMismatch` fields so the hot render path stays DB-free), inject it into the
template data maps, and render a conditional `<img>` in each of the three surfaces.

### 1. Go — cache + loader (`arx_go/handlers.go`)
- Add field `companyLogo string` to the `Handler` struct (next to `releaseNotes`, line ~30).
- Add method `loadCompanyLogo(ctx)`: sets `h.companyLogo = h.appConfigGetOr(ctx, "company_logo", "")`
  (safe when `h.db == nil` — `appConfigGet` already returns `"", nil`). Model it on
  `CheckSchemaVersion` (handlers.go:93).
- Inject into both render paths:
  - `render` (handlers.go:176-183): `m["CompanyLogo"] = h.companyLogo`.
  - `renderTR` (render_tr.go:18-24): `m["CompanyLogo"] = h.companyLogo`.

### 2. Go — load at the two DB-connect sites
`loadCompanyLogo` must run wherever the DB comes up, exactly like `CheckSchemaVersion`:
- Startup: `arx_go/main.go:46`, right after `h.CheckSchemaVersion(context.Background())`.
- Reconnect: `arx_go/settings.go:237`, right after `h.CheckSchemaVersion(r.Context())`.

### 3. Go — upload / remove handlers (`arx_go/settings.go`)
Add two handlers alongside `SettingsAttachmentCategoriesSave` (settings.go:151), each with its own
endpoint so the partial form can't blank the main-form fields:
- `SettingsCompanyLogoSave`:
  - `r.ParseMultipartForm(1 << 20)` (1 MB cap — the data URI is inlined into every page, so keep it small).
  - `file, _, err := r.FormFile("company_logo")`; `data, _ := io.ReadAll(file)` (guard the size).
  - Detect type with `http.DetectContentType(data)` and validate against an allowed raster set. **Reuse
    the existing `pasteImageExts` map** in `arx_go/api.go:197` (`image/png|jpeg|webp|gif`) as the
    allow-list; reject others with a settings error. (SVG is out of scope — `DetectContentType` doesn't
    reliably identify it.)
  - Build `dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)`.
  - `h.appConfigSet(ctx, "company_logo", dataURI)` then `h.companyLogo = dataURI` (refresh cache).
  - Redirect to `/settings`.
- `SettingsCompanyLogoRemove`: `h.appConfigSet(ctx, "company_logo", "")` + `h.companyLogo = ""`; redirect.

### 4. Go — routes (`arx_go/main.go`)
Register next to the existing settings POSTs (main.go:121):
```go
r.Post("/settings/company-logo", h.SettingsCompanyLogoSave)
r.Post("/settings/company-logo/remove", h.SettingsCompanyLogoRemove)
```

### 5. Go — login + settings data maps
- Login: in `renderLogin` (auth.go:327) add `data["CompanyLogo"] = h.companyLogo` (single choke point
  for the login template).
- Settings preview: add `"CompanyLogo": h.companyLogo` to the map in `settingsData` (settings.go:99) so
  the Configuration tab can show the current logo.

### 6. Go — PO print (`arx_go/pos.go`)
In `POPrint` (pos.go:936) add `"CompanyLogo": h.companyLogo` to the `renderPrint` data map.

### 7. Templates
Render a conditional image in each surface (guard with `{{if .CompanyLogo}}` so nothing shows when unset):
- **`templates/pm/layout.html`** (header ~line 15) and **`templates/tr/layout.html`** (same): add
  `{{if .CompanyLogo}}<img src="{{.CompanyLogo}}" alt="" class="company-logo">{{end}}` in the
  `.header-actions` group (right side, before the user name).
- **`templates/pm/login.html`** (above `.login-title`, ~line 19): centered logo, `max-height` ~64px.
- **`templates/pm/po_print.html`** (top-left `<div>` of `.po-header`, ~line 71, above the `<h1>`):
  `{{if .CompanyLogo}}<img src="{{.CompanyLogo}}" alt="" style="max-height:60px; margin-bottom:6px;">{{end}}`.
- **`static/app.css`**: add `.company-logo { max-height: 24px; width: auto; }` (header is 36px tall).
  login.html and po_print.html carry their own inline/scoped styles, so no shared CSS needed there.

### 8. Settings UI (`templates/pm/settings.html`)
Add a "Company Logo" section in the Configuration tab (after Attachment Categories, ~line 275):
- `<form method="post" action="/settings/company-logo" enctype="multipart/form-data">` with the CSRF
  hidden input, `<input type="file" name="company_logo" accept="image/*" class="form-control">`, and a
  `btn btn-primary` submit.
- If `{{.CompanyLogo}}` is set, show a preview `<img>` and a separate small
  `POST /settings/company-logo/remove` form with a `btn btn-sm btn-secondary` "Remove" button.

## What this deliberately does NOT do
- No new DB table/column, no schema-version bump (reuses `app_config`, already `VARCHAR(MAX)`).
- No SVG support, no image resizing/cropping, no per-app (pm vs tr) distinct logos.
- No static/bundled logo asset.

## Files touched
- `arx_go/handlers.go` — struct field, `loadCompanyLogo`, inject into `render`.
- `arx_go/render_tr.go` — inject into `renderTR`.
- `arx_go/main.go` — startup load + 2 routes.
- `arx_go/settings.go` — reconnect load, 2 handlers, `settingsData` field.
- `arx_go/pos.go` — inject into `POPrint`.
- `arx_go/auth.go` — inject into `renderLogin`.
- `arx_go/templates/pm/layout.html`, `tr/layout.html`, `pm/login.html`, `pm/po_print.html`, `pm/settings.html`.
- `arx_go/static/app.css`.
- `CHANGELOG.md` — one entry (per-PR).

## Verification
- `cd arx_go && go build ./... && go vet ./... && go test ./...` — must pass. `templates_parse_test.go`
  re-parses every template, catching any `{{.CompanyLogo}}` syntax error.
- Manual (user-run, since the app isn't run by the agent):
  1. `/settings` → Configuration → upload a PNG logo. Confirm the preview appears and it's stored.
  2. Check the logo shows in the PM header, the TR header, and on `/login` (sign out to see it).
  3. Open a PO → Print/Save PDF → confirm the logo is in the print header, and that a saved-to-disk
     PDF/HTML still shows the logo offline (validates the inline data-URI choice).
  4. Upload a non-image (e.g. a .txt) → confirm it's rejected with a settings error.
  5. Remove the logo → confirm all four surfaces fall back cleanly (no broken-image icon).

## Suggested test (post-implementation)
A small unit test for the content-type validation / data-URI construction: extract the mime-check +
encode into a testable helper and assert png/jpeg accepted, a bogus type rejected. Worth it because it's
silent-failure-prone logic; skip if it ends up trivially inline.

## Changelog
`- Add company logo to the UI header, login page, and PO PDFs, uploadable in Settings ([#538](https://github.com/Jolls/arx-legacy/issues/538))`
