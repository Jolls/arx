package main

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/internal/purchasing"
	"arx/internal/urlutil"
)

// ── ContactSummary is used for supplier/receiver contact dropdowns ──────────

type ContactSummary struct {
	ID          int
	DisplayName string
	Address     string
	City        string
	State       string
	Zipcode     string
	Country     string
	Phone       string
	Fax         string
	Email       string
}

func (h *Handler) contactsForSupplier(r *http.Request, supplierID int) []ContactSummary {
	if supplierID <= 0 {
		return nil
	}
	contacts, err := h.contacts().ListActiveForCompany(r.Context(), supplierID)
	if err != nil {
		log.Printf("contactsForSupplier: %v", err)
		return nil
	}
	out := make([]ContactSummary, len(contacts))
	for i, c := range contacts {
		out[i] = ContactSummary{ID: c.ID, DisplayName: c.DisplayName, Address: c.Address, City: c.City,
			State: c.State, Zipcode: c.Zipcode, Country: c.Country, Phone: c.Phone1, Fax: c.Fax, Email: c.Email}
	}
	return out
}

// ── SuggestLink is a PO line that has a VendorPN with no supplier_part entry ─

type SuggestLink struct {
	Index      int
	PartID     int
	PartNumber string
	VendorPN   string
}

func (h *Handler) fetchSuggestLinks(r *http.Request, poNum string) []SuggestLink {
	links, err := h.purchasing().ListSuggestedLinks(r.Context(), poNum)
	if err != nil {
		log.Printf("fetchSuggestLinks: %v", err)
		return nil
	}
	out := make([]SuggestLink, len(links))
	for i, l := range links {
		out[i] = SuggestLink{Index: i, PartID: l.PartID, PartNumber: l.PartNumber, VendorPN: l.VendorPartNumber}
	}
	return out
}

// ── SuggestPrice is a PO line whose cost isn't already covered by an active
// price at the same or lower pack size — i.e. it's a genuine new price break,
// not just the same cost repeated at a higher quantity ──────────────────────

type SuggestPrice struct {
	Index      int
	PartID     int
	PartNumber string
	Cost       float64
	PackSize   float64
}

func (h *Handler) fetchSuggestPrices(r *http.Request, poNum string) []SuggestPrice {
	prices, err := h.purchasing().ListSuggestedPrices(r.Context(), poNum)
	if err != nil {
		log.Printf("fetchSuggestPrices: %v", err)
		return nil
	}
	out := make([]SuggestPrice, len(prices))
	for i, p := range prices {
		out[i] = SuggestPrice{Index: i, PartID: p.PartID, PartNumber: p.PartNumber, Cost: p.UnitCost, PackSize: p.Qty}
	}
	return out
}

// ── polRow is a parsed line-item from the edit form ──────────────────────────

type polRow struct {
	Item       string
	PartNumber string
	Rev        string
	Desc       string
	VendorPN   string
	Qty        string
	Cost       string
	PNID       string
}

func (row polRow) isBlank() bool {
	return row.Item == "" && row.PartNumber == "" && row.Rev == "" && row.Desc == "" &&
		row.VendorPN == "" && row.Qty == "" && row.Cost == "" && row.PNID == ""
}

func extractPolRows(form url.Values, prefix string) map[string]polRow {
	rows := map[string]polRow{}
	for key, vals := range form {
		if !strings.HasPrefix(key, prefix+"[") {
			continue
		}
		rest := key[len(prefix)+1:]
		before, after, ok := strings.Cut(rest, "][")
		if !ok {
			continue
		}
		id := before
		field := strings.TrimSuffix(after, "]")
		val := ""
		if len(vals) > 0 {
			val = strings.TrimSpace(vals[0])
		}
		row := rows[id]
		switch field {
		case "POLItem":
			row.Item = val
		case "POLPNPartNumber":
			row.PartNumber = val
		case "POLRev":
			row.Rev = val
		case "POLDesc":
			row.Desc = val
		case "VendorPN":
			row.VendorPN = val
		case "POLQty":
			row.Qty = val
		case "POLCost":
			row.Cost = val
		case "POLPNID":
			row.PNID = val
		}
		rows[id] = row
	}
	return rows
}

func polRowToArgs(row polRow) (item int, qty, cost float64, pnid any) {
	item, _ = strconv.Atoi(row.Item)
	qty, _ = strconv.ParseFloat(row.Qty, 64)
	cost, _ = strconv.ParseFloat(row.Cost, 64)
	if row.PNID != "" {
		if v, err := strconv.Atoi(row.PNID); err == nil && v > 0 {
			pnid = v
		}
	}
	return
}

// polLine converts a form row to the po_line fields POCreate/POUpdate write, with rev as the revision snapshot.
func polLine(row polRow, rev string) purchasing.POLine {
	item, qty, cost, pnid := polRowToArgs(row)
	l := purchasing.POLine{LineNumber: item, PartNumberSnapshot: row.PartNumber, RevisionSnapshot: rev,
		Description: row.Desc, Qty: qty, UnitCost: cost, VendorPartNumber: row.VendorPN}
	if id, ok := pnid.(int); ok {
		l.PartID = &id
	}
	return l
}

// poFromForm reads the PO edit form's header fields; empty or invalid ids,
// amounts and dates read as nil.
func poFromForm(r *http.Request) purchasing.PO {
	return purchasing.PO{Orderer: fv(r, "orderer"), AccountID: fv(r, "account_id"),
		SupplierID: intPtrOrNil(fv(r, "supplier_id")), SupplierName: fv(r, "supplier_name"),
		SupplierContact: fv(r, "supplier_contact"), SupplierEmail: fv(r, "supplier_email"),
		SupplierAddress: fv(r, "supplier_address"), SupplierCity: fv(r, "supplier_city"),
		SupplierState: fv(r, "supplier_state"), SupplierZipcode: fv(r, "supplier_zipcode"),
		SupplierCountry: fv(r, "supplier_country"), SupplierPhoneNumber: fv(r, "supplier_phone_number"),
		SupplierFaxNumber: fv(r, "supplier_fax_number"), ReceiverID: intPtrOrNil(fv(r, "receiver_id")),
		ReceiverName: fv(r, "receiver_name"), ReceiverContact: fv(r, "receiver_contact"), ReceiverEmail: fv(r, "receiver_email"),
		ReceiverAddress: fv(r, "receiver_address"), ReceiverCity: fv(r, "receiver_city"),
		ReceiverState: fv(r, "receiver_state"), ReceiverZipcode: fv(r, "receiver_zipcode"),
		ReceiverCountry: fv(r, "receiver_country"), ReceiverPhone: fv(r, "receiver_phone"), ReceiverFax: fv(r, "receiver_fax"),
		Tax1: nullableFloat(fv(r, "tax1")), ShippingCost: nullableFloat(fv(r, "shipping_cost")),
		MiscCost: nullableFloat(fv(r, "misc_cost")), Notes: fv(r, "notes"), InternalNotes: fv(r, "internal_notes"),
		DateOrdered: parseFormDate(fv(r, "date_ordered")), DateRequested: parseFormDate(fv(r, "date_requested")),
		DateClosed: parseFormDate(fv(r, "date_closed")), DatePrinted: parseFormDate(fv(r, "date_printed")),
		SupplierContactID: intPtrOrNil(fv(r, "supplier_contact_id")), ReceiverContactID: intPtrOrNil(fv(r, "receiver_contact_id")),
	}
}

func rowLineTotal(rows map[string]polRow) float64 {
	var total float64
	for _, row := range rows {
		qty, _ := strconv.ParseFloat(row.Qty, 64)
		cost, _ := strconv.ParseFloat(row.Cost, 64)
		total += qty * cost
	}
	return total
}

func parseFormDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// isoDate formats t as YYYY-MM-DD, "" for nil.
func isoDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}


func parseFormFloat(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return f
}

// ── POList — GET /pos ────────────────────────────────────────────────────────

