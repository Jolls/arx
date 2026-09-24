# Arx — Future Goals

> **Purpose:** Guide future Claude sessions on long-term direction so that schema and design decisions point toward the end state, not away from it. Read this before proposing structural changes. Near-term milestone plan: [`ROADMAP.md`](../ROADMAP.md). Issue numbers are `Jolls/arx` issues; work with no open issue is treated as shipped.

## Long-Term Vision

Arx grows from a parts catalog + purchasing + test data tool into a full engineering shop system.

**Parts & purchasing:**
- Parts catalog with compliance, revision history, alternate parts, and configurable custom fields
- Sourcing layer (AVL, lead times, supplier performance)
- Inventory-driven purchasing: reorder points generating draft POs
- Reporting and data exchange: import/export, labels, dashboards

**Records (test data & quality):**
- Revision-controlled form definitions
- Analytics: yield dashboard, cross-form search

**Platform:**
- Postgres-only, self-hostable (StartOS), with backup/restore
- Audit trail across all tables
- JSON API and mobile-friendly UI

---

## Parts & Purchasing

### Catalog
- Alternate / substitute parts ([#7](https://github.com/Jolls/arx/issues/7))
- Part revision history / ECO log ([#8](https://github.com/Jolls/arx/issues/8)) and the ECO process built on it ([#1](https://github.com/Jolls/arx/issues/1))
- Custom field labels for PNUser1-10 ([#9](https://github.com/Jolls/arx/issues/9)); longer term, replace the flat EAV columns ([#14](https://github.com/Jolls/arx/issues/14))
- RoHS / compliance flags ([#13](https://github.com/Jolls/arx/issues/13))

### Sourcing & purchasing
- AVL attributes on sourcing links ([#4](https://github.com/Jolls/arx/issues/4))
- Lead time per PO line, promised vs actual ([#6](https://github.com/Jolls/arx/issues/6)); feeds supplier performance metrics ([#5](https://github.com/Jolls/arx/issues/5))
- One-click draft PO for below-reorder parts ([#23](https://github.com/Jolls/arx/issues/23))

### Data & audit
- CSV import for parts ([#10](https://github.com/Jolls/arx/issues/10))
- Barcode / QR label generation ([#11](https://github.com/Jolls/arx/issues/11))
- Audit / change log ([#12](https://github.com/Jolls/arx/issues/12)); the existing `test_definition_history` trigger design needs re-examining first for multi-table audit ([#25](https://github.com/Jolls/arx/issues/25))

---

## Records

- Yield dashboard across all forms ([#3](https://github.com/Jolls/arx/issues/3))
- Global record search across all forms ([#2](https://github.com/Jolls/arx/issues/2))
- Evaluate converting result snapshots to revision-controlled definitions ([#18](https://github.com/Jolls/arx/issues/18))

---

## Platform

- Postgres migration ([#21](https://github.com/Jolls/arx/issues/21)): dialect gaps ([#32](https://github.com/Jolls/arx/issues/32), [#33](https://github.com/Jolls/arx/issues/33)), seeded verification ([#28](https://github.com/Jolls/arx/issues/28)), cutover to Postgres-only ([#29](https://github.com/Jolls/arx/issues/29))
- Package Postgres as a StartOS service ([#24](https://github.com/Jolls/arx/issues/24))
- Backup/restore ([#26](https://github.com/Jolls/arx/issues/26))
- JSON endpoints for external clients ([#15](https://github.com/Jolls/arx/issues/15)); mobile-responsive UI ([#16](https://github.com/Jolls/arx/issues/16))
- Migration runner instead of hand-run portal scripts ([#91](https://github.com/Jolls/arx/issues/91)); integration tests in CI ([#19](https://github.com/Jolls/arx/issues/19))
- Structured `app_config` instead of a flat key-value table ([#17](https://github.com/Jolls/arx/issues/17))

---

## Cleanup Candidates

- `is_lot_tracked` dead column ([#31](https://github.com/Jolls/arx/issues/31)); Arx header icon ([#22](https://github.com/Jolls/arx/issues/22))
- **Drop `company.SUWeb` and `company.SUContact1`** (no issue) — dead columns, no Go references (still in `SQL/azure/company.sql`). Verify no orphan references first.
- **Records index query refactor** (no issue) — the `RecordsIndex` SQL in `records.go` is verbose inline SQL; candidate for a view or stored proc once the schema stabilises.
- **Debug fields on `TestStep`** (`ArchiveID`, `Revision`, `Category`, `SheetName`) (no issue) — loaded but only needed during form-def authoring; remove once the edit UI stabilises.
