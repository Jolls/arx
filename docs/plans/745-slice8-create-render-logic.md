# Slice 8 (#745) — create/render logic (batch-vs-unit, completeness, retest)

Part of epic #736. Pure code, no migration. Depends on slices 3–7 (all merged).

## Resolved decisions (user, this session)

- **Q1 → Re-enable the trace panel.** Uncomment the existing trace/linkage dropdowns on `record_edit.html` (62–100) so serial/lot_serial records capture lot/build; unit provenance = `build_id` (preferred) else `lot_id`. (Also pre-answers slice 10 OQ3.)
- **Q2 → Builds only.** Completeness "Tested N / qty" on the Build history table using `build.qty`. Defer lot-level completeness with a TODO comment (no lot-qty denominator exists).
- **Q3 → NULL write-shape.** On new/updated unit-level saves set `unit_id`, leave `lot_id`/`build_id` NULL; read lot/build through the unit for display. Leave existing seed rows as-is.
- **Q4 → 4-value dropdown.** Replace the `part_edit.html` checkbox with a none/lot/serial/lot_serial dropdown writing `tracking_mode`; keep `is_lot_tracked` in sync (`= mode in {lot,lot_serial}`).
- **Q5 → keep IsLotTracked derived + add TrackingMode.** Populate existing `IsLotTracked bool` by deriving from `tracking_mode`; add `TrackingMode string` for serial/unit branches. Zero template churn on the lot path.
- **Q6 → keep is_lot_tracked + deprecation comment.** Do not drop; add a one-line deprecation comment at `models/part.go:25` and `SQL/part.sql:60`.

## Original open questions (superseded by the decisions above)

1. **Provenance source at unit-creation time.** The record→lot/build linkage picker in `record_edit.html` (lines 62–100) is entirely commented out (hidden since 0.6.0, deferred to v0.7.0). A unit-level `form_record` has no in-UI way to supply the lot/build a new `unit` needs to satisfy `CK_unit_provenance`.
   - **Recommendation:** #745 re-enables *only* that already-written trace panel (the `RecordTrace` code in `records.go` still runs; just uncomment the template block) so serial/lot_serial parts can capture lot/build; unit provenance is taken from the record's chosen `build_id` (preferred) else `lot_id`. Do NOT build the embedded build-at-test-time UX (that's slice 10 / #747).
   - **Fallback:** create units only when a lot/build is already linked, skip creation otherwise.

2. **Completeness denominator for lots.** `build.qty` exists (`SQL/build.sql:16`), but `lot` has NO qty column — lot quantity would have to come from inventory/PO (non-trivial).
   - **Recommendation:** scope #745 completeness to **builds only** — a "Tested N / qty" column in the Build history table (`part_build.html` ~114–138, `BuildView` in `build.go`). Defer lot-level completeness with a noted TODO.

3. **Q8 write shape: NULL vs denormalized-equal.** Issue point 6 says unit-level records should leave `form_record.lot_id`/`build_id` NULL; the seed (rows 7011–7013) instead sets them equal to the unit's (also permitted by §6 Q8).
   - **Recommendation:** on new/updated unit-level saves, set `unit_id` and leave `lot_id`/`build_id` NULL; read lot/build through the unit for display. Leave existing seed rows as-is.

4. **`part_edit` tracking control.** Today: single "Batch / lot controlled" checkbox writing `is_lot_tracked` (`part_edit.html:82–84`; `parts.go:504,610`). Serial/lot_serial can't be set in-app otherwise.
   - **Recommendation:** replace checkbox with a 4-value `tracking_mode` dropdown (none/lot/serial/lot_serial) writing `tracking_mode` directly, keep `is_lot_tracked` in sync (`= mode in {lot,lot_serial}`) for back-compat.
   - **Fallback:** keep checkbox (lot vs none only); serial parts need manual DB update.

5. **Read-swap minimization strategy.**
   - **Recommendation:** keep existing `IsLotTracked bool` fields but populate them by reading `tracking_mode` and deriving (`mode == "lot" || mode == "lot_serial"`); ADD a `TrackingMode string` field for new serial/unit branches. Zero template churn for the lot path.

6. **`is_lot_tracked` becomes dead-ish after the swap.** §5 step 4 does not call for dropping it.
   - **Recommendation:** keep column + model derivation; add a one-line deprecation comment at `models/part.go:25` and `SQL/part.sql:60`. Do not drop (out of scope, no migration).

**Vestige check:** no `is_batch` flag/column exists anywhere in code or DDL — nothing to remove.

## File-by-file plan

