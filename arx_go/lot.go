package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"arx/internal/inventory"
	"arx/internal/records"
	"github.com/shopspring/decimal"
)

// Lot control (#676, part of the #568 lot epic). A lot is one batch instance of a
// lot-tracked part (part.tracking_mode lot/lot_serial). Purchased lots are created at goods
// receipt (po_line_id set); manufactured lots are created by a build that produces
// a lot-tracked output part. Auto-issued lot_number defaults to the lot's own id
// (#687); lot_description carries the human-readable provenance. The genealogy
// table records which parent (component) lots were consumed into a child (output) lot.

// LotOption is one active lot of a part, offered in the build form's per-component
// lot picker. Label is a human-readable identifier (lot number + vendor/PO hint).
type LotOption = inventory.LotOption

// lotCreateArgs groups createLot's free-text fields so a positional call can't
// silently swap VendorLot and Description (both are plain strings).
type lotCreateArgs = inventory.LotCreate

// createLot inserts one lot row inside the caller's tx and returns its new id.
// poLineID is nil for manufactured (build) lots. args.LotNumber == "" (auto-issued
// lots) defers to the lot's own id, guaranteed unique by construction (#687) — a
// second UPDATE sets it once the id is known post-insert.
func (h *Handler) createLot(ctx context.Context, tx *txLogger, partID int, args lotCreateArgs, poLineID *int) (int, error) {
	return inventory.New(tx).CreateLot(ctx, partID, args, poLineID)
}

// activeLotsForPart returns a part's active lots, newest first, for the build
// form's component lot picker. Empty (not an error) when the part has no active lot.
func (h *Handler) activeLotsForPart(ctx context.Context, partID int) ([]LotOption, error) {
	return h.inventory().ListActiveLots(ctx, partID)
}

// lotBelongsToPart reports whether lotID is an active lot of partID — guards the
// build's genealogy writes against a forged or stale lot selection in the POST.
func (h *Handler) lotBelongsToPart(ctx context.Context, tx *txLogger, lotID, partID int) (bool, error) {
	return inventory.New(tx).LotBelongsToPart(ctx, lotID, partID)
}

// recordGenealogy inserts one genealogy edge linking a consumed parent (component)
// lot to the child (output) lot it fed, inside the caller's tx. Lot→lot only; unit
// endpoints (parent_unit_id/child_unit_id) are written from slice 8 (#736).
func (h *Handler) recordGenealogy(ctx context.Context, tx *txLogger, parentLotID, childLotID int, qtyConsumed decimal.Decimal) error {
	return inventory.New(tx).RecordGenealogy(ctx, parentLotID, childLotID, qtyConsumed)
}

// ── Lots view (#676) ─────────────────────────────────────────────────────────

// LotRow is one lot in the Lots subtab list and the header of a genealogy trace.
// LotDescription is the human-readable provenance stored at creation (#687): "PO
// <number>" for a purchased lot, "Build #<id>" for a manufactured one, "Manual
// entry" for one created directly on the inventory adjustment tab.
type LotRow = inventory.LotRow

// lotsForPart returns every lot of a part, newest first, for the Lots subtab list.
func (h *Handler) lotsForPart(ctx context.Context, partID int) ([]LotRow, error) {
	return h.inventory().ListPartLots(ctx, partID)
}

// recentPartLots returns the most recent lots for a part, newest first, capped
// at limit, for the Part dashboard "Lots" card (#798).
func (h *Handler) recentPartLots(ctx context.Context, partID int, limit int) ([]LotRow, error) {
	return h.inventory().ListRecentPartLots(ctx, partID, limit)
}

// lotCountForPart returns the total number of lots for a part, for the Part
// dashboard "Lots" card (#798).
func (h *Handler) lotCountForPart(ctx context.Context, partID int) (int, error) {
	return h.inventory().CountPartLots(ctx, partID)
}

