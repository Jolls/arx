# #148 — generic error responses for JSON/plain-text handler paths (part of #142)

Reuses the `serverError(w, msg, err)` helper already added in `arx_go/auth.go` for #110
(logs `msg: err` via `log.Printf`, responds with generic `msg` + 500). No new helper needed —
it already covers 30 of the 31 sites below (all currently `http.StatusInternalServerError`).
Debuggability: `serverError` always logs the full error server-side regardless of DEBUG_MODE
(DEBUG_MODE only gates separate SQL-query terminal logging in `handlers.go`) — no DEBUG_MODE
branch needed to keep local debugging intact.

One site (`unit.go:315`) is `http.StatusBadRequest`, not 500, and doesn't fit `serverError`'s
fixed 500 status — handled inline (step 10) instead of extending the helper's signature.

Generic message for all 30 `StatusInternalServerError` sites: `"database error"` (matches the
#110 precedent exactly).

## 1. `arx_go/contacts.go`
- Line 49 (`ContactsRows`, query error): replace `http.Error(w, err.Error(), http.StatusInternalServerError)` with `serverError(w, "database error", err)`
- Line 65 (`ContactsRows`, row scan error): same replacement

## 2. `arx_go/lot.go`
- Line 428 (`LotRecordsRows`): replace `http.Error(w, err.Error(), http.StatusInternalServerError)` with `serverError(w, "database error", err)`

## 3. `arx_go/named_query.go`
- Line 220 (`APINamedQuery`, `runNamedQuery` error): replace `http.Error(w, err.Error(), http.StatusInternalServerError)` with `serverError(w, "database error", err)`

## 4. `arx_go/parts.go`
- Line 192 (`PartsRows`, query error): replace with `serverError(w, "database error", err)`
- Line 204 (`PartsRows`, row scan error): same replacement
- Line 228 (`PartsRows`, `rows.Err()`): same replacement
- Line 2086 (`APIPartLocalAttachments`, query error): same replacement
- Line 2197 (`PartRecordsRows`, `scopedRecordsRows` error): same replacement
- Line 2762 (`PartsExportCSV`, query error): same replacement
- Line 2821 (`BOMExportCSV`, query error): same replacement

## 5. `arx_go/pos.go`
- Line 315 (`PORows`, query error): replace with `serverError(w, "database error", err)`
- Line 328 (`PORows`, row scan error): same replacement
- Line 352 (`PORows`, `rows.Err()`): same replacement
- Line 1110 (`POMarkPrinted`, `execContext` update error): same replacement
- Line 2778 (`POsExportCSV`, query error): same replacement

## 6. `arx_go/records.go`
- Line 320 (`RecordsRows`, query error): replace with `serverError(w, "database error", err)`
- Line 333 (`RecordsRows`, row scan error): same replacement
- Line 351 (`RecordsRows`, `rows.Err()`): same replacement

## 7. `arx_go/reports.go`
- Line 684 (`ReportsOnTimeExportCSV`, query error): replace with `serverError(w, "database error", err)`
- Line 789 (`ReportsCycleTimeExportCSV`, query error): same replacement
- Line 901 (`ReportsDataQualityNoAttachmentsExportCSV`, query error): same replacement
- Line 912 (`ReportsDataQualityMissingSupplierExportCSV`, query error): same replacement
- Line 923 (`ReportsDataQualityStaleRollupExportCSV`, query error): same replacement
- Line 1020 (`ReportsSpendBySupplierExportCSV`, query error): same replacement
- Line 1036 (`ReportsSpendByPartExportCSV`, query error): same replacement

## 8. `arx_go/suppliers.go`
- Line 67 (`SuppliersRows`, query error): replace with `serverError(w, "database error", err)`
- Line 78 (`SuppliersRows`, row scan error): same replacement
- Line 91 (`SuppliersRows`, `rows.Err()`): same replacement

## 9. `arx_go/unit.go` — 500 site
- Line 250 (`UnitRecordsRows`, `scopedRecordsRows` error): replace `http.Error(w, err.Error(), http.StatusInternalServerError)` with `serverError(w, "database error", err)`

## 10. `arx_go/unit.go` — 400 site (does not use `serverError`)
- Line 315 (`UnitCreate`, `recordLinkageArgs` error — mix of app-generated validation strings like "invalid selection" and raw driver errors from an internal `queryRowContext` call, so must still be genericized): replace
  ```go
  http.Error(w, err.Error(), http.StatusBadRequest)
  ```
  with
  ```go
  log.Printf("invalid lot/build selection: %v", err)
  http.Error(w, "invalid lot or build selection", http.StatusBadRequest)
  ```
  `unit.go` must import `"log"` — check current import block and add it if missing.

## Verify
1. `cd arx_go; .\build.bat` (runs `go vet`/`go test ./...`) — confirms no leftover unused imports (`log` still used if newly added to `unit.go`) and no compile errors.
2. Grep to confirm zero remaining raw sites: `git grep -n 'http.Error(w, err.Error()' -- 'arx_go/*.go' | grep -v _test` should return nothing.

## Test file impacts
Checked existing test assertions on response bodies (`rec.Body.String()` across `*_test.go`) —
none assert on raw driver-error text from any of the 31 sites above. The two tests that check
`err.Error()` content for `runNamedQuery` (`integration_test.go` lines ~3232, ~3264) call
`h.runNamedQuery` directly, not through `APINamedQuery`/HTTP, so they're unaffected. No test
changes required.
