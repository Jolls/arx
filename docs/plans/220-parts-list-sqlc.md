# #220 — Parts list rows and CSV export to sqlc

Part of #220 / #190. First slice of the `arx_go/parts.go` catalog: the two whole-catalog reads.

## Scope
- `PartsRows` (GET /parts/rows, the /parts grid JSON) and `PartsExportCSV` (GET /parts/export.csv).
- `parts.go` keeps its other raw SQL, so it does not join `convertedFiles` yet. No `cfg.*Table()`
  helper loses its last caller.

## Changes
- `internal/parts/parts.sql` (+ `sqlc generate`): `ListParts :many`, the `PartsRows` query with
  `COALESCE` on the nullable text columns (`''`), `is_active` (`TRUE`) and the two counts (`0`), plus the
  thumbnail subquery as `COALESCE((SELECT MIN(a.file_name) ...), '')`, `$1` = thumbnail category,
  `ORDER BY part_number`.
- `internal/parts/parts.go`: `type ListedPart{ID int; PartNumber, Revision, Description, Detail,
  RequestedBy, Category string; CreatedDate, ModifiedDate *time.Time; IsActive bool; AttachmentCount,
  POLineCount int; BelowMin bool; ThumbFile string}` and `ListParts(ctx, thumbCategory string)`.
- `arx_go/parts.go`: both handlers call `h.parts().ListParts(ctx, thumbnailCategory)`. Export ignores the
  extra fields; the thumbnail subquery it now also runs is one indexed lookup per part.

## Test plan
1. Coverage audit: nothing covers either handler.
2. Characterization (`arx_go/parts_list_integration_test.go`), shared seed: FULL (every field set, stock 1 <
   reorder_min 5, active LOCAL thumbnails `b`/`c`, and a lower-named inactive thumbnail and active
   non-thumbnail that must be ignored); BARE (nullable columns NULL, no reorder_min, http thumbnail →
   no `thumb`); OFF (`is_active` FALSE).
   - `PartsRows_Fields`: exact JSON row per seeded part, relative order FULL < BARE < OFF.
   - `PartsExportCSV_Fields`: header plus exact CSV record per seeded part.
3. Red: none. This is a pure refactor and `parts.go` stays in the raw-SQL set.
4. Manual-only: /parts grid loads (hover thumbnail, below-min flag, inactive rows); Export CSV downloads.
