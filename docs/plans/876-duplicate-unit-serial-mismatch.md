# #876 — Saving a serial-tracked record mints a duplicate unit when record and unit serials disagree

## Root cause

`arx_go/records.go`, `SaveResults` (around line 2745), decides how to link a
serial/lot_serial record to its unit purely from `record.SerialNumber`,
ignoring `record.UnitID` even when the record already has one:

```go
	var unitArg interface{}
	if models.TracksSerials(trackingMode) && record.SerialNumber != "" && (buildArg != nil || lotArg != nil) {
		uid, uerr := h.upsertUnitForRecord(r.Context(), tx, record.PartNumberID, record.SerialNumber, buildArg, lotArg)
		if uerr != nil {
			http.Error(w, "could not record unit: "+uerr.Error(), http.StatusInternalServerError)
			return
		}
		unitArg = uid
		lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
	}
```

`upsertUnitForRecord` (line 1920) looks up `unit` by `(part_id, serial_number)`
only. If a unit's `serial_number` is later edited (#799, Part → Units) so it
no longer matches the record's `serial_number`, the lookup misses, mints a
new unit, and repoints the record — orphaning the original unit.

`record.UnitID` is `*int` (`arx_go/models/trmodels.go:78`) and is already
read into `record` at the top of `SaveResults`
(`arx_go/records.go:2458-2467`, `unit_id` scanned into `&record.UnitID`).

## Fix

**File:** `arx_go/records.go`
**Location:** replace the block at lines 2745-2754 (the `unitArg` block,
immediately after the `trackingMode` lookup and before the `UPDATE ... SET
record_date=...` / `UPDATE ... SET comments=...` statements).

Current code (lines 2745-2754):

```go
	var unitArg interface{}
	if models.TracksSerials(trackingMode) && record.SerialNumber != "" && (buildArg != nil || lotArg != nil) {
		uid, uerr := h.upsertUnitForRecord(r.Context(), tx, record.PartNumberID, record.SerialNumber, buildArg, lotArg)
		if uerr != nil {
			http.Error(w, "could not record unit: "+uerr.Error(), http.StatusInternalServerError)
			return
		}
		unitArg = uid
		lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
	}
```

New code:

```go
	var unitArg interface{}
	if record.UnitID != nil {
		// Already linked to a unit — reuse it rather than re-deriving from
		// serial_number. A unit's serial can be edited after the fact (#799, Part →
		// Units), which would otherwise desync it from record.SerialNumber; re-deriving
		// by serial on every save would then silently mint a duplicate unit and orphan
		// the original (#876). There is no UI to change a record's serial_number after
		// creation, so the record<->unit link, once set, is authoritative.
		unitArg = *record.UnitID
		lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
	} else if models.TracksSerials(trackingMode) && record.SerialNumber != "" && (buildArg != nil || lotArg != nil) {
		uid, uerr := h.upsertUnitForRecord(r.Context(), tx, record.PartNumberID, record.SerialNumber, buildArg, lotArg)
		if uerr != nil {
			http.Error(w, "could not record unit: "+uerr.Error(), http.StatusInternalServerError)
			return
		}
		unitArg = uid
		lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
	}
```

### Why this doesn't conflict with other `record.UnitID` usage in `SaveResults`

- The `build_panel == "1"` block (lines 2672-2679) already rejects the
  request with 400 when `record.UnitID != nil || record.BuildID != nil`,
  before this new branch is reached — so a record with an existing unit
  never falls through to the build-panel path that sets `buildArg`/`lotArg`
  from a fresh build. The only way `buildArg`/`lotArg` are non-nil when
  `record.UnitID != nil` is a manual `lot_id`/`build_id` form pick via
  `recordLinkageArgs` (line 2632); those are intentionally discarded here,
  identical to what the pre-existing upsert branch already does after
  minting/re-linking a unit (Q8: provenance lives on the unit, not the
  record).
- `record` is read once at the top of `SaveResults` and never mutated
  mid-handler, so `record.UnitID` reflects the DB state at read time — no
  other code path in `SaveResults` reassigns it before this block.

No changes needed to `upsertUnitForRecord` itself — it's still correct for
the "record has no unit yet" case (initial mint and same-serial retest).

## Regression test

**File:** `arx_go/integration_test.go` (build tag `integration`, live-DB
harness via `liveHandler`) — this needs a live DB because it exercises
`h.SaveResults` end-to-end through the same code path as the manual repro,
matching the existing pattern in `TestIntegration_SerialUnitCreationAndRetest`
(same file, ~line 2172) and `TestIntegration_UnitSerialLocked` (~line 2533).

Add a new test function directly after `TestIntegration_SerialUnitCreationAndRetest`
(after its closing `}` at line 2266):

