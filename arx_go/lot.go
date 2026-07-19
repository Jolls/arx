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

// Lot control (#676, part of the #568 lot epic). A lot is one batch instance of a
// lot-tracked part (part.is_lot_tracked). Purchased lots are created at goods
// receipt (po_line_id set); manufactured lots are created by a build that produces
// a lot-tracked output part. Auto-issued lot_number defaults to the lot's own id
// (#687); lot_description carries the human-readable provenance. The genealogy
// table records which parent (component) lots were consumed into a child (output) lot.

// LotOption is one active lot of a part, offered in the build form's per-component
// lot picker. Label is a human-readable identifier (lot number + vendor/PO hint).
type LotOption struct {
	ID    int
	Label string
}

// lotCreateArgs groups createLot's free-text fields so a positional call can't
// silently swap VendorLot and Description (both are plain strings).
type lotCreateArgs struct {
	LotNumber   string // "" for auto-issued lots — defaults to the lot's own id (#687)
	VendorLot   string // supplier's own lot/batch ID (purchased lots); "" when unknown
	Description string // human-readable provenance stored in lot_description
}

// createLot inserts one lot row inside the caller's tx and returns its new id.
// poLineID is nil for manufactured (build) lots. args.LotNumber == "" (auto-issued
// lots) defers to the lot's own id, guaranteed unique by construction (#687) — a
// second UPDATE sets it once the id is known post-insert.
func (h *Handler) createLot(ctx context.Context, tx *txLogger, partID int, args lotCreateArgs, poLineID *int) (int, error) {
	var poArg interface{}
	if poLineID != nil {
		poArg = *poLineID
	}
	insert := h.dia().InsertReturningID(h.cfg.LotTable(),
		`part_id, lot_number, lot_description, vendor_lot_number, po_line_id, created_at, is_active`,
		`@p1, @p2, @p3, @p4, @p5, @p6, @p7`,
		false)
	var lotID int
	err := tx.QueryRowContext(ctx, insert,
		partID, args.LotNumber, args.Description, nullableText(args.VendorLot), poArg, time.Now(), true,
	).Scan(&lotID)
	if err != nil || args.LotNumber != "" {
		return lotID, err
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET lot_number = @p1 WHERE id = @p2`, h.cfg.LotTable()),
		strconv.Itoa(lotID), lotID)
	return lotID, err
}

// activeLotsForPart returns a part's active lots, newest first, for the build
// form's component lot picker. Empty (not an error) when the part has no active lot.
func (h *Handler) activeLotsForPart(ctx context.Context, partID int) ([]LotOption, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, lot_number, vendor_lot_number
		FROM %s WHERE part_id = @p1 AND is_active = %s
		ORDER BY created_at DESC, id DESC
	`, h.cfg.LotTable(), h.dia().BoolLiteral(true)), partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lots []LotOption
	for rows.Next() {
		var o LotOption
		var lotNumber, vendorLot sql.NullString
		if err := rows.Scan(&o.ID, &lotNumber, &vendorLot); err != nil {
			return nil, err
		}
		o.Label = lotNumber.String
		if vendorLot.String != "" {
			o.Label += " (vendor " + vendorLot.String + ")"
		}
		lots = append(lots, o)
	}
	return lots, rows.Err()
}

// lotBelongsToPart reports whether lotID is an active lot of partID — guards the
// build's genealogy writes against a forged or stale lot selection in the POST.
func (h *Handler) lotBelongsToPart(ctx context.Context, tx *txLogger, lotID, partID int) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE id = @p1 AND part_id = @p2 AND is_active = %s`,
		h.cfg.LotTable(), h.dia().BoolLiteral(true)), lotID, partID).Scan(&n)
	return n == 1, err
}

// recordGenealogy inserts one genealogy edge linking a consumed parent (component)
// lot to the child (output) lot it fed, inside the caller's tx. Lot→lot only; unit
// endpoints (parent_unit_id/child_unit_id) are written from slice 8 (#736).
func (h *Handler) recordGenealogy(ctx context.Context, tx *txLogger, parentLotID, childLotID int, qtyConsumed float64) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (parent_lot_id, child_lot_id, qty_consumed)
		VALUES (@p1, @p2, @p3)
	`, h.cfg.GenealogyTable()), parentLotID, childLotID, qtyConsumed)
	return err
}

// ── Lots view (#676) ─────────────────────────────────────────────────────────

