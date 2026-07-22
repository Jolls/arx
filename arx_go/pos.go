package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, display_name, address, city, state, zipcode,
		       country, phone_1, fax, email
		FROM %s WHERE company_id = @p1 AND is_active = %s ORDER BY display_name
	`, h.cfg.ContactTable(), h.dia().BoolLiteral(true)), supplierID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ContactSummary
	for rows.Next() {
		var c ContactSummary
		var name, addr, city, state, zip, country, phone, fax, email sql.NullString
		if err := rows.Scan(&c.ID, &name, &addr, &city, &state, &zip, &country, &phone, &fax, &email); err != nil {
			log.Printf("contactsForSupplier: scan error: %v", err)
			break
		}
		c.DisplayName = name.String
		c.Address = addr.String
		c.City = city.String
		c.State = state.String
		c.Zipcode = zip.String
		c.Country = country.String
		c.Phone = phone.String
		c.Fax = fax.String
		c.Email = email.String
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		log.Printf("contactsForSupplier: rows error: %v", err)
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pol.part_id, pol.part_number_snapshot, pol.vendor_part_number
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE po.number = @p1
		  AND pol.part_id IS NOT NULL
		  AND pol.vendor_part_number IS NOT NULL AND pol.vendor_part_number <> ''
		  AND po.supplier_id IS NOT NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM %s sp
		    WHERE sp.part_id = pol.part_id
		      AND sp.supplier_id = po.supplier_id
		      AND sp.supplier_pn = pol.vendor_part_number
		  )
	`, h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.SupplierPartTable()), poNum)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SuggestLink
	for rows.Next() {
		var l SuggestLink
		var partID sql.NullInt64
		var partNum, vendorPN sql.NullString
		if err := rows.Scan(&partID, &partNum, &vendorPN); err != nil {
			log.Printf("fetchSuggestLinks: scan error: %v", err)
			break
		}
		l.Index = len(out)
		l.PartID = int(partID.Int64)
		l.PartNumber = partNum.String
		l.VendorPN = vendorPN.String
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		log.Printf("fetchSuggestLinks: rows error: %v", err)
	}
	return out
}

// ── SuggestPrice is a PO line whose cost has no matching active price record ──

type SuggestPrice struct {
	Index      int
	PartID     int
	PartNumber string
	Cost       float64
}

