package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// Unit — Tier-3 serialized instance of a part (#740, traceability epic #736). A unit
// is created lazily at test time (#745, Q5); this file adds the read-only Units subtab
// list and the per-serial genealogy trace — the "birth certificate" (#746 slice 9).

// UnitRow is one serialized unit for the Units subtab list and the header of a unit's
// genealogy trace. LotNumber is joined for display and is "" when LotID is nil.
type UnitRow struct {
	ID           int
	SerialNumber string
	PartID       int
	PartNumber   string
	PartTitle    string
	LotID        *int
	LotNumber    string
	BuildID      *int
	IsActive     bool
	CreatedAt    time.Time
}

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
		SELECT u.id, u.serial_number, u.part_id, p.part_number, p.title,
		       u.lot_id, l.lot_number, u.build_id, u.is_active, u.created_at
		FROM %s u
		JOIN %s p ON p.id = u.part_id
		LEFT JOIN %s l ON l.id = u.lot_id
	`, h.cfg.UnitTable(), h.cfg.PartsTable(), h.cfg.LotTable())
}

// scanUnitRow reads one UnitRow from a cursor over unitRowSelect's columns.
func scanUnitRow(sc interface{ Scan(...any) error }) (UnitRow, error) {
	var ur UnitRow
	var partNumber, partTitle, lotNumber sql.NullString
	var lotID, buildID sql.NullInt64
	if err := sc.Scan(&ur.ID, &ur.SerialNumber, &ur.PartID, &partNumber, &partTitle,
		&lotID, &lotNumber, &buildID, &ur.IsActive, &ur.CreatedAt); err != nil {
		return UnitRow{}, err
	}
	ur.PartNumber = partNumber.String
	ur.PartTitle = partTitle.String
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
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
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
	h.render(w, r, "parts/part_unit_trace.html", map[string]any{
		"Part": p, "Unit": unit, "Build": build, "Ancestors": ancestors, "Descendants": descendants,
		"ActiveTab": "parts", "ActiveSubTab": "units",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}
