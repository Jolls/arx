package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"arx/arx_go/models"
	"arx/internal/inventory"
	"arx/internal/records"
	"github.com/go-chi/chi/v5"
)

// Unit — Tier-3 serialized instance of a part (#740, traceability epic #736). A unit
// is created lazily at test time (#745, Q5); this file adds the read-only Units subtab
// list and the per-serial genealogy trace — the "birth certificate" (#746 slice 9).

// UnitRow is one serialized unit for the Units subtab list and the header of a unit's
// genealogy trace. LotNumber is joined for display and is "" when LotID is nil.
type UnitRow = inventory.UnitRow

// unitsForPart returns every unit of a part, newest first, for the Units subtab list.
func (h *Handler) unitsForPart(ctx context.Context, partID int) ([]UnitRow, error) {
	return h.inventory().ListPartUnits(ctx, partID)
}

// recentPartUnits returns the most recent units for a part, newest first, capped
// at limit, for the Part dashboard "Units" card (#798).
func (h *Handler) recentPartUnits(ctx context.Context, partID int, limit int) ([]UnitRow, error) {
	return h.inventory().ListRecentPartUnits(ctx, partID, limit)
}

// unitCountForPart returns the total number of units for a part, for the Part
// dashboard "Units" card (#798).
func (h *Handler) unitCountForPart(ctx context.Context, partID int) (int, error) {
	return h.inventory().CountPartUnits(ctx, partID)
}

// fetchUnitRow loads a single unit for the trace header. ok=false (nil error) when
// the unit does not exist.
func (h *Handler) fetchUnitRow(ctx context.Context, unitID int) (UnitRow, bool, error) {
	return h.inventory().GetUnit(ctx, unitID)
}

