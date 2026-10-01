# #263 UpsertTestUnit: single-statement upsert (ON CONFLICT)

## Findings (current code)
- `internal/inventory/inventory.go` `UpsertTestUnit` (l.428): `GetUnitIDBySerial`; on `sql.ErrNoRows` -> `CreateTestUnit`; other errors returned.
- `internal/inventory/inventory.sql` l.183 `GetUnitIDBySerial` (`WHERE part_id=.. AND serial_number=..`, no NULL/empty special-casing), l.186 `CreateTestUnit` (`INSERT ... source 'test' RETURNING id`).
- Only caller: `arx_go/records.go` ~l.2171 (SaveResults); it already skips the call when serial is `""` or both build/lot are nil.
- DDL `SQL/postgres/unit.sql` l.25: `CONSTRAINT UQ_unit_serial UNIQUE (part_id, serial_number)`; `serial_number VARCHAR(255) NOT NULL`. Plain (non-partial) unique constraint -> `ON CONFLICT (part_id, serial_number)` matches. No schema change/migration/seed change.
- `GetUnitIDBySerial` / `CreateTestUnit` have no other callers (Go) besides `UpsertTestUnit`.

## Changes
1. `internal/inventory/inventory.sql`: replace `GetUnitIDBySerial` and `CreateTestUnit` with one query:
```sql
-- name: UpsertTestUnit :one
INSERT INTO unit (part_id, serial_number, build_id, lot_id, source)
VALUES (sqlc.arg(part_id), sqlc.arg(serial_number), sqlc.narg(build_id), sqlc.narg(lot_id), 'test')
ON CONFLICT (part_id, serial_number) DO UPDATE SET serial_number = EXCLUDED.serial_number
RETURNING id;
```
   (DO UPDATE no-op is needed so RETURNING yields the existing id; build_id/lot_id/source are not in SET, so provenance is set only on creation.)
2. Run `sqlc generate` (repo root); commit regenerated `internal/dbq/inventory.sql.go` (never hand-edit). Removes `GetUnitIDBySerial*`/`CreateTestUnit*` from dbq.
3. `internal/inventory/inventory.go` `UpsertTestUnit`: body becomes `return s.q.UpsertTestUnit(ctx, dbq.UpsertTestUnitParams{PartID: partID, SerialNumber: serial, BuildID: buildID, LotID: lotID})`. Update doc comment (single statement, race-safe). Remove `errors`/`database/sql` imports only if now unused (both still used by `UnitProvenance` at l.442 -> `errors`; check `sql`).
4. `CHANGELOG.md`: new top `## [0.8.x]` entry (or existing unreleased) under `### Fixed`: concurrent test saves with the same new serial no longer fail with a 500 ([#263](https://github.com/Jolls/arx/issues/263)).
5. Tests below in `arx_go/inventory_integration_test.go` (build tag `integration`).

## Test plan
### 1) Coverage audit
- `TestIntegration_InventoryUpsertUnitForRecord` (inventory_integration_test.go l.460): covers create (provenance + source 'test') and retest re-link without provenance change, in one tx.
- `TestIntegration_ManualUnitDuplicateSerial` covers manual path only (CreateManualUnit, unchanged).
- Not covered: two concurrent transactions upserting the same new serial; empty-serial/both-nil skip in records.go (handler guard, unchanged).
- No unit test without DB exists for inventory (queries live-DB only).

### 2) Characterization tests (pass on unchanged code)
- Existing `TestIntegration_InventoryUpsertUnitForRecord` must keep passing unchanged after the change (create-then-relink, provenance preserved).
- Add `TestIntegration_InventoryUpsertTestUnit_ExistingSeedSerial`: in `invTx`, `UpsertTestUnit(ctx, 3005, "SN-3005-001", nil, nil)` returns seed unit id 8503 and leaves its build/lot/source unchanged (SELECT before/after equal). Passes today (find path).
- Add `TestIntegration_InventoryUpsertTestUnit_SamePartSerialDifferentPart`: upsert same new serial for part 3005 (build 8201) and part 3012 (build 8202) in one tx -> two different ids. Passes today.

### 3) Red tests (ArxDev, integration tag only)
- `TestIntegration_InventoryUpsertTestUnit_Concurrent` (same file). Steps: `serial := smokeUniq("SN-263")`; `t.Cleanup` -> `smokeExec(DELETE FROM unit WHERE part_id=3005 AND serial_number=$1)`; open tx1, tx2 via `h.beginTx` (separate connections, NOT `invTx` shared); tx1 `UpsertTestUnit(3005, serial, &8201, nil)` -> id1; start goroutine running tx2 `UpsertTestUnit(3005, serial, &8201, nil)` -> id2/err2 (this blocks on tx1's uncommitted row once past the SELECT/at INSERT); `time.Sleep(200ms)`; commit tx1; wait for goroutine; commit tx2.
  - Assert: err2 == nil, id2 == id1, `SELECT count(*) FROM unit WHERE part_id=3005 AND serial_number=$1` == 1.
  - Why fails today: tx2's `GetUnitIDBySerial` sees no row (tx1 uncommitted), its `CreateTestUnit` waits for tx1 commit then fails `uq_unit_serial` (23505) -> err2 != nil.
- `TestIntegration_InventoryUpsertTestUnit_ConcurrentProvenanceKept`: same shape, tx2 passes `buildID=nil`; additionally assert unit's build_id remains 8201 and source 'test'. Fails today for same reason.
- Optional handler-level red test via `h.SaveResults` x2 concurrently: not planned (see Open questions).

### 4) Manual-only
- Two browser sessions saving two records of one serialized part with the same new serial simultaneously; both redirect 303, one unit exists. (Timing window too narrow to reproduce reliably by hand; the integration test is the primary check.)

### New seed rows
None.

## Verify
`go build ./... && go vet ./... && go test ./...`; `sqlc diff`; `ARX_TEST_FROM_CONFIG=1 go test -tags integration ./arx_go/...`.

## Open questions
1. DO UPDATE no-op rewrites the existing row on every retest (new row version, would fire any update trigger/bump `updated_at` if one exists; unit.sql shows no trigger/updated column). Acceptable?
2. Remove the now-unused `GetUnitIDBySerial` query (plan removes it) or keep it?
3. Add a handler-level concurrent `SaveResults` test in addition to the service-level tests?

## Resolved decisions
- Remove the now-unused GetUnitIDBySerial query (and regenerate sqlc).
- No-op DO UPDATE rewrite on retest is accepted (unit has no trigger/updated_at).
- Service-level concurrency tests only; no handler-level SaveResults test.
