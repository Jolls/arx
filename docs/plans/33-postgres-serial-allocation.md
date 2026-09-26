# #33 — Postgres serial allocation in CreateRecord

## Problem
`CreateRecord` (arx_go/records.go ~1651-1670) allocates the next per-form serial inside the tx with `FROM %s WITH (UPDLOCK, HOLDLOCK)` — a SQL Server hint, a syntax error on Postgres (`syntax error at or near "WITH"`). Every auto-serial record create fails on `main`.

Grep of `arx_go/` for `UPDLOCK|HOLDLOCK|ROWLOCK|NOLOCK|READPAST|TABLOCK`: only this site (records.go 1657 comment, 1663 SQL).

## Approach
Transaction-scoped advisory lock keyed on form id, taken before the `MAX()+1` read; released automatically on commit/rollback. No schema change; no new dialect abstraction (`main` is Postgres-only).

## File changes

### 1. `arx_go/records.go`
a. Package-level constant near `isAutoSerial` (~line 112):
```go
// serialAllocLockNS namespaces the per-form advisory lock that serializes
// auto serial allocation in CreateRecord (arx-legacy#369, #33).
const serialAllocLockNS = 369
```

b. In `CreateRecord`, replace the whole `if isAutoSerial(serialNumber, suggestedSN) { ... }` block (~1655-1670, including the 4-line SQL Server comment) with:
```go
if isAutoSerial(serialNumber, suggestedSN) {
	// Serialize concurrent auto-allocations for this form: the xact-scoped
	// advisory lock is held until tx commit/rollback, so the MAX()+1 read and
	// the INSERT below are atomic w.r.t. other creates on the same form.
	if _, err = tx.ExecContext(r.Context(),
		`SELECT pg_advisory_xact_lock(@p1, @p2)`, serialAllocLockNS, formID); err != nil {
		serverError(w, "serial lock error", err)
		return
	}
	var nextSN int
	err = tx.QueryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(MAX(%s), 0) + 1
		FROM %s WHERE form_id = @p1`,
		h.dia().TryCastInt("serial_number"), h.cfg().RecordsTable()), formID).Scan(&nextSN)
	if err != nil {
		serverError(w, "serial number error", err)
		return
	}
	serialNumber = strconv.Itoa(nextSN)
}
```
Keep the comment at ~1651-1654. `tx.ExecContext` rewrites `@pN` (handlers.go:244). If Postgres reports an ambiguous signature, use `pg_advisory_xact_lock(@p1::int, @p2::int)`.

c. GET new-record comment (~1564-1565) already accurate; no change.

### 2. `arx_go/records_create_integration_test.go` (new, `//go:build integration`)
See Test plan §3.

## Resolved decisions
- Lock key: two-int form `pg_advisory_xact_lock(369, form_id)` (namespace 369 = arx-legacy race fix).
- `h.dia().TryCastInt` left in place; seam removal belongs to #29.

## Test plan

### 1. Coverage audit
- `arx_go/helpers_tr_test.go` — table test for `isAutoSerial` (pure unit, no SQL).
- `arx_go/smoke_post_test.go` — header (line 30) explicitly excludes `CreateRecord`.
- `arx_go/records_lock_integration_test.go` — `seedLockTestForm` seeds part/form/form_row; inserts records via raw SQL, never calls `CreateRecord`.
- `arx_go/integration_test.go` — raw-SQL record inserts; none exercises `CreateRecord`.
- Result: nothing covers `CreateRecord` / serial allocation.

### 2. Characterization tests
None — current behavior is a hard failure on Postgres. `TestIsAutoSerial` must keep passing.

### 3. Red tests (`arx_go/records_create_integration_test.go`)
Use `liveHandler(t)`, `seedLockTestForm(t, h, ctx)` + deferred cleanup, `postForm`, `withID`, `assertStatus`.
- `TestIntegration_CreateRecord_AutoSerial`: POST `h.CreateRecord` with `serial_number=1`, `suggested_serial_number=1`, `record_type=Production`; assert 303, parse id from `Location` (strip `/edit`), assert stored `serial_number` `"1"`; second POST → `"2"`. Manual-override case: `serial_number=ABC-7`, `suggested_serial_number=3` → stored `"ABC-7"`. Fails today (500, WITH syntax error).
- `TestIntegration_CreateRecord_ConcurrentDistinctSerials`: 10 goroutines released together via start channel + WaitGroup, each own recorder, same form, SN=suggested=`"1"`; all 303; stored serials exactly {"1".."10"}. Fails today.

### 4. Manual-only
- Postgres ArxDev app: form → New Record → accept suggested SN → submit → redirects to `/records/{id}/edit`.
- Two tabs on the same form's New Record, submit both → consecutive SNs.
