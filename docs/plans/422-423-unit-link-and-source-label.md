# Plan: #422 + #423 — link records to existing units; "test record" source label

One PR. No schema change, no migration, no seed change.

## #422 — file changes

### 1. `internal/inventory/inventory.sql`

Replace the `UpsertTestUnit` query (currently ~line 221) with:

```sql
-- name: UpsertTestUnit :one
-- The conflict branch fills lot_id/build_id only where the existing unit has none (COALESCE keeps existing
-- provenance) and its no-op serial_number assignment makes RETURNING yield the existing id.
INSERT INTO unit (part_id, serial_number, build_id, lot_id, source)
VALUES (sqlc.arg(part_id), sqlc.arg(serial_number), sqlc.narg(build_id), sqlc.narg(lot_id), 'test')
ON CONFLICT (part_id, serial_number) DO UPDATE SET
    serial_number = EXCLUDED.serial_number,
    lot_id = COALESCE(unit.lot_id, EXCLUDED.lot_id),
    build_id = COALESCE(unit.build_id, EXCLUDED.build_id)
RETURNING id;
```

`source` is NOT touched on conflict (a manual unit stays `manual`).

Add directly after it:

```sql
-- name: FindUnitIDBySerial :one
SELECT id FROM unit WHERE part_id = sqlc.arg(part_id) AND serial_number = sqlc.arg(serial_number);
```

### 2. `internal/dbq`
Run `sqlc generate` (repo root); commit the regenerated `internal/dbq`. Never hand-edit.

### 3. `internal/inventory/inventory.go`
- Update the `UpsertTestUnit` doc comment (~line 551-553): provenance is set on creation; on an existing unit, only a NULL lot/build is filled, existing values are never overwritten.
- Add after `UpsertTestUnit`:

```go
// FindUnitIDBySerial returns the id of the existing unit for (partID, serial), or nil when there is none.
func (s *Service) FindUnitIDBySerial(ctx context.Context, partID int, serial string) (*int, error) {
	id, err := s.q.FindUnitIDBySerial(ctx, dbq.FindUnitIDBySerialParams{PartID: partID, SerialNumber: serial})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}
```
(`errors`/`sql` already imported — used by `UnitProvenance`.)

### 4. `arx_go/records.go` — `SaveResults`, the `else if` at ~line 2171
Replace the single `else if ... (buildArg != nil || lotArg != nil) { ... }` branch with:

```go
} else if models.TracksSerials(trackingMode) && record.SerialNumber != "" {
	// (comment) With a picked lot/build: find-or-create the unit (#745, Q5), filling a NULL lot/build on an
	// existing unit (#422). With neither: link to an existing (part, serial) unit only — never mint a unit
	// without provenance (app rule, not a DB CHECK: #799 dropped CK_unit_provenance for manual units).
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
```
Keep the existing `if unitArg != nil { lotArg, buildArg = nil, nil }` block untouched. The `record.UnitID != nil` first branch is untouched.

## #423 — file changes

### 5. `arx_go/templates/parts/part_units.html` line 36
`{{else}}test{{end}}` -> `{{else}}<span class="badge bg-secondary">test record</span>{{end}}`.

### 6. `arx_go/templates/parts/part_unit_trace.html` line 27
`{{else}}test{{end}}` -> `{{else}}<span class="badge bg-secondary">test record</span> — created by saving a test record{{end}}`.