// LotRow is one lot in the Lots subtab list and the header of a genealogy trace.
// LotDescription is the human-readable provenance stored at creation (#687): "PO
// <number>" for a purchased lot, "Build #<id>" for a manufactured one, "Manual
// entry" for one created directly on the inventory adjustment tab.
type LotRow struct {
	ID             int
	LotNumber      string
	VendorLot      string
	PartID         int
	PartNumber     string
	PartTitle      string
	LotDescription string
	CreatedAt      time.Time
	IsActive       bool
}

// lotRowSelect is the shared SELECT for a lot joined to its part. A `WHERE …`
// clause and ordering are appended by callers.
func (h *Handler) lotRowSelect() string {
	return fmt.Sprintf(`
		SELECT l.id, l.lot_number, l.vendor_lot_number, l.part_id,
		       p.part_number, p.title, l.lot_description, l.created_at, l.is_active
		FROM %s l
		JOIN %s p ON p.id = l.part_id
	`, h.cfg.LotTable(), h.cfg.PartsTable())
}

// scanLotRow reads one LotRow from a row cursor over lotRowSelect's columns.
func scanLotRow(sc interface{ Scan(...any) error }) (LotRow, error) {
	var lr LotRow
	var vendorLot, partNumber, partTitle sql.NullString
	if err := sc.Scan(&lr.ID, &lr.LotNumber, &vendorLot, &lr.PartID,
		&partNumber, &partTitle, &lr.LotDescription, &lr.CreatedAt, &lr.IsActive); err != nil {
		return LotRow{}, err
	}
	lr.VendorLot = vendorLot.String
	lr.PartNumber = partNumber.String
	lr.PartTitle = partTitle.String
	return lr, nil
}

