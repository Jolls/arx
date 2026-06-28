# Fix per-form serial-number race in record creation (#369)

## Problem

Test-record creation assigns a serial number (SN) using
`MAX(TRY_CAST(serial_number AS INT)) + 1` scoped per `form_id`. The value is
computed at **GET** time in `RecordNew` and pre-filled into an editable text
field on the New Record form. Two users (or two parallel API calls) who open the
form before either submits both see the same suggested SN and both submit it,
producing two records with the same serial number. Low probability in
single-user systray usage; real if a script drives creation in parallel.

## Constraints (current behavior to preserve)

- Serial numbers are **per-form**, stored as `VARCHAR(64)`, and the New Record
  field is a user-editable `required` text input. Manual/custom SNs are
  supported.
- `DuplicateRecord` deliberately **reuses** the source record's SN, so SNs are
  *not* unique per form. A plain `UNIQUE` constraint is therefore not viable.
- Decision (confirmed): the SN field is **auto unless overridden** — normally the
  system assigns the next sequential per-form SN; the field exists for the
  occasional manual override.

## Approach (chosen: A — in-transaction recompute with lock hints)

Make the auto path re-derive and allocate the SN **atomically inside the insert
transaction**, instead of trusting a value computed at GET time. A counter table
(FUTURE_GOALS.md suggestion) was rejected: it requires a schema migration plus
seeding and reconciliation against manual overrides and duplicate-record SN
reuse, for the same guarantee.

### 1. Template — `arx_go/templates/tr/record_new.html`

Add a hidden field carrying the GET-time suggestion next to the existing visible
input:

```html
<input type="hidden" name="suggested_serial_number" value="{{.NextSN}}">
```

### 2. Handler — `CreateRecord` (`arx_go/records.go`)

- Read and trim both `serial_number` (visible) and `suggested_serial_number`
  (hidden).
- `auto := serialNumber == suggested` — the user accepted the default.
- If `auto`: inside the existing `tx`, re-derive the next SN atomically before
  the insert:

  ```sql
  SELECT COALESCE(MAX(TRY_CAST(serial_number AS INT)), 0) + 1
  FROM <RecordsTable> WITH (UPDLOCK, HOLDLOCK)
  WHERE form_id = @p1
  ```

  `UPDLOCK, HOLDLOCK` holds the lock until the transaction commits, so a
  concurrent same-form create blocks and then reads the committed max — the two
  creates receive N and N+1. Convert the int result to the SN string used in the
  insert.
- If not `auto`: insert the user's typed `serial_number` unchanged (current
  behavior).
- Everything else (BOM PN resolution, `materializeRecordSteps`, commit,
  redirect) is unchanged.

### Edge case (confirmed acceptable)

If a user deliberately types a value equal to the suggestion, it is treated as
auto and recomputed. The recomputed value equals what they intended, or
correctly bumps past a record that landed concurrently. No extra UI signal.

## Not touched

- `RecordNew` (GET) keeps computing the suggestion; it is now only a hint.
- `DuplicateRecord` — unchanged; reuses source SN, never auto-allocates.
- No schema change, no new table, no test-mode / DDL / schema.md ripple.

## Cleanup

- Update the comment at the SN-suggestion site in `records.go` to note that
  allocation is now atomic at create time.
- Strike through the FUTURE_GOALS.md "Serial number sequence" bullet (completed),
  per the repo convention of preserving completed-work history.

## Testing

- Extract the auto-vs-override decision into a small pure helper and unit-test it
  (exact match → auto; differing value → override; whitespace trimming; empty
  override). Lives alongside existing `arx_go/helpers_tr_test.go` tests.
- The locking guarantee requires a live DB; an optional build-tagged integration
  test driving concurrent creates against ArxDev is noted but not written here.
