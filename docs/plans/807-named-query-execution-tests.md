# Plan: Close named-query execution test coverage gap (#807)

## Context established by reading the code

- `arx_go/named_query.go`: `runNamedQuery` (line ~114) looks up `sql, result_type` from `named_queries` by `name`, then calls `execQuery` (line ~142), which enforces `isSafeQuery`, binds `sql.Named(k, v)` params, executes, and scans into `QueryResult{Rows, ResultType}`.
- Existing unit tests in `arx_go/helpers_tr_test.go` cover only `isSafeQuery`/`parseQuerySpec` (pure parsing, no DB). No file named `*named_query*_test.go` exists.
- Integration harness: `arx_go/integration_test.go`, build-tagged `integration`, uses `liveHandler(t)` (opens ArxDev via `ARX_TEST_DSN`, asserts DSN contains "arxdev"), helper `assertStatus`/`assert302`, and direct `h.DB().QueryRowContext`/`h.queryContext` for setup/cleanup. Tests that insert rows clean them up in a `defer`; tests that only read seeded fixture rows do no cleanup (e.g. `TestIntegration_UserPODefaults`).
- Seed data: `SQL/seed_test_data.sql` (~line 110) inserts the canonical `named_queries` rows (identity-assigned, looked up by `name`) including `max_subbatch_result` with params `form_row_id, record_date` — i.e., **already renamed**, matching current code, not the pre-incident state. `SQL/named_queries.sql` is the reference DDL/seed source of truth, kept in sync manually.
- The production incident: `SQL/migrations/migrate_max_subbatch_result_param_rename.sql` documents that `max_subbatch_result`'s stored `sql`/`params` were renamed `@test_id → @form_row_id`, but `form_row.spec_nom`/`result.spec_nom` *usage text* still said `query:max_subbatch_result(@test_id=...)`. Since `parseQuerySpec` parses params from the **spec_nom text**, not from the `named_queries.params` column, this produced a runtime SQL Server error: `Must declare the scalar variable '@form_row_id'`.
- `h.dia().BoolLiteral(true)` / `h.cfg.NamedQueriesTable()` are the dialect/table helpers used in existing code — reuse for any new SQL in tests.

## Resolved decisions

1. **No SQL-mock library.** `go.mod` does not include one; adding a new test-only dependency for a single test file is not worth it. Skip the pure-unit `named_query_test.go` file entirely. Fold all `execQuery`/`runNamedQuery` coverage into the integration suite (build-tagged, real ArxDev).