func (h *Handler) POList(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "pos/pos.html", map[string]any{
		"ActiveTab": "pos", "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) PORows(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	type row struct {
		Num      string  `json:"num"`
		Status   string  `json:"status"`
		SID      *int    `json:"sid"`
		GID      *int    `json:"gid"` // rfq_group_id — set when this row is an RFQ quote
		Supplier string  `json:"supplier"`
		Ordered  string  `json:"ordered"`
		Closed   string  `json:"closed"`
		Orderer  string  `json:"orderer"`
		Cost     float64 `json:"cost"`
	}
	pos, err := h.purchasing().ListPORows(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	out := make([]row, len(pos))
	for i, p := range pos {
		out[i] = row{Num: p.Number, Status: p.Status, SID: &p.SupplierID, GID: p.RFQGroupID, Supplier: p.SupplierName,
			Ordered: isoDate(p.DateOrdered), Closed: isoDate(p.DateClosed), Orderer: p.Orderer, Cost: p.Total}
	}
	log.Printf("[rows] pos: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

// ── PODetail — GET /po/{id} ──────────────────────────────────────────────────

func (h *Handler) PODetail(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	items, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}
	var lineTotal float64
	for _, item := range items {
		lineTotal += item.Qty * item.UnitCost
	}
	h.setNavContext(w, r, fmt.Sprintf("/po/%s", po.Number), "PO #"+po.Number)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	canApprove := false
	if u := h.currentUser(r); u != nil {
		canApprove = u.CanApprovePO
	}
	bulkOrderDelimiter, bulkOrderPNSource := h.fetchSupplierBulkOrderOptions(r, po.SupplierID)
	tplData := map[string]any{
		"PO": po, "POItems": items, "LineTotal": lineTotal,
		"ActiveTab": "pos", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode":           h.cfg().TestMode,
		"StatusActions":      poStatusActions(po.Status),
		"ApprovalLabel":      poApprovalLabels[po.ApprovalStatus],
		"ApprovalActions":    poApprovalActions(po.ApprovalStatus, canApprove),
		"History":            h.fetchPOHistory(r, po.ID),
		"Receipts":           h.fetchPOReceipts(r, po.ID),
		"Today":              h.userNow(r).Format("2006-01-02"),
		"CanSend":            poApprovalAllowsSend(po.ApprovalStatus),
		"CSRFToken":          h.csrfToken(w, r),
		"BulkOrderDelimiter": bulkOrderDelimiter,
		"BulkOrderPNSource":  bulkOrderPNSource,
	}
	if r.URL.Query().Get("suggest_links") == "1" {
		if links := h.fetchSuggestLinks(r, num); len(links) > 0 {
			tplData["SuggestLinks"] = links
			tplData["CSRFToken"] = h.csrfToken(w, r)
		}
		if prices := h.fetchSuggestPrices(r, num); len(prices) > 0 {
			tplData["SuggestPrices"] = prices
			tplData["CSRFToken"] = h.csrfToken(w, r)
		}
	}
	h.render(w, r, "pos/po_detail.html", tplData)
}

// fetchSupplierBulkOrderOptions looks up the PO's supplier's "Copy for Ordering" clipboard
// settings (#80) live by supplier_id — an ordering preference, not a PO-time snapshot like the
// address/contact fields already on purchase_order. Falls back to the column defaults if the
// supplier is missing or unset.
func (h *Handler) fetchSupplierBulkOrderOptions(r *http.Request, supplierID *int) (delimiter, pnSource string) {
	delimiter, pnSource = "comma", "internal"
	if supplierID == nil {
		return
	}
	d, src, err := h.purchasing().GetSupplierBulkOrder(r.Context(), *supplierID)
	if err != nil {
		return
	}
	if d != "" {
		delimiter = d
	}
	if src != "" {
		pnSource = src
	}
	return
}

// ── PONew — GET /pos/new ─────────────────────────────────────────────────────

// applyPODefaults populates a new PO/RFQ with the logged-in user's default
// receiver/contact (Profile → PO defaults, issue #463), returning the contact
// dropdown lists for the edit form. When the user has no default contact, the
// receiver company's own default_contact is used as a fallback.
func (h *Handler) applyPODefaults(r *http.Request, po *models.PurchaseOrder) (supplierContacts, receiverContacts []ContactSummary, noDefaultReceiver bool) {
	var receiverID, contactID int
	if u := h.currentUser(r); u != nil {
		receiverID = u.DefaultPOReceiverID
		contactID = u.DefaultPOContactID
		noDefaultReceiver = receiverID <= 0
	}
	if rid := receiverID; rid > 0 {
		// A missing receiver company leaves the name and default contact blank.
		receiverName, receiverDefaultContact, _ := h.purchasing().GetSupplierContactDefault(r.Context(), rid)
		po.ReceiverName = receiverName
		v := rid
		po.ReceiverID = &v
		receiverContacts = h.contactsForSupplier(r, rid)

		// Pick the receiver contact: the resolved PO default contact wins;
		// otherwise fall back to the receiver company's own default_contact.
		wantContact := contactID
		if wantContact <= 0 && receiverDefaultContact != nil {
			wantContact = *receiverDefaultContact
		}
		if wantContact > 0 {
			for _, c := range receiverContacts {
				if c.ID == wantContact {
					po.ReceiverContact = c.DisplayName
					po.ReceiverEmail = c.Email
					po.ReceiverAddress = c.Address
					po.ReceiverCity = c.City
					po.ReceiverState = c.State
					po.ReceiverZipcode = c.Zipcode
					po.ReceiverCountry = c.Country
					po.ReceiverPhone = c.Phone
					po.ReceiverFax = c.Fax
					break
				}
			}
		}
	}
	return supplierContacts, receiverContacts, noDefaultReceiver
}

func (h *Handler) PONew(w http.ResponseWriter, r *http.Request) {
	now := h.userNow(r)
	po := models.PurchaseOrder{Status: "draft", IsActive: true, DateOrdered: &now, DateRequested: &now}
	if u := h.currentUser(r); u != nil {
		po.Orderer = u.DisplayName
	}
	supplierContacts, receiverContacts, noDefaultReceiver := h.applyPODefaults(r, &po)
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": po, "POItems": nil, "IsNew": true,
		"SupplierContacts": supplierContacts, "ReceiverContacts": receiverContacts,
		"NoDefaultReceiver": noDefaultReceiver,
		"ActiveTab": "pos", "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// ── POCreate — POST /pos ─────────────────────────────────────────────────────

func (h *Handler) POCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}

	// RFQs (#270) reuse one sequence number per group: the first quote takes a
	// number and stores it as "<base>R1"; additional supplier quotes reuse <base>
	// with the next R suffix; the winner is renamed to bare <base> on convert.
	isRFQ := fv(r, "rfq") == "1"
	rfqGroup := ""
	if isRFQ {
		rfqGroup = fv(r, "rfq_group_id")
	}

	var newNumber string
	if isRFQ && rfqGroup != "" {
		// Additional supplier quote: reuse the group's base number, next R suffix.
		groupID, err := strconv.Atoi(rfqGroup)
		if err != nil {
			h.renderError(w, r, "Error loading RFQ group: "+err.Error())
			return
		}
		anchorNum, err := h.purchasing().GetPONumber(r.Context(), groupID)
		if err != nil {
			h.renderError(w, r, "Error loading RFQ group: "+err.Error())
			return
		}
		count, err := h.purchasing().CountRFQQuotes(r.Context(), groupID)
		if err != nil {
			h.renderError(w, r, "Error counting RFQ quotes: "+err.Error())
			return
		}
		newNumber = fmt.Sprintf("%sR%d", rfqBaseNumber(anchorNum), count+1)
	} else {
		// New PO or first RFQ quote: take one sequence number. The sequence is
		// outside the transaction — sequences never roll back in Postgres, which
		// is correct: a rolled-back PO should not reuse its number.
		base, err := h.purchasing().NextPONumber(r.Context())
		if err != nil {
			h.renderError(w, r, "Error getting PO number: "+err.Error())
			return
		}
		newNumber = base
		if isRFQ {
			newNumber = base + "R1"
		}
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

	// New POs start as 'draft'; RFQs (#270) start as 'rfq'. Later status changes go
	// through POStatusTransition.
	newStatus := "draft"
	if isRFQ {
		newStatus = "rfq"
	}
	pur := purchasing.New(tx)
	po := poFromForm(r)
	po.Number, po.Status, po.IsActive = newNumber, newStatus, statusIsActive(newStatus)
	newID, err := pur.CreatePO(r.Context(), po)
	if err != nil {
		h.renderError(w, r, "Error creating PO: "+err.Error())
		return
	}

	// Record the creation as the first history entry (status event, from_status NULL).
	if err := pur.CreatePOStatusEvent(r.Context(), newID, nil, newStatus, h.actorName(r)); err != nil {
		h.renderError(w, r, "Error recording PO status: "+err.Error())
		return
	}

	// RFQ grouping (#270): join an existing group when adding another supplier's quote,
	// otherwise anchor a new group to this RFQ's own id.
	if isRFQ {
		groupID := newID
		if g := intPtrOrNil(fv(r, "rfq_group_id")); g != nil {
			groupID = *g
		}
		if err := pur.SetRFQGroup(r.Context(), newID, groupID); err != nil {
			h.renderError(w, r, "Error setting RFQ group: "+err.Error())
			return
		}
	}

	newRows := extractPolRows(r.Form, "new_pol")
	var lineTotal float64
	for _, row := range newRows {
		if row.isBlank() {
			continue
		}
		l := polLine(row, h.resolvePolRev(r, row.Rev, row.PNID))
		if err := pur.CreatePOLine(r.Context(), newID, l); err != nil {
			h.renderError(w, r, "Error adding PO line: "+err.Error())
			return
		}
		lineTotal += l.Qty * l.UnitCost
	}

	tax, _ := strconv.ParseFloat(fv(r, "tax1"), 64)
	ship, _ := strconv.ParseFloat(fv(r, "shipping_cost"), 64)
	misc, _ := strconv.ParseFloat(fv(r, "misc_cost"), 64)
	totalCost := lineTotal + tax + ship + misc
	if err := pur.SetPOTotal(r.Context(), newID, totalCost); err != nil {
		h.renderError(w, r, "Error updating PO total: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving PO: "+err.Error())
		return
	}
	committed = true

	// RFQs don't get a folder yet — the winning quote's folder is created on convert,
	// keyed to the bare base number (the folder lookup is a prefix match, so an
	// "1050R1" folder would wrongly match a converted PO "1050").
	if !isRFQ {
		h.createPOFolder(r, newNumber, fv(r, "supplier_id"))
	}
	http.Redirect(w, r, "/po/"+newNumber+"?suggest_links=1", http.StatusFound)
}

// ── POEdit — GET /po/{id}/edit ───────────────────────────────────────────────

func (h *Handler) POEdit(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	items, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}
	supID := 0
	if po.SupplierID != nil {
		supID = *po.SupplierID
	}
	recID := 0
	if po.ReceiverID != nil {
		recID = *po.ReceiverID
	}
	h.setNavContext(w, r, fmt.Sprintf("/po/%s", po.Number), "PO #"+po.Number)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": po, "POItems": items, "IsNew": false,
		"SupplierContacts": h.contactsForSupplier(r, supID),
		"ReceiverContacts": h.contactsForSupplier(r, recID),
		"ActiveTab":        "pos", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg().TestMode, "CSRFToken": h.csrfToken(w, r),
	})
}

