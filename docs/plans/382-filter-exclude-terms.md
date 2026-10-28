# Plan: #382 column filters, `!` exclude terms and `;`-separated terms

Scope: `arx_go/static/shared/app.js` only (plus CHANGELOG and a test file). Text filters only. Do not touch popover work (#381), date filters, select filters.

## Current code (verified)

- `arx_go/static/shared/app.js:347` `compileGlob(raw)`: trims; returns `null` if empty or no `*`/`?`; else case-insensitive whole-field `RegExp` (`^...$`, regex specials escaped, `*`->`.*`, `?`->`.`).
- `app.js:357` `getFilterColumns()`: per filter `th`; date -> `{type:'date',from,to}`; else reads `th.querySelector('input, select')` value, returns `{type:'text', value: raw, regex: compileGlob(raw)}` (line 366).
- `app.js:370` `matchesRow(row, cols)`: `cols.every(...)` over `row._text[i]` (already lowercased at line ~556). Text branch (lines 381-383): `if (c.regex) return c.regex.test(text); const v = value.toLowerCase().trim(); return !v || text.includes(v);`
- Callers of `matchesRow`: lines 464 and 586 (`allRows.filter(r => matchesRow(r, getFilterColumns()))`); no change needed.
- `app.js:117-118` persists `c.value` (raw string) into the URL as `f.<key>`; stays valid because raw string is kept in `value`. No change.
- Select filters: `input, select` value goes through the same text branch today; `;`/`!` parsing will also apply to them. Option values containing `;` or a leading `!` would change behavior (see Open question 4).

## Changes

1. `app.js`: add `parseFilterTerms(raw)` directly below `compileGlob`. Splits `raw` on `;`, trims each piece, drops empty pieces. A piece starting with `!` is an exclude term (term = text after the `!`); otherwise include. Each term is converted with the existing rules: `compileGlob(term)` regex if it has `*`/`?`, else lowercase substring. Returns `{ include: [matcher...], exclude: [matcher...] }` where a matcher is `{regex}` or `{sub}`. Spaces inside a term preserved (only ends trimmed).
2. `app.js`: add `makeTextFilter(raw)` returning `{ type:'text', value: raw, terms: parseFilterTerms(raw) }`. Use it in `getFilterColumns` in place of line 366 (drop the `regex` field).
3. `app.js` `matchesRow` text branch: replace lines 381-383 with: row matches iff every include matcher matches `text` AND no exclude matcher matches `text`. Matcher test: `regex ? regex.test(text) : text.includes(sub)`. No terms -> match (same as empty filter today).
4. `CHANGELOG.md`: new top entry (next `0.8.x`, today's date) under `### Added`: one line for `;` multi-term and `!` exclude, linking `https://github.com/Jolls/arx/issues/382`.
5. Test file (see Test plan): `arx_go/filter_terms_js_test.go`.

## Test infrastructure (finding)

No JS tests exist: no `package.json`, no `*.test.js`, no Go test that executes JS (Go tests only `fs.Stat`/regex over embedded static files, e.g. `layout_theme_test.go`). CI (`.github/workflows/test.yml`) runs `go vet`, `go build`, `go test -race ./...` on ubuntu-latest, plus the integration job; `build.bat` runs `go test ./...`. Nothing runs node. `app.js` is a classic browser script (no exports, top-level DOM state), so it cannot be `require`d directly.

Minimal fit (needs approval, Open question 1): a Go test in package `main` that reads `static/shared/app.js` from `staticFS`, slices out the source of `compileGlob`, `parseFilterTerms`, `makeTextFilter`, `matchesRow` (by `function <name>` through the matching closing brace at column 0), concatenates them with a small driver that reads JSON cases and prints JSON results, and runs it with `node` via `os/exec`. `t.Skip` when `node` is not on PATH. Node is preinstalled on ubuntu-latest, so CI runs it; local Windows runs skip unless node is installed (node 24 present on the maintainer machine). No new Go dependency.

## Test plan

### 1) Coverage audit
No existing test covers `compileGlob`, `getFilterColumns`, `matchesRow`, or the `*`/`?` filter behavior from #51 (grep of `*_test.go` for these names and `app.js` found only `decimal_test.go` comment mention and `dead_schema_names_test.go` text scan of `.js` files). Coverage is nil.

### 2) Characterization tests (pass on unchanged code)
Prerequisite step 0 (pure refactor, no behavior change, done first, before any other change): add `makeTextFilter(raw)` returning `{type:'text', value: raw, regex: compileGlob(raw)}` (the exact current object) and use it at line 366. Also add the node-runner Go test harness. Then add, in `TestFilterTermsJS_Characterization` (table-driven, each case = filter string, row text(s) lowercased, expected bool, via `matchesRow({_text:[text]}, [makeTextFilter(raw)])`):
- empty filter and whitespace-only filter match everything.
- substring: `obs` matches `abc-obs`, not `abc-01`; case-insensitive (`OBS` vs lowered text).
- substring with inner spaces: `standard drone` matches `skyrunner standard drone`, not `standard  drone` (two spaces).
- glob whole-field: `abc*` matches `abc-01`, not `xabc-01`; `*-01` matches `abc-01`; `a?c` matches `abc`, not `abbc`.
- glob special chars escaped: `a.c*` does not match `abc`.
- date column untouched: `{type:'date',from:'2026-01-01',to:'2026-01-31'}` with `_text` `2026-01-15` true, `2026-02-01` false, empty text false.
- multi-column AND: two text columns, both must match.

### 3) Red tests (fail on current code)
`TestFilterTermsJS_Terms` (same harness; filter shape built by `makeTextFilter`). Stub needed so they compile/run and fail rather than error: step 0's `makeTextFilter` (returns the old shape) and an added `parseFilterTerms` stub `function parseFilterTerms(raw) { return { include: [], exclude: [] }; }` is NOT required by the matcher yet; the red tests call only `makeTextFilter` and `matchesRow`, so no further stub. They fail today because `;` and `!` are treated as literal substring characters.
- `exclude_substring`: `!obs` vs `abc-obs` -> false, vs `abc-01` -> true. Fails today: literal `!obs` matches neither (expects true for `abc-01`).
- `exclude_glob_whole_field`: `!*-obs` vs `abc-obs` -> false, `abc-obs-2` -> true, `abc-01` -> true. Fails today for the same reason.
- `and_two_includes`: `a; b` vs `a-b` -> true, `a-c` -> false, `b-c` -> false. Fails today: literal `a; b` matches nothing.
- `include_and_exclude`: `a; !b` vs `a1` -> true, `ab` -> false, `b1` -> false.
- `mixed_example`: `abc*; *-01; !*-obs` vs `abc-01` true, `abc-obs` false, `abc-02` false, `xabc-01` false.
- `whitespace_around_semicolon`: `a;b`, `a ; b`, ` a;  b ` all behave identically (`a-b` true, `a-c` false).
- `inner_spaces_preserved`: `standard drone; !obs` vs `skyrunner standard drone` true, `standard  drone` false, `standard drone obs` false.
- `empty_terms_ignored`: `a;;b`, `a;`, `;a`, `; ;` : `a;;b` equals `a; b`; `a;` and `;a` equal `a`; `; ;` matches everything.
- `no_semicolon_no_bang_unchanged`: re-assert the characterization cases still pass (same table, run after change).
- `bang_not_leading_is_literal`: `a!b` as substring matches `xa!bx`, not `ab` (acceptance: only a leading `!` is special).
- `exclude_only_filter`: `!obs` alone shows rows not containing obs (included above; additionally empty text row -> true).
- `multi_column_and`: col0 `a; !b`, col1 `x`; row passes only when both columns pass.
- `date_and_select_unaffected`: date col unchanged result; a text column fed a select-style value without `;`/leading `!` behaves as substring (same as before).

### 4) Manual-only
- In a browser on `/parts`, `/suppliers`, `/records`: type `ABC*; *-01; !*-OBS` in a column filter; confirm rows filter live, paging resets, and combining with another column's filter ANDs.
- Reload/share URL with `f.<col>=a; !b` (URL persistence at app.js:117) and confirm the filter box restores and applies.
- Confirm a filter on a select-type column (e.g. status) still works.
- Confirm date-range popover unchanged.

## Open questions

1. Test mechanism: OK to add a Go test that shells out to `node` (skipped when node missing) over source sliced from `app.js`? Alternatives: add a Go JS interpreter dependency (goja), add `package.json` + `node --test` plus a CI step, or leave JS untested and rely on manual testing.
2. A lone `!` (empty exclude term, e.g. `!` or `a; !`): ignore it as an empty term (proposed), or treat as literal/hide nothing/hide empty fields?
3. Whitespace after `!`: is `! obs` the same as `!obs` (trim after `!`) or does the term keep the leading space?
4. Select-type filter columns share the text branch; any select option value containing `;` or a leading `!` would now be parsed as terms. Accept (nothing in current templates is known to do this, not audited), or skip term parsing for `<select>` inputs?
5. Behavior of `!*` (excludes everything) and `*`-only includes: accept as natural consequence of the rules?

## Resolved decisions
- Tests: Go test in `arx_go` slices the filter functions from embedded app.js and runs them via `node` (os/exec); skips if node absent. No new deps/CI step.
- Lone `!` (empty exclude term, e.g. `a; !`) is ignored as an empty term.
- Term is trimmed after `!`: `! obs` == `!obs`.
- `!*` hides every row (plain consequence of the rules).
- `;`/`!` parsing applies to text inputs only; skipped for `<select>` filters.
