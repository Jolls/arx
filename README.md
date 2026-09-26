# Arx

Parts catalog, purchasing, and test-record management for an engineering/manufacturing shop. One Windows desktop app — single executable, no installer, SQL Server backend, running at `http://localhost:4568`.

![Screenshot: Parts view (default `/` route)](docs/screenshots/PartsView.png)

---

## What Arx is

Arx is a **part and quality-record-centric ERP** for engineering and manufacturing shops. Instead of bolting quality onto a parts database as an afterthought, Arx follows each part through its whole life — from the first drawing to the final tested assembly — and keeps a change-controlled record (future feature) at every step. A part number is designed and documented, purchased, received into a traceable lot, inspected, assembled from a bill of materials, and finally tested as an individual unit. At each stage the paperwork that proves what happened — the PO, the receipt, the inspection, the test results — lives in one place and traces back to the exact lots and parts it came from.

```mermaid
flowchart LR
    A["<b>Design</b><br/>part numbers<br/>drawings · BOMs"]
    B["<b>Purchase</b><br/>RFQs · POs"]
    C["<b>Receive</b><br/>into lots"]
    D["<b>Inspect</b><br/>incoming quality"]
    E["<b>Assemble</b><br/>build from BOM"]
    F["<b>Final Test</b><br/>per unit"]
    A --> B --> C --> D --> E --> F
```

The result is end-to-end traceability: pick up any finished unit and follow it back through the assemblies, lots, suppliers, and test records that produced it.

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

Requires Go 1.27+ and a SQL Server instance.

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
4. Connection settings are saved to `config\local.json`; the DB password is saved per-user to `%APPDATA%\Arx\local.json`. Neither is committed.

---

## Configuration

| Source | Notes |
|---|---|
| `.env` (repo root or `arx_go/`) | Non-secret config: `TEST_MODE`, `DEBUG_MODE` |
| `config/local.json` | Connection settings — always wins over env, gitignored |
| `%APPDATA%\Arx\local.json` | Per-user secrets (DB password, session secret) |

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
# ...or use the test-mode connection saved via Settings instead of a DSN
$env:ARX_TEST_FROM_CONFIG="1"; go test -tags integration ./arx_go/...
```

See [`CLAUDE.md`](CLAUDE.md) for schema conventions, branching rules, and architecture decisions, and [`ROADMAP.md`](ROADMAP.md) for what's planned.

---

## License

Copyright (C) 2026 Jolls and contributors. Licensed under the GNU Affero General Public License v3.0 — see [`LICENSE`](LICENSE).
