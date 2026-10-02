# Plan: dev container wired to the compose DB (#302)

Part of #260. Depends on #300 (`SQL/postgres/compose.yml`, landed) and #301 (`ARX_HEADLESS=1`, assumed landed first on this branch).

## Facts the plan relies on
- Test mode (`TEST_MODE=true`) connects to `TestDBName` (default `ArxDev`); `TestDBServer`/`TestDBUser`/`TestDBPassword` are blank so they inherit `DB_SERVER`/`DB_USER`/`db_password` (`internal/config/base.go`).
- `db_password` is read only from the per-user secrets file `os.UserConfigDir()/Arx/local.json` (`~/.config/Arx/local.json` on Linux), key `db_password` (`internal/config/secrets.go`). No env var.
- `config/local.json` resolves relative to the process cwd, so the app runs from `arx_go/`.
- `RequireLocalHost` (`arx_go/handlers.go`) accepts only `localhost|127.0.0.1|[::1]:<port>`.
- Compose merges multiple files with relative paths resolved against the FIRST file's directory (`SQL/postgres/`), so paths in `.devcontainer/compose.yml` are written relative to `SQL/postgres/`.
- Seeded logins: `admin`/`admin`.

## File changes

### 1. `.devcontainer/Dockerfile` (new)
```dockerfile
FROM golang:1.27

ARG TARGETARCH
# Keep in sync with sqlc-version in .github/workflows/test.yml (Dependabot cannot bump this ARG).
ARG SQLC_VERSION=1.31.1

RUN apt-get update \
    && apt-get install -y --no-install-recommends pkg-config libayatana-appindicator3-dev postgresql-client \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://downloads.sqlc.dev/sqlc_${SQLC_VERSION}_linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin sqlc

# The official golang image sets GOTOOLCHAIN=local; let go.mod pick the Go version.
ENV GOTOOLCHAIN=auto
```

### 2. `.devcontainer/compose.yml` (new)
```yaml
# Adds the dev service next to the db service from SQL/postgres/compose.yml (#302).
# Paths are relative to SQL/postgres/ (the first compose file), not this directory.
services:
  dev:
    build:
      context: ../../.devcontainer
    command: sleep infinity
    volumes:
      - ../..:/workspaces/arx:cached
    depends_on:
      db:
        condition: service_healthy
```

### 3. `.devcontainer/devcontainer.json` (new)
```json
{
  "name": "Arx",
  "dockerComposeFile": ["../SQL/postgres/compose.yml", "compose.yml"],
  "service": "dev",
  "workspaceFolder": "/workspaces/arx",
  "containerEnv": {
    "TEST_MODE": "true",
    "DB_SERVER": "db:5432",
    "DB_USER": "postgres",
    "ARX_TEST_DSN": "postgres://postgres:postgres@db:5432/ArxDev?sslmode=require"
  },
  "postCreateCommand": "mkdir -p ~/.config/Arx && printf '{\"db_password\":\"postgres\"}' > ~/.config/Arx/local.json && chmod 600 ~/.config/Arx/local.json",
  "forwardPorts": [4568],
  "portsAttributes": {
    "4568": { "requireLocalPort": true }
  },
  "customizations": {
    "vscode": { "extensions": ["golang.go"] }
  }
}
```

### 4. `.vscode/tasks.json` (new; `.vscode` is not gitignored)
```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "Run Arx (headless)",
      "type": "shell",
      "command": "go run .",
      "options": {
        "cwd": "${workspaceFolder}/arx_go",
        "env": { "ARX_HEADLESS": "1" }
      },
      "problemMatcher": []
    }
  ]
}
```

