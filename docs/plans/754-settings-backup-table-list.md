# #754 — Complete Settings backup table list + fix LinksTable (H5)

## Findings

### 1. `LinksTable()` bug — `arxlib/config/config.go:212`
```go
func (c *Config) LinksTable() string              { return "LNK" }
```
`LNK` was renamed to `supplier_part` in commit `93ca837` ("feat: rename LNK -> supplier_part, add mfg_part, modernize schema (#225)"). Confirmed via `SQL/SCHEMA.md:153` — `supplier_part` is documented as "Sourcing links — maps parts to supplier catalog entries."

`SupplierPartTable()` already exists at `arxlib/config/config.go:200` and returns `"supplier_part"` — identical to what the fixed `LinksTable()` would return. `LinksTable()` is used in exactly one place: `arx_go/settings.go:553` (the backup table list). No other callers (confirmed via repo-wide grep for `LinksTable()`).

**Fix:** in `arxlib/config/config.go:212`, change:
```go
func (c *Config) LinksTable() string              { return "LNK" }
```
to:
```go
func (c *Config) LinksTable() string              { return "supplier_part" }
```
(Leave `SupplierPartTable()` at line 200 untouched — even though the two helpers now return the same string, removing/consolidating either is out of scope for this issue; CLAUDE.md rule 3 says touch only what's needed.)

### 2. Missing backup tables — helper methods already exist
All six tables named in the issue already have `cfg.*Table()` helpers in `arxlib/config/config.go` — none need to be added:

| Table (issue name) | Existing helper | Line | Returns |
|---|---|---|---|
| `inventory_transaction` | `InventoryTxnTable()` | 205 | `"inventory_transaction"` |
| `build` | `BuildTable()` | 206 | `"build"` |
| `lot` | `LotTable()` | 207 | `"lot"` |
| `lot_genealogy` | `GenealogyTable()` | 208 | `"genealogy"` |
| `purchase_order_history` | `POHistoryTable()` | 198 | `"purchase_order_history"` |
| `record_event_results` | `RecordEventResultsTable()` | 223 | `"record_event_results"` |

Note: the table the issue calls `lot_genealogy` was renamed to `genealogy` in migration `SQL/migrations/migrate_741_genealogy_table.sql` (#741, part of the traceability epic #736) and widened to also cover unit-to-unit provenance, not just lots. The corresponding Go helper is `GenealogyTable()`, not `LotGenealogyTable()` — the issue's helper name is stale relative to #741. Use `GenealogyTable()`.

### 3. Backup list — `arx_go/settings.go:550-559`
Current:
```go
tables := []string{
    h.cfg.PartsTable(), h.cfg.BOMTable(), h.cfg.CompanyTable(),
    h.cfg.ContactTable(), h.cfg.POTable(), h.cfg.POLineTable(),
    h.cfg.AttachmentsTable(), h.cfg.LinksTable(), h.cfg.PriceTable(),
    h.cfg.MfgPartTable(), h.cfg.SupplierPartTable(), h.cfg.CompanyAttachmentsTable(),
    h.cfg.UomTable(), h.cfg.AppConfigTable(),
    h.cfg.FormsTable(), h.cfg.RecordsTable(), h.cfg.ResultsTable(),
    h.cfg.StepsTable(), h.cfg.FormEventsTable(), h.cfg.RecordEventsTable(),
    h.cfg.NamedQueriesTable(), h.cfg.FormRowHistoryTable(),
}
```
Missing all six tables from the issue.

**Decision:** `zw.Create(table + ".csv")` (settings.go:602) names the zip entry directly from
the table string, so after fix #1 both `LinksTable()` and `SupplierPartTable()` calls would
create `supplier_part.csv` twice in the same zip — not a crash, but wasted work and
confusing/duplicate output in a backup users trust. Resolution: drop the `h.cfg.LinksTable()`
call from the `tables` slice (line 553), keeping only `h.cfg.SupplierPartTable()` (line 554).
`LinksTable()` itself is still fixed per #1 (in case something else calls it later), but its
call site in the backup list is removed rather than kept alongside `SupplierPartTable()`.

Updated slice (line 553's `h.cfg.LinksTable()` call is deleted, not just left as-is, and the
six new tables are appended):
```go
tables := []string{
    h.cfg.PartsTable(), h.cfg.BOMTable(), h.cfg.CompanyTable(),
    h.cfg.ContactTable(), h.cfg.POTable(), h.cfg.POLineTable(),
    h.cfg.AttachmentsTable(), h.cfg.PriceTable(),
    h.cfg.MfgPartTable(), h.cfg.SupplierPartTable(), h.cfg.CompanyAttachmentsTable(),
    h.cfg.UomTable(), h.cfg.AppConfigTable(),
    h.cfg.FormsTable(), h.cfg.RecordsTable(), h.cfg.ResultsTable(),
    h.cfg.StepsTable(), h.cfg.FormEventsTable(), h.cfg.RecordEventsTable(),
    h.cfg.NamedQueriesTable(), h.cfg.FormRowHistoryTable(),
    h.cfg.InventoryTxnTable(), h.cfg.BuildTable(), h.cfg.LotTable(),
    h.cfg.GenealogyTable(), h.cfg.POHistoryTable(), h.cfg.RecordEventResultsTable(),
}
```
