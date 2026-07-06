# #346 — Suggest next available base number

## Context

Issue #346 asks for a way to know the next available "base number" when
creating a part, instead of the user having to know/guess it by hand. Today
`PartsCreate` (`arx_go/parts.go:407`) only requires `part_number` be
non-empty — there is no generation logic anywhere, and `PartDuplicate`
(`arx_go/parts.go:388`) explicitly blanks the field with a comment telling the
user to type a new one.

This shop's current convention is a 3-segment numeric pattern:
`xxx-yyyyy-zz` — a numeric category prefix, an incrementing base number, and a
trailing config/dash number (aerospace-style "dash number" for
variant/configuration, distinct from `revision`). The "base number" (`yyyyy`)
is the piece users struggle to pick by hand.

**Scope cut from the original plan:** this no longer renders a full part
number (no category-code mapping, no fixed segments, no auto-fill of the
`part_number` field). It only isolates and suggests the base-number segment
as informational text — the user still types/edits the full `part_number`
themselves. This avoids ever clobbering a manually-entered value and keeps
the config surface small.

The base-number *segment position* stays configurable (separator, which
segment index it is, zero-pad width) so a shop with a different segment
layout doesn't need a code change — mirroring how `part_categories` is
already a JSON blob in `app_config` edited via Settings.

Two generation modes, since shops mix "smart" manually-assigned numbers
(e.g. `00305` = M3x0.5 screw) with a plain sequential range:
- **`max_plus_one`** — next number is `max(existing) + 1`.
- **`next_open_after`** — smallest unused integer `>= floor` (gap-filling,
  so numbers freed up by deleted/renumbered parts below the "smart" range
  get reused, and the smart-number range below `floor` is never touched).

## Design

### 1. Config storage — `app_config` key `part_numbering`

New JSON blob, loaded/saved the same way `part_categories` is
(`h.loadCategories` / `SettingsCategoriesSave` in `arx_go/categories.go`) —
no new table.

```json
{
  "separator": "-",
  "segmentIndex": 1,
  "width": 5,
  "mode": "max_plus_one",
  "floor": 0
}
```

- `separator` splits existing `part_number` values into segments.
- `segmentIndex` (0-based) is which segment holds the base number.
- `width` is the zero-pad width used to render the suggestion.
- `mode` is `"max_plus_one"` or `"next_open_after"`.
- `floor` is only used by `"next_open_after"` — the minimum number to
  consider part of the sequential range (numbers below it are assumed to be
  "smart"/manually-assigned and are left alone, not filled into).

`DefaultBaseNumberConfig()` in `arx_go/models/part.go` returns the shop's
current shape (`separator: "-"`, `segmentIndex: 1`, `width: 5`,
`mode: "max_plus_one"`) so behavior is sane out of the box with no config.

### 2. Generation logic — new `arx_go/partnumber.go`

```go
func (h *Handler) loadBaseNumberConfig(ctx context.Context) models.BaseNumberConfig
func (h *Handler) nextBaseNumber(ctx context.Context) (string, error)
```

`nextBaseNumber`:
1. `SELECT part_number FROM part` (no filtering — global scan only; no
   per-category scoping in this simplified version).
2. Split each value on `Separator`, take the segment at `SegmentIndex`,
   `strconv.Atoi` it; skip values that don't parse or don't have enough
   segments (malformed/legacy part numbers are silently ignored, not errors).
3. Depending on `Mode`:
   - `max_plus_one`: track the max parsed value; result is `max + 1` (or `1`
     if nothing parsed).
   - `next_open_after`: collect parsed values into a set; walk integers
     starting at `Floor` and return the first one not in the set.
4. Zero-pad the result to `Width` and return as a string.
5. This is a *suggestion only* — the existing DB unique constraint and
   "Part Number is required" validation still catch collisions at save time,
   same as today.

### 3. Surface it in the New Part form

- New route `GET /parts/next-number` → `h.PartsNextNumber`, returns the
  suggestion as JSON (`{"suggestion": "00312"}`).
- `part_edit.html`: on the new-part form only, a small fetch-on-load shows
  helper text next to the `part_number` field, e.g. "Next available base
  number: 00312". **No auto-fill** — the user still types the full part
  number by hand, so there's no risk of clobbering input or interacting
  awkwardly with `PartDuplicate`'s pre-blanked field.

### 4. Settings UI

Add a "Part Numbering" section to Settings (near the existing Part
Categories editor) with fields for `separator`, `segmentIndex`, `width`,
`mode` (select: "Max + 1" / "Next open after N"), and `floor` (only
meaningful/shown for `next_open_after`, but keep the form simple — no
show/hide JS complexity required, just always show the field). Same
save/load pattern as `SettingsCategoriesSave`.

## Files touched

- `arx_go/models/part.go` — `BaseNumberConfig` type + `DefaultBaseNumberConfig()`.
- `arx_go/partnumber.go` (new) — `loadBaseNumberConfig`, `nextBaseNumber`.
- `arx_go/parts.go` — new `PartsNextNumber` handler.
- `arx_go/main.go` — route registration: `GET /parts/next-number`.
- `arx_go/templates/pm/part_edit.html` — fetch-on-load helper text (new-part only).
- `arx_go/templates/pm/settings.html` — new "Part Numbering" section.
- `arx_go/settings.go` (or `categories.go`-style dedicated file) — `SettingsPartNumberingSave` handler; `settingsData` gains the loaded config.
- `arx_go/main.go` — route registration: `POST /settings/part-numbering`.

## Verification

- `go build`, `go vet`, `go test ./...` (arx_go + arxlib).
- Manual (user, since app can't be run by the agent per CLAUDE.md): open
  `/parts/new`, confirm helper text shows a sane next base number; create a
  few parts and confirm the suggestion advances; in Settings, change mode to
  "next open after N" with a floor, confirm the suggestion fills a gap
  instead of just incrementing past the max.
- Suggest (not written yet, pending user agreement per CLAUDE.md test
  policy): a unit test for `nextBaseNumber` covering both modes (max+1 with
  gaps below floor untouched; next_open_after filling a gap) given a fake
  set of existing part numbers — off-by-one bugs here would silently suggest
  a colliding or wrong number.
