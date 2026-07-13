# #568 — Lot Control & Genealogy

## Status
- **#675 (landed):** generic `build` consume/produce flow, no lot awareness.
- **#676 (landed):** `part.is_lot_tracked`, `lot`, `lot_genealogy`; lot created at goods
  receipt (vendor lot capture) and by a lot-tracked build (output lot + one genealogy
  edge per lot-tracked component consumed). The build's lot machinery engages only when
  the **output** part is lot-tracked. (The issue text called the flag `is_batch_controlled`;
  it landed as `is_lot_tracked` to match this plan and the rest of the epic.)
- **Remaining:** `test_record.lot_id` (step 3 below) and the existing-records backfill.

## Problem
Lot = PO number today, no vendor lot ID field, no way to trace a serialized unit back through assembled lots to raw vendor lots (e.g. final product lot consumes an Assy lot + 3 Reagent lots; Assy lot consumes Film + Disc lots).

## Sequencing

Lot control is a feature *of* a build/consumption flow, not a standalone addition to `test_record`. Nothing populates `lot_genealogy` except a receipt (purchased lots) or a build consuming components into an output (manufactured lots). So:

1. **`build`** — generic consume-components/produce-output flow (no lot control). Confirm first whether any such flow already exists (e.g. informally in `inventory.go`) before assuming it's net-new.
2. **Lot control on top of `build`** — `part.is_lot_tracked`, `lot`, `lot_genealogy`, `vendor_lot_number` on receipt.
3. **`test_record.lot_id`** — last; just a reference to whatever lot a `build` or receipt already produced.

## Schema diagram

```
purchase_order
      │
      ▼
   po_line ───────────────► inventory_transaction (receipt)
      │
      │ (purchased, lot-tracked)
      ▼
     lot ◄──────────────────── part.is_lot_tracked (new)
      │ ▲                        │
      │ │                        │ part_id
      │ │ output lot              │
      │ │ (if lot-tracked)   ▼
      │ └───────────────────── build ──► inventory_transaction
      │                           │        (issue: components,
      │                           │         receipt: output)
      │                           │
      │                     (per bom line,
      │                      component lot-tracked)
      │                           ▼
      │                    lot_genealogy
      │                   ┌──────────────────────┐
      └──────────────────►│ parent_lot_id (FK)    │
                           │ child_lot_id  (FK)    │◄──── (child = build's output lot)
                           │ qty_consumed          │
                           └──────────────────────┘

     lot
      │
      │ lot_id (new, nullable FK)
      ▼
  test_record   (serialized unit → the lot it belongs to)
```

- `lot` = node table (one row per lot instance), FK'd from `po_line` (purchased) or `build` (manufactured).
- `lot_genealogy` = edge table, self-referencing `lot` twice (parent consumed, child produced) — same shape as `bom`'s parent/child part reference, one level down at the instance level.
- `build` is driven by `bom` (which components + quantities) filtered through `part.is_lot_tracked` (which of those components need a lot picked).
- `test_record.lot_id` is a plain FK to whichever `lot` a `build` or receipt already produced — no write-back to genealogy from `test_record` itself.

## Schema changes

- **`part`**: add `is_lot_tracked` BIT NOT NULL DEFAULT 0 — gates which parts require a lot at receipt/build.
- **`lot`** (new): `id` PK, `part_id` → `part.id`, `lot_number` (internal lot #, defaults to PO number for purchased lots but editable), `vendor_lot_number` (nullable, vendor's own ID), `po_line_id` (nullable FK, set for purchased receipts, NULL for manufactured lots), `created_at`, `is_active`.
- **`lot_genealogy`** (new): `id` PK, `parent_lot_id` → `lot.id`, `child_lot_id` → `lot.id`, `qty_consumed`. Many-to-many — a child lot can consume multiple parent lots (Assy = Film + Disc); a parent lot can feed multiple child lots.
- **`test_record`**: add `lot_id` (nullable FK → `lot.id`) — ties a serialized unit to the lot it came from.
- **`build`** (new, scaffolding only — not scoped this pass): consumes component inventory (`inventory_transaction` issues, per `bom` line) and produces output inventory (`inventory_transaction` receipt) for one parent part. Writes `lot_genealogy` per component line where the component is lot-tracked; writes a new `lot` row for the output if the output part is lot-tracked.

## Use cases

1. **Receipt of a lot-tracked purchased part** — creates a `lot` row, `lot_number` defaults to PO number, user can enter `vendor_lot_number`.
2. **Build consumes component lots into a new lot** — `build` creates a `lot` row for the output part, plus one `lot_genealogy` row per lot-tracked component consumed.
3. **Serialize a unit within a lot** — `test_record.lot_id` set at record creation, referencing the `lot` a prior `build` or receipt already produced.
4. **Forward/backward traceability** — recursive query from a serial number's `lot_id` up through `lot_genealogy` to every raw vendor lot that fed it (or the reverse: every lot/serial downstream of a given vendor lot).
5. **Final disposition of a lot (sold/consumed/scrapped)** — no new mechanism; just an `inventory_transaction` adjustment/issue row with a `note`, same as any other stock-out.

## Migration path
Existing `test_record` rows predate `lot_id` and have no `lot` row to point at — they only have the PO-number-as-lot convention embedded in free text. A one-time backfill script would need to: create a `lot` row per distinct PO number referenced by existing lot-tracked parts' records, then set `test_record.lot_id` accordingly. Scope and script not written yet — flag as a migration task once the schema lands, not part of this design pass.

## Open questions
- Does any build/consumption flow already exist (even informally) to extend, or is `build` fully net-new?
- `build` table shape, status lifecycle, partial builds/backorders — not scoped.
- UI entry points (receipt form, build/consumption flow) not yet designed.
- Backfill script for existing `test_record` rows (see Migration path) — not written yet.