2. **Fixture for single-result test**: have the test create a temporary part + part_attachment fixture in a `defer`-cleaned setup step (matches `TestIntegration_PartLifecycle`'s self-contained pattern), rather than relying on hardcoded seed data that could drift.

3. **Error substring assertion**: use a loose, case-insensitive `strings.Contains` check for `"declare the scalar variable"` (avoid brittleness on exact quoting/punctuation).

4. **`APINamedQuery` bad-prefix test**: since it never touches the DB (400 fires before `runNamedQuery` is reached), write it as a plain unit test (no `integration` build tag) in a new non-integration test file so it runs in default `go test ./...`.

5. **Temp `named_queries` row insert**: plain `INSERT ... VALUES` (IDENTITY PK, no triggers per `SQL/named_queries.sql`), looked up/deleted by unique `name`, not by id.

## File changes

### 1. New file `arx_go/named_query_unit_test.go` (no build tag)

- `TestAPINamedQuery_BadSpecPrefix` — build `httptest.NewRequest` with `?spec=badprefix:foo`, call `h.APINamedQuery(rec, req)` directly, assert `rec.Code == http.StatusBadRequest`.

### 2. Additions to `arx_go/integration_test.go` (build-tagged `integration`)

Add near the existing read-mostly integration tests:

- **`TestIntegration_RunNamedQuery_SingleResult`**
  - Setup: insert a temporary `part` row and a `part_attachment` row marked primary (unique part number via timestamp suffix), `defer` cleanup (delete attachment then part).
  - Call `h.runNamedQuery(ctx, fmt.Sprintf("query:pn_primary_attachment(@pn=%s)", partNumber))`.
  - Assert `err == nil`, `ResultType == "single"`, exactly 1 row, `Value`/`Label` match the inserted attachment's file_name/category.

- **`TestIntegration_RunNamedQuery_ListResult_MultiColumn`**
  - Use an existing named query that returns 2+ columns against seeded data already in `SQL/seed_test_data.sql` (e.g. `parts_matching` or `pos_for_pn` — implementer picks whichever seeded named query returns 2-column list results with existing seed rows; grep `SQL/named_queries.sql` for a `result_type='list'` row with a `SELECT` returning 2+ columns).
  - Assert `ResultType == "list"`, at least 1 row, `Value != Label`.

- **`TestIntegration_RunNamedQuery_NoRowsIsNotError`**
  - Call an existing named query (e.g. `parts_matching`) with a pattern guaranteed to match nothing (`ZZZ-NO-MATCH-%`).
  - Assert `err == nil`, `len(Rows) == 0`.

- **`TestIntegration_RunNamedQuery_UnknownName`**
  - Call `h.runNamedQuery(ctx, "query:does_not_exist_xyz(@x=1)")`.
  - Assert `err != nil`, `strings.Contains(err.Error(), "not found")`.

- **`TestIntegration_RunNamedQuery_StaleSpecNomParamRename`** — regression test for the production incident
  - Setup: insert a temporary `named_queries` row via `h.execContext`: `name = fmt.Sprintf("itest_stale_param_%d", time.Now().UnixNano())`, `sql = "SELECT 1 AS val WHERE @new_param = @new_param"`, `params = "new_param"`, `result_type = "single"`, `is_active = 1`.
  - `defer` cleanup: `DELETE FROM named_queries WHERE name = @name`.
  - Call `h.runNamedQuery(ctx, fmt.Sprintf("query:%s(@old_param=1)", name))` — spec_nom supplies `@old_param`, stored SQL declares `@new_param`.
  - Assert `err != nil` and `strings.Contains(strings.ToLower(err.Error()), "declare the scalar variable")`.
  - Add a comment cross-referencing #807 and `SQL/migrations/migrate_max_subbatch_result_param_rename.sql`, noting this is the documented incident, not a bug being fixed by this test.

- **`TestIntegration_APINamedQuery_EndToEnd`**
  - Build `GET /api/named-query?spec=query:parts_matching(@pattern=...)` against a seeded part number pattern, call `h.APINamedQuery(rec, req)` directly.
  - Assert 200 + JSON body decodes to expected rows (non-empty).

## Test summary

| Test | File | Build tag | Asserts |
|---|---|---|---|
| `TestAPINamedQuery_BadSpecPrefix` | named_query_unit_test.go | none | bad spec prefix → 400 |
| `TestIntegration_RunNamedQuery_SingleResult` | integration_test.go | integration | real single-result query against self-created fixture |
| `TestIntegration_RunNamedQuery_ListResult_MultiColumn` | integration_test.go | integration | real multi-row/2-col query against seed |
| `TestIntegration_RunNamedQuery_NoRowsIsNotError` | integration_test.go | integration | zero matches ⇒ nil err, empty Rows |
| `TestIntegration_RunNamedQuery_UnknownName` | integration_test.go | integration | unknown name ⇒ "not found" |
| `TestIntegration_RunNamedQuery_StaleSpecNomParamRename` | integration_test.go | integration | **regression test**: reproduces "Must declare the scalar variable" from stale spec_nom param name |
| `TestIntegration_APINamedQuery_EndToEnd` | integration_test.go | integration | HTTP handler happy path |

This is a test-only change — no production code modified.

## Critical files

- arx_go/named_query.go (read only, not modified)
- arx_go/integration_test.go (additions)
- arx_go/named_query_unit_test.go (new)
- SQL/named_queries.sql (reference, read only)
- SQL/seed_test_data.sql (reference, read only)
