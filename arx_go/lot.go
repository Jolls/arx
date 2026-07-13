package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
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
