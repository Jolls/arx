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
// nil for everything except receipts.
func (h *Handler) recordInventoryTxn(r *http.Request, tx *txLogger, partID int, txnType string, qty float64, txnDate time.Time, reference, note string, poLineID *int) error {
	ctx := r.Context()
	var poArg interface{}
	if poLineID != nil {
		poArg = *poLineID
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (part_id, txn_type, qty, txn_date, username, reference, note, po_line_id, created_at)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9)
	`, h.cfg.InventoryTxnTable()),
		partID, txnType, qty, txnDate, h.actorName(r),
		nullableText(reference), nullableText(note), poArg, time.Now(),
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
		SELECT txn_type, qty, txn_date, username, reference, note
		FROM %s WHERE part_id = @p1 ORDER BY txn_date ASC, id ASC
	`, h.cfg.InventoryTxnTable()), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving transactions: "+err.Error())
		return
	}
	defer rows.Close()
	var asc []InventoryTxnView
	for rows.Next() {
		var v InventoryTxnView
		var username, reference, note sql.NullString
		var date sql.NullTime
		if err := rows.Scan(&v.Type, &v.Qty, &date, &username, &reference, &note); err != nil {
			h.renderError(w, r, "Error reading transactions: "+err.Error())
			return
		}
		v.Username = username.String
		v.Reference = reference.String
		v.Note = note.String
		if date.Valid {
			v.Date = date.Time.Format("2006-01-02")
		}
		asc = append(asc, v)
	}
	txns := ledgerWithBalances(asc)

	h.render(w, r, "part_transactions.html", map[string]any{
		"Part": p, "Txns": txns, "Today": time.Now().Format("2006-01-02"),
		"ActiveTab": "parts", "ActiveSubTab": "transactions",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// ── PartStockAdjust — POST /part/{id}/adjust-stock ───────────────────────────

func (h *Handler) PartStockAdjust(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	partID, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Invalid part")
		return
	}
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

	if err := h.recordInventoryTxn(r, tx, partID, "adjustment", qty, *txnDate, "", reason, nil); err != nil {
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
