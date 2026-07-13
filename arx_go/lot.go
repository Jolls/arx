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
// receipt (po_line_id set, lot_number defaults to the PO number); manufactured
// lots are created by a build that produces a lot-tracked output part. lot_genealogy
// records which parent (component) lots were consumed into a child (output) lot.

// LotOption is one active lot of a part, offered in the build form's per-component
// lot picker. Label is a human-readable identifier (lot number + vendor/PO hint).
type LotOption struct {
	ID    int
	Label string
}

// createLot inserts one lot row inside the caller's tx and returns its new id.
// poLineID is nil for manufactured (build) lots; vendorLot is "" when unknown.
func (h *Handler) createLot(ctx context.Context, tx *txLogger, partID int, lotNumber, vendorLot string, poLineID *int) (int, error) {
	var poArg interface{}
	if poLineID != nil {
		poArg = *poLineID
	}
	insert := h.dialect.InsertReturningID(h.cfg.LotTable(),
		`part_id, lot_number, vendor_lot_number, po_line_id, created_at, is_active`,
		`@p1, @p2, @p3, @p4, @p5, @p6`,
		false)
	var lotID int
	err := tx.QueryRowContext(ctx, insert,
		partID, lotNumber, nullableText(vendorLot), poArg, time.Now(), true,
	).Scan(&lotID)
	return lotID, err
}

// activeLotsForPart returns a part's active lots, newest first, for the build
// form's component lot picker. Empty (not an error) when the part has no active lot.
func (h *Handler) activeLotsForPart(ctx context.Context, partID int) ([]LotOption, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, lot_number, vendor_lot_number
		FROM %s WHERE part_id = @p1 AND is_active = %s
		ORDER BY created_at DESC, id DESC
	`, h.cfg.LotTable(), h.dialect.BoolLiteral(true)), partID)
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
		h.cfg.LotTable(), h.dialect.BoolLiteral(true)), lotID, partID).Scan(&n)
	return n == 1, err
}

// recordLotGenealogy inserts one edge linking a consumed parent (component) lot to
// the child (output) lot it fed, inside the caller's tx.
func (h *Handler) recordLotGenealogy(ctx context.Context, tx *txLogger, parentLotID, childLotID int, qtyConsumed float64) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (parent_lot_id, child_lot_id, qty_consumed)
		VALUES (@p1, @p2, @p3)
	`, h.cfg.LotGenealogyTable()), parentLotID, childLotID, qtyConsumed)
	return err
}

// ── Lots view (#676) ─────────────────────────────────────────────────────────

// LotRow is one lot in the Lots subtab list and the header of a genealogy trace.
// Source is a human-readable origin: "PO <number>" for a purchased lot, "Build
// #<id>" for a manufactured one, or "" when neither is linked.
type LotRow struct {
	ID         int
	LotNumber  string
	VendorLot  string
	PartID     int
	PartNumber string
	PartTitle  string
	Source     string
	CreatedAt  time.Time
	IsActive   bool
}

// lotRowSelect is the shared SELECT for a lot joined to its part and origin (PO
// line → PO number, or the build that produced it). A `WHERE …` clause and
// ordering are appended by callers.
func (h *Handler) lotRowSelect() string {
	return fmt.Sprintf(`
		SELECT l.id, l.lot_number, l.vendor_lot_number, l.part_id,
		       p.part_number, p.title, po.number, b.id, l.created_at, l.is_active
		FROM %s l
		JOIN %s p ON p.id = l.part_id
		LEFT JOIN %s pl ON pl.id = l.po_line_id
		LEFT JOIN %s po ON po.id = pl.po_id
		LEFT JOIN %s b ON b.output_lot_id = l.id
	`, h.cfg.LotTable(), h.cfg.PartsTable(), h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.BuildTable())
}

// scanLotRow reads one LotRow from a row cursor over lotRowSelect's columns.
func scanLotRow(sc interface{ Scan(...any) error }) (LotRow, error) {
	var lr LotRow
	var vendorLot, partNumber, partTitle, poNumber sql.NullString
	var buildID sql.NullInt64
	if err := sc.Scan(&lr.ID, &lr.LotNumber, &vendorLot, &lr.PartID,
		&partNumber, &partTitle, &poNumber, &buildID, &lr.CreatedAt, &lr.IsActive); err != nil {
		return LotRow{}, err
	}
	lr.VendorLot = vendorLot.String
	lr.PartNumber = partNumber.String
	lr.PartTitle = partTitle.String
	switch {
	case poNumber.Valid:
		lr.Source = "PO " + poNumber.String
	case buildID.Valid:
		lr.Source = fmt.Sprintf("Build #%d", buildID.Int64)
	}
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
	`, h.cfg.LotGenealogyTable(), h.cfg.LotTable(), joinCol, h.cfg.PartsTable(), whereCol), lotID)
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

// lotTrace walks lot_genealogy from rootID and returns the reachable lots flattened
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
	lots, err := h.lotsForPart(r.Context(), p.PNID)
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
	if !found || lot.PartID != p.PNID {
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