func (h *Handler) fetchSuggestPrices(r *http.Request, poNum string) []SuggestPrice {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT DISTINCT pol.part_id, pol.part_number_snapshot, pol.unit_cost
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE po.number = @p1
		  AND pol.part_id IS NOT NULL
		  AND pol.unit_cost > 0
		  AND po.supplier_id IS NOT NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM %s pr
		    WHERE pr.part_id = pol.part_id
		      AND pr.supplier_id = po.supplier_id
		      AND pr.pack_size = 1
		      AND pr.is_active = %s
		      AND pr.price_ea = pol.unit_cost
		  )
	`, h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PriceTable(), h.dia().BoolLiteral(true)), poNum)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SuggestPrice
	for rows.Next() {
		var s SuggestPrice
		var partID sql.NullInt64
		var partNum sql.NullString
		if err := rows.Scan(&partID, &partNum, &s.Cost); err != nil {
			log.Printf("fetchSuggestPrices: scan error: %v", err)
			break
		}
		s.Index = len(out)
		s.PartID = int(partID.Int64)
		s.PartNumber = partNum.String
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		log.Printf("fetchSuggestPrices: rows error: %v", err)
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
		sep := strings.Index(rest, "][")
		if sep < 0 {
			continue
		}
		id := rest[:sep]
		field := strings.TrimSuffix(rest[sep+2:], "]")
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

func polRowToArgs(row polRow) (item int, qty, cost float64, pnid interface{}) {
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

func parseFormFloat(s string) interface{} {
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
		"ActiveTab": "pos", "TestMode": h.cfg.TestMode,
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT number, status, supplier_id, rfq_group_id, supplier_name,
		       date_ordered, date_closed, orderer, total_cost
		FROM %s ORDER BY number DESC
	`, h.cfg.POTable()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := make([]row, 0)
	for rows.Next() {
		var po row
		var supplierID, groupID sql.NullInt64
		var supplierName, orderer, status sql.NullString
		var dateOrdered, dateClosed sql.NullTime
		var totalCost sql.NullFloat64
		if err := rows.Scan(&po.Num, &status, &supplierID, &groupID, &supplierName,
			&dateOrdered, &dateClosed, &orderer, &totalCost); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		po.Status = status.String
		po.Supplier = supplierName.String
		po.Orderer = orderer.String
		po.Cost = totalCost.Float64
		if supplierID.Valid {
			v := int(supplierID.Int64)
			po.SID = &v
		}
		if groupID.Valid {
			v := int(groupID.Int64)
			po.GID = &v
		}
		if dateOrdered.Valid {
			po.Ordered = dateOrdered.Time.Format("2006-01-02")
		}
		if dateClosed.Valid {
			po.Closed = dateClosed.Time.Format("2006-01-02")
		}
		out = append(out, po)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
	tplData := map[string]any{
		"PO": po, "POItems": items, "LineTotal": lineTotal,
		"ActiveTab": "pos", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode":        h.cfg.TestMode,
		"StatusActions":   poStatusActions(po.Status),
		"ApprovalLabel":   poApprovalLabels[po.ApprovalStatus],
		"ApprovalActions": poApprovalActions(po.ApprovalStatus, canApprove),
		"History":         h.fetchPOHistory(r, po.ID),
		"Receipts":        h.fetchPOReceipts(r, po.ID),
		"Today":           time.Now().Format("2006-01-02"),
		"CanSend":         poApprovalAllowsSend(po.ApprovalStatus),
		"CSRFToken":       h.csrfToken(w, r),
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

// ── PONew — GET /pos/new ─────────────────────────────────────────────────────

// applyPODefaults populates a new PO/RFQ with the logged-in user's default
// receiver/contact (Profile → PO defaults, issue #463), returning the contact
// dropdown lists for the edit form. When the user has no default contact, the
// receiver company's own default_contact is used as a fallback.
func (h *Handler) applyPODefaults(r *http.Request, po *models.PurchaseOrder) (supplierContacts, receiverContacts []ContactSummary) {
	var receiverID, contactID int
	if u := h.currentUser(r); u != nil {
		receiverID = u.DefaultPOReceiverID
		contactID = u.DefaultPOContactID
	}
	if rid := receiverID; rid > 0 {
		var rName sql.NullString
		var rDefaultContact sql.NullInt64
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT name, default_contact FROM %s WHERE id = @p1`, h.cfg.CompanyTable(),
		), rid).Scan(&rName, &rDefaultContact)
		po.ReceiverName = rName.String
		v := rid
		po.ReceiverID = &v
		receiverContacts = h.contactsForSupplier(r, rid)

		// Pick the receiver contact: the resolved PO default contact wins;
		// otherwise fall back to the receiver company's own default_contact.
		wantContact := contactID
		if wantContact <= 0 && rDefaultContact.Valid {
			wantContact = int(rDefaultContact.Int64)
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
	return supplierContacts, receiverContacts
}

func (h *Handler) PONew(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	po := models.PurchaseOrder{Status: "draft", IsActive: true, DateOrdered: &now, DateRequested: &now}
	if u := h.currentUser(r); u != nil {
		po.Orderer = u.DisplayName
	}
	supplierContacts, receiverContacts := h.applyPODefaults(r, &po)
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": po, "POItems": nil, "IsNew": true,
		"SupplierContacts": supplierContacts, "ReceiverContacts": receiverContacts,
		"ActiveTab": "pos", "TestMode": h.cfg.TestMode,
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
		var anchorNum sql.NullString
		var count int
		if err := h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT number FROM %s WHERE id=@p1`, h.cfg.POTable()), rfqGroup).Scan(&anchorNum); err != nil {
			h.renderError(w, r, "Error loading RFQ group: "+err.Error())
			return
		}
		if err := h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT COUNT(*) FROM %s WHERE rfq_group_id=@p1`, h.cfg.POTable()), rfqGroup).Scan(&count); err != nil {
			h.renderError(w, r, "Error counting RFQ quotes: "+err.Error())
			return
		}
		newNumber = fmt.Sprintf("%sR%d", rfqBaseNumber(anchorNum.String), count+1)
	} else {
		// New PO or first RFQ quote: take one sequence number. The sequence is
		// outside the transaction — sequences never roll back in SQL Server, which
		// is correct: a rolled-back PO should not reuse its number.
		var base string
		if err := h.queryRowContext(r.Context(),
			fmt.Sprintf("SELECT CAST(%s AS VARCHAR)", h.dia().NextSequenceValueExpr("po_number_seq")),
		).Scan(&base); err != nil {
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

	now := time.Now()
	// OUTPUT INSERTED.ID is blocked on tables with triggers; combine INSERT + SCOPE_IDENTITY()
	// in one batch so they share the same scope.
	// New POs start as 'draft'; RFQs (#270) start as 'rfq'. Later status changes go
	// through POStatusTransition.
	newStatus := "draft"
	if isRFQ {
		newStatus = "rfq"
	}
	var newID int
	insertPO := h.dia().InsertReturningID(h.cfg.POTable(),
		`number, status, is_active, orderer, account_id,
		 supplier_id, supplier_name, supplier_contact, supplier_email,
		 supplier_address, supplier_city, supplier_state, supplier_zipcode,
		 supplier_country, supplier_phone_number, supplier_fax_number,
		 receiver_id, receiver_name, receiver_contact, receiver_email,
		 receiver_address, receiver_city, receiver_state, receiver_zipcode,
		 receiver_country, receiver_phone, receiver_fax,
		 tax1, shipping_cost, misc_cost, notes, internal_notes, date_ordered,
		 date_requested, date_closed, date_modified, total_cost,
		 supplier_contact_id, receiver_contact_id`,
		`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,
		 @p17,@p18,@p19,@p20,@p21,@p22,@p23,@p24,@p25,@p26,@p27,
		 @p28,@p29,@p30,@p31,@p32,@p33,@p34,@p35,@p36,@p37,@p38,@p39`,
		true)
	if err := tx.QueryRowContext(r.Context(), insertPO,
		newNumber, newStatus, statusIsActive(newStatus), fv(r, "orderer"), fv(r, "account_id"),
		nullableInt(fv(r, "supplier_id")), fv(r, "supplier_name"), fv(r, "supplier_contact"), fv(r, "supplier_email"),
		fv(r, "supplier_address"), fv(r, "supplier_city"), fv(r, "supplier_state"), fv(r, "supplier_zipcode"),
		fv(r, "supplier_country"), fv(r, "supplier_phone_number"), fv(r, "supplier_fax_number"),
		nullableInt(fv(r, "receiver_id")), fv(r, "receiver_name"), fv(r, "receiver_contact"), fv(r, "receiver_email"),
		fv(r, "receiver_address"), fv(r, "receiver_city"), fv(r, "receiver_state"), fv(r, "receiver_zipcode"),
		fv(r, "receiver_country"), fv(r, "receiver_phone"), fv(r, "receiver_fax"),
		parseFormFloat(fv(r, "tax1")), parseFormFloat(fv(r, "shipping_cost")), parseFormFloat(fv(r, "misc_cost")),
		fv(r, "notes"), fv(r, "internal_notes"),
		parseFormDate(fv(r, "date_ordered")), parseFormDate(fv(r, "date_requested")), parseFormDate(fv(r, "date_closed")),
		now, 0.0,
		nullableInt(fv(r, "supplier_contact_id")), nullableInt(fv(r, "receiver_contact_id")),
	).Scan(&newID); err != nil {
		h.renderError(w, r, "Error creating PO: "+err.Error())
		return
	}

	// Record the creation as the first history entry (status event, from_status NULL).
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, from_status, to_status, changed_by, changed_at)
		VALUES (@p1, 'status', NULL, @p2, @p3, @p4)
	`, h.cfg.POHistoryTable()), newID, newStatus, h.actorName(r), now); err != nil {
		h.renderError(w, r, "Error recording PO status: "+err.Error())
		return
	}

	// RFQ grouping (#270): join an existing group when adding another supplier's quote,
	// otherwise anchor a new group to this RFQ's own id.
	if isRFQ {
		groupID := newID
		if g := nullableInt(fv(r, "rfq_group_id")); g != nil {
			groupID = g.(int)
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET rfq_group_id=@p1 WHERE ID=@p2`, h.cfg.POTable(),
		), groupID, newID); err != nil {
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
		item, qty, cost, pnid := polRowToArgs(row)
		rev := h.resolvePolRev(r, row.Rev, row.PNID)
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s (po_id, line_number, part_number_snapshot, revision_snapshot, description, qty, unit_cost, vendor_part_number, part_id)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9)
		`, h.cfg.POLineTable()), newID, item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid); err != nil {
			h.renderError(w, r, "Error adding PO line: "+err.Error())
			return
		}
		lineTotal += qty * cost
	}

	tax, _ := strconv.ParseFloat(fv(r, "tax1"), 64)
	ship, _ := strconv.ParseFloat(fv(r, "shipping_cost"), 64)
	misc, _ := strconv.ParseFloat(fv(r, "misc_cost"), 64)
	totalCost := lineTotal + tax + ship + misc
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET total_cost=@p1 WHERE ID=@p2`, h.cfg.POTable(),
	), totalCost, newID); err != nil {
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
		"TestMode": h.cfg.TestMode, "CSRFToken": h.csrfToken(w, r),
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
	var poID int
	var priorApproval sql.NullString
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
		`SELECT ID, approval_status FROM %s WHERE number=@p1`, h.cfg.POTable()),
		num).Scan(&poID, &priorApproval); err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}

	// Delete flagged line items
	for _, idStr := range r.Form["delete_pol[]"] {
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`DELETE FROM %s WHERE id=@p1 AND po_id=@p2`, h.cfg.POLineTable(),
		), idStr, poID); err != nil {
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
		item, qty, cost, pnid := polRowToArgs(row)
		rev := h.resolvePolRev(r, row.Rev, row.PNID)
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET line_number=@p1, part_number_snapshot=@p2, revision_snapshot=@p3, description=@p4,
			              qty=@p5, unit_cost=@p6, vendor_part_number=@p7, part_id=@p8
			WHERE id=@p9 AND po_id=@p10
		`, h.cfg.POLineTable()), item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid, polID, poID); err != nil {
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
			item, qty, cost, pnid := polRowToArgs(row)
			rev := h.resolvePolRev(r, row.Rev, row.PNID)
			if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s (po_id, line_number, part_number_snapshot, revision_snapshot, description, qty, unit_cost, vendor_part_number, part_id)
				VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9)
			`, h.cfg.POLineTable()), poID, item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid); err != nil {
				h.renderError(w, r, "Error adding PO line: "+err.Error())
				return
			}
		}
	}

	// Recalculate total from all remaining lines (reads within the transaction,
	// so it sees the deletes/inserts above before any other writer can interfere)
	var lineSum float64
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(SUM(pol.qty * pol.unit_cost), 0)
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE po.number = @p1
	`, h.cfg.POLineTable(), h.cfg.POTable()), num).Scan(&lineSum); err != nil {
		h.renderError(w, r, "Error recalculating PO total: "+err.Error())
		return
	}

	tax, _ := strconv.ParseFloat(fv(r, "tax1"), 64)
	ship, _ := strconv.ParseFloat(fv(r, "shipping_cost"), 64)
	misc, _ := strconv.ParseFloat(fv(r, "misc_cost"), 64)
	totalCost := lineSum + tax + ship + misc

	// status and is_active are intentionally NOT updated here — they change only
	// via POStatusTransition (POST /po/{id}/status), which records the transition.
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  orderer=@p1, account_id=@p2,
		  supplier_id=@p3, supplier_name=@p4, supplier_contact=@p5, supplier_email=@p6,
		  supplier_address=@p7, supplier_city=@p8, supplier_state=@p9, supplier_zipcode=@p10,
		  supplier_country=@p11, supplier_phone_number=@p12, supplier_fax_number=@p13,
		  receiver_id=@p14, receiver_name=@p15, receiver_contact=@p16, receiver_email=@p17,
		  receiver_address=@p18, receiver_city=@p19, receiver_state=@p20, receiver_zipcode=@p21,
		  receiver_country=@p22, receiver_phone=@p23, receiver_fax=@p24,
		  tax1=@p25, shipping_cost=@p26, misc_cost=@p27,
		  notes=@p28, internal_notes=@p29,
		  date_ordered=@p30, date_requested=@p31, date_closed=@p32, date_printed=@p33,
		  date_modified=@p34, total_cost=@p35,
		  supplier_contact_id=@p37, receiver_contact_id=@p38
		WHERE number=@p36
	`, h.cfg.POTable()),
		fv(r, "orderer"), fv(r, "account_id"),
		nullableInt(fv(r, "supplier_id")), fv(r, "supplier_name"), fv(r, "supplier_contact"), fv(r, "supplier_email"),
		fv(r, "supplier_address"), fv(r, "supplier_city"), fv(r, "supplier_state"), fv(r, "supplier_zipcode"),
		fv(r, "supplier_country"), fv(r, "supplier_phone_number"), fv(r, "supplier_fax_number"),
		nullableInt(fv(r, "receiver_id")), fv(r, "receiver_name"), fv(r, "receiver_contact"), fv(r, "receiver_email"),
		fv(r, "receiver_address"), fv(r, "receiver_city"), fv(r, "receiver_state"), fv(r, "receiver_zipcode"),
		fv(r, "receiver_country"), fv(r, "receiver_phone"), fv(r, "receiver_fax"),
		parseFormFloat(fv(r, "tax1")), parseFormFloat(fv(r, "shipping_cost")), parseFormFloat(fv(r, "misc_cost")),
		fv(r, "notes"), fv(r, "internal_notes"),
		parseFormDate(fv(r, "date_ordered")), parseFormDate(fv(r, "date_requested")), parseFormDate(fv(r, "date_closed")), parseFormDate(fv(r, "date_printed")),
		time.Now(), totalCost, num,
		nullableInt(fv(r, "supplier_contact_id")), nullableInt(fv(r, "receiver_contact_id")),
	); err != nil {
		h.renderError(w, r, "Error saving PO: "+err.Error())
		return
	}

	// Reset approval if this edit invalidated a prior decision (#267).
	if priorApproval.String == "approved" || priorApproval.String == "pending" {
		if err := h.resetApproval(r, tx, poID, "PO edited after "+priorApproval.String); err != nil {
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
	for i := 0; i < linksCount; i++ {
		if r.FormValue(fmt.Sprintf("add_%d", i)) != "1" {
			continue
		}
		partID := r.FormValue(fmt.Sprintf("part_id_%d", i))
		supplierPN := r.FormValue(fmt.Sprintf("supplier_pn_%d", i))
		if partID == "" || supplierPN == "" || supplierID == "" {
			continue
		}
		if _, err := h.execContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s (part_id, supplier_id, supplier_pn)
			SELECT @p1, @p2, @p3
			WHERE NOT EXISTS (
			  SELECT 1 FROM %s WHERE part_id=@p1 AND supplier_id=@p2 AND supplier_pn=@p3
			)
		`, h.cfg.SupplierPartTable(), h.cfg.SupplierPartTable()),
			partID, supplierID, supplierPN,
		); err != nil {
			h.renderError(w, r, "Error adding supplier link: "+err.Error())
			return
		}
	}

	today := time.Now().Format("2006-01-02")
	pr := h.cfg.PriceTable()
	pricesCount, _ := strconv.Atoi(r.FormValue("prices_count"))
	for i := 0; i < pricesCount; i++ {
		if r.FormValue(fmt.Sprintf("add_price_%d", i)) != "1" {
			continue
		}
		partID := r.FormValue(fmt.Sprintf("price_part_id_%d", i))
		cost := r.FormValue(fmt.Sprintf("price_cost_%d", i))
		if partID == "" || cost == "" || supplierID == "" {
			continue
		}
		// Deactivate any existing active price at pack_size=1 for this part+supplier.
		if _, err := h.execContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET is_active=%s
			WHERE part_id=@p1 AND supplier_id=@p2 AND pack_size=1 AND is_active=%s
		`, pr, h.dia().BoolLiteral(false), h.dia().BoolLiteral(true)), partID, supplierID); err != nil {
			h.renderError(w, r, "Error updating price: "+err.Error())
			return
		}
		if _, err := h.execContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
			VALUES (@p1, @p2, 1, @p3, @p3, @p4, %s)
		`, pr, h.dia().BoolLiteral(true)), partID, supplierID, cost, today); err != nil {
			h.renderError(w, r, "Error inserting price: "+err.Error())
			return
		}
		h.ensureDefaultSupplier(r.Context(), partID, supplierID)
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
		"ActiveTab":        "pos", "TestMode": h.cfg.TestMode,
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
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
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
		var code sql.NullString
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT SUSupplierCode FROM %s WHERE id=@p1`, h.cfg.CompanyTable(),
		), *po.SupplierID).Scan(&code)
		supplierCode = code.String
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
	if root := h.cfg.POFolderRoot; root != "" {
		if base := findPOBaseFolder(root, num); base != "" {
			folderPath = filepath.Join(root, base)
		}
	}

	h.renderPrint(w, "pos/po_print.html", map[string]any{
		"PO": po, "POItems": items, "LineTotal": lineTotal,
		"SupplierCode": supplierCode, "TestMode": h.cfg.TestMode,
		"POFolderPath": folderPath, "IsRFQ": po.Status == "rfq",
		"CompanyLogo": h.companyLogoURL(),
	})
}

// ── POMarkPrinted — POST /po/{id}/mark-printed ───────────────────────────────

func (h *Handler) POMarkPrinted(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	// Approval gate (#267): only set date_printed for approved POs. RFQs (#270)
	// print without approval.
	var approval, status sql.NullString
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT approval_status, status FROM %s WHERE number=@p1`, h.cfg.POTable()),
		num).Scan(&approval, &status); err != nil || (status.String != "rfq" && !poApprovalAllowsSend(approval.String)) {
		http.Error(w, "PO is not approved", http.StatusForbidden)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET date_printed=@p1 WHERE number=@p2`, h.cfg.POTable(),
	), time.Now(), num); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POOpenFolder — POST /po/{id}/open-folder ─────────────────────────────────

func (h *Handler) POOpenFolder(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.POFolderRoot
	if root == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	num := chi.URLParam(r, "id")
	var supplierID sql.NullInt64
	h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT supplier_id FROM %s WHERE number=@p1`, h.cfg.POTable(),
	), num).Scan(&supplierID)
	supplierIDStr := ""
	if supplierID.Valid {
		supplierIDStr = strconv.FormatInt(supplierID.Int64, 10)
	}
	h.createPOFolder(r, num, supplierIDStr)
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
	root := h.cfg.POFolderRoot
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
	path := base
	if len(subParts) > 0 {
		path = filepath.Join(append([]string{base}, subParts...)...)
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

	var parentURL string
	if len(subParts) > 1 {
		parentURL = fmt.Sprintf("/po/%s/folder/%s", po.Number, strings.Join(subParts[:len(subParts)-1], "/"))
	} else if len(subParts) == 1 {
		parentURL = fmt.Sprintf("/po/%s/folder", po.Number)
	}

	rawEntries, _ := os.ReadDir(path)
	sort.Slice(rawEntries, func(i, j int) bool {
		di, dj := rawEntries[i].IsDir(), rawEntries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(rawEntries[i].Name()) < strings.ToLower(rawEntries[j].Name())
	})

	var entries []DirEntry
	var numDirs, numFiles int
	for _, e := range rawEntries {
		name := e.Name()
		isDir := e.IsDir()
		var relURL string
		rel := append(subParts, name)
		if isDir {
			relURL = fmt.Sprintf("/po/%s/folder/%s", po.Number, strings.Join(rel, "/"))
			numDirs++
		} else {
			relURL = fmt.Sprintf("/po/%s/file/%s", po.Number, strings.Join(rel, "/"))
			numFiles++
		}
		entry := DirEntry{Name: name, IsDir: isDir, URL: relURL}
		if !isDir {
			entry.Ext = strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
			if fi, err := e.Info(); err == nil {
				entry.Size = formatFileSize(fi.Size())
			}
		}
		entries = append(entries, entry)
	}

	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "shared/local_dir.html", map[string]any{
		"PO":        &po,
		"DirName":   dirName,
		"FullPath":  path,
		"ParentURL": parentURL,
		"Entries":   entries,
		"NumDirs":   numDirs,
		"NumFiles":  numFiles,
		"ActiveTab": "pos", "ActiveSubTab": "folder",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode,
	})
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
	for _, seg := range strings.Split(splat, "/") {
		base := filepath.Base(seg)
		if base != "" && base != "." && base != ".." {
			subParts = append(subParts, base)
		}
	}
	h.renderPOFolder(w, r, po, subParts)
}

