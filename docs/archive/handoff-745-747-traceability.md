# Handoff — Traceability slices 8–10 (#745 / #746 / #747)

**Branch:** `feature/745-747-traceability-render-view-buildux`
**Epic:** [#736](https://github.com/Jolls/arx-legacy/issues/736) (Part → Lot → Unit traceability)
**Status:** implemented, tested on ArxDev (migration + reseed + integration tests green), PR to `main`.
**Version:** 0.6.25.

## What landed

- **Slice 8 (#745) — create/render logic.** Read-swap from `is_lot_tracked` to
  `part.tracking_mode` (`none|lot|serial|lot_serial`) across parts/build/pos/records;
  `IsLotTracked` kept derived, `is_lot_tracked` written in lockstep for back-compat.
  Part-edit gains a 4-value Traceability dropdown. Testing a serial/lot_serial record
  mints a `unit` (lazy, keyed on part+serial; retest reuses it) with provenance from the
  linked lot/build; the record points at the unit via `unit_id` and leaves `lot_id`/
  `build_id` NULL (Q8), read back through the unit. Build history shows "Tested N / qty".
- **Slice 9 (#746) — traceability view.** Generalized the genealogy walk
  (`TraceNode`/`traceNeighbors`/`genealogyTraceRoots`) to union lot **and** unit endpoints;
  `PartLotTrace` uses it. New `unit.go`: Units subtab + per-serial "birth certificate"
  (`PartUnitTrace`), which seeds the walk from both the unit and its lot (a lot_serial
  unit's components are its lot's). Covering-index migration on `genealogy`.
- **Slice 10 (#747) — embedded build UX.** Extracted `performBuild` (shared by the Build
  tab and the record editor). "Build this unit" now expands an inline BOM/lot panel;
  saving builds one unit (single-unit, qty=1) and links it in the same transaction.
  Guards: rejects a re-build of an already-linked record and a blank-serial build.

## Migrations to apply to ArxProd

Both migrations in this branch are pinned to ArxDev via `USE ArxDev;` and must be
re-pointed to ArxProd by a human (change that one line; never run by an agent). Both are
idempotent/single-batch.

- [x] **Prerequisite:** confirm the epic's slices 0–7 schema is already on ArxProd —
      `unit` table (#740), `genealogy` widening (#741), `form_record.unit_id` (#742),
      `part.tracking_mode` (#743), enum CHECKs (#744). Slices 8–10 code assumes all of it.
      If any is missing, apply those migrations first, in order.
- [x] `SQL/migrations/migrate_746_genealogy_trace_indexes.sql` — covering indexes on
      `genealogy` (perf only, no behavior change). Change `USE ArxDev` → `USE ArxProd`.
- [x] `SQL/migrations/migrate_max_subbatch_result_param_rename.sql` — rewrites the
      `max_subbatch_result(@test_id=…)` usage sites in `form_row.spec_nom` / `result.spec_nom`
      to `@form_row_id=` (the #769 rename patched the named_query row but not these usage
      sites, so they fail at runtime). Change `USE ArxDev` → `USE ArxProd`.
- [x] After applying, redeploy `Arx.exe` (v0.6.25) so the tracking_mode-aware code and the
      renamed-param usage land together.

## Small improvements / follow-ups (not blocking)

- **Serial-only-build unit genealogy is unrecorded.** The birth-certificate fix bridges via
  the unit's lot, so a `serial` part with no output lot (build-only provenance) still shows
  an empty ancestry. Recording component→unit edges for the no-lot case needs a small design
  pass (lot-vs-unit granularity). Track on #736.
- **`is_lot_tracked` is now dead-for-reads but still written.** Filed as
  [#854](https://github.com/Jolls/arx-legacy/issues/854).
- **Record *detail* view still hides the lot/build/unit linkage panel** — only the *edit* page
  re-enabled it (that's where provenance is captured). Filed as
  [#855](https://github.com/Jolls/arx-legacy/issues/855).
- **Lot-level completeness deferred.** Only build completeness ("Tested N / qty") is shown; the
  `lot` table has no quantity column to serve as a denominator. Filed as
  [#856](https://github.com/Jolls/arx-legacy/issues/856).
- **Batch-at-test-time is out of scope** (OQ1 chose single-unit qty=1). Filed as
  [#857](https://github.com/Jolls/arx-legacy/issues/857).
- **RELEASE_NOTES not updated** — hold for the public v0.7.0 milestone release (per-release, not
  per-patch), then write one entry covering the whole traceability epic.
- **Seed fixture note:** unit 8501 carries both direct genealogy edges (8404/8405) and the
  lot-bridge, so its trace is intentionally rich/slightly redundant — fine for a test fixture.

## Reference

- Plans: `docs/plans/745-slice8-create-render-logic.md`, `…/746-…`, `…/747-…`.
- Code review + resolutions: `docs/archive/code-review-745-747-2026-07-22.md`.
- Data model: `docs/plans/736-traceability-data-model.md`.
