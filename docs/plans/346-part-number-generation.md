# #346 — Auto-suggest new part number (configurable numbering pattern)

## Context

Issue #346 asks for a way to generate a new PN with a fresh "base number" when
creating a part, instead of the user having to know/guess the next available
number by hand. Today `PartsCreate` (`arx_go/parts.go:407`) only requires
`part_number` be non-empty — there is no generation logic anywhere, and
`PartDuplicate` (`arx_go/parts.go:388`) explicitly blanks the field with a
comment telling the user to type a new one.

This shop's current convention is a 3-segment numeric pattern:
`xxx-yyyyy-zz` — a numeric category prefix, an incrementing base number, and a
trailing config/dash number (aerospace-style "dash number" for
variant/configuration, distinct from `revision` which already has its own
column). The user wants the *generation logic* built as a configurable pattern
rather than hardcoded to this one shop's style, so a different numbering
convention can be configured later without a code rewrite — mirroring how
`part_categories` is already a JSON blob in `app_config` edited via Settings
rather than a hardcoded list.

Common real-world PN conventions this pattern engine should be able to express
(informs the segment model below, not all built as UI presets):
- **Dumb/sequential**: meaningless incrementing number only (no prefix/suffix).
- **Significant/intelligent**: a leading code segment encodes category/family/material.
- **Base + dash number**: MIL-STD-100 style — same base design, `-01`/`-02` etc.
  for interchangeable variants, kept separate from revision (design change
  history within a dash number). This matches the shop's `zz` segment.
- Fixed-width, zero-padded numeric segments joined by a separator (dash, dot,
  or none) are the near-universal formatting choice across all of the above.

The engine below models exactly these building blocks: literal/category/sequence
segments, fixed widths, zero-padding, and a configurable separator — enough to
express the shop's current format and the common alternatives above, without
speculative extras (no date-encoding, checksum digits, etc. — not requested).

## Design

### 1. Config storage — `app_config` key `part_numbering`

New JSON blob, loaded/saved the same way `part_categories` is
(`h.loadCategories`, Settings handler) — no new table.

```json
{
  "segments": [
    { "kind": "category", "width": 3 },
    { "kind": "sequence", "width": 5, "scope": "global" },
    { "kind": "fixed",    "value": "01" }
  ],
  "separator": "-",
  "categoryCodes": { "ASM": "010", "BUY": "020", "DWG": "030", "DOC": "040",
                      "FORM": "050", "MFG": "060", "OPS": "070", "RAW": "080",
                      "SVC": "090", "TOOL": "100" }
}
```

- `kind: "category"` → looks up the part's category code in `categoryCodes`,
  zero-pads/truncates to `width`.
- `kind: "sequence"` → the incrementing base number. `scope: "global"` scans
  all existing part numbers; `scope: "category"` scans only numbers sharing
  this part's category segment. (Shop's current style uses `global`.)
- `kind: "fixed"` → literal text, e.g. the default `"01"` dash/config number.
- `separator` joins segments (empty string supported for no separator).

A `DefaultPartNumbering()` in `models/part.go` returns the shop's exact current
pattern above, so behavior is identical out of the box — no migration needed
before the feature is configured.

### 2. Generation logic — new `arx_go/partnumber.go`

```go
type NumberSegment struct {
    Kind     string // "category" | "sequence" | "fixed"
    Width    int
    Scope    string // "global" | "category" (sequence only)
    Value    string // fixed only
}
type NumberingPattern struct {
    Segments      []NumberSegment
    Separator     string
    CategoryCodes map[string]string
}

func (h *Handler) loadNumberingPattern(ctx context.Context) (NumberingPattern, error)
func (h *Handler) nextPartNumber(ctx context.Context, category string) (string, error)
```

`nextPartNumber`:
1. Build a regex from the pattern's segment widths/separator to parse existing
   `part_number` values (`SELECT part_number FROM part WHERE part_number LIKE ...`
   filtered by category prefix when scope is `"category"`, else unfiltered).
2. For the `sequence` segment, take the max parsed value across matches, +1,
   zero-pad to `width`. (Base case / no matches → `1`.)
3. Render all segments in order joined by `separator`.
4. This is a *suggestion*, not a reservation — the existing DB `UQ_part_number_part_number`
   constraint plus the current "Part Number is required" validation still catch
   collisions at save time (two users racing for the same number is already
   possible today; unchanged behavior).

### 3. Wire into the New Part form

- New route `GET /parts/next-number?category=ASM` → `h.nextPartNumber`,
  returns the suggested string as plain text/JSON.
- `part_edit.html`: small JS `change` handler on the category `<select>` that
  fetches the suggestion and fills the `part_number` input *only when it's
  currently empty* (so it never clobbers a manually-typed number, and doesn't
  fire on `PartDuplicate`'s pre-filled-but-blanked field unexpectedly — same
  empty-check covers that case for free).
- Field stays a normal editable text input; user can override before saving.

### 4. Settings UI

Add a "Part Numbering" section to Settings (near the existing Categories
editor) to configure `segments`, `separator`, and `categoryCodes` — same
save/load pattern as `part_categories`. Kept minimal: a small form, not a
drag-and-drop builder.

## Files touched

- `arx_go/models/part.go` — `NumberingPattern`/`NumberSegment` types + `DefaultPartNumbering()`.
- `arx_go/partnumber.go` (new) — `loadNumberingPattern`, `nextPartNumber`, regex parse/render helpers.
- `arx_go/parts.go` — new `PartsNextNumber` handler; route registration alongside other `/parts/*` routes.
- `arx_go/templates/pm/part_edit.html` — JS fetch-and-fill on category change.
- `arx_go/templates/pm/settings*.html` (wherever categories are edited) — new numbering-pattern section.
- `arx_go/settings.go` (or wherever `part_categories` is saved) — load/save `part_numbering` key.

## Verification

- `go build`, `go vet`, `go test ./...` (arx_go + arxlib).
- Manual (user, since app can't be run by the agent per CLAUDE.md): open
  `/parts/new`, pick a category, confirm the part number field auto-fills with
  the next number in `xxx-yyyyy-zz` form; change category and confirm the
  category segment updates; manually type a number and confirm the JS doesn't
  overwrite it; save and confirm no collision with existing data.
- Suggest (not written yet, pending user agreement per CLAUDE.md test policy):
  a unit test for `nextPartNumber`'s max-parsing/increment logic given a fake
  set of existing part numbers, since off-by-one or regex-width bugs there
  would silently generate colliding or malformed numbers.
