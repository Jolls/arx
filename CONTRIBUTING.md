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

## Local test database

For manual testing without access to a shared `ArxDev`, run a throwaway Postgres 17 with the schema and test seed. The app always connects with `sslmode=require`, so the server needs a certificate; a self-signed one is fine. Replace `<password>` with one you choose (`podman` works in place of `docker`):

```
docker run -d --name arx-pg -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=<password> -e POSTGRES_DB=ArxDev postgres:17 \
  bash -c 'openssl req -new -x509 -days 30 -nodes -subj /CN=localhost -out /tmp/server.crt -keyout /tmp/server.key 2>/dev/null && chown postgres /tmp/server.* && chmod 600 /tmp/server.key && exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key'
until docker exec arx-pg pg_isready -h 127.0.0.1 -U postgres -d ArxDev; do sleep 1; done
bash SQL/postgres/build_schema.sh | docker exec -i -e PGOPTIONS=--client-min-messages=warning arx-pg psql -U postgres -d ArxDev -q -v ON_ERROR_STOP=1
```

Then in Settings, turn on Test Mode, set DB Server to `127.0.0.1:55432` (the field takes `host:port`), Username to `postgres` and the password you chose, and save. The Test Connection fields can stay blank because they inherit these, and the test database name defaults to `ArxDev`. Log in as `admin`/`admin` or `tester`/`tester` (see [`SQL/SCHEMA.md`](SQL/SCHEMA.md)). The same database works for the integration tests below. Remove it with `docker rm -f arx-pg`.

## Integration tests

`go test -tags integration ./arx_go/...` needs `ARX_TEST_DSN` pointing at a seeded test database (or `ARX_TEST_FROM_CONFIG=1` to use the test-mode connection saved via Settings). The database must be named `ArxDev` (any case); anything else, and anything containing `arxprod`, is refused. The seed data uses fixed IDs that the tests assert against (e.g. part `3005`), so don't renumber seed rows. They are excluded from the default `go test ./...`.

## Licensing

Contributions are accepted under the project's AGPL-3.0 license.

## Changelog

Add one entry per PR under the top version in [`CHANGELOG.md`](CHANGELOG.md).
