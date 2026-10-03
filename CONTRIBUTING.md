# Contributing

## Build and test

The repo is a single Go module rooted at the repo root. From the root:

```
go vet ./... && go build ./... && go test ./...
```

Windows-only source files need a `!windows`-tagged counterpart so the Linux CI job compiles.

## Database and migrations

- Schema changes ship as a goose migration in `SQL/postgres/migrations/`, applied with `go run ./arx_go/cmd/migrate up` (never at app startup) by a DDL-capable login from `ARX_MIGRATE_DSN`.
- `SQL/postgres/*.sql` is reference DDL, not a migration runner; keep it in sync with the migrations.
- Details: [`SQL/SCHEMA.md`](SQL/SCHEMA.md).

## Local test database

For manual testing without access to a shared `ArxDev`, run a throwaway Postgres 17 with the schema and test seed. It needs only docker (`podman compose` works too), with no psql, bash or WSL on the host. The data lives on tmpfs, so stopping the container wipes it:

```
docker compose -f SQL/postgres/compose.yml up -d --wait db   # returns once the seed has loaded
docker compose -f SQL/postgres/compose.yml restart db        # reset to a fresh seed
docker compose -f SQL/postgres/compose.yml down              # remove
```

Then in Settings, turn on Test Mode, set DB Server to `127.0.0.1:55432` (the field takes `host:port`), Username to `postgres` and Password to `postgres`, and save. The Test Connection fields can stay blank because they inherit these, and the test database name defaults to `ArxDev`. Log in as `admin`/`admin` or `tester`/`tester` (see [`SQL/SCHEMA.md`](SQL/SCHEMA.md)).

## Integration tests

`go test -tags integration ./arx_go/...` needs `ARX_TEST_DSN` pointing at a seeded test database (or `ARX_TEST_FROM_CONFIG=1` to use the test-mode connection saved via Settings). Against the local container above (about a minute): `ARX_TEST_DSN=postgres://postgres:postgres@127.0.0.1:55432/ArxDev?sslmode=require`. Run `restart db` between runs, since rows left by one run can fail the next. The database must be named `ArxDev` (any case); anything else, and anything containing `arxprod`, is refused. The seed data uses fixed IDs that the tests assert against (e.g. part `3005`), so don't renumber seed rows. They are excluded from the default `go test ./...`.

## Dev container

`.devcontainer/` gives a Go toolchain, `sqlc`, `psql` and the seeded `ArxDev` database from `SQL/postgres/compose.yml` (reachable as `db:5432`), with no manual setup. In VS Code use "Reopen in Container". `go build ./...`, `go test ./...` and `go test -tags integration ./arx_go/...` work as-is. The dev container and the local test database recipe above share a container name, so run one at a time.

- Start the app with the "Run Arx (headless)" task (Terminal > Run Task). Browse to `http://localhost:4568` and log in as `admin`/`admin`. Chrome warns that this password appeared in a data breach; that's expected for the throwaway seed logins and can be dismissed.
- Browsing works only through VS Code port forwarding. The listener binds `127.0.0.1`, so the devcontainer CLI and podman can run builds and tests but cannot browse. Port 4568 is set to `requireLocalPort`, because the Host check accepts only `localhost:4568` and a remapped port would fail every request with 421.
- GitHub Codespaces: build and test work; browsing fails because forwarded URLs are `*.app.github.dev` and the Host check rejects them.
- File-backed features (PO folders, attachments, folder picker, open-folder) use container paths only.
- Windows and macOS: clone into a container volume (or WSL) rather than a bind mount, for Go build speed.

## Licensing

Contributions are accepted under the project's AGPL-3.0 license.

## Changelog

Add one entry per PR under the top version in [`CHANGELOG.md`](CHANGELOG.md).
