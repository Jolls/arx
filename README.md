# Arx

Parts catalog, purchasing, and test-record management for an engineering/manufacturing shop. One Windows desktop app — single executable, no installer, SQL Server backend, running at `http://localhost:4568`.

![Screenshot: Parts view (default `/` route)](docs/screenshots/PartsView.png)

---

## Features

Arx is organized into a few sections, navigable from a single top nav bar:

- **Parts** — catalog with CSV export, create/edit/duplicate, BOM (view/edit/CSV export, rollup and build cost), where-used lookups, attachments (incl. paste-from-clipboard, primary attachment), order history, price history and qty-break pricing tiers, stock adjustments/transactions, and manufacturer part cross-references.
- **Vendors** — supplier list, detail/edit, linked parts, PO history, attachments, folder browsing.
- **POs** — purchase order list with CSV export, create/edit/duplicate, status transitions, receiving, approvals, notes, print-to-PDF, folder browsing, and an RFQ workflow (create RFQ, add supplier quotes, compare, convert to PO).
- **Contacts** — list, detail, create, edit.
- **Records** — production test data capture for manufactured units. Engineers define test procedures as forms (steps with pass/fail criteria, spec limits, conditional visibility, result formats). Technicians fill them out per serial number, recording measured values and results. Records can be locked/approved, printed to PDF, bulk-locked, and include an image gallery. Form definitions keep a full edit history; records keep a per-record audit trail.
- **Settings** — DB connection, attachment/part categories, part numbering, company logo, named queries, user management, per-user preferences (e.g. default PO receiver/contact), backup, utilities report.

![Screenshot: Records / test form](docs/screenshots/records.png)

Cross-cutting: session-based login, local file/folder browsing for attachments, search/autocomplete across parts, suppliers, and contacts.

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
| `config/local.json` | DB password — always wins, gitignored |

`DEBUG_MODE=true` opens a console window logging SQL queries and per-route round-trip counts/timings, and surfaces extra debug info in some pages.
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

---

## License

Copyright (C) 2026 Jolls. Licensed under the GNU Affero General Public License v3.0 — see [`LICENSE`](LICENSE).
