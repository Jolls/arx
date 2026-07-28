# #809 — Test coverage for resolveAttachmentFileInput / deleteAttachmentFileIfUnshared / setPrimaryAttachment

Part of #801 (test coverage gap audit).

## Test file

Extend `arx_go/integration_test.go` (`//go:build integration`). No new test file — these
functions take `ctx` and call `h.queryRowContext`/`h.execContext` against real tables
(`is_active` filters, `COUNT(*)`, `UPDATE`), and the repo has no DB-mocking library
(no sqlmock anywhere in `go.mod`/source). Every existing DB-touching test in this repo
uses the same live-ArxDev, build-tagged pattern (`liveHandler(t)` + `ARX_TEST_DSN`).
`resolveAttachmentFileInput` and `deleteAttachmentFileIfUnshared` also do real filesystem
I/O, which the existing pattern already sets up per-test via `h.cfg.ImageRoot = t.TempDir()`
(see `TestIntegration_PasteResultImageGuards`). Follow the identical approach here with
`h.cfg.DocControlRoot`.

Add three new test functions to `arx_go/integration_test.go`:
- `TestIntegration_ResolveAttachmentFileInput`
- `TestIntegration_DeleteAttachmentFileIfUnshared`
- `TestIntegration_SetPrimaryAttachment`

All three call `liveHandler(t)` then set `h.cfg.DocControlRoot = t.TempDir()` (no restore
needed — `h` is a fresh live connection per test, discarded at test end).

## Fixtures

Do not reuse the pinned seed rows (part 3002/3004, part_attachment 8101/8102, or any
company row — `company_attachment` isn't seeded at all per `SQL/seed_test_data.sql`
comment at line 26). These tests mutate rows (`primary_attachment_id`, deleting files,
inserting attachment rows), so — matching every other mutating integration test in this
file (`TestIntegration_PartLifecycle`, `seedCyclePair`, etc.) — seed throwaway `ITEST-`
prefixed rows with `time.Now().UnixNano()` suffixes and delete them in `defer` cleanup.

Needed per test, using `h.cfg.PartsTable()` / `h.cfg.AttachmentsTable()` /
`h.cfg.CompanyTable()` / `h.cfg.CompanyAttachmentsTable()` (never hardcode table names):
- A throwaway part: `INSERT INTO %s (part_number, revision, title, release_status, is_active) OUTPUT INSERTED.id VALUES (@p1,'A','Integration Test Part','U',1)`.
- A throwaway part_attachment row on that part when a test needs an existing attachment ID: `INSERT INTO %s (part_id, file_name, category, sort_order) OUTPUT INSERTED.id VALUES (@p1,@p2,'Test',1)`.
- A throwaway supplier/company row for the supplier-side `setPrimaryAttachment`/`deleteAttachmentFileIfUnshared` case: `INSERT INTO %s (name, is_active) OUTPUT INSERTED.id VALUES (@p1,1)`.
- A throwaway company_attachment row when needed: `INSERT INTO %s (supplier_id, file_path) OUTPUT INSERTED.supplier_attachment_id VALUES (@p1,@p2)`.

Building HTTP requests to call `resolveAttachmentFileInput` (it takes `*http.Request`):
use the existing `postForm(target, url.Values{...})` helper already in this file — do not
invent a new request builder.

## 1. `resolveAttachmentFileInput` test cases

Function signature: `resolveAttachmentFileInput(ctx, r, partID, rev, category, comment, replaceName) attachmentFileInput`.

1. **Manual FILFileName, no import** — `source_path` absent, `FILFileName` set to a plain
   URL. Expect `FileName == urlutil.NormalizeLink(fileName)`, no `MoveSrc`, no `Collision`,
   no `ErrMsg`. No part row or DocControlRoot needed (function returns before touching
   either) — use a bogus partID to prove it never reaches `fetchPartBasic`.
2. **DocControlRoot not configured** — set `h.cfg.DocControlRoot = ""`, post a form with
   `source_path` set to some path. Expect `ErrMsg == "DOC_CONTROL_ROOT is not configured; cannot import files."`.
   Restore `h.cfg.DocControlRoot = t.TempDir()` after (local var, not shared across tests).
3. **Fresh import copy (happy path)** — seed a throwaway part, write a real source file
   under `t.TempDir()` (a *different* dir than DocControlRoot), post form with
   `source_path` = that file, `FILPNRev`, `category` set. Expect `FileName` to start with
   `"LOCAL:"`, the generated name matching `buildAttachmentFileName(partNumber, rev, title, category, ext)`,
   the file actually copied into `h.cfg.DocControlRoot` (read it back, compare bytes), and
   `MoveSrc == ""` (move_source not set).
4. **Move mode** — same as #3 but with `move_source=1` in the form. Expect
   `MoveSrc == src` (the original source path) so the caller knows to remove it — do not
   have this test remove it (that's the caller's job in parts.go), just assert the field.
5. **Import collision** — same as #3, but pre-create a file at the target generated name
   inside `h.cfg.DocControlRoot` *before* calling the function. Expect
   `Collision != nil` with `Collision["Name"]` equal to the generated name and
   `Collision["SourcePath"]` equal to `src`; `FileName` and `MoveSrc` both empty. Confirms
   the collision path never silently overwrites or orphans the existing file — read the
   pre-created file back afterward and confirm its original bytes are untouched.
6. **link_existing=1 skips the copy** — post `source_path` + `link_existing=1`, no file
   pre-created at the target name. Expect `FileName == "LOCAL:" + name` returned anyway,
   but assert via `os.Stat` that no file was created at that path in DocControlRoot (the
   "link to an existing file already on disk" flow never writes).
