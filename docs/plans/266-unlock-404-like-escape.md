# Plan: #266 UnlockRecord 404 for missing record; LIKE-escape part/supplier search

## Findings (current code)
- `arx_go/records.go` `UnlockRecord` (~L1704): `rec, err := h.records().GetRecord(...)`; any err -> `serverError(w, "unlock error", err)` (500). Other handlers (e.g. L278, L410, L587) do `if err == sql.ErrNoRows { http.NotFound(w, r); return }` before the generic error.
- `internal/parts/parts.go` `SearchParts` (L898-902) builds `"%" + q + "%"`; comment says "q is not LIKE-escaped".
- `internal/purchasing/purchasing.go` `SearchSuppliers` (L147-150) same; comment "not LIKE-escaped".
- SQL: `internal/parts/parts.sql` `SearchParts` (L358-367): three `ILIKE sqlc.arg(pattern)::text`; `internal/purchasing/purchasing.sql` `SearchSuppliers` (L41-50): `su.name ILIKE sqlc.arg(pattern)::text`.
- No existing LIKE-escape helper anywhere (grep of *.go/*.sql outside internal/dbq found none). No other `ESCAPE` in any .sql. Create one.
- Both services already import `arx/internal/dbq`; module is `arx`.

## File changes
1. `arx_go/records.go` UnlockRecord: after `GetRecord`, add before `serverError`:
   `if err == sql.ErrNoRows { http.NotFound(w, r); return }` (match the style at L278). `database/sql` is already imported in this file.
2. New `internal/db/likeescape.go` (package `db`):
   `func EscapeLike(s string) string` — replaces `\` -> `\`, `%` -> `\%`, `_` -> `\_` (backslash first; use `strings.NewReplacer`). Doc comment: pair with `ESCAPE '\'`.
3. `internal/parts/parts.sql`: append ` ESCAPE '\'` after each of the three `ILIKE sqlc.arg(pattern)::text` (description, detail, part_number). Update the header comment to mention escaped pattern.
4. `internal/purchasing/purchasing.sql`: `su.name ILIKE sqlc.arg(pattern)::text ESCAPE '\'`.
5. Run `sqlc generate` (do not hand-edit `internal/dbq`); commit regenerated files. Confirm param names/types unchanged (`Pattern string`).
6. `internal/parts/parts.go` and `internal/purchasing/purchasing.go`: use `"%" + db.EscapeLike(q) + "%"`; add import `arx/internal/db`; update the two doc comments to say q is matched literally (LIKE-escaped).
   (If `arx/internal/db` creates an import cycle with parts/purchasing, see Open questions.)

## Test plan
### 1) Coverage audit
- UnlockRecord: `arx_go/records_lock_integration_test.go` `TestIntegration_LockApproveUnlockLifecycle` covers 403 (no permission), 400 (no comment), success. No test for a nonexistent record id.
- Search: `arx_go/api_part_integration_test.go` `TestIntegration_APIPartSearch` (substring, by=desc, case-insensitive, no match) and `arx_go/suppliers_sqlc_integration_test.go` `TestIntegration_SupplierSQLC_Search` cover plain-text matching. Nothing covers `%` / `_` / `\` in q. No unit tests exist in `internal/parts`, `internal/purchasing`; `internal/db` has `namedparams_test.go`.
- These integration tests seed their own rows via `seedAPIParts` / `seedCompanies` with cleanup (not ArxDev seed rows).

### 2) Characterization tests (pass on unchanged code; keep passing)
- Existing `TestIntegration_APIPartSearch` and `TestIntegration_SupplierSQLC_Search` unchanged (plain-text queries behave identically after escaping).
- Existing lifecycle test's 403/400/redirect unlock cases unchanged.

### 3) Red tests (fail today)
- `internal/db/likeescape_test.go` `TestEscapeLike` (unit, table): `"50%"` -> `50\%`; `"A_1"` -> `A\_1`; `` `a\b` `` -> `` `a\b` ``; `"plain"` -> `plain`; `""` -> `""`. Fails today: function does not exist (compile failure).
- `arx_go/records_lock_integration_test.go` `TestIntegration_UnlockRecord_Missing`: call `h.UnlockRecord` via `reviewerCtx(withID(postForm("/records/2147483647/unlock", url.Values{"comment": {"x"}}), 2147483647))`; assert `http.StatusNotFound`. Fails today: returns 500. Needs no seed rows.
- `arx_go/api_part_integration_test.go` `TestIntegration_APIPartSearch_LikeEscape`: seed via `seedAPIParts` with `base := smokeUniq("APE")` parts `{base+"-50%", nil, nil}`, `{base+"-505", nil, nil}`, `{base+"-A_1", nil, nil}`, `{base+"-AX1", nil, nil}`; assert `q=base+"-50%25"` (URL-encoded `%`) returns only the `-50%` part; `q=base+"-A_1"` returns only `-A_1`. Also a by=desc case: seed descriptions `"x 50% y"` / `"x 505 y"`, `by=desc&q=50%25` restricted by unique token is not possible with a bare `50%`, so use `tok := smokeUniq("dsc")` and descriptions `tok+"_1"` / `tok+"X1"`, query `by=desc&q=tok_1` -> only the `_` row. Fails today: wildcard also matches `-505` / `-AX1`.
- `arx_go/suppliers_sqlc_integration_test.go` `TestIntegration_SupplierSQLC_Search_LikeEscape`: seed via `seedCompanies` (name, true, true) with names `base+" 50%"`, `base+" 505"`, `base+" A_1"`, `base+" AX1"` (`base := smokeUniq("SLE")`); assert `q=base+" 50%25"` returns only the `50%` company and `q=base+" A_1"` only the `A_1` company (URL-encode the query with `url.QueryEscape`). Fails today: wildcards over-match.

### 4) Manual-only
- In the running UI, part autocomplete: type `%` and `_` alone; confirm results are only parts literally containing those characters (the API requires a min query length; confirm `%`-only input is handled by that rule). Same for supplier typeahead.
- Confirm `sqlc generate` leaves `git diff internal/dbq` limited to the SQL text in the generated query constants.

## Open questions
1. Place `EscapeLike` in `internal/db` (assumed; does `internal/db` import parts/purchasing, causing a cycle?) or a different package?
2. Should the 404 for a missing record use plain `http.NotFound` (as other record handlers do) — assumed yes.
3. Should `Escape` also be applied to any other ILIKE/LIKE uses (none found in internal/*.sql; arx_go inline SQL was not exhaustively audited) — out of scope assumed.

## Resolved decisions
- EscapeLike goes in internal/db (verified: internal/db imports no arx/ packages, no cycle).
- Missing record in UnlockRecord uses plain http.NotFound, as other record handlers do.
- LIKE-escaping of other uses is out of scope.
