package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"arx/arx_go/models"
	"github.com/go-chi/chi/v5"
)

// Unit — Tier-3 serialized instance of a part (#740, traceability epic #736). A unit
// is created lazily at test time (#745, Q5); this file adds the read-only Units subtab
// list and the per-serial genealogy trace — the "birth certificate" (#746 slice 9).

// UnitRow is one serialized unit for the Units subtab list and the header of a unit's
// genealogy trace. LotNumber is joined for display and is "" when LotID is nil.
type UnitRow struct {
	ID              int
	SerialNumber    string
	PartID          int
	PartNumber      string
	PartDescription string
	LotID           *int
	LotNumber       string
	BuildID         *int
	IsActive        bool
	CreatedAt       time.Time
	Source          string // "test" | "manual" (#799)
}

// IsManual reports whether this unit was manually back-filled (#799), for the
// Units list's Source badge.
func (u UnitRow) IsManual() bool { return u.Source == "manual" }

// LotIDVal / BuildIDVal return the dereferenced id (0 when nil) for template links;
// templates gate on {{if .LotID}} first, so 0 is never rendered as a live link.
func (u UnitRow) LotIDVal() int {
	if u.LotID != nil {
		return *u.LotID
	}
	return 0
}
func (u UnitRow) BuildIDVal() int {
	if u.BuildID != nil {
		return *u.BuildID
	}
	return 0
}

// unitRowSelect is the shared SELECT for a unit joined to its part and (nullable) lot.
// A `WHERE …` clause and ordering are appended by callers.
func (h *Handler) unitRowSelect() string {
	return fmt.Sprintf(`
		SELECT u.id, u.serial_number, u.part_id, p.part_number, p.description,
		       u.lot_id, l.lot_number, u.build_id, u.is_active, u.created_at, u.source
		FROM %s u
		JOIN %s p ON p.id = u.part_id
		LEFT JOIN %s l ON l.id = u.lot_id
	`, h.cfg().UnitTable(), h.cfg().PartsTable(), h.cfg().LotTable())
}

// scanUnitRow reads one UnitRow from a cursor over unitRowSelect's columns.
func scanUnitRow(sc interface{ Scan(...any) error }) (UnitRow, error) {
	var ur UnitRow
	var partNumber, partDescription, lotNumber sql.NullString
	var lotID, buildID sql.NullInt64
	if err := sc.Scan(&ur.ID, &ur.SerialNumber, &ur.PartID, &partNumber, &partDescription,
		&lotID, &lotNumber, &buildID, &ur.IsActive, &ur.CreatedAt, &ur.Source); err != nil {
		return UnitRow{}, err
	}
	ur.PartNumber = partNumber.String
	ur.PartDescription = partDescription.String
	ur.LotNumber = lotNumber.String
	if lotID.Valid {
		v := int(lotID.Int64)
		ur.LotID = &v
	}
	if buildID.Valid {
		v := int(buildID.Int64)
		ur.BuildID = &v
	}
	return ur, nil
}

// unitsForPart returns every unit of a part, newest first, for the Units subtab list.
func (h *Handler) unitsForPart(ctx context.Context, partID int) ([]UnitRow, error) {
	rows, err := h.queryContext(ctx, h.unitRowSelect()+
		`WHERE u.part_id = @p1 ORDER BY u.created_at DESC, u.id DESC`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnitRow
	for rows.Next() {
		ur, err := scanUnitRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ur)
	}
	return out, rows.Err()
}

// recentPartUnits returns the most recent units for a part, newest first, capped
// at limit, for the Part dashboard "Units" card (#798).
func (h *Handler) recentPartUnits(ctx context.Context, partID int, limit int) ([]UnitRow, error) {
	top, limitClause := h.topLimit("@p2")
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %su.id, u.serial_number, u.part_id, p.part_number, p.description,
		       u.lot_id, l.lot_number, u.build_id, u.is_active, u.created_at, u.source
		FROM %s u
		JOIN %s p ON p.id = u.part_id
		LEFT JOIN %s l ON l.id = u.lot_id
		WHERE u.part_id = @p1 ORDER BY u.created_at DESC, u.id DESC
	`+limitClause, top, h.cfg().UnitTable(), h.cfg().PartsTable(), h.cfg().LotTable()), partID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnitRow
	for rows.Next() {
		ur, err := scanUnitRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ur)
	}
	return out, rows.Err()
}

// unitCountForPart returns the total number of units for a part, for the Part
// dashboard "Units" card (#798).
func (h *Handler) unitCountForPart(ctx context.Context, partID int) (int, error) {
	var count int
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE part_id = @p1`, h.cfg().UnitTable()), partID).Scan(&count)
	return count, err
}

// fetchUnitRow loads a single unit for the trace header. ok=false (nil error) when
// the unit does not exist.
func (h *Handler) fetchUnitRow(ctx context.Context, unitID int) (UnitRow, bool, error) {
	ur, err := scanUnitRow(h.queryRowContext(ctx, h.unitRowSelect()+`WHERE u.id = @p1`, unitID))
	if err == sql.ErrNoRows {
		return UnitRow{}, false, nil
	}
	if err != nil {
		return UnitRow{}, false, err
	}
	return ur, true, nil
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
	typeOptions, err := h.scopedRecordTypeOptions(r.Context(), "unit_id", unitID)
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
	out, err := h.scopedRecordsRows(r.Context(), "unit_id", unitID)
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
	var n int
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE unit_id = @p1 AND is_locked = %s`,
		h.cfg().RecordsTable(), h.dia().BoolLiteral(true)), unitID).Scan(&n)
	return n > 0, err
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
	insertUnit := h.dia().InsertReturningID(h.cfg().UnitTable(),
		`part_id, serial_number, lot_id, build_id, source`, `@p1, @p2, @p3, @p4, 'manual'`, false)
	var unitID int
	if err := h.queryRowContext(r.Context(), insertUnit, p.ID, serial, lotArg, buildArg).Scan(&unitID); err != nil {
		h.renderUnitSaveErr(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/units/%d", partID, unitID), http.StatusSeeOther)
}

// renderUnitSaveErr renders a friendly message for a duplicate serial
// (UQ_unit_serial), or the raw error otherwise — shared by UnitCreate and
// UnitUpdate, the two unit-writing handlers (#799). Matched case-insensitively:
// Postgres folds an unquoted constraint name to lowercase (uq_unit_serial) where
// SQL Server preserves the case as declared in SQL/azure/unit.sql.
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
		_, err = h.execContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET is_active = @p1 WHERE id = @p2 AND part_id = @p3`,
			h.cfg().UnitTable()), isActive, unitID, p.ID)
	} else {
		serial := fv(r, "serial_number")
		if serial == "" {
			h.renderError(w, r, "Serial # is required.")
			return
		}
		_, err = h.execContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET is_active = @p1, serial_number = @p2 WHERE id = @p3 AND part_id = @p4`,
			h.cfg().UnitTable()), isActive, serial, unitID, p.ID)
	}
	if err != nil {
		h.renderUnitSaveErr(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/units/%d", id, unitID), http.StatusSeeOther)
}