func (h *Handler) POFile(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	root := h.cfg.POFolderRoot
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
	var parts []string
	for _, seg := range strings.Split(splat, "/") {
		base := filepath.Base(seg)
		if base != "" && base != "." && base != ".." {
			parts = append(parts, base)
		}
	}
	path := filepath.Join(append([]string{root, baseName}, parts...)...)
	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && info.IsDir()) {
		http.NotFound(w, r)
		return
	}
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	http.ServeFile(w, r, path)
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
	for _, t := range poTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT event_type, from_status, to_status, action, note, changed_by, changed_at
		FROM %s WHERE po_id = @p1 ORDER BY changed_at DESC, id DESC
	`, h.cfg.POHistoryTable()), poID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []POHistoryEvent
	for rows.Next() {
		var e POHistoryEvent
		var from, to, action, note, by sql.NullString
		var at sql.NullTime
		if err := rows.Scan(&e.EventType, &from, &to, &action, &note, &by, &at); err != nil {
			log.Printf("fetchPOHistory: scan error: %v", err)
			break
		}
		e.FromStatus = from.String
		e.ToStatus = to.String
		e.Action = action.String
		e.Note = note.String
		e.ChangedBy = by.String
		if at.Valid {
			e.ChangedAt = at.Time
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		log.Printf("fetchPOHistory: rows error: %v", err)
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

	var poID int
	var current, approval sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT ID, status, approval_status FROM %s WHERE number = @p1`, h.cfg.POTable()),
		num).Scan(&poID, &current, &approval)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	if !poCanTransition(current.String, target) {
		h.renderError(w, r, fmt.Sprintf("Cannot change status from %q to %q.", current.String, target))
		return
	}
	// Approval gate (#267): a PO cannot be sent until it has been approved.
	if target == "sent" && approval.String != "approved" {
		h.renderError(w, r, "This PO must be approved before it can be marked Sent.")
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

	if err := h.recordPOStatusChange(r, tx, poID, current.String, target); err != nil {
		h.renderError(w, r, "Error updating PO status: "+err.Error())
		return
	}

	// Cancelling a PO clears any approval (#267): a cancelled PO is not approved,
	// and a later reopen must go through approval again.
	if target == "cancelled" && approval.String != "not_submitted" {
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
	ctx := r.Context()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, from_status, to_status, changed_by, changed_at)
		VALUES (@p1, 'status', @p2, @p3, @p4, @p5)
	`, h.cfg.POHistoryTable()), poID, from, to, h.actorName(r), time.Now()); err != nil {
		return err
	}
	// date_closed mirrors the closed state: set it when closing (if unset),
	// clear it when reopening from closed.
	query := fmt.Sprintf(`UPDATE %s SET status=@p1, is_active=@p2, date_modified=@p3`, h.cfg.POTable())
	switch {
	case to == "closed":
		query += `, date_closed=COALESCE(date_closed, CAST(GETDATE() AS DATE))`
	case from == "closed":
		query += `, date_closed=NULL`
	}
	query += ` WHERE ID=@p4`
	_, err := tx.ExecContext(ctx, query, to, statusIsActive(to), time.Now(), poID)
	return err
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

	var poID int
	var status sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT ID, status FROM %s WHERE number = @p1`, h.cfg.POTable()),
		num).Scan(&poID, &status)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}
	if status.String != "sent" && status.String != "partially_received" {
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
		now := time.Now()
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
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET received_qty = received_qty + @p1, date_received = @p2 WHERE id = @p3`,
			h.cfg.POLineTable()), d, *txnDate, items[i].ID); err != nil {
			h.renderError(w, r, "Error updating line item: "+err.Error())
			return
		}
		items[i].ReceivedQty += d // keep in-memory copy current for status derivation
	}

	// Re-derive the PO status from the now-updated line receipts.
	if target := derivePOReceiptStatus(items); target != "" && target != status.String &&
		poCanTransition(status.String, target) {
		if err := h.recordPOStatusChange(r, tx, poID, status.String, target); err != nil {
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
	ctx := r.Context()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET approval_status='not_submitted' WHERE ID=@p1`, h.cfg.POTable()), poID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, action, note, changed_by, changed_at)
		VALUES (@p1, 'approval', 'reset', @p2, @p3, @p4)
	`, h.cfg.POHistoryTable()), poID, note, h.actorName(r), time.Now())
	return err
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

	var poID int
	var current sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT ID, approval_status FROM %s WHERE number = @p1`, h.cfg.POTable()),
		num).Scan(&poID, &current)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading PO: "+err.Error())
		return
	}

	next, ok := poApprovalNext(action, current.String)
	if !ok {
		h.renderError(w, r, fmt.Sprintf("Cannot %s a PO whose approval status is %q.", action, current.String))
		return
	}

	// Map the action verb to the logged past-tense form.
	logged := map[string]string{"submit": "submitted", "approve": "approved", "reject": "rejected"}[action]
	note := fv(r, "note")

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

	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, action, note, changed_by, changed_at)
		VALUES (@p1, 'approval', @p2, @p3, @p4, @p5)
	`, h.cfg.POHistoryTable()), poID, logged, nullableText(note), h.actorName(r), time.Now()); err != nil {
		h.renderError(w, r, "Error recording approval: "+err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET approval_status=@p1 WHERE ID=@p2`, h.cfg.POTable()),
		next, poID); err != nil {
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
	var po models.PurchaseOrder
	var (
		isActive                                                     sql.NullBool
		supplierID, receiverID, rfqGroupID                           sql.NullInt64
		supContactID, recContactID                                   sql.NullInt64
		number, orderer, accountID, status, approvalStatus           sql.NullString
		supName, supContact, supEmail                                sql.NullString
		supAddr, supCity, supState, supZip, supCountry               sql.NullString
		supPhone, supFax                                             sql.NullString
		recName, recContact, recEmail                                sql.NullString
		recAddr, recCity, recState, recZip, recCountry               sql.NullString
		recPhone, recFax                                             sql.NullString
		tax1, shipping, misc, totalCost                              sql.NullFloat64
		notes, internalNotes                                         sql.NullString
		dateOrdered, dateRequested, dateClosed, datePrinted, dateMod sql.NullTime
	)
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ID, number, status, approval_status, is_active, orderer, account_id,
		       supplier_id, supplier_name, supplier_contact, supplier_email,
		       supplier_address, supplier_city, supplier_state, supplier_zipcode, supplier_country,
		       supplier_phone_number, supplier_fax_number,
		       receiver_id, receiver_name, receiver_contact, receiver_email,
		       receiver_address, receiver_city, receiver_state, receiver_zipcode, receiver_country,
		       receiver_phone, receiver_fax,
		       tax1, shipping_cost, misc_cost, total_cost,
		       notes, internal_notes, rfq_group_id,
		       date_ordered, date_requested, date_closed, date_printed, date_modified,
		       supplier_contact_id, receiver_contact_id
		FROM %s WHERE number = @p1
	`, h.cfg.POTable()), num).Scan(
		&po.ID, &number, &status, &approvalStatus, &isActive, &orderer, &accountID,
		&supplierID, &supName, &supContact, &supEmail,
		&supAddr, &supCity, &supState, &supZip, &supCountry, &supPhone, &supFax,
		&receiverID, &recName, &recContact, &recEmail,
		&recAddr, &recCity, &recState, &recZip, &recCountry, &recPhone, &recFax,
		&tax1, &shipping, &misc, &totalCost,
		&notes, &internalNotes, &rfqGroupID,
		&dateOrdered, &dateRequested, &dateClosed, &datePrinted, &dateMod,
		&supContactID, &recContactID,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return po, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving purchase order: "+err.Error())
		return po, false
	}
	po.Number = number.String
	po.Status = status.String
	po.ApprovalStatus = approvalStatus.String
	po.IsActive = isActive.Bool
	po.Orderer = orderer.String
	po.AccountID = accountID.String
	po.SupplierName = supName.String
	po.SupplierContact = supContact.String
	po.SupplierEmail = supEmail.String
	po.SupplierAddress = supAddr.String
	po.SupplierCity = supCity.String
	po.SupplierState = supState.String
	po.SupplierZipcode = supZip.String
	po.SupplierCountry = supCountry.String
	po.SupplierPhoneNumber = supPhone.String
	po.SupplierFaxNumber = supFax.String
	po.ReceiverName = recName.String
	po.ReceiverContact = recContact.String
	po.ReceiverEmail = recEmail.String
	po.ReceiverAddress = recAddr.String
	po.ReceiverCity = recCity.String
	po.ReceiverState = recState.String
	po.ReceiverZipcode = recZip.String
	po.ReceiverCountry = recCountry.String
	po.ReceiverPhone = recPhone.String
	po.ReceiverFax = recFax.String
	po.Notes = notes.String
	po.InternalNotes = internalNotes.String
	if supplierID.Valid {
		v := int(supplierID.Int64)
		po.SupplierID = &v
	}
	if receiverID.Valid {
		v := int(receiverID.Int64)
		po.ReceiverID = &v
	}
	if supContactID.Valid {
		v := int(supContactID.Int64)
		po.SupplierContactID = &v
	}
	if recContactID.Valid {
		v := int(recContactID.Int64)
		po.ReceiverContactID = &v
	}
	if rfqGroupID.Valid {
		v := int(rfqGroupID.Int64)
		po.RFQGroupID = &v
	}
	if tax1.Valid {
		po.Tax1 = &tax1.Float64
	}
	if shipping.Valid {
		po.ShippingCost = &shipping.Float64
	}
	if misc.Valid {
		po.MiscCost = &misc.Float64
	}
	if totalCost.Valid {
		po.TotalCost = &totalCost.Float64
	}
	if dateOrdered.Valid {
		po.DateOrdered = &dateOrdered.Time
	}
	if dateRequested.Valid {
		po.DateRequested = &dateRequested.Time
	}
	if dateClosed.Valid {
		po.DateClosed = &dateClosed.Time
	}
	if datePrinted.Valid {
		po.DatePrinted = &datePrinted.Time
	}
	if dateMod.Valid {
		po.DateModified = &dateMod.Time
	}
	return po, true
}

func (h *Handler) fetchPOItems(r *http.Request, num string) ([]models.PurchaseOrderLine, error) {
	pol, po, parts, fil := h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PartsTable(), h.cfg.AttachmentsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pol.id, pol.line_number, pol.part_number_snapshot, pol.revision_snapshot, pol.description,
		       pol.qty, pol.unit_cost, pol.vendor_part_number, pol.part_id, pol.lead_time_days,
		       pol.received_qty, pol.date_received,
		       p.tracking_mode,
		       fil.id, fil.file_name, fil.category
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		LEFT JOIN %s p ON pol.part_id = p.id
		LEFT JOIN %s fil ON p.primary_attachment_id = fil.id
		WHERE po.number = @p1
		ORDER BY pol.line_number
	`, pol, po, parts, fil), num)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []models.PurchaseOrderLine
	for rows.Next() {
		var item models.PurchaseOrderLine
		var partNumber, rev, desc, vendorPN sql.NullString
		var polpnid, leadTime sql.NullInt64
		var dateReceived sql.NullTime
		var trackingMode sql.NullString
		var filID sql.NullInt64
		var filFileName, filCategory sql.NullString
		if err := rows.Scan(&item.ID, &item.LineNumber, &partNumber, &rev, &desc,
			&item.Qty, &item.UnitCost, &vendorPN, &polpnid, &leadTime,
			&item.ReceivedQty, &dateReceived,
			&trackingMode,
			&filID, &filFileName, &filCategory); err != nil {
			return nil, err
		}
		item.IsLotTracked = models.TracksLots(trackingMode.String)
		item.PartNumberSnapshot = partNumber.String
		item.RevisionSnapshot = rev.String
		item.Description = desc.String
		item.VendorPN = vendorPN.String
		if polpnid.Valid {
			v := int(polpnid.Int64)
			item.PartID = &v
		}
		if leadTime.Valid {
			v := int(leadTime.Int64)
			item.LeadTimeDays = &v
		}
		if dateReceived.Valid {
			t := dateReceived.Time
			item.DateReceived = &t
		}
		if filID.Valid {
			item.PrimaryAtt = &models.Attachment{
				ID:       int(filID.Int64),
				FileName: filFileName.String,
				Category: filCategory.String,
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT it.txn_date, pol.part_id, pol.part_number_snapshot, it.qty, it.username
		FROM %s it
		JOIN %s pol ON it.po_line_id = pol.id
		WHERE pol.po_id = @p1 AND it.txn_type = 'receipt'
		ORDER BY it.txn_date DESC, it.id DESC
	`, h.cfg.InventoryTxnTable(), h.cfg.POLineTable()), poID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []POReceiptView
	for rows.Next() {
		var v POReceiptView
		var date sql.NullTime
		var partID sql.NullInt64
		var pn, user sql.NullString
		if err := rows.Scan(&date, &partID, &pn, &v.Qty, &user); err != nil {
			log.Printf("fetchPOReceipts: scan error: %v", err)
			break
		}
		if date.Valid {
			v.Date = date.Time.Format("2006-01-02")
		}
		if partID.Valid {
			id := int(partID.Int64)
			v.PartID = &id
		}
		v.PartNumber = pn.String
		v.Username = user.String
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		log.Printf("fetchPOReceipts: rows error: %v", err)
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
	var rev sql.NullString
	h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT revision FROM %s WHERE id=@p1`, h.cfg.PartsTable(),
	), pnidStr).Scan(&rev)
	return rev.String
}

