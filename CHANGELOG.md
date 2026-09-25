# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.7.72] - 2026-09-25
### Changed
- Config, DB connection and cached settings data (schema status, logo, part categories) now live in one immutable snapshot swapped atomically on save, removing data races between Settings saves and in-flight requests; CI now runs `go test -race` ([#196](https://github.com/Jolls/arx/issues/196))

## [0.7.71] - 2026-09-24
### Changed
- Templates are now parsed once at startup instead of on every request; a broken template fails at startup ([#197](https://github.com/Jolls/arx/issues/197))

## [0.7.70] - 2026-09-24
### Changed
- Merged `arxlib` and `go.work` into a single Go module at the repo root; `arxlib/*` moved to `internal/*`, CI/build/dependabot simplified ([#195](https://github.com/Jolls/arx/issues/195))

## [0.7.69] - 2026-09-23
### Added
- `.github/dependabot.yml`: weekly Dependabot update checks for the Go modules (`arx_go`, `arxlib`) and GitHub Actions ([#166](https://github.com/Jolls/arx/issues/166))

## [0.7.68] - 2026-09-23
### Security
- `build.bat` now builds with `-trimpath`, so `Arx.exe` no longer embeds the builder's local source and module-cache paths ([#163](https://github.com/Jolls/arx/issues/163))

## [0.7.67] - 2026-09-23
### Security
- Requests whose `Host` header isn't `localhost`, `127.0.0.1` or `[::1]` on the app's port now get 421, closing a DNS-rebinding path to the loopback listener ([#167](https://github.com/Jolls/arx/issues/167))

### Changed
- Refreshed outsider-facing docs: Go version, secrets location, `SCHEMA.md` link, contribution licensing, `.env.example` and `start.ps1` ([#171](https://github.com/Jolls/arx/issues/171))

## [0.7.66] - 2026-09-23
### Security
- `SESSION_SECRET` from the environment is now ignored (with a warning) when it is the old `.env.example` placeholder or under 32 bytes, so an install with a short `SESSION_SECRET` in `.env` switches to the generated key and is logged out once; `.env.example` no longer sets it ([#168](https://github.com/Jolls/arx/issues/168))
- ~120 more handlers no longer return driver/parse error text to the client; the detail is logged server-side and the client gets a generic message ([#169](https://github.com/Jolls/arx/issues/169))
- `POST /po/{id}/open-folder` validates the PO number and 404s for unknown POs instead of creating a folder from the raw route parameter ([#170](https://github.com/Jolls/arx/issues/170))

## [0.7.65] - 2026-09-22
### Changed
- Copyright notice in `LICENSE`/`README.md` now reads "Jolls and contributors", reflecting confirmed non-maintainer authorship in history ([#152](https://github.com/Jolls/arx/issues/152))
- Moved the three dated 745-747/test-coverage review docs into `docs/archive/` with a note that they're historical snapshots, not current documentation ([#151](https://github.com/Jolls/arx/issues/151))

## [0.7.64] - 2026-09-22
### Changed
- Corrected stale CRLF/line-ending guidance in `CLAUDE.md` and annotated the maintainer-local `CLAUDE.local.md` import ([#149](https://github.com/Jolls/arx/issues/149))

### Added
- AGPL-3.0 licence and source-link notice in the app footer ([#154](https://github.com/Jolls/arx/issues/154))

## [0.7.63] - 2026-09-22
### Security
- Handlers no longer return raw driver error text to clients; the real error is logged server-side and a generic message returned instead (~31 sites) ([#148](https://github.com/Jolls/arx/issues/148))
- Named-query guard now rejects `OPENROWSET`/`OPENDATASOURCE`/`OPENQUERY`/`BULK` and the Postgres file-read and sleep functions; required least-privilege DB grants documented in `SQL/SCHEMA.md` ([#146](https://github.com/Jolls/arx/issues/146))
- CI's vulnerability check now pins `govulncheck` to a fixed release instead of `@latest` ([#153](https://github.com/Jolls/arx/issues/153))

## [0.7.62] - 2026-09-21
### Security
- CDN-loaded Bootstrap, Bootstrap Icons and SortableJS now carry Subresource Integrity hashes ([#145](https://github.com/Jolls/arx/issues/145))
- `/images/*` now uses the symlink-hardened `safePath`, and no longer lists directories ([#147](https://github.com/Jolls/arx/issues/147))

### Removed
- Unused `resolveUnder` and `urlutil.SafePathSegments` ([#147](https://github.com/Jolls/arx/issues/147))

## [0.7.61] - 2026-09-21
### Fixed
- Record view and print show MISSING for unrecorded steps that edit mode flags ([#138](https://github.com/Jolls/arx/issues/138))
- Formula default_result fields on a fresh record's edit page compute live (blue) instead of showing raw formula text ([#139](https://github.com/Jolls/arx/issues/139))

## [0.7.60] - 2026-09-21
### Changed
- User release notes now cover 0.7.49 through 0.7.60

## [0.7.59] - 2026-09-21
### Added
- Test records: double-click a blue default_result field to override it; overridden values show an amber tint with an "Overrides default" tooltip, and clearing the field or double-clicking again restores the default ([#133](https://github.com/Jolls/arx/issues/133))

## [0.7.58] - 2026-09-21
### Fixed
- Test step default_result is evaluated as math only when it starts with `=`; `{record.pn}` (e.g. `750-01347-01`) no longer becomes `-598`. Existing formula defaults get the `=` via migration `20260921120000_132_default_result_formula_prefix.sql` ([#132](https://github.com/Jolls/arx/issues/132))

## [0.7.57] - 2026-09-20
### Added
- "Create RFQs" on the BOM tab: flattens the BOM for N assemblies, nets against stock and reorder minimums, and creates one RFQ per default supplier after an editable preview; new per-category **Purchased** flag in Settings > Part Categories ([#99](https://github.com/Jolls/arx/issues/99))

## [0.7.56] - 2026-09-20
### Added
- List tables (parts, contacts, suppliers, POs, records, test report, part records, unit/lot trace) support drag-to-resize columns, remembered per table, with double-click to reset one column and a "Reset column widths" button ([#20](https://github.com/Jolls/arx/issues/20))

### Changed
- The test report now uses the shared list-table engine: drag-and-drop column reordering, date-range filters on Record/Result Date, shared sort and pagination, and "Copy for Excel" copies every filtered row in the default column order; Record Date now shows 24-hour time ([#126](https://github.com/Jolls/arx/issues/126))

## [0.7.55] - 2026-09-20
### Added
- List tables (parts, contacts, suppliers, POs, records) support drag-and-drop column reordering, remembered per table, with a "Reset column order" button; shared filter/sort URLs are now keyed by column name instead of position, so older shared filter links no longer apply ([#101](https://github.com/Jolls/arx/issues/101))

## [0.7.54] - 2026-09-20
### Added
- The first active attachment on a part or supplier with no primary is now set as primary automatically, and deleting the primary promotes the next active attachment (or clears it when none remain); migration `20260919231000_121_backfill_primary_attachment.sql` backfills existing records ([#121](https://github.com/Jolls/arx/issues/121))

### Changed
- Admin-only endpoints are now enforced by route middleware declared in `main.go` instead of per-handler checks; the named-query 403 message is now the generic "Only an admin can do this." ([#120](https://github.com/Jolls/arx/issues/120))
- `app_config` credentials are now identified by a `secret_` key prefix and excluded from the Settings backup automatically; the DigiKey client secret key is renamed to `secret_digikey_client` — run migration `20260919230000_119_secret_prefix_digikey_client.sql` when deploying ([#119](https://github.com/Jolls/arx/issues/119))

## [0.7.53] - 2026-09-19
### Added
- `SECURITY.md` (private vulnerability reporting) and `CONTRIBUTING.md` (build/test commands, workspace and migration conventions) ([#114](https://github.com/Jolls/arx/issues/114))
- `LICENSE` now names the copyright holder ([#113](https://github.com/Jolls/arx/issues/113))

### Security
- File serving and uploads now reject paths that reach outside a configured root through a symlink, including dangling ones ([#117](https://github.com/Jolls/arx/issues/117))
- The CI workflow's `GITHUB_TOKEN` is now limited to `contents: read` ([#115](https://github.com/Jolls/arx/issues/115))

## [0.7.52] - 2026-09-19
### Security
- Bumped chi (v5.3.0), `golang.org/x/crypto` (v0.56.0, now aligned across both modules) and `golang.org/x/image` (v0.45.0) past published advisories, and added a `govulncheck` step to CI so new ones surface early ([#109](https://github.com/Jolls/arx/issues/109))
- The login page no longer returns raw database driver errors to the browser, which could expose the server, login and database name before sign-in; the detail is logged instead ([#110](https://github.com/Jolls/arx/issues/110))

## [0.7.51] - 2026-09-19
### Security
- `.gitignore` now protects local-only files by name (`*.local`, `*.local.*`) as well as by directory, matches `local/` and `.local/` at any depth, and covers `.bacpac` database exports — so moving or renaming a local-only file no longer silently makes it committable ([#108](https://github.com/Jolls/arx/issues/108))

## [0.7.50] - 2026-09-18
### Security
- The Named Queries editor is now admin-only. It executes SQL supplied in the request, and its plain-SELECT check blocks writes but not reads, so any signed-in user could previously read any table the app's database login can reach — including stored password hashes ([#103](https://github.com/Jolls/arx/issues/103))
- Settings' data backup no longer exports the DigiKey client secret. The export already withheld user password hashes, but carried the `app_config` table wholesale, which is where that credential is stored ([#104](https://github.com/Jolls/arx/issues/104))
- Changing the database connection, saving DigiKey credentials, downloading a backup, opening Utilities and browsing for a folder are now admin-only. First-run setup and broken-connection recovery still work without an admin, so a bad connection can't lock you out. Attachment categories, part categories, part numbering and the company logo stay editable by any signed-in user ([#106](https://github.com/Jolls/arx/issues/106))
- `.gitignore` now covers `.env.*` variants, database backup exports and a repo-root `Arx.exe`, so a downloaded backup or an `.env.local` can't be committed by accident ([#107](https://github.com/Jolls/arx/issues/107))
### Changed
- The bundled verbatim copy of DigiKey's API User Agreement is replaced by a pointer to their developer portal, and the DigiKey test fixture now uses synthetic data rather than a captured live API response ([#111](https://github.com/Jolls/arx/issues/111))
## [0.7.49] - 2026-09-18
### Added
- `schema_migrations` ledger table; new migrations use `YYYYMMDDHHMMSS_<issue>_<desc>.sql` names and self-register, enforced by a `go test` lint ([#48](https://github.com/Jolls/arx/issues/48))

## [0.7.48] - 2026-09-18
### Added
- `default_result` formulas now support `min`, `max`, `abs`, `mod`, `round`, `floor`, `ceil`, `sqrt`, and `pow` function calls, evaluated by a safe allowlisted parser rather than a widened character whitelist ([#95](https://github.com/Jolls/arx/issues/95))

## [0.7.47] - 2026-09-17
### Added
- BOM line editing now supports searching by description/detail (in addition to part number), matching PO line item search ([#94](https://github.com/Jolls/arx/issues/94))

## [0.7.46] - 2026-09-17
### Added
- Paste-import BOM rows from Excel (tab-separated part number + qty) with a preview showing new/updated/unchanged/error rows before committing ([#53](https://github.com/Jolls/arx/issues/53))
- Attachments now carry a content hash and warn before saving a file or link that's already attached elsewhere, with an option to add it anyway ([#71](https://github.com/Jolls/arx/issues/71))

## [0.7.45] - 2026-09-17
### Removed
- Dead `_Test` clone-table DDL from `SQL/azure/app_config.sql`, `uom.sql`, `company_attachment.sql` — a fossil of the pre-#241 test-mode mechanism, superseded by the ArxDev connection-profile swap ([#92](https://github.com/Jolls/arx/issues/92))

## [0.7.44] - 2026-09-17
### Added
- Part Attachments' Add form accepts multiple files at once, each with its own category, importing them all in one submission instead of one file per submission ([#70](https://github.com/Jolls/arx/issues/70))
### Fixed
- Uploading a file over the 100 MB limit now shows a clear "file too large" message instead of a generic, CSRF-looking "Invalid form submission" error ([#88](https://github.com/Jolls/arx/issues/88))

## [0.7.43] - 2026-09-17
### Changed
- Attachment import now uses a standard browser file upload on both part and vendor Attachments tabs, replacing the native Windows file-picker dialog; the Copy/Move toggle is gone — imports are always a copy ([#65](https://github.com/Jolls/arx/issues/65))
- Vendor Attachments tab now also supports uploading a file directly, alongside the existing URL/path text field ([#65](https://github.com/Jolls/arx/issues/65))
### Removed
- `GET /api/browse-file` and `folderpick.BrowseFile`/`BrowseFileContext` — the folder-picker dialog (Settings) is unaffected ([#65](https://github.com/Jolls/arx/issues/65))

## [0.7.42] - 2026-09-16
### Added
- On a PO, selecting a supplier with only one contact now auto-selects that contact instead of leaving it blank ([#82](https://github.com/Jolls/arx/issues/82))
### Fixed
- Error page's "Back" link now returns to the page you came from (via Referer) instead of always going to Parts, fixing the wrong "Back to Parts" link shown after a folder→print navigation error ([#83](https://github.com/Jolls/arx/issues/83))
- "Copy for Excel" buttons on the Part Build Cost and Test Report pages could fire before table-sort.js loaded, throwing a console error and silently failing to attach ([#84](https://github.com/Jolls/arx/issues/84))

## [0.7.41] - 2026-09-16
### Added
- "Start RFQ" button on draft POs clones the PO's line items and supplier pricing into a new RFQ as the first quote, leaving the original PO untouched ([#74](https://github.com/Jolls/arx/issues/74))
- File upload in PO/part/supplier folder views now supports drag-and-drop, in addition to click-to-browse ([#73](https://github.com/Jolls/arx/issues/73))
### Fixed
- Sub-tabs (and the PO detail action buttons alongside them) now wrap to a second row instead of overflowing on mobile/narrow windows ([#72](https://github.com/Jolls/arx/issues/72))
- Suppliers can set a bulk-order delimiter and PN source in Ordering Options; PO detail pages now have a "Copy for Ordering" button that copies part number + qty pairs formatted for pasting into the supplier's ordering system, e.g. McMaster-Carr's comma-separated bulk order form ([#80](https://github.com/Jolls/arx/issues/80))

## [0.7.40] - 2026-09-16
### Added
- Selecting a part on a PO line now autofills Qty (from the supplier's minimum order increment) and Unit Cost (from the supplier's cheapest active price) when available ([#76](https://github.com/Jolls/arx/issues/76))
- Adding/editing a price now auto-calculates Price/Unit from Price/Pack (or vice versa) when only one is entered, using the pack size ([#75](https://github.com/Jolls/arx/issues/75))

## [0.7.39] - 2026-09-16
### Added
- Part attachments can now be scoped to one of the part's supplier links or manufacturer parts, via a new "Linked Vendor" picker on the Add/Edit Attachment forms and a matching column on the Attachments tab; scoped attachments appear under their supplier/manufacturer row on the Suppliers and Mfg Parts tabs, while part-level attachments stay on the Attachments tab as before ([#56](https://github.com/Jolls/arx/issues/56))
- Part breadcrumb PN label now shows the same `/parts` hover-tooltip thumbnail when the part has one ([#56](https://github.com/Jolls/arx/issues/56))
### Fixed
- Part Records tab was missing its breadcrumb trail ([#56](https://github.com/Jolls/arx/issues/56))
### Changed
- Part sub-tab breadcrumb markup consolidated into a shared partial, so every part tab renders the same trail ([#56](https://github.com/Jolls/arx/issues/56))

## [0.7.38] - 2026-09-14
### Fixed
- Accepting a PO's "New unit cost(s) — add to part pricing?" suggestion always saved the price at `pack_size=1`, clobbering the part's base unit price with what was actually a quantity-break price; it now saves at the PO line's ordered quantity, and the suggestion matching/dedup check no longer assumes `pack_size=1` either ([#57](https://github.com/Jolls/arx/issues/57))
### Added
- Deactivated prices on the Part Pricing tab can now be permanently deleted ([#57](https://github.com/Jolls/arx/issues/57))

## [0.7.37] - 2026-09-14
### Added
- Supplier "Linked Parts" table now shows the same `/parts` hover-tooltip thumbnail on the Internal PN column when a linked part has one ([#63](https://github.com/Jolls/arx/issues/63))
- Table text filters now support `*`/`?` glob wildcards (e.g. `65*-013*-*`), matched whole-field and case-insensitively; plain filter text with no wildcard chars still matches by substring as before ([#51](https://github.com/Jolls/arx/issues/51))
### Fixed
- Date-range filter dropdown on tables (e.g. `/parts`, `/records`) was clipped by the table's bottom edge when the filtered result set was short, since the dropdown's toggle button is created after the shared "fixed positioning" pass that escapes the table wrapper's scroll clipping; the same fix is now reapplied when the date-filter control is built ([#52](https://github.com/Jolls/arx/issues/52))

## [0.7.36] - 2026-09-14
### Added
- "Fetch from DigiKey" is now available on the Edit Supplier Link form, not just Add Supplier — pulls fresh description/lead time/MOQ/pricing/datasheet/photo/manufacturer data when editing an existing DigiKey-sourced supplier link
### Fixed
- DigiKey-imported datasheet/photo downloads failed with "unsupported protocol scheme" for the schemeless `//host/...` URLs the API returns for `DatasheetUrl`/`PhotoUrl`; these are now normalized to `https://` before fetching

## [0.7.35] - 2026-09-14
### Added
- New Part button now opens a quick-add modal for entering Part Number/Description before continuing to the full New Part form, prefilling those two fields ([#50](https://github.com/Jolls/arx/issues/50))

## [0.7.34] - 2026-09-14
### Fixed
- DigiKey-imported "Datasheet"/"Photo" attachments are now copied into `DOC_CONTROL_ROOT` like every other attachment instead of stored as a bare remote URL, which silently excluded the photo from the part detail page's Photos card and prevented Generate Thumbnail from working on the imported datasheet ([#62](https://github.com/Jolls/arx/issues/62))
### Added
- "Generate thumbnail from photo" option on the DigiKey import form, building the `/parts` hover-tooltip Thumbnail from the imported photo without a separate manual step ([#62](https://github.com/Jolls/arx/issues/62))

## [0.7.33] - 2026-09-14
### Changed
- DigiKey API client ID/secret now stored in `app_config` (shared by every user of the shop's install) instead of the per-user secrets store, matching the shop-level nature of the DigiKey app registration ([#60](https://github.com/Jolls/arx/issues/60))

## [0.7.32] - 2026-09-11
### Added
- Import part metadata from DigiKey: fetch pricing, datasheet, photo, manufacturer, and MPN by DigiKey part number on the part Sourcing tab's Add Supplier form; requires a DigiKey developer API client ID/secret configured in Settings ([#27](https://github.com/Jolls/arx/issues/27))
## [0.7.31] - 2026-09-14
### Added
- Preferred Supplier card on the part detail dashboard showing the preferred supplier's part number, description, and active price ([#55](https://github.com/Jolls/arx/issues/55))

## [0.7.30] - 2026-09-11
### Changed
- `migrate_40_part_description.sql` now bumps `schema_version` 9 → 10 and `ExpectedSchemaVersion` is bumped to match — the `part.title` → `part.description` rename in 0.7.29 is not backward-compatible (a pre-#40 binary queries the old column name) and was missing the schema-version gate every other rename migration uses to show the mismatch banner instead of a raw DB error ([#40](https://github.com/Jolls/arx/issues/40))

## [0.7.29] - 2026-09-10
### Changed
- Renamed `part.title` → `part.description`, previously labeled inconsistently as "Title" or "Name" across pages, now consistently the `description` column labeled "Description" everywhere. Run `migrate_40_part_description.sql` ([#40](https://github.com/Jolls/arx/issues/40))

## [0.7.28] - 2026-09-10
### Fixed
- Supplier link "preference" field crashed with a SQL conversion error since it was a free-text input over an INT column; now a Primary/Alternate/Backup dropdown ([#41](https://github.com/Jolls/arx/issues/41))
- Add/Edit Supplier Link forms lost the user's input on a validation or DB error instead of redisplaying it ([#42](https://github.com/Jolls/arx/issues/42))
- Edit Supplier Link form showed a blank supplier name on open despite a valid link ([#43](https://github.com/Jolls/arx/issues/43))

## [0.7.27] - 2026-09-04
### Added
- Warning when navigating away from the PO edit form (new/edit/RFQ/duplicate) with unsaved changes ([#34](https://github.com/Jolls/arx/issues/34))
- Banner prompting a user with no default PO receiver configured to finish account setup, shown on New PO/RFQ ([#35](https://github.com/Jolls/arx/issues/35))
- Upload a file directly into a PO folder, supplier folder, or the generic document-control folder browsers ([#36](https://github.com/Jolls/arx/issues/36))

## [0.7.26] - 2026-08-27
### Changed
- Pre-publication privacy cleanup: rewrote history to drop the tracked `CLAUDE.md` (named the private companion repo and local paths) and a since-removed `.claude/settings.local.json`, and redacted a changelog byline naming the repo owner. Split `CLAUDE.md` into a sanitized tracked file plus a gitignored `CLAUDE.local.md` for the private sections.

## [0.7.25] - 2026-08-26
### Changed
- Repository migrated to `Jolls/arx`; the pre-migration history now lives in the private `Jolls/arx-legacy`. Existing changelog and docs issue links intentionally point at `arx-legacy`, where those numbers resolve — new entries link to `Jolls/arx`

## [0.7.24] - 2026-08-20
### Added
- GitHub Actions workflow (`test.yml`) running `go vet`/`go build`/`go test` for `arx_go` and `arxlib` on every push to main and PR — unit tests only, no DB credentials or integration tests involved

## [0.7.23] - 2026-08-20
### Changed
- Upgraded Go toolchain to 1.27.0 and applied `go fix`'s modernizers across `arx_go` (range-over-int loops, `any` instead of `interface{}`, `strings.Cut`/`CutPrefix`/`SplitSeq`, `maps.Copy`, `slices.Contains`) — no behavior change

## [0.7.22] - 2026-08-07
### Changed
- Renamed `form_record.comments` → `record_type`: the column never held comments — it holds the record Type ("New Release" / "Re-Test" / "Upgrade") shown in the UI. The misleading name blocked #870 from adding a real record-level comment field. Bumps `ExpectedSchemaVersion` 8 → 9; run `migrate_874_form_record_type_rename.sql` ([#874](https://github.com/Jolls/arx-legacy/issues/874))

## [0.7.21] - 2026-08-07
### Changed
- Promoted four deferred logical references to real, DB-enforced foreign keys: `contact.company_id` and `part.default_supplier_id` → `company.id`; `part.price_id` → `price.id`; `part.primary_attachment_id` → `part_attachment.id`. The latter two also convert their `DEFAULT 0` "no value" sentinel to `NULL`, matching the existing `company.primary_attachment_id` pattern — `Part.PrimaryAttachmentID` is now `*int` instead of `int` ([#735](https://github.com/Jolls/arx-legacy/issues/735), follow-up to [#213](https://github.com/Jolls/arx-legacy/issues/213))

### Fixed
- `seed_test_data.sql` (both dialects) now breaks the circular FK between `contact`/`company` and nulls `part`'s three newly-FK'd columns before its DELETE cascade, so a reseed no longer fails against the new constraints
- `TestIntegration_RunNamedQuery_SingleResult`'s cleanup now clears `part.primary_attachment_id` before deleting the attachment row, avoiding an FK violation that silently orphaned test fixtures

## [0.7.20] - 2026-08-06
### Fixed
- `SuppliersRows` now checks `rows.Err()` after its scan loop, matching `PartsRows`/`PORows` — a failure part-way through row iteration no longer returns a silently truncated supplier list as HTTP 200 ([#862](https://github.com/Jolls/arx-legacy/issues/862))
- `SupplierFile`/`POFile` now set `Cache-Control: no-cache` and RFC 6266-encode filenames via `mime.FormatMediaType`, matching `ServeLocalFile`/`ServeSupplierFile` — closes the gap left when #363 and #839 were fixed in only two of the four file-serving handlers. Image inlining (alongside PDFs) is now uniform across all four file-serving sites ([#861](https://github.com/Jolls/arx-legacy/issues/861))

### Changed
- Unified the four directory-listing handlers (`ServeLocalDir`, `ServeSupplierDir`, `renderSupplierFolder`, `renderPOFolder`) and four file-serving handlers (`ServeLocalFile`, `ServeSupplierFile`, `SupplierFile`, `POFile`) onto shared `renderDirListing`/`serveLocalizedFile` helpers in `files.go`, removing near-duplicate sort/entry-loop/path-containment logic. `renderPOFolder`/`POFile` gain `safePath` containment (previously unchecked); `renderSupplierFolder`'s path-traversal handling now matches the other three sites (silently resolves under root instead of rejecting with 400) ([#864](https://github.com/Jolls/arx-legacy/issues/864))

## [0.7.19] - 2026-08-06
### Added
- "Receive All" button on the PO detail page's receive form, pre-filling every open line's remaining quantity and submitting in one click alongside the existing "Receive" button ([#879](https://github.com/Jolls/arx-legacy/issues/879))

### Fixed
- Saving a serial/lot_serial test record already linked to a unit now reuses that unit via `record.UnitID` instead of re-deriving it from `serial_number` — previously, editing a unit's serial after linking (#799) and then re-saving the record would silently mint a duplicate unit and orphan the original ([#876](https://github.com/Jolls/arx-legacy/issues/876))
- Seed record 7013's `serial_number` corrected to match its linked unit 8501's serial, removing a self-corrupting seed fixture ([#877](https://github.com/Jolls/arx-legacy/issues/877))

## [0.7.18] - 2026-08-06
### Added
- New "Records" subtab on the part detail page listing every active test record across every test form for that part, sortable/filterable/linkable ([#875](https://github.com/Jolls/arx-legacy/issues/875))
- Lot and unit genealogy trace pages gain a matching test-records table scoped to that lot/unit ([#875](https://github.com/Jolls/arx-legacy/issues/875))

## [0.7.17] - 2026-08-06
### Added
- Test record's read-only view shows the linked build, alongside the existing lot card, without needing to open Edit; removed a dead commented-out block from #677 that this supersedes ([#871](https://github.com/Jolls/arx-legacy/issues/871))

## [0.7.16] - 2026-08-06
### Added
- Lots carry free-text notes (`lot.notes`), editable in full on the lot edit page and shown on the lot detail header, the per-part Lots subtab, the cross-part Lots list, and both genealogy trace tables ([#872](https://github.com/Jolls/arx-legacy/issues/872))
- A test record's read-only view gains a related-lot card beside the record info, showing the linked lot's number, description, vendor lot, and current batch note ([#872](https://github.com/Jolls/arx-legacy/issues/872))
- A test record's edit page can append a line to its linked lot's note, saved with the record in the same transaction. Only the new text is posted and the server concatenates it, stamped with the author's username and date, so two testers appending from long-open record pages both land instead of one clobbering the other ([#872](https://github.com/Jolls/arx-legacy/issues/872))
- Test records carry a record-level note (`form_record.notes`) for remarks covering the whole test session, distinct from the per-step Comment column. It shows on the record view and printed record, freezes when the record is locked/approved, and is deliberately not carried over by Duplicate ([#870](https://github.com/Jolls/arx-legacy/issues/870))

## [0.7.15] - 2026-08-05
### Added
- ArxDev seed data: BOM line 3913 adds part 3012 (`lot`-tracked, not `lot_serial`, with its own BOM) as a further FORM-1001 testable unit, so the whole-lot/batch build-at-test-time qty field has a selectable, buildable fixture to exercise ([#867](https://github.com/Jolls/arx-legacy/issues/867))

### Fixed
- Inline "Build this unit" panel on a test record's edit page hardcoded qty=1 even for whole-lot/batch records (a `lot`/`none`-tracked part's record, which has no single serialized unit under Q8) — those records now get a "Qty to build" input instead of being stuck at 1; serial/lot_serial records are unaffected ([#867](https://github.com/Jolls/arx-legacy/issues/867))

## [0.7.14] - 2026-08-05
### Added
- Test coverage for the local-filesystem browse/serve handlers (`files.go`'s four handlers and the `suppliers.go`/`pos.go` folder+file equivalents), pinning current behavior including drift between the three copies ([#863](https://github.com/Jolls/arx-legacy/issues/863))

### Changed
- Integration tests (`-tags integration`) no longer trust the target database's name — `ARX_TEST_DSN` is now validated by checking for known seed content instead, since a test target may not literally be named "ArxDev". A `TestMain` runs this check once up front so a bad credential/connection fails the whole run immediately instead of every test independently redialing and failing.

## [0.7.13] - 2026-07-29
### Changed
- ArxDev seed data: renamed the "Widget" reference product family to a "Skyrunner Drone" one (part titles, form title, and denormalized record snapshots) so the assembly/sub-assembly/tracking-mode structure reads as a recognizable, concrete product instead of a generic placeholder ([#797](https://github.com/Jolls/arx-legacy/issues/797))

## [0.7.12] - 2026-07-29
### Fixed
- `/settings` was unreachable to fix a misconfigured or unreachable DB connection: auth was only bypassed when no DB was configured at all, and a connected-but-broken connection also left `/login` itself failing, so there was no way back. `/settings` now bypasses login whenever the connection is genuinely unusable (bad server/auth/connectivity), while a plain schema-version mismatch on an otherwise-working connection still requires login as before. An unauthenticated visit in this state can't reconnect using a previously-stored password — reconnecting requires typing it again ([#852](https://github.com/Jolls/arx-legacy/issues/852))

## [0.7.11] - 2026-07-29
### Added
- Part → Units: "Add Unit" and per-unit "Edit" let a user create or fix a serial with no test record involved — for a pre-existing unit that predates Arx's traceability data ([#799](https://github.com/Jolls/arx-legacy/issues/799))

### Changed
- `unit.source` (`test`/`manual`) distinguishes a test-minted unit from a manually back-filled one; the Build tab's "Tested" completeness count now excludes manual units so a back-filled serial doesn't inflate it ([#799](https://github.com/Jolls/arx-legacy/issues/799))

## [0.7.10] - 2026-07-29
### Fixed
- Postgres reference DDL (`SQL/postgres/`) had fallen behind `SQL/`: `users.sql` was missing the `timezone` column from #847, and `app_config.sql` seeded a stale `schema_version` ('6' instead of '8') on both dialects

## [0.7.9] - 2026-07-29
### Added
- Test coverage for `arxlib/folderpick` (context cancellation/timeout paths) ([#822](https://github.com/Jolls/arx-legacy/issues/822))
- Test coverage for `icon.go`'s ICO byte-encoding logic (`appIcon`/`fallbackIcon`/`buildICO`) ([#823](https://github.com/Jolls/arx-legacy/issues/823))
- Correctness assertions (status + rendered content) to `TestIntegration_RouteRoundTrips`, previously profiling-only ([#824](https://github.com/Jolls/arx-legacy/issues/824))
- Template render-data assertions covering one representative page per family (core tab, records, core print, shared standalone), beyond the existing parse-only checks ([#825](https://github.com/Jolls/arx-legacy/issues/825))

## [0.7.8] - 2026-07-29
### Added
- Per-user timezone preference (Settings → My Preferences), used to convert UTC audit timestamps into the user's local calendar day ([#847](https://github.com/Jolls/arx-legacy/issues/847))

### Fixed
- Form-definition history bucketed audit timestamps by UTC calendar day, so edits made in the evening local time were attributed to the next day and the pre-change snapshot silently fell back to current values; timestamps are now bucketed in each user's timezone ([#847](https://github.com/Jolls/arx-legacy/issues/847))

## [0.7.7] - 2026-07-28
### Added
- Test coverage for `pdfthumb.go`'s PDF render/pool/PNG-encode path (`renderPDFFirstPage`/`getPdfiumPool`/`encodePNG`) ([#818](https://github.com/Jolls/arx-legacy/issues/818))
- Test coverage for `categories.go`'s `SettingsCategoriesSave` handler ([#820](https://github.com/Jolls/arx-legacy/issues/820))

## [0.7.6] - 2026-07-28
### Added
- Integration test coverage for attachment resolve/delete/primary-attachment logic (`resolveAttachmentFileInput`/`deleteAttachmentFileIfUnshared`/`setPrimaryAttachment`) ([#809](https://github.com/Jolls/arx-legacy/issues/809))
- Test coverage for `api.go`'s attachment-paste/thumbnail handlers (`APIPartAttachmentName`/`APIPartPasteAttachment`/`APIPartPasteAttachmentReplace`/`APIPartGenerateThumbnail`/`upsertGeneratedAttachment`/`decodePastedImage`), including the previously-untested paste-result-image write path ([#821](https://github.com/Jolls/arx-legacy/issues/821))
- Integration test coverage for the Reports family beyond the 3 dashboard cards (Spend/OnTime/CycleTime/DataQuality queries, handlers, and CSV exports) ([#815](https://github.com/Jolls/arx-legacy/issues/815))
- Integration test coverage for the `ReportsDashboard` page handler assembly ([#816](https://github.com/Jolls/arx-legacy/issues/816))
- Integration test coverage for Contacts/Suppliers/MfgParts/SupplierPart edit/update/detail handlers ([#817](https://github.com/Jolls/arx-legacy/issues/817))

## [0.7.5] - 2026-07-27
### Added
- Integration test coverage for the record lock/approve state machine (`LockRecord`/`ApproveRecord`/`UnlockRecord`/`BulkLockRecords`) and the completion-audit snapshot chain it drives ([#806](https://github.com/Jolls/arx-legacy/issues/806))
- Integration test coverage for form definition CRUD + history (`SaveFormDef`/`EditFormDef`/`ArchiveStep`/`FormDefHistory`), including regression tests that archiving/editing a form def never alters an already-locked record's frozen snapshot ([#813](https://github.com/Jolls/arx-legacy/issues/813))
- Integration test coverage for lot handlers and manual stock adjustment (`PartLots`/`PartLotTrace`/`LotEdit`/`LotUpdate`/`AllLots`/`PartStockAdjust`) ([#808](https://github.com/Jolls/arx-legacy/issues/808))
- Integration test coverage for unit read/trace handlers (`PartUnits`/`PartUnitTrace`) ([#814](https://github.com/Jolls/arx-legacy/issues/814))

## [0.7.4] - 2026-07-27
### Added
- Part detail dashboard now shows Lots/Units cards (total count + 5 most recent) for lot- and serial-tracked parts ([#798](https://github.com/Jolls/arx-legacy/issues/798))

## [0.7.3] - 2026-07-27
### Fixed
- Editing a part attachment to import a replacement PDF appeared not to reimport the file — the attachment's `LOCAL:` URL stays the same on replace, and the browser could serve the old PDF from cache without revalidating; doc-control file routes now send `Cache-Control: no-cache` ([#839](https://github.com/Jolls/arx-legacy/issues/839))
- Regenerating a part's PDF thumbnail/preview images left a `(2)`-suffixed file behind instead of replacing the original in place, since the write raced the not-yet-deleted original under the same name; the generated images now overwrite the existing file directly when the name is unchanged ([#839](https://github.com/Jolls/arx-legacy/issues/839))

## [0.7.2] - 2026-07-27
### Fixed
- The native Browse file/folder picker (part attachments, Settings) could open behind other windows — its owner form was never realized before `ShowDialog`, so `TopMost` wasn't reliably applied; the owner is now shown (invisibly, zero-opacity) before the dialog opens

## [0.7.1] - 2026-07-27
### Fixed
- PDF attachment thumbnails failed to render with `pdfium instance: could not instantiate webassembly module: GetFileType /dev/stdout: The handle is invalid` — go-pdfium's wasm module defaulted its WASI stdout/stderr to `os.Stdout`/`os.Stderr`, which are invalid handles under Arx's `-H windowsgui` build; now explicitly discarded ([#837](https://github.com/Jolls/arx-legacy/issues/837))

## [0.7.0] - 2026-07-27
### Added
- Traceability data model v0.7 (epic [#736](https://github.com/Jolls/arx-legacy/issues/736)): Part → Lot → Unit three-tier identity is complete and verified end-to-end against ArxProd. Parts carry a `none | lot | serial | lot_serial` tracking mode; serialized units are real rows with provenance (lot/build), a unified `genealogy` edge table covers both lot and unit endpoints, and a build/lot/unit traceability view (including a per-serial "birth certificate") and embedded build-at-test-time UX ship with it.

## [0.6.29] - 2026-07-25
### Added
- Postgres port of `SQL/seed_test_data.sql` and `SQL/seed_company_logo.sql` (`SQL/postgres/`) — the synthetic ArxDev reference dataset now has a Postgres-native equivalent ([#829](https://github.com/Jolls/arx-legacy/issues/829))
- `Dialect.BoolFromCondition` and `Dialect.RewriteNamedParams` helpers, closing the remaining SQL Server-only boolean-idiom and named-parameter gaps in `arx_go`'s BOM cost rollup and named-query execution paths ahead of Postgres integration ([#831](https://github.com/Jolls/arx-legacy/issues/831), [#830](https://github.com/Jolls/arx-legacy/issues/830))
- Postgres translation of the 9 seeded `named_queries` rows ([#830](https://github.com/Jolls/arx-legacy/issues/830))
### Fixed
- `named_query.go`'s `execQuery` built driver args by iterating a Go map in random order — harmless on SQL Server but silently broken on Postgres (positional binding, no `@name` placeholder syntax); args are now built deterministically from the params map ([#830](https://github.com/Jolls/arx-legacy/issues/830))
- `SQL/postgres/users.sql` was missing the `is_admin` column present in `SQL/users.sql` since #750 ([#829](https://github.com/Jolls/arx-legacy/issues/829))

## [0.6.28] - 2026-07-24
### Added
- Integration test coverage for `rollupCost`/`PartRollupCost` (legacy BOM cost rollup): nested/flat rollup, memoization, cycle detection, and the handler's write-back transaction ([#804](https://github.com/Jolls/arx-legacy/issues/804))
- Test coverage for `SettingsSave`'s DB connection/secrets swap: connect success/failure, password precedence, field clear-vs-preserve semantics, and the test-mode-swap forced relogin ([#805](https://github.com/Jolls/arx-legacy/issues/805))
- End-to-end test coverage for `config.Load`'s `.env` → env vars → `local.json` precedence chain ([#810](https://github.com/Jolls/arx-legacy/issues/810))
- Test coverage for `migrateLegacy` (pre-#422 two-app config merge) ([#819](https://github.com/Jolls/arx-legacy/issues/819))
### Changed
- Extracted `connectDB`/`selectConnectPassword` seams from `SettingsSave` to make the connect-failure path and password precedence unit-testable without a real DB dial (part of #805)

## [0.6.27] - 2026-07-24
### Added
- Integration/unit test coverage for auth middleware composition, login lockout, and idle-timeout — `buildRouter`'s real route table is now walked and asserted to require auth on every non-public route ([#802](https://github.com/Jolls/arx-legacy/issues/802))
- Integration test coverage for named-query execution (`runNamedQuery`/`execQuery`), including a regression test reproducing the stale-`spec_nom`-param-rename production incident ([#807](https://github.com/Jolls/arx-legacy/issues/807))
- Integration test coverage for `POReceive`/`POStatusTransition`/`POApprovalAction` end-to-end (partial/full receiving, lot creation, status transitions, approval workflow) ([#803](https://github.com/Jolls/arx-legacy/issues/803))
- Integration test coverage for the RFQ award/convert flow (`RFQNew`/`RFQAddSupplier`/`RFQCompare`/`RFQCompareSave`/`RFQConvert`) ([#812](https://github.com/Jolls/arx-legacy/issues/812))

## [0.6.26] - 2026-07-24
### Fixed
- BOM view's lazily-expanded sub-assembly rows linked to `/part/undefined` and showed a blank quantity — the JS row builder read stale field names (`PLPartID`/`PLQty`) that don't match the API's `ComponentPartID`/`Qty` ([#795](https://github.com/Jolls/arx-legacy/issues/795))
### Changed
- Dropped unnecessary single-letter table aliases (`p.`, `f.`) from the `bom_pn_by_item`, `pn_primary_attachment`, and `form_primary_attachment` named queries ([#794](https://github.com/Jolls/arx-legacy/issues/794))
- Documented an orphan-check requirement for future FK-promotion migrations, after `migrate_742` failed on ArxProd due to pre-mating orphan rows ArxDev's seed never surfaced ([#775](https://github.com/Jolls/arx-legacy/issues/775))

## [0.6.25] - 2026-07-22
### Added
- Serialized-unit create/render logic (traceability epic [#736](https://github.com/Jolls/arx-legacy/issues/736), slice 8): parts now carry a `none | lot | serial | lot_serial` tracking mode (replacing the lot-only checkbox); testing a serial/lot_serial part mints a `unit` row with provenance from the linked lot/build, retests reuse the same unit, unit-level records carry `unit_id` and read lot/build through the unit, and a build's history shows a "Tested N / qty" completeness column ([#745](https://github.com/Jolls/arx-legacy/issues/745))
- Build/lot/unit traceability view (slice 9): a read-only Units subtab and a per-serial genealogy "birth certificate" that walks the unified lot+unit `genealogy` edge table (seeded from the unit and its lot) to show a unit's as-built components; the existing lot trace now renders unit endpoints too ([#746](https://github.com/Jolls/arx-legacy/issues/746))
- Embedded build-at-test-time UX (slice 10): the "Build this unit" affordance on a test record now expands an inline BOM/lot panel that builds one unit and links it to the record in one transactional save, instead of navigating to the Build tab and back ([#747](https://github.com/Jolls/arx-legacy/issues/747))
### Fixed
- Settings' folder/file Browse… dialogs opened without an owner window, so they could appear behind Arx.exe with no taskbar entry and hang indefinitely waiting for a click nobody could make; the PowerShell picker now uses a topmost invisible owner form so the dialog always comes to the foreground
- The `max_subbatch_result` named query's `spec_nom` usage sites still passed `@test_id` after the [#769](https://github.com/Jolls/arx-legacy/issues/769) column rename to `@form_row_id`, failing at runtime with "Must declare the scalar variable '@form_row_id'"; a migration rewrites the stored `spec_nom` text in `form_row` and `result`

## [0.6.24] - 2026-07-19
### Security
- `GET /settings` now gated behind `RequireAuthOnceConnected` (same pattern as the POST sibling from [#748](https://github.com/Jolls/arx-legacy/issues/748)) so an unauthenticated caller on a connected instance can no longer load the settings form pre-filled with `db_server`, `db_user`, and the configured filesystem roots ([#781](https://github.com/Jolls/arx-legacy/issues/781))

## [0.6.23] - 2026-07-19
### Security
- Security-hardening batch (epic [#719](https://github.com/Jolls/arx-legacy/issues/719)): the `SettingsSave` DB swap now stores an immutable `{db, dialect}` snapshot through an `atomic.Pointer` so a concurrent request can no longer read a torn `h.db`/`h.dialect` pair or use a handle mid-close (the wider `cfg`/`companyLogo`/`partCategories`/`schemaMismatch` mutation race is deferred as a follow-up); the native folder/file browse endpoints (`/api/browse-folder`, `/api/browse-file`) are gated behind `RequireAuthOnceConnected` so an unauthenticated caller on a connected instance can't spawn local dialogs; CSRF verification uses a constant-time compare and the token is rotated on login/logout; a 7-day session idle timeout logs out abandoned sessions independent of the cookie's absolute lifetime; failed logins are throttled per username (10 fails → 1-minute cooldown, counter resets after the window, map bounded against unbounded growth); and the Postgres DSN now forces `sslmode=require` instead of `prefer` so a plaintext fallback can never be silently used ([#757](https://github.com/Jolls/arx-legacy/issues/757))
### Fixed
- Stopped discarding write errors and silently truncating scan loops across the PO and records handlers (M8–M10, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)): `execContext`/`tx.ExecContext` calls whose returned error was ignored now surface a 500 instead of redirecting as if the write succeeded, `for rows.Next()` loops that swallowed a `Scan` error via `continue` now stop and report, and every result set gets a post-loop `rows.Err()` check so a mid-stream driver error is no longer mistaken for a clean end of rows ([#756](https://github.com/Jolls/arx-legacy/issues/756))
- Reporting/named-query correctness fixes (M26–M28, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)): failure-mode reports (`records_failure_modes.go` and the dashboard top-failure-modes tile) labeled each `form_row_id` group with `MAX(res.parameter)` — the alphabetically-largest parameter name ever recorded, not the current one — now replaced with the latest definition via a portable `TOP 1`/`LIMIT 1` correlated subquery; spend-by-part grouped on `part_number_snapshot` so one part split across rows when its snapshot text drifted, now grouped by `part_id` with the snapshot as fallback; and a named query returning three or more columns silently returned nothing (the scan expected exactly one or two), now scans all columns and uses the first two ([#780](https://github.com/Jolls/arx-legacy/issues/780))
### Added
- Test coverage for the auth/user-admin surface and standalone templates (epic [#719](https://github.com/Jolls/arx-legacy/issues/719)): unit tests for the `db == nil` guard, `requireAdmin`'s 403 gate, `Logout`, and `validResultType`; integration tests (live ArxDev) for `LoginPost` success/failure branches and all six `SettingsUsers*` handlers including the self-deactivate/self-remove-admin guards; and a parse test for the `templates/shared` standalone pages that had zero parse coverage ([#758](https://github.com/Jolls/arx-legacy/issues/758))
## [0.6.22] - 2026-07-18
### Fixed
- Settings → Backup now includes `inventory_transaction`, `build`, `lot`, `genealogy`, `purchase_order_history`, and `record_event_results` (H5, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)), which were previously silently omitted from a backup users trust to be complete. Also fixed `LinksTable()` returning the renamed-away `"LNK"` (dropped from the backup's table list entirely since the fixed string now duplicates the existing `SupplierPartTable()` entry) ([#754](https://github.com/Jolls/arx-legacy/issues/754))
## [0.6.21] - 2026-07-18
### Fixed
- Postgres-readiness batch (epic [#719](https://github.com/Jolls/arx-legacy/issues/719)): ported the lot/build/inventory schema to `SQL/postgres/*` (missing `inventory_transaction.build_id` + FK, `lot.lot_description`, and a `README.md` run-order bug that listed `form_record` before `unit` despite depending on it) and routed the remaining SQL-Server-only outlier SQL through the `arxlib/db` dialect seam: `POAddSuggestions`'s `IF NOT EXISTS...INSERT` rewritten as a portable `INSERT...SELECT...WHERE NOT EXISTS`, `RFQCompareSave`'s `UPDATE...FROM...OUTER APPLY` rewritten as a portable correlated subquery, inline `BIT` literals in `RFQConvert`/`CreateRecord`/`DuplicateRecord`/`CreateForm`/`CreateDuplicate` replaced with `dialect.BoolLiteral`, and `copyFormSteps` switched from a raw `*sql.Tx` (bypassing `dialect.Rewrite`/DEBUG logging, and reading its source outside the transaction it was handed) to the `*txLogger` wrapper via `h.beginTx` (H4, M1-M5, [#753](https://github.com/Jolls/arx-legacy/issues/753), [#755](https://github.com/Jolls/arx-legacy/issues/755))
## [0.6.20] - 2026-07-18
### Security
- Scoped PO-line and BOM row edits/deletes to their parent record (H2+H3, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)). `POUpdate`'s delete/update loops and `PartBOMSave`'s delete/update loops took the row id straight from the submitted form with no `AND po_id=`/`AND parent_part_id=` guard, so a user editing PO A or part X's BOM could overwrite or delete a row belonging to PO B or part Y by crafting the form/id, silently drifting the other record's `total_cost`/BOM. Both loops now scope by the already-resolved parent id, matching the existing `MfgPartDelete`/`PriceDeactivate` guard pattern ([#752](https://github.com/Jolls/arx-legacy/issues/752))

## [0.6.19] - 2026-07-18
### Fixed
- Unified the leaf-cost rule between Roll Up Cost and the BOM tab/CSV export, and stopped storing blank preferred-price form fields as `0` instead of `NULL` (H1, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)). Roll Up Cost used a looser rule (`preferredPrice.Valid` alone) than `bomLeafCost`'s `Valid && > 0` check, so a leaf whose preferred price was stored as `0.00` rolled up as `$0` while the BOM tab/CSV fell back to `current_cost` for the same leaf, silently understating assembly cost. `rollupCost` now calls the shared `bomLeafCost` helper, and `PriceCreate`/`PriceUpdate` now use `nullableFloat` so a blank price/pack-size field stores `NULL` rather than `0` ([#760](https://github.com/Jolls/arx-legacy/issues/760))

## [0.6.17] - 2026-07-18
### Security
- Gated the five user-admin endpoints and the Settings → Users management section behind a new `users.is_admin` flag (C3, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)). They were protected only by `RequireAuth`, so any logged-in user could self-grant `can_approve_po`/`can_approve_records`, reset any user's password with no old-password check, or deactivate other accounts — a privilege-escalation hole with no admin/role concept in `users`. Added `is_admin BIT` (bootstrap first-run user is created as admin; the guarded migration `SQL/migrations/migrate_750_users_is_admin.sql` backfills existing active users so nobody is locked out), a `requireAdmin` gate on all five handlers plus the new admin-only `POST /settings/users/{id}/toggle-admin` (which can't strip your own admin rights), and hid the Users tab/panel from non-admins ([#750](https://github.com/Jolls/arx-legacy/issues/750))
### Fixed
- Record-history diff now flags a step's `specification`/`spec_units`/`parameter` as changed between Complete-event snapshots, not just `result`/`comment`/`pass_fail`. Resyncing an unlocked record pulls those fields from the live form definition, so a complete → unlock → resync → complete cycle could shift a step's acceptance criteria while the history view silently reported it as "unchanged" (H7, epic [#719](https://github.com/Jolls/arx-legacy/issues/719), [#779](https://github.com/Jolls/arx-legacy/issues/779))

## [0.6.15] - 2026-07-18
### Security
- Gated the main `POST /settings` save behind login once a database is connected (C1, epic [#719](https://github.com/Jolls/arx-legacy/issues/719)). The route stayed in the always-accessible block for first-run setup, so with a DB already connected an unauthenticated caller could submit a blank password with an attacker-controlled `db_server`; the handler fell back to the stored password (`firstNonEmpty(pw, cfg.DBPassword)`) and dialed the attacker's server, exfiltrating it. A new `RequireAuthOnceConnected` middleware lets `POST /settings` through only while `h.db == nil` (first-run) and requires a logged-in user otherwise; it intentionally skips the schema-mismatch redirect so an admin can still re-point a mis-connected DB ([#748](https://github.com/Jolls/arx-legacy/issues/748))

## [0.6.14] - 2026-07-18
### Added
- Formalized three previously-inferred value sets as `VARCHAR`+`CHECK` "enum" columns (traceability epic slice 7): `lot.source` (`purchase|build|adjust`, nullable — no neutral resting source, backfilled by inference from `po_line_id`/owning build), `form.form_type` (`inspection|test|calibration|checklist|batch record`, `NOT NULL DEFAULT 'test'` — the kind of quality document, orthogonal to `record_types`; `batch record` is a `form_type` value not a `record_types` token, Q10), and `form_row.granularity` (`lot|unit`, `NOT NULL DEFAULT 'unit'` — per-unit vs per-lot check). Also promoted `form.part_number_id` from a bare logical reference to a real FK (`FK_form_part`, §4.4). Additive and inert until a later slice reads these columns, so `ExpectedSchemaVersion` is deliberately unchanged; ships DDL for both SQL Server and Postgres, the guarded migration `SQL/migrations/migrate_744_enum_formalization.sql`, seed fixture backfill, and schema docs ([#744](https://github.com/Jolls/arx-legacy/issues/744))

## [0.6.13] - 2026-07-18
### Added
- Added `part.tracking_mode` (traceability epic slice 6): a `VARCHAR(10)` + `CK_part_number_tracking_mode` CHECK restricting it to `none|lot|serial|lot_serial`, backfilled from `is_lot_tracked` (`0`→`none`, `1`→`lot`). Additive and inert — `is_lot_tracked` stays and keeps driving reads; the read-swap happens in a later slice. Ships DDL for both SQL Server and Postgres, the guarded migration `SQL/migrations/migrate_743_part_tracking_mode.sql`, seed fixture backfill, and schema docs ([#743](https://github.com/Jolls/arx-legacy/issues/743))

## [0.6.12] - 2026-07-18
### Added
- Added the nullable `form_record.unit_id` FK (traceability epic slice 5) — the link from a quality record to the single serialized `unit` it tests (Q8): set on a unit-testing/retest record, NULL for whole-lot/batch records (no form_record↔unit m2m, so a plain FK, no join table). Also promoted `form_record.part_id` from a bare logical reference to a real FK (`FK_form_record_part`, §4.4). Additive and inert until a later slice wires create/render logic, so `ExpectedSchemaVersion` is deliberately unchanged; ships DDL for both SQL Server and Postgres (also closing pre-existing Postgres drift where `form_record` was missing `lot_id`/`build_id`), the guarded migration `SQL/migrations/migrate_742_form_record_unit_fk.sql`, seed fixtures wiring three records to units, and schema docs. The Q8 FK-consistency invariant (read lot/build through the unit when `unit_id` is set) is documented as an app-layer rule for the behavior slice ([#742](https://github.com/Jolls/arx-legacy/issues/742))

## [0.6.11] - 2026-07-18
### Security
- Gated the Settings → Configuration tab (attachment categories, categories, part numbering, company logo) behind login. Its five save routes were registered outside the `RequireAuth` group, so anyone who could reach the HTTP port could mutate shop-wide `app_config` without a session; they now sit inside the authenticated group and the Configuration tab link/panel is shown only when a user is logged in, matching the Named Queries/Users/Preferences tabs ([#771](https://github.com/Jolls/arx-legacy/issues/771))

## [0.6.10] - 2026-07-18
### Changed
- Widened the lot-genealogy edge table into a single provenance table for both lots and serialized units (traceability epic slice 4): renamed `lot_genealogy` → `genealogy`, added nullable `parent_unit_id` / `child_unit_id` FKs to `unit`, relaxed the lot columns to nullable, and added the `CK_gen_one_parent` / `CK_gen_one_child` CHECKs enforcing exactly one parent FK and one child FK per edge (so an edge is lot→lot, lot→unit, unit→lot, or unit→unit). Additive for data — existing lot→lot rows stay valid (unit columns NULL); no code writes unit endpoints until a later slice. Renamed the `Config.LotGenealogyTable()` helper to `GenealogyTable()`, ships DDL for both SQL Server and Postgres, the guarded migration `SQL/migrations/migrate_741_genealogy_table.sql`, updated seed fixtures, and schema docs. Bumped `ExpectedSchemaVersion` to `8` (the table rename is not backward-compatible) ([#741](https://github.com/Jolls/arx-legacy/issues/741))

## [0.6.9] - 2026-07-18
### Added
- Added the `unit` table — the Tier-3 serialized-instance (keystone) of the traceability data model: a serial number becomes a real row with FKs (`part_id`, nullable `lot_id`, nullable `build_id`, `serial_number` unique per part) rather than a parsed string. A `CK_unit_provenance` CHECK enforces every unit traces to at least a lot or a build. Additive and inert until a later slice wires create/render logic, so `ExpectedSchemaVersion` is deliberately unchanged; ships DDL for both SQL Server and Postgres, the guarded migration `SQL/migrations/migrate_740_unit_table.sql`, seed fixtures, and schema docs. Also renamed the stale `Config.UnitTable()` helper (which returned `"uom"`) to `UomTable()`, freeing `UnitTable()` for the new table ([#740](https://github.com/Jolls/arx-legacy/issues/740))

## [0.6.8] - 2026-07-18
### Changed
- Completed the traceability-epic slice-1 rename wave with the pure column renames the slice bullets never carved: `result.record_id` → `form_record_id`, `record_events.test_record_id` → `form_record_id`, `form_record.serial_number_pn` / `serial_number_pn_desc` → `subject_part_number` / `subject_pn_description`, and `form_record.part_number_id` → `part_id` (the form's own `form.part_number_id` deliberately unchanged). Pure renames, no behavior change, across DDL (both SQL Server and Postgres), Go, seed, and schema docs; the stored `max_subbatch_result` named query and its `@test_id` param (→ `@form_row_id`) are rewritten in lockstep. Bumped `ExpectedSchemaVersion` to `7`; ships with the guarded rename migration `SQL/migrations/migrate_769_traceability_renames.sql`, which gates the rollout via the schema banner ([#769](https://github.com/Jolls/arx-legacy/issues/769), closes [#717](https://github.com/Jolls/arx-legacy/issues/717))

## [0.6.7] - 2026-07-17
### Changed
- Renamed the unit-of-measure reference table `unit` → `uom` (and `part.unit_id` / `supplier_part.unit_id` → `uom_id`), freeing the `unit` name for the Tier-3 serialized-instance table in a later traceability-epic slice. Pure rename, no behavior change; ships with migration `SQL/migrations/migrate_rename_uom.sql` and bumps `schema_version` 5 → 6 ([#739](https://github.com/Jolls/arx-legacy/issues/739), absorbs [#712](https://github.com/Jolls/arx-legacy/issues/712))

## [0.6.6] - 2026-07-17
### Changed
- Cleaned up leftover PM/TR (Parts Master/Test Records) naming from the pre-merge separate-app era: dropped unused `TestRecordsURL`/`PartsMasterURL` config fields and `TR_URL`/`PM_URL` env vars (dead data, not referenced in any template), renamed `renderTR`/`renderPrintTR`/`trTemplateFuncs` to `renderRecords`/`renderPrintRecords`/`recordsTemplateFuncs`, `pmTemplateFuncs` to `coreTemplateFuncs`, and fixed CLAUDE.md's stale `templates/pm/`+`templates/tr/` description to match the actual unified `templates/` tree ([#764](https://github.com/Jolls/arx-legacy/issues/764))

## [0.6.5] - 2026-07-17
### Fixed
- Schema-version mismatch now blocks app routes (redirecting to `/login`) instead of only showing a banner while every page kept querying the DB, since rename migrations bump `schema_version` last, leaving a window where renamed/dropped columns could throw raw DB errors on live pages ([#720](https://github.com/Jolls/arx-legacy/issues/720))

## [0.6.4] - 2026-07-17
### Changed
- Renamed the test-record table family for the traceability data model epic (slice 1, pure rename, no behavior change): `test_record`→`form_record`, `test_result`→`result`, `test_definition`→`form_row` (`test_definition_history`→`form_row_history`, column `test_id`→`form_row_id`), across DDL (both SQL Server and Postgres), Go, seed, and schema docs. Bumped `ExpectedSchemaVersion` to `5`; a human-run guarded rename migration (`SQL/migrations/migrate_rename_test_record_family.sql`) recreates the definition-history trigger against the new names, rewrites the two stored `named_queries` that referenced the old table/column names, and gates the rollout via the schema banner ([#738](https://github.com/Jolls/arx-legacy/issues/738))
### Fixed
- Reconciled the reference/seed `named_queries` set (`SQL/named_queries.sql`, `SQL/seed_test_data.sql`) with production, which had drifted: `fil_category_for_pn` now selects the attachment `comment` (was incorrectly `category`, from an un-applied migration that no longer exists), `recent_serial_numbers_for_form` is `multi`, and `pos_for_pn`/`vendor_pns_for_pn` match the production query text ([#738](https://github.com/Jolls/arx-legacy/issues/738))

## [0.6.3] - 2026-07-17
### Changed
- Expanded `SQL/seed_test_data.sql` into a richer PRE-state migration testbed for the traceability data model epic: a manually-adjusted lot with no `po_line_id`/owning build (the third `lot.source` origin), and a full receipt→incoming-inspection→build→build→final-test chain (new lot-tracked assembly 3013) with a two-level, branching lot genealogy tree, so later epic slices can dry-run their migrations against real rows ([#737](https://github.com/Jolls/arx-legacy/issues/737))

## [0.6.2] - 2026-07-17
### Fixed
- Folder-root settings (`DOC_CONTROL_ROOT`, `PO_FOLDER_ROOT`, `SUPPLIER_FILES_ROOT`, `IMAGE_ROOT`) under a user's profile directory (e.g. OneDrive) are now stored with a `%USERPROFILE%` token instead of a hardcoded path, so a shared/OneDrive `Arx.exe`'s `config/local.json` resolves correctly for every user instead of only the one who last saved Settings ([#731](https://github.com/Jolls/arx-legacy/issues/731))

## [0.6.1] - 2026-07-17
### Security
- Moved the DB passwords and session-signing secret out of the shared, exe-adjacent `config/local.json` into a per-user store (`%APPDATA%\Arx\local.json`; `~/.config/arx/local.json` on Linux). A shared/OneDrive `Arx.exe` no longer exposes one user's plaintext DB password to everyone with folder access or lets any user forge another's session cookie via a shared signing key. First run after upgrade migrates existing secrets into the per-user file and scrubs them from the shared file ([#732](https://github.com/Jolls/arx-legacy/issues/732))

## [0.6.0] - 2026-07-16
### Changed
- Bumped `ExpectedSchemaVersion` to `4` for the non-backwards-compatible schema changes below (column drops, the `named_queries` rename, and the `release_status` CHECK); a mismatched binary/DB now shows the schema banner until `SQL/migrations/migrate_schema_v4.sql` is run last after the four v4 migrations ([#540](https://github.com/Jolls/arx-legacy/issues/540))
- Renamed the `named_queries.active` column to `is_active` for naming consistency with every other table; updated the Go queries and reference DDL, with a human-run rename migration (`SQL/migrations/migrate_rename_named_queries_active.sql`) ([#573](https://github.com/Jolls/arx-legacy/issues/573), [#540](https://github.com/Jolls/arx-legacy/issues/540))
- Added a `CK_part_number_release_status` CHECK constraint restricting `part.release_status` to `U`/`A`/`D` at the DB level, with a human-run migration (`SQL/migrations/migrate_release_status_check.sql`) ([#542](https://github.com/Jolls/arx-legacy/issues/542), [#540](https://github.com/Jolls/arx-legacy/issues/540))
### Removed
- Dropped the unused `part.has_bom` column (BOM presence is computed on demand via `EXISTS(bom)` since #555), with a human-run migration (`SQL/migrations/migrate_drop_has_bom.sql`) ([#540](https://github.com/Jolls/arx-legacy/issues/540))
- Dropped the unused `contact.user_account_link` column (the User Account field was removed from the contact form/detail in #534) and its remaining read-only Go references, with a human-run migration (`SQL/migrations/migrate_drop_contact_user_account_link.sql`) ([#534](https://github.com/Jolls/arx-legacy/issues/534), [#540](https://github.com/Jolls/arx-legacy/issues/540))
- Hid the test-record lot/build linkage UI (lot/build pickers on the record editor, lot/build display rows on the record view) — the feature isn't finished for 0.6.0; the `lot_id`/`build_id` columns and data are untouched, and the completion gate requiring a lot on lot-tracked records is disabled since there's no picker to satisfy it. Deferred to v0.7.0's Lot & Serial epic ([#677](https://github.com/Jolls/arx-legacy/issues/677), [#687](https://github.com/Jolls/arx-legacy/issues/687))

## [0.5.131] - 2026-07-16
### Added
- Cross-part "All Lots" list at `/lots` for browsing lots without drilling into a part first, styled like the Parts table with sortable headers (Lot Number, Vendor Lot, Lot Description, Part Number, Part Description, Created, Status) ([#701](https://github.com/Jolls/arx-legacy/issues/701))
- Edit form for a lot's Description and Vendor Lot, reachable from the lot's trace page ([#701](https://github.com/Jolls/arx-legacy/issues/701))

## [0.5.130] - 2026-07-15
### Changed
- Reformatted CHANGELOG.md to follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/): added an `[Unreleased]` section, grouped every version's entries under `Added`/`Changed`/`Fixed`/`Removed`/`Security` subheadings, and dropped the per-version author suffix; updated CLAUDE.md's changelog instructions to match ([#699](https://github.com/Jolls/arx-legacy/issues/699))

## [0.5.129] - 2026-07-15
### Changed
- Renamed the `TestForm` and `TestRecord` Go struct fields (and the records-domain `BOMPart`/`formPN`/records-list view structs) to match their renamed snake_case DB columns (`PNID`→`PartNumberID`, `Locked`→`IsLocked`, `Approved`→`IsApproved`, `Active`→`IsActive`); no behavior change ([#693](https://github.com/Jolls/arx-legacy/issues/693))

## [0.5.128] - 2026-07-14
### Changed
- Renamed the `Contact` Go struct fields (and the `Supplier` contact join plus the `ContactSummary`/`siblingContact` view structs) from `CN`-prefixed names to match the renamed snake_case `contact` DB columns (`CNName`→`DisplayName`, `CNID`→`ID`, `CNDateModified`→`UpdatedAt`, etc.); no behavior change ([#693](https://github.com/Jolls/arx-legacy/issues/693))

## [0.5.127] - 2026-07-14
### Changed
- Renamed the `PurchaseOrderLine` Go struct fields (`POLQty`→`Qty`, `POLID`→`ID`, etc.) to match their renamed snake_case `po_line` DB columns; no behavior change ([#693](https://github.com/Jolls/arx-legacy/issues/693))

## [0.5.126] - 2026-07-14
### Changed
- Renamed the `Part`, `Attachment`, and `BOMItem` Go struct fields (and the `SupplierPart` part join) to match their renamed snake_case DB columns; no behavior change ([#693](https://github.com/Jolls/arx-legacy/issues/693))

## [0.5.125] - 2026-07-13
### Fixed
- Fixed auto-issued lot numbers colliding across multiple lot-tracked lines/partial receipts on the same PO by defaulting `lot_number` to the lot's own id instead of the PO number/build reference; added a `lot_description` column that instead carries that human-readable provenance ("PO 5003" / "Build #8202" / "Manual entry"), shown on the Lots subtab and lot detail ([#687](https://github.com/Jolls/arx-legacy/issues/687))
- Fixed the Lots subtab not appearing on a lot-tracked part's main Details page until another subtab was visited first (`PartDetail` wasn't loading `is_lot_tracked`) ([#687](https://github.com/Jolls/arx-legacy/issues/687))
### Changed
- A test record for a lot-tracked part can no longer be marked Complete without a lot selected ([#687](https://github.com/Jolls/arx-legacy/issues/687))

## [0.5.124] - 2026-07-13
### Added
- Added a tooltip to test record rows backed by a named query, showing the query's description on hover ([#688](https://github.com/Jolls/arx-legacy/issues/688))

## [0.5.123] - 2026-07-13
### Added
- Linked test records to the lot/build that produced the tested unit: the record editor now has Lot and Build fields (saved with the record's normal Save) for picking the unit's lot (on lot-controlled parts) and/or the build that made it, plus a "Build this unit" button that jumps to the Build form and links the resulting build and its output lot back to the record on return — auto-restoring your in-progress results when you come back — so a serial number traces through to the component lots it consumed even when the assembly itself isn't lot-controlled; the inventory ledger now also stamps each build's issue/receipt rows with the build id ([#677](https://github.com/Jolls/arx-legacy/issues/677))

## [0.5.122] - 2026-07-13
### Added
- Added a read-only Lot column to the part Transactions tab; adjusting a lot-controlled part now requires attributing the movement to an existing lot or a newly created one, matching the lot requirement builds already enforce on component consumption ([#682](https://github.com/Jolls/arx-legacy/issues/682))

## [0.5.121] - 2026-07-12
### Added
- Added lot/batch control: a new "Batch / lot controlled" flag on parts makes goods receipt create a lot record (defaulting the lot number to the PO number, with an optional vendor lot number per line), and makes a build of a lot-controlled assembly create an output lot and record its genealogy — one edge per lot-controlled component consumed, picked from that component's active lots on the Build form — so an output lot can be traced back through its component lots to the raw vendor lots ([#676](https://github.com/Jolls/arx-legacy/issues/676))
- Added a Lots subtab on lot-controlled parts: it lists the part's lots (lot #, vendor lot, source PO/build, created, status) and, per lot, traces its genealogy both directions — ancestors down to the raw vendor lots it came from, and the downstream assemblies it was consumed into ([#676](https://github.com/Jolls/arx-legacy/issues/676))
### Changed
- Reworked the Build subtab to show the consumed BOM components up front with quantity per assembly, live required quantity, and current on-hand (shortages highlighted inline), replacing the previous after-the-fact shortage notice; the lot picker now shows for any lot-controlled component being consumed, regardless of whether the assembly being built is itself lot-controlled ([#676](https://github.com/Jolls/arx-legacy/issues/676))
- Made the inventory ledger lot-aware: each stock movement records the lot it touched (inventory_transaction.lot_id) — the lot created on a receipt, the component lot consumed by a build, and the output lot produced — so a consumed component lot is captured even when the built assembly isn't lot-controlled ([#676](https://github.com/Jolls/arx-legacy/issues/676))

## [0.5.120] - 2026-07-12
### Fixed
- Fixed the Sent, Labor, and Part badges shifting to the accent color theme (e.g. unreadable white-on-teal) by giving them fixed colors instead of the theme-linked bg-primary variant, and fixed the /pos table's client-side JS having its own separate (and now out-of-sync) copy of the same badge markup

## [0.5.119] - 2026-07-12
### Added
- Added a generic Build flow on assemblies: a Build subtab consumes the part's BOM components (an inventory issue per line) and produces the output part (a receipt) in one transaction, keeping stock on hand in sync on both sides, with a non-blocking notice when a consumed component (e.g. an unbuilt sub-assembly) drops below zero on hand; no lot control yet ([#675](https://github.com/Jolls/arx-legacy/issues/675))
### Changed
- Enforced category-tab gating server-side across all part subtabs (BOM, Build, Order History, Transactions, Pricing, Mfg Parts, Suppliers) so a subtab hidden for a part's category can no longer be viewed or posted to via a direct URL; builds additionally skip non-stocked BOM components (e.g. OPS labor) instead of issuing them ([#675](https://github.com/Jolls/arx-legacy/issues/675))

## [0.5.118] - 2026-07-11
### Added
- Gave Test Mode a full second connection profile (server, engine, DB name, username, password) so ArxDev can live on a separate Postgres server while ArxProd stays on SQL Server; blank test fields inherit the prod value, and the fields are editable in Settings' new Test Connection section ([#672](https://github.com/Jolls/arx-legacy/issues/672))

## [0.5.117] - 2026-07-11
### Changed
- Ported the SQL Server triggers to Postgres (denormalized count maintenance and the test_definition audit-history snapshot) and routed the audit user through the Dialect (CONTEXT_INFO on SQL Server, an arx.username session GUC on Postgres); no behavior change on SQL Server ([#670](https://github.com/Jolls/arx-legacy/issues/670))

## [0.5.116] - 2026-07-11
### Added
- Introduced a Dialect abstraction over SQL-Server-specific SQL (pagination, insert-and-get-id, TRY_CAST, DATEFROMPARTS, ISNULL/COALESCE) as groundwork for Postgres/SQLite support; no behavior change on SQL Server ([#625](https://github.com/Jolls/arx-legacy/issues/625))

## [0.5.115] - 2026-07-10
### Added
- Added per-part reorder points: a Reorder Minimum field, below-minimum flags on the parts list and part detail, and a Below Reorder Point card on the Reports dashboard ([#273](https://github.com/Jolls/arx-legacy/issues/273))

## [0.5.114] - 2026-07-10
### Changed
- Moved Test Mode and Debug Mode to Bootstrap toggle switches at the top of the Database Connection section in Settings, and fixed stale Debug Mode label ([#656](https://github.com/Jolls/arx-legacy/issues/656))

## [0.5.113] - 2026-07-10
### Added
- Added Supplier On-Time Delivery, PO Cycle Time, and Attachment/Data Quality Gaps reports to the Reports tab ([#659](https://github.com/Jolls/arx-legacy/issues/659))

## [0.5.112] - 2026-07-10
### Added
- Added Stale WIP Records and POs Pending Approval summary cards to the Reports dashboard ([#658](https://github.com/Jolls/arx-legacy/issues/658))

## [0.5.111] - 2026-07-10
### Added
- Added a Failure Modes report per test form — ranks test steps by failure count, with total tested and failure rate, over a selectable date range ([#245](https://github.com/Jolls/arx-legacy/issues/245))
- Added Top Failing Steps and Lowest Yield Forms summary cards to the Reports dashboard ([#245](https://github.com/Jolls/arx-legacy/issues/245), [#244](https://github.com/Jolls/arx-legacy/issues/244))

## [0.5.110] - 2026-07-10
### Added
- Added a Yield Summary view per test form — total/passed/failed record counts and first-pass yield % over a selectable date range, optionally grouped by month ([#244](https://github.com/Jolls/arx-legacy/issues/244))

## [0.5.109] - 2026-07-10
### Added
- Added a Spend Analysis subtab under Reports — total spend by supplier and by part over This Month/This Quarter/YTD/custom date ranges, with CSV export ([#283](https://github.com/Jolls/arx-legacy/issues/283))

## [0.5.108] - 2026-07-10
### Added
- Added a Reports tab with a KPI dashboard — open POs, POs received this month, and a recent-activity feed ([#282](https://github.com/Jolls/arx-legacy/issues/282))
### Changed
- Made the home/landing page a per-user preference (any tab or a custom filtered link); the app root now redirects there and the parts list moved to `/parts` ([#282](https://github.com/Jolls/arx-legacy/issues/282))

## [0.5.107] - 2026-07-09
### Added
- Added per-user preset accent color themes (blue/indigo/teal/green/slate) on Settings → My Preferences, so primary-colored elements no longer blend into the app header ([#537](https://github.com/Jolls/arx-legacy/issues/537))

## [0.5.106] - 2026-07-09
### Security
- Replaced the hardcoded `"change-me-in-production"` session secret with an auto-generated random key persisted to `local.json`, and hardened the session cookie (`HttpOnly`, `SameSite=Lax`); regression of [#352](https://github.com/Jolls/arx-legacy/issues/352) reintroduced in the two-app merge ([#648](https://github.com/Jolls/arx-legacy/issues/648))

## [0.5.105] - 2026-07-09
### Changed
- Reorganized templates/ and static/ into tab-based subfolders (parts, suppliers, pos, contacts, settings, records, shared), replacing the leftover pm/tr split from the old separate-apps era

## [0.5.104] - 2026-07-09
### Changed
- Vendor PN on a PO line now links to the linked part's primary attachment ([#642](https://github.com/Jolls/arx-legacy/issues/642))
- Renamed "Supplier Code" to "Folder Stub" with a tooltip clarifying it names the supplier's attachment/PO folders, and validated it against filesystem-breaking characters on save ([#638](https://github.com/Jolls/arx-legacy/issues/638))

## [0.5.103] - 2026-07-08
### Fixed
- Fixed PO line items with only a vendor part number (or only qty/cost/rev) entered being silently dropped instead of saved ([#639](https://github.com/Jolls/arx-legacy/issues/639))

## [0.5.102] - 2026-07-08
### Removed
- Retired the legacy "None" sentinel part used as an Excel-era PO line spacer/comment workaround; migrated ~4993 po_line rows to use a null part_id (already supported) instead ([#561](https://github.com/Jolls/arx-legacy/issues/561))

## [0.5.101] - 2026-07-07
### Changed
- PO defaults are now per-user: buyers set their own default receiver/contact on a new Settings → My Preferences tab; the machine-level global PO defaults have been removed ([#463](https://github.com/Jolls/arx-legacy/issues/463))

## [0.5.100] - 2026-07-07
### Changed
- Force a re-login when Test Mode is toggled so writes are always attributed to a real user in the now-active database ([#631](https://github.com/Jolls/arx-legacy/issues/631))

## [0.5.99] - 2026-07-07
### Fixed
- arx: audited and backfilled pre-auth changed_by/username values in test_definition_history, form_events, and record_events ([#630](https://github.com/Jolls/arx-legacy/issues/630))

## [0.5.98] - 2026-07-08
### Removed
- arx: removed the SYSTEM_USER fallback from trg_test_definition_history now that all instances run the F1+ binary ([#461](https://github.com/Jolls/arx-legacy/issues/461))

## [0.5.97] - 2026-07-07
### Changed
- Deduplicate the attachment filename convention: the Browse live preview now fetches the name from a server endpoint instead of mirroring the naming rule in JS ([#558](https://github.com/Jolls/arx-legacy/issues/558))
### Fixed
- Fix the row-action kebab menu getting clipped for rows near the bottom of a table

## [0.5.96] - 2026-07-06
### Removed
- Drop the redundant `has_bom` column; compute BOM presence on demand instead of maintaining a denormalized flag ([#555](https://github.com/Jolls/arx-legacy/issues/555))

## [0.5.95] - 2026-07-06
### Changed
- Reduce redundant SQL round trips on part detail and parts pages by consolidating attachment queries and caching part categories ([#618](https://github.com/Jolls/arx-legacy/issues/618))

## [0.5.94] - 2026-07-06
### Changed
- Cache the logged-in session user server-side (with TTL + invalidation on permission/active-status changes) instead of re-querying it from the DB on every request ([#620](https://github.com/Jolls/arx-legacy/issues/620))

## [0.5.93] - 2026-07-06
### Changed
- Show a browser-tab favicon matching the active Parts Master tab, and update the Test Records favicon to the current icon design ([#621](https://github.com/Jolls/arx-legacy/issues/621))

## [0.5.92] - 2026-07-06
### Added
- Add per-request SQL round-trip count and timing to debug logs, with a per-route running summary and a curated route-profiling integration test ([#613](https://github.com/Jolls/arx-legacy/issues/613))

## [0.5.91] - 2026-07-06
### Changed
- Deduplicate the shared PM/TR page layout into one template ([#614](https://github.com/Jolls/arx-legacy/issues/614))

## [0.5.90] - 2026-07-06
### Changed
- Make the parts list "Type" filter a dropdown of configured categories instead of free text ([#611](https://github.com/Jolls/arx-legacy/issues/611))
- Split the price-history chart's price-list points into one series per pack-size tier so qty breaks no longer blend into a single trend line ([#612](https://github.com/Jolls/arx-legacy/issues/612))

## [0.5.89] - 2026-07-06
### Added
- Add qty-break-aware "Cost to Build" calculator on the BOM edit page: consolidates each leaf part's demand across every occurrence in the tree and prices it at the matching quantity-break tier ([#466](https://github.com/Jolls/arx-legacy/issues/466))

## [0.5.88] - 2026-07-06
### Added
- Suggest the next available part base number on the New Part form, configurable in Settings (separator, segment position, zero-pad width, max+1 or gap-filling mode) ([#346](https://github.com/Jolls/arx-legacy/issues/346))

## [0.5.87] - 2026-07-05
### Added
- Add company logo to the UI header, login page, and PO PDFs, uploadable in Settings ([#538](https://github.com/Jolls/arx-legacy/issues/538))

## [0.5.86] - 2026-07-05
### Added
- Add a "where used" view showing every part and vendor that links a given file ([#557](https://github.com/Jolls/arx-legacy/issues/557))
### Changed
- Collapse the part/vendor attachment row actions (Set default, Edit, Where used, Delete) into a single kebab menu ([#606](https://github.com/Jolls/arx-legacy/issues/606))

## [0.5.85] - 2026-07-05
### Added
- Show a contact's associated purchase orders on the contact detail page, linking POs to contacts via new nullable FK columns while keeping the printed name snapshot ([#597](https://github.com/Jolls/arx-legacy/issues/597))
- Link the supplier/receiver contact on the PO detail page to its contact record when the PO carries a contact id ([#597](https://github.com/Jolls/arx-legacy/issues/597))
- Add an "Other Contacts" card to the vendor detail page listing the vendor's non-default contacts

## [0.5.84] - 2026-07-04
### Added
- Add a Utilities section under Settings with read-only data-integrity diagnostics: dead attachment links, orphaned part pointers, soft-deleted primary attachments, and PO is_active drift ([#601](https://github.com/Jolls/arx-legacy/issues/601))

## [0.5.83] - 2026-07-04
### Changed
- Auto-detect attachment link types on input and render absolute/UNC paths as a copy-path control; keep `LOCAL:` internal-only (hidden from all views) ([#599](https://github.com/Jolls/arx-legacy/issues/599))

## [0.5.82] - 2026-07-03
### Added
- Add show/hide inactive toggle to the contacts list, defaulting to hidden, matching other list pages ([#596](https://github.com/Jolls/arx-legacy/issues/596))

## [0.5.81] - 2026-07-03
### Added
- Add clipboard-paste image attachments for test-record result steps (new `pf_type = "attach"`), reusing the parts paste pattern ([#587](https://github.com/Jolls/arx-legacy/issues/587))

## [0.5.80] - 2026-07-03
### Added
- Add clipboard-paste image attachments for parts (category "Photo"), with a thumbnail grid on the part detail page and hover-preview on the attachments list ([#587](https://github.com/Jolls/arx-legacy/issues/587))

## [0.5.79] - 2026-07-03
### Added
- Add a free-text comment field to part attachments, shown as a list column and editable via Add/Edit ([#585](https://github.com/Jolls/arx-legacy/issues/585))

## [0.5.78] - 2026-07-03
### Added
- Add per-tab icons to the Parts/Vendors/POs/Contacts/Records nav bar, with a distinct icon for the active tab

## [0.5.77] - 2026-07-02
### Added
- Add the Arx icon to the upper-left header bar ([#586](https://github.com/Jolls/arx-legacy/issues/586))

## [0.5.76] - 2026-07-02
### Added
- Add a Named Queries editor in Settings to create, edit, and deactivate `spec_nom` named queries without raw SQL, saved per row, with a per-row test-drive that runs the query and builds a copy-ready `spec_nom` string ([#573](https://github.com/Jolls/arx-legacy/issues/573))

## [0.5.75] - 2026-07-02
### Added
- Add Expand All / Collapse All to assembly BOMs so sub-assembly BOM lines can be viewed nested inline, indented and sub-numbered ([#579](https://github.com/Jolls/arx-legacy/issues/579))

## [0.5.74] - 2026-07-02
### Added
- Add a description autocomplete to the PO add-item form so parts can be searched by description as well as part number ([#580](https://github.com/Jolls/arx-legacy/issues/580))
### Fixed
- Fix the new BOM line quantity field to hint "0" instead of "1", matching the value actually saved when left blank ([#578](https://github.com/Jolls/arx-legacy/issues/578))

## [0.5.73] - 2026-07-02
### Fixed
- Fix a template panic ("error calling len") that crashed the PO detail page after saving when a PO produced only one kind of post-save suggestion (new vendor PN or new price, but not both)

## [0.5.72] - 2026-07-02
### Changed
- Replace `SQL/_test.sql` (prod-clone) with `SQL/seed_test_data.sql`: a documented, fixed-ID synthetic reference dataset covering every PO/RFQ/test-record lifecycle state for ArxDev ([#545](https://github.com/Jolls/arx-legacy/issues/545))

## [0.5.71] - 2026-07-02
### Changed
- Merge the two post-save PO suggestion banners (supplier links and pricing) into one banner so submitting or dismissing it no longer discards the other suggestion ([#567](https://github.com/Jolls/arx-legacy/issues/567))

## [0.5.70] - 2026-07-01
### Changed
- Show the primary attachment plus up to 5 more (by order) on the Part Details view, instead of just the primary ([#562](https://github.com/Jolls/arx-legacy/issues/562))

## [0.5.69] - 2026-07-01
### Added
- Add a view-only "Available named queries" reference to the form definition editor so authors can see which named queries exist and what parameters they take ([#564](https://github.com/Jolls/arx-legacy/issues/564))

## [0.5.68] - 2026-07-01
### Added
- Auto-fill the Order Number field with the next available value when adding a part or supplier attachment ([#565](https://github.com/Jolls/arx-legacy/issues/565))

## [0.5.67] - 2026-07-01
### Added
- Add an Order History sub-tab to the vendor detail page, listing all POs placed with that vendor ([#566](https://github.com/Jolls/arx-legacy/issues/566))
- Add `.gitattributes` to make line-ending handling explicit (LF for source/docs, CRLF for Windows scripts) and stop editing tools from flipping endings
### Fixed
- Fix a nil-pointer panic on `/settings` when the DB auto-connect fails on startup

## [0.5.66] - 2026-07-01
### Added
- Add a Browse button to part attachments that copies (or moves, via a toggle) the picked file into Doc Control, renaming it `<Part> <Rev> <Title> <Category>.<ext>`; offers to link to an existing file on name collision ([#547](https://github.com/Jolls/arx-legacy/issues/547))

## [0.5.65] - 2026-07-01
### Added
- Duplicate a part, including its BOM, from the part detail view ([#548](https://github.com/Jolls/arx-legacy/issues/548))
### Fixed
- BOM edits now keep the part's `has_bom` flag in sync (previously never maintained) ([#548](https://github.com/Jolls/arx-legacy/issues/548))

## [0.5.64] - 2026-07-01
### Added
- Part Title now shown alongside the part number in the breadcrumb on every part subtab ([#552](https://github.com/Jolls/arx-legacy/issues/552))

## [0.5.63] - 2026-07-01
### Added
- Parts list and BOM view now show Attachments and PO Lines counts, with a Columns dropdown to hide/show any column ([#550](https://github.com/Jolls/arx-legacy/issues/550))

## [0.5.62] - 2026-07-01
### Changed
- Redesign Part, Supplier, and Contact detail views as 2-wide dashboard grids summarizing sibling sub-tabs ([#521](https://github.com/Jolls/arx-legacy/issues/521))

## [0.5.61] - 2026-06-30
### Changed
- Supplier fields on Add/Edit Price, Add/Edit Contact, Add/Edit Supplier Sourcing, and the Settings default receiver now use search-as-you-type instead of a plain dropdown ([#528](https://github.com/Jolls/arx-legacy/issues/528))
- Add Price now defaults Effective Date to today ([#528](https://github.com/Jolls/arx-legacy/issues/528))

## [0.5.60] - 2026-06-30
### Changed
- Part Release Status now always has a value: new parts default to "Under Review", the blank option is gone, and the column is enforced `NOT NULL` in the database ([#542](https://github.com/Jolls/arx-legacy/issues/542))

## [0.5.59] - 2026-06-30
### Changed
- Renamed the part "Add Supplier Link" control to "Add Supplier" ([#531](https://github.com/Jolls/arx-legacy/issues/531))
### Removed
- Removed the redundant "Active" and "Has BOM" checkboxes from the part form; a part's active state now derives from its Release Status (Deprecated = inactive) and BOM visibility is governed by the part type ([#532](https://github.com/Jolls/arx-legacy/issues/532))
- Removed the unused "User Account" field from the contact form and detail page ([#534](https://github.com/Jolls/arx-legacy/issues/534))

## [0.5.58] - 2026-06-30
### Added
- New Price History tab on the part detail page: an SVG chart of unit cost over time, one point per purchase-order line plus any price-list entries, with hover tooltips showing PO number, supplier, date, and cost ([#284](https://github.com/Jolls/arx-legacy/issues/284))

## [0.5.57] - 2026-06-30
### Added
- Filter, sort, and page state on every main table (Parts, POs, Suppliers, Contacts, Test Records) now mirrors into the URL, so a filtered view can be bookmarked or shared; a pasted link reproduces the sender's view and wins over saved session state ([#513](https://github.com/Jolls/arx-legacy/issues/513))

## [0.5.56] - 2026-06-30
### Fixed
- Fixed needing to double-click a row link after filtering a list; a redundant `change` listener re-rendered the table on blur and swallowed the first click ([#525](https://github.com/Jolls/arx-legacy/issues/525))

## [0.5.55] - 2026-06-30
### Fixed
- Fixed new attachments saving with an empty Category; the add form's selected category is now persisted instead of an unused field ([#527](https://github.com/Jolls/arx-legacy/issues/527))

## [0.5.54] - 2026-06-30
### Changed
- Swept hand-written inline styles on the settings, login, PO, attachment, sourcing, and pricing pages over to Bootstrap utility classes; kept only genuinely custom values (fixed pixel widths, grid layouts) inline ([#500](https://github.com/Jolls/arx-legacy/issues/500))

## [0.5.53] - 2026-06-30
### Changed
- Form fields, select boxes, and inline validation now use native Bootstrap `form-control`/`form-select`/`is-invalid` styling instead of hand-maintained CSS clones; removed the duplicate rules from app.css ([#500](https://github.com/Jolls/arx-legacy/issues/500))

## [0.5.52] - 2026-06-30
### Changed
- Settings and login pages now use Bootstrap alert/badge/table components instead of hand-rolled inline-styled equivalents, and the login button uses the proper `btn btn-primary` styling ([#500](https://github.com/Jolls/arx-legacy/issues/500))

## [0.5.51] - 2026-06-30
### Changed
- Test record lists now use the same client-side filterable/sortable table as Parts/POs/Suppliers/Contacts, instead of full-page reloads ([#502](https://github.com/Jolls/arx-legacy/issues/502))
### Added
- Date columns on Parts, POs, Contacts, and Test Records now support a From/To range filter ([#502](https://github.com/Jolls/arx-legacy/issues/502))

## [0.5.50] - 2026-06-29
### Changed
- Test record headers (type 1/2/3) are now collapsible, hiding rows until the next header of the same or shallower level ([#509](https://github.com/Jolls/arx-legacy/issues/509))

## [0.5.49] - 2026-06-29
### Added
- Formal revision numbers on form definitions, captured on test records ([#260](https://github.com/Jolls/arx-legacy/issues/260))

## [0.5.48] - 2026-06-28
### Added
- Test records now capture a snapshot of their results each time they are completed; expand a "completed" entry in a record's audit log to see the result values at that moment with changes since the previous completion highlighted. Includes a one-time bulk action (TR reviewers, Complete view) to backfill history for already-completed records ([#251](https://github.com/Jolls/arx-legacy/issues/251))

## [0.5.47] - 2026-06-28
### Changed
- Test steps with a conditional hide formula now show/hide live in the record editor as you enter results, instead of only when the record is reloaded ([#257](https://github.com/Jolls/arx-legacy/issues/257))

## [0.5.46] - 2026-06-28
### Added
- Advanced filters (status, type, date range) on the test-record list, persisted as shareable query params; replaces the WIP-only toggle with a Status dropdown ([#247](https://github.com/Jolls/arx-legacy/issues/247))

## [0.5.45] - 2026-06-28
### Changed
- Concurrent "New Record" creations for the same form now get distinct sequential serial numbers — the serial number is allocated atomically at save time instead of when the form opens ([#369](https://github.com/Jolls/arx-legacy/issues/369))

## [0.5.44] - 2026-06-27
### Added
- BOM cost rollup now sources each part's leaf cost from its preferred supplier's cheapest active price (falling back to the unit cost), with a "Set preferred" control on the Pricing tab ([#465](https://github.com/Jolls/arx-legacy/issues/465))
- New OPS (Operation / Labor) part category — add a labor job to an assembly's BOM with qty = hours and the rollup includes hours × hourly rate; the unit cost / rate is now editable on the part form ([#465](https://github.com/Jolls/arx-legacy/issues/465))

## [0.5.43] - 2026-06-27
### Added
- "Duplicate" button on a test record creates a new WIP record with the same serial number, dated today, and all results copied, for quick re-testing ([#254](https://github.com/Jolls/arx-legacy/issues/254))

## [0.5.42] - 2026-06-27
### Added
- Bulk "Lock selected" action on the Test Records list marks multiple WIP records Complete at once ([#253](https://github.com/Jolls/arx-legacy/issues/253))

## [0.5.41] - 2026-06-25
### Added
- Test records now have a three-state lifecycle: WIP → Complete → Approved. Any user marks a record Complete; a TR reviewer Approves it, after which only a reviewer can unlock ([#249](https://github.com/Jolls/arx-legacy/issues/249))
- New per-user "TR Reviewer" permission, toggled in Settings → Users; gates approving and unlocking approved records ([#249](https://github.com/Jolls/arx-legacy/issues/249))
- Record detail page shows a collapsible audit log of complete/approve/unlock events with user, timestamp, and unlock reason ([#250](https://github.com/Jolls/arx-legacy/issues/250))

## [0.5.40] - 2026-06-24
### Added
- CSV export for parts list, PO list (one row per line item), and BOM with costs — download buttons on each list page ([#286](https://github.com/Jolls/arx-legacy/issues/286))

## [0.5.39] - 2026-06-24
### Changed
- Enter key in result/comment inputs advances focus to next input instead of submitting the form ([#258](https://github.com/Jolls/arx-legacy/issues/258))
### Added
- Auto-save partial results to localStorage every 30s; restore/discard banner on re-entering the edit page; draft cleared on save ([#259](https://github.com/Jolls/arx-legacy/issues/259))

## [0.5.37] - 2026-06-24
### Changed
- wrap `CreateRecord` record insert + step materialization in a single transaction so a record is either fully created+materialized or not at all ([#490](https://github.com/Jolls/arx-legacy/issues/490))

## [0.5.36] - 2026-06-23
### Added
- freeze saved test records to a materialized snapshot — every applicable step (incl. section headings) is captured into the record at creation, so a record renders spec/parameter/limits/units/pf_type/format/headings and evaluates pass/fail entirely from what it was created with, never the live definition; editing a form no longer changes existing records ([#487](https://github.com/Jolls/arx-legacy/issues/487))
- schema — adds `test_result.pf_type`, `format`, `type`, `hide_formula`, `default_result` snapshot columns; additive and rollback-safe — run `SQL/migrations/migrate_record_snapshot.sql` against ArxProd and ArxDev before deploying ([#487](https://github.com/Jolls/arx-legacy/issues/487))
### Changed
- editing a record always works against its frozen snapshot; "Update to latest" on an unlocked record is the only way to re-pull the current definition (refreshing the snapshot, materializing newly-added steps, and re-evaluating pass/fail) ([#487](https://github.com/Jolls/arx-legacy/issues/487))

## [0.5.35] - 2026-06-23
### Added
- archive/retire a test step instead of the `hide_formula="HIDE"` workaround — archived steps drop off new records and the live definition view, stay rendered on historical records that already recorded a result for them, and can be archived/restored from the definition editor with a "Show archived" toggle on the definition view ([#403](https://github.com/Jolls/arx-legacy/issues/403))
- schema — adds `test_definition.archived` (BIT, defaulted); additive and rollback-safe — run `SQL/migrations/migrate_tr_archive.sql` against ArxProd and ArxDev before deploying ([#403](https://github.com/Jolls/arx-legacy/issues/403))

## [0.5.34] - 2026-06-23
### Added
- import a part's LOCAL: file into the PO folder from the PO detail page ([#156](https://github.com/Jolls/arx-legacy/issues/156))

## [0.5.33] - 2026-06-23
### Added
- hide/show columns on the Records list, Test Results, and Form Definition tables — toggle per-column visibility from a Columns dropdown; preference persists across navigation in localStorage ([#386](https://github.com/Jolls/arx-legacy/issues/386))
- filter/sort state preserved on list pages — navigating to a record and back restores the filter and sort that was active ([#390](https://github.com/Jolls/arx-legacy/issues/390))

## [0.5.32] - 2026-06-23
### Added
- PO receiving / goods receipt — receive line items (partial or full) from the PO detail page; each receipt posts to the inventory ledger so stock-on-hand rises, the PO auto-advances to Partially Received or Closed, and receipt history is shown on the PO. Receipts cross-link to the part's transactions, and receipt rows on the transactions tab link back to the originating PO ([#269](https://github.com/Jolls/arx-legacy/issues/269))
- schema — adds `po_line.received_qty` (defaulted) and `po_line.date_received` (nullable); additive and rollback-safe — run `SQL/migrations/migrate_po_receiving.sql` against ArxProd and ArxDev before deploying ([#269](https://github.com/Jolls/arx-legacy/issues/269))

## [0.5.31] - 2026-06-23
### Added
- Parts and Vendors lists gain a "Show inactive" toggle (off by default) that hides soft-deleted (inactive) rows; shown inactive rows are styled muted/struck-through ([#477](https://github.com/Jolls/arx-legacy/issues/477))
- added a "PO Links" column to the vendor's parts page listing the POs placed with that vendor for each part ([#475](https://github.com/Jolls/arx-legacy/issues/475))
### Changed
- list pagination is now a fixed 30 rows per page (was 20), and the Test Records form and record lists are now paginated ([#479](https://github.com/Jolls/arx-legacy/issues/479))

## [0.5.30] - 2026-06-22
### Added
- Request for Quotation (RFQ) — request a quote from a supplier, add more suppliers' quotes to the same RFQ, enter each supplier's unit price and lead time per line on a side-by-side comparison grid, and award by converting the winning quote into a new PO (the RFQ and its quotes are retained, closed/cancelled, for the record). An RFQ group consumes a single PO number (quotes are `<base>R1`, `<base>R2`, … and the awarded PO is the bare `<base>`), and RFQ quotes are hidden on the PO list behind a "Show RFQs" toggle ([#270](https://github.com/Jolls/arx-legacy/issues/270))
- schema — adds `purchase_order.rfq_group_id` and `po_line.lead_time_days` (both nullable) and the `rfq` status; additive and rollback-safe — run `SQL/migrations/migrate_po_rfq.sql` against ArxProd and ArxDev before deploying ([#270](https://github.com/Jolls/arx-legacy/issues/270))
### Fixed
- fixed PO/RFQ default contact — the Settings default contact now populates the receiver contact (it was using the receiver company's own default contact instead), and the Settings default-contact dropdown is scoped to the selected default receiver's contacts

## [0.5.29] - 2026-06-21
### Changed
- db: renamed legacy tables/columns to the go-forward snake_case convention — `CN`→`contact`, `PL`→`bom`, `FIL`→`part_attachment`, `PO`/`PO_history`→`purchase_order`/`purchase_order_history`, `POL`→`po_line`, `PN`→`part_number`→`part`, and the test-records group (`Forms`→`form`, `TestRecords`→`test_record`, `TestResults`→`test_result`); columns modernized to bare `id` PKs, `{stem}_id` FKs, and `is_` boolean prefixes. Internal only — no behavior change; Go struct/field names are unchanged (reads are positional)
- db: this build expects the renamed schema — run the `SQL/migrations/migrate_rename_*.sql` scripts in commit order (contact → bom → part_attachment → purchase_order → po_line → part_number → test_records → part) against the DB before deploying
### Removed
- db: dropped vestigial `*_Test` legacy table copies (and `Tests_vba_archive_bak`) left over from before TEST_MODE used a separate ArxDev database — run `SQL/migrations/drop_legacy_test_tables.sql` against both ArxProd and ArxDev

## [0.5.28] - 2026-06-20
### Added
- pm: Inventory core — per-part stock on hand backed by an append-only `inventory_transaction` ledger; Transactions tab on stockable parts (configurable per category in Settings) with running balance + manual adjustments (reason required) ([#272](https://github.com/Jolls/arx-legacy/issues/272), [#274](https://github.com/Jolls/arx-legacy/issues/274))
### Removed
- pm: dropped the legacy `PN.PNQty` column (superseded by `stock_on_hand`); DB schema bumped to v3 — run `migrate_inventory_core.sql` before deploying this build

## [0.5.27] - 2026-06-20
### Added
- pm: PO approval workflow — Submit / Approve / Reject from the PO page; POs cannot be sent or printed until approved; editing an approved PO resets its approval; designated approvers configured via a "PO Approver" toggle in Settings → Users. Status and approval events now share one unified PO history timeline (`PO_history`, replacing `PO_status_history`) ([#267](https://github.com/Jolls/arx-legacy/issues/267))

## [0.5.26] - 2026-06-20
### Added
- pm: PO status lifecycle — Draft → Open → Sent → Partially Received → Closed (plus Cancelled), with transitions driven by buttons on the PO page, an audited status-history log (who/when), prominent status display, and a status filter on the PO list ([#271](https://github.com/Jolls/arx-legacy/issues/271))

## [0.5.25] - 2026-06-19
### Added
- pm: Settings → Configuration tab — Download Backup button exports all app tables as a ZIP of CSVs ([#467](https://github.com/Jolls/arx-legacy/issues/467))

## [0.5.24] - 2026-06-19
### Added
- arx: user identity tracking — username/password login, session-based auth, Settings → Users management ([#207](https://github.com/Jolls/arx-legacy/issues/207))
### Changed
- arx: SET CONTEXT_INFO before form-definition saves so trg_test_definition_history records the app user instead of SYSTEM_USER ([#207](https://github.com/Jolls/arx-legacy/issues/207))
- arx: lock/unlock record and form events now write the logged-in app user instead of the server OS user ([#207](https://github.com/Jolls/arx-legacy/issues/207))

## [0.5.23] - 2026-06-19
### Added
- parts_master: Suppliers tab shows active prices per supplier inline, with a link to the Pricing tab ([#457](https://github.com/Jolls/arx-legacy/issues/457))

## [0.5.22] - 2026-06-19
### Added
- parts_master: after saving a PO, offer to add new unit costs to part pricing if no matching active price exists ([#457](https://github.com/Jolls/arx-legacy/issues/457))

## [0.5.21] - 2026-06-19
### Added
- parts_master: after saving a PO, offer to add new vendor PNs to the supplier catalog if no link exists ([#455](https://github.com/Jolls/arx-legacy/issues/455))

## [0.5.20] - 2026-06-19
### Added
- parts_master: auto-populate Vendor PN when selecting a part on a PO edit if a supplier part record exists ([#444](https://github.com/Jolls/arx-legacy/issues/444))

## [0.5.19] - 2026-06-19
### Added
- parts_master: click any column header to sort Parts, Vendors, Contacts, and POs lists ascending/descending ([#448](https://github.com/Jolls/arx-legacy/issues/448))
- test_records: filter row on the Test Report page to narrow results by serial number, part number, date, result, pass/fail, or comment; Copy for Excel respects the active filter ([#449](https://github.com/Jolls/arx-legacy/issues/449))

## [0.5.18] - 2026-06-19
### Added
- parts_master: PO print page now shows the ship-to contact person's name ([#451](https://github.com/Jolls/arx-legacy/issues/451))
- parts_master: PO print page shows the PO folder path so users know where to save the PDF ([#450](https://github.com/Jolls/arx-legacy/issues/450))
- parts_master: new PO date ordered and date requested now default to today ([#445](https://github.com/Jolls/arx-legacy/issues/445))
- parts_master: adding a line item to a PO auto-increments the item number ([#443](https://github.com/Jolls/arx-legacy/issues/443))

## [0.5.17] - 2026-06-17
### Added
- arx: proportional column widths for Parts, Vendors, POs, and Contacts list tables; elastic column absorbs remaining width and scales responsively with the viewport ([#441](https://github.com/Jolls/arx-legacy/issues/441))
- arx: hovering a truncated table cell shows a native tooltip with the full text ([#441](https://github.com/Jolls/arx-legacy/issues/441))

## [0.5.16] - 2026-06-17
### Changed
- arx: unify PM and TR header bar and tab row — both now use the same structure, Bootstrap CSS, full-width tabs, and `.ActiveTab`-driven active state; fix TR header text (was "Parts Master") and hardcoded active tab
- arx: list pages (Parts, Vendors, Contacts, POs) now load the page shell instantly and fetch row data asynchronously via `/api/*/rows` JSON endpoints, eliminating the white-flash delay on navigation
- arx: client-side filtering and pagination now operate on in-memory JSON objects rather than injecting all rows into the DOM — render time dropped from ~574ms to ~5ms for 2240 rows
### Fixed
- arx: fix `start.ps1` to patch `config/local.json` (was looking for removed `local.pm.json` / `local.tr.json`) and remove stale two-port output

## [0.5.15] - 2026-06-08
### Added
- arx: add hover tooltips to column headers and field labels across Parts Master and Test Records ([#333](https://github.com/Jolls/arx-legacy/issues/333))

## [0.5.14] - 2026-06-07
### Changed
- arx: bind to 127.0.0.1 instead of 0.0.0.0 — app is localhost-only ([#361](https://github.com/Jolls/arx-legacy/issues/361))
### Fixed
- test_records: fix isSafeQuery false positives on column names containing keyword substrings (e.g. created_at, updated_at, alternate) by switching to word-boundary regex matching ([#358](https://github.com/Jolls/arx-legacy/issues/358))

## [0.5.13] - 2026-06-07
### Changed
- test_records: form definition history now shows the historical step values, not just which rows changed ([#389](https://github.com/Jolls/arx-legacy/issues/389))

## [0.5.12] - 2026-06-05
### Added
- both: add a Records tab to the Parts Master nav and show the shared Parts Master header + tab bar on Test Records pages (minimal slice of the unified shell) ([#421](https://github.com/Jolls/arx-legacy/issues/421))

## [0.5.11] - 2026-06-05
### Added
- parts_master: part subtabs (BOM, Order History, Pricing, Mfg Parts, Suppliers) now show or gray out per part category, with an editable category + tab-visibility table in Settings ([#345](https://github.com/Jolls/arx-legacy/issues/345))

## [0.5.10] - 2026-06-05
### Changed
- both: collapse the three Go packages into a single arx_go package ([#422](https://github.com/Jolls/arx-legacy/issues/422))

## [0.5.9] - 2026-06-04
### Added
- parts_master: add build-tagged live-DB integration tests for the part + attachment lifecycle (identity insert, FIL-count trigger) ([#416](https://github.com/Jolls/arx-legacy/issues/416))

## [0.5.8] - 2026-06-04
### Changed
- both: unify the session cookie into one shared name across the merged app ([#423](https://github.com/Jolls/arx-legacy/issues/423))

## [0.5.7] - 2026-06-04
### Changed
- both: collapse the two config.Config types and Load() functions into one shared arxlib/config.Config ([#419](https://github.com/Jolls/arx-legacy/issues/419))
- both: share a single DB connection pool across the merged app instead of one pool per app ([#420](https://github.com/Jolls/arx-legacy/issues/420))
### Removed
- both: retire per-app duplicates — single RELEASE_NOTES embed and one ExpectedSchemaVersion/CheckSchemaVersion ([#424](https://github.com/Jolls/arx-legacy/issues/424))

## [0.5.6] - 2026-06-04
### Changed
- both: merge Parts Master and Test Records onto a single port (4568); Test Records moves to /records, one unified settings page and config/local.json ([#413](https://github.com/Jolls/arx-legacy/issues/413))

## [0.5.5] - 2026-06-04
### Added
- both: expand the test suite with pure-function unit tests (path/SQL safety, form parsing, query-spec/token helpers) and no-DB httptest coverage of the RequireAuth and CSRF middleware ([#414](https://github.com/Jolls/arx-legacy/issues/414), [#240](https://github.com/Jolls/arx-legacy/issues/240))

## [0.5.4] - 2026-06-04
### Changed
- both: TEST_MODE now swaps the connection to a separate ArxDev database instead of _Test table-name suffixes; cfg.*Table() helpers return bare names ([#241](https://github.com/Jolls/arx-legacy/issues/241))

## [0.5.3] - 2026-06-03
### Removed
- parts_master_go: remove unused gopkg.in/yaml.v3 dependency, collapse the vestigial config.Settings wrapper into Config, and fix the stale settings.yml reference in the Settings UI ([#321](https://github.com/Jolls/arx-legacy/issues/321))

## [0.5.2] - 2026-06-03
### Changed
- arxlib: document LOCAL: FILFileName invariant in conventions.md; fix helper-function location reference ([#375](https://github.com/Jolls/arx-legacy/issues/375))
- both: extract arxlib/config.Base to de-duplicate BuildDSN, DSN, ConnectionSummary, Pick, and GetEnv across the two apps; no behavior change ([#372](https://github.com/Jolls/arx-legacy/issues/372))
### Removed
- test_records_go: drop pf_formula from test_definition and test_definition_history; add migration script ([#385](https://github.com/Jolls/arx-legacy/issues/385))

## [0.5.1] - 2026-06-03
### Changed
- parts_master_go: appConfigGet returns an error so callers can tell a missing key from a DB failure ([#371](https://github.com/Jolls/arx-legacy/issues/371))
- both: read cached schema-version check instead of re-querying app_config on every page load ([#370](https://github.com/Jolls/arx-legacy/issues/370))
- parts_master_go: txLogger now logs Commit/Rollback in debug mode ([#373](https://github.com/Jolls/arx-legacy/issues/373))
- test_records_go: view definition now shows raw spec_nom instead of expanding {id} tokens, so it matches the editor ([#405](https://github.com/Jolls/arx-legacy/issues/405))
- test_records_go: cap Comment column width to 240px so long step comments don't stretch the results table

## [0.5.0] - 2026-06-03
### Changed
- infra: merge Parts Master and Test Records into a single Arx.exe with one systray icon; both apps still serve on ports 4568/4569 ([#218](https://github.com/Jolls/arx-legacy/issues/218))

## [0.4.2] - 2026-06-03
### Security
- security: encode CSRF tokens as base64url instead of hex (same entropy, shorter token) ([#377](https://github.com/Jolls/arx-legacy/issues/377))
### Added
- arxlib: add BrowseFolderContext with exec.CommandContext so HTTP handlers can't hang on an open folder dialog ([#359](https://github.com/Jolls/arx-legacy/issues/359))
### Changed
- docs: relocate scattered code TODOs to tracked references in FUTURE_GOALS.md and issue numbers ([#368](https://github.com/Jolls/arx-legacy/issues/368))
- docs: relocate stale SQL DDL TODOs to #213 references in FUTURE_GOALS.md ([#376](https://github.com/Jolls/arx-legacy/issues/376))
- docs: reconcile FUTURE_GOALS.md — strike through completed Test Records items; fix two doc-drift references ([#384](https://github.com/Jolls/arx-legacy/issues/384))
- build: align go.mod toolchain with workspace go 1.26.3 ([#382](https://github.com/Jolls/arx-legacy/issues/382))
- build: stop running arxlib tests twice per full build; add root test.bat ([#380](https://github.com/Jolls/arx-legacy/issues/380))
- build: rewrite start.ps1 to launch the Go apps ([#349](https://github.com/Jolls/arx-legacy/issues/349))
- test_records_go: audit and document hide_formula nil-context call paths; nil is intentional for form-def view ([#360](https://github.com/Jolls/arx-legacy/issues/360))
- both: poll the port instead of a fixed 600ms sleep before opening the browser ([#364](https://github.com/Jolls/arx-legacy/issues/364))
- parts_master_go: local config ACL confirmed owner-only by inheritance — no code change needed ([#381](https://github.com/Jolls/arx-legacy/issues/381))
- parts_master_go: render() type guard confirmed present in both apps — no fix needed ([#366](https://github.com/Jolls/arx-legacy/issues/366))
### Fixed
- parts_master_go: nullableInt now parses to int instead of returning a raw string (caller panicked on type assertion) ([#355](https://github.com/Jolls/arx-legacy/issues/355))
- parts_master_go: raise test-mode PO sequence seed floor from 0 to 99999 so all-non-numeric snapshots can't collide ([#378](https://github.com/Jolls/arx-legacy/issues/378))
- parts_master_go: avoid double-rollback log noise on committed transactions (BOM update, PO create, PO update) ([#367](https://github.com/Jolls/arx-legacy/issues/367))
- both: RFC 6266-encode the Content-Disposition filename with mime.FormatMediaType ([#363](https://github.com/Jolls/arx-legacy/issues/363))
- parts_master_go: guard openDebugConsole against repeated allocation ([#379](https://github.com/Jolls/arx-legacy/issues/379))

## [0.4.1] - 2026-06-02
### Security
- security: CSRF check moved to middleware covering all POST routes in both apps; fixes two previously unprotected PO handlers ([#362](https://github.com/Jolls/arx-legacy/issues/362), [#350](https://github.com/Jolls/arx-legacy/issues/350))
- security: `crypto/rand.Read` error in CSRF token generation now panics instead of silently using a zeroed token ([#351](https://github.com/Jolls/arx-legacy/issues/351))
- security: hardcoded `"change-me-in-production"` session secret replaced with auto-generated random secret persisted to `local.json` ([#352](https://github.com/Jolls/arx-legacy/issues/352))
### Added
- test_records_go: add `{record.datetime}` formula token (MM/DD/YYYY H:MM AM/PM)
### Changed
- infra: `config/local.json` writes are now atomic (tmp → fsync → rename) to prevent zero-byte corruption on crash ([#356](https://github.com/Jolls/arx-legacy/issues/356))
- db: `h.db` access guarded with `sync.RWMutex`; old connection pool closed after reconnect from Settings ([#357](https://github.com/Jolls/arx-legacy/issues/357))
- db: connection pool limits added after `Ping()` — max 25 open, 5 idle, 30 min lifetime ([#353](https://github.com/Jolls/arx-legacy/issues/353))
- test_records_go: Test Date field now stores and displays date + time; inputs use datetime-local picker
### Removed
- db: drop legacy `trg_Tests_history` trigger that silently rolled back all `test_definition` UPDATEs; document in schema.md and TestRecords.sql
### Fixed
- db: silent `execContext` and `Scan` failures across both apps now log with context instead of discarding errors ([#354](https://github.com/Jolls/arx-legacy/issues/354))
- infra: `log.Fatal` in HTTP server goroutine replaced with `log.Print` + `systray.Quit()` so `onExit`/`CloseDB` runs on bind errors ([#365](https://github.com/Jolls/arx-legacy/issues/365))
- test_records_go: fix `{record.date}` formula overwriting stored result as decimal on edit ([#391](https://github.com/Jolls/arx-legacy/issues/391))

## [0.4.0] - 2026-05-26
### Changed
- release: v0.4.0 milestone — conditional step visibility, result format display, units of measure, vendor rename, manufacturer links, Windows login auto-fill

## [0.3.53] - 2026-05-26
### Added
- test_records_go: hide_formula — support `{token}=value` and `{token}!=value` expressions for dynamic per-record step visibility ([#210](https://github.com/Jolls/arx-legacy/issues/210))

## [0.3.52] - 2026-05-26
### Removed
- test_records_go: deprecate pf_formula — remove from all step queries and struct; pass/fail uses pf_type + ComputePassFail ([#199](https://github.com/Jolls/arx-legacy/issues/199))

## [0.3.51] - 2026-05-26
### Changed
- parts_master_go: rename "Suppliers" section to "Vendors" throughout UI; add Roles row to vendor detail page ([#336](https://github.com/Jolls/arx-legacy/issues/336))
### Fixed
- parts_master_go: fix "Supplier is active" label and manufacturer checkbox formatting ([#336](https://github.com/Jolls/arx-legacy/issues/336))

## [0.3.50] - 2026-05-26
### Changed
- parts_master_go: manufacturer name on Mfg Parts tab is now a clickable link to the supplier detail page ([#338](https://github.com/Jolls/arx-legacy/issues/338))

## [0.3.49] - 2026-05-26
### Added
- test_records_go: apply `format` field to result display in show/print views; add format placeholder to edit inputs; expose format column in form def editor ([#209](https://github.com/Jolls/arx-legacy/issues/209))

## [0.3.48] - 2026-05-26
### Added
- parts_master_go: add units of measure — `unit` reference table, base unit on parts (`PNUNID`), purchase unit on sourcing (`supplier_part.unit_id`); supplier parts list shows effective unit with base-unit fallback ([#314](https://github.com/Jolls/arx-legacy/issues/314))

## [0.3.47] - 2026-05-25
### Changed
- test_records_go: use `os/user.Current()` instead of `os.Getenv("USERNAME")` for audit username in lock/unlock handlers ([#330](https://github.com/Jolls/arx-legacy/issues/330))
- parts_master_go: pre-populate PO orderer field with Windows login name on new PO ([#330](https://github.com/Jolls/arx-legacy/issues/330))
- parts_master_go: pre-populate Requested By field with Windows login name on new part ([#330](https://github.com/Jolls/arx-legacy/issues/330))

## [0.3.46] - 2026-05-25
### Added
- parts_master_go: add manufacturer part number (MPN) management on part detail page; flag companies as manufacturers; soft-delete support ([#303](https://github.com/Jolls/arx-legacy/issues/303))

## [0.3.45] - 2026-05-25
### Added
- test_records_go: add New Form action on forms index — creates blank form (or optionally copies steps from an existing form) for any unassigned FORM-category PN ([#319](https://github.com/Jolls/arx-legacy/issues/319))
- test_records_go: add Duplicate Form action on form definition page — copies all steps, record_types, and instrument_types to a new form with chosen PN ([#255](https://github.com/Jolls/arx-legacy/issues/255))
- test_records_go: add record_types and instrument_types fields to form definition edit page

## [0.3.44] - 2026-05-25
### Added
- test_records_go: implement Form lock/unlock with audit trail written to `form_events`; require comment on unlock ([#200](https://github.com/Jolls/arx-legacy/issues/200))

## [0.3.43] - 2026-05-25
### Removed
- parts_master_go: drop unused `is_active` column from `supplier_part`; remove Active column from supplier linked-parts view

## [0.3.42] - 2026-05-25
### Added
- parts_master_go: add `status` enum to PO (`pending/placed/complete/cancelled/on_hold`); replace `is_active` checkbox with status dropdown; `is_active` kept in sync as convenience bit ([#300](https://github.com/Jolls/arx-legacy/issues/300))

## [0.3.41] - 2026-05-25
### Changed
- schema: replace `TestRecordHistory` with `form_events` + `record_events`; migrate 90 rows; real FK constraints on both tables ([#298](https://github.com/Jolls/arx-legacy/issues/298))
### Added
- test_records_go: lock/unlock UI for test records with required comment on unlock ([#201](https://github.com/Jolls/arx-legacy/issues/201))
- test_records_go: write lock/unlock audit events to `record_events` using Windows login ([#200](https://github.com/Jolls/arx-legacy/issues/200))

## [0.3.40] - 2026-05-25
### Changed
- schema: migrate `FIL.FILPNID` from `VARCHAR` to `INT NOT NULL` with enforced FK to `PN.PNID`; fix `FIL_Test` missing `is_active` DEFAULT in `_test.sql`; correct stale column names in schema docs ([#297](https://github.com/Jolls/arx-legacy/issues/297))

## [0.3.39] - 2026-05-24
### Changed
- parts_master_go: rename `FIL.FILNotes` → `FIL.category`; attachment categories now stored in `app_config` and editable via Settings; drop dead `settings.yml` / yaml approach ([#313](https://github.com/Jolls/arx-legacy/issues/313))

## [0.3.38] - 2026-05-24
### Removed
- test_records_go: drop `TestResults.form_id` — write-only denormalized column, never read by the app ([#299](https://github.com/Jolls/arx-legacy/issues/299))

## [0.3.37] - 2026-05-24
### Added
- test_records_go: add `instrument_type` field to `TestRecords` and rename `applicable_instrs` → `instrument_types` on `test_definition`; record show/edit/print views now filter steps by instrument type — steps with `instrument_types` set are hidden when the record's `instrument_type` doesn't match ([#203](https://github.com/Jolls/arx-legacy/issues/203))
- test_records_go: add `instrument_types` to `Forms`; record create/edit show a dropdown for Instrument Type when the form has types configured, free-text otherwise ([#203](https://github.com/Jolls/arx-legacy/issues/203))

## [0.3.36] - 2026-05-24
### Changed
- docs: refactor CLAUDE.md — move per-table schema reference into `SQL/schema.md`, attachment/PO folder conventions into new `docs/conventions.md`; CLAUDE.md now links to reference docs rather than embedding them ([#315](https://github.com/Jolls/arx-legacy/issues/315))

## [0.3.35] - 2026-05-24
### Changed
- parts_master_go: rename `LNK` → `supplier_part` with modernized column names (`LNKID`→`id`, `LNKSUID`→`supplier_id`, `LNKPNID`→`part_id`, `LNKVendorPN`→`supplier_pn`, `LNKVendorDesc`→`supplier_desc`, `LNKLeadtime`→`lead_time`, `LNKChoice`→`preference`, `LNKUse`→`is_active`, etc.); drop obsolete columns (`LNKMFRID`, `LNKMFRPNID`, `LNKUNID`, `LNKToPNID`, `LNKAtQty`, `LNKCurrentCost`, `LNKRFQDate`); add `mfg_part` table for manufacturer part records; add `is_supplier`/`is_manufacturer` role flags to `supplier`; update config helpers, models, handlers, templates, and schema docs; add test + prod migration scripts with backup and validation ([#225](https://github.com/Jolls/arx-legacy/issues/225))
- parts_master_go: rename `supplier` → `company` and `supplier_attachment` → `company_attachment`; manufacturers and distributors share one table distinguished by role flags; update all FK constraint names, config helpers (`CompanyTable`, `CompanyAttachmentsTable`), handlers, `_test.sql`, `triggers.sql`, and schema docs; fix deferred trigger bodies that still referenced `dbo.LNK`/`LNKSUID`/`dbo.supplier` ([#225](https://github.com/Jolls/arx-legacy/issues/225))

## [0.3.34] - 2026-05-23
### Added
- parts_master_go: add price CRUD — create, edit (deactivates old row + inserts new), deactivate, activate; add `effective_date` column for price history; replace unique constraint with filtered index (active rows only) so inactive rows serve as history ([#310](https://github.com/Jolls/arx-legacy/issues/310))

## [0.3.33] - 2026-05-22
### Changed
- both apps: rename `Tests` table → `test_definition` (and `Tests_Test` → `test_definition_Test`); update `StepsTable()` config helper, DDL, schema diagram, CLAUDE.md ([#215](https://github.com/Jolls/arx-legacy/issues/215))

## [0.3.32] - 2026-05-22
### Changed
- parts_master_go: rename `PNType` → `category` (CHECK-constrained: ASM/BUY/DWG/DOC/FORM/MFG/RAW/SVC/TOOL); add `has_bom` BIT column as explicit BOM capability driver; rename 5 `PN` columns to snake_case (`PNPartNumber`→`part_number`, `PNTitle`→`title`, `PNDetail`→`detail`, `PNStatus`→`status`, `PNActive`→`active`); update all queries, models, templates, and `named_queries` data (schema version 2)

## [0.3.31] - 2026-05-22
### Added
- both apps: embed user-facing release notes in binary; `/whats-new` route; "new version" banner in layout; release notes replace changelog on settings page ([#295](https://github.com/Jolls/arx-legacy/issues/295))

## [0.3.30] - 2026-05-22
### Added
- test_records_go: add image gallery to record detail and print views ([#208](https://github.com/Jolls/arx-legacy/issues/208))

## [0.3.29] - 2026-05-22
### Added
- test_records_go: add print/PDF export view for test records ([#204](https://github.com/Jolls/arx-legacy/issues/204))

## [0.3.28] - 2026-05-22
### Added
- parts_master_go: capture part revision at time of order — `POL.POLRev VARCHAR(10) NULL` wired up on INSERT and UPDATE; server-side fallback looks up `PN.revision` when POLPNID is set and form field is blank
- parts_master_go: `APIPartSearch` now returns `revision` field; PO edit autocomplete auto-fills Rev on part selection
- parts_master_go: Rev column added to PO detail, edit form, and print views
- SQL: add `POLRev VARCHAR(10) NULL` to `po_items.sql` DDL; migration: `ALTER TABLE dbo.POL ADD POLRev VARCHAR(10) NULL`
### Changed
- parts_master_go: PO print page auto-names PDF to `<PO Number> <SupplierCode>` via `<title>` tag
- parts_master_go: print button opens PO folder in Explorer after marking printed (`POST /po/{id}/open-folder`), creating folder if it doesn't exist
- parts_master_go: suppress browser URL/date headers from PO print output via `@page { margin: 0 }`
### Fixed
- parts_master_go: fix PO creation failure on tables with triggers — replace `OUTPUT INSERTED.ID` with combined INSERT + `SCOPE_IDENTITY()` batch
- closes #220

## [0.3.27] - 2026-05-21
### Added
- parts_master_go: add BOM rollup cost button on BOM tab — `POST /part/{id}/rollup-cost` computes `SUM(PNCurrentCost * PLQty)` for direct components and writes to `PN.PNLastRollupCost` + new `PN.PNLastRollupAt`
- parts_master_go: add Unit Cost column to BOM table (PNCurrentCost per component)
- SQL: add `PN.PNLastRollupAt DATETIME NULL` column (NULL = never run, paired with existing `PNLastRollupCost`)

## [0.3.26] - 2026-05-21
### Added
- SQL: add NOT NULL + FK constraints to LNK, PL, POL, PO, price, supplier, CN, Forms, Tests, TestRecords, TestResults; migration scripts in `SQL/migrate_add_constraints_parts.sql` and `SQL/migrate_add_constraints_tests.sql`
- SQL: add missing DEFAULT constraints for `supplier.is_active`, `supplier.SUNumOfLNKs`, `supplier.SUNumOfPOs`, `price.pack_size`
- Both apps: add cross-app navigation link in header (Parts Master ↔ Test Records); configurable via `PM_URL` / `TR_URL` env vars, defaulting to localhost ports
### Changed
- SQL: rename all auto-generated constraint names to explicit `DF_table_column` / `UQ_table_column` conventions; rename script in `SQL/rename_constraints.sql`
- SQL: update all DDL reference files with explicit CONSTRAINT names, NOT NULL, and FK declarations; remove resolved TODO comments
### Fixed
- parts_master_go: fix nil panic on new supplier form — `{{if not .Contacts}}` instead of `{{if eq (len .Contacts) 0}}`
- test_records_go: replace `serial_number + 0` sort trick with `TRY_CAST(serial_number AS INT)` in RecordsList and RecordDetail prev/next queries

## [0.3.25] - 2026-05-21
### Added
- Add `arxlib/urlutil/urlutil_test.go` — first unit test suite; covers all 9 urlutil functions
### Changed
- SQL: migrate `PN.PNLastRollupCost` from `VARCHAR(255)` to `DECIMAL(16,8) NULL`; NULL = no rollup ever run
### Fixed
- SQL: add `NOT NULL DEFAULT 0` to `PL.PLItem` and `PL.PLQty`; fixes NULL scan error on where-used page
- Fix `SafePathSegments` to correctly drop `..` traversal segments (was passing through via `filepath.Base`)

## [0.3.24] - 2026-05-20
### Added
- Both apps: open a Windows console window on startup when `DEBUG_MODE=true` (`console_windows.go`); SQL query logging is now visible without running from a terminal
- Both apps: add `app_config` table (key/value store for DB-side metadata); seed `schema_version = '1'`; add `app_config_Test` variant and update `_test.sql`
- Both apps: add `CheckSchemaVersion` — checks `app_config.schema_version` against `config.ExpectedSchemaVersion` on startup, on settings save, and on Parts/Forms/Records page load; shows a persistent banner if there is a mismatch

## [0.3.23] - 2026-05-20
### Added
- `parts_master_go`: add `PO.date_printed` to model, `fetchPO` SELECT/scan, update handler, detail status bar, and edit form; set automatically via `POST /po/{id}/mark-printed` when print button is clicked
### Changed
- Both apps: link `DEBUG_MODE` to SQL query logging — all `QueryContext`/`QueryRowContext`/`ExecContext`/transaction calls log to terminal when debug mode is on; `parts_master_go` adds wrapper methods; `test_records_go` unifies on `DebugMode` (removes separate `SQLDebug` field)
### Removed
- Schema audit: drop dead columns `supplier.SUWeb`, `supplier.SUContact1`, `Forms.custom_sheets`, `POL.POLRev`, `price.price_type` from DDL; update comments on remaining flagged columns confirming status

## [0.3.22] - 2026-05-19
### Added
- `test_records_go`: prev/next navigation arrows on record view — steps through records in the same form by the same ordering as the records list (serial number descending)
- `test_records_go`: debug mode setting — toggleable in Settings; when on, shows raw step fields panel on record view; persisted in `config/local.tr.json`
### Changed
- Both apps: test mode now toggleable in Settings UI under Developer; persisted in `local.json` which overrides `.env`; confirmation popup warns to close other tabs before switching
- Both apps: remove test mode port switching — each app runs on one port regardless of mode; test mode now only switches table name variants

## [0.3.21] - 2026-05-19
### Removed
- Remove `report_text` field — never used in VBA or Go UI; removed from `Tests` DDL, `test_definition_history` DDL and trigger, all Go handlers, models, templates, and migration script; DB column drop (`ALTER TABLE Tests DROP COLUMN report_text`) to be run separately

## [0.3.20] - 2026-05-19
### Changed
- DB migration: VBA `ArchiveTest` rows moved from `Tests` into `test_definition_history`; all definition change history now consolidated in one table and surfaced in the form definition timeline UI
- `SQL/migrate_vba_archive_to_history.sql`: migration script with pre-flight checks, `_Test` and prod steps, backup tables, and cleanup placeholders (section 4 deferred — archive rows and column drops pending)
### Removed
- `SQL/_test.sql`: remove conditional guard on `test_definition_history_Test` recreate — table is now permanent
- `test_records_go`: remove Calc P/F column from read-only record view — stored `pass_fail` is the source of truth; live spec comparison deferred to a future feature
- `TestRecord` xlsm: remove `Revision` column from `tbl_NewTests` test modification sheet

## [0.3.19] - 2026-05-18
### Removed
- `TestRecord` VBA: remove `ArchiveTest` mechanism — test definition history is now handled entirely by the `trg_Tests_history` DB trigger writing to `test_definition_history`; removes `ArchiveTest` sub, `Call ArchiveTest` call site, revision increment logic, and `Revision` column from the `Tests` SELECT query
### Changed
- Both apps: `PM_PORT`/`TR_PORT` and `PM_TEST_PORT`/`TR_TEST_PORT` env vars allow all four ports to be configured in a single shared `.env` file; falls back to `PORT`/`TEST_PORT` for backwards compatibility
- Both apps: app-specific local config files (`config/local.pm.json`, `config/local.tr.json`) prevent each app from clobbering the other's settings when run from the same folder
- `test_records_go`: `max_subbatch_result` named query refined — joins `TestRecords` to filter by `record_date <= @record_date`, preventing later batches from inflating the max when editing historical records

## [0.3.18] - 2026-05-18
### Added
- `parts_master_go`: Add BOM editing — GET `/part/{id}/bom/edit` renders an editable table; POST `/part/{id}/bom` saves changes (add rows, update item#/qty, delete rows) in one transaction; part number typeahead reused from PO line items; "Edit BOM" button added to the read-only BOM view
- `parts_master_go`: `TEST_PORT` env var allows prod and test mode to run simultaneously on different ports
- `test_records_go`: Form definition editor — drag-and-drop row reordering (SortableJS), add new test rows, hide checkbox on heading rows, hide applies to all row types
- `test_records_go`: Named queries — live re-resolution of `{id}` cross-step tokens on the edit page without a save/reload cycle; auto-fill single-result queries trigger P/F immediately; open-link icon for LOCAL: file results; `{form.id}`, `{form.pnid}`, `{form.pn}` tokens added; named query errors now surface in the UI instead of silently leaving the field stuck
- `test_records_go`: Add `pn_primary_attachment` and `form_primary_attachment` named queries using `PN.PNFILIDPrimary` for primary attachment lookup
- `test_records_go`: Add named queries — `recent_serial_numbers_for_form` (last 20 SNs for a form by date), `max_subbatch_result` (max integer result for a test step capped at record date), `bom_pn_by_item` changed to `list`
- `test_records_go`: `result_type = 'multi'` on named queries renders a checkbox list; selected values stored comma-delimited; includes "— Other —" for custom entries
- `test_records_go`: Formula evaluation in `default_result` — expressions like `{113} + {116}` auto-compute live on the edit page; computed inputs are readonly and highlighted blue
- `test_records_go`: `pf_type = 'comment'` always passes regardless of value
- `test_records_go`: `spec_nom` syntax lint in form definition editor — flags missing `@` on named query params on blur
- `test_records_go`: `TEST_PORT` env var allows prod and test mode to run simultaneously on different ports
### Changed
- `test_records_go`: Definition history timeline collapses to one dot per calendar day

## [0.3.17] - 2026-05-16
### Added
- Both apps now display proper branded icons in the system tray and browser tab
- `parts_master_go`: Arx single-gear icon (Arx-32.png embedded as systray ICO and favicon)
- `test_records_go`: Arx Assemblies double-gear icon (Arx-Assemblies-32.png embedded as systray ICO and favicon)
- `icons/` folder added to repo with SVG sources and PNG rasters at 16/32/64/128/256/512/1024px

## [0.3.16] - 2026-05-16
### Added
- Add `trg_FIL_part_count` trigger on `FIL` to maintain `PN.PNFILLinks` (active rows only)
- `SQL/_test.sql`: add `trg_FIL_Test_part_count`, combined FIL+POL recalibration
### Changed
- `PartsMaster/Part.cls`: convert FIL hard-delete to soft-delete; add `is_active=1` filter to `BuildFileLinkCollection` and `NumFileLinksViaSQL`; remove `UpdateNumFileLinks` (trigger owns count)
- `FIL.FILPNID` migrated from VARCHAR(255) to INT NOT NULL with FK constraint to PN.PNID
- Document all 4 triggers (+ 4 test variants) in `SQL/SCHEMA.md`, `SQL/FIL.sql`, `SQL/part_number.sql`, `CLAUDE.md`

## [0.3.15] - 2026-05-16
### Added
- Add `trg_FIL_part_count` trigger on `FIL` to maintain `PN.PNFILLinks` (active rows only, handles VARCHAR→INT FILPNID via TRY_CAST)
- `SQL/_test.sql`: add `trg_FIL_Test_part_count`, combine FIL+POL recalibration into one update
### Changed
- `PartsMaster/Part.cls`: convert FIL hard-delete to soft-delete (`UPDATE SET is_active=0`); add `is_active=1` filter to `BuildFileLinkCollection` and `NumFileLinksViaSQL`; remove `UpdateNumFileLinks` (trigger owns the count)
- Document in `SQL/SCHEMA.md`, `SQL/FIL.sql`, `SQL/part_number.sql`, and `CLAUDE.md`

## [0.3.14] - 2026-05-16
### Added
- Add `trg_POL_part_count` trigger on `POL` to maintain `PN.PNPOLinks` after any INSERT/UPDATE/DELETE
- `SQL/_test.sql`: add `trg_POL_Test_part_count` on `POL_Test` and recalibrate `PN_Test.PNPOLinks` in snapshot
### Changed
- Document in `SQL/SCHEMA.md`, `SQL/po_items.sql`, `SQL/part_number.sql`, and `CLAUDE.md`

## [0.3.13] - 2026-05-16
### Changed
- Document DB triggers in `SQL/SCHEMA.md` (new Triggers section), `SQL/LNK.sql`, `SQL/PO.sql`, and `CLAUDE.md`

## [0.3.12] - 2026-05-16
### Changed
- `POCreate` and `POUpdate` handlers wrapped in `db.BeginTx` transactions — PO header, line items, and total update now commit atomically or roll back together
- `POUpdate`: resolve PO ID once before new-line loop instead of per-row
### Fixed
- All DB calls in both handlers now check and surface errors (previously silently discarded)

## [0.3.11] - 2026-05-16
### Added
- Add `SQL/triggers.sql`: DB triggers `trg_LNK_supplier_count` and `trg_PO_supplier_count` keep `supplier.SUNumOfLNKs` / `SUNumOfPOs` accurate after any LNK or PO write, from any client
- `SQL/_test.sql`: recreate equivalent triggers on `_Test` tables after snapshot; recalibrate copied counts
### Changed
- `SQL/supplier.sql`: update comment — counts are now maintained by triggers, not the application
### Removed
- `PartsMaster/LNKs.bas`: remove `UpdateSUNumOfLNKs` (was querying `LNK_Test` against prod, overwriting the correct trigger value with a stale count)

## [0.3.10] - 2026-05-16
### Added
- New `arxlib/` shared Go module: `db` (Connect), `urlutil` (IsHTTPURL, IsLocalFile, LocalFileURL, FileIcon, SafePathSegments, etc.), `folderpick` (PowerShell folder picker)
- Go workspace (`go.work`) links arxlib, parts_master_go, test_records_go
### Changed
- Both apps now delegate duplicate helpers to arxlib; local db/db.go are thin wrappers

## [0.3.9] - 2026-05-16
### Added
- New `test_records_go/` Go app: port of `test_records/` Ruby/Sinatra app to Go
- Same chi/systray/go:embed/go-mssqldb stack as `parts_master_go`; port 4569
- Routes: `/` (forms list), `/forms/{id}/records` (WIP filter), `/records/{id}` (detail with hierarchical test results, p/f badges, image hover preview, LOCAL: file links)
- File serving: `/local/*` (DOC_CONTROL_ROOT), `/images/*` (IMAGE_ROOT, auto-.PNG)
- Settings page with DB connection, IMAGE_ROOT, DOC_CONTROL_ROOT, browse-folder picker
- Test-mode table variants: Forms/Forms_Test, TestRecords/TestRecords_Test, Tests/Tests_Test, TestResults/TestResults_Test
- Updated CLAUDE.md: both Go apps documented, test-mode table expanded

## [0.3.8] - 2026-05-15
### Added
- Supplier edit form: live contact details panel (phone, email, city) below default contact dropdown, updates on selection change
- Supplier detail view: dedicated "Default Contact Information" section showing name (linked), phone, email, city joined from CN
### Changed
- SUCity removed from Go model, queries, forms, and templates; supplier search API now joins CN for city
- Added TODO notes on SUWeb and SUContact1 for future removal (superseded by attachments and CN respectively)
### Removed
- Dropped unused supplier columns: SUFollowup, SUCode, SUCurDedExRate, SUCurExRate, SUCURID, SUCurReverse, SUNoPhonePrefix, SUContact2, SUDateLast, SUAccount, SUTerms, SUFedTaxID, SUStateTaxID, SUEMail2, SUZipcode, SUState, SUCity (confirmed absent from Go app and VBA codebase)

## [0.3.7] - 2026-05-15
### Added
- Added DEFAULT values to LNK.LNKUse, LNKChoice, LNKCurrentCost; PO.is_active; FIL.order_id
- Added new columns: supplier.SUZipcode/SUState, POL.POLRev, price.price_type
- Added schema_diagram.md ER diagram (new file)
- PO.sql: added CREATE SEQUENCE DDL for PO_Number_Seq
### Changed
- SQL schema cleanup: VARCHAR expansions (FIL, CN, PO, POL), DATE→DATETIME on date_modified/CNDateModified/PNDateModified, DEFAULT GETDATE() on date columns
- Diagram updated: supplier_attachment entity and relationships added
- TODO annotations: unsafe changes flagged with verification queries; unused columns flagged (supplier currency fields, SUFollowup, SUCode, SUNoPhonePrefix, PO.date_printed)

## [0.3.6] - 2026-05-15
### Added
- Part attachments: soft-delete (is_active flag); Delete button with confirm on attachments tab
- Supplier attachments: default attachment (primary_attachment_id on supplier table); star/Set UI matches parts
### Changed
- Shared helpers: softDeleteAttachment + setPrimaryAttachment in handlers/attachments.go used by both
### Fixed
- Part attachments: fixed PartSetPrimaryAttachment to use proper int parameters
- Fixed `not` template function to accept any type instead of requiring bool (was crashing notes_select)

## [0.3.5] - 2026-05-15
### Added
- Suppliers: new Attachments subtab for files and URLs (SUFIL / SUFIL_Test tables)
- Settings: SUPPLIER_FILES_ROOT path field with Browse button (falls back to DOC_CONTROL_ROOT if blank)
- File serving: /supplier-local/* and /supplier-local-dir/* routes for supplier file access

## [0.3.4.2] - 2026-05-14
### Added
- CSS: .form-input.input-invalid style for red border on invalid typeahead fields
### Changed
- PO edit: supplier and receiver typeahead now validates on blur and blocks save if no valid selection made
- PO new/edit: supplier and receiver are required fields; empty submission blocked with error banner
- Settings: version displayed as "Arx Parts Master vX.Y.Z"
- AppVersion constant in config.go drives both settings display and static asset cache-busting URL
### Fixed
- PO new: fixed broken JS caused by {{len .POItems}} on nil — all JavaScript now initializes correctly

## [0.3.4] - 2026-05-14
### Removed
- Removed parts_master_web (Ruby/Sinatra app) — superseded by parts_master_go

## [0.3.3] - 2026-05-14
### Added
- Cache-busting ?v= query string on static assets to force browser refresh after rebuild
### Changed
- Settings: Default Receiver dropdown now shows suppliers (not contacts) since receiver_id is a supplier ID
- Settings: PO default contact/receiver dropdowns now inside form so they save correctly
- Filter performance: replaceChildren replaces show/hide loop — INP drops from 2000ms to 40ms
- Filter: cell text cached at page load (initRows) — no DOM reads per keystroke
- CSS: contain:layout style on .table-wrapper isolates table repaints from page gradient/shadow
### Fixed
- Fixed supplier edit template error (not on []ContactSummary — use len check instead)

## [0.3.2] - 2026-05-14
### Changed
- templates/ and static/ embedded into binary via go:embed — distribute exe only, no supporting folders required
- config/local.json and .env still read from the exe's working directory at runtime

## [0.3.1] - 2026-05-14
### Added
- Settings page: DB connection fields (server, name, user, password) and file path overrides editable in UI
### Changed
- DB password removed from .env — stored only in gitignored config/local.json; app prompts for it on first run
- All app routes redirect to /settings when no database connection is active (RequireAuth middleware)
- Auto-reconnects on startup if local.json contains a saved password
- Backward compat: existing DATABASE_DSN in .env is parsed to extract server/db/user (password still required via UI)

## [0.3.0] - 2026-05-14
### Added
- Added parts_master_go: full Go port of parts_master_web
- Standalone Windows .exe — no Ruby/gems required
- System tray icon with Open/Quit; auto-opens browser on launch
- All read views: parts, suppliers, contacts, purchase orders, BOM, where-used, attachments, pricing, order history
- All edit/create/delete forms: parts, suppliers, contacts, purchase orders with line items
- PO sequence number, folder creation, duplicate, print, note, folder browser
- Local file and directory serving with path-traversal protection
- Supplier/part/contact search API endpoints for PO edit autocomplete
- CSS custom properties in static/app.css for easy retheme; JS in static/app.js

## [0.2.61] - 2026-05-08
### Changed
- Added changelog update instructions to CLAUDE.md

## [0.2.60] - 2026-05-08
### Changed
- PO folder name appends -testmode suffix when running in test mode

## [0.2.59] - 2026-05-08
### Added
- New PO creation auto-creates folder in PO_FOLDER_ROOT named "<number> <SUSupplierCode>"
### Changed
- PO folder tab replaces Open Folder button; folder listing shows PO header and sub-tabs

## [0.2.58] - 2026-05-08
### Added
- Added PO folder subtab: GET /po/:id/folder serves directory listing from PO_FOLDER_ROOT
### Changed
- Folder lookup matches any directory starting with the PO number (e.g. "4309 Acme")

## [0.2.57] - 2026-05-08
### Added
- Duplicate PO: added "Duplicate PO" button on PO detail/edit view; pre-fills new PO form with supplier, ship-to, line items, and costs from the source PO

## [0.2.56] - 2026-05-08
### Added
- Attachment CRUD: add, edit, and set-primary attachment actions now work on the part attachments tab

## [0.2.55] - 2026-05-08
### Added
- Folder attachments: LOCAL:path\to\folder\ syntax opens a server-side directory listing with file links and a Copy Path button

## [0.2.54] - 2026-05-08
### Fixed
- Fixed assign_po_fields writing to non-existent columns; supplier/receiver detail fields (address, email, phone, etc.) now save correctly when creating or editing a PO

## [0.2.53] - 2026-05-08
### Added
- Settings view accessible via gear icon in app header
- Changelog rendered from CHANGELOG.md in settings view

## [0.2.52] - 2026-05-08
### Changed
- PO supplier schema improved

## [0.2.51] - 2026-05-08
### Added
- Added PO_Test number sequence to remove race condition

## [0.2.50] - 2024-07-18
### Fixed
- Updating PO's now refresh PO List table

## [0.2.48] - 2023-08-10
### Changed
- Updated Qty on Parts List to 5 decimal places

## [0.2.48] - 2023-07-21
### Added
- Added Internal Notes to PO_Test Form

## [0.2.47] - 2023-07-21
### Changed
- Creating new Contact doesn't prompt for "SUID" anymore, prompts for "Supplier"

## [0.2.46] - 2023-07-21
### Fixed
- Fixed "Update Source Costs" where it would only go to a few decimals

## [0.2.45] - 2023-05-25
### Fixed
- Fixed re/deactivate on PN and CN tables

## [0.2.44] - 2023-02-07
### Fixed
- Fixed bug on double-click date in PO (vartypecheck error)

## [0.2.43] - 2023-02-03
### Changed
- Reduced min PO items from 5 to 1

## [0.2.42] - 2023-02-03
### Changed
- Refactored Link creation and viewing

## [0.2.41] - 2023-02-02
### Fixed
- Fixed issue with new contact creation
### Added
- Added button to update associated supplier for contact

## [0.2.40] - 2023-02-02
### Fixed
- Fixed Part Info dbl-click on Supplier bug

## [0.2.39] - 2023-02-01
### Added
- Added Discount Qty for parts onto Parts List

## [0.2.38] - 2023-01-30
### Fixed
- Fixed bug with adding Part Link to preexisting file from Doc Control

## [0.2.37] - 2023-01-27
### Changed
- Add Release Notes now had default incremented version

## [0.2.36] - 2023-01-27
### Fixed
- Pricing List now handles "Cancel" or "X" on both popups

## [0.2.35] - 2023-01-26
### Added
- Expanding BOM Item now shows Ext Cost with Links

## [0.2.34] - 2023-01-25
### Changed
- minor cleanup and refactor

## [0.2.33] - 2023-01-19
### Added
- Doubleclick on File now opens location of file in FileExplorer

## [0.2.32] - 2023-01-19
### Changed
- Assy cost now updates upon updating. also upon refreshing

## [0.2.31] - 2023-01-19
### Fixed
- Fixed where export/backup to csv was only doing limited columns

## [0.2.30] - 2023-01-03
### Added
- Initial draft release of Quanity Pricing

## [0.2.25] - 2022-12-28
### Changed
- Purchase History refreshes every time you access it now

## [0.2.24] - 2022-12-28
### Fixed
- Fixed issue on double-clicking merged cells; Contact issue, PO Inactivate button
### Added
- ADDED Ext. Costs and FIL Links to Parts List

## [0.2.23] - 2022-11-03
### Changed
- Updated dbcharlimit to include more supplier stuff

## [0.2.22] - 2022-10-29
### Changed
- Updated PO Form to use Supplier/Contact objects properly

## [0.2.21] - 2022-10-22
### Changed
- Updated supplier stuff to use the supplier object more

## [0.2.20] - 2022-10-20
### Removed
- Removed QuerySUInfo in favor of using Supplier Object

## [0.2.19] - 2022-10-15
### Changed
- admin Columns now autohide based on userConfig

## [0.2.18] - 2022-10-15
### Fixed
- Fixed bug on PO creation not saving order date

## [0.2.17] - 2022-10-15
### Changed
- Made default contact more robust on Supplier sheet

## [0.2.16] - 2022-10-14
### Changed
- Improved sqlExecute logging

## [0.2.15] - 2022-10-14
### Added
- Added admin mode to release notes

## [0.2.14] - 2022-10-14
### Added
- Added Copy Link to Part Info. So now you can select a link, hit that button to copy it to clipboard for ease of opening in other applications (or attaching to emails)

## [0.2.13] - 2022-10-11
### Added
- sqlExecute updates and added release note functionality

## [0.2.12] - 2022-10-02
### Fixed
- Fixed minor release_notes bug

## [0.2.11] - 2022-10-02
### Changed
- Replaced Supplier Info with Default Contact info

## [0.2.10] - 2022-10-01
### Added
- Added prompt to link new Contact with Supplier

## [0.2.9] - 2022-09-30
### Added
- Solo and Bulk methods to add Part to Parts List

## [0.2.8] - 2022-09-29
### Added
- Added ability to replace part on BOM with new part

## [0.2.7] - 2022-09-23
### Added
- Added ability to add HTTP link to Part via clipboard

## [0.2.6] - 2022-09-17
### Changed
- Updated Logging table and columns to new schema

## [0.2.5] - 2022-09-17
### Fixed
- Fixed column name bug

## [0.2.4] - 2022-09-15
### Added
- added release notes to database and Show Release Notes button to UI/Settings

## [0.2.3] - 2022-09-15
### Fixed
- Fixed bug where you couldn't add a PO Line Item if Line Item had no assigned supplier in PartInfo

## [0.2.2] - 2022-09-15
### Fixed
- Fixed bug where when creating new Supplier it didn't filter onto the supplier

## [0.2.1] - 2022-09-15
### Changed
- When adding new File/Link it will auto-increment the Order

## [0.2.0] - 2022-09-15
### Added
- Initial test release
