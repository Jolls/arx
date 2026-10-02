package main

import (
	"net/http"
	"strconv"
	"time"

	"arx/internal/inventory"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

// recordInventoryTxn appends one row to the inventory ledger and updates the
// part's cached stock_on_hand by the same signed qty, inside the caller's tx.
// Shared by manual adjustments (#272/#274) and PO receiving (#269). poLineID is
// nil for everything except receipts. lotID (#676) is the lot this movement touched
// — the lot created on a receipt, the component lot consumed by a build issue, or the
// output lot produced by a build receipt — and is nil for non-lot-tracked parts.
// buildID (#677) is the build that wrote this row (component issue / output receipt),
// nil for movements not driven by a build.
func (h *Handler) recordInventoryTxn(r *http.Request, tx *txLogger, partID int, txnType string, qty decimal.Decimal, txnDate time.Time, reference, note string, poLineID, lotID, buildID *int) error {
	return inventory.New(tx).RecordTxn(r.Context(), inventory.Txn{
		PartID: partID, Type: txnType, Qty: qty, Date: txnDate, Username: h.actorName(r),
		Reference: reference, Note: note, POLineID: poLineID, LotID: lotID, BuildID: buildID,
	})
}

func (h *Handler) inventory() *inventory.Service { return inventory.New(handlerDB{h}) }

// InventoryTxnView is one ledger row for the Transactions tab, with the running
// on-hand balance as of that transaction.
type InventoryTxnView struct {
	Type      string
	Qty       decimal.Decimal
	Date      string
	Username  string
	Reference string
	Note      string
	LotID     int
	LotNumber string
	Balance   decimal.Decimal
}

// ledgerWithBalances takes ledger rows oldest-first, fills each row's running
// on-hand Balance (accumulated oldest→newest), and returns them newest-first for
// display. Pure function so the balance math is unit-testable.
func ledgerWithBalances(asc []InventoryTxnView) []InventoryTxnView {
	var balance decimal.Decimal
	for i := range asc {
		balance = balance.Add(asc[i].Qty)
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
	rows, err := h.inventory().ListLedger(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving transactions: "+err.Error())
		return
	}
	asc := make([]InventoryTxnView, len(rows))
	for i, row := range rows {
		asc[i] = InventoryTxnView{
			Type: row.Type, Qty: row.Qty, Date: row.Date.Format("2006-01-02"), Username: row.Username,
			Reference: row.Reference, Note: row.Note, LotID: row.LotID, LotNumber: row.LotNumber,
		}
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
		"Part": p, "Txns": txns, "Lots": lots, "Today": h.userNow(r).Format("2006-01-02"),
		"ActiveTab": "parts", "ActiveSubTab": "transactions",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
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
	qty, err := parseDecimal(fv(r, "qty"))
	if err != nil || qty.IsZero() {
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
		now := h.userNow(r)
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
