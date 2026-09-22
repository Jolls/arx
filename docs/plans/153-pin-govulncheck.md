# #153 Pin govulncheck version in CI

Part of #142. Fixes `@latest` resolving at run time in the vulnerability check
steps of `.github/workflows/test.yml`.

## Decision: direct `@vX.Y.Z` pin, not go.sum tool dependency

Using the direct version pin (`go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`)
rather than adding it as a `go.mod` tool dependency. Reasoning: one-line change,
no go.mod/go.sum edits in either module, no risk of touching unrelated
build/vet/test behavior in `arx_go` or `arxlib`. Matches CLAUDE.md's
Simplicity First / Surgical Changes rules. The tool-dependency approach adds
Dependabot visibility, which isn't needed here — Dependabot bumps aren't in
scope for this issue.

Version chosen: **v1.8.0** — latest stable tag per
`go list -m -versions golang.org/x/vuln` (checked 2026-09-22; latest listed
was v1.8.0).

## File changes

`.github/workflows/test.yml`:

- Line 44: change
  `run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
  to
  `run: go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`
- Line 46: change
  `- run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
  to
  `- run: go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`

No other files change. `actions/checkout@v4` and `actions/setup-go@v5` stay as-is per the issue.

## Verification

- No local build/test needed (workflow-only change, not exercised by `go test`/`build.bat`).
- Confirm YAML is still valid (e.g. visually diff, or `gh workflow view` won't catch syntax — just re-read the file after editing).
- On the PR, confirm the "Vulnerability check" CI step runs and passes using the pinned version (visible in the Actions log: `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0`).
