# Arx

Parts catalog and purchasing management for an engineering shop. One Windows desktop app — single executable, no installer, SQL Server backend.

**Parts Master** — parts catalog, suppliers, purchase orders, attachments, BOM, pricing history  
**Test Records** — production test data capture for manufactured units. Engineers define test procedures as forms (steps with pass/fail criteria, spec limits, conditional visibility, and result formats). Technicians fill them out per serial number, recording measured values and pass/fail results. Records can be locked when complete, printed to PDF, and include an image gallery for photos or screenshots taken during the test. Supports form definition history and per-record audit events.

Both apps run together at `http://localhost:4568`.

---

## Build

Requires Go 1.22+ and a SQL Server instance.

```bat
cd arx_go
build.bat
```

Outputs `arx_go\Arx.exe`. Runs all tests before building.

---

## First Run

1. Copy `Arx.exe` to a folder and run it. A tray icon appears.
2. Open `http://localhost:4568` in a browser.
3. The app redirects to `/settings` — enter your SQL Server connection details.
4. Credentials are saved to `config\local.json` (never committed).

---

## Configuration

| Source | Notes |
|---|---|
| `.env` (repo root or `arx_go/`) | Non-secret config: `TEST_MODE`, `DEBUG_MODE` |
| `config/local.json` | DB password and PO defaults — always wins, gitignored |

`DEBUG_MODE=true` logs all SQL queries to the terminal.  
`TEST_MODE=true` connects to the `ArxDev` database instead of production.

---

## Development

```powershell
# Unit tests (no DB required)
cd arx_go
go test ./...

# Integration tests (live ArxDev DB)
$env:ARX_TEST_DSN="sqlserver://user:pass@server?database=ArxDev&encrypt=true"
go test -tags integration ./arx_go/...
```

See [`CLAUDE.md`](CLAUDE.md) for schema conventions, branching rules, and architecture decisions.