7. **replaceName match uses replaceLocalFile, not copyIntoDocControl** — pre-create the
   target name in DocControlRoot with known "old" bytes, pass `replaceName` equal to that
   same generated name (case-insensitive per `strings.EqualFold`), and a source file with
   different "new" bytes. Expect no `Collision` (this is the "same row replacing its own
   file" branch), `FileName == "LOCAL:" + name`, and the target file's bytes now equal to
   the new source content (proves the file was swapped in place, not reported as a false
   collision against itself).
8. **Part not found** — pass a partID that doesn't exist (e.g. a very large int as a
   string) with `source_path` set. `fetchPartBasic` returns an error (its `Scan` on zero
   rows is not special-cased to `sql.ErrNoRows`, so `err != nil` here is expected). Expect
   `ErrMsg` starting with `"Error loading part: "`.

## 2. `deleteAttachmentFileIfUnshared` test cases

Signature: `deleteAttachmentFileIfUnshared(ctx, table, idCol, fileCol, excludeID, fullFileName, root, strippedName) error`.

1. **Not shared → file removed** — seed one throwaway part_attachment row with
   `file_name = "LOCAL:<name>"`, write the actual file at `<DocControlRoot>/<name>`. Call
   with `excludeID` = that row's own id (simulating "this row is being replaced/deleted;
   is anyone else still pointing at the old name"), `fullFileName = "LOCAL:<name>"`,
   `strippedName = "<name>"`. Expect `err == nil` and the file gone (`os.Stat` →
   `os.IsNotExist`).
2. **Shared → file preserved (the #809 orphan-risk case)** — seed *two* throwaway
   part_attachment rows on the same or different parts, both with the identical
   `file_name` value, both `is_active` (default). Call with `excludeID` = the first row's
   id (so the query's `idCol<>excludeID` still counts the second row). Expect `err == nil`
   and the file **still present** on disk afterward — this is the exact scenario the issue
   calls out ("orphaning a file that's still shared elsewhere").
3. **Inactive sharer doesn't count as shared** — seed two rows with the same file_name,
   but soft-delete the second one (`is_active = 0`) before calling. Expect the file **is**
   removed (an inactive row must not keep a file alive), proving the `is_active=1` filter
   in the COUNT query is load-bearing.
4. **Already-gone file is not an error** — don't create the file on disk at all, seed the
   row (unshared), call the function. Expect `err == nil` (per the doc comment: "A file
   that's already gone is treated as success, not an error").
5. **Table-agnostic: supplier/company_attachment path** — repeat case 1 (not-shared →
   removed) using `h.cfg.CompanyAttachmentsTable()`, `"supplier_attachment_id"`,
   `"file_path"` against a throwaway company + company_attachment row, proving the same
   function correctly parametrizes for the supplier side (this is the code path
   `SupplierAttachmentUpdate` in suppliers.go exercises).

## 3. `setPrimaryAttachment` test cases

Signature: `setPrimaryAttachment(ctx, table, idCol, primaryCol, parentID, attachmentID any) error`.

1. **Set primary on a part** — seed a throwaway part + part_attachment row on it. Call
   `setPrimaryAttachment(ctx, h.cfg.PartsTable(), "id", "primary_attachment_id", partID, attID)`.
   Verify via `SELECT primary_attachment_id FROM part WHERE id=@p1` that it now equals
   `attID`.
2. **Clear primary (nil)** — continuing from #1 (or fresh), call again with
   `attachmentID = nil`. Verify the column is now `NULL` (scan into `sql.NullInt64`,
   assert `!Valid`). This directly covers the issue's "losing/corrupting the pointer" risk
   in the clear direction — confirms `nil` clears rather than erroring or leaving stale data.
3. **Set primary on a supplier (table-agnostic)** — seed a throwaway company +
   company_attachment row. Call with `h.cfg.CompanyTable()`, `"id"`,
   `"primary_attachment_id"`, the company's id, the attachment's id. Verify via
   `SELECT primary_attachment_id FROM company WHERE id=@p1` it's set — covers the "or
   supplier" half of the issue text, which the two callers (`PartSetPrimaryAttachment` /
   `SupplierSetPrimaryAttachment`) exercise separately but no test currently touches
   either.
4. **No-op on nonexistent parentID** — call with a `parentID` that doesn't exist (e.g.
   `999999999`). Expect `err == nil` (a `WHERE` clause matching zero rows is not a SQL
   error) — this documents current behavior (silent no-op, no rows affected) rather than
   asserting a design change; no caller currently checks affected-row count.

## Notes for implementation

- Every seeded row (part, part_attachment, company, company_attachment) needs a `defer`
  cleanup that hard-deletes it, mirroring the existing style in this file (delete children
  before parents).
- Use `strconv.FormatInt(time.Now().UnixNano(), 10)` suffixes for part_number/company name
  uniqueness, matching existing tests in the file.
- `h.cfg.DocControlRoot` is a plain string field (see `arxlib/config/config.go`) — set it
  directly per test the same way `TestIntegration_PasteResultImageGuards` sets
  `h.cfg.ImageRoot`.
- No CLAUDE.md "new table" work applies — no schema/seed_test_data.sql changes needed;
  all fixtures here are ephemeral, created and torn down within each test.

## Open questions

None — the unit-vs-integration question is resolved (integration, following the
established pattern), and no schema/behavior ambiguity remained after reading the code.
