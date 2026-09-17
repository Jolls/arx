# Batch: issues #72, #73, #74 — one branch, one combined PR

## Context

Three independent, small-to-medium UI/backend issues, applied in ascending
complexity on one branch, left uncommitted until the human tests the
cumulative tip:

1. **#72** — subtabs don't wrap to a 2nd row on narrow viewports (pure CSS).
2. **#73** — file upload (PO folder / doc-control / supplier folder views)
   needs drag-and-drop, not just click-to-browse.
3. **#74** — add a "Start RFQ" button on draft POs that clones the PO's line
   items into a brand-new RFQ as its first quote, leaving the source PO
   untouched.

Order: 72 → 73 → 74 (simplest first). `templates/shared/partials.html` is
touched by both group 1 (subtabs wrap) and group 3 (Start RFQ button), so
the batch lands as one combined commit rather than per-issue commits.

---

## Group 1 — Issue #72: subtabs wrap on mobile

**File:** `arx_go/static/app.css`

`.sub-tabs` was a plain flex row with no wrap. Added `flex-wrap: wrap;`.

Also, `po_tabs` (`arx_go/templates/shared/partials.html`) wraps `.sub-tabs`
in its own inline-styled flex row alongside the action buttons (Duplicate PO
/ Start RFQ / RFQ buttons). Added `flex-wrap:wrap; gap:8px;` to that inline
style so the action buttons drop below the subtabs on narrow screens instead
of squeezing them.

**Verify:** build succeeds; manually resize browser / use dev-tools device
toolbar on a PO detail page (has both subtabs and the extra action-button
row) and a Reports page (subtabs only) to confirm both wrap cleanly at mobile
widths with no horizontal overflow.

---

## Group 2 — Issue #73: drag-and-drop upload

**Scope:** the shared upload partial only — `arx_go/templates/shared/local_dir.html`,
which is rendered by PO folder view, part doc-control folder view, and
supplier folder view, all posting to the single shared handler
`handleDirUpload` (`arx_go/files.go:275`). Attachment browse/paste UI
(`parts/part_attachments.html`, `suppliers/supplier_attachments.html`) and the
Settings company-logo upload are a different mechanism and were **not**
touched.

**Behavior:** dropping a file auto-submits the form (uploads immediately),
matching typical drag-drop UX.

**Change:** gave the upload `<form>` an id, added a dashed-border drop-zone
style, and a small inline `<script>` block (matching this file's existing
house style — vanilla JS, no jQuery/htmx) that:
- Prevents default on `dragover`/`dragenter` (required so `drop` fires) and
  toggles a highlight style on dragover/dragleave.
- On `drop`: reads `event.dataTransfer.files`, assigns the first file to the
  `<input type="file" name="upload">` via `DataTransfer`, then calls
  `form.submit()`.
- Only wired up inside `{{if .UploadFormAction}}` (same guard as the form
  itself) so it's a no-op on read-only directory views.

No backend change was needed — `handleDirUpload` already handles any file
arriving via the existing `upload` field.

**Verify:** manually test: drag a file onto the upload zone in a PO's folder
view and confirm it uploads and the page reloads showing the new file;
confirm click-to-browse still works unchanged; spot-check the part
doc-control folder view and supplier folder view (same partial) still work
for both click and drag.

---

## Group 3 — Issue #74: "Start RFQ" button on draft POs

**Pattern:** mirrors `PODuplicate` (`arx_go/pos.go`), not `RFQAddSupplier` —
preserves the source PO's supplier, unit costs, and vendor part numbers
exactly as-is, since this is turning an existing quote into the RFQ's first
quote, not soliciting a fresh one.

### 3a. New handler — `arx_go/pos.go`

Added `POStartRFQ` (GET `/po/{id}/start-rfq`), placed after `PODuplicate`,
modeled on it: loads the source draft PO and its line items, guards on
`source.Status != "draft"`, clears number/dates/total-cost, sets
`Status = "rfq"`, and renders `pos/po_edit.html` with `IsRFQ: true`,
`StartRFQFrom: num`, and `DuplicateItems` seeded from the source's line items
(unit costs and vendor PNs preserved, unlike `RFQAddSupplier` which blanks
them for a new supplier's quote).

No `RFQGroupID`/`rfq_group_id` is passed — this deliberately mirrors `RFQNew`
(no group), so `POCreate`'s existing RFQ-creation branch (the
`isRFQ`/`rfq_group_id` handling already used by `RFQNew`/`RFQAddSupplier`)
allocates a fresh sequence number and RFQ group with this as quote 1 — no
changes needed there.

### 3b. Route — `arx_go/main.go`

Added `r.Get("/po/{id}/start-rfq", h.POStartRFQ)` next to the existing
`r.Get("/po/{id}/duplicate", h.PODuplicate)`.

### 3c. Button — `arx_go/templates/shared/partials.html` (`po_tabs`)

Added a "Start RFQ" button alongside "Duplicate PO", gated to
`{{if eq .PO.Status "draft"}}`.

### 3d. Template — `arx_go/templates/pos/po_edit.html`

Added a `StartRFQFrom` case alongside the existing `RFQAddFrom`/`IsDuplicate`
cases for the breadcrumb, the info alert ("Starting an RFQ from PO #N — a new
RFQ number will be assigned on save; this PO's line items, supplier, and
pricing carry over as the first quote. The original PO is unchanged."), and
the Cancel link.

**Verify:** `go build`/`go vet`/`go test ./...` pass. Manually: open a draft
PO, click "Start RFQ", confirm the new-RFQ form is pre-filled with the same
supplier/line items/costs, save it, confirm a new RFQ (status `rfq`, fresh
number) is created and the original draft PO is unchanged (same number,
still `draft`). Confirm the button is absent on non-draft POs (open, sent,
closed, rfq) and "Duplicate PO" still works as before.

---

## Verification performed

- `go build ./...`, `go vet ./...`, `go test ./...` — pass after each group.
- `/code-review low` run on each group's incremental diff — no findings.
- Live ArxDev integration tests (`go test -tags integration ./arx_go/...`) —
  pass (run by user, since `pos.go` has existing coverage via
  `TestIntegration_RFQAddSupplier_ClonesLinesBlanksSupplier`).
- Manual test-points list (subtabs wrap, drag-and-drop upload, Start RFQ
  golden path + draft-only gating + Duplicate PO regression) — all pass, per
  user.
