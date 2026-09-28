# #220 — attachments to sqlc

Part of #220 / #190 (touches #221's company attachments too). Moves every `part_attachment` and
`company_attachment` statement in the attachment handlers into a new `internal/attachments` package, so
`arx_go/attachments.go` joins `convertedFiles`.

## Scope
- `arx_go/attachments.go`: all SQL (delete-if-unshared, ensure/set primary, vendor scope, vendor-scoped
  lists, where-used, duplicate hash).
- `arx_go/parts.go`: `renderPartAttachments`, `insertAttachmentRow`, `PartAttachmentUpdate`,
  `PartAttachmentDelete`, `PartSetPrimaryAttachment`, `APIPartLocalAttachments`.
- `arx_go/api.go`: `APIPartPasteAttachment`, `APIPartPasteAttachmentReplace`, `APIPartGenerateThumbnail`,
  `upsertGeneratedAttachment`, `saveGeneratedAttachment`.
- `arx_go/suppliers.go`: `renderSupplierAttachments`, `SupplierAttachmentCreate/Update/Delete`,
  `SupplierSetPrimaryAttachment`.
- Out of scope (stay raw): part/supplier detail and list joins on the attachment tables (`PartDetail`,
  `PartsRows`, `fetchPartBasic`, `SupplierDetail`), settings backup, `parts.CreateImportedAttachment`.
  `parts.go`, `api.go` and `suppliers.go` do not join `convertedFiles`. No `cfg.*Table()` helper loses its
  last caller.

## Changes
- `sqlc.yaml`: add `internal/attachments/attachments.sql` to `queries`.
- `internal/attachments/attachments.sql` (+ `sqlc generate`):
  - Part: `ListPartAttachments` (with `COALESCE(sc.name, mc.name, '')` vendor name), `GetPartAttachment`
    (file_name, category, part_revision, is_active), `CreatePartAttachment`, `UpdatePartAttachment`,
    `UpdatePartAttachmentFile`, `ReplacePartAttachmentPhoto`, `SoftDeletePartAttachment`,
    `PartFileInUse`, `EnsurePartPrimary` (generated categories as params), `SetPartPrimary`,
    `FindDuplicatePartAttachment`, `SupplierPartOfPart`, `MfgPartOfPart`, `GetGeneratedAttachment`,
    `CreateGeneratedAttachment`, `UpdateGeneratedAttachment`. `fetchAttachmentsByVendor` and
    `APIPartLocalAttachments` reuse `ListPartAttachments` and filter in Go.
  - Company: `ListCompanyAttachments`, `GetCompanyAttachmentPath`, `CreateCompanyAttachment`,
    `UpdateCompanyAttachment`, `UpdateCompanyAttachmentFile`, `SoftDeleteCompanyAttachment`,
    `CompanyFileInUse`, `EnsureCompanyPrimary`, `SetCompanyPrimary`, `FindDuplicateCompanyAttachment`.
  - `WhereUsed` (the part + supplier UNION).
- `internal/attachments/attachments.go`: `Service`/`New(dbq.DBTX)`; types `PartAttachment`,
  `CompanyAttachment`, `Duplicate`, `Usage`; `PreviewCategory`/`ThumbnailCategory` constants (arx_go's
  `previewCategory`/`thumbnailCategory` alias them).
- `arx_go`:
  - `h.attachments()`, plus `attachmentTx(ctx, fn)` in place of `execThenEnsurePrimary`; the caller runs
    the write and the ensure inside it.
  - `deleteAttachmentFileIfUnshared` takes an in-use func (`PartFileInUse`/`CompanyFileInUse`) instead of
    table/column names.
  - `primaryAttachmentEnsureSQL`, `ensurePartPrimary`, `ensureSupplierPrimary`, `setPrimaryAttachment`,
    the generic `findDuplicateAttachment` and `attachmentUsage` go away (and so does
    `TestPrimaryAttachmentEnsureSQL`).
  - Vendor-scope and sort-order values become `*int` through the batch helpers.
  - Ids from the URL are parsed with `strconv.Atoi`; a bad id takes the same error branch the DB error
    used.
- Fix: `SupplierAttachmentUpdate` removes the replaced file from `companyAttachmentRoot()`, not
  `DocControlRoot`.
- Update the existing tests that call the changed helpers (`deleteAttachmentFileIfUnshared`,
  `setPrimaryAttachment`, `insertAttachmentRow`).

## Resolved decisions
- Both tables go in one package (user choice), since the attachment helpers are shared.
- Supplier-root bug: fix it here with a red test (user choice).

## Test plan
1. Coverage audit (`integration_test.go`): `AttachmentDuplicateHash`, `DeleteAttachmentFileIfUnshared`,
   `SetPrimaryAttachment`, `AutoPrimaryAttachment`, `APIPartPasteAttachment`,
   `APIPartPasteAttachmentReplace`, `APIPartGenerateThumbnail`, `UpsertGeneratedAttachment`,
   `ResolveAttachmentFileInput`, `SupplierPartCreate_DigiKeyImport`;
   `categories_integration_test.go` `PartAttachments_CategoryOptions`.
2. Characterization (`attachments_integration_test.go`):
   - `PartAttachmentVendorScope`: create with `s:`/`m:` scopes; stored ids; the page shows the vendor
     name; `fetchAttachmentsByVendor` keys by link; another part's link and a bad token are rejected.
   - `PartAttachmentUpdate_Metadata`: no file change keeps file_name and hash, clears scope and sort order.
   - `AttachmentWhereUsed`: one link shared by a part and a supplier lists both.
   - `APIPartLocalAttachments`: only LOCAL: files, not dirs or URLs.
   - `SupplierAttachments_ListUpdateDelete`: listing, metadata update, file change sets hash, soft delete,
     `SupplierSetPrimaryAttachment`.
3. Red: `attachments.go` in `convertedFiles`; `SupplierAttachmentUpdate_RemovesOldFileFromSupplierRoot`
   (SupplierFilesRoot ≠ DocControlRoot, same-named file in both: supplier copy removed, Doc Control copy
   kept).
4. Manual-only: part attachments page (add/edit/delete, vendor picker, paste, batch upload, thumbnail
   button), supplier attachments page, where-used link.