### 1. `arx_go/models/part.go`
- Add `TrackingMode string` field (near line 25). Add deprecation comment to `IsLotTracked` (line 25).
- Add helpers: `func TracksLots(mode string) bool { return mode == "lot" || mode == "lot_serial" }` and `func TracksSerials(mode string) bool { return mode == "serial" || mode == "lot_serial" }`.
- `ShowLots()` (line 149): leave as-is if `IsLotTracked` stays derived; else `TracksLots(p.TrackingMode)`.

### 2. `arx_go/models/trmodels.go`
- On `TestRecord` (line 60–77): add `UnitID *int` and `UnitSerial string`. Add `func (r TestRecord) IsUnitLevel() bool { return r.UnitID != nil }` — structural unit-vs-batch classifier.

### 3. `arx_go/parts.go` (read-swap + tracking_mode load/save)
- `fetchPartBasic` select (35) + assign (44): add `tracking_mode`; derive `IsLotTracked`, set `TrackingMode`.
- `fetchPartFull` select (242) + assign (281): same.
- Second fetch select (696) + assign (715): same.
- Insert (487–505): add `tracking_mode` column + value; keep `is_lot_tracked` in sync.
- Update (594–612): add `tracking_mode=@pN`; keep `is_lot_tracked` in sync.
- `partFromForm` (661): set `TrackingMode` from form; derive `IsLotTracked`.

### 4. `arx_go/build.go` (read-swap + completeness)
- Component field (94), select (105), assign (126), branches (135, 197), output flag (225), second select (252)/assign (269): read `tracking_mode`, derive via `TracksLots`.
- `PartBuild` (154–210) + `BuildView`: add completeness subquery `COUNT(u.id) FROM unit u WHERE u.build_id = build.id`; add `TestedCount int` to `BuildView`; query via `h.cfg.UnitTable()`.

### 5. `arx_go/inventory.go`, `arx_go/pos.go`, `arx_go/models/purchase_order.go`
- Read-swap sites (`inventory.go:122,183`; `pos.go:1588,1914,1943`; `purchase_order.go:65`) to derive from `tracking_mode`. Behavior-preserving.

### 6. `arx_go/records.go` (core behavior)
- `RecordTrace` (1622) + `loadRecordTrace` (1637): select `tracking_mode` (1655), derive `IsLotTracked` (1664), add `TracksSerials` flag; branch at 1683 unchanged for lots. Load `unit_id`/unit serial.
- Record SELECTs (1181, 1320, 1761, 2016, 2274, 2497, 3063): add `unit_id`, scan into `TestRecord.UnitID`.
- `CreateRecord` (1462–1577): after insert, if tested part `TracksSerials`, lazily upsert a `unit` (SELECT by `part_id`+`serial_number`, INSERT if absent with provenance `build_id` else `lot_id`, honoring `CK_unit_provenance`), then set `form_record.unit_id`.
- `UpdateRecord` (2442–2477): before UPDATE, compute unit linkage; for unit-level parts set `unit_id`, pass NULL for `lot_id`/`build_id` (Q8, open q3). Add `unit_id` to SET.
- `DuplicateRecord` (retest, 2003–2067): add `unit_id` to source SELECT (2016) and INSERT (2056–2063) so retest carries the SAME `unit_id` — do NOT create a new unit. Comment marks retest→same-unit path.
- Add helper `func (h *Handler) upsertUnitForRecord(ctx, tx, partID int, serial string, buildID, lotID *int) (int, error)` centralizing lazy creation + provenance-invariant assignment.

### 7. `arx_go/lot.go`
- Update slice-8 comment (205–208): genealogy unit-endpoint generalization is #746's job (slice 9), not #745. No walk change here.

### 8. Templates
- `parts/part_build.html`: add "Tested" column to Build history (118–133) rendering `{{.TestedCount}} / {{printf "%g" .Qty}}`; read-swap `IsLotTracked` badges (26,66,75,110) — unchanged if `IsLotTracked` stays derived.
- `parts/part_edit.html` (82–84): per open q4, tracking_mode dropdown.
- `records/record_edit.html` (62–100): per open q1, re-enable trace/linkage panel (or leave commented if scoped out).
- `parts/part_transactions.html:33`, `pos/po_detail.html:248`: no change if `IsLotTracked` stays derived.

### 9. `SQL/seed_test_data.sql`
- Add at least one `serial` and one `lot_serial` part (current seed only has `lot`, 412–416).
- Add a retest pair: two `form_record` rows sharing one `unit_id`.
- Optionally a build-sourced serial unit whose `build_id` lets a build's "Tested N/qty" show N>0.

### 10. `arx_go/integration_test.go` (`//go:build integration`)
- Cases: (a) tracking_mode read-swap branching; (b) lazy unit creation on first serial test + no duplicate on retest (same `unit_id`); (c) build completeness count; (d) Q8 FK-consistency (unit-level record leaves lot_id/build_id NULL). Reuse seed part ids at 1540–1541, 1684.