// fetchLotRow loads a single lot for the genealogy trace header. ok=false (nil
// error) when the lot does not exist.
func (h *Handler) fetchLotRow(ctx context.Context, lotID int) (LotRow, bool, error) {
	return h.inventory().GetLot(ctx, lotID)
}

// TraceNode is one lot OR unit in a genealogy trace (#746), flattened with Depth for
// indented rendering — see inventory.TraceNode.
type TraceNode = inventory.TraceNode

// traceRoot is one starting node (a lot or a unit) for a genealogy walk.
type traceRoot struct {
	id       int
	nodeType string // "lot" | "unit"
}

// genealogyTrace walks the genealogy table from a single root — see genealogyTraceRoots.
func (h *Handler) genealogyTrace(ctx context.Context, rootID int, rootType string, ancestors bool) ([]TraceNode, error) {
	return h.genealogyTraceRoots(ctx, []traceRoot{{rootID, rootType}}, ancestors)
}

// genealogyTraceRoots walks the genealogy table from one or more roots and returns the
// reachable lot/unit nodes flattened depth-first (ancestors=parents down to raw vendor
// lots / root units, or descendants=children). A single visited set is shared across
// all roots so a node reachable from several roots is expanded once. It is keyed by
// (NodeType, ID): lot and unit id spaces are independent, so a bare int key would
// falsely conflate a lot and a unit that share the same id. Multiple roots let a unit
// be traced together with its lot (#746): a lot_serial unit's as-built components are
// its lot's, so its birth certificate seeds from both the unit and the lot it belongs to.
func (h *Handler) genealogyTraceRoots(ctx context.Context, roots []traceRoot, ancestors bool) ([]TraceNode, error) {
	rs := make([]inventory.TraceRoot, len(roots))
	for i, rt := range roots {
		rs[i] = inventory.TraceRoot{ID: rt.id, NodeType: rt.nodeType}
	}
	return h.inventory().Trace(ctx, rs, ancestors)
}