// lotsForPart returns every lot of a part, newest first, for the Lots subtab list.
func (h *Handler) lotsForPart(ctx context.Context, partID int) ([]LotRow, error) {
	rows, err := h.queryContext(ctx, h.lotRowSelect()+
		`WHERE l.part_id = @p1 ORDER BY l.created_at DESC, l.id DESC`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LotRow
	for rows.Next() {
		lr, err := scanLotRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// fetchLotRow loads a single lot for the genealogy trace header. ok=false (nil
// error) when the lot does not exist.
func (h *Handler) fetchLotRow(ctx context.Context, lotID int) (LotRow, bool, error) {
	lr, err := scanLotRow(h.queryRowContext(ctx, h.lotRowSelect()+`WHERE l.id = @p1`, lotID))
	if err == sql.ErrNoRows {
		return LotRow{}, false, nil
	}
	if err != nil {
		return LotRow{}, false, err
	}
	return lr, true, nil
}

// LotTraceNode is one lot in a genealogy trace, flattened with Depth for indented
// rendering. Qty is qty_consumed on the edge connecting this node to its
// predecessor (how much of a parent lot fed the child that led here).
type LotTraceNode struct {
	ID          int
	LotNumber   string
	VendorLot   string
	PartID      int
	PartNumber  string
	PartTitle   string
	IsVendorLot bool // po_line_id set → a purchased raw/vendor lot (a genealogy leaf)
	Qty         float64
	Depth       int
}

// lotNeighbors returns the immediate parent (ancestors) or child (descendants)
// lots of one lot in the genealogy. It fully drains its cursor before returning so
// the caller can recurse without exhausting the connection pool.
//
// Lot endpoints only: the inner JOIN on g.parent_lot_id/child_lot_id silently skips
// any edge with a unit endpoint (those lot columns NULL). Correct today — every
// genealogy row is lot→lot — but slice 8 (#736), which starts writing unit endpoints,
// must generalize this walk (union the unit joins) or the trace will under-report.
func (h *Handler) lotNeighbors(ctx context.Context, lotID int, ancestors bool) ([]LotTraceNode, error) {
	joinCol, whereCol := "parent_lot_id", "child_lot_id"
	if !ancestors {
		joinCol, whereCol = "child_lot_id", "parent_lot_id"
	}
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT l.id, l.lot_number, l.vendor_lot_number, l.po_line_id,
		       p.id, p.part_number, p.title, g.qty_consumed
		FROM %s g
		JOIN %s l ON l.id = g.%s
		JOIN %s p ON p.id = l.part_id
		WHERE g.%s = @p1
		ORDER BY l.id
	`, h.cfg.GenealogyTable(), h.cfg.LotTable(), joinCol, h.cfg.PartsTable(), whereCol), lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LotTraceNode
	for rows.Next() {
		var n LotTraceNode
		var vendorLot, partNumber, partTitle sql.NullString
		var poLineID sql.NullInt64
		if err := rows.Scan(&n.ID, &n.LotNumber, &vendorLot, &poLineID,
			&n.PartID, &partNumber, &partTitle, &n.Qty); err != nil {
			return nil, err
		}
		n.VendorLot = vendorLot.String
		n.PartNumber = partNumber.String
		n.PartTitle = partTitle.String
		n.IsVendorLot = poLineID.Valid
		out = append(out, n)
	}
	return out, rows.Err()
}

// lotTrace walks the genealogy table from rootID and returns the reachable lots flattened
// depth-first (ancestors=parents down to raw vendor lots, or descendants=children).
// A visited set guards against cycles, so each lot's subtree is expanded once.
func (h *Handler) lotTrace(ctx context.Context, rootID int, ancestors bool) ([]LotTraceNode, error) {
	var out []LotTraceNode
	visited := map[int]bool{rootID: true}
	var walk func(id, depth int) error
	walk = func(id, depth int) error {
		neighbors, err := h.lotNeighbors(ctx, id, ancestors)
		if err != nil {
			return err
		}
		for _, n := range neighbors {
			n.Depth = depth
			out = append(out, n)
			if !visited[n.ID] {
				visited[n.ID] = true
				if err := walk(n.ID, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(rootID, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// PartLots — GET /part/{id}/lots. Lists a lot-controlled part's lots, each linking
// to its genealogy trace.
func (h *Handler) PartLots(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "lots")
	if !ok {
		return
	}
	lots, err := h.lotsForPart(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving lots: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_lots.html", map[string]any{
		"Part": p, "Lots": lots,
		"ActiveTab": "parts", "ActiveSubTab": "lots",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// PartLotTrace — GET /part/{id}/lots/{lotID}. Shows one lot's genealogy: its
// ancestors (recursed to raw vendor lots) and descendants.
func (h *Handler) PartLotTrace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "lots")
	if !ok {
		return
	}
	lotID, err := strconv.Atoi(chi.URLParam(r, "lotID"))
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
	ancestors, err := h.lotTrace(r.Context(), lotID, true)
	if err != nil {
		h.renderError(w, r, "Error tracing lot ancestry: "+err.Error())
		return
	}
	descendants, err := h.lotTrace(r.Context(), lotID, false)
	if err != nil {
		h.renderError(w, r, "Error tracing lot descendants: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_lot_trace.html", map[string]any{
		"Part": p, "Lot": lot, "Ancestors": ancestors, "Descendants": descendants,
		"ActiveTab": "parts", "ActiveSubTab": "lots",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// LotEdit — GET /part/{id}/lots/{lotID}/edit. Form to edit a lot's Lot
// Description and Vendor Lot (#701) — the only two free-text fields set at
// creation that are safe to revise after the fact.
func (h *Handler) LotEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "lots")
	if !ok {
		return
	}
	lotID, err := strconv.Atoi(chi.URLParam(r, "lotID"))
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
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// LotUpdate — POST /part/{id}/lots/{lotID}. Saves Lot Description and Vendor
// Lot; every other lot field is read-only (#701).
func (h *Handler) LotUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "lots")
	if !ok {
		return
	}
	lotID, err := strconv.Atoi(chi.URLParam(r, "lotID"))
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
	_, err = h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET lot_description = @p1, vendor_lot_number = @p2 WHERE id = @p3 AND part_id = @p4`,
		h.cfg.LotTable()), description, nullableText(vendorLot), lotID, p.ID)
	if err != nil {
		h.renderError(w, r, "Error saving lot: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/lots/%d", id, lotID), http.StatusFound)
}

// ── All lots (#701) ──────────────────────────────────────────────────────────

// AllLots — GET /lots. Cross-part list of every lot, newest first, for
// browsing without drilling into a specific part first.
func (h *Handler) AllLots(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryContext(r.Context(), h.lotRowSelect()+
		`ORDER BY l.created_at DESC, l.id DESC`)
	if err != nil {
		h.renderError(w, r, "Error retrieving lots: "+err.Error())
		return
	}
	defer rows.Close()
	var lots []LotRow
	for rows.Next() {
		lr, err := scanLotRow(rows)
		if err != nil {
			h.renderError(w, r, "Error retrieving lots: "+err.Error())
			return
		}
		lots = append(lots, lr)
	}
	if err := rows.Err(); err != nil {
		h.renderError(w, r, "Error retrieving lots: "+err.Error())
		return
	}
	h.render(w, r, "parts/all_lots.html", map[string]any{
		"Lots": lots, "ActiveTab": "parts", "TestMode": h.cfg.TestMode,
	})
}
