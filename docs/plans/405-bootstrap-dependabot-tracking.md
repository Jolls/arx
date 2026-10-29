# Plan: #405 Track Bootstrap versions with Dependabot via tracking-only package.json

No schema change. No `RELEASE_NOTES.md` change (per-release only). No runtime/template change.

## Current code (verified)

- `arx_go/templates/shared/layout.html:9` bootstrap CSS `bootstrap@5.3.8/dist/css/bootstrap.min.css`; `:10` `bootstrap-icons@1.11.3/font/bootstrap-icons.min.css`; `:54` `bootstrap@5.3.8/dist/js/bootstrap.bundle.min.js`. All via `https://cdn.jsdelivr.net/npm/<pkg>@<ver>/...` with `integrity="sha384-..."`.
- `arx_go/templates/shared/login.html:9` bootstrap CSS `5.3.8`; `:10` bootstrap-icons `1.11.3` (inside `{{if .SchemaMismatch}}...{{end}}`, same line).
- Versions in use: bootstrap `5.3.8`, bootstrap-icons `1.11.3`.
- No root `package.json`, no `node_modules`/`package` mention in `.gitignore`, `.github/workflows/*` (`test.yml` runs only sqlc diff, vet, build, `go test -race ./...`, govulncheck), `.devcontainer/*`. A root `package.json` affects none of them (Go ignores it).
- `.github/dependabot.yml` has ecosystems gomod, github-actions, docker (`/.devcontainer`), docker-compose (`/SQL`), devcontainers; each `interval: weekly`. No npm.
- Templates are embedded: `arx_go/embed.go` `templatesFS` (`//go:embed templates`). Existing helper `readTemplate(t, path)` in `arx_go/layout_theme_test.go` reads from `templatesFS` (paths like `templates/shared/layout.html`). Tests in `arx_go` run with cwd `arx_go/`, so repo root is `..`.
- `CONTRIBUTING.md` sections: Build and test, Database and migrations, Local test database, Integration tests, Dev container, Licensing, Changelog. No Bootstrap/Dependabot text anywhere in CONTRIBUTING.md, `docs/conventions.md`, README.md.

## Changes

1. **New `package.json`** (repo root): `"name": "arx-cdn-versions"`, `"private": true`, `"description"` stating it exists only so Dependabot tracks CDN versions and that a Dependabot PR bumping it must be followed by hand-updating the CDN URLs and SRI hashes in `arx_go/templates/shared/layout.html` and `arx_go/templates/shared/login.html`. `"dependencies"`: `"bootstrap": "5.3.8"`, `"bootstrap-icons": "1.11.3"` (exact, no range operator). No `scripts`, no lockfile.
2. **`.github/dependabot.yml`**: append an entry `package-ecosystem: npm`, `directory: /`, `schedule.interval: weekly`, matching existing entry style.
3. **`CONTRIBUTING.md`**: add section `## Bootstrap CDN versions` immediately before `## Changelog`. Content: Bootstrap and Bootstrap Icons load from jsDelivr in `layout.html` and `login.html`; root `package.json` only mirrors versions so Dependabot opens PRs; a Dependabot PR changes only `package.json`, so before merge update the version in every CDN URL and regenerate the `integrity` SRI hashes (copy from the jsDelivr/getbootstrap.com snippet for that exact version) in both templates; `TestTemplateCDNVersionsMatchPackageJSON` fails until they match.
4. **New `arx_go/bootstrap_version_test.go`** (`package main`, imports `encoding/json`, `os`, `regexp`, `testing`; use existing `readTemplate`): see Test plan.
5. **`CHANGELOG.md`**: add under the current top `## [x.y.z]` entry only if it is still unreleased; otherwise add a new top patch entry (next `z`, today's date) per CLAUDE.md. Subheading `### Added`. Line: `- Dependabot now tracks Bootstrap and Bootstrap Icons versions via a tracking-only root package.json, with a test that fails if the template CDN versions drift from it ([#405](https://github.com/Jolls/arx/issues/405))`. Do not invent the version; read the top entry and decide by whether it is already released/merged.

## Test plan

1. **Coverage audit.** `arx_go/layout_theme_test.go` (`TestLayout_ThemeScriptInHeadBeforeCSS` etc.) reads `layout.html`/`login.html` but asserts nothing about Bootstrap CDN URLs or versions. A grep of `arx_go/*.go` for `bootstrap@` / `cdn.jsdelivr` finds no Go test or code referencing them. `filter_help_test.go` only walks templates for filter rows. No test touches `package.json` or `dependabot.yml`.
2. **Characterization tests.** None.
3. **Red tests.** One test, `TestTemplateCDNVersionsMatchPackageJSON` in `arx_go/bootstrap_version_test.go`:
   - Reads `../package.json` via `os.ReadFile`, unmarshals `dependencies`.
   - For each of `templates/shared/layout.html` and `templates/shared/login.html`, finds all matches of `cdn\.jsdelivr\.net/npm/(bootstrap|bootstrap-icons)@([^/"]+)/` and asserts: at least one match per package per file, and every match's version equals `dependencies[pkg]`. (Both packages appear in both files.)
   - Fails today because `../package.json` does not exist (read error); after step 1 it passes on the current tree (5.3.8 / 1.11.3). It also fails if any template URL or the manifest is bumped alone.
4. **Manual-only items.**
   - After merge, confirm GitHub Insights > Dependency graph > Dependabot shows the npm ecosystem and the first weekly PR opens against `package.json`.
   - On the first Dependabot Bootstrap PR, follow the CONTRIBUTING note (update URLs + SRI by hand) and confirm the test goes green.

## Verify

`go build ./...`, `go vet ./...`, `go test ./arx_go/ -run TestTemplateCDNVersionsMatchPackageJSON`, then `go test ./...`.

## Resolved decisions

- No lockfile (confirmed).
- Exact pins, no `^` ranges (confirmed).