// PartLots — GET /part/{id}/lots. Lists a lot-controlled part's lots, each linking
// to its genealogy trace.
func (h *Handler) PartLots(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "lots")
	if !ok {
		return
	}
	lots, err := h.lotsForPart(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving lots: "+err.Error())
		return
	}
	lotIDs := make([]int, len(lots))
	for i, l := range lots {
		lotIDs[i] = l.ID
	}
	hasSources, err := h.inventory().LotsWithSources(r.Context(), lotIDs)
	if err != nil {
		h.renderError(w, r, "Error retrieving lot sources: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_lots.html", map[string]any{
		"Part": p, "Lots": lots, "HasSources": hasSources,
		"ActiveTab": "parts", "ActiveSubTab": "lots",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// PartLotTrace — GET /part/{id}/lots/{lotID}. Shows one lot's genealogy: its
// ancestors (recursed to raw vendor lots) and descendants.
func (h *Handler) PartLotTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "lots")
	if !ok {
		return
	}
	lotID, err := strconv.Atoi(r.PathValue("lotID"))
	if err != nil {
		h.renderError(w, r, "Invalid lot id")
		return
	}
	lot, found, err := h.fetchLotRow(r.Context(), lotID)
	if err != nil {
		h.renderError(w, r, "Error retrieving lot: "+err.Error())
		return
	}
	if !found || lot.PartID != p.ID {
		h.renderError(w, r, "Lot not found for this part")
		return
	}
	ancestors, err := h.genealogyTrace(r.Context(), lotID, "lot", true)
	if err != nil {
		h.renderError(w, r, "Error tracing lot ancestry: "+err.Error())
		return
	}
	descendants, err := h.genealogyTrace(r.Context(), lotID, "lot", false)
	if err != nil {
		h.renderError(w, r, "Error tracing lot descendants: "+err.Error())
		return
	}
	typeOptions, err := h.scopedRecordTypeOptions(r.Context(), records.ScopeLot, lotID)
	if err != nil {
		h.renderError(w, r, "Error retrieving record types: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_lot_trace.html", map[string]any{
		"Part": p, "Lot": lot, "Ancestors": ancestors, "Descendants": descendants,
		"TypeOptions": typeOptions,
		"ActiveTab":   "parts", "ActiveSubTab": "lots",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// LotRecordsRows — GET /api/part/{id}/lots/{lotID}/records/rows. JSON rows for
// the records table on the lot trace page (#875).
func (h *Handler) LotRecordsRows(w http.ResponseWriter, r *http.Request) {
	lotID, err := strconv.Atoi(r.PathValue("lotID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := h.scopedRecordsRows(r.Context(), records.ScopeLot, lotID)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeJSON(w, out)
}

// LotEdit — GET /part/{id}/lots/{lotID}/edit. Form to edit a lot's Lot
// Description and Vendor Lot (#701) — the only two free-text fields set at
// creation that are safe to revise after the fact.
func (h *Handler) LotEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "lots")
	if !ok {
		return
	}
	lotID, err := strconv.Atoi(r.PathValue("lotID"))
	if err != nil {
		h.renderError(w, r, "Invalid lot id")
		return
	}
	lot, found, err := h.fetchLotRow(r.Context(), lotID)
	if err != nil {
		h.renderError(w, r, "Error retrieving lot: "+err.Error())
		return
	}
	if !found || lot.PartID != p.ID {
		h.renderError(w, r, "Lot not found for this part")
		return
	}
	h.render(w, r, "parts/part_lot_edit.html", map[string]any{
		"Part": p, "Lot": lot,
		"ActiveTab": "parts", "ActiveSubTab": "lots",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// LotUpdate — POST /part/{id}/lots/{lotID}. Saves Lot Description and Vendor
// Lot; every other lot field is read-only (#701).
func (h *Handler) LotUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := h.requireTab(w, r, id, "lots")
	if !ok {
		return
	}
	lotID, err := strconv.Atoi(r.PathValue("lotID"))
	if err != nil {
		h.renderError(w, r, "Invalid lot id")
		return
	}
	lot, found, err := h.fetchLotRow(r.Context(), lotID)
	if err != nil {
		h.renderError(w, r, "Error retrieving lot: "+err.Error())
		return
	}
	if !found || lot.PartID != p.ID {
		h.renderError(w, r, "Lot not found for this part")
		return
	}
	description := fv(r, "lot_description")
	vendorLot := fv(r, "vendor_lot")
	notes := fv(r, "notes")
	err = h.inventory().UpdateLot(r.Context(), lotID, p.ID, description, vendorLot, notes)
	if err != nil {
		h.renderError(w, r, "Error saving lot: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/lots/%d", id, lotID), http.StatusFound)
}

// appendLotNote appends one entry to a lot's notes (#872). The concatenation happens
// server-side, from just the new text, rather than the caller posting back a whole
// rewritten field: a test record's edit page stays open for a whole session, so a
// full-field write would silently drop anything another tester appended in the
// meantime. Entries carry a [username date] prefix — a multi-author free-text field
// is unreadable without attribution. Takes the caller's tx so an append made while saving
// a record rolls back with the record if that save fails.
func (h *Handler) appendLotNote(ctx context.Context, tx *txLogger, lotID int, text, username string) error {
	return inventory.New(tx).AppendLotNote(ctx, lotID, text, username, time.Now().In(h.userLocationCtx(ctx)))
}

// ── All lots (#701) ──────────────────────────────────────────────────────────

// AllLots — GET /lots. Cross-part list of every lot, newest first, for
// browsing without drilling into a specific part first.
func (h *Handler) AllLots(w http.ResponseWriter, r *http.Request) {
	lots, err := h.inventory().ListAllLots(r.Context())
	if err != nil {
		h.renderError(w, r, "Error retrieving lots: "+err.Error())
		return
	}
	h.render(w, r, "parts/all_lots.html", map[string]any{
		"Lots": lots, "ActiveTab": "parts", "TestMode": h.cfg().TestMode,
	})
}
