# #117 — safePath resolves symlinks

## Design (arx_go/files.go, safePath)
Keep existing lexical logic. After the lexical prefix check passes, add a resolved check:
1. `realRoot, err := filepath.EvalSymlinks(absRoot)`; error → `"", false`.
2. Resolve `absResult` by walking up to the deepest existing ancestor (handles upload targets not yet created): loop `EvalSymlinks(p)`; on `os.IsNotExist`, set `p = filepath.Dir(p)` and remember the unresolved tail; stop at success. Any other error → `"", false`. Rejoin tail onto the resolved ancestor.
3. Require resolved result == realRoot or has prefix `realRoot + separator`; else `"", false`.
4. Return the original lexical `result` (unchanged return value, so callers/tests keep their paths).

Root missing entirely: EvalSymlinks(root) not-exist → reject (false). Check callers (files.go, attachments.go, pos.go, suppliers.go, utilities.go, api.go) for a not-yet-created root: they all serve/list existing roots except upload, whose `dir` exists (listed). Confirm by running the test suite.

## Tests (arx_go/files_test.go or helpers_test.go)
- Symlink inside temp root → outside dir: `safePath(root, "link/secret.txt")` and `safePath(root, "link")` return false. `t.Skip` if `os.Symlink` errors (Windows).
- Nonexistent leaf under real dir still ok (`safePath(root, "new.txt")`).
- Existing safePath tests pass (temp roots on Windows may resolve 8.3 names — compare via resolved roots only inside safePath, not in test expectations).

## Not touched
- `arx_go/cmd/backfill_attachment_hash/main.go` has its own safePath copy; out of scope (mention in PR).
