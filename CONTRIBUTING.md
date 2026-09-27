# Contributing

## Build and test

The repo is a single Go module rooted at the repo root. From the root:

```
go vet ./... && go build ./... && go test ./...
```

On Linux you also need `libayatana-appindicator3-dev` (systray's cgo dependency). Windows-only source files need a `!windows`-tagged counterpart so the Linux CI job compiles.

## Database and migrations

- Schema changes ship as a goose migration in `SQL/postgres/migrations/`, applied with `go run ./arx_go/cmd/migrate up` (never at app startup) by a DDL-capable login from `ARX_MIGRATE_DSN`.
- `SQL/postgres/*.sql` is reference DDL, not a migration runner; keep it in sync with the migrations.
- Details: [`SQL/SCHEMA.md`](SQL/SCHEMA.md).

## Integration tests

`go test -tags integration ./arx_go/...` needs `ARX_TEST_DSN` pointing at a seeded test database (or `ARX_TEST_FROM_CONFIG=1` to use the test-mode connection saved via Settings). The database must be named `ArxDev` (any case); anything else, and anything containing `arxprod`, is refused. The seed data uses fixed IDs that the tests assert against (e.g. part `3005`), so don't renumber seed rows. They are excluded from the default `go test ./...`.

## Licensing

Contributions are accepted under the project's AGPL-3.0 license.

## Changelog

Add one entry per PR under the top version in [`CHANGELOG.md`](CHANGELOG.md).
