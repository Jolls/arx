# #452 — Auto-mint a unit for a no-BOM serial-tracked part on record save

No migration needed. No DDL/schema change — `unit.lot_id`/`unit.build_id` are already nullable
and `UQ_unit_serial (part_id, serial_number)` is the only uniqueness constraint involved.

## Implementation deviations from this plan

Two conflicts with existing tests surfaced mid-implementation that this plan's coverage audit
missed (both in `arx_go/integration_test.go`, a different/older file than the ones audited
below) and were resolved with the user before coding:

1. `TestIntegration_SaveResults_NoUnitWithoutProvenance` pinned the old Q5 rule — no mint
   without provenance — for part 3013, which HAS a BOM. Resolved: apply the fix universally
   (any serial-tracked save with no provenance now mints), not scoped to no-BOM parts only.
   That test was flipped to assert the new behavior.
2. `TestIntegration_SaveResults_LinksExistingUnitWithoutPick` pinned that a no-pick save
   linking to an EXISTING manual unit must leave its `source` as `'manual'`. Collapsing to a
   single unconditional `UpsertTestUnit(nil, nil)` call (as this plan originally specified)
   would have flipped that unit to `'test'` (`UpsertTestUnit`'s `ON CONFLICT` always sets
   `source='test'`). Resolved: added a new query/method, `FindOrCreateTestUnit`, used only in
   the no-provenance branch — it mints a new unit as `'test'` when none exists, but leaves an
   existing unit's source/provenance completely untouched. The real-provenance branch is
   unchanged (still calls `UpsertTestUnit`).

Both characterization and red tests below were run for real against the pre-fix code (via
`git stash` of the production change) before the fix was reapplied, confirming the stated
pre/post behavior rather than relying on static analysis.

## Open questions

