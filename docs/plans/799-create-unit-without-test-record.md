# #799 — Create a serialized unit without a test record

## Context

Issue #799: *"How to create a serial number without using a test record? Most Units will be
built via testing, but this feature would allow creating pre-existing units."* Milestone
v0.7.0, no labels/comments — low-spec, so the design decisions below are the substance of
this plan.

Today a `unit` row is only ever minted as a side effect of saving a test record for a
`serial`/`lot_serial` part — `upsertUnitForRecord` ([records.go:1825](arx_go/records.go#L1825)),
called from `SaveResults`. The Units subtab is read-only: `PartUnits` +
`PartUnitTrace` ([unit.go:153](arx_go/unit.go#L153)), no create/edit route exists. That is
epic #736 §6 **Q5** working as designed ("lazy, one at a time, at test time; no bulk/eager
add"). #799 is a deliberate, narrow exception to Q5: legacy units that physically exist and
carry a serial, but predate Arx's traceability data and will never have a test record.

Two things block a straight INSERT:

1. **`CK_unit_provenance CHECK (lot_id IS NOT NULL OR build_id IS NOT NULL)`**
   ([SQL/unit.sql:30](SQL/unit.sql#L30)). A genuinely pre-existing unit has neither a `lot`
   nor a `build` row in Arx, so the constraint rejects exactly the case #799 wants.
2. **`BuildView.TestedCount`** ([build.go:22](arx_go/build.go#L22)) counts `unit` rows per
   `build_id` as the *tested* numerator of §6 Q6 completeness ("19 / 20"). A manually
   back-filled unit that names a build would silently inflate that number — it was never
   tested.

Both are solved by making origin explicit on the unit, mirroring the existing `lot.source`
pattern ([SQL/lot.sql:19](SQL/lot.sql#L19)).

Intended outcome: from **Part → Units**, a user can add a serial for a part that tracks
serials, optionally linking a lot and/or build, with no test record involved; and can later
fix a typo'd serial or mark a unit scrapped.

## Decisions (settled with the user)

| Question | Decision |
|---|---|
| Provenance | Drop `CK_unit_provenance`; add `unit.source` (`test`\|`manual`) |
| Entry point | Part → Units subtab (`/part/{id}/units/new`) |
| Bulk | Single serial per submit |
| Scope | Create **and** edit serial + scrap toggle |

`source` is a two-value enum, not three: units are minted by exactly two paths (test-record
save, manual entry). No `build` value — nothing mints a unit from a build today, and adding
the value speculatively buys nothing.

## Schema change

### `SQL/migrations/migrate_799_unit_source.sql` (new; human runs it, pinned `USE ArxDev;`)

Single batch, no `GO`. Three guarded steps:

1. `IF EXISTS (… CK_unit_provenance) ALTER TABLE dbo.unit DROP CONSTRAINT CK_unit_provenance;`
2. `IF COL_LENGTH('dbo.unit','source') IS NULL ALTER TABLE dbo.unit ADD source VARCHAR(10)
   NOT NULL CONSTRAINT DF_unit_source DEFAULT 'test';` — existing rows all came from the
   test path, so the default *is* the backfill; no separate `UPDATE` needed.
3. The `CK_unit_source` CHECK **must be wrapped in `EXEC(N'…')`** — it references the column
   added in step 2, and same-batch name resolution would fail with "Invalid column name"
   (the `migrate_743_part_tracking_mode.sql` pattern; see CLAUDE.md).

`schema_version` is **not** bumped: an older binary never writes `source` (the default
covers its INSERTs) and never inserts a both-NULL-provenance row, so the change is
backwards-compatible — same reasoning as `migrate_740`'s header comment. Include the
Postgres equivalent in trailing comments, as `migrate_740` does.

No FK-orphan check is needed (this adds no FK), and no `named_queries` patch (no rename).

### Reference DDL + docs to keep in sync

- [SQL/unit.sql](SQL/unit.sql) — add `source`, remove `CK_unit_provenance`, and rewrite the
  header comment: the provenance invariant is now "app-level for test-minted units; a
  `manual` unit may legitimately have neither."
- [SQL/postgres/unit.sql](SQL/postgres/unit.sql) — same change.
- [SQL/schema.md](SQL/schema.md) — `unit` row of the table reference (line ~152) and the
  8501-8599 seed row (line ~93).
- [SQL/seed_test_data.sql](SQL/seed_test_data.sql) + `SQL/postgres/seed_test_data.sql` —
  add `source` to the `unit` INSERT column list (`'test'` for 8501-8503) and add **8504**:
  part 3005 (`tracking_mode = serial`), `lot_id`/`build_id` both NULL, `source = 'manual'`
  — the fixture that proves a provenance-less unit is now legal and that `TestedCount`
  ignores it. Note in the seed comment that this row supersedes the old
  "three rows cover every branch of `CK_unit_provenance`" claim.

No new `cfg.*Table()` helper — `UnitTable()` already exists
([config.go:209](arxlib/config/config.go#L209)).

## Go changes — all in [arx_go/unit.go](arx_go/unit.go)

Add `Source string` to `UnitRow` and to `unitRowSelect()` / `scanUnitRow` (and the
duplicated column list in `recentPartUnits`, which does not use `unitRowSelect`).

Four handlers, following `PriceNew`/`PriceCreate`
([parts.go:2098](arx_go/parts.go#L2098), [parts.go:2141](arx_go/parts.go#L2141)) and
`LotEdit`/`LotUpdate` ([lot.go:410](arx_go/lot.go#L410)) exactly:

- **`UnitNew`** — `GET /part/{id}/units/new`. `partPageBase(…, "units")` gates the part to
  `serial`/`lot_serial` via the existing `tabVisible` → `Part.ShowUnits()`. Loads pickable
  provenance by reusing `activeLotsForPart` (only when `models.TracksLots(p.TrackingMode)`)
  and `activeBuildsForPart`. Renders with `CSRFToken`.
- **`UnitCreate`** — `POST /part/{id}/units`. `requireTab(…, "units")`; serial required
  (non-empty after `fv`); reuse `recordLinkageArgs(r, p.ID)`
  ([records.go:1787](arx_go/records.go#L1787)) to read + ownership-validate `lot_id`/`build_id`
  — it already returns `nil` for blank and errors on a foreign id, which is exactly the
  semantics needed, and both being nil is now legal. INSERT with `source = 'manual'`.
  Map a `UQ_unit_serial` violation to a friendly message (the `UQ_price` string-match
  pattern in `PriceCreate`), not a raw driver error. Redirect to the new unit's trace page.
- **`UnitEdit`** — `GET /part/{id}/units/{unitID}/edit`. Reuse `fetchUnitRow`; reject when
  `!found || unit.PartID != p.ID` (the `LotEdit` guard). Also compute `SerialLocked` — see
  below — so the template renders the serial input read-only once it's frozen.
- **`UnitUpdate`** — `POST /part/{id}/units/{unitID}`. Same guards. Updates `is_active`
  always; updates `serial_number` **only** when not locked (server-side check, not just a
  disabled input). `WHERE id = @pN AND part_id = @pN` like `LotUpdate`.

New helper in `unit.go`:

```go
// unitSerialLocked reports whether a unit's serial is frozen — true once any locked
// form_record points at it (#736 §6 Q5: "editable until the unit's first locked
// form_record"). Scrap (is_active) stays editable regardless.
func (h *Handler) unitSerialLocked(ctx context.Context, unitID int) (bool, error)
```

`SELECT COUNT(*) FROM <RecordsTable()> WHERE unit_id = @p1 AND is_locked = <BoolLiteral(true)>`
— use `h.dia().BoolLiteral(true)` rather than a literal `1`, per the #831 Postgres-bit
convention.

### Completeness fix — [arx_go/build.go](arx_go/build.go)

In `PartBuild`'s `TestedCount` subquery, exclude manual units:
`… FROM unit u WHERE u.build_id = build.id AND u.source <> 'manual'`. Add a one-line comment
tying it to #799. `source` is `NOT NULL`, so no NULL-guard is needed.

## Templates — [arx_go/templates/parts/](arx_go/templates/parts/)

- **`part_units.html`** — "Add Unit" button (`btn btn-primary`) linking to
  `/part/{{.Part.ID}}/units/new`; new **Source** column (`badge bg-secondary` for `manual`,
  plain text for `test`) so back-filled units are visibly distinct; per-row **Edit** link.
  Update the intro paragraph and the empty-state text, which both currently assert a unit is
  only created by testing.
- **`part_unit_form.html`** (new) — one template for both create and edit, `IsNew`-switched,
  same shape as `part_pricing_form.html`. Fields: Serial # (read-only when `SerialLocked`,
  with a muted explanatory note), Lot select (only when the part tracks lots), Build select,
  Active checkbox (edit only). Bootstrap classes only, CSRF hidden input.
- **`part_unit_trace.html`** — show Source in the header alongside lot/build, and make sure
  the both-NULL case reads sensibly rather than as an error.

## Routes — [arx_go/main.go:245](arx_go/main.go#L245)

```go
r.Get("/part/{id}/units", h.PartUnits)
r.Get("/part/{id}/units/new", h.UnitNew)          // before {unitID}
r.Post("/part/{id}/units", h.UnitCreate)
r.Get("/part/{id}/units/{unitID}", h.PartUnitTrace)
r.Get("/part/{id}/units/{unitID}/edit", h.UnitEdit)
r.Post("/part/{id}/units/{unitID}", h.UnitUpdate)
```

chi prefers a static segment over a wildcard, so `/new` resolves ahead of `{unitID}`
regardless of order; listing it first is for readability.

## Tests

Extend [arx_go/integration_test.go](arx_go/integration_test.go) (build-tagged, ArxDev only —
existing unit coverage is at lines 2117 and 2256, follow its style):

1. Manual unit with `lot_id`/`build_id` both NULL inserts successfully and reads back with
   `source = 'manual'` — the regression test for the dropped CHECK.
2. Duplicate `(part_id, serial_number)` is rejected and surfaces the friendly message, not a
   driver error.
3. `TestedCount` for a build is unchanged by adding a `manual` unit that names that build.
4. `unitSerialLocked` returns true for seeded unit 8501 (locked record 7013 points at it) and
   false for the new manual unit 8504.

## Verification

1. `cd arx_go; go build ./...`, `go vet ./...`, `go test ./...` — then `.\build.bat` for the
   full sweep before shipping.
2. Human applies `SQL/migrations/migrate_799_unit_source.sql` to ArxDev, then reseeds ArxDev
   with the updated `SQL/seed_test_data.sql` (both are human actions — do not run them).
3. Integration tests against ArxDev:
   `$env:ARX_TEST_DSN="sqlserver://…?database=ArxDev&encrypt=true"; go test -tags integration ./arx_go/...`
   — first confirm `arx_go/config/local.json` does not have `test_engine: "postgres"`, which
   breaks a SQL Server `ARX_TEST_DSN` connect.
4. Manual (user runs the app):
   - Part 3005 (`serial`) → Units → **Add Unit** → serial only, no lot/build → saves, shows
     `manual` badge, trace page renders with no provenance rows.
   - Part 3013 (`lot_serial`) → Add Unit with a lot picked → lot link resolves on the trace.
   - Re-submit the same serial for the same part → friendly duplicate message.
   - Edit unit 8504's serial → allowed. Edit unit 8501's serial → input read-only, and a
     hand-crafted POST does not change it.
   - Toggle a unit inactive → Units list shows the "Scrapped" badge.
   - Part → Build tab: the "Tested" column is unchanged after adding a manual unit against
     that build.
5. `CHANGELOG.md`: one entry for the PR under `### Added` (Add Unit / edit) and `### Changed`
   (`unit.source`, completeness excludes manual units), linking #799. Tag after the version
   bump commit.

## Not in scope

- Bulk / range serial entry (rejected above; `serial_number` is deliberately a free string).
- Cross-part `/units` list analogous to `AllLots` — no request for it.
- Any reconciliation of unit count vs. build/lot qty — explicitly out of scope per §6 Q2/Q6.
- Deleting a unit (scrap via `is_active` only, matching `lot`).
