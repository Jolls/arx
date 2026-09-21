# #139 Formula default_result not computed on /records/{id}/edit after Create

## Root cause (verified in current code)
- `records.go` `EditRecord` (~2009-2012) sets `RawDefault = Step.DefaultResult`, then `resolveStepRefs` rewrites `Step.DefaultResult`. With `={629}/2` and no result for 629, the token stays `={629}/2`. If 629 has a spec_nom, `substituteRefs` (~200-206) substitutes that nominal instead.
- `templates/records/record_edit.html` ~278 (plain-input branch) renders `value="{{$cur}}"`. `$cur` is `EffectiveValue` (`models/trmodels.go` ~353), which returns `Step.DefaultResult` when the result is empty. So a fresh record's input already contains the formula text, not an empty value.
- `static/records/app.js` `applyFormulas()` (~521-567):
  - On load, `{629}` is unresolved, so it returns at ~527-532 before `input.dataset.seen` is set (~541).
  - When 5 is keyed into 629, the first resolved pass sets `seen`. The input value `={629}/2` is non-empty and not equal to `2.5`, so it sets `dataset.override='1'` (~543).
  - The row goes down the override branch (~547-554): editable, amber `formula-overridden` (yellow, matching the report), never blue computed.
- The P/F badge stays wrong or MISSING: `updatePF` runs on the raw formula text, and `parseFloat("=...")` is NaN.
- Secondary defect in the same function: once computed, clearing a referenced value leaves a stale computed value in the input (the unresolved branch does not clear it).

## Changes

### 1. `arx_go/templates/records/record_edit.html` line ~278, plain-input branch only
Replace `value="{{$cur}}"` with:
`value="{{if .RawDefault}}{{if .Result}}{{.Result.Result}}{{end}}{{else}}{{$cur}}{{end}}"`
- Formula rows now start with the saved result only (empty on a fresh record).
- Rows with no default are unchanged.
- The other branches (attach, select, query) are untouched. Do not add `data-formula` to them.

### 2. `arx_go/static/records/app.js` `applyFormulas()`, unresolved branch (~527-532)
After `input.removeAttribute('title')`, and before `return`, add:
- If `!input.dataset.override && input.value !== ''` and `input.dataset.seen`:
  - `input.value = ''`
  - `var sid = input.name ? input.name.slice(7) : ''`
  - `if (sid) setLive(sid, '')`
  - `updatePF(input)`
- Do not change the leading-`=` handling, the `seen`/override logic, or the dblclick handler.

Note: with change 1, a saved result that differs from the computed default (#133 override on reload) still works. `seen` is first set on the first resolved pass, and a non-empty saved value differing from the computed one still becomes an override.

## Overlap with #138
`app.js`: this touches only `applyFormulas`. It does not touch `pfBadge`/`updatePF` or `render_records.go`. No conflict expected.

## Verification (manual, do not run the app)
1. `go build ./...`, `go vet ./...` and `go test ./...` in `arx_go/` pass.
2. New record on a form with step 629 (plain input) and a formula step `={629}/2`. The formula row is initially blank. Key 5 into 629 and the row turns blue read-only showing 2.5, with the P/F badge updated. Change 629 to 6 and it shows 3. Clear 629 and the formula row clears.
3. Double-click the blue row: amber override. Double-click again: reverts to blue computed.
4. Save, then reopen. The value is 2.5, blue, and matches what was displayed.

## Open questions
1. Is step 629 a plain text input? Formulas only recompute on `input`/`change` of `result_N` fields. If 629 is a select or a `query:` row, the event wiring needs a separate check. The repro says keyed in, so this plan assumes a plain input.
2. The reported symptom was "blank plus MISSING". Code inspection shows amber override with the raw formula text as the value, not a blank input. It is unconfirmed whether a blank display occurs. Please confirm the repro after the change.
