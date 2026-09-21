# #138 Record view: MISSING badge for unrecorded steps

## Current state (verified)
- `arx_go/render_records.go:97-108` `pfBadge(res *models.TestResult)`: `res == nil` returns grey "—"; `res.Result == ""` returns MISSING.
- Called only from `arx_go/templates/records/records_show.html:233` and `record_print.html:98` as `{{pfBadge .Result}}`, inside `{{range .Rows}}` (`.` is `models.ResultRow`, Level 0 branch).
- Edit-mode rule is `arx_go/static/records/app.js:118-136` (out of scope, do not touch): empty value -> MISSING for `filled`/`attach`, and for range (anything not filled/attach/comment) when `SpecMin` or `SpecMax` is non-empty; otherwise nothing. Same rule in `models.ResultRow.CalcPF` (`arx_go/models/trmodels.go:311-341`).
- `ResultRow.Step` is always set for Level 0 rows (templates already use `.Step.ID`).

## Changes
1. `arx_go/render_records.go` (`pfBadge`, lines 97-108):
   - Change signature to `func(row models.ResultRow) template.HTML`.
   - If `row.Result == nil`: return MISSING when `strings.ToLower(row.Step.PFType)` is `filled` or `attach`, or is not `comment` and (`row.Step.SpecMin != ""` or `row.Step.SpecMax != ""`); else return the grey "—" badge.
   - If `row.Result != nil`: keep existing logic unchanged (`row.Result.Result == ""` -> MISSING, PassFail true -> PASS, else FAIL).
2. `arx_go/templates/records/records_show.html:233`: `{{pfBadge .Result}}` -> `{{pfBadge .}}`.
3. `arx_go/templates/records/record_print.html:98`: `{{pfBadge .Result}}` -> `{{pfBadge .}}`.
4. No change to `arx_go/static/records/app.js` (no overlap with #139's pfBadge/new-record-form work).

## Verify
- `cd arx_go; go build ./... ; go vet ./... ; go test ./...` (no existing test calls the template `pfBadge`; `trmodels_test.go` TestCalcPF unaffected).
- Manual (user): record with unsaved filled/attach step, unsaved range step with min/max, unsaved comment/no-bound range step; check view + print show MISSING / MISSING / "—"; saved steps unchanged.

## Changelog
- Add `### Fixed` entry under new top `## [x.y.z]` in `CHANGELOG.md` (bump per repo convention) linking `https://github.com/Jolls/arx/issues/138`. `RELEASE_NOTES.md` only if the release is user-facing per convention.

## Resolved decisions
1. View is default-aware: a nil-result step with a non-empty default_result follows edit mode (CalcPF-style evaluation of the effective value); MISSING only when there is no default either and edit mode would flag it.
2. CHANGELOG: one combined "Fixed" entry for the whole PR (with #139); no RELEASE_NOTES entry (per-release, not per-patch).
3. Merge order: #138 applied first, then #139 on top (app.js pfBadge/updatePF overlap).