func (h *Handler) createPOFolder(r *http.Request, poNumber, supplierIDStr string) {
	root := h.cfg.POFolderRoot
	if root == "" {
		return
	}
	folderName := poNumber
	if supplierIDStr != "" {
		var code sql.NullString
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT SUSupplierCode FROM %s WHERE id=@p1`, h.cfg.CompanyTable(),
		), supplierIDStr).Scan(&code)
		if code.String != "" {
			folderName += " " + code.String
		}
	}
	if h.cfg.TestMode {
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
	now := time.Now()
	po := models.PurchaseOrder{Status: "rfq", IsActive: true, DateRequested: &now}
	if u := h.currentUser(r); u != nil {
		po.Orderer = u.DisplayName
	}
	supplierContacts, receiverContacts := h.applyPODefaults(r, &po)
	h.render(w, r, "pos/po_edit.html", map[string]any{
		"PO": po, "POItems": nil, "IsNew": true, "IsRFQ": true,
		"SupplierContacts": supplierContacts, "ReceiverContacts": receiverContacts,
		"ActiveTab": "pos", "TestMode": h.cfg.TestMode,
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
		"ActiveTab":        "pos", "TestMode": h.cfg.TestMode,
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT po.number, po.supplier_name, po.supplier_id, po.status, po.total_cost,
		       pol.id, pol.part_number_snapshot, pol.revision_snapshot, pol.description,
		       pol.qty, pol.unit_cost, pol.lead_time_days
		FROM %s po
		LEFT JOIN %s pol ON pol.po_id = po.ID
		WHERE po.rfq_group_id = @p1
		ORDER BY po.ID, pol.line_number
	`, h.cfg.POTable(), h.cfg.POLineTable()), group)
	if err != nil {
		h.renderError(w, r, "Error loading RFQ group: "+err.Error())
		return
	}
	defer rows.Close()

	var lines []rfqScanLine
	for rows.Next() {
		var number, supName, status, partNum, rev, desc sql.NullString
		var supID, polID, leadDays sql.NullInt64
		var total, qty, cost sql.NullFloat64
		if err := rows.Scan(&number, &supName, &supID, &status, &total,
			&polID, &partNum, &rev, &desc, &qty, &cost, &leadDays); err != nil {
			h.renderError(w, r, "Error loading RFQ group: "+err.Error())
			return
		}
		ln := rfqScanLine{
			Number: number.String, SupplierName: supName.String, Status: status.String,
			Total: total.Float64, HasLine: polID.Valid, POLID: int(polID.Int64),
			PartNumber: partNum.String, Rev: rev.String, Description: desc.String,
			Qty: qty.Float64, Cost: cost.Float64,
		}
		if supID.Valid {
			v := int(supID.Int64)
			ln.SupplierID = &v
		}
		if leadDays.Valid {
			v := int(leadDays.Int64)
			ln.LeadDays = &v
		}
		lines = append(lines, ln)
	}
	if err := rows.Err(); err != nil {
		h.renderError(w, r, "Error loading RFQ group: "+err.Error())
		return
	}

	suppliers, orderedRows := buildRFQGrid(lines)
	if len(suppliers) == 0 {
		h.renderError(w, r, "RFQ group not found.")
		return
	}

	h.render(w, r, "pos/rfq_compare.html", map[string]any{
		"Group": group, "Suppliers": suppliers, "Rows": orderedRows,
		"ActiveTab": "pos", "TestMode": h.cfg.TestMode,
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

	// All line ids in the group, so we only accept input for lines that belong to it.
	idRows, err := tx.QueryContext(r.Context(), fmt.Sprintf(`
		SELECT pol.id FROM %s pol JOIN %s po ON pol.po_id = po.ID
		WHERE po.rfq_group_id = @p1
	`, h.cfg.POLineTable(), h.cfg.POTable()), group)
	if err != nil {
		h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
		return
	}
	var polIDs []int
	for idRows.Next() {
		var id int
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
			return
		}
		polIDs = append(polIDs, id)
	}
	if err := idRows.Err(); err != nil {
		idRows.Close()
		h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
		return
	}
	idRows.Close()

	for _, id := range polIDs {
		idStr := strconv.Itoa(id)
		cost := 0.0
		if v, ok := parseFormFloat(fv(r, "cost_"+idStr)).(float64); ok {
			cost = v
		}
		lead := nullableInt(fv(r, "lead_"+idStr))
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET unit_cost=@p1, lead_time_days=@p2 WHERE id=@p3`, h.cfg.POLineTable()),
			cost, lead, id); err != nil {
			h.renderError(w, r, "Error saving quote: "+err.Error())
			return
		}
	}

	// Recompute each quote's total (line sum + its own tax/shipping/misc).
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		UPDATE %s
		SET total_cost = COALESCE((SELECT SUM(pol.qty * pol.unit_cost) FROM %s pol WHERE pol.po_id = %s.ID), 0)
		    + COALESCE(tax1, 0) + COALESCE(shipping_cost, 0) + COALESCE(misc_cost, 0),
		    date_modified = GETDATE()
		WHERE rfq_group_id = @p1
	`, h.cfg.POTable(), h.cfg.POLineTable(), h.cfg.POTable()), group); err != nil {
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

	var poID int
	var status sql.NullString
	var groupID, supplierID sql.NullInt64
	err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT ID, status, rfq_group_id, supplier_id FROM %s WHERE number = @p1`, h.cfg.POTable()),
		num).Scan(&poID, &status, &groupID, &supplierID)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Purchase order not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error loading RFQ: "+err.Error())
		return
	}
	if status.String != "rfq" {
		h.renderError(w, r, "Only an RFQ can be converted to a PO.")
		return
	}

	// The new PO takes the bare base number (1050R2 -> 1050). Guard against the
	// base already being in use before inserting it.
	base := rfqBaseNumber(num)
	var taken int
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE number=@p1`, h.cfg.POTable()), base).Scan(&taken); err != nil {
		h.renderError(w, r, "Error checking PO number: "+err.Error())
		return
	}
	if taken > 0 {
		h.renderError(w, r, "Cannot convert: PO number "+base+" is already in use.")
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

	now := time.Now()
	actor := h.actorName(r)

	// Duplicate the winning quote's header into a new real PO: bare base number,
	// draft, no rfq_group_id (so it always shows on the PO list).
	var newID int
	insertPO := h.dia().InsertSelectReturningID(h.cfg.POTable(),
		`number, status, is_active, approval_status, rfq_group_id,
		  orderer, account_id,
		  supplier_id, supplier_name, supplier_contact, supplier_email,
		  supplier_address, supplier_city, supplier_state, supplier_zipcode,
		  supplier_country, supplier_phone_number, supplier_fax_number,
		  receiver_id, receiver_name, receiver_contact, receiver_email,
		  receiver_address, receiver_city, receiver_state, receiver_zipcode,
		  receiver_country, receiver_phone, receiver_fax,
		  tax1, shipping_cost, misc_cost, total_cost, notes, internal_notes,
		  date_ordered, date_requested, date_closed, date_printed, date_modified,
		  supplier_contact_id, receiver_contact_id`,
		fmt.Sprintf(`SELECT @p1, 'draft', %s, 'not_submitted', NULL,
		  orderer, account_id,
		  supplier_id, supplier_name, supplier_contact, supplier_email,
		  supplier_address, supplier_city, supplier_state, supplier_zipcode,
		  supplier_country, supplier_phone_number, supplier_fax_number,
		  receiver_id, receiver_name, receiver_contact, receiver_email,
		  receiver_address, receiver_city, receiver_state, receiver_zipcode,
		  receiver_country, receiver_phone, receiver_fax,
		  tax1, shipping_cost, misc_cost, total_cost, notes, internal_notes,
		  CAST(GETDATE() AS DATE), date_requested, NULL, NULL, @p2,
		  supplier_contact_id, receiver_contact_id
		FROM %s WHERE id=@p3`, h.dia().BoolLiteral(true), h.cfg.POTable()),
		true)
	if err := tx.QueryRowContext(r.Context(), insertPO, base, now, poID).Scan(&newID); err != nil {
		h.renderError(w, r, "Error creating PO: "+err.Error())
		return
	}

	// Copy the line items onto the new PO.
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (po_id, line_number, part_number_snapshot, revision_snapshot,
		  description, qty, unit_cost, vendor_part_number, part_id, lead_time_days)
		SELECT @p1, line_number, part_number_snapshot, revision_snapshot,
		  description, qty, unit_cost, vendor_part_number, part_id, lead_time_days
		FROM %s WHERE po_id=@p2
	`, h.cfg.POLineTable(), h.cfg.POLineTable()), newID, poID); err != nil {
		h.renderError(w, r, "Error copying line items: "+err.Error())
		return
	}

	// Record the new PO's creation, noting the RFQ it came from.
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, from_status, to_status, note, changed_by, changed_at)
		VALUES (@p1, 'status', NULL, 'draft', @p2, @p3, @p4)
	`, h.cfg.POHistoryTable()), newID, "Converted from RFQ "+num, actor, now); err != nil {
		h.renderError(w, r, "Error recording PO creation: "+err.Error())
		return
	}

	// Close out the awarded quote (retained for the record).
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, from_status, to_status, note, changed_by, changed_at)
		VALUES (@p1, 'status', 'rfq', 'closed', @p2, @p3, @p4)
	`, h.cfg.POHistoryTable()), poID, "Awarded — converted to PO #"+base, actor, now); err != nil {
		h.renderError(w, r, "Error recording award: "+err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET status='closed', is_active=%s, date_modified=@p1 WHERE ID=@p2`, h.cfg.POTable(), h.dia().BoolLiteral(false)),
		now, poID); err != nil {
		h.renderError(w, r, "Error closing awarded quote: "+err.Error())
		return
	}

	// Decline the other quotes in the group (retained, not deleted).
	if groupID.Valid {
		sibRows, err := tx.QueryContext(r.Context(), fmt.Sprintf(
			`SELECT ID FROM %s WHERE rfq_group_id=@p1 AND status='rfq' AND ID<>@p2`, h.cfg.POTable()),
			groupID.Int64, poID)
		if err != nil {
			h.renderError(w, r, "Error finding sibling quotes: "+err.Error())
			return
		}
		var sibIDs []int
		for sibRows.Next() {
			var id int
			if err := sibRows.Scan(&id); err != nil {
				sibRows.Close()
				h.renderError(w, r, "Error finding sibling quotes: "+err.Error())
				return
			}
			sibIDs = append(sibIDs, id)
		}
		if err := sibRows.Err(); err != nil {
			sibRows.Close()
			h.renderError(w, r, "Error finding sibling quotes: "+err.Error())
			return
		}
		sibRows.Close()
		note := "Not awarded — PO #" + base + " issued"
		for _, id := range sibIDs {
			if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s (po_id, event_type, from_status, to_status, note, changed_by, changed_at)
				VALUES (@p1, 'status', 'rfq', 'cancelled', @p2, @p3, @p4)
			`, h.cfg.POHistoryTable()), id, note, actor, now); err != nil {
				h.renderError(w, r, "Error recording decline: "+err.Error())
				return
			}
			if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
				`UPDATE %s SET status='cancelled', is_active=%s, date_modified=@p1 WHERE ID=@p2`, h.cfg.POTable(), h.dia().BoolLiteral(false)),
				now, id); err != nil {
				h.renderError(w, r, "Error declining quote: "+err.Error())
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
	supplierIDStr := ""
	if supplierID.Valid {
		supplierIDStr = strconv.FormatInt(supplierID.Int64, 10)
	}
	h.createPOFolder(r, base, supplierIDStr)
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

	if h.cfg.POFolderRoot == "" {
		h.renderError(w, r, "PO_FOLDER_ROOT is not configured.")
		return
	}
	if h.cfg.DocControlRoot == "" {
		h.renderError(w, r, "DOC_CONTROL_ROOT is not configured.")
		return
	}

	var poID int
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT ID FROM %s WHERE number = @p1`, h.cfg.POTable()), num).Scan(&poID); err != nil {
		h.renderError(w, r, "Purchase order not found.")
		return
	}

	attID := r.FormValue("att_id")
	partID := r.FormValue("part_id")
	if attID == "" || partID == "" {
		h.renderError(w, r, "Missing attachment or part.")
		return
	}

	var fname sql.NullString
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT file_name FROM %s WHERE id = @p1 AND part_id = @p2 AND is_active = %s`,
		h.cfg.AttachmentsTable(), h.dia().BoolLiteral(true)), attID, partID).Scan(&fname); err != nil {
		h.renderError(w, r, "Attachment not found.")
		return
	}

	srcPath, ok := localFilePath(h.cfg.DocControlRoot, fname.String)
	if !ok {
		h.renderError(w, r, "Selected attachment is not a LOCAL: file.")
		return
	}

	base := findPOBaseFolder(h.cfg.POFolderRoot, num)
	if base == "" {
		h.renderError(w, r, "No folder found for PO "+num+". Open the PO folder first to create it.")
		return
	}

	dst := filepath.Join(h.cfg.POFolderRoot, base, filepath.Base(srcPath))
	if err := copyFile(srcPath, dst); err != nil {
		h.renderError(w, r, "Error copying file: "+err.Error())
		return
	}

	http.Redirect(w, r, "/po/"+num, http.StatusFound)
}

// ── POsExportCSV — GET /pos/export.csv ──────────────────────────────────────

func (h *Handler) POsExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT p.number, p.status, p.supplier_name, p.date_ordered, p.date_closed,
		       p.orderer, p.total_cost,
		       l.line_number, l.part_number, l.description, l.qty, l.unit_cost, l.vendor_pn
		FROM %s p
		LEFT JOIN %s l ON l.po_number = p.number
		ORDER BY p.number DESC, l.line_number
	`, h.cfg.POTable(), h.cfg.POLineTable()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="purchase-orders.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"PO Number", "Status", "Supplier", "Date Ordered", "Date Closed", "Orderer", "PO Total", "Line #", "Part Number", "Description", "Qty", "Unit Cost", "Vendor PN"})
	for rows.Next() {
		var num, status, supplier, orderer sql.NullString
		var dateOrdered, dateClosed sql.NullTime
		var totalCost sql.NullFloat64
		var lineNum sql.NullInt64
		var linePN, lineDesc, lineVendorPN sql.NullString
		var lineQty, lineUnitCost sql.NullFloat64
		if err := rows.Scan(&num, &status, &supplier, &dateOrdered, &dateClosed,
			&orderer, &totalCost, &lineNum, &linePN, &lineDesc, &lineQty, &lineUnitCost, &lineVendorPN); err != nil {
			return
		}
		orderedStr := ""
		if dateOrdered.Valid {
			orderedStr = dateOrdered.Time.Format("2006-01-02")
		}
		closedStr := ""
		if dateClosed.Valid {
			closedStr = dateClosed.Time.Format("2006-01-02")
		}
		lineNumStr := ""
		if lineNum.Valid {
			lineNumStr = fmt.Sprintf("%d", lineNum.Int64)
		}
		_ = cw.Write([]string{
			num.String, status.String, supplier.String, orderedStr, closedStr,
			orderer.String, fmt.Sprintf("%.2f", totalCost.Float64),
			lineNumStr, linePN.String, lineDesc.String,
			fmt.Sprintf("%.4g", lineQty.Float64), fmt.Sprintf("%.2f", lineUnitCost.Float64),
			lineVendorPN.String,
		})
	}
	cw.Flush()
}
