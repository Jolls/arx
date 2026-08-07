# 877 — Fix seed record 7013's serial_number to match unit 8501

## Problem
`form_record` row 7013 has `serial_number = '7013'`, but it points at `unit_id = 8501`,
whose `serial_number = 'SN-3013-001'`. Record 7014 (the retest of the same unit) is seeded
correctly with `serial_number = 'SN-3013-001'`, and its seed comment calls this out
explicitly. 7013 is an oversight.

## Scope
Seed-data-only fix. Do NOT touch `arx_go/records.go`'s `upsertUnitForRecord` (that's #876,
a separate plan applied after this one).

## Files to change

### 1. `SQL/azure/seed_test_data.sql`, line 588

Old:
```sql
        (7013, 6001, 3013, '2026-05-31', '7013', 'ASM-1003', 'Skyrunner Deluxe Drone', '6101,6102,6103,6104', 'New Release', 'ModelA', 0, 0, 1, 1, 8306, 8203, 8501, '2020-01-01T00:00:00', '2026-05-31T00:00:00', NULL), -- WIP, Final Test on the top-level assembly: lot 8306 + build 8203 (#737) — completes the receipt(7012)→build(8202)→build(8203)→final-test flow (plan §0); unit 8501 under test (#742, Q8)
```

New (only the 5th value, `'7013'` → `'SN-3013-001'`, changes; everything else identical
including the trailing comment):
```sql
        (7013, 6001, 3013, '2026-05-31', 'SN-3013-001', 'ASM-1003', 'Skyrunner Deluxe Drone', '6101,6102,6103,6104', 'New Release', 'ModelA', 0, 0, 1, 1, 8306, 8203, 8501, '2020-01-01T00:00:00', '2026-05-31T00:00:00', NULL), -- WIP, Final Test on the top-level assembly: lot 8306 + build 8203 (#737) — completes the receipt(7012)→build(8202)→build(8203)→final-test flow (plan §0); unit 8501 under test (#742, Q8)
```

Locate via: search for `(7013, 6001, 3013,` inside the `form_record` `INSERT` block (between
the `SET IDENTITY_INSERT dbo.form_record ON;` above and `SET IDENTITY_INSERT dbo.form_record
OFF;` on the line right after 7014, currently line 594). Row 7012 is directly above it, row
7014 (retest, with its 3-line comment block) directly below.

### 2. `SQL/postgres/seed_test_data.sql`, line 534

Old:
```sql
        (7013, 6001, 3013, '2026-05-31', '7013', 'ASM-1003', 'Skyrunner Deluxe Drone', '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, 8306, 8203, 8501, '2020-01-01T00:00:00', '2026-05-31T00:00:00', NULL), -- WIP, Final Test on the top-level assembly: lot 8306 + build 8203 (#737) — completes the receipt(7012)→build(8202)→build(8203)→final-test flow (plan §0); unit 8501 under test (#742, Q8)
```

New (same single-value change, `'7013'` → `'SN-3013-001'`; only difference from the Azure
row is the pre-existing `TRUE`/`FALSE` vs `1`/`0` boolean literals, already accounted for):
```sql
        (7013, 6001, 3013, '2026-05-31', 'SN-3013-001', 'ASM-1003', 'Skyrunner Deluxe Drone', '6101,6102,6103,6104', 'New Release', 'ModelA', FALSE, FALSE, TRUE, 1, 8306, 8203, 8501, '2020-01-01T00:00:00', '2026-05-31T00:00:00', NULL), -- WIP, Final Test on the top-level assembly: lot 8306 + build 8203 (#737) — completes the receipt(7012)→build(8202)→build(8203)→final-test flow (plan §0); unit 8501 under test (#742, Q8)
```

Locate via: search for `(7013, 6001, 3013,` inside the `form_record` block (row 7012 directly
above, the 7014 retest with its comment block directly below, `SET IDENTITY_INSERT` /
sequence-restart handling immediately after — mirrors the Azure file's structure).

## What NOT to change (verified by repo-wide grep for literal `7013`)

Grepped the whole repo (`arx_go`, `docs`, `SQL`) for the literal string `7013`. Every other
hit refers to **record ID 7013**, never to the string `'7013'` used as its serial number
value — nothing pins the old (buggy) serial literal:

- `SQL/SCHEMA.md` lines 84–85, 93 — table-reference prose describing record/result ID ranges
  and "locked record 7013 points at [unit 8501]"; no serial value quoted, no change needed.
- `arx_go/integration_test.go` line 2531 and lines 2527–2532 (comment above
  `TestIntegration_UnitSerialLocked`) — refers to "seed records 7013/7014" by ID only; the
  test itself inserts its own temporary record with a `smokeUniq("SN-LOCK")` serial and does
  not touch 7013/7014's rows or assert on their serial values.
- `docs/plans/745-slice8-create-render-logic.md` line 23, `docs/plans/799-create-unit-...md`
  lines 171, 189 — reference record/unit IDs, not the serial literal.

No code, test, or doc needs updating beyond the two SQL literal changes above.

## Verification
1. Diff both files after edit: only the one field (`'7013'` → `'SN-3013-001'`) changes per
   file, nothing else on the line or elsewhere moves.
2. `cd arx_go; go build ./...`, `go vet ./...`, `go test ./...` (seed files aren't compiled,
   so this just confirms the surgical edit didn't touch any Go code).
3. Reseeding ArxDev and running integration tests is a human action (per CLAUDE.md) — not
   required to validate this plan's edits, but note for the human: after reseeding, record
   7013's serial should now read `SN-3013-001` on its record page, matching unit 8501 and
   record 7014.

## Open questions
No open questions.
