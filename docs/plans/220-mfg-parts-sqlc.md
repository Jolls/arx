# #220 (slice): convert mfg_parts to sqlc

Part of #220 / #190. Stacked on PR #227 (part categories). Scope: `arx_go/mfg_parts.go` only
(tables `mfg_part`, `company`).

## Changes

1. `internal/parts/parts.sql` — append:
   - `ListMfgParts :many` — `mp.id, mp.part_id, mp.mfg_id, mp.mfg_part_number, COALESCE(mp.description, '') AS description, mp.is_active, c.name AS mfg_name` from `mfg_part mp JOIN company c ON mp.mfg_id = c.id WHERE mp.part_id = $1 AND mp.is_active = TRUE ORDER BY c.name, mp.mfg_part_number`.
   - `GetMfgPart :one` — `id, part_id, mfg_id, mfg_part_number, COALESCE(description, '') AS description` `WHERE id = sqlc.arg(id) AND part_id = sqlc.arg(part_id) AND is_active = TRUE`.
   - `CreateMfgPart :exec` — `INSERT (part_id, mfg_id, mfg_part_number, description, is_active) VALUES (..., sqlc.arg(description)::text, TRUE)`.
   - `UpdateMfgPart :exec` — `SET mfg_id, mfg_part_number, description = sqlc.arg(description)::text WHERE id AND part_id AND is_active = TRUE`.
   - `DeleteMfgPart :exec` — `SET is_active = FALSE WHERE id AND part_id`.
   - `ListManufacturers :many` — `id, name FROM company WHERE is_manufacturer = TRUE AND is_active = TRUE ORDER BY name`.
   Then `sqlc generate`.
2. `internal/parts/parts.go` — types `MfgPart` (same fields/order as `models.MfgPart`, so the
   `ListMfgParts` row converts with `MfgPart(r)`) and `Manufacturer{ID int; Name string}`; methods
   `ListMfgParts(ctx, partID int)`, `GetMfgPart(ctx, id, partID int)` (passes `sql.ErrNoRows`
   through), `CreateMfgPart(ctx, MfgPart)`, `UpdateMfgPart(ctx, MfgPart)`,
   `DeleteMfgPart(ctx, id, partID int)`, `ListManufacturers(ctx)`. Update the package doc line.
3. `arx_go/mfg_parts.go` — handlers call `h.parts()`; no `fmt.Sprintf` SQL / `*Table()` / raw calls.
   - Part id: use `p.ID` from `partPageBase`/`requireTab` (already validated there).
   - `fetchMfgParts(r, partID string)` keeps its signature (attachments.go calls it); `strconv.Atoi`
     inside, returns the error. Returns `[]parts.MfgPart`.
   - `fetchManufacturers(r)` stays as a thin wrapper returning `[]parts.Manufacturer`;
     delete `manufacturerOption`.
   - `mfg_id` form value: `strconv.Atoi`; a parse error goes down the same error branch the DB
     error used ("Error adding manufacturer part: …" / "Error updating manufacturer part: …").
   - `mid` URL param: `strconv.Atoi`; parse error → `renderError("Manufacturer part not found")`
     in Edit/Update/Delete (contacts precedent, #190).
4. `arx_go/sourcing.go` — 3× `var manufacturers []manufacturerOption` → `[]parts.Manufacturer`
   (+ import).
5. `arx_go/models/supplier.go` — delete `MfgPart` (orphaned).
6. `arx_go/sqlc_converted_lint_test.go` — add `"mfg_parts.go"`.
7. Keep `MfgPartTable()`/`CompanyTable()` — still called from attachments/parts/settings/sourcing.
8. CHANGELOG `0.8.9`.

## Resolved decisions
- Non-numeric `mid`: "Manufacturer part not found" (was a raw Postgres error). Matches contacts.
- Non-numeric `mfg_id` form value: same error message prefix as before, Atoi error text instead of
  the Postgres one.

## Test plan
1. **Coverage audit** (`arx_go/mfg_parts_integration_test.go`): `PartMfgParts` (active filter,
   manufacturer filter), `MfgPartEdit` (+ NotFound, WrongPartScope), `MfgPartUpdate`
   (+ MissingRequired, WrongPartScope silent no-op), `MfgPartDelete` (sibling unaffected).
   `MfgPartCreate` only exercised via `seedMfgPart` (status + MAX(id)).
2. **Characterization** (integration, pass on unchanged code):
   - `TestIntegration_MfgPartCreate` — trimmed MPN/description stored, part_id/mfg_id right, is_active.
   - `TestIntegration_MfgPartCreate_EmptyDescription` — description stored as `''`, not NULL.
   - `TestIntegration_MfgPartCreate_MissingRequired` — re-render with message, no row.
   - `TestIntegration_MfgPartCreate_Duplicate` — second identical create re-renders
     "Error adding manufacturer part", one active row.
   - `TestIntegration_MfgPartDelete_WrongPartScope` — silent no-op, row stays active.
3. **Red**: `TestSQLCConvertedFilesHaveNoRawSQL` with `mfg_parts.go` added (fails on the 6 sites).
4. **Manual-only**: page render/add/edit/delete at `/part/3002/mfg-parts`; attachments vendor-scope
   picker still lists "Mfg:" entries; Sourcing tab DigiKey manufacturer picker (DigiKey enabled only).