```go
// TestIntegration_SaveDoesNotDuplicateUnitOnSerialMismatch verifies #876: saving a
// serial/lot_serial record that's already linked to a unit reuses that unit via
// record.UnitID rather than re-deriving it from record.SerialNumber. Editing a
// unit's serial_number after linking (#799, Part → Units) desyncs it from the
// record's serial; before the fix, the next save of the record would silently
// mint a duplicate unit and repoint the record at it, orphaning the original.
func TestIntegration_SaveDoesNotDuplicateUnitOnSerialMismatch(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	rt := h.cfg.RecordsTable()
	ut := h.cfg.UnitTable()

	const testedPart = 3013 // ASM-1003, tracking_mode lot_serial in seed
	const provBuild = 8203  // a build of 3013
	origSerial := smokeUniq("SN-MISMATCH-ORIG")

	// Mint a unit directly (as upsertUnitForRecord would) and a record linked to it.
	insertUnit := h.dia().InsertReturningID(h.cfg.UnitTable(),
		`part_id, build_id, serial_number, source`, `@p1, @p2, @p3, 'test'`, false)
	var unitID int
	if err := h.DB().QueryRowContext(ctx, insertUnit, testedPart, provBuild, origSerial).Scan(&unitID); err != nil {
		t.Fatalf("seed unit: %v", err)
	}

	var recordID int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_id, part_id, serial_number, subject_part_number, subject_pn_description, comments, test_order, is_locked, is_active, unit_id)
		 OUTPUT INSERTED.id VALUES (6001, @p1, @p2, 'ASM-1003', 'Widget Deluxe Assembly', '', '', 0, 1, @p3)`, rt),
		testedPart, origSerial, unitID).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	defer func() {
		// Records reference the unit via unit_id FK — delete them before the unit(s).
		// Also sweep any duplicate unit the bug would have minted with origSerial.
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id = @p1`, rt), recordID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id = @p1 AND serial_number = @p2`, ut), testedPart, origSerial)
	}()

	// Simulate #799: the unit's serial is corrected after linking, diverging it
	// from the (unchangeable) record.serial_number.
	if _, err := h.DB().ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET serial_number = @p1 WHERE id = @p2`, ut),
		smokeUniq("SN-MISMATCH-RENAMED"), unitID); err != nil {
		t.Fatalf("rename unit serial: %v", err)
	}

	// Save the record with no changes — this alone must not touch unit linkage.
	req := withID(postForm(fmt.Sprintf("/records/%d/edit", recordID), url.Values{}), recordID)
	rec := httptest.NewRecorder()
	h.SaveResults(rec, req)
	assertStatus(t, "SaveResults(unit serial mismatch)", rec, http.StatusSeeOther)

	var gotUnit sql.NullInt64
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT unit_id FROM %s WHERE id = @p1`, rt), recordID).Scan(&gotUnit); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !gotUnit.Valid || int(gotUnit.Int64) != unitID {
		t.Errorf("record unit_id = %v, want unchanged %d (reuse, not re-derive)", gotUnit, unitID)
	}

	var dupCount int
	if err := h.DB().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE part_id = @p1 AND serial_number = @p2`, ut), testedPart, origSerial).
		Scan(&dupCount); err != nil {
		t.Fatalf("count units with original serial: %v", err)
	}
	if dupCount != 0 {
		t.Errorf("found %d unit row(s) still/newly carrying the original serial %q — save minted a duplicate unit", dupCount, origSerial)
	}
}
```

### Test helper note

`smokeExec` (`arx_go/smoke_post_test.go:41`) swallows its error (`_, _ :=
h.DB().ExecContext(...)`) — it's only used for best-effort cleanup deletes in
`defer`, never for a statement whose success the test depends on. The unit
rename above uses `h.DB().ExecContext` directly with its own `t.Fatalf` check
instead, matching the style already used for the unit INSERT in this same
test and for inserts elsewhere in the file (e.g.
`TestIntegration_ManualUnitTestedCountUnaffected`). `smokeExec` is still used
for the deferred cleanup deletes since a cleanup failure shouldn't mask the
test's real assertion failures.

### Verification

1. `cd arx_go; go build ./... ; go vet ./... ; go test ./...` — must pass
   (existing suite, no build tag).
2. Run integration tests against ArxDev per CLAUDE.md:
   ```
   $env:ARX_TEST_DSN="sqlserver://user:pass@server?database=ArxDev&encrypt=true"
   go test -tags integration ./arx_go/...
   ```
   Confirm the new test passes, and `TestIntegration_SerialUnitCreationAndRetest`
   and `TestIntegration_ManualUnitTestedCountUnaffected` still pass (the
   latter is the test the issue reports as currently broken by this bug via
   record 7013 — note #877, applied before this plan, fixes record 7013's
   seed data separately; this plan does not touch seed data).
3. Manual repro from the issue: reseed ArxDev, open record 7013 → Edit →
   Save with no changes, confirm no new `unit` row appears and
   `form_record.unit_id` is unchanged.

## Resolved decision

User confirmed **Option A — reuse the linked unit** (as implemented above),
over Option B (resync unit's serial from record), which was flagged as
actively harmful since it would undo a deliberate correction made via
Part → Units (#799).