### 5. `.github/workflows/devcontainer.yml` (new; `paths` filters apply per workflow, so `test.yml` is not edited)
```yaml
name: devcontainer

on:
  pull_request:
    paths:
      - '.devcontainer/**'
      - 'go.mod'
      - 'sqlc.yaml'
      - 'SQL/postgres/compose.yml'
      - '.github/workflows/devcontainer.yml'
  push:
    branches: [main]
    paths:
      - '.devcontainer/**'
      - 'go.mod'
      - 'sqlc.yaml'
      - 'SQL/postgres/compose.yml'
      - '.github/workflows/devcontainer.yml'

permissions:
  contents: read

jobs:
  build-in-devcontainer:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7

      - name: Build dev image
        run: docker compose -f SQL/postgres/compose.yml -f .devcontainer/compose.yml build dev

      - name: go build inside the dev image
        run: docker compose -f SQL/postgres/compose.yml -f .devcontainer/compose.yml run --rm --no-deps dev go build ./...
```

### 6. `.github/dependabot.yml`
Append:
```yaml

  - package-ecosystem: docker
    directory: /.devcontainer
    schedule:
      interval: weekly

  - package-ecosystem: docker-compose
    directory: /SQL/postgres
    schedule:
      interval: weekly

  - package-ecosystem: devcontainers
    directory: /
    schedule:
      interval: weekly
```

### 7. `CONTRIBUTING.md`
Insert a new `## Dev container` section after `## Integration tests` and before `## Licensing`:
```markdown
## Dev container

`.devcontainer/` gives a Go toolchain, the systray build dependencies, `sqlc`, `psql` and the seeded `ArxDev` database from `SQL/postgres/compose.yml` (reachable as `db:5432`), with no manual setup. In VS Code use "Reopen in Container". `go build ./...`, `go test ./...` and `go test -tags integration ./arx_go/...` work as-is.

- Start the app with the "Run Arx (headless)" task (Terminal > Run Task). Browse to `http://localhost:4568` and log in as `admin`/`admin`.
- Browsing works only through VS Code port forwarding. The listener binds `127.0.0.1`, so the devcontainer CLI and podman can run builds and tests but cannot browse. Port 4568 is set to `requireLocalPort`, because the Host check accepts only `localhost:4568` and a remapped port would fail every request with 421.
- GitHub Codespaces: build and test work; browsing fails because forwarded URLs are `*.app.github.dev` and the Host check rejects them.
- File-backed features (PO folders, attachments, folder picker, open-folder) use container paths only.
- Windows and macOS: clone into a container volume (or WSL) rather than a bind mount, for Go build speed.
```

## Test plan
1. Coverage audit: No tests: config/docs only, no Go code changed.
2. Characterization tests: No tests: no existing Go behavior is touched.
3. Red tests: No tests: no Go behavior is added.
4. Manual-only (user runs):
   - VS Code: "Reopen in Container"; in the terminal run `go build ./...`, `go test ./...`, `go test -tags integration ./arx_go/...` (restart the `db` service between integration runs); `sqlc version` prints 1.31.1; `go version` matches `go.mod`.
   - Run task "Run Arx (headless)"; open `http://localhost:4568` from the host browser; confirm the forwarded local port is exactly 4568 and `admin`/`admin` logs in.
   - devcontainer CLI: `devcontainer up --workspace-folder .` then `devcontainer exec --workspace-folder . go test ./...` and `... go test -tags integration ./arx_go/...`.
   - podman: same CLI steps with `--docker-path podman --docker-compose-path "podman compose"` (or `DOCKER_HOST` pointing at the podman socket).
   - Apple Silicon / arm64 (if available): image builds and `sqlc version` runs.
   - CI: open the PR with a `.devcontainer/` change; confirm the `devcontainer` workflow runs and passes, and does not run on a PR touching neither the filtered paths.
   - Dependabot: after merge, confirm the three new ecosystems appear in Insights > Dependency graph > Dependabot.

## Resolved decisions
- Headless start: `.vscode/tasks.json` task ("Run Arx (headless)"), not a launch config.
- Fixed `container_name: arx-test-db` stays; dev container and host compose recipe can't run simultaneously: accepted, no change to #300's compose.
- sqlc version: `ARG` in the Dockerfile, synced by hand with `test.yml`; no extra CI check.
- Separate `.github/workflows/devcontainer.yml` (paths filters are per workflow) building the image and running `go build ./...` via docker compose: accepted.
- `golang:1.27` tag assumed as the issue specifies.
