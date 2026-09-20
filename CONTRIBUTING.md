# Contributing

## Build and test

The repo root is a Go workspace (`go.work`) spanning two modules, not a module itself — `go build ./...` from the root fails. Run inside each module:

```
cd arx_go && go vet ./... && go build ./... && go test ./...
cd ../arxlib && go vet ./... && go build ./... && go test ./...
```

On Linux you also need `libayatana-appindicator3-dev` (systray's cgo dependency). Windows-only source files need a `!windows`-tagged counterpart so the Linux CI job compiles.

## Database and migrations

- Schema changes ship as a migration in `SQL/azure/migrations/`. Migrations are authored, never run automatically — the maintainer runs them by hand.
- `SQL/azure/*.sql` and `SQL/postgres/*.sql` are reference DDL, not migration runners; keep them in sync.
- Details: [`SQL/schema.md`](SQL/schema.md).

## Integration tests

`go test -tags integration ./arx_go/...` needs `ARX_TEST_DSN` pointing at a seeded test database. The seed data uses fixed IDs that the tests assert against (e.g. part `3005`), so don't renumber seed rows. They are excluded from the default `go test ./...`.

## Changelog

Add one entry per PR under the top version in [`CHANGELOG.md`](CHANGELOG.md).
