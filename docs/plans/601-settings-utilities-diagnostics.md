# #601 — Add a Utilities section under Settings (read-only maintenance diagnostics)

## Context

Issue #601 asks for a **Utilities** area under Settings whose first job is to surface data-integrity problems — "dead attachment links, dead parts, dead ?". The maintainer comment on the issue proposes a set of read-only diagnostic checks (count/list, no auto-fix) for a first pass. This plan takes that list, corrects the parts that don't match the actual schema, and scopes a minimal first slice.

**Guiding constraints for the first pass:**
- Read-only. Every check produces a count + a drill-down list. No auto-fix / no deletes.
- Cheap queries only — this is a diagnostics dashboard, not a batch job.
- Lives in a new **Utilities** section on the Settings page, gated the same way other DB-dependent sections are (`.Connected`).

## Schema corrections to the issue-comment proposal

Verified against the live DDL — three items in the proposal are wrong or need qualification:

1. **`company.primary_attachment_id` is an *enforced* FK, not a soft pointer.** [company.sql:27](../../SQL/company.sql#L27) declares `FK_company_primary_attachment` → `company_attachment(supplier_attachment_id)`. The DB already guarantees it can't reference a missing row, so a "row missing entirely" orphan check on it is dead code. Drop it from the orphan-pointer list. (The soft-*deleted*-target case still applies — see check #3 — because an FK does not prevent pointing at an `is_active = 0` row.) Note also the column it references is `company_attachment.supplier_attachment_id`, and the file column on that table is `file_path`, not `file_name`.

2. **The `part` soft pointers default to `0`, not NULL.** `part.price_id` and `part.primary_attachment_id` both `DEFAULT 0` ([part_number.sql:49,54](../../SQL/part_number.sql#L49)); `part.default_supplier_id` is NULL. A naïve `WHERE primary_attachment_id NOT IN (SELECT id FROM part_attachment)` flags **every part with no primary set** as a dead pointer. Each orphan query must exclude the unset sentinel (`> 0` for the two DEFAULT-0 columns, `IS NOT NULL` for `default_supplier_id`) — the same guard the existing named queries use ([NamedQueries.sql:70](../../SQL/NamedQueries.sql#L70)).

3. **The dead-link check must branch on link type.** `part_attachment.file_name` (and `company_attachment.file_path`) hold three shapes — `http(s)://`, `LOCAL:file`, and `LOCAL:dir\` (trailing slash = directory) — per [docs/conventions.md](../conventions.md). The check must skip http URLs, stat `LOCAL:` files as files and `LOCAL:...\` as directories, and resolve relative to `DocControlRoot`. `arxlib/urlutil` already provides `IsHTTPURL`, `IsLocalFile`, `IsLocalDir`, and `StripLocalPrefix` — reuse them; do not re-parse the prefix by hand.

## Checks in scope (first slice)

| # | Check | Query shape | Notes |
|---|-------|-------------|-------|
| 1 | **Dead attachment file links** | Fetch `id, part_id, file_name` from `part_attachment WHERE is_active = 1` and `supplier_attachment_id, supplier_id, file_path` from `company_attachment WHERE is_active = 1`; in Go, for each `LOCAL:` value `os.Stat(filepath.Join(DocControlRoot, StripLocalPrefix(v)))` and flag missing. | Skip http links. File vs dir via `IsLocalFile`/`IsLocalDir`. |
| 2 | **Orphaned soft-FK pointers** (part only) | `part.default_supplier_id` → `company.id`; `part.price_id` → `price.id`; `part.primary_attachment_id` → `part_attachment.id`. Each: `WHERE <col> <unset-guard> AND NOT EXISTS (SELECT 1 FROM <target> t WHERE t.<pk> = p.<col>)`. | Unset guard per correction #2. Company pointer dropped (correction #1). |
| 3 | **Attachment pointer → soft-deleted target** | `part.primary_attachment_id` and `company.primary_attachment_id` that reference an attachment row that exists but has `is_active = 0`. | Cheaper/distinct from #2. Company one is valid here (FK allows pointing at inactive rows). |
| 4 | **PO `is_active` drift** | `purchase_order WHERE (status IN ('draft','open','sent','partially_received') AND is_active = 0) OR (status IN ('closed','cancelled') AND is_active = 1)`. | `is_active` is an app-maintained convenience bit; `status` is authoritative ([schema.md:170](../../SQL/schema.md#L170)). |

Deferred (mentioned in the issue comment, not in the first slice): the remaining soft-FK relationships (`contact.company_id`, `test_record.part_number_id`, `test_result.record_id`/`test_id`). These are legitimate unenforced references and can be added as more rows in the same "Orphaned pointers" check once the pattern is established — left out of slice 1 to keep it small and attachment-focused, matching the issue's own framing.

## Changes

### 1. Handler — new file `arx_go/utilities.go`
- One handler `UtilitiesReport(w, r)` that runs the checks above via `h.queryContext` / `h.queryRowContext` (never `h.db.*` directly) and renders a template with per-check counts + lists. Each check is a small helper returning a typed slice so the template can drill down.
- The dead-link check (#1) reads `h.cfg.DocControlRoot` and stats the filesystem; guard against `DocControlRoot == ""` (show "not configured" rather than flagging everything).
- Register the route in the router next to the other settings routes, behind `RequireAuth` (it needs `h.db`).

### 2. Template — new `arx_go/templates/pm/utilities.html`
- Served from a **standalone `/settings/utilities` route** (decided), linked from a "Utilities" entry on the Settings page. This keeps the potentially slow filesystem stat off every settings-page load — the checks run only when the user opens Utilities.
- Each check = a Bootstrap card: title, count badge (`badge bg-danger` when > 0, `badge bg-success` when 0), and a collapsible `table table-sm` listing the offending rows with links to the relevant part/PO/company detail page.
- Add a small "Utilities" link/section on `settings.html` pointing at `/settings/utilities` (gated on `.Connected`).

### 3. No schema / no config / no seed changes
- All checks read existing tables. No new table → the CLAUDE.md "new table" checklist does not apply.
- No `*Table()` helper needed — use the existing `cfg.PartTable()`, `cfg.PartAttachmentTable()`, `cfg.PurchaseOrderTable()`, etc. Never hardcode names.

## Verification
- `go build ./...`, `go vet`, `go test ./...` from `arx_go/` (see CLAUDE.md Building).
- Manual: seeded ArxDev has URL-only attachments (8101/8102) — check #1 should report **0 dead file links** there (no `LOCAL:` file rows seeded), confirming http links are correctly skipped. To exercise a positive, the user can temporarily add a `part_attachment` row with a bogus `LOCAL:` path in ArxDev.
- Check #4: seeded POs cover one per status ([seed_test_data.sql](../../SQL/seed_test_data.sql) 5001-5099) with `is_active` in sync, so the drift check should report 0 against a clean seed.
- Consider an integration test (`//go:build integration`, ArxDev only) asserting each check returns 0 against the clean seed — a good regression guard since these are pure read queries with no UI to break. Suggest to the user; don't add unless agreed.

## Decisions (settled)
- **Placement:** standalone `/settings/utilities` route, linked from the Settings page — not a tab. Rationale: the dead-link check stats the filesystem for every attachment; running that on-demand keeps it off the main settings load.
- **Slice size:** checks #1–#4 only. The `contact.company_id`, `test_record.part_number_id`, and `test_result.record_id`/`test_id` orphan checks are deferred — they extend the same "Orphaned pointers" pattern and can be added as extra rows once slice 1 lands, keeping this PR small and attachment-focused per the issue's framing.
