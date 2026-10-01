# #233 Shared attachment root: file-in-use must check both tables

## Root cause
`companyAttachmentRoot()` (arx_go/attachments.go:689) returns `DocControlRoot` when `SupplierFilesRoot == ""`. `deleteAttachmentFileIfUnshared` (arx_go/attachments.go:528) is a plain function that only calls the `inUse` callback it is given. Part callers pass `Service.PartFileInUse` (part_attachment only), supplier callers pass `Service.CompanyFileInUse` (company_attachment only). With a shared folder, the other table's active link is ignored.

## Choke point
All 6 call sites go through `deleteAttachmentFileIfUnshared`, but it is a free function with no access to config, so it cannot decide on its own. Choke point is the `inUse` callback: add two Handler methods that wrap the Service calls, and swap the callback argument at each of the 6 sites (one-token edit each; the free function and its signature stay unchanged so existing tests compile).

## Changes
1. arx_go/attachments.go, next to `companyAttachmentRoot()`:
   - `type fileInUseFunc func(ctx context.Context, fileName string, excludeID int) (bool, error)`
   - Pure helper `func combineInUse(own, other fileInUseFunc, shared bool) fileInUseFunc`: if `!shared` return `own`. Otherwise return a func that calls `own(ctx, name, excludeID)`; if error or true, return that; else return `other(ctx, name, 0)` (excludeID 0 because the caller's excludeID is an id in its own table, not the other table).
   - `func (h *Handler) sharedAttachmentRoot() bool { return h.companyAttachmentRoot() == h.cfg().DocControlRoot }`
   - `func (h *Handler) partFileInUse(ctx, name string, excludeID int) (bool, error)`: `svc := h.attachments(); return combineInUse(svc.PartFileInUse, svc.CompanyFileInUse, h.sharedAttachmentRoot())(ctx, name, excludeID)`
   - `func (h *Handler) companyFileInUse(...)`: same with own/other swapped.
2. Replace the callback argument (method values `h.partFileInUse` / `h.companyFileInUse`):
   - arx_go/api.go:315 (`APIPartPasteAttachmentReplace`): `h.attachments().PartFileInUse` -> `h.partFileInUse`
   - arx_go/api.go:477 (`upsertGeneratedAttachment`): `svc.PartFileInUse` -> `h.partFileInUse`
   - arx_go/parts.go:1374 (`PartAttachmentCreate` discard_import): -> `h.partFileInUse`
   - arx_go/parts.go:1578 (`PartAttachmentUpdate`): -> `h.partFileInUse`
   - arx_go/suppliers.go:365 (`SupplierAttachmentCreate` discard): `h.attachments().CompanyFileInUse` -> `h.companyFileInUse`
   - arx_go/suppliers.go:524 (`SupplierAttachmentUpdate`): -> `h.companyFileInUse`
3. Update the doc comment on `deleteAttachmentFileIfUnshared` to say the callback should be `Handler.partFileInUse` / `companyFileInUse`.

## Test plan
### 1) Coverage audit
- Existing: `TestIntegration_DeleteAttachmentFileIfUnshared` (arx_go/integration_test.go:5188) covers single-table shared / unshared / inactive sharer / already gone / supplier side, calling the free function directly with Service methods (unchanged by this fix).
- Existing: `TestIntegration_SupplierAttachmentUpdate_RemovesOldFileFromSupplierRoot` (arx_go/attachments_integration_test.go:~376) covers separate roots only.
- Not covered: cross-table check; blank SupplierFilesRoot; any of the 6 call sites with a shared root; `companyAttachmentRoot()` fallback.

### 2) Characterization tests (pass on unchanged code and must still pass)
- Existing `TestIntegration_DeleteAttachmentFileIfUnshared` subtests, unchanged.
- New unit test in arx_go/attachments_test.go `TestCombineInUse_NotShared_UsesOwnOnly`: fakes with other=true, shared=false; assert result equals own's result and `other` never called. (Passes today only once helper exists; treat as characterization of intended separate-root behavior alongside the existing integration test for separate roots.)
- Existing separate-roots integration test above.

### 3) Red tests (fail today)
Unit (no DB, fakes; fail to compile today since helper is missing, i.e. red):
- `TestCombineInUse_Shared_OtherTableCounts`: shared=true, own returns false, other returns true -> result true. Today no such union exists.
- `TestCombineInUse_Shared_OtherGetsZeroExclude`: shared=true, own false; assert other received excludeID 0 when called with 7.
- `TestCombineInUse_Shared_OwnErrorShortCircuits`: own returns error -> error returned, other not called.

Integration (arx_go/attachments_integration_test.go, liveHandler, ArxDev seed):
- `TestIntegration_SharedRoot_PartReplaceKeepsSupplierLinkedFile`: `tempDocControlRoot(t,h)`; set `h.cfg().SupplierFilesRoot = ""` (restore in defer); write `foo-233.txt` with unique content; seed part (`seedThrowawayPart(t,h,ctx,"233")`) + part_attachment `LOCAL:foo-233.txt` (`seedThrowawayAttachment`); seed supplier (`seedSupplier`) + company_attachment `LOCAL:foo-233.txt`; call `h.PartAttachmentUpdate` replacing the part file with a different upload; assert `foo-233.txt` still exists. Fails today: PartFileInUse ignores company_attachment so the file is removed.
- `TestIntegration_SharedRoot_SupplierReplaceKeepsPartLinkedFile`: same seed; call `h.SupplierAttachmentUpdate` replacing the supplier file; assert file still exists. Fails today for the mirror reason.
- Optional control in the same file `..._SeparateRoots_StillRemoves`: SupplierFilesRoot = t.TempDir(); the two roots differ; supplier replace removes the old file from supRoot (guards `shared` stays false).

New seed rows (exact, ArxDev, all removed in defers, none persistent):
- part (throwaway via seedThrowawayPart "233"), part_attachment(file_name `LOCAL:foo-233.txt`, category "Test", via seedThrowawayAttachment)
- company (via seedSupplier), company_attachment(supplier_id, file_path `LOCAL:foo-233.txt`)

### 4) Manual-only
- Via UI with SUPPLIER_FILES_ROOT blank: attach LOCAL file to a part and a supplier, replace the supplier file, confirm the part attachment still opens; repeat from the part side. Also the Create-duplicate "cancel/discard" path on each side, and the paste-replace and thumbnail/preview regeneration paths (api.go), which have no cheap automated driver.

## Open questions
1. Shared-root detection is string equality of `companyAttachmentRoot()` and `DocControlRoot`. Should it instead compare cleaned/absolute paths (e.g. trailing slash, different spelling of same folder, or both configured to the same dir explicitly)? Plan assumes: equality via `filepath.Clean` on both is NOT applied unless you say so.
2. Should the same cross-table check apply when SUPPLIER_FILES_ROOT is explicitly set to the same folder as DOC_CONTROL_ROOT? Plan assumes yes, as that is covered by the equality check.
3. Do `PartFileInUse` / `CompanyFileInUse` SQL both filter `is_active` and match the exact `LOCAL:` string (case-sensitive)? Not verified in the SQL; plan assumes existing semantics are acceptable.

## Resolved decisions
- Shared-root detection compares filepath.Clean(filepath.Abs(...)) of companyAttachmentRoot() and DocControlRoot (not plain string equality); also applies when SUPPLIER_FILES_ROOT is explicitly set to the same folder.
- Existing PartFileInUse/CompanyFileInUse SQL behavior unchanged.
