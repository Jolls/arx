# #190 — Data-access layer (sqlc), first PR: foundation + contacts pilot

Architecture review item 4. Converts one domain at a time; this PR lays the foundation and converts contacts.

## Decisions
- **Scope:** sqlc foundation + contacts pilot. Other domains get their own sub-issues/PRs.
- **`cfg.*Table()`:** dropped per domain as it converts; the helpers are deleted once their last caller is gone.
- **Layout:** one generated package `internal/dbq` (all queries + models, one `models.go`); domain services in `internal/<domain>`; HTTP handlers stay in `arx_go`.
- **Driver:** sqlc's `database/sql` output, so queries still go through the handler's `logSQL`/`timeQuery` wrappers. `*txLogger` already satisfies `dbq.DBTX`.
- **Schema source:** the reference DDL in `SQL/postgres/*.sql`, listed explicitly in `sqlc.yaml` (the seed files don't parse as schema, so the directory can't be used). Migrations are deltas on top of that DDL, not a full schema.
- **Types:** `sqlc.yaml` overrides map `int4` → `int` (an oversized id errors in Postgres instead of wrapping in an `int32` conversion) and nullable `int4`/`timestamptz`/`date` → pointers, matching the Go models, so services need no `sql.Null*` conversion helpers.
- **Nulls:** `COALESCE(col, '')` where the Go side flattens NULL to `""` anyway, so sqlc emits plain `string`. `sqlc.arg(x)::text`/`::int` for parameters on nullable columns.
- **`numeric`:** `COALESCE(total_cost, 0)::float8` keeps today's `float64`; the decimal type is #193.

## Steps
1. `sqlc.yaml` at repo root → `internal/dbq`. Adapter `handlerDB` in `arx_go` routes sqlc calls through the logging wrappers. CI runs `sqlc diff`; `build.bat` doesn't need sqlc.
   Verify: `sqlc generate`, `go build/vet/test ./...`.
2. Contacts: queries in `internal/contacts/contacts.sql`, service `internal/contacts`, handlers only parse/call/render. `models.Contact` moves to `contacts.Contact` (same field names, templates unchanged).
   Verify: `go test ./...` + live integration suite (`contacts_integration_test.go`).
3. Docs: CLAUDE.md (sqlc workflow, new-table checklist), `SQL/schema.md`, architecture review item 4 status, CHANGELOG.
4. Follow-ups: one sub-issue per remaining domain (parts, purchasing, inventory, records, auth/settings, reports).

## Behavior change
A non-numeric contact id in the URL shows "Contact not found" instead of a raw DB error.
