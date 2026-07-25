# #819: Test coverage for migrateLegacy

New file: `arxlib/config/local_test.go` (package `config`).

Reuse `isolateStores(t)` helper already defined in `arxlib/config/session_secret_test.go` (same package, no import needed) to isolate `config/local.pm.json` / `config/local.tr.json` reads/writes into a temp cwd per test.

Since `migrateLegacy` is unexported, tests must live in package `config` (not `config_test`) — matches `local.go`'s own package, so call `migrateLegacy()` directly.

Helper to write legacy files: use `os.MkdirAll("config", 0755)` then `os.WriteFile("config/local.pm.json", []byte(json), 0644)` / `"config/local.tr.json"` per test as needed. Write raw JSON literals (not structs, since `legacyPM`/`legacyTR` are unexported local types inside the function) so each test controls exactly which fields are set/omitted.

Add these test functions to `local_test.go`:

1. `TestMigrateLegacy_NoFiles` — call `isolateStores(t)`, call `migrateLegacy()`, assert result is `nil`.

2. `TestMigrateLegacy_PMOnly` — write only `config/local.pm.json` with all PM fields set to distinct non-zero values (db_server, db_name, db_user, doc_control_root, po_folder_root, supplier_files_root, debug_mode: true, test_mode: true, test_db_name). Call `migrateLegacy()`. Assert returned `*LocalConfig` has each PM-sourced field matching, and `ImageRoot == ""` (TR-only field, no TR file present).

3. `TestMigrateLegacy_TROnly` — write only `config/local.tr.json` with db_server, db_name, db_user, doc_control_root, image_root, debug_mode: true, test_mode: true, test_db_name all set to distinct non-zero values. Call `migrateLegacy()`. Assert DBServer/DBName/DBUser/DocControlRoot/TestMode/TestDBName equal the TR values (fallback fired since PM absent → zero values), ImageRoot equals TR's image_root, and SupplierFilesRoot == "" and DebugMode == false (PM-only fields, no PM file present, so they stay zero-value — TR's debug_mode is ignored since LocalConfig.DebugMode is only ever assigned from pm.DebugMode).

4. `TestMigrateLegacy_BothFiles_PMWins` — write both files with all overlapping fields (db_server, db_name, db_user, doc_control_root, test_mode, test_db_name) set to different non-empty values in PM vs TR, and TR image_root set. Call `migrateLegacy()`. Assert DBServer/DBName/DBUser/DocControlRoot/TestMode/TestDBName all equal PM's values (not TR's), and ImageRoot equals TR's value regardless.

5. `TestMigrateLegacy_PerFieldFallback` — write PM file with only some of the fallback-eligible fields set (e.g. db_server set, db_name/db_user/doc_control_root/test_db_name left as empty string or omitted, test_mode omitted/nil) and TR file with all of those fields set to distinct values. Call `migrateLegacy()`. Assert: DBServer equals PM's value (PM was non-empty, no fallback), and DBName/DBUser/DocControlRoot/TestDBName/TestMode each equal TR's value (PM was empty/nil, fallback fired). This confirms fallback is evaluated independently per field, not as an all-or-nothing merge.

6. `TestMigrateLegacy_MalformedJSON`:
   - Sub-case A: malformed `config/local.pm.json` (invalid JSON) + valid `config/local.tr.json`. Assert `migrateLegacy()` returns non-nil, with PM-sourced fields all zero-value (DBServer/DBName/DBUser/DocControlRoot/POFolderRoot/SupplierFilesRoot/DebugMode/TestDBName all zero, TestMode nil) and TR-fallback fields (DBServer/DBName/DBUser/DocControlRoot/TestMode/TestDBName) populated from TR since PM's unmarshal failed silently, leaving `found` set only via TR and PM fields at Go zero values.
   - Sub-case B: malformed both files. Assert `migrateLegacy()` returns `nil` (found stays false since both `json.Unmarshal` calls error).
   - Comment above these tests noting this documents current (silent-swallow) behavior per issue #819 — not asserting it's correct, out of scope to change.

7. `TestMigrateLegacy_TestModePointerSemantics`:
   - Sub-case A: PM `test_mode` omitted (nil), TR `test_mode: false`. Assert result `TestMode` is a non-nil pointer to `false` (fallback copies TR's pointer even though `false` is the Go zero value — proves the check is `lc.TestMode == nil`, not a value/falsy check).
   - Sub-case B: PM `test_mode: false` (explicit, non-nil pointer), TR `test_mode: true`. Assert result `TestMode` is a non-nil pointer to `false` (PM's explicit false must NOT be overwritten by TR's true, since fallback only fires when PM's pointer is nil).

Each test that needs `*bool` field checks should dereference and check both `!= nil` and the pointed-to value, failing with a clear message if nil.

## Open questions
None — plan is self-contained given existing `isolateStores` helper and the read `migrateLegacy` source.
