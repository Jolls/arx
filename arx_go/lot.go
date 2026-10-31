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

// LotRow is one lot in the Lots detail tab list and the header of a genealogy trace.
// LotDescription is the human-readable provenance stored at creation (#687): "PO
// <number>" for a purchased lot, "Build #<id>" for a manufactured one, "Manual
// entry" for one created directly on the inventory adjustment tab.
type LotRow = inventory.LotRow

// lotsForPart returns every lot of a part, newest first, for the Lots detail tab list.
func (h *Handler) lotsForPart(ctx context.Context, partID int) ([]LotRow, error) {
	return h.inventory().ListPartLots(ctx, partID)
}

// recentPartLots returns the most recent lots for a part, newest first, capped
// at limit, and the part's total lot count, for the Part dashboard "Lots" card (#798).
func (h *Handler) recentPartLots(ctx context.Context, partID int, limit int) ([]LotRow, int, error) {
	return h.inventory().ListRecentPartLots(ctx, partID, limit)
}

func (h *Handler) buildCountForPart(ctx context.Context, partID int) (int, error) {
	return h.inventory().CountPartBuilds(ctx, partID)
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
	var lots []LotRow
	var hasSources map[int]bool
	p, backURL, backLabel, ok := h.partTabPage(w, r, id, "lots", func(ctx context.Context) error {
		pid, err := strconv.Atoi(id)
		if err != nil {
			return err
		}
		if lots, err = h.lotsForPart(ctx, pid); err != nil {
			return pageErr("Error retrieving lots", err)
		}
		lotIDs := make([]int, len(lots))
		for i, l := range lots {
			lotIDs[i] = l.ID
		}
		hasSources, err = h.inventory().LotsWithSources(ctx, lotIDs)
		return pageErr("Error retrieving lot sources", err)
	})
	if !ok {
		return
	}
	h.render(w, r, "parts/part_lots.html", map[string]any{
		"Part": p, "Lots": lots, "HasSources": hasSources,
		"ActiveTab": "parts", "ActiveDetailTab": "lots",
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
	var ancestors, descendants []TraceNode
	var typeOptions []string
	var units []UnitRow
	if err := parallel(r.Context(),
		func(ctx context.Context) (err error) {
			ancestors, err = h.genealogyTrace(ctx, lotID, "lot", true)
			return pageErr("Error tracing lot ancestry", err)
		},
		func(ctx context.Context) (err error) {
			descendants, err = h.genealogyTrace(ctx, lotID, "lot", false)
			return pageErr("Error tracing lot descendants", err)
		},
		func(ctx context.Context) (err error) {
			typeOptions, err = h.scopedRecordTypeOptions(ctx, records.ScopeLot, lotID)
			return pageErr("Error retrieving record types", err)
		},
		func(ctx context.Context) (err error) {
			units, err = h.inventory().ListLotUnits(ctx, lotID)
			return pageErr("Error retrieving lot units", err)
		},
	); err != nil {
		h.renderError(w, r, err.Error())
		return
	}
	h.render(w, r, "parts/part_lot_trace.html", map[string]any{
		"Part": p, "Lot": lot, "Ancestors": ancestors, "Descendants": descendants, "Units": units,
		"TypeOptions": typeOptions,
		"ActiveTab":   "parts", "ActiveDetailTab": "lots",
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
		"ActiveTab": "parts", "ActiveDetailTab": "lots",
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
	h.render(w, r, "parts/all_lots.html", map[string]any{
		"ActiveTab": "records", "SectionTab": "lots", "TestMode": h.cfg().TestMode,
	})
}

// AllLotsRows — GET /api/lots/rows. JSON rows for the All Lots table (#411).
func (h *Handler) AllLotsRows(w http.ResponseWriter, r *http.Request) {
	lots, err := h.inventory().ListAllLots(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeJSON(w, lotListRows(lots, h.userLocation(r)))
}

// lotListRow is one row of /api/lots/rows (#411); dates arrive pre-formatted in the user's zone.
type lotListRow struct {
	ID       int    `json:"id"`
	PartID   int    `json:"partId"`
	Lot      string `json:"lot"`
	Vendor   string `json:"vendorLot"`
	LotDesc  string `json:"lotDesc"`
	PN       string `json:"pn"`
	PartDesc string `json:"partDesc"`
	Notes    string `json:"notes"`
	Created  string `json:"created"`
	Active   bool   `json:"active"`
}

func lotListRows(lots []LotRow, loc *time.Location) []lotListRow {
	out := make([]lotListRow, len(lots))
	for i, l := range lots {
		out[i] = lotListRow{ID: l.ID, PartID: l.PartID, Lot: l.LotNumber, Vendor: l.VendorLot, LotDesc: l.LotDescription,
			PN: l.PartNumber, PartDesc: l.PartDescription, Notes: l.Notes, Created: localTime(loc, l.CreatedAt).Format("2006-01-02"), Active: l.IsActive}
	}
	return out
}

// unitListRow is one row of /api/units/rows (#411); LotID is 0 when the unit has no lot.
type unitListRow struct {
	ID       int    `json:"id"`
	PartID   int    `json:"partId"`
	SN       string `json:"sn"`
	PN       string `json:"pn"`
	PartDesc string `json:"partDesc"`
	LotID    int    `json:"lotId"`
	Lot      string `json:"lot"`
	Created  string `json:"created"`
	Active   bool   `json:"active"`
}

func unitListRows(units []UnitRow, loc *time.Location) []unitListRow {
	out := make([]unitListRow, len(units))
	for i, u := range units {
		out[i] = unitListRow{ID: u.ID, PartID: u.PartID, SN: u.SerialNumber, PN: u.PartNumber, PartDesc: u.PartDescription,
			LotID: u.LotIDVal(), Lot: u.LotNumber, Created: localTime(loc, u.CreatedAt).Format("2006-01-02"), Active: u.IsActive}
	}
	return out
}

// buildListRow is one row of /api/builds/rows (#411); OutLotID is 0 when the build made no lot.
type buildListRow struct {
	ID       int    `json:"id"`
	PartID   int    `json:"partId"`
	PN       string `json:"pn"`
	PartDesc string `json:"partDesc"`
	Qty      string `json:"qty"`
	Date     string `json:"date"`
	OutLotID int    `json:"outLotId"`
	Lot      string `json:"lot"`
	Note     string `json:"note"`
}

func buildListRows(builds []inventory.BuildListRow) []buildListRow {
	out := make([]buildListRow, len(builds))
	for i, b := range builds {
		out[i] = buildListRow{ID: b.ID, PartID: b.PartID, PN: b.PartNumber, PartDesc: b.PartDescription, Qty: b.Qty.String(),
			Date: b.Date.Format("2006-01-02"), OutLotID: b.OutputLotIDVal(), Lot: b.LotNumber, Note: b.Note}
	}
	return out
}

// ── All units / all builds (#362) ────────────────────────────────────────────

// AllUnits — GET /units. Cross-part list of every serialized unit, newest first.
func (h *Handler) AllUnits(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "parts/all_units.html", map[string]any{
		"ActiveTab": "records", "SectionTab": "units", "TestMode": h.cfg().TestMode,
	})
}

// AllUnitsRows — GET /api/units/rows. JSON rows for the All Units table (#411).
func (h *Handler) AllUnitsRows(w http.ResponseWriter, r *http.Request) {
	units, err := h.inventory().ListAllUnits(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeJSON(w, unitListRows(units, h.userLocation(r)))
}

// AllBuilds — GET /builds. Cross-part list of every build, newest first.
func (h *Handler) AllBuilds(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "parts/all_builds.html", map[string]any{
		"ActiveTab": "records", "SectionTab": "builds", "TestMode": h.cfg().TestMode,
	})
}

// AllBuildsRows — GET /api/builds/rows. JSON rows for the All Builds table (#411).
func (h *Handler) AllBuildsRows(w http.ResponseWriter, r *http.Request) {
	builds, err := h.inventory().ListAllBuilds(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeJSON(w, buildListRows(builds))
}