### 7. `CHANGELOG.md`
New top `## [x.y.z]` entry (next patch of the current top version, today's date). Under `### Changed`: source shown as a `test record` badge on part Units tab and unit trace page ([#423](https://github.com/Jolls/arx/issues/423)). Under `### Fixed`: saving a test record now links to an existing unit when no lot/build is picked, and fills a manual unit's blank lot/build ([#422](https://github.com/Jolls/arx/issues/422)). Not a RELEASE_NOTES change unless the user asks.

## Test plan

### (1) Coverage audit
- `UpsertTestUnit` (integration, `arx_go/inventory_integration_test.go`): `TestIntegration_InventoryUpsertUnitForRecord`, `..._ExistingSeedSerial`, `..._SamePartSerialDifferentPart`, `..._Concurrent`, `..._ConcurrentProvenanceKept`.
- `SaveResults` unit linking (`arx_go/integration_test.go`): `TestIntegration_SerialUnitCreationAndRetest` (mint with lot+build, retest relinks), `TestIntegration_SaveDoesNotDuplicateUnitOnSerialMismatch` (record.UnitID reuse #876). Other SaveResults tests (`records_writes_integration_test.go`, `tx_boundaries_integration_test.go`) use no serial-tracked part.
- Linkage validation: `arx_go/inventory_integration_test.go` (the `args(...)` test ~line 440, lot/build ownership).
- Templates: `TestFilterTable_ExistingTablesStructure` (`arx_go/filter_table_char_test.go`) renders `part_unit_trace.html` with `Unit: UnitRow{}` but pins only the `<table data-rows-url>` golden, not the Source row. Nothing renders `part_units.html` or asserts the Source cell. `UnitRow.IsManual` is exercised only in `inventory_integration_test.go:277-280`.

### (2) Characterization tests (pass on unchanged code)
Add to `arx_go/inventory_integration_test.go`:
- `TestIntegration_InventoryUpsertTestUnit_KeepsExistingProvenance`: in `invTx`, upsert a fresh serial on part 3005 with build 8201, then upsert same serial with build 8202; assert unit's build_id is still 8201 and source `test`. (Pins "never overwrite"; passes today and after.)
- `TestIntegration_SaveResults_NoSerialNoUnitMinted` is NOT needed; covered by the red test below for the "no unit created" case, which passes today and after (see (3) case D).

Add to `arx_go/templates_parse_test.go` (or new `arx_go/unit_source_label_test.go`, package main), pattern of `TestContactDetailTemplateRenders` (`template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS, "templates/shared/layout.html", "templates/shared/partials.html", "templates/parts/part_units.html")`, `renderToString(t, tmpl, "layout", coreLayoutFakeData(...))`):
- `TestPartUnitsTemplate_ManualBadgeUnchanged`: Units contains one `UnitRow{Source: "manual", ...}`; assert output contains `<span class="badge bg-secondary">manual</span>`. Data keys needed: `ActiveTab: "parts"`, `Part`, `Units`, `UserLoc: time.UTC`, plus whatever `part_units.html` requires (read the file's top 20 lines and the `h.render` call at `arx_go/unit.go:60` to supply them).
- Same for `part_unit_trace.html`: assert manual output contains `manual</span> — added directly, no test record`. Data as in `filter_table_char_test.go:43` plus `Unit: UnitRow{Source: "manual", ...}`.

### (3) Red tests (fail today, pass after)
Integration tests in `arx_go/inventory_integration_test.go` (service level, `invTx`, rolled back):
- `TestIntegration_InventoryUpsertTestUnit_FillsNullProvenanceOnManualUnit`: `CreateManualUnit(3005, uniq, nil, nil)`, then `UpsertTestUnit(3005, same serial, &8201, nil)`; assert same id, build_id=8201, source still `manual`. Fails today: conflict branch writes nothing.
- `TestIntegration_InventoryUpsertTestUnit_FillsOnlyNullSide`: manual unit created with build 8201 and nil lot; upsert with build 8202 and lot 8301... use a lot that belongs to part 3005 per seed (look up from `SQL/seed_test_data.sql`; 8301 belongs to 3007 so it can't be used on 3005 — pick a 3005 lot, or use part 3013 with lot 8306/build 8203 and a second build of 3013 if one exists). Assert build unchanged, lot filled. Fails today.
- `TestIntegration_InventoryFindUnitIDBySerial`: seed serial `SN-3005-001` on 3005 returns 8503; unknown serial returns nil; same serial on part 3012 returns nil. Fails today (method doesn't exist: compile failure is the red state).

Handler-level, add to `arx_go/integration_test.go` next to `TestIntegration_SerialUnitCreationAndRetest`, same helpers (`mkRecord` pattern: insert `form_record` with form 6001, part 3005 serial `smokeUniq(...)`, subject `ASM-1001`; `withID(postForm(...))`; `h.SaveResults`; delete records before units in cleanup):
- A `TestIntegration_SaveResults_FillsManualUnitProvenance`: insert manual unit (3005, serial S, no lot/build) via `INSERT INTO unit (part_id, serial_number, source) VALUES (3005,$1,'manual')`; record with serial S; save with `build_id=8201`. Assert record.unit_id = that unit, record.build_id/lot_id NULL (Q8), unit.build_id = 8201, unit.source = `manual`. Fails today: provenance discarded.
- B `TestIntegration_SaveResults_KeepsExistingUnitProvenance`: unit (3005, S, build 8201, source `test`); record serial S; save with `build_id=8201` only a different valid pick is not possible for 3005 if only one build exists — if seed has no second build of 3005, assert with the same-build pick that unit.build_id stays 8201 and no second unit exists (passes today and after; characterization). 
- C `TestIntegration_SaveResults_LinksExistingUnitWithoutPick`: unit (3005, S, build 8201, `test`); record serial S; save with empty form. Assert record.unit_id = unit id; unit provenance unchanged. Fails today: no link when nothing picked.
- D `TestIntegration_SaveResults_NoUnitWithoutProvenance`: record serial S, no existing unit, empty form. Assert record.unit_id NULL and `COUNT(*) FROM unit WHERE part_id=3005 AND serial_number=S` = 0. Passes today (guards the Q5 rule; characterization).

Template tests (same file as the manual-badge ones in (2)):
- `TestPartUnitsTemplate_TestRecordBadge`: Units has `UnitRow{Source: "test"}`; assert output contains `<span class="badge bg-secondary">test record</span>`. Fails today: plain `test`.
- `TestPartUnitTraceTemplate_TestRecordBadge`: `Unit: UnitRow{Source: "test"}`; assert contains `test record</span> — created by saving a test record`. Fails today.

New seed rows: none (all tests create and delete their own rows or roll back; manual unit 8504 is not mutated). No reseed.

### (4) Manual-only
- Units tab and unit trace page: test-sourced unit shows grey `test record` badge; manual unit unchanged.
- Part with serial tracking and a hand-added unit: save a record for that serial with a lot/build picked; unit's Lot/Build columns fill in.
- Save a record for an existing serial with nothing picked; record links to the unit (unit trace shows the record).

## Run
`go build ./... && go vet ./... && go test ./...`; integration tests against the local container per CLAUDE.md (`-tags integration ./arx_go/...`), since SQL and `SaveResults` are touched.

## Open questions
1. Handler tests B and the second red test need a second valid build/lot for the same part to prove "not overwritten" through the handler. Seed has only build 8201 for part 3005 and lot 8306/build 8203 for 3013 (verify in `SQL/seed_test_data.sql`). If no second valid build/lot exists, may I assert non-overwrite at the service level only (the `KeepsExistingProvenance` test) and use the same-pick form for handler test B? Or add a seed row (requires human reseed; part 3005 sentinel unchanged)?
2. When a hand-added unit is filled by a record's pick, should `source` stay `manual` (plan assumes yes; issue says nothing about changing it)?

## Resolved decisions (override anything above that conflicts)
1. **Seed row (second build for 3005).** In `SQL/seed_test_data.sql`'s `INSERT INTO build` block (~line 477) add `(8204, 3005, NULL, 1, '2026-06-01', 'tester', 'Second build of assembly 3005 (#422 non-overwrite test)')`. 8204 is inside the existing `trace_id_seq` range (setval 8599): no setval change. Before adding, grep tests and `SQL/SCHEMA.md` for count assertions on part 3005's builds / build history and fix or flag any that would break; sentinel part 3005 / ASM-1001 / Skyrunner is untouched. Handler test B and `FillsOnlyNullSide` then use builds 8201 and 8204 (both part 3005) instead of the same-pick workaround. **Human step: reseed ArxDev** (or `docker compose -f SQL/compose.yml restart db`); Claude never reseeds ArxDev. "New seed rows: none" in the Test plan is superseded.
2. **Source flips to `test`.** In the `UpsertTestUnit` conflict branch also set `source = 'test'` (a hand-added unit that a test record supplies lot/build for becomes `test`). Applies only to the upsert path (a pick was supplied); the no-pick `FindUnitIDBySerial` link does not change `source`. Replace "`source` is NOT touched on conflict" in file change 1 accordingly. Verify the `unit.source` CHECK and any provenance constraint (CK_unit_provenance was dropped for manual units, #799) allow the result; if the conflict UPDATE could violate one, stop and ask. Update tests: `FillsNullProvenanceOnManualUnit` and handler test A assert `source = 'test'` after the save (not `manual`); `KeepsExistingProvenance` unchanged. Add to the CHANGELOG line that a manual unit becomes `test` when a record fills its provenance.
3. Open questions 1 and 2 above are closed by decisions 1 and 2.