// PartUnits — GET /part/{id}/units. Lists a serial/lot_serial-tracked part's units,
// each linking to its genealogy trace (#746).
func (h *Handler) PartUnits(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "units")
	if !ok {
		return
	}
	units, err := h.unitsForPart(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving units: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_units.html", map[string]any{
		"Part": p, "Units": units,
		"ActiveTab": "parts", "ActiveSubTab": "units",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// PartUnitTrace — GET /part/{id}/units/{unitID}. Shows one unit's full genealogy —
// its ancestors (recursed to raw vendor lots and serialized parent units) and
// descendants — the per-serial "birth certificate" (#746).
func (h *Handler) PartUnitTrace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "units")
	if !ok {
		return
	}
	unitID, err := strconv.Atoi(chi.URLParam(r, "unitID"))
	if err != nil {
		h.renderError(w, r, "Invalid unit id")
		return
	}
	unit, found, err := h.fetchUnitRow(r.Context(), unitID)
	if err != nil {
		h.renderError(w, r, "Error retrieving unit: "+err.Error())
		return
	}
	if !found || unit.PartID != p.ID {
		h.renderError(w, r, "Unit not found for this part")
		return
	}
	var build *BuildOption
	if unit.BuildID != nil {
		build, err = h.fetchBuildOption(r.Context(), *unit.BuildID)
		if err != nil {
			h.renderError(w, r, "Error retrieving build: "+err.Error())
			return
		}
	}
	// A lot_serial unit belongs to its lot, and its as-built components ARE that lot's
	// (#746, finding 1). Seed the walk from both the unit (direct unit-endpoint edges)
	// and its lot (which carries the consumed-component genealogy written at build time),
	// sharing one visited set so nothing is double-expanded.
	roots := []traceRoot{{unitID, "unit"}}
	if unit.LotID != nil {
		roots = append(roots, traceRoot{*unit.LotID, "lot"})
	}
	ancestors, err := h.genealogyTraceRoots(r.Context(), roots, true)
	if err != nil {
		h.renderError(w, r, "Error tracing unit ancestry: "+err.Error())
		return
	}
	descendants, err := h.genealogyTraceRoots(r.Context(), roots, false)
	if err != nil {
		h.renderError(w, r, "Error tracing unit descendants: "+err.Error())
		return
	}
	typeOptions, err := h.scopedRecordTypeOptions(r.Context(), records.ScopeUnit, unitID)
	if err != nil {
		h.renderError(w, r, "Error retrieving record types: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_unit_trace.html", map[string]any{
		"Part": p, "Unit": unit, "Build": build, "Ancestors": ancestors, "Descendants": descendants,
		"TypeOptions": typeOptions,
		"ActiveTab": "parts", "ActiveSubTab": "units",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// UnitRecordsRows — GET /api/part/{id}/units/{unitID}/records/rows. JSON rows
// for the records table on the unit trace page (#875).
func (h *Handler) UnitRecordsRows(w http.ResponseWriter, r *http.Request) {
	unitID, err := strconv.Atoi(chi.URLParam(r, "unitID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := h.scopedRecordsRows(r.Context(), records.ScopeUnit, unitID)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeJSON(w, out)
}

// unitSerialLocked reports whether a unit's serial is frozen — true once any locked
// form_record points at it (#736 §6 Q5: "editable until the unit's first locked
// form_record"). Scrap (is_active) stays editable regardless.
func (h *Handler) unitSerialLocked(ctx context.Context, unitID int) (bool, error) {
	return h.inventory().UnitSerialLocked(ctx, unitID)
}

// UnitNew — GET /part/{id}/units/new. Form to add a serial for a serial/lot_serial
// part with no test record involved (#799: a pre-existing unit that predates Arx's
// traceability data). Optionally links a lot and/or build for provenance.
func (h *Handler) UnitNew(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "units")
	if !ok {
		return
	}
	var lots []LotOption
	var err error
	if models.TracksLots(p.TrackingMode) {
		lots, err = h.activeLotsForPart(r.Context(), p.ID)
		if err != nil {
			h.renderError(w, r, "Error retrieving lots: "+err.Error())
			return
		}
	}
	builds, err := h.activeBuildsForPart(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving builds: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_unit_form.html", map[string]any{
		"Part": p, "Unit": UnitRow{}, "IsNew": true, "Lots": lots, "Builds": builds,
		"ActiveTab": "parts", "ActiveSubTab": "units",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// UnitCreate — POST /part/{id}/units. Manually mints a unit with source='manual',
// no test record involved (#799). lot_id/build_id are optional and, unlike a
// test-minted unit, may both be blank — the provenance CHECK dropped in migrate_799
// no longer requires either.
func (h *Handler) UnitCreate(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, partID, "units")
	if !ok {
		return
	}
	serial := fv(r, "serial_number")
	if serial == "" {
		h.renderError(w, r, "Serial # is required.")
		return
	}
	lotArg, buildArg, err := h.recordLinkageArgs(r, p.ID)
	if err != nil {
		log.Printf("invalid lot/build selection: %v", err)
		http.Error(w, "invalid lot or build selection", http.StatusBadRequest)
		return
	}
	unitID, err := h.inventory().CreateManualUnit(r.Context(), p.ID, serial, lotArg, buildArg)
	if err != nil {
		h.renderUnitSaveErr(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/units/%d", partID, unitID), http.StatusSeeOther)
}

// renderUnitSaveErr renders a friendly message for a duplicate serial
// (UQ_unit_serial), or the raw error otherwise — shared by UnitCreate and
// UnitUpdate, the two unit-writing handlers (#799). Matched case-insensitively:
// Postgres folds the unquoted constraint name to lowercase (uq_unit_serial).
func (h *Handler) renderUnitSaveErr(w http.ResponseWriter, r *http.Request, err error) {
	if strings.Contains(strings.ToLower(err.Error()), "uq_unit_serial") {
		h.renderError(w, r, "A unit with this serial already exists for this part.")
		return
	}
	h.renderError(w, r, "Error saving unit: "+err.Error())
}

// UnitEdit — GET /part/{id}/units/{unitID}/edit. Form to fix a typo'd serial or
// toggle scrap (#799). Serial is read-only once the unit's serial is locked (see
// unitSerialLocked).
func (h *Handler) UnitEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "units")
	if !ok {
		return
	}
	unitID, err := strconv.Atoi(chi.URLParam(r, "unitID"))
	if err != nil {
		h.renderError(w, r, "Invalid unit id")
		return
	}
	unit, found, err := h.fetchUnitRow(r.Context(), unitID)
	if err != nil {
		h.renderError(w, r, "Error retrieving unit: "+err.Error())
		return
	}
	if !found || unit.PartID != p.ID {
		h.renderError(w, r, "Unit not found for this part")
		return
	}
	locked, err := h.unitSerialLocked(r.Context(), unitID)
	if err != nil {
		h.renderError(w, r, "Error checking unit lock state: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_unit_form.html", map[string]any{
		"Part": p, "Unit": unit, "IsNew": false, "SerialLocked": locked,
		"ActiveTab": "parts", "ActiveSubTab": "units",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// UnitUpdate — POST /part/{id}/units/{unitID}. Saves the scrap toggle always, and
// the serial only when not locked (server-side re-check — never trust a disabled
// input alone) (#799).
func (h *Handler) UnitUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "units")
	if !ok {
		return
	}
	unitID, err := strconv.Atoi(chi.URLParam(r, "unitID"))
	if err != nil {
		h.renderError(w, r, "Invalid unit id")
		return
	}
	unit, found, err := h.fetchUnitRow(r.Context(), unitID)
	if err != nil {
		h.renderError(w, r, "Error retrieving unit: "+err.Error())
		return
	}
	if !found || unit.PartID != p.ID {
		h.renderError(w, r, "Unit not found for this part")
		return
	}
	isActive := fv(r, "is_active") != ""
	locked, err := h.unitSerialLocked(r.Context(), unitID)
	if err != nil {
		h.renderError(w, r, "Error checking unit lock state: "+err.Error())
		return
	}
	if locked {
		err = h.inventory().SetUnitActive(r.Context(), unitID, p.ID, isActive)
	} else {
		serial := fv(r, "serial_number")
		if serial == "" {
			h.renderError(w, r, "Serial # is required.")
			return
		}
		err = h.inventory().UpdateUnit(r.Context(), unitID, p.ID, serial, isActive)
	}
	if err != nil {
		h.renderUnitSaveErr(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/units/%d", id, unitID), http.StatusSeeOther)
}
