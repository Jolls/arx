# Inventory Management System — staged design (#272, #274, #269, #273, #7)

> Per-feature design doc (like `f1-user-identity-tracking.md`). The
> [v0.6 PR plan](v0.6-major-feature-release-pr-plan.md) links here; this file holds the architecture
> and stage breakdown. Read before touching inventory/receiving schema.

## Context

PO Receiving (#269, PO-2) can't be built in isolation: received quantities must "feed into stock on
hand (INV-1)", and a credible on-hand number needs an auditable movement log (INV-3). The issues
describe a circular dependency (#269 needs INV-1; INV-1 auto-updates from #269). This design resolves
it by building an **inventory core first** (ledger + cached on-hand + manual adjustments), then
plugging receiving, reorder points, and lot/cycle-counts into it.

Decisions taken: **single-location** stock (one on-hand per part); **`PN.PNQty` is legacy**
(replaced by a ledger-derived `stock_on_hand`; its raw "Quantity" display is removed).

## Architecture (end state every stage builds toward)

- **Ledger is the source of truth.** `inventory_transaction` records every movement
  (receipt / issue / adjustment / count) with a **signed qty** (+receipt, −issue, ±adjustment),
  date, user, reference, optional `po_line_id`. On-hand is always reconstructable as `SUM(qty)`
  (satisfies #274's "running balance from history").
- **`PN.stock_on_hand` is a cached balance** for fast list/dashboard reads, maintained by the app
  **in the same transaction** that writes a ledger row — mirrors the `PO_history` pattern
  (`arx_go/pos.go`, app-written in `beginTx`), not a trigger. (Triggers here are reserved for pure
  denormalized counts like `PNPOLinks`.)
- **Actor = username string**, not a `users.id` FK — consistent with `PO_history.changed_by`,
  `record_events.username`. Audit rows must survive user renames/removal. Reuse `h.actorName(r)`.
- **Stockable categories** reuse the configurable `CategoryTabs` mechanism (`arx_go/models/part.go`):
  add an `Inventory` flag, default **true** for BUY/RAW/MFG/ASM, **false** for DOC/DWG/FORM/SVC/TOOL.
  Editable in Settings via the existing category JSON editor.
- **Units:** transactions in the part's base unit (`PN.PNUNID`). Purchase-pack → base-unit conversion
  on receipt is **deferred** (don't build it).
- **Additive / rollback-safe:** new table + nullable/defaulted columns only. DDL runs manually on
  **ArxProd and ArxDev**; mirror in `SQL/_test.sql`. Idempotent migrations
  (style: `SQL/migrations/migrate_po_approval.sql`).

### Reuse these existing patterns
- `PO_history` write + `fetchPOHistory` + `actorName` (`arx_go/pos.go`) → transaction writes/reads.
- `PartOrders` handler (`arx_go/parts.go:991`) + `templates/pm/part_orders.html` → the Transactions subtab.
- `CategoryTabs` / `TabsForCategory` / `Part.ShowOrders()` (`arx_go/models/part.go`) → subtab gating.
- `po_status_badge` partial (`templates/pm/partials.html`) → txn-type badge.
- `#271` `poTransitions` / `statusIsActive` (`arx_go/pos.go`) → Stage 2 status derivation.

---

## Stage 1 — Inventory Core  (closes #272 INV-1 + #274 INV-3)  ← ✓ DONE (PR #474)

INV-1 and INV-3 are inseparable in a ledger-first design; they ship together.
**Shipped** in PR #474 (commit `7574cd5`): `inventory_transaction` ledger, cached `part.stock_on_hand`,
the `recordInventoryTxn` helper (with its `poLineID` seam for Stage 2), the Transactions subtab, and
the manual stock-adjust action.

**Schema**
- New `SQL/inventory_transaction.sql`:
  `id, part_id INT NOT NULL FK→PN.PNID, txn_type VARCHAR(20) CHECK in (receipt|issue|adjustment|count),
  qty DECIMAL(16,8) NOT NULL (signed), txn_date DATE, username VARCHAR(128), reference VARCHAR(255),
  note VARCHAR(MAX), po_line_id INT NULL FK→POL.POLID, created_at DATETIME`. Index `(part_id, txn_date)`.
  (`po_line_id` nullable from the start so Stage 2 receipts attach with no later schema change.)
- `SQL/part_number.sql`: add `stock_on_hand DECIMAL(16,8) NOT NULL DEFAULT 0`.
- `arxlib/config/config.go`: `InventoryTxnTable() → "inventory_transaction"`.
- `SQL/_test.sql`: drop + `SELECT * INTO`. `SQL/SCHEMA.md`: table + PN note.
- `SQL/migrations/migrate_inventory_core.sql` (idempotent): add column + table. No seed (PNQty legacy).

**Go** (`arx_go/inventory.go` new, + `arx_go/parts.go`)
- `Part.StockOnHand` + `models.CategoryTabs.Inventory` + `Part.ShowInventory()`.
- `PartDetail`/`PartsRows`: select stock_on_hand.
- `recordInventoryTxn(r, tx, partID, txnType, qty, ref, note, poLineID)` shared helper — inserts the
  ledger row and bumps `stock_on_hand`; used by adjustments (Stage 1) and receiving (Stage 2).
- `PartTransactions` `GET /part/{id}/transactions` — ledger newest-first + running balance (Go).
- `PartStockAdjust` `POST /part/{id}/adjust-stock` — qty delta + **required reason** + date.

**Templates:** `part_tabs` Transactions subtab (gated by `ShowInventory`); new `part_transactions.html`;
`part_detail.html` shows stock on hand + **drops the PNQty display**; `settings.html` category editor
gains an Inventory checkbox.

**Tests:** running-balance math; adjustment requires reason; `ShowInventory` per category.

---

## Stage 2 — Receiving / Goods Receipt  (closes #269 PO-2)  ← active PR (Stage 1 ✓ + #271 ✓)

- **Schema:** POL `received_qty DECIMAL(11,2) NOT NULL DEFAULT 0`, `date_received DATE NULL`. Receipts
  live in the ledger (`txn_type='receipt'`, `po_line_id` set, `reference`=PO#) — no separate table.
- **Go** (`arx_go/pos.go`): `POReceive` `POST /po/{id}/receive` — per-line received qty (partial
  allowed); for each line in one tx → `recordInventoryTxn(...'receipt')`, bump `received_qty`/`date_received`.
  Derive #271 status: any line partial → `partially_received`; all full → `closed` (via the status path
  so `PO_history` logs it; makes `partially_received` app-driven). Receipt history on PO detail = ledger
  rows for the PO.
- **Templates:** `po_detail.html` per-line receive controls + Receipts section.
- **Tests:** partial/full/over-receipt; status derivation.

---

## Stage 3 — Reorder Points  (closes #273 INV-2)  ← ✓ DONE

- **Schema:** `PN.reorder_min DECIMAL(16,8) NULL`. (No `reorder_max` — not used by any acceptance
  criterion; would only matter for the draft-PO generator, which is deferred, see below.)
- Edit fields on `part_edit.html`; below-min indicator (`stock_on_hand < reorder_min`) on parts list +
  detail; a below-reorder report, surfaced as a dashboard widget (RPT-1 / #282 already shipped).
- One-click generate **draft** PO for below-min parts — split out to #664, milestoned Far Future.
- **Tests:** below-min boundary.

---

## Stage 4 — Lot/Batch & Cycle Counts  (closes #7)  ← extends Stage 1, stretch

- Extend `inventory_transaction` with optional `lot`/`batch`.
- **Count** action: counted qty → `count` transaction (DateCounted, comments, user) + an `adjustment`
  for the delta. Maps #7's "Inventory Verification (PNID, Inventory, DateCounted, Comments, User)".
- Most speculative; keep last.

---

## Dependency graph

```
Stage 1  Inventory Core (#272 + #274)        ← foundation (manual adjustments only)
   ├── Stage 2  Receiving (#269)  [+ #271 ✓] ← receipts post to the ledger; drive lifecycle
   ├── Stage 3  Reorder points (#273)        ← consumes on-hand; dashboard tile needs #282
   └── Stage 4  Lot/batch + counts (#7)      ← extends the ledger (stretch)
```

## Out of scope / deferred
- Multi-location stock & transfers (single-location chosen; ledger can extend later).
- Purchase-pack → base-unit UoM conversion on receipt.
- ~~Dropping the `PN.PNQty` column~~ — done in Stage 1: column dropped, `schema_version` bumped to v3
  (a breaking change, applied on a weekend with the app idle).

## Verification (per stage, against ArxDev)
1. Apply that stage's migration to ArxDev; `test_mode: true`; `arx_go\build.bat`; run `Arx.exe`.
2. **Stage 1:** stockable part → Transactions tab; post +10 adjustment w/ reason → on-hand 10, ledger +
   running balance correct; a DOC part hides the tab.
3. **Stage 2:** approve+send a PO; receive a line partially → `partially_received`, on-hand rises,
   receipt on PO + in part ledger (ref = PO#); receive rest → `closed`.
4. **Stage 3:** reorder_min above on-hand → flagged below-min on list/detail.
5. `build.bat` green; ledger/balance + status-derivation unit tests added.
