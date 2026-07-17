# Hide test-record ↔ lot/build linkage for 0.6.0

**Issues:** #677, #687 (lot/build linkage), deferred completion under #702 (v0.7.0 Lot & Serial epic)

## Goal

Ship 0.6.0 with the test-record → lot/build linkage **hidden** (it isn't finished),
while keeping the parts of lot control that work: PO-line lot creation at receipt,
the `/lots` list, and lot editing/genealogy (#701, #687). No schema change — hide UI
only, so v0.7 resumes cleanly.

## The non-obvious trap

There is a completion gate: `errRecordNeedsLot` in `completeRecordTx`, hit from
`LockRecord` (records.go:~1810-1814), **blocks marking a lot-tracked record Complete
unless a lot is selected**. Hide the picker without touching this and lot-tracked
parts can't be locked → soft-brick. So the plan is two template hides **plus** one
unavoidable handler tweak.

## Changes

**Hide (UI, comment out):**

1. `record_edit.html:62–95` — the whole `{{if .Trace}}` block: the **Lot** dropdown,
   **Build** dropdown, and **"Build this unit"** button. This is the entire linkage
   input surface on the record editor. (The `data-build-this-unit` draft-save JS at
   ~304 goes dead but harmless — leave it.)
2. `records_show.html:116–126` — the **Lot** and **Build** display rows on the
   record view.

`record_new.html` and `record_print.html` have no lot/build UI — nothing to touch.

**Handler (required, not UI):**

3. Disable the `errRecordNeedsLot` gate in `completeRecordTx` (short-circuit the
   "lot-tracked record needs a lot before Complete" check). Without the picker there's
   no way to satisfy it, so it must come out or lot-tracked records can't be locked.
   One small guarded edit.

## Deliberately keep (untouched)

- PO-line lot creation at receipt.
- Lot editing, `/lots` list, lot detail + genealogy (#701, #687).
- Lots subtab on the part page.
- **`test_record.lot_id` / `build_id` columns and all data** — no migration, nothing
  destructive. Hiding UI, not removing the model, so v0.7 resumes cleanly.

## Verify

- Grep tests for `errRecordNeedsLot` and the lot picker (e.g. `helpers_tr_test.go`) —
  the gate likely has a unit test needing the same short-circuit or a skip.
- `go build/vet/test`.
- Manual: confirm a lot-tracked part's record still completes with the picker gone.

## Version decision

Ship 0.6.0 with this hidden — correct call. "Feature isn't ready, ship the rest behind
a hide" is standard. Nothing is deleted (columns, receipt-side lot control, lot editing
all stay), so 0.6.0 is coherent. The linkage gets finished **properly** under v0.7.0
(#702), where the `is_batch`/`trace_id`/batch-qty questions are resolved — rather than
shipping the half-built version and reworking around it.

Footprint: **2 template blocks commented, 1 handler gate disabled.**
