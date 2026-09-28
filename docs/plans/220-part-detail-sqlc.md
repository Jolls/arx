# #220 — Part detail/edit reads and writes to sqlc

Part of #220 / #190. Second slice of the `arx_go/parts.go` catalog: the single-part row.

## Scope
- `fetchPartBasic` (every part sub-tab page, api.go, attachments.go, sourcing.go), the `PartDetail` part
  query plus its `uom` abbreviation lookup, `fetchPartFull` (PartEdit/PartDuplicate), the `PartsCreate`
  INSERT and the `PartUpdate` UPDATE.
- Out of scope, still raw SQL: PartDetail's attachment/price queries, PartDuplicate's BOM count,
  `copyBOM`, and everything BOM/pricing/orders. `parts.go` does not join `convertedFiles`, and no
  `cfg.*Table()` helper loses its last caller.

## Changes
- `internal/parts/parts.sql` (+ `sqlc generate`):
  - `GetPartBasic :one`: the `fetchPartBasic` columns plus `has_bom` (EXISTS on bom) and the thumbnail
    subquery as `COALESCE(..., '')::text`. Nullable text is `COALESCE(... ,'')`.
  - `GetPart :one`: the `PartDetail` columns, with `LEFT JOIN uom` for the abbreviation (replacing the
    second query). NULL text → `''`, NULL `is_active` → `FALSE`, NULL `current_cost`/`last_rollup_cost`
    → `0`, NULL counts → `0`. `reorder_min`, `uom_id`, `primary_attachment_id`, dates stay nullable.
  - `CreatePart :one` (RETURNING id) and `UpdatePart :exec`: `NULLIF(sqlc.arg(category)::text, '')`,
    `sqlc.narg` for `uom_id`/`reorder_min`, `::text`/`::numeric`/`::boolean`/`::date` casts elsewhere.
- `internal/parts/parts.go`: `type PartBasic`, `type Part` (all detail fields, also the write input,
  like `MfgPart`), `GetPartBasic(ctx, id, thumbCategory)`, `GetPart(ctx, id)`,
  `CreatePart(ctx, p, now) (int, error)`, `UpdatePart(ctx, p, now) error` (p.ID is the target).
- `arx_go/parts.go`:
  - `fetchPartBasic` / `fetchPartFull` parse the id (`strconv.Atoi`; a bad id returns that error, as in
    `fetchMfgParts`) and call the service. `sql.ErrNoRows` passes through unwrapped.
  - `fetchPartFull` uses `GetPart`, so the edit/duplicate `models.Part` also carries the detail-only
    fields. Neither `part_edit.html` nor `part_tabs` reads them.
  - `PartDetail` calls `fetchPartFull` instead of its inline query.
  - `PartsCreate`/`PartUpdate` build the input from `partFromForm(r)`. Its parsing is the same as the
    `nullableText`/`nullableInt`/`nullableFloat`/`floatOrZero` calls it replaces.
  - `fetchPartFull` maps `parts.Part` → `models.Part` inline, applying `releaseStatusOrUnderReview` and
    `models.TracksLots` as the old scans did; `partInput(models.Part) parts.Part` is the write-side mapping.

## Test plan
1. Coverage audit: `TestIntegration_PartTrackingModeRoundTrip` (tracking mode via create/update +
   fetchPartBasic), `TestIntegration_PartLifecycle` (create + description update),
   `TestIntegration_RouteRoundTrips` "part detail" (renders ASM-1001). No test pins the other columns.
2. Characterization (`arx_go/part_detail_integration_test.go`), must pass on unchanged code:
   - `PartCreateUpdate_Columns`: PartsCreate with every form field set → every written column read back;
     PartUpdate with blanks (category/unit/reorder_min NULL, cost 0, status U, active) → read back;
     status D → inactive.
   - `FetchPartFull_Fields`: FULL part (every column set, a uom, rollup values) and BARE (nullable
     columns NULL incl. is_active) → the edit-form fields of `fetchPartFull`.
   - `FetchPartBasic_Fields`: FULL (a BOM line, LOCAL thumbnail, primary attachment, stock) and BARE.
   - `PartDetail_Renders`: FULL renders unit abbreviation, notes, a user field, revision, detail.
3. Red: none. This is a pure refactor and `parts.go` stays in the raw-SQL set.
4. Manual-only: part detail page, edit form save, new part, duplicate part.
