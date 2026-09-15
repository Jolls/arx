# #51 — Glob wildcard filter (`*` / `?`) for text columns

## Scope
`arx_go/static/shared/app.js` only. Client-side text-column filter matching
used by every JS-driven filterable table (parts, POs, suppliers, contacts,
records, BOM trace views, etc. — all share `matchesRow`/`getFilterColumns`/
`applyFilters`). No backend/template/schema changes.

## Current structure (verified line numbers)
- `getFilterColumns()` (app.js:309-319): builds one descriptor per
  `tr.filter-row th`, in column order. Text columns currently produce
  `{ type: 'text', value: input ? input.value : '' }`.
- `matchesRow(row, cols)` (app.js:321-335): `Array.every` over descriptors.
  Text branch (app.js:332-334):
  ```js
  const v = (c.value || '').toLowerCase().trim();
  return !v || text.includes(v);
  ```
  `row._text[i]` is already lowercased at load time (app.js:500), so only
  the filter value needs lowercasing here.
- Callers, both via `Array.filter` (one `matchesRow` call per row, called
  fresh each keystroke): `applyFilters()` at app.js:409 and `exportCSV()` at
  app.js:530. Both call `getFilterColumns()` once per invocation, then
  `matchesRow` once per row — so compiling a pattern inside
  `getFilterColumns()` (per filter value, i.e. per column, not per row)
  is the correct place for a one-time-per-keystroke compile.

## Design decision (fixed requirements from task)
- No `*`/`?` in the typed value → behavior identical to today: lowercase,
  trim, substring `includes`.
- Case-insensitive, matching current behavior.
- Pattern must be compiled once per filter value (in `getFilterColumns`),
  not once per row (`matchesRow` stays a lookup/test, no compilation).

## Design decision (mine, not left open)
When `*`/`?` are present, match is **anchored to the whole field**
(`^...$`), not substring. Rationale: this is standard glob semantics and
matches the issue's own example (`65*-013*-*` describes the shape of the
*entire* part number). A user who wants substring-style wildcard matching
gets it for free by adding a leading/trailing `*` (e.g. `*013*`) — so
anchoring doesn't remove any capability, it just makes the empty-wildcard
case (no `*`/`?` at all) keep meaning "substring," which is exactly the
backward-compat requirement.

## Change

### 1. `getFilterColumns()` (app.js:309-319)
For the text branch, after reading `input.value`, build the descriptor as
today but add a precompiled matcher when wildcards are present:

```js
const input = th.querySelector('input, select');
const raw = input ? input.value : '';
return { type: 'text', value: raw, regex: compileGlob(raw) };
```

### 2. New helper `compileGlob(raw)` (place just above `getFilterColumns`, ~line 308)
```js
// Compiles a `*`/`?` glob into a case-insensitive, whole-field RegExp.
// Returns null when the trimmed value has no wildcard chars, so callers
// can fall back to the existing substring `includes` behavior unchanged.
function compileGlob(raw) {
    const v = (raw || '').trim();
    if (!v || !/[*?]/.test(v)) return null;
    const pattern = v
        .replace(/[.+^${}()|[\]\\]/g, '\\$&') // escape regex specials (not * or ?)
        .replace(/\*/g, '.*')
        .replace(/\?/g, '.');
    return new RegExp('^' + pattern + '$', 'i');
}
```
Notes:
- `String.replace` with `$&` needs no escaping of `*`/`?` themselves since
  they're excluded from the character class being escaped.
- Case-insensitivity via the `i` flag means the regex path does NOT need
  `text`/`value` lowercased first — but `row._text[i]` is already
  lowercased at load (app.js:500) and that's harmless/redundant with `i`,
  no change needed there.

### 3. `matchesRow()` text branch (app.js:332-334)
```js
if (c.regex) return c.regex.test(text);
const v = (c.value || '').toLowerCase().trim();
return !v || text.includes(v);
```

## Verify
- `go build ./...` / `go vet ./...` not applicable (pure JS) — no Go
  touched, so `arx_go/build.bat` still runs clean (regression check only).
- Manual verification (user, per CLAUDE.md — do not launch the app):
  1. On `/parts`, filter a text column with a plain substring (e.g. part of
     a description) — confirm identical results to pre-change behavior.
  2. Filter with `65*-013*-*` style pattern against part numbers — confirm
     only matching part numbers show.
  3. Filter with `?` single-char wildcard (e.g. `65?-01301-01`) — confirm
     exact-length substitution matches.
  4. Filter with a value containing regex-special chars but no wildcards
     (e.g. a description containing `(`, `.`, `+`) — confirm it still
     matches via plain substring (regex path not engaged, no crash from
     unescaped specials).
  5. Confirm CSV export (`exportCSV`, app.js:530) reflects the same glob
     filter results as the on-screen table (it reuses `matchesRow`/
     `getFilterColumns`, no separate change needed).
  6. Spot-check one other filterable table (e.g. `/suppliers` or `/pos`)
     since the fix is shared, not per-page.

## Open questions
None — anchoring and backward-compat were fixed/decided above per task
instructions.
