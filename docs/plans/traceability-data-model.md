# Traceability Data Model — Part → Lot → Unit

**Status:** DRAFT / not frozen. This is the discussion surface for the v0.7 traceability
redesign. Nothing here is committed until the "Freeze" checklist at the bottom is checked
and an epic issue is filed. Sub-issues are carved **after** freeze, from the migration path,
not from the target schema.

**Epic issue:** _TBD_ (file once frozen; rename this doc to `<epic#>-traceability-data-model.md`).

**Supersedes:** the `trace_id` / `is_batch` issue set — #702 (parent design thread), #713
(serial_number→trace_id rename), #714 (is_batch + compound trace_id), #715 (build detail view),
#716 (embedded build UX). Those are to be **closed as superseded** (not deleted — the reasoning
and the rejected dead-ends are the audit trail), each pointing back at the epic.

**Origin:** design artifact "Parts · Lots · Testing — from-scratch data model" (three-tier
identity) plus the review of the #702 thread. This doc reconciles that greenfield model against
the *existing* Arx schema and splits the work into target-state vs. migration.

---

## 1. Goal (milestone v0.7)

Integrate the full sequence of testing **from receipt through final testing of individual
assemblies**, in a change-controlled manner. Concretely the model must represent, as first-class
data (not parsed strings or proxy flags):

- Incoming inspection of a purchased lot.
- In-process / post-assembly testing of a build.
- Final testing of an **individual serialized assembly**.
- Retest of one failed unit drawn from a batch.
- The provenance chain: which raw lots → which build → which output lot/units → which test events.

## 2. Core thesis (from the artifact)

Identity is a three-tier hierarchy, each tier a **real table**, never one overloaded string:

| Tier | Table | Answers | Exists today? |
|------|-------|---------|---------------|
| 1 · catalog | `part` | what a thing *is* | yes |
| 2 · batch instance | `lot` | which batch | yes (#676) |
| 3 · serialized instance | **`unit`** (new) | which exact one | **no — this is the gap** |

The `unit` table is the piece Arx lacks, and its absence *is* the whole `trace_id` / `is_batch`
struggle in #702. A serial number becomes a **row with real FKs** (`part_id`, nullable `lot_id`,
nullable `build_id`), not a parsed string like `LOT123.4`.

Two nullable FKs carry the model:

- **`unit.lot_id` nullable** — set for `lot_serial` parts (a serialized unit inside a lot);
  NULL for serial-only parts with no batch. One `unit` table serves both.
- **per-unit result FK nullable** — a whole-lot measurement leaves it NULL; a per-unit reading
  points at the covered unit. One results table carries both granularities, no separate batch
  structure.

"Is this a batch?" needs **no flag**: an inspection is a batch because it links a lot and/or has
per-unit coverage rows. Retest of unit 4 = a new inspection with one coverage row pointing at that
same `unit_id`. No compound string, no ambiguous delimiter, no `is_batch` boolean.

## 3. ⚠️ Naming collision — `unit` is already taken

`unit` **already exists** as the unit-of-measure reference table (`part.unit_id → unit.unit_id`,
`supplier_part.unit_id`). The Tier-3 table **cannot** be named `unit` as-is. Options:

- **(A)** Rename existing UoM table `unit` → `uom` (and `part.unit_id` → `uom_id`,
  `supplier_part.unit_id` → `uom_id`) **first**, freeing the name — this is exactly what **#712**
  proposed. Under this option #712 becomes an **epic prerequisite**, not an orthogonal cleanup.
- **(B)** Name the Tier-3 table something else — `serialized_unit`, `unit_instance`, or `item` —
  and leave UoM alone. Zero rename blast radius, but the model's vocabulary drifts from the artifact.