// ── POUpdate — POST /po/{id} ─────────────────────────────────────────────────

func (h *Handler) POUpdate(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
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

	// Editing a PO that was already approved (or awaiting approval) invalidates
	// that decision (#267) — capture the current state so we can reset it below.
	pur := purchasing.New(tx)
	state, err := pur.LockPOState(r.Context(), num) // #261: lock so the approval read can't go stale
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	poID, priorApproval := state.ID, state.ApprovalStatus

	// Delete flagged line items
	for _, idStr := range r.Form["delete_pol[]"] {
		id, err := strconv.Atoi(idStr)
		if err == nil {
			err = pur.DeletePOLine(r.Context(), poID, id)
		}
		if err != nil {
			h.renderError(w, r, "Error deleting PO line: "+err.Error())
			return
		}
	}

	// Update existing line items
	existRows := extractPolRows(r.Form, "pol")
	deleteSet := map[string]bool{}
	for _, idStr := range r.Form["delete_pol[]"] {
		deleteSet[idStr] = true
	}
	for polID, row := range existRows {
		if deleteSet[polID] {
			continue
		}
		l := polLine(row, h.resolvePolRev(r, row.Rev, row.PNID))
		id, err := strconv.Atoi(polID)
		if err == nil {
			l.ID = id
			err = pur.UpdatePOLine(r.Context(), poID, l)
		}
		if err != nil {
			h.renderError(w, r, "Error updating PO line: "+err.Error())
			return
		}
	}

	// Insert new line items (poID resolved above)
	newRows := extractPolRows(r.Form, "new_pol")
	if len(newRows) > 0 {
		for _, row := range newRows {
			if row.isBlank() {
				continue
			}
			if err := pur.CreatePOLine(r.Context(), poID, polLine(row, h.resolvePolRev(r, row.Rev, row.PNID))); err != nil {
				h.renderError(w, r, "Error adding PO line: "+err.Error())
				return
			}
		}
	}

	// Recalculate total from all remaining lines (reads within the transaction,
	// so it sees the deletes/inserts above before any other writer can interfere)
	lineSum, err := pur.SumPOLines(r.Context(), poID)
	if err != nil {
		h.renderError(w, r, "Error recalculating PO total: "+err.Error())
		return
	}

	tax, _ := strconv.ParseFloat(fv(r, "tax1"), 64)
	ship, _ := strconv.ParseFloat(fv(r, "shipping_cost"), 64)
	misc, _ := strconv.ParseFloat(fv(r, "misc_cost"), 64)
	totalCost := lineSum + tax + ship + misc

	// status and is_active are intentionally NOT updated here — they change only
	// via POStatusTransition (POST /po/{id}/status), which records the transition.
	if err := pur.UpdatePOHeader(r.Context(), num, totalCost, poFromForm(r)); err != nil {
		h.renderError(w, r, "Error saving PO: "+err.Error())
		return
	}

	// Reset approval if this edit invalidated a prior decision (#267).
	if priorApproval == "approved" || priorApproval == "pending" {
		if err := h.resetApproval(r, tx, poID, "PO edited after "+priorApproval); err != nil {
			h.renderError(w, r, "Error resetting approval: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving PO: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, "/po/"+num+"?suggest_links=1", http.StatusFound)
}

// ── POAddSuggestions — POST /po/{id}/add-suggestions ──────────────────────────

func (h *Handler) POAddSuggestions(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	supplierID := r.FormValue("supplier_id")

	linksCount, _ := strconv.Atoi(r.FormValue("links_count"))
	for i := range linksCount {
		if r.FormValue(fmt.Sprintf("add_%d", i)) != "1" {
			continue
		}
		partID := r.FormValue(fmt.Sprintf("part_id_%d", i))
		supplierPN := r.FormValue(fmt.Sprintf("supplier_pn_%d", i))
		if partID == "" || supplierPN == "" || supplierID == "" {
			continue
		}
		pid, err := strconv.Atoi(partID)
		sid, err2 := strconv.Atoi(supplierID)
		if err = errors.Join(err, err2); err == nil {
			err = h.parts().LinkSupplierPN(r.Context(), pid, sid, supplierPN)
		}
		if err != nil {
			h.renderError(w, r, "Error adding supplier link: "+err.Error())
			return
		}
	}

	today := h.userNow(r).Format("2006-01-02")
	pricesCount, _ := strconv.Atoi(r.FormValue("prices_count"))
	for i := range pricesCount {
		if r.FormValue(fmt.Sprintf("add_price_%d", i)) != "1" {
			continue
		}
		partID := r.FormValue(fmt.Sprintf("price_part_id_%d", i))
		cost := r.FormValue(fmt.Sprintf("price_cost_%d", i))
		packSize := r.FormValue(fmt.Sprintf("price_pack_size_%d", i))
		if partID == "" || cost == "" || supplierID == "" {
			continue
		}
		if packSize == "" {
			packSize = "1"
		}
		pid, err1 := strconv.Atoi(partID)
		sid, err2 := strconv.Atoi(supplierID)
		packSizeF, err3 := strconv.ParseFloat(packSize, 64)
		costF, err4 := strconv.ParseFloat(cost, 64)
		// Deactivate any existing active price at this pack size for this part+supplier.
		err := errors.Join(err1, err2, err3, err4)
		if err == nil {
			err = h.parts().DeactivatePrices(r.Context(), pid, sid, packSizeF)
		}
		if err != nil {
			h.renderError(w, r, "Error updating price: "+err.Error())
			return
		}
		packPrice := costF * packSizeF
		if err := h.parts().CreatePrice(r.Context(), pid, sid, &packSizeF, &costF, &packPrice, today); err != nil {
			h.renderError(w, r, "Error inserting price: "+err.Error())
			return
		}
		h.ensureDefaultSupplier(r.Context(), pid, sid)
	}

	http.Redirect(w, r, "/po/"+num, http.StatusFound)
}

// ── PODuplicate — GET /po/{id}/duplicate ─────────────────────────────────────

func (h *Handler) PODuplicate(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	source, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	sourceItems, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}

	// Clear fields that shouldn't carry over
	source.Number = ""
	source.DateOrdered = nil
	source.DateRequested = nil
	source.DateClosed = nil
	source.TotalCost = nil
	source.Status = "draft"
	source.IsActive = true

	supID := 0
	if source.SupplierID != nil {
		supID = *source.SupplierID
	}
	recID := 0
	if source.ReceiverID != nil {
		recID = *source.ReceiverID
	}
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": source, "POItems": nil, "DuplicateItems": sourceItems,
		"IsNew": true, "IsDuplicate": true, "DuplicateFrom": num,
		"SupplierContacts": h.contactsForSupplier(r, supID),
		"ReceiverContacts": h.contactsForSupplier(r, recID),
		"ActiveTab":        "pos", "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// ── POStartRFQ — GET /po/{id}/start-rfq ──────────────────────────────────────

// Starts a new RFQ from a draft PO: clones its line items and supplier pricing
// into a brand-new RFQ as the first quote. The source PO is left untouched —
// no renumbering, no status change.
func (h *Handler) POStartRFQ(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	source, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	if source.Status != "draft" {
		h.renderError(w, r, "Only a draft PO can be used to start an RFQ.")
		return
	}
	sourceItems, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}

	source.Number = ""
	source.DateOrdered = nil
	source.DateRequested = nil
	source.DateClosed = nil
	source.TotalCost = nil
	source.Status = "rfq"
	source.IsActive = true

	supID := 0
	if source.SupplierID != nil {
		supID = *source.SupplierID
	}
	recID := 0
	if source.ReceiverID != nil {
		recID = *source.ReceiverID
	}
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": source, "POItems": nil, "DuplicateItems": sourceItems,
		"IsNew": true, "IsRFQ": true, "StartRFQFrom": num,
		"SupplierContacts": h.contactsForSupplier(r, supID),
		"ReceiverContacts": h.contactsForSupplier(r, recID),
		"ActiveTab":        "pos", "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// ── PONote — GET /po/{id}/note ───────────────────────────────────────────────

func (h *Handler) PONote(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/po/%s", po.Number), "PO #"+po.Number)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "pos/po_note.html", map[string]any{
		"PO": po, "ActiveTab": "pos", "ActiveSubTab": "note",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// ── POPrint — GET /po/{id}/print ─────────────────────────────────────────────

func (h *Handler) POPrint(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	// Approval gate (#267): a PO cannot be printed until it has been approved.
	// RFQs (#270) print without approval — they are quote requests, not commitments.
	if po.Status != "rfq" && !poApprovalAllowsSend(po.ApprovalStatus) {
		h.renderError(w, r, "This PO must be approved before it can be printed.")
		return
	}

	var supplierCode string
	if po.SupplierID != nil {
		supplierCode, _ = h.purchasing().GetSupplierCode(r.Context(), *po.SupplierID) // missing supplier → no code
	}

	items, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}
	var lineTotal float64
	for _, item := range items {
		lineTotal += item.Qty * item.UnitCost
	}

	var folderPath string
	if root := h.cfg().POFolderRoot; root != "" {
		if base := findPOBaseFolder(root, num); base != "" {
			folderPath = filepath.Join(root, base)
		}
	}

	h.renderPrint(w, "pos/po_print.html", map[string]any{
		"PO": po, "POItems": items, "LineTotal": lineTotal,
		"SupplierCode": supplierCode, "TestMode": h.cfg().TestMode,
		"POFolderPath": folderPath, "IsRFQ": po.Status == "rfq",
		"CompanyLogo": h.companyLogoURL(),
	})
}

// ── POMarkPrinted — POST /po/{id}/mark-printed ───────────────────────────────

func (h *Handler) POMarkPrinted(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	// Approval gate (#267): only set date_printed for approved POs. RFQs (#270)
	// print without approval.
	st, err := h.purchasing().GetPOState(r.Context(), num)
	if err != nil || (st.Status != "rfq" && !poApprovalAllowsSend(st.ApprovalStatus)) {
		http.Error(w, "PO is not approved", http.StatusForbidden)
		return
	}
	if err := h.purchasing().MarkPOPrinted(r.Context(), num, h.userNow(r).Format("2006-01-02")); err != nil {
		serverError(w, "database error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POOpenFolder — POST /po/{id}/open-folder ─────────────────────────────────

func (h *Handler) POOpenFolder(w http.ResponseWriter, r *http.Request) {
	root := h.cfg().POFolderRoot
	if root == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	num := chi.URLParam(r, "id")
	if err := validateFolderStub(num); err != nil {
		http.Error(w, "invalid PO number", http.StatusBadRequest)
		return
	}
	supplierID, err := h.purchasing().GetPOSupplierID(r.Context(), num)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		serverError(w, "database error", err)
		return
	}
	h.createPOFolder(r, num, strconv.Itoa(supplierID))
	if folder := findPOBaseFolder(root, num); folder != "" {
		exec.Command("explorer.exe", filepath.Join(root, folder)).Start() //nolint:errcheck
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POFolder / POFolderSub / POFile ─────────────────────────────────────────

// findPOBaseFolder finds the first directory in root that starts with poNumber.
func findPOBaseFolder(root, poNumber string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), poNumber) {
			return e.Name()
		}
	}
	return ""
}

func (h *Handler) renderPOFolder(w http.ResponseWriter, r *http.Request, po models.PurchaseOrder, subParts []string) {
	root := h.cfg().POFolderRoot
	if root == "" {
		http.Error(w, "PO_FOLDER_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	baseName := findPOBaseFolder(root, po.Number)
	if baseName == "" {
		http.Error(w, "No folder found for PO "+po.Number, http.StatusNotFound)
		return
	}

	base := filepath.Join(root, baseName)
	path, ok := safePath(base, strings.Join(subParts, "/"))
	if !ok {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		http.NotFound(w, r)
		return
	}

	dirName := po.Number
	if len(subParts) > 0 {
		dirName = subParts[len(subParts)-1]
	}

	folderURL := fmt.Sprintf("/po/%s/folder", po.Number)
	parentURL := dirParentURL(folderURL, folderURL, subParts)

	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.renderDirListing(w, r, dirListingParams{
		Path: path, RelParts: subParts,
		DirURLPrefix:  folderURL,
		FileURLPrefix: fmt.Sprintf("/po/%s/file", po.Number),
		DirName:       dirName,
		ParentURL:     parentURL,
		PO:            &po,
		ActiveTab:     "pos", ActiveSubTab: "folder",
		NavBackURL: backURL, NavBackLabel: backLabel,
		UploadURLPrefix: fmt.Sprintf("/po/%s/folder-upload", po.Number),
	})
}

// POFolderUpload — POST /po/{id}/folder-upload
func (h *Handler) POFolderUpload(w http.ResponseWriter, r *http.Request) {
	h.poFolderUpload(w, r, nil)
}

// POFolderUploadSub — POST /po/{id}/folder-upload/*
func (h *Handler) POFolderUploadSub(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/po/%s/folder-upload/", num))
	var subParts []string
	for seg := range strings.SplitSeq(splat, "/") {
		base := filepath.Base(seg)
		if base != "" && base != "." && base != ".." {
			subParts = append(subParts, base)
		}
	}
	h.poFolderUpload(w, r, subParts)
}

func (h *Handler) poFolderUpload(w http.ResponseWriter, r *http.Request, subParts []string) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	root := h.cfg().POFolderRoot
	if root == "" {
		http.Error(w, "PO_FOLDER_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	baseName := findPOBaseFolder(root, po.Number)
	if baseName == "" {
		http.Error(w, "No folder found for PO "+po.Number+". Open the PO folder first to create it.", http.StatusNotFound)
		return
	}
	base := filepath.Join(root, baseName)
	dir, ok := resolveUploadDir(base, strings.Join(subParts, "/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	redirectURL := fmt.Sprintf("/po/%s/folder", po.Number)
	if len(subParts) > 0 {
		redirectURL += "/" + strings.Join(subParts, "/")
	}
	h.handleDirUpload(w, r, dir, redirectURL)
}

func (h *Handler) POFolder(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/po/%s", po.Number), "PO #"+po.Number)
	h.renderPOFolder(w, r, po, nil)
}

func (h *Handler) POFolderSub(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/po/%s", po.Number), "PO #"+po.Number)
	splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/po/%s/folder/", num))
	var subParts []string
	for seg := range strings.SplitSeq(splat, "/") {
		base := filepath.Base(seg)
		if base != "" && base != "." && base != ".." {
			subParts = append(subParts, base)
		}
	}
	h.renderPOFolder(w, r, po, subParts)
}

func (h *Handler) POFile(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	root := h.cfg().POFolderRoot
	if root == "" {
		http.Error(w, "PO_FOLDER_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	baseName := findPOBaseFolder(root, num)
	if baseName == "" {
		http.NotFound(w, r)
		return
	}
	splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/po/%s/file/", num))
	h.serveLocalizedFile(w, r, fileServingParams{Root: filepath.Join(root, baseName), Splat: splat})
}

// ── PO status lifecycle (issue #271) ─────────────────────────────────────────
// Codes are stored in PO.status; labels are shown in the UI.

var poStatusLabels = map[string]string{
	"rfq":                "RFQ",
	"draft":              "Draft",
	"open":               "Open",
	"sent":               "Sent",
	"partially_received": "Partially Received",
	"closed":             "Closed",
	"cancelled":          "Cancelled",
}

// poTransitions maps each status to the statuses it may move to.
var poTransitions = map[string][]string{
	"rfq":                {"cancelled"}, // decline (#270); awarding is a duplicate-to-PO via RFQConvert
	"draft":              {"open", "cancelled"},
	"open":               {"sent", "cancelled"},
	"sent":               {"partially_received", "closed", "cancelled"},
	"partially_received": {"closed", "cancelled"},
	"closed":             {"open"},  // reopen
	"cancelled":          {"draft"}, // reopen
}

func poCanTransition(from, to string) bool {
	return slices.Contains(poTransitions[from], to)
}

// StatusAction is a transition button rendered on the PO detail page.
type StatusAction struct {
	Target  string
	Label   string
	Class   string // Bootstrap button variant
	Confirm bool   // ask for confirmation before submitting
}

func poStatusActions(current string) []StatusAction {
	var out []StatusAction
	for _, to := range poTransitions[current] {
		out = append(out, StatusAction{
			Target:  to,
			Label:   poActionLabel(current, to),
			Class:   poActionClass(to),
			Confirm: to == "closed" || to == "cancelled",
		})
	}
	return out
}

func poActionLabel(from, to string) string {
	switch {
	case from == "rfq" && to == "draft":
		return "Convert to PO"
	case from == "rfq" && to == "cancelled":
		return "Decline RFQ"
	case to == "open" && from == "closed":
		return "Reopen"
	case to == "draft" && from == "cancelled":
		return "Reopen as Draft"
	case to == "cancelled":
		return "Cancel PO"
	case to == "closed":
		return "Close PO"
	default:
		return "Mark " + poStatusLabels[to]
	}
}

func poActionClass(to string) string {
	switch to {
	case "cancelled":
		return "btn-outline-danger"
	case "closed":
		return "btn-secondary"
	default:
		return "btn-primary"
	}
}

// statusIsActive returns true for statuses that represent an open/in-progress PO.
// is_active is kept in sync with this value; status is authoritative.
func statusIsActive(status string) bool {
	switch status {
	case "rfq", "draft", "open", "sent", "partially_received":
		return true
	}
	return false
}

// actorName returns the logged-in user's username for audit fields, matching the
// other audit/event tables (record_events.username, form_row_history.changed_by).
// Falls back to "system" when there is no user.
func (h *Handler) actorName(r *http.Request) string {
	if u := h.currentUser(r); u != nil && u.Username != "" {
		return u.Username
	}
	return "system"
}

// POHistoryEvent is one row of the unified PO activity log (status + approval).
type POHistoryEvent struct {
	EventType  string // "status" | "approval"
	FromStatus string // status events: prior status code for the badge ("" on creation)
	ToStatus   string // status events: new status code for the badge
	Action     string // approval events: action code for the badge (submitted|approved|rejected|reset)
	Note       string // approval events: optional note
	ChangedBy  string
	ChangedAt  time.Time
}

// fetchPOHistory returns the combined status + approval timeline for a PO, newest first.
func (h *Handler) fetchPOHistory(r *http.Request, poID int) []POHistoryEvent {
	events, err := h.purchasing().ListPOHistory(r.Context(), poID)
	if err != nil {
		log.Printf("fetchPOHistory: %v", err)
		return nil
	}
	out := make([]POHistoryEvent, len(events))
	for i, e := range events {
		out[i] = POHistoryEvent(e)
	}
	return out
}

// ── POStatusTransition — POST /po/{id}/status ────────────────────────────────

func (h *Handler) POStatusTransition(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	target := fv(r, "target")
	if poStatusLabels[target] == "" {
		h.renderError(w, r, "Unknown status: "+target)
		return
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

	// #261: lock the PO row and check its state inside the tx.
	st, err := purchasing.New(tx).LockPOState(r.Context(), num)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	poID, current, approval := st.ID, st.Status, st.ApprovalStatus
	if !poCanTransition(current, target) {
		h.renderError(w, r, fmt.Sprintf("Cannot change status from %q to %q.", current, target))
		return
	}
	// Approval gate (#267): a PO cannot be sent until it has been approved.
	if target == "sent" && approval != "approved" {
		h.renderError(w, r, "This PO must be approved before it can be marked Sent.")
		return
	}

	if err := h.recordPOStatusChange(r, tx, poID, current, target); err != nil {
		h.renderError(w, r, "Error updating PO status: "+err.Error())
		return
	}

	// Cancelling a PO clears any approval (#267): a cancelled PO is not approved,
	// and a later reopen must go through approval again.
	if target == "cancelled" && approval != "not_submitted" {
		if err := h.resetApproval(r, tx, poID, "PO cancelled"); err != nil {
			h.renderError(w, r, "Error clearing approval: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving status: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, "/po/"+num, http.StatusFound)
}

// recordPOStatusChange writes the PO_history status event and updates the PO's
// status/is_active/date_closed inside the caller's tx. Shared by the manual status
// transition handler (#271) and PO receiving (#269) so both audit identically.
// The caller is responsible for validating the transition (poCanTransition) first.
func (h *Handler) recordPOStatusChange(r *http.Request, tx *txLogger, poID int, from, to string) error {
	pur := purchasing.New(tx)
	if err := pur.CreatePOStatusEvent(r.Context(), poID, &from, to, h.actorName(r)); err != nil {
		return err
	}
	// date_closed mirrors the closed state: set it when closing (if unset),
	// clear it when reopening from closed.
	return pur.SetPOStatus(r.Context(), poID, from, to, statusIsActive(to), h.userNow(r))
}

// ── PO receiving / goods receipt (issue #269) ────────────────────────────────

// derivePOReceiptStatus returns the status a sent / partially-received PO should
// hold given its current lines: "closed" when every line is fully received
// (received >= ordered), "partially_received" when any qty has been received, or
// "" (no change) when nothing has been received.
func derivePOReceiptStatus(items []models.PurchaseOrderLine) string {
	if len(items) == 0 {
		return ""
	}
	anyReceived, allFull := false, true
	for _, it := range items {
		if it.ReceivedQty > 0 {
			anyReceived = true
		}
		if it.ReceivedQty < it.Qty {
			allFull = false
		}
	}
	switch {
	case allFull:
		return "closed"
	case anyReceived:
		return "partially_received"
	default:
		return ""
	}
}

// parseReceiveDeltas reads the per-line "receive now" quantities from a submitted
// receive form. Each line is looked up via get("recv[<ID>]"); blank entries are
// skipped and non-positive values are ignored. A value that is present but not a
// number is a hard error. The returned map holds only the positive deltas keyed by
// po_line id; an empty map means nothing was entered to receive.
func parseReceiveDeltas(items []models.PurchaseOrderLine, get func(string) string) (map[int]float64, error) {
	deltas := map[int]float64{}
	for _, it := range items {
		raw := strings.TrimSpace(get(fmt.Sprintf("recv[%d]", it.ID)))
		if raw == "" {
			continue
		}
		d, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid quantity %q for line %d", raw, it.ID)
		}
		if d > 0 {
			deltas[it.ID] = d
		}
	}
	return deltas, nil
}

// POReceive — POST /po/{id}/receive. Records a (partial or full) goods receipt:
// per line, the posted "receive now" delta bumps po_line.received_qty/date_received
// and — when the line maps to a catalog part — posts a 'receipt' row to the
// inventory ledger (raising stock_on_hand). The PO status is then re-derived
// (partially_received / closed) through the audited status path.
func (h *Handler) POReceive(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}

	st, err := h.purchasing().GetPOState(r.Context(), num)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	poID, status := st.ID, st.Status
	if status != "sent" && status != "partially_received" {
		h.renderError(w, r, "Only a sent or partially-received PO can receive goods.")
		return
	}

	items, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}
	txnDate := parseFormDate(fv(r, "txn_date"))
	if txnDate == nil {
		now := h.userNow(r)
		txnDate = &now
	}

	deltas, err := parseReceiveDeltas(items, func(k string) string { return fv(r, k) })
	if err != nil {
		h.renderError(w, r, "Invalid quantity for a line item.")
		return
	}
	if len(deltas) == 0 {
		h.renderError(w, r, "Enter a quantity to receive on at least one line.")
		return
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

	// #191: lock the PO row and re-check its status inside the tx.
	pur := purchasing.New(tx)
	if status, err = pur.LockPOStatus(r.Context(), poID); err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	if status != "sent" && status != "partially_received" {
		h.renderError(w, r, "Only a sent or partially-received PO can receive goods.")
		return
	}

	for i := range items {
		d, ok := deltas[items[i].ID]
		if !ok {
			continue
		}
		// Catalog-mapped lines move stock via the ledger; others just record receipt.
		if items[i].PartID != nil {
			polID := items[i].ID
			// Lot-tracked part (#676): create this receipt's lot first, so the ledger
			// row can reference it. lot_number auto-defaults to the lot's own id
			// (#687); lot_description records the PO as provenance. vendor_lot_number
			// is captured per line.
			var lotID *int
			if items[i].IsLotTracked {
				vendorLot := fv(r, fmt.Sprintf("vlot[%d]", polID))
				id, err := h.createLot(r.Context(), tx, *items[i].PartID,
					lotCreateArgs{VendorLot: vendorLot, Description: "PO " + num}, &polID)
				if err != nil {
					h.renderError(w, r, "Error creating lot: "+err.Error())
					return
				}
				lotID = &id
			}
			if err := h.recordInventoryTxn(r, tx, *items[i].PartID, "receipt", d, *txnDate, num, "", &polID, lotID, nil); err != nil {
				h.renderError(w, r, "Error recording receipt: "+err.Error())
				return
			}
		}
		if err := pur.ReceivePOLine(r.Context(), items[i].ID, d, *txnDate); err != nil {
			h.renderError(w, r, "Error updating line item: "+err.Error())
			return
		}
	}

	// Re-derive the PO status from the committed-plus-this-tx line receipts (#191:
	// not the pre-tx copy, which can miss a concurrent receipt).
	lines, err := pur.ListPOLineQtys(r.Context(), poID)
	if err != nil {
		h.renderError(w, r, "Error reloading PO lines: "+err.Error())
		return
	}
	current := make([]models.PurchaseOrderLine, len(lines))
	for i, l := range lines {
		current[i] = models.PurchaseOrderLine{Qty: l.Qty, ReceivedQty: l.ReceivedQty}
	}
	if target := derivePOReceiptStatus(current); target != "" && target != status &&
		poCanTransition(status, target) {
		if err := h.recordPOStatusChange(r, tx, poID, status, target); err != nil {
			h.renderError(w, r, "Error updating PO status: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving receipt: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, "/po/"+num, http.StatusFound)
}

// ── PO approval workflow (issue #267) ────────────────────────────────────────

var poApprovalLabels = map[string]string{
	"not_submitted": "Not Submitted",
	"pending":       "Pending Approval",
	"approved":      "Approved",
	"rejected":      "Rejected",
}

// poApprovalNext returns the approval status that results from applying action to
// current, and whether that action is allowed from the current state.
func poApprovalNext(action, current string) (string, bool) {
	switch action {
	case "submit":
		if current == "not_submitted" || current == "rejected" {
			return "pending", true
		}
	case "approve":
		if current == "pending" {
			return "approved", true
		}
	case "reject":
		if current == "pending" {
			return "rejected", true
		}
	}
	return "", false
}

// poApprovalAllowsSend reports whether a PO in the given approval status may be
// sent or printed.
func poApprovalAllowsSend(approval string) bool { return approval == "approved" }

// resetApproval clears a PO's approval back to not_submitted and logs a 'reset'
// approval event with the given note. Used when an edit or a cancellation
// invalidates a prior approval decision (#267). Runs inside the caller's tx.
func (h *Handler) resetApproval(r *http.Request, tx *txLogger, poID int, note string) error {
	pur := purchasing.New(tx)
	if err := pur.SetPOApproval(r.Context(), poID, "not_submitted"); err != nil {
		return err
	}
	return pur.CreatePOApprovalEvent(r.Context(), poID, "reset", &note, h.actorName(r))
}

// ApprovalAction is an approval button rendered on the PO detail page.
type ApprovalAction struct {
	Action   string
	Label    string
	Class    string // Bootstrap button variant
	NeedNote bool   // show a reason/comment field
}

// poApprovalActions returns the approval buttons available for the current state.
// Approve/Reject are only offered to designated approvers (can_approve_po).
func poApprovalActions(current string, canApprove bool) []ApprovalAction {
	var out []ApprovalAction
	if current == "not_submitted" || current == "rejected" {
		out = append(out, ApprovalAction{Action: "submit", Label: "Submit for Approval", Class: "btn-primary"})
	}
	if current == "pending" && canApprove {
		out = append(out, ApprovalAction{Action: "approve", Label: "Approve", Class: "btn-success"})
		out = append(out, ApprovalAction{Action: "reject", Label: "Reject", Class: "btn-outline-danger", NeedNote: true})
	}
	return out
}

// ── POApprovalAction — POST /po/{id}/approval ────────────────────────────────

func (h *Handler) POApprovalAction(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	action := fv(r, "action")
	if action != "submit" && action != "approve" && action != "reject" {
		h.renderError(w, r, "Unknown approval action: "+action)
		return
	}
	// Approve/reject require an approver; submit is open to any signed-in user.
	if action == "approve" || action == "reject" {
		if u := h.currentUser(r); u == nil || !u.CanApprovePO {
			h.renderError(w, r, "You are not authorized to approve or reject purchase orders.")
			return
		}
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

	// #261: lock the PO row and check its state inside the tx.
	pur := purchasing.New(tx)
	st, err := pur.LockPOState(r.Context(), num)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	poID := st.ID

	next, ok := poApprovalNext(action, st.ApprovalStatus)
	if !ok {
		h.renderError(w, r, fmt.Sprintf("Cannot %s a PO whose approval status is %q.", action, st.ApprovalStatus))
		return
	}

	// Map the action verb to the logged past-tense form.
	logged := map[string]string{"submit": "submitted", "approve": "approved", "reject": "rejected"}[action]
	note := fv(r, "note")

	var notePtr *string // blank note → NULL
	if note != "" {
		notePtr = &note
	}
	if err := pur.CreatePOApprovalEvent(r.Context(), poID, logged, notePtr, h.actorName(r)); err != nil {
		h.renderError(w, r, "Error recording approval: "+err.Error())
		return
	}
	if err := pur.SetPOApproval(r.Context(), poID, next); err != nil {
		h.renderError(w, r, "Error updating approval status: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving approval: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, "/po/"+num, http.StatusFound)
}

// ── shared helpers ───────────────────────────────────────────────────────────

func (h *Handler) fetchPO(w http.ResponseWriter, r *http.Request, num string) (models.PurchaseOrder, bool) {
	po, err := h.purchasing().GetPO(r.Context(), num)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return models.PurchaseOrder{}, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving purchase order: "+err.Error())
		return models.PurchaseOrder{}, false
	}
	return models.PurchaseOrder(po), true
}

func (h *Handler) fetchPOItems(r *http.Request, num string) ([]models.PurchaseOrderLine, error) {
	lines, err := h.purchasing().ListPOLines(r.Context(), num)
	if err != nil {
		return nil, err
	}
	items := make([]models.PurchaseOrderLine, len(lines))
	for i, l := range lines {
		items[i] = models.PurchaseOrderLine{ID: l.ID, LineNumber: l.LineNumber, PartNumberSnapshot: l.PartNumberSnapshot,
			RevisionSnapshot: l.RevisionSnapshot, Description: l.Description, Qty: l.Qty, UnitCost: l.UnitCost,
			VendorPN: l.VendorPartNumber, PartID: l.PartID, LeadTimeDays: l.LeadTimeDays, ReceivedQty: l.ReceivedQty,
			DateReceived: l.DateReceived, IsLotTracked: models.TracksLots(l.TrackingMode)}
		if l.AttID != nil {
			items[i].PrimaryAtt = &models.Attachment{ID: *l.AttID, FileName: l.AttFileName, Category: l.AttCategory}
		}
	}
	return items, nil
}

// POReceiptView is one goods-receipt (a 'receipt' ledger row) for the PO detail page.
type POReceiptView struct {
	Date       string
	PartID     *int // catalog part, for linking to its transactions tab
	PartNumber string
	Qty        float64
	Username   string
}

// fetchPOReceipts returns the receipt ledger rows recorded against a PO, newest first.
func (h *Handler) fetchPOReceipts(r *http.Request, poID int) []POReceiptView {
	receipts, err := h.purchasing().ListPOReceipts(r.Context(), poID)
	if err != nil {
		log.Printf("fetchPOReceipts: %v", err)
		return nil
	}
	out := make([]POReceiptView, len(receipts))
	for i, rc := range receipts {
		out[i] = POReceiptView{Date: rc.TxnDate.Format("2006-01-02"), PartID: rc.PartID, PartNumber: rc.PartNumber,
			Qty: rc.Qty, Username: rc.Username}
	}
	return out
}

// resolvePolRev returns the revision to store on a POL row. It prefers the
// value submitted from the form; if blank and a PNID is known, it looks up
// PN.revision as a fallback so server-side saves always capture the snapshot.
func (h *Handler) resolvePolRev(r *http.Request, formRev, pnidStr string) string {
	if formRev != "" {
		return formRev
	}
	if pnidStr == "" {
		return ""
	}
	id, err := strconv.Atoi(pnidStr)
	if err != nil {
		return ""
	}
	rev, _ := h.parts().GetPartRevision(r.Context(), id) // unknown part → ""
	return rev
}

func (h *Handler) createPOFolder(r *http.Request, poNumber, supplierIDStr string) {
	root := h.cfg().POFolderRoot
	if root == "" {
		return
	}
	folderName := poNumber
	if id, err := strconv.Atoi(supplierIDStr); err == nil {
		code, _ := h.purchasing().GetSupplierCode(r.Context(), id) // missing supplier → no code
		if code != "" {
			folderName += " " + code
		}
	}
	if h.cfg().TestMode {
		folderName += "-testmode"
	}
	os.MkdirAll(filepath.Join(root, folderName), 0755)
}

// ── RFQ — Request for Quotation (issue #270) ─────────────────────────────────
// An RFQ is a purchase_order with status 'rfq'. Sibling quotes (one per supplier)
// share rfq_group_id. Responses (price + lead time per line) are captured on the
// comparison grid. "Convert to PO" is the rfq -> draft transition.
//
// Numbering (#270): an RFQ group consumes one sequence number. Quotes are stored as
// "<base>R<n>" (e.g. 1050R1, 1050R2); the winning quote is renamed to bare "<base>"
// on convert, so only one PO number is used per RFQ regardless of supplier count.

var rfqSuffixRE = regexp.MustCompile(`R\d+$`)

// rfqBaseNumber strips a trailing RFQ quote suffix: "1050R2" -> "1050".
func rfqBaseNumber(number string) string {
	return rfqSuffixRE.ReplaceAllString(number, "")
}

// RFQNew — GET /rfqs/new
func (h *Handler) RFQNew(w http.ResponseWriter, r *http.Request) {
	now := h.userNow(r)
	po := models.PurchaseOrder{Status: "rfq", IsActive: true, DateRequested: &now}
	if u := h.currentUser(r); u != nil {
		po.Orderer = u.DisplayName
	}
	supplierContacts, receiverContacts, noDefaultReceiver := h.applyPODefaults(r, &po)
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": po, "POItems": nil, "IsNew": true, "IsRFQ": true,
		"SupplierContacts": supplierContacts, "ReceiverContacts": receiverContacts,
		"NoDefaultReceiver": noDefaultReceiver,
		"ActiveTab": "pos", "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// RFQAddSupplier — GET /rfq/{id}/add-supplier
// Clones an existing RFQ into the same group for a different supplier: same parts
// and quantities, blank supplier and quoted figures.
func (h *Handler) RFQAddSupplier(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	source, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	if source.Status != "rfq" {
		h.renderError(w, r, "Only an RFQ can have supplier quotes added.")
		return
	}
	group := source.ID
	if source.RFQGroupID != nil {
		group = *source.RFQGroupID
	}
	items, err := h.fetchPOItems(r, num)
	if err != nil {
		h.renderError(w, r, "Error loading PO items: "+err.Error())
		return
	}
	for i := range items {
		items[i].UnitCost = 0
		items[i].VendorPN = ""
		items[i].LeadTimeDays = nil
	}
	recID := 0
	if source.ReceiverID != nil {
		recID = *source.ReceiverID
	}
	// Blank supplier-specific fields; keep ship-to, notes and quantities.
	source.Number = ""
	source.SupplierID = nil
	source.SupplierContactID = nil
	source.SupplierName, source.SupplierContact, source.SupplierEmail = "", "", ""
	source.SupplierAddress, source.SupplierCity, source.SupplierState = "", "", ""
	source.SupplierZipcode, source.SupplierCountry = "", ""
	source.SupplierPhoneNumber, source.SupplierFaxNumber = "", ""
	source.TotalCost = nil

	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": source, "POItems": nil, "DuplicateItems": items,
		"IsNew": true, "IsRFQ": true, "RFQGroupID": group, "RFQAddFrom": num,
		"ReceiverContacts": h.contactsForSupplier(r, recID),
		"ActiveTab":        "pos", "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// rfqCell is one supplier's quote for one part (a cell in the comparison grid).
type rfqCell struct {
	POLID    int
	Cost     float64
	LeadDays *int
	Quoted   bool // supplier entered a unit cost
	Best     bool // lowest quoted cost in the row
}

// rfqRow is one part across all suppliers in the group.
type rfqRow struct {
	PartNumber  string
	Rev         string
	Description string
	Qty         float64
	Cells       []rfqCell // aligned to the suppliers slice
}

// rfqSupplier is one quote (one PO) in the group — a column in the grid.
type rfqSupplier struct {
	Number       string
	SupplierName string
	SupplierID   *int
	Status       string
	TotalCost    float64
	Best         bool // lowest non-zero total in the group
}

// rfqScanLine is one (quote, line) row from the comparison query, decoded out of
// the SQL nullable types so the grid algorithm can be built and tested without a DB.
// HasLine is false when the LEFT JOIN produced no po_line (a quote with no lines).
type rfqScanLine struct {
	Number       string
	SupplierName string
	SupplierID   *int
	Status       string
	Total        float64
	HasLine      bool
	POLID        int
	PartNumber   string
	Rev          string
	Description  string
	Qty          float64
	Cost         float64
	LeadDays     *int
}

// buildRFQGrid turns the flat (quote, line) rows into the comparison grid: one
// column per quote (first-seen order), one row per part, cells aligned to columns
// and padded to a rectangle. It marks the cheapest quoted cell in each row and the
// quote with the lowest non-zero total. Pure — no DB, no request.
func buildRFQGrid(lines []rfqScanLine) (suppliers []rfqSupplier, rows []*rfqRow) {
	supIdx := map[string]int{}     // PO number -> column index
	rowIdx := map[string]*rfqRow{} // part key -> row

	for _, ln := range lines {
		col, ok := supIdx[ln.Number]
		if !ok {
			col = len(suppliers)
			supIdx[ln.Number] = col
			suppliers = append(suppliers, rfqSupplier{
				Number: ln.Number, SupplierName: ln.SupplierName,
				SupplierID: ln.SupplierID, Status: ln.Status, TotalCost: ln.Total,
			})
		}
		if !ln.HasLine {
			continue // quote with no lines yet
		}
		key := ln.PartNumber
		if key == "" {
			key = ln.Description
		}
		row, ok := rowIdx[key]
		if !ok {
			row = &rfqRow{PartNumber: ln.PartNumber, Rev: ln.Rev,
				Description: ln.Description, Qty: ln.Qty, Cells: make([]rfqCell, 0)}
			rowIdx[key] = row
			rows = append(rows, row)
		}
		// Grow the cells slice to cover this column.
		for len(row.Cells) <= col {
			row.Cells = append(row.Cells, rfqCell{})
		}
		row.Cells[col] = rfqCell{POLID: ln.POLID, Cost: ln.Cost, Quoted: ln.Cost > 0, LeadDays: ln.LeadDays}
	}

	// Pad every row to the full column count so the grid is rectangular, and mark
	// the lowest quoted cost in each row.
	for _, row := range rows {
		for len(row.Cells) < len(suppliers) {
			row.Cells = append(row.Cells, rfqCell{})
		}
		bestIdx, bestCost := -1, 0.0
		for i, c := range row.Cells {
			if c.Quoted && (bestIdx < 0 || c.Cost < bestCost) {
				bestIdx, bestCost = i, c.Cost
			}
		}
		if bestIdx >= 0 {
			row.Cells[bestIdx].Best = true
		}
	}

	// Mark the quote with the lowest non-zero total.
	bestSup, bestTotal := -1, 0.0
	for i, s := range suppliers {
		if s.TotalCost > 0 && (bestSup < 0 || s.TotalCost < bestTotal) {
			bestSup, bestTotal = i, s.TotalCost
		}
	}
	if bestSup >= 0 {
		suppliers[bestSup].Best = true
	}

	return suppliers, rows
}

// RFQCompare — GET /rfq/{group}/compare
func (h *Handler) RFQCompare(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	var lines []rfqScanLine
	if groupID, err := strconv.Atoi(group); err == nil { // non-numeric → no quotes → not found
		rows, err := h.purchasing().ListRFQGroupLines(r.Context(), groupID)
		if err != nil {
			h.renderError(w, r, "Error loading RFQ group: "+err.Error())
			return
		}
		lines = make([]rfqScanLine, len(rows))
		for i, l := range rows {
			lines[i] = rfqScanLine{
				Number: l.Number, SupplierName: l.SupplierName, SupplierID: &l.SupplierID, Status: l.Status,
				Total: l.TotalCost, HasLine: l.PolID != nil, PartNumber: l.PartNumber, Rev: l.Revision,
				Description: l.Description, Qty: l.Qty, Cost: l.UnitCost, LeadDays: l.LeadTimeDays,
			}
			if l.PolID != nil {
				lines[i].POLID = *l.PolID
			}
		}
	}

	suppliers, orderedRows := buildRFQGrid(lines)
	if len(suppliers) == 0 {
		h.renderError(w, r, "RFQ group not found.")
		return
	}

	h.render(w, r, "pos/rfq_compare.html", map[string]any{
		"Group": group, "Suppliers": suppliers, "Rows": orderedRows,
		"ActiveTab": "pos", "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// RFQCompareSave — POST /rfq/{group}/compare
// Persists the quoted unit cost + lead time entered per cell, then recomputes
// every quote's total in the group.
func (h *Handler) RFQCompareSave(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	groupID, err := strconv.Atoi(group)
	if err != nil {
		h.renderError(w, r, "Error loading RFQ lines: invalid RFQ group")
		return
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

	pur := purchasing.New(tx)

	// Queue behind an in-flight RFQConvert, so a stale page can't rewrite quotes it just awarded.
	if err := pur.LockRFQGroup(r.Context(), groupID); err != nil {
		h.renderError(w, r, "Error locking RFQ group: "+err.Error())
		return
	}
	total, open, err := pur.CountRFQGroupQuotes(r.Context(), groupID)
	if err != nil {
		h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
		return
	}
	if total > 0 && open == 0 {
		h.renderError(w, r, "No quotes in this RFQ group are still open; nothing was saved.")
		return
	}

	// The group's still-open line ids, so we only accept input for lines that belong to it.
	polIDs, err := pur.ListOpenRFQLineIDs(r.Context(), groupID)
	if err != nil {
		h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
		return
	}

	for _, id := range polIDs {
		idStr := strconv.Itoa(id)
		cost := 0.0
		if v, ok := parseFormFloat(fv(r, "cost_"+idStr)).(float64); ok {
			cost = v
		}
		if err := pur.SetRFQLineQuote(r.Context(), id, cost, intPtrOrNil(fv(r, "lead_"+idStr))); err != nil {
			h.renderError(w, r, "Error saving quote: "+err.Error())
			return
		}
	}

	// Recompute each quote's total (line sum + its own tax/shipping/misc).
	if err := pur.RecomputeRFQTotals(r.Context(), groupID); err != nil {
		h.renderError(w, r, "Error recomputing totals: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving quotes: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, "/rfq/"+group+"/compare", http.StatusFound)
}

// RFQConvert — POST /rfq/{id}/convert
// Awards the winning quote: duplicates it into a new real PO at the bare base
// number, and closes out the RFQ group (awarded quote -> closed, others ->
// cancelled). The RFQ quotes are retained with their history for the record.
func (h *Handler) RFQConvert(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}

	quote, err := h.purchasing().GetRFQQuote(r.Context(), num)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading RFQ: "+err.Error())
		return
	}
	poID := quote.ID
	if quote.Status != "rfq" {
		h.renderError(w, r, "Only an RFQ can be converted to a PO.")
		return
	}

	// The new PO takes the bare base number (1050R2 -> 1050). Guard against the
	// base already being in use before inserting it.
	base := rfqBaseNumber(num)
	if _, err := h.purchasing().GetPOState(r.Context(), base); err == nil {
		h.renderError(w, r, "Cannot convert: PO number "+base+" is already in use.")
		return
	} else if err != sql.ErrNoRows {
		h.renderError(w, r, "Error checking PO number: "+err.Error())
		return
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

	actor := h.actorName(r)
	pur := purchasing.New(tx)
	rfq := "rfq"

	// #191: lock the whole RFQ group in ID order first, so two quotes of one group
	// converted at once queue up instead of deadlocking on each other's rows.
	if quote.RFQGroupID != nil {
		if err := pur.LockRFQGroup(r.Context(), *quote.RFQGroupID); err != nil {
			h.renderError(w, r, "Error locking RFQ group: "+err.Error())
			return
		}
	}

	// #191: claim the quote — only a still-'rfq' quote can be awarded. Closes out the
	// awarded quote (retained for the record) and locks it for the rest of the tx.
	awarded, err := pur.AwardRFQQuote(r.Context(), poID)
	if err != nil {
		h.renderError(w, r, "Error closing awarded quote: "+err.Error())
		return
	}
	if !awarded {
		h.renderError(w, r, "Only an RFQ can be converted to a PO.")
		return
	}

	// Duplicate the winning quote's header into a new real PO: bare base number,
	// draft, no rfq_group_id (so it always shows on the PO list).
	newID, err := pur.CopyPOForConversion(r.Context(), poID, base, h.userNow(r))
	if err != nil {
		h.renderError(w, r, "Error creating PO: "+err.Error())
		return
	}

	// Copy the line items onto the new PO.
	if err := pur.CopyPOLines(r.Context(), poID, newID); err != nil {
		h.renderError(w, r, "Error copying line items: "+err.Error())
		return
	}

	// Record the new PO's creation, noting the RFQ it came from.
	if err := pur.CreatePOStatusEventNote(r.Context(), newID, nil, "draft", "Converted from RFQ "+num, actor); err != nil {
		h.renderError(w, r, "Error recording PO creation: "+err.Error())
		return
	}

	// Close out the awarded quote (retained for the record).
	if err := pur.CreatePOStatusEventNote(r.Context(), poID, &rfq, "closed", "Awarded — converted to PO #"+base, actor); err != nil {
		h.renderError(w, r, "Error recording award: "+err.Error())
		return
	}

	// Decline the other quotes in the group (retained, not deleted).
	if quote.RFQGroupID != nil {
		sibIDs, err := pur.ListOpenRFQSiblings(r.Context(), *quote.RFQGroupID, poID)
		if err != nil {
			h.renderError(w, r, "Error finding sibling quotes: "+err.Error())
			return
		}
		note := "Not awarded — PO #" + base + " issued"
		for _, id := range sibIDs {
			// #191: only decline a quote still in 'rfq' — one changed meanwhile is left alone.
			declined, err := pur.DeclineRFQQuote(r.Context(), id)
			if err != nil {
				h.renderError(w, r, "Error declining quote: "+err.Error())
				return
			}
			if !declined {
				continue
			}
			if err := pur.CreatePOStatusEventNote(r.Context(), id, &rfq, "cancelled", note, actor); err != nil {
				h.renderError(w, r, "Error recording decline: "+err.Error())
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving conversion: "+err.Error())
		return
	}
	committed = true

	// Give the new PO a folder under the bare base number.
	h.createPOFolder(r, base, strconv.Itoa(quote.SupplierID))
	http.Redirect(w, r, "/po/"+base, http.StatusFound)
}

// ── PO import part file (issue #156) ────────────────────────────────────────

// localFilePath resolves a LOCAL: FILFileName value to an absolute path.
// Returns ("", false) when root is empty, the value isn't LOCAL:, it is a directory
// reference, or it would escape root (path traversal attempt).
func localFilePath(root, filename string) (string, bool) {
	if root == "" || !urlutil.IsLocalFile(filename) {
		return "", false
	}
	rel := strings.ReplaceAll(urlutil.StripLocalPrefix(filename), "\\", "/")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.HasSuffix(rel, "/") {
		return "", false
	}
	return safePath(root, rel)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// POImportPartFile — POST /po/{id}/import-part-file (#156)
// Copies a LOCAL: file attachment from a catalog part into the PO's folder.
func (h *Handler) POImportPartFile(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}

	if h.cfg().POFolderRoot == "" {
		h.renderError(w, r, "PO_FOLDER_ROOT is not configured.")
		return
	}
	if h.cfg().DocControlRoot == "" {
		h.renderError(w, r, "DOC_CONTROL_ROOT is not configured.")
		return
	}

	if _, err := h.purchasing().GetPOState(r.Context(), num); err != nil {
		h.renderError(w, r, "Purchase order not found.")
		return
	}

	attID := r.FormValue("att_id")
	partID := r.FormValue("part_id")
	if attID == "" || partID == "" {
		h.renderError(w, r, "Missing attachment or part.")
		return
	}

	aid, err1 := strconv.Atoi(attID)
	pid, err2 := strconv.Atoi(partID)
	if errors.Join(err1, err2) != nil {
		h.renderError(w, r, "Attachment not found.")
		return
	}
	att, err := h.attachments().GetActivePartAttachment(r.Context(), aid, pid)
	if err != nil {
		h.renderError(w, r, "Attachment not found.")
		return
	}

	srcPath, ok := localFilePath(h.cfg().DocControlRoot, att.FileName)
	if !ok {
		h.renderError(w, r, "Selected attachment is not a LOCAL: file.")
		return
	}

	base := findPOBaseFolder(h.cfg().POFolderRoot, num)
	if base == "" {
		h.renderError(w, r, "No folder found for PO "+num+". Open the PO folder first to create it.")
		return
	}

	dst := filepath.Join(h.cfg().POFolderRoot, base, filepath.Base(srcPath))
	if err := copyFile(srcPath, dst); err != nil {
		h.renderError(w, r, "Error copying file: "+err.Error())
		return
	}

	http.Redirect(w, r, "/po/"+num, http.StatusFound)
}

// ── POsExportCSV — GET /pos/export.csv ──────────────────────────────────────

func (h *Handler) POsExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.purchasing().ListPOExportRows(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="purchase-orders.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"PO Number", "Status", "Supplier", "Date Ordered", "Date Closed", "Orderer", "PO Total", "Line #", "Part Number", "Description", "Qty", "Unit Cost", "Vendor PN"})
	for _, p := range rows {
		lineNumStr := ""
		if p.LineNumber != nil {
			lineNumStr = strconv.Itoa(*p.LineNumber)
		}
		_ = cw.Write([]string{
			p.Number, p.Status, p.SupplierName, isoDate(p.DateOrdered), isoDate(p.DateClosed),
			p.Orderer, fmt.Sprintf("%.2f", p.Total),
			lineNumStr, p.PartNumber, p.Description,
			fmt.Sprintf("%.4g", p.Qty), fmt.Sprintf("%.2f", p.UnitCost),
			p.VendorPN,
		})
	}
	cw.Flush()
}
