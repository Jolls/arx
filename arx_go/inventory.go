package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// recordInventoryTxn appends one row to the inventory ledger and updates the
// part's cached stock_on_hand by the same signed qty, inside the caller's tx.
// Shared by manual adjustments (#272/#274) and PO receiving (#269). poLineID is
// nil for everything except receipts. lotID (#676) is the lot this movement touched
// — the lot created on a receipt, the component lot consumed by a build issue, or the
// output lot produced by a build receipt — and is nil for non-lot-tracked parts.
// buildID (#677) is the build that wrote this row (component issue / output receipt),
// nil for movements not driven by a build.
func (h *Handler) recordInventoryTxn(r *http.Request, tx *txLogger, partID int, txnType string, qty float64, txnDate time.Time, reference, note string, poLineID, lotID, buildID *int) error {
	ctx := r.Context()
	var poArg any
	if poLineID != nil {
		poArg = *poLineID
	}
	var lotArg any
	if lotID != nil {
		lotArg = *lotID
	}
	var buildArg any
	if buildID != nil {
		buildArg = *buildID
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (part_id, txn_type, qty, txn_date, username, reference, note, po_line_id, lot_id, build_id, created_at)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11)
	`, h.cfg.InventoryTxnTable()),
		partID, txnType, qty, txnDate, h.actorName(r),
		nullableText(reference), nullableText(note), poArg, lotArg, buildArg, time.Now(),
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET stock_on_hand = stock_on_hand + @p1 WHERE id = @p2`, h.cfg.PartsTable()),
		qty, partID)
	return err
}

// InventoryTxnView is one ledger row for the Transactions tab, with the running
// on-hand balance as of that transaction.
type InventoryTxnView struct {
	Type      string
	Qty       float64
	Date      string
	Username  string
	Reference string
	Note      string
	LotID     int
	LotNumber string
	Balance   float64
}

// ledgerWithBalances takes ledger rows oldest-first, fills each row's running
// on-hand Balance (accumulated oldest→newest), and returns them newest-first for
// display. Pure function so the balance math is unit-testable.
func ledgerWithBalances(asc []InventoryTxnView) []InventoryTxnView {
	var balance float64
	for i := range asc {
		balance += asc[i].Qty
		asc[i].Balance = balance
	}
	out := make([]InventoryTxnView, len(asc))
	for i, v := range asc {
		out[len(asc)-1-i] = v
	}
	return out
}

// ── PartTransactions — GET /part/{id}/transactions ───────────────────────────

func (h *Handler) PartTransactions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "transactions")
	if !ok {
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT it.txn_type, it.qty, it.txn_date, it.username, it.reference, it.note, l.id, l.lot_number
		FROM %s it
		LEFT JOIN %s l ON l.id = it.lot_id
		WHERE it.part_id = @p1 ORDER BY it.txn_date ASC, it.id ASC
	`, h.cfg.InventoryTxnTable(), h.cfg.LotTable()), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving transactions: "+err.Error())
		return
	}
	defer rows.Close()
	var asc []InventoryTxnView
	for rows.Next() {
		var v InventoryTxnView
		var username, reference, note, lotNumber sql.NullString
		var lotID sql.NullInt64
		var date sql.NullTime
		if err := rows.Scan(&v.Type, &v.Qty, &date, &username, &reference, &note, &lotID, &lotNumber); err != nil {
			h.renderError(w, r, "Error reading transactions: "+err.Error())
			return
		}
		v.Username = username.String
		v.Reference = reference.String
		v.Note = note.String
		v.LotID = int(lotID.Int64)
		v.LotNumber = lotNumber.String
		if date.Valid {
			v.Date = date.Time.Format("2006-01-02")
		}
		asc = append(asc, v)
	}
	txns := ledgerWithBalances(asc)

	var lots []LotOption
	if p.IsLotTracked {
		lots, err = h.activeLotsForPart(r.Context(), p.ID)
		if err != nil {
			h.renderError(w, r, "Error retrieving lots: "+err.Error())
			return
		}
	}

	h.render(w, r, "parts/part_transactions.html", map[string]any{
		"Part": p, "Txns": txns, "Lots": lots, "Today": time.Now().Format("2006-01-02"),
		"ActiveTab": "parts", "ActiveSubTab": "transactions",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// ── PartStockAdjust — POST /part/{id}/adjust-stock ───────────────────────────

func (h *Handler) PartStockAdjust(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "transactions")
	if !ok {
		return
	}
	partID := p.ID
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	qty, err := strconv.ParseFloat(fv(r, "qty"), 64)
	if err != nil || qty == 0 {
		h.renderError(w, r, "Enter a non-zero quantity (use a negative value to remove stock).")
		return
	}
	reason := fv(r, "reason")
	if reason == "" {
		h.renderError(w, r, "A reason is required for a stock adjustment.")
		return
	}
	txnDate := parseFormDate(fv(r, "txn_date"))
	if txnDate == nil {
		now := time.Now()
		txnDate = &now
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, r, "Error starting transaction: "+err.Error())
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// A lot-tracked part must attribute every adjustment to a lot — same invariant
	// a build's component consumption enforces — either an existing active lot or
	// a brand-new one named on the spot (e.g. a cycle count finding stock that isn't
	// part of any existing lot). Non-lot-tracked parts ignore both fields entirely.
	var lotID *int
	if p.IsLotTracked {
		lotStr := fv(r, "lot_id")
		newLotNumber := fv(r, "new_lot_number")
		switch {
		case lotStr != "" && newLotNumber != "":
			h.renderError(w, r, "Choose an existing lot or enter a new lot number, not both.")
			return
		case lotStr != "":
			picked, err := strconv.Atoi(lotStr)
			if err != nil {
				h.renderError(w, r, "Invalid lot selection.")
				return
			}
			okLot, err := h.lotBelongsToPart(r.Context(), tx, picked, partID)
			if err != nil {
				h.renderError(w, r, "Error validating lot: "+err.Error())
				return
			}
			if !okLot {
				h.renderError(w, r, "Selected lot is not an active lot of this part.")
				return
			}
			lotID = &picked
		case newLotNumber != "":
			created, err := h.createLot(r.Context(), tx, partID,
				lotCreateArgs{LotNumber: newLotNumber, Description: "Manual entry"}, nil)
			if err != nil {
				h.renderError(w, r, "Error creating lot: "+err.Error())
				return
			}
			lotID = &created
		default:
			h.renderError(w, r, "This part is lot-controlled — select a lot or enter a new lot number.")
			return
		}
	}

	if err := h.recordInventoryTxn(r, tx, partID, "adjustment", qty, *txnDate, "", reason, nil, lotID, nil); err != nil {
		h.renderError(w, r, "Error recording adjustment: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving adjustment: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, "/part/"+id+"/transactions", http.StatusFound)
}