**OPEN Q1 — decide the Tier-3 table name (and whether #712 is a prerequisite).**

## 4. Target schema (end-state, grounded to current tables)

Only the identity + testing core. Inventory ledger, BOM, purchasing, and pricing tables are real
but out of scope here **except** where §6 open questions force an interaction.

### 4.1 New / changed tables

```
part            (existing)  + tracking_mode   -- replaces/extends is_lot_tracked
lot             (existing)  + source enum      -- purchase|build|adjust (today inferred from po_line_id)
unit            (NEW, Q1)   id, part_id FK, lot_id FK?, build_id FK?, serial_number VARCHAR, is_active
                            -- serial_number is a STRING (shops use non-numeric serials); UNIQUE per part_id
inspection      (= test_record, reshaped)      form_id, part_id, lot_id?, build_id?, unit_id?  -- disposition derived (Q3)
result          (= test_result, reshaped)      inspection_id, test_id, pass_fail   -- unit granularity via inspection.unit_id (Q8)
form            (existing)  + inspection_type   -- incoming|in_process|final; part_number_id stays (it's the FORM's own PN)
test_definition (existing)  + granularity       -- lot|unit
build           (existing, unchanged)
lot_genealogy   (existing, unchanged)
```

### 4.2 Old → new mapping (for reviewers who know today's schema)

| Today | Target | Note |
|-------|--------|------|
| `part.is_lot_tracked` (bool) | `part.tracking_mode` enum `none\|lot\|serial\|lot_serial` | adds the serial axis Arx has no concept of today |
| `test_record` | `inspection` | rename + reshape (see §5 on whether the rename is worth its blast radius) |
| `test_record.serial_number` | **gone** — replaced by `unit` rows + nullable `inspection.unit_id` (Q8) | this is the whole point; kills the `trace_id`/`is_batch` idea |
| `test_record.serial_number_pn` / `_pn_desc` | denormalized snapshots, keep (retarget name, see #717) | *not* serials; the `serial_number_*` prefix was always wrong |
| `test_result` | `result` + nullable per-unit FK | one table, both granularities (whole-lot vs unit) |
| `unit` (UoM) | `uom` | renamed to free `unit` for Tier-3 (Q1, absorbs #712) |
| `form` (record_types, instrument_types, revision) | `form` + `inspection_type` | change-control columns **stay** (Q4); `part_number_id` unchanged — it's the FORM's own PN |
| `test_definition` (spec_*, pf_type, archived, history) | `test_definition` + `granularity` | change-control machinery **stays** (see OPEN Q4) |
| `lot` (po_line_id NULL ⇒ built) | `lot.source` explicit | today source is inferred; make it a column |

### 4.3 What the artifact's ERD *omitted* and we must not

The artifact footer trims inventory/BOM/UoM "out of scope." Those omissions are exactly where the
hard design is — captured as open questions in §6.

## 5. Migration philosophy — "from scratch" is the *design*, not the *rewrite*

The design starts clean; the **implementation migrates a live DB with a ~2200-line `records.go`
built on `test_record`/`test_result` column names.** Do not let greenfield leak into big-bang.
Adoption order (Q7 resolved — renames first, in isolation):

1. **Rename wave, isolated PR(s) (Q7 + Q1):** `test_record`→`inspection`, `test_result`→`result`,
   and UoM `unit`→`uom` (`part.unit_id`/`supplier_part.unit_id`→`uom_id`, absorbing #712). Pure
   renames, no behavior change — stand up and test the reshaped DB before anything is built on it.
   Get the churn against `records.go` out of the way while behavior is provably unchanged, and free
   the `unit` name for step 2.
2. **Additive keystone:** add the `unit` table (Tier-3), a nullable per-unit FK on `result`, and
   `tracking_mode` on part. Units are created **lazily, one at a time at test time** (Q5) —
   independent of inventory (Q2). Pending Q8: nullable `inspection.unit_id` rather than an
   `inspection_unit` m2m.
3. **Enum formalization:** `lot.source`, `form.inspection_type`, `test_definition.granularity`.
4. **Behavior:** derive batch-vs-unit from structure; completeness "N tested of build.qty" (Q6);
   retest = a unit-testing inspection pointing at the same unit.

Throughout, honor the Q4 constraint: don't strand the revision-control snapshot columns or the
`test_definition_history` trigger — leave real change control (v0.9) easy to add later.

## 6. Open questions (resolve before freeze)

- **Q1 — Tier-3 table name / #712 prerequisite. → RESOLVED: rename UoM `unit`→`uom`.** Absorb #712
  as an epic prerequisite (part of the early rename wave, §5/§7). This frees `unit` for the
  Tier-3 serialized-instance table and keeps the artifact's vocabulary.
- **Q2 — Unit ↔ inventory / stock. → RESOLVED: fully decoupled.** `stock_on_hand` stays
  `SUM(inventory_transaction.qty)`, unchanged. **Unit count is independent of inventory; inventory
  needs no knowledge of units, and no `unit` row is ever written in an inventory transaction.**
  There is no reconciliation requirement — if a build makes 20 and only 19 are ever tested, there
  are 19 `unit` rows and that's correct; the 20th is simply untested/unaccounted. A future flag or
  report could surface a units-vs-build-qty mismatch, but that is **out of scope** for v0.7.
- **Q3 — `disposition`: computed or stored? → RESOLVED: computed/derived.** Keep Arx's current
  approach — derive pass/fail from results + spec; do not store a `disposition` column that can
  drift. `inspection`/`inspection_unit` roll-up is computed at read time, not persisted.
- **Q4 — Change control placement. → RESOLVED (scope): deferred to v0.9.** Full change control is a
  later (v0.9) feature. v0.7 keeps the **existing limited revision control** as-is
  (`form.revision`, `test_record`/`inspection`.`form_revision` snapshot, `test_result`/`result.*`
  snapshots, `test_definition_history`). **Constraint on this epic:** every schema decision here
  must leave adding real change control later *easy*, not foreclosed — don't reshape `inspection`/
  `result` in a way that strands the snapshot columns or the history trigger. Carry this as a
  review lens on every sub-issue, not as work in v0.7.
- **Q5 — Unit lifecycle. → RESOLVED: lazy, one at a time, at test time.**
  - A `unit` row is created **one at a time**, when that individual unit is tested (unit testing).
    **No bulk/eager add** — a build of 20 does *not* pre-create 20 unit rows.
  - Consequence: unit-row count is the *numerator* of completeness, not the denominator
    (see revised Q6). Units are independent of both inventory (Q2) and lot/build quantity.
  - Units exist **only** for `tracking_mode` `serial` / `lot_serial`, and only for items actually
    unit-tested — which naturally bounds row volume.
  - `is_active` for scrap; `serial_number` entered at test time; editable until the unit's first
    *locked* inspection; uniqueness scope TBD (per-part vs per-lot).
  - **Batch vs unit testing:** unit testing = one inspection per unit → one `unit` row, full
    per-parameter results. Batch testing = one lot/build-granularity inspection covering many units
    at once (whole-lot y/n), which does **not** enumerate or create per-unit rows.
- **Q6 — Completeness "19 of 20 tested". → RESOLVED, denominator relocated.** Track completeness as
  `COUNT(units for the build/lot) / build.qty (or lot qty)`. The **denominator is the build/lot
  quantity, which already exists** (this is the old thread's correct "qty lives on the build"
  insight); the **numerator is the count of unit-tested units**. Missing units (built-but-untested)
  are simply absent from the unit table — a future report can surface the gap (out of scope). This
  supersedes both the old thread's "don't reconcile at all" *and* the earlier draft's mistaken
  "eager-create all N units" — neither is needed.
- **Q7 — Renames worth it? → RESOLVED: yes, and done early in isolation.** `test_record`→
  `inspection` and `test_result`→`result` ship as their **own early PR** — pure rename, no behavior
  change, so the reshaped DB can be stood up and tested in isolation before the new tables are built
  on top of the clean names. This moves the renames to the *front* of the epic (see §5/§7), not the
  end.
- **Q8 — `inspection_unit`. → RESOLVED: not m2m; use a nullable `inspection.unit_id` FK.** An
  inspection covers at most one specific serialized unit (unit testing); batch testing is whole-lot
  and enumerates no individual units. So there is no inspection↔unit many-to-many — drop the
  `inspection_unit` table, add a nullable `inspection.unit_id`. Retest = a unit-testing inspection
  with `unit_id` set.
- **Q9 — Form ↔ Assembly many-to-many. → RESOLVED: already exists, no schema change.** A **Form is
  itself a part** (`part.category = 'FORM'`); `form.part_number_id` links to the FORM's *own* part
  number (inspection/test plans have PNs) — **not** to the assembly under test. The FORM part carries
  a **BOM**, whose components are the assembly part numbers the form applies to. So Form → many
  assemblies (its BOM lines) and assembly → many Forms (it appears in many FORM parts' BOMs). The
  m2m is provided by the existing (Form-is-a-part + BOM) mechanism. **No `form_part` junction, no
  drop of `form.part_number_id`.** An `inspection` still carries both `part_id` (assembly under test)
  and `form_id`; applicability is validated by the form's BOM containing that part.

## 7. Sub-issue carving (DRAFT — finalize after freeze)

Carved from the migration path (§5), one shippable guarded-migration slice each, dependency order:

1. **Rename wave** (Q7 + Q1) — `test_record`→`inspection`, `test_result`→`result`, UoM
   `unit`→`uom` (absorbs #712). Pure renames, isolated, tested standalone. Front of the epic; may be
   one PR or split (TR-rename vs UoM-rename) since they're independent.
2. **`unit` table** (DDL both dialects, seed, `*Table()` helper, SCHEMA.md, migration) — keystone.
   Lazy one-at-a-time creation at test time (Q5), independent of inventory (Q2).
3. **Per-unit result FK** — nullable `inspection.unit_id` (pending Q8; drops the `inspection_unit`
   m2m) + nullable per-unit FK on `result`.
4. **`tracking_mode`** on part (migrate `is_lot_tracked` values).
5. **Enum formalization** (`lot.source`, `form.inspection_type`, `test_definition.granularity`).
6. **Create/render logic** — derive batch-vs-unit from structure; completeness "N of build.qty"
   (Q6); retest = a unit-testing inspection pointing at the same unit.
7. **Build/lot/unit traceability view** (re-scoped #715 — read-only drill-down + indexes).
8. _(optional)_ embedded build-at-test-time UX (re-scoped #716) — highest risk, last.

## 8. Stays outside the epic

- **#717** — `serial_number_pn` / `_pn_desc` rename. Still valid (they're not serials), but the
  target name must avoid the new `unit` vocabulary — retarget to e.g. `tested_part_number` /
  `tested_pn_description`, **not** `unit_part_number`.
- **#712** — **now absorbed into the epic** (Q1 = A). It's the UoM half of the step-1 rename wave,
  not an outside cleanup. Close/relabel it accordingly when the epic is filed.

## 9. Freeze checklist

- [x] Q1–Q7 resolved and recorded inline (Q1 uom-rename, Q2 decoupled, Q3 derived, Q4 v0.9-deferred,
      Q5 lazy one-at-a-time, Q6 denominator=build.qty, Q7 renames early).
- [x] Q8 resolved (no `inspection_unit`; nullable `inspection.unit_id`).
- [x] Q9 resolved (Form↔Assembly m2m already exists via Form-is-a-part + BOM; no schema change).
- [x] §4 detail settled: `unit.serial_number` is a **string, UNIQUE per `part_id`**;
      `tracking_mode` migration maps `is_lot_tracked` `0→none`, `1→lot` (`serial`/`lot_serial` set
      per-part afterward; existing data implies neither).
- [ ] Migration slices §7 each map to exactly one guarded migration.
- [ ] Epic issue filed; this doc renamed with its number; old #702 set closed as superseded.