None (as originally evaluated). The one thing the issue flagged as needing verification — whether `UpsertTestUnit`'s
`ON CONFLICT` target is `(part_id, serial_number)` (safe to call with both `lot_id`/`build_id`
nil) rather than something keyed on provenance — is confirmed safe by reading
`internal/inventory/inventory.sql`'s `UpsertTestUnit` query: its `ON CONFLICT (part_id,
serial_number)` matches `UQ_unit_serial` exactly, and the `DO UPDATE` only
`COALESCE`s existing lot/build (never overwrites with a NULL). Calling it with
`buildID=nil, lotID=nil` on a brand-new serial inserts a row with both NULL; on an existing
unit it's a no-op write (same values). This is also already exercised in
`arx_go/inventory_integration_test.go`'s `TestIntegration_InventoryUpsertUnitForRecord` (second
call, line ~486) and `TestIntegration_InventoryUpsertTestUnit_ExistingSeedSerial` — both call
`UpsertTestUnit(ctx, 3005, serial, nil, nil)` against an *already-existing* unit. No existing
test calls it with both nil as the *first* (creating) call — that gap is exactly what the new
red test below closes.

## 1. Code changes

### `arx_go/records.go` — the unit-lookup branch

Current code (inside `SaveResults`, right after the lot-note block, ~line 2172-2202):

```go
var unitArg *int
if record.UnitID != nil {
	// Already linked to a unit — reuse it rather than re-deriving from
	// serial_number. A unit's serial can be edited after the fact (#799, Part →
	// Units), which would otherwise desync it from record.SerialNumber; re-deriving
	// by serial on every save would then silently mint a duplicate unit and orphan
	// the original (#876). There is no UI to change a record's serial_number after
	// creation, so the record<->unit link, once set, is authoritative.
	unitArg = record.UnitID
} else if models.TracksSerials(trackingMode) && record.SerialNumber != "" {
	// With a picked build/lot, find or lazily create the unit for (part, serial) (#745, Q5): a
	// retest re-links the existing unit, filling only a NULL lot/build (#422). With neither, link to
	// an existing unit only — a test-minted unit should carry at least one of build/lot (an app rule,
	// not a DB CHECK: #799 dropped CK_unit_provenance since a manual unit legitimately has neither).
	inv := inventory.New(tx)
	if buildArg != nil || lotArg != nil {
		uid, uerr := inv.UpsertTestUnit(r.Context(), record.PartNumberID, record.SerialNumber, buildArg, lotArg)
		if uerr != nil {
			serverError(w, "could not record unit", uerr)
			return
		}
		unitArg = &uid
	} else {
		uid, uerr := inv.FindUnitIDBySerial(r.Context(), record.PartNumberID, record.SerialNumber)
		if uerr != nil {
			serverError(w, "could not look up unit", uerr)
			return
		}
		unitArg = uid
	}
}
if unitArg != nil {
	lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
}
```

Change: collapse the `if buildArg != nil || lotArg != nil { … } else { … }` into one
unconditional `UpsertTestUnit` call, since it's already find-or-create and safely accepts
`nil, nil`. `FindUnitIDBySerial` is no longer called from this branch (leave the method itself
alone — `TestIntegration_InventoryFindUnitIDBySerial` still exercises it directly, and nothing
else orphans by removing this one call site). Drop the now-unused `uerr`/`uid` duplication:

```go
var unitArg *int
if record.UnitID != nil {
	// Already linked to a unit — reuse it rather than re-deriving from
	// serial_number. A unit's serial can be edited after the fact (#799, Part →
	// Units), which would otherwise desync it from record.SerialNumber; re-deriving
	// by serial on every save would then silently mint a duplicate unit and orphan
	// the original (#876). There is no UI to change a record's serial_number after
	// creation, so the record<->unit link, once set, is authoritative.
	unitArg = record.UnitID
} else if models.TracksSerials(trackingMode) && record.SerialNumber != "" {
	// Find-or-create the unit for (part, serial) (#745, Q5, #452): a retest re-links the
	// existing unit, filling only a NULL lot/build (#422). With no provenance to attach
	// (no build-at-test-time, no picked lot/build — the common case for a no-BOM
	// serial-tracked part, which can never have a build), this still mints the unit with
	// both lot_id/build_id left NULL (#452) rather than merely looking one up: a
	// provenance-less source='test' unit is legitimate, not an invariant violation (#799
	// already dropped CK_unit_provenance for the same reason on manual units).
	uid, uerr := inventory.New(tx).UpsertTestUnit(r.Context(), record.PartNumberID, record.SerialNumber, buildArg, lotArg)
	if uerr != nil {
		serverError(w, "could not record unit", uerr)
		return
	}
	unitArg = &uid
}
if unitArg != nil {
	lotArg, buildArg = nil, nil // Q8: provenance lives on the unit, not the record
}
```

Notes for the implementer:
- The branch above it (`record.UnitID != nil`) is untouched — still reuses the linked unit
  verbatim, never touches `UpsertTestUnit`/serial at all.
- The `unitArg != nil` cleanup below is untouched; it already fires whenever `unitArg` is set,
  which now includes the plain-lookup case too (previously `unitArg` could be set via
  `FindUnitIDBySerial` returning non-nil, so this is not a new code path for that block).
- `inv := inventory.New(tx)` local var can be inlined as a one-off expression (as shown) since
  it's now used exactly once in the branch, or left as a local — either is fine; keep the diff
  minimal by matching whichever reads better against surrounding style (a local var matches the
  existing style slightly more closely, since `invSvc` a few lines up is also stored in a
  local — use `inv := inventory.New(tx)` then `inv.UpsertTestUnit(...)` to match).
- `record.PartNumberID` must be non-zero for this branch to ever run in practice (serial-tracked
  requires a part), but note it's already guarded by `models.TracksSerials(trackingMode)`, which
  itself came from `svc.GetPartTracking(r.Context(), record.PartNumberID)` a few dozen lines up —
  unchanged, not touched by this fix.

## 2. Comment / doc rewrites

### `arx_go/records.go`

Two comments assert the now-false invariant and must change:

1. The block comment directly above this branch (~line 2071-2074, before `pt, e :=
   svc.GetPartTracking`):
   ```go
   // #745: a serial/lot_serial part mints (or, on retest, re-links) the serialized
   // unit this record refers to when saved with provenance (a picked build or lot).
   // The record then points at the unit via unit_id and leaves lot_id/build_id NULL —
   // lot/build are read through the unit (Q8 FK-consistency invariant).
   ```
   Change "when saved with provenance (a picked build or lot)" → drop the provenance
   qualifier, since minting now happens whenever there's a serial, with or without
   provenance (#452). E.g.:
   ```go
   // #745: a serial/lot_serial part mints (or, on retest, re-links) the serialized
   // unit this record refers to, with or without a picked build/lot as provenance (#452 —
   // a no-BOM part has neither to offer, so the unit mints with both left NULL). The
   // record then points at the unit via unit_id and leaves lot_id/build_id NULL — lot/build
   // are read through the unit (Q8 FK-consistency invariant).
   ```

2. The in-branch comment quoted above under "Code changes" (~line 2182-2185) — already rewritten
   above as part of the code change; its old text ("a test-minted unit should carry at least
   one of build/lot (an app rule...)") is the exact sentence the issue calls out to revise.

### `SQL/SCHEMA.md` — `unit` table reference entry

Search for the "Tier-3 serialized instance" entry in the Table reference section. It currently
reads (per `SQL/unit.sql`'s header, mirrored in SCHEMA.md): a test-minted unit is described as
always carrying build or lot as an **app-level invariant** (not a DB CHECK — `CK_unit_provenance`
was dropped at #799). Locate the exact sentence with:
`grep -n "at least one" SQL/SCHEMA.md` (the issue's own wording quotes it as "a test-minted
unit always has at least one set (app-level Q5 invariant)").

Replace that sentence with something to the effect of: a `source='test'` unit mints with
`lot_id`/`build_id` both NULL when the record had no provenance to attach (most commonly a
no-BOM serial-tracked part, which can never go through the build-at-test-time or lot-pick
paths) — `source='test'` no longer implies at least one of lot/build is set; only `source` tells
you how the unit was minted (test-record save vs. #799 manual back-fill), not what provenance
it carries. Keep the existing wording style (dense, parenthetical issue references) rather than
rewriting the whole paragraph — this is a one-clause edit, not a rewrite.

Also check `SQL/unit.sql`'s header comment (lines 1-12, already read): it currently says only
"a `manual` unit (#799...) may legitimately have neither" — it should be extended to say a
`test` unit may now legitimately have neither too (#452), for the same reason SCHEMA.md needs
updating. `SQL/unit.sql` is reference DDL (comments only change here, no DDL/column change, so
no migration) — edit its header comment in the same PR.

## 3. Test plan

### Coverage audit

`SaveResults`'s unit-handling branches have no plain Go unit tests (no stub/mock DB anywhere in
`arx_go` for this handler) — only integration tests against a live Postgres (ArxDev or the
throwaway container), build-tagged `integration`, in `arx_go/*_integration_test.go`.

Existing coverage, found via grep for `SaveResults`, `UnitID`, `FindUnitIDBySerial`,
`UpsertTestUnit` across `arx_go/*_test.go`:

- `arx_go/records_writes_integration_test.go`:
  - `TestIntegration_RecordsWrites_SaveResults` — exercises `SaveResults` end-to-end (result
    rows, record_type/notes/date), but its fixture's `partID` is `nil` (NULL `form_record.part_id`
    — see `recRecord(t, h, f, nil, "77", …)`), so `trackingMode` resolves to `""` and the entire
    unit-lookup branch is skipped. Does not touch `unit_id`/the new code path at all.
  - `TestIntegration_RecordsWrites_SaveResultsNullColumns` — same, NULL part/serial, not
    relevant to unit-minting.
  - No existing test in this file sets a part with `tracking_mode='serial'`/`'lot_serial'` and
    calls `SaveResults`.
- `arx_go/inventory_integration_test.go` (tests `UpsertTestUnit`/`FindUnitIDBySerial` directly,
  not through `SaveResults`):
  - `TestIntegration_InventoryUpsertUnitForRecord` — creates a unit with `(build8201, nil)`,
    then re-upserts with `(nil, nil)` on the *same already-created* unit (asserts id reused,
    provenance untouched). Does not test an `(nil, nil)` call as the unit's *first* (creating)
    call.
  - `TestIntegration_InventoryUpsertTestUnit_ExistingSeedSerial` — `(nil, nil)` against the
    seeded unit 8503 (pre-existing), asserts provenance/source unchanged. Also not a creating
    call.
  - `TestIntegration_InventoryUpsertTestUnit_SamePartSerialDifferentPart`,
    `_KeepsExistingProvenance`, `_FillsNullProvenanceOnManualUnit`, `_FillsOnlyNullSide`,
    `_Concurrent`, `_ConcurrentProvenanceKept` — all pass at least one non-nil build/lot on the
    creating call; none creates a unit with both nil.
  - `TestIntegration_InventoryFindUnitIDBySerial` — tests `FindUnitIDBySerial` directly (unaffected
    by this change; still a used, tested method).

Conclusion: no existing test pins the current (buggy) `SaveResults` behavior for a no-BOM
serial-tracked part, and no existing test exercises `UpsertTestUnit(nil, nil)` as a unit's
*creating* call. Both are gaps this PR must fill with new tests in
`arx_go/records_writes_integration_test.go` (the right file: it's where `SaveResults` behavior
is characterized) plus one addition to `arx_go/inventory_integration_test.go` for the
`UpsertTestUnit` creating-call gap specifically.

These are integration tests against a live DB (no mock/stub DB path exists for `SaveResults`),
and the existing fixtures (`repPart`, `recForm`, `recRecord`) already build throwaway parts/forms
per test, not seeded fixed-ID rows — so **no new seed data in `SQL/seed_test_data.sql` is
needed**. `repPart(t, h, "ASM")` creates a part with `tracking_mode` defaulting to `'none'`
(`SQL/part.sql`'s `DEFAULT 'none'`) and no BOM rows (nothing in the test helpers inserts `bom`
rows for a `repPart`-created part), so a one-line
`repExec(t, h, "UPDATE part SET tracking_mode='serial' WHERE id=$1", partID)` — exactly the
pattern `recSetup` already uses for `tracking_mode='lot'` — produces the no-BOM
serial-tracked fixture needed, cleaned up automatically via `repPart`'s registered
`DELETE FROM part`.

### Characterization tests (pin CURRENT behavior; must pass unchanged, before the fix)

Add to `arx_go/records_writes_integration_test.go`, near `TestIntegration_RecordsWrites_SaveResults`:

1. **`TestIntegration_RecordsWrites_SaveResults_NoBOMSerialMintsNothing_Characterization`** — locks
   in the bug this PR fixes; must be deleted or rewritten once the fix lands (it asserts the
   *current*, wrong behavior).
   - Setup: `f := recForm(t, h)`; `partID, _ := repPart(t, h, "ASM")`;
     `repExec(t, h, "UPDATE part SET tracking_mode='serial' WHERE id=$1", partID)` (no BOM rows
     inserted for this part — none exist by construction); `r := recRecord(t, h, f, partID,
     smokeUniq("SN-452-CHAR"), "2026-07-01 10:00", "New Release", false, true, 2)`.
   - Act: `wPost(h.SaveResults, "/records/x/edit", url.Values{"record_type": {"X"}}, r)` — no
     `lot_id`/`build_id`/`build_panel` posted.
   - Assert (current/buggy behavior): `SELECT unit_id FROM form_record WHERE id=$1` is NULL, and
     `SELECT COUNT(*) FROM unit WHERE part_id=$1 AND serial_number=$2` is 0.
   - Mark with a `// LOCKS IN A BUG #452 FIXES — this test documents wrong behavior and must be
     deleted (or inverted) once the fix lands` comment at the top. **Do not keep this test post-fix**
     — it exists only to prove the bug is real before touching code; it gets deleted in the same
     commit that applies the fix (superseded by the red test below), not kept as a permanent
     regression test for buggy behavior.

2. **`TestIntegration_RecordsWrites_SaveResults_RetestReusesLinkedUnit_Characterization`** — pins
   that a record already linked to a unit (`record.UnitID != nil`) keeps reusing it untouched,
   unaffected by this change (this is the branch above the one being edited).
   - Setup: same serial-tracked no-BOM part/form; create a unit directly via
     `inventory.New(tx-or-handler-exec).CreateManualUnit`-equivalent SQL insert (or reuse
     `UpsertTestUnit` in a throwaway tx the way `inventory_integration_test.go` does) to get a
     unit id `u`; insert the record with `unit_id=u` already set (extend `recRecord`'s insert, or
     a follow-up `UPDATE form_record SET unit_id=$1 WHERE id=$2` after creating via `recRecord`).
   - Act: `SaveResults` with no `lot_id`/`build_id` posted (same as above).
   - Assert: `form_record.unit_id` is still `u`; `unit` row count for that `(part_id, serial)` is
     still 1 (no duplicate minted).
   - This test passes both before and after the fix (it's characterizing the untouched branch),
     so it is a permanent regression test, not deleted post-fix.

3. **`TestIntegration_RecordsWrites_SaveResults_LotTrackedProvenanceUnaffected_Characterization`**
   — pins that a build/lot-tracked part's existing `UpsertTestUnit`-with-real-provenance call
   still works exactly as before (the `buildArg != nil || lotArg != nil` path that already called
   `UpsertTestUnit`, just now unconditionally reached via the same function instead of an
   `if`/`else`).
   - Setup: serial-tracked part with a BOM line (one component via `repPart` + a `bom` insert, or
     simpler: reuse `recSetup`'s existing lot-tracked assembly-with-BOM fixture pattern from
     `records_reads_integration_test.go`, which already builds an assembly with a lot and a BOM
     component) and an active lot on that part (`invSvc.CreateLot`-equivalent, or direct insert +
     cleanup) with id `lotID`.
   - Act: `SaveResults` posting `"lot_id": {strconv.Itoa(lotID)}`.
   - Assert: a `unit` row exists for `(part_id, serial)` with `lot_id = lotID`, `build_id` NULL,
     `source='test'`; `form_record.unit_id` is set to it, `lot_id`/`build_id` on the record itself
     are NULL (Q8).
   - Passes both before and after the fix; permanent regression test.

### Red tests (fail today, pass after the fix — the acceptance criteria)

Add to `arx_go/records_writes_integration_test.go`:

4. **`TestIntegration_RecordsWrites_SaveResults_NoBOMSerialAutoMints`** — the issue's core
   acceptance criterion.
   - Setup: identical to characterization test #1 above (no-BOM serial-tracked part, no
     lot/build/build_panel posted, brand-new serial via `smokeUniq`).
   - Act: `SaveResults`.
   - Assert: `form_record.unit_id` is non-NULL; the `unit` row it points at has
     `part_id = partID`, `serial_number = <the posted serial>`, `source = 'test'`,
     `lot_id IS NULL`, `build_id IS NULL`; exactly one `unit` row exists for that
     `(part_id, serial_number)` pair.
   - Fails today because the branch calls `FindUnitIDBySerial` (returns nil for a brand-new
     serial) instead of `UpsertTestUnit`, so `unitArg` stays nil and no unit row is ever
     inserted.

5. **`TestIntegration_RecordsWrites_SaveResults_NoBOMSerialRetestFindsExistingUnit`** — acceptance
   criterion "a retest of a serial that already has a unit continues to re-link ... find-or-create,
   not always-create", specifically for the *no-provenance* path (test #2 above already covers the
   `record.UnitID != nil` branch; this one covers hitting the same serial via the lookup branch a
   second time without ever having `unit_id` set on the first record — e.g. two different records
   for the same part/serial, or the same record saved twice before `unit_id` persists — pick
   whichever is reachable; the simplest is: save once (mints unit `u1`), save again on the *same*
   record — by then `record.UnitID` is `u1` so it goes through the top branch, which test #2
   covers — so instead test this via **two separate records** for the same part/serial: record A
   saved first (mints unit), record B created after for the same part+serial and saved — B's
   `unit_id` arrives via the `UpsertTestUnit` find-or-create, not the `record.UnitID != nil` reuse
   branch, since B's own `unit_id` starts NULL).
   - Setup: no-BOM serial-tracked part/form; one serial string reused across two `recRecord`
     fixtures A and B (same `partID`, same `sn`).
   - Act: `SaveResults` on A (no provenance) → mints unit `u`. `SaveResults` on B (no provenance,
     same serial) → should find-or-create and resolve to the *same* unit `u`, not mint a second one.
   - Assert: B's `form_record.unit_id == u`; exactly one row in `unit` for that `(part_id, serial)`.
   - Fails today for the same reason as #4 (A never mints a unit in the first place, so there's
     nothing for B to find — B would also just get `unitArg = nil` via `FindUnitIDBySerial`, and
     the test's "exactly one unit row, both records point at it" assertion fails).

6. **`TestIntegration_InventoryUpsertTestUnit_CreatesWithBothNil`** — add to
   `arx_go/inventory_integration_test.go`, next to `TestIntegration_InventoryUpsertUnitForRecord`,
   closing the gap identified in the coverage audit: no existing test calls `UpsertTestUnit` with
   both args nil as the unit's *creating* call.
   - Act: `id, err := inventory.New(tx).UpsertTestUnit(ctx, 3005, smokeUniq("SN-452-NILNIL"), nil, nil)`
     on a brand-new serial that has no existing unit.
   - Assert: `err == nil`; a `unit` row with that `id` exists with `lot_id IS NULL`,
     `build_id IS NULL`, `source = 'test'`.
   - This test does NOT fail today — `UpsertTestUnit`/the SQL query are already correct and
     unchanged by this PR; its purpose is to close the stated test gap so the `records.go` fix is
     built on a verified-safe primitive, and to guard against a future accidental change to
     `UpsertTestUnit`'s SQL that would make `nil, nil` unsafe. (This is the test that resolves the
     "Open question" above — included regardless of whether it fails before the fix, since it's
     about the primitive, not the handler.)

### Manual-only

- Visually confirming the new unit appears in Part → Units for a real no-BOM serial-tracked part
  after saving a record (the UI list itself, `ListPartUnits`/`ListRecentPartUnits`, is unchanged —
  it already renders whatever rows exist in `unit`, so this is confirming the mint, not new UI
  logic). Suggested manual check: create a part with `tracking_mode=serial` and no BOM components,
  a form with one step, a record with a new serial, save without checking "Build at test time" or
  picking a lot/build, then open Part → Units and confirm the unit is listed with no lot/build
  column populated.
- No other manual-only surface — this is pure backend logic with full integration-test coverage
  above.

## Fixture ID / seed note

No `SQL/seed_test_data.sql` changes. No human reseed step. All new fixtures are the existing
throwaway-row pattern (`repPart`, `recForm`, `recRecord`, `repExec`/`repInsert` with registered
cleanup) already used throughout `arx_go/*_integration_test.go`; seeded ArxDev rows (3005, 3012,
3013, 8201-8203, 8301-8306, 8503, etc.) are read-only reference fixtures for the one added
`inventory_integration_test.go` test (#6 above), consistent with how the rest of that file already
uses them.
