# #114 — SECURITY.md + CONTRIBUTING.md

## Changes
1. New `SECURITY.md`: report via GitHub private vulnerability reporting (repo Security tab → "Report a vulnerability"; owner must enable it in Settings → Code security). Scope: the Arx app + arxlib. State: binds localhost, trusted single-shop deployment assumed. Response: best-effort acknowledgement, no committed timeline (resolved decision).
2. New `CONTRIBUTING.md`, terse, sections:
   - Build/test: `cd arx_go; go build ./... ; go vet ./... ; go test ./...` and same in `arxlib` (from `.github/workflows/test.yml`). `go build ./...` from repo root fails (go.work workspace, not a module).
   - Migrations: authored in `SQL/azure/migrations/`, never auto-run; human runs them. Reference DDL in `SQL/azure/`, `SQL/postgres/`.
   - Seed data: fixed IDs asserted by integration tests (`-tags integration`, `ARX_TEST_DSN`); sentinel part 3005.
   - Windows-only files need `!windows` counterpart for Linux CI.
3. Issue templates: skipped (optional in issue).

## Verify
- Files exist at repo root; commands match test.yml.
