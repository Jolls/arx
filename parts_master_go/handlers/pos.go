package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/parts_master_go/models"
)

// ── ContactSummary is used for supplier/receiver contact dropdowns ──────────

type ContactSummary struct {
	CNID    int
	CNName  string
	Address string
	City    string
	State   string
	Zipcode string
	Country string
	Phone   string
	Fax     string
	Email   string
}

func (h *Handler) contactsForSupplier(r *http.Request, supplierID int) []ContactSummary {
	if supplierID <= 0 {
		return nil
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT CNID, CNName, CNAddress, CNCity, CNState, CNZipcode,
		       CNCountry, CNPhone1, CNFAX, CNEmail
		FROM %s WHERE CNSUID = @p1 AND CNActive = 1 ORDER BY CNName
	`, h.cfg.ContactTable()), supplierID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ContactSummary
	for rows.Next() {
		var c ContactSummary
		var name, addr, city, state, zip, country, phone, fax, email sql.NullString
		if rows.Scan(&c.CNID, &name, &addr, &city, &state, &zip, &country, &phone, &fax, &email) == nil {
			c.CNName = name.String
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
		case "POLItem":          row.Item = val
		case "POLPNPartNumber":  row.PartNumber = val
		case "POLRev":           row.Rev = val
		case "POLDesc":          row.Desc = val
		case "VendorPN":         row.VendorPN = val
		case "POLQty":           row.Qty = val
		case "POLCost":          row.Cost = val
		case "POLPNID":          row.PNID = val
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
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT number, status, supplier_id, supplier_name,
		       date_ordered, date_closed, orderer, total_cost
		FROM %s ORDER BY number DESC
	`, h.cfg.POTable()))
	if err != nil {
		h.renderError(w, "Error connecting to database: "+err.Error())
		return
	}
	defer rows.Close()
	var pos []models.PurchaseOrder
	for rows.Next() {
		var po models.PurchaseOrder
		var supplierID sql.NullInt64
		var supplierName, orderer, status sql.NullString
		var dateOrdered, dateClosed sql.NullTime
		var totalCost sql.NullFloat64
		if err := rows.Scan(&po.Number, &status, &supplierID, &supplierName,
			&dateOrdered, &dateClosed, &orderer, &totalCost); err != nil {
			h.renderError(w, "Error reading POs: "+err.Error())
			return
		}
		po.Status = status.String
		po.SupplierName = supplierName.String
		po.Orderer = orderer.String
		if supplierID.Valid {
			v := int(supplierID.Int64)
			po.SupplierID = &v
		}
		if totalCost.Valid {
			po.TotalCost = &totalCost.Float64
		}
		if dateOrdered.Valid {
			po.DateOrdered = &dateOrdered.Time
		}
		if dateClosed.Valid {
			po.DateClosed = &dateClosed.Time
		}
		pos = append(pos, po)
	}
	h.render(w, "pos.html", map[string]any{
		"POs": pos, "ActiveTab": "pos", "TestMode": h.cfg.TestMode,
	})
}

// ── PODetail — GET /po/{id} ──────────────────────────────────────────────────

func (h *Handler) PODetail(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	items := h.fetchPOItems(w, r, num)
	var lineTotal float64
	for _, item := range items {
		lineTotal += item.POLQty * item.POLCost
	}
	h.setNavContext(w, r, fmt.Sprintf("/po/%s", po.Number), "PO #"+po.Number)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, "po_detail.html", map[string]any{
		"PO": po, "POItems": items, "LineTotal": lineTotal,
		"ActiveTab": "pos", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode,
	})
}

// ── PONew — GET /pos/new ─────────────────────────────────────────────────────

func (h *Handler) PONew(w http.ResponseWriter, r *http.Request) {
	po := models.PurchaseOrder{Status: "pending", IsActive: true}
	if u, err := user.Current(); err == nil {
		po.Orderer = u.Username
	}

	// Apply PO defaults from settings
	if cid := h.cfg.Settings.PODefaults.ContactID; cid > 0 {
		var cnName sql.NullString
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT CNName FROM %s WHERE CNID = @p1`, h.cfg.ContactTable(),
		), cid).Scan(&cnName)
		po.SupplierContact = cnName.String
	}
	var supplierContacts, receiverContacts []ContactSummary
	if rid := h.cfg.Settings.PODefaults.ReceiverID; rid > 0 {
		var rName sql.NullString
		var rDefaultContact sql.NullInt64
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT name, default_contact FROM %s WHERE id = @p1`, h.cfg.CompanyTable(),
		), rid).Scan(&rName, &rDefaultContact)
		po.ReceiverName = rName.String
		v := rid
		po.ReceiverID = &v
		receiverContacts = h.contactsForSupplier(r, rid)
		if rDefaultContact.Valid && rDefaultContact.Int64 > 0 {
			for _, c := range receiverContacts {
				if c.CNID == int(rDefaultContact.Int64) {
					po.ReceiverContact = c.CNName
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
	h.render(w, "po_edit.html", map[string]any{
		"PO": po, "POItems": nil, "IsNew": true,
		"SupplierContacts": supplierContacts, "ReceiverContacts": receiverContacts,
		"ActiveTab": "pos", "TestMode": h.cfg.TestMode,
		"CSRFToken": h.csrfToken(w, r),
	})
}

// ── POCreate — POST /pos ─────────────────────────────────────────────────────

func (h *Handler) POCreate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, "Error parsing form: "+err.Error())
		return
	}

	// Sequence is outside the transaction — sequences never roll back in SQL Server,
	// which is correct: a rolled-back PO should not reuse its number.
	seqName := "dbo.PO_Number_Seq"
	if h.cfg.TestMode {
		seqName = "dbo.PO_Number_Seq_Test"
	}
	var newNumber string
	if err := h.queryRowContext(r.Context(),
		fmt.Sprintf("SELECT CAST(NEXT VALUE FOR %s AS VARCHAR)", seqName),
	).Scan(&newNumber); err != nil {
		h.renderError(w, "Error getting PO number: "+err.Error())
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, "Error starting transaction: "+err.Error())
		return
	}
	defer tx.Rollback()

	now := time.Now()
	// OUTPUT INSERTED.ID is blocked on tables with triggers; combine INSERT + SCOPE_IDENTITY()
	// in one batch so they share the same scope.
	newStatus := fs(r, "status")
	var newID int
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (number, status, is_active, orderer, account_id,
		  supplier_id, supplier_name, supplier_contact, supplier_email,
		  supplier_address, supplier_city, supplier_state, supplier_zipcode,
		  supplier_country, supplier_phone_number, supplier_fax_number,
		  receiver_id, receiver_name, receiver_contact, receiver_email,
		  receiver_address, receiver_city, receiver_state, receiver_zipcode,
		  receiver_country, receiver_phone, receiver_fax,
		  tax1, shipping_cost, misc_cost, notes, internal_notes, date_ordered,
		  date_requested, date_closed, date_modified, total_cost)
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,
		        @p17,@p18,@p19,@p20,@p21,@p22,@p23,@p24,@p25,@p26,@p27,
		        @p28,@p29,@p30,@p31,@p32,@p33,@p34,@p35,@p36,@p37);
		SELECT CAST(SCOPE_IDENTITY() AS INT)
	`, h.cfg.POTable()),
		newNumber, newStatus, statusIsActive(newStatus), fs(r, "orderer"), fs(r, "account_id"),
		nullableInt(fs(r, "supplier_id")), fs(r, "supplier_name"), fs(r, "supplier_contact"), fs(r, "supplier_email"),
		fs(r, "supplier_address"), fs(r, "supplier_city"), fs(r, "supplier_state"), fs(r, "supplier_zipcode"),
		fs(r, "supplier_country"), fs(r, "supplier_phone_number"), fs(r, "supplier_fax_number"),
		nullableInt(fs(r, "receiver_id")), fs(r, "receiver_name"), fs(r, "receiver_contact"), fs(r, "receiver_email"),
		fs(r, "receiver_address"), fs(r, "receiver_city"), fs(r, "receiver_state"), fs(r, "receiver_zipcode"),
		fs(r, "receiver_country"), fs(r, "receiver_phone"), fs(r, "receiver_fax"),
		parseFormFloat(fs(r, "tax1")), parseFormFloat(fs(r, "shipping_cost")), parseFormFloat(fs(r, "misc_cost")),
		fs(r, "notes"), fs(r, "internal_notes"),
		parseFormDate(fs(r, "date_ordered")), parseFormDate(fs(r, "date_requested")), parseFormDate(fs(r, "date_closed")),
		now, 0.0,
	).Scan(&newID); err != nil {
		h.renderError(w, "Error creating PO: "+err.Error())
		return
	}

	newRows := extractPolRows(r.Form, "new_pol")
	var lineTotal float64
	for _, row := range newRows {
		if row.PartNumber == "" && row.Desc == "" {
			continue
		}
		item, qty, cost, pnid := polRowToArgs(row)
		rev := h.resolvePolRev(r, row.Rev, row.PNID)
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s (POLPOID, POLItem, POLPNPartNumber, POLRev, POLDesc, POLQty, POLCost, VendorPN, POLPNID)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9)
		`, h.cfg.POLineTable()), newID, item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid); err != nil {
			h.renderError(w, "Error adding PO line: "+err.Error())
			return
		}
		lineTotal += qty * cost
	}

	tax, _ := strconv.ParseFloat(fs(r, "tax1"), 64)
	ship, _ := strconv.ParseFloat(fs(r, "shipping_cost"), 64)
	misc, _ := strconv.ParseFloat(fs(r, "misc_cost"), 64)
	totalCost := lineTotal + tax + ship + misc
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET total_cost=@p1 WHERE ID=@p2`, h.cfg.POTable(),
	), totalCost, newID); err != nil {
		h.renderError(w, "Error updating PO total: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, "Error saving PO: "+err.Error())
		return
	}

	h.createPOFolder(r, newNumber, fs(r, "supplier_id"))
	http.Redirect(w, r, "/po/"+newNumber, http.StatusFound)
}

// ── POEdit — GET /po/{id}/edit ───────────────────────────────────────────────

func (h *Handler) POEdit(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	po, ok := h.fetchPO(w, r, num)
	if !ok {
		return
	}
	items := h.fetchPOItems(w, r, num)
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
	h.render(w, "po_edit.html", map[string]any{
		"PO": po, "POItems": items, "IsNew": false,
		"SupplierContacts": h.contactsForSupplier(r, supID),
		"ReceiverContacts": h.contactsForSupplier(r, recID),
		"ActiveTab": "pos", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode, "CSRFToken": h.csrfToken(w, r),
	})
}

// ── POUpdate — POST /po/{id} ─────────────────────────────────────────────────

func (h *Handler) POUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	num := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, "Error parsing form: "+err.Error())
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, "Error starting transaction: "+err.Error())
		return
	}
	defer tx.Rollback()

	// Delete flagged line items
	for _, idStr := range r.Form["delete_pol[]"] {
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`DELETE FROM %s WHERE POLID=@p1`, h.cfg.POLineTable(),
		), idStr); err != nil {
			h.renderError(w, "Error deleting PO line: "+err.Error())
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
			UPDATE %s SET POLItem=@p1, POLPNPartNumber=@p2, POLRev=@p3, POLDesc=@p4,
			              POLQty=@p5, POLCost=@p6, VendorPN=@p7, POLPNID=@p8
			WHERE POLID=@p9
		`, h.cfg.POLineTable()), item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid, polID); err != nil {
			h.renderError(w, "Error updating PO line: "+err.Error())
			return
		}
	}

	// Insert new line items — resolve PO ID once before the loop
	newRows := extractPolRows(r.Form, "new_pol")
	if len(newRows) > 0 {
		var poID int
		if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
			`SELECT ID FROM %s WHERE number=@p1`, h.cfg.POTable(),
		), num).Scan(&poID); err != nil {
			h.renderError(w, "Error resolving PO ID: "+err.Error())
			return
		}
		for _, row := range newRows {
			if row.PartNumber == "" && row.Desc == "" {
				continue
			}
			item, qty, cost, pnid := polRowToArgs(row)
			rev := h.resolvePolRev(r, row.Rev, row.PNID)
			if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
				INSERT INTO %s (POLPOID, POLItem, POLPNPartNumber, POLRev, POLDesc, POLQty, POLCost, VendorPN, POLPNID)
				VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9)
			`, h.cfg.POLineTable()), poID, item, row.PartNumber, rev, row.Desc, qty, cost, row.VendorPN, pnid); err != nil {
				h.renderError(w, "Error adding PO line: "+err.Error())
				return
			}
		}
	}

	// Recalculate total from all remaining lines (reads within the transaction,
	// so it sees the deletes/inserts above before any other writer can interfere)
	var lineSum float64
	if err := tx.QueryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ISNULL(SUM(pol.POLQty * pol.POLCost), 0)
		FROM %s pol
		JOIN %s po ON pol.POLPOID = po.ID
		WHERE po.number = @p1
	`, h.cfg.POLineTable(), h.cfg.POTable()), num).Scan(&lineSum); err != nil {
		h.renderError(w, "Error recalculating PO total: "+err.Error())
		return
	}

	tax, _ := strconv.ParseFloat(fs(r, "tax1"), 64)
	ship, _ := strconv.ParseFloat(fs(r, "shipping_cost"), 64)
	misc, _ := strconv.ParseFloat(fs(r, "misc_cost"), 64)
	totalCost := lineSum + tax + ship + misc

	updStatus := fs(r, "status")
	if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  status=@p1, is_active=@p2, orderer=@p3, account_id=@p4,
		  supplier_id=@p5, supplier_name=@p6, supplier_contact=@p7, supplier_email=@p8,
		  supplier_address=@p9, supplier_city=@p10, supplier_state=@p11, supplier_zipcode=@p12,
		  supplier_country=@p13, supplier_phone_number=@p14, supplier_fax_number=@p15,
		  receiver_id=@p16, receiver_name=@p17, receiver_contact=@p18, receiver_email=@p19,
		  receiver_address=@p20, receiver_city=@p21, receiver_state=@p22, receiver_zipcode=@p23,
		  receiver_country=@p24, receiver_phone=@p25, receiver_fax=@p26,
		  tax1=@p27, shipping_cost=@p28, misc_cost=@p29,
		  notes=@p30, internal_notes=@p31,
		  date_ordered=@p32, date_requested=@p33, date_closed=@p34, date_printed=@p35,
		  date_modified=@p36, total_cost=@p37
		WHERE number=@p38
	`, h.cfg.POTable()),
		updStatus, statusIsActive(updStatus), fs(r, "orderer"), fs(r, "account_id"),
		nullableInt(fs(r, "supplier_id")), fs(r, "supplier_name"), fs(r, "supplier_contact"), fs(r, "supplier_email"),
		fs(r, "supplier_address"), fs(r, "supplier_city"), fs(r, "supplier_state"), fs(r, "supplier_zipcode"),
		fs(r, "supplier_country"), fs(r, "supplier_phone_number"), fs(r, "supplier_fax_number"),
		nullableInt(fs(r, "receiver_id")), fs(r, "receiver_name"), fs(r, "receiver_contact"), fs(r, "receiver_email"),
		fs(r, "receiver_address"), fs(r, "receiver_city"), fs(r, "receiver_state"), fs(r, "receiver_zipcode"),
		fs(r, "receiver_country"), fs(r, "receiver_phone"), fs(r, "receiver_fax"),
		parseFormFloat(fs(r, "tax1")), parseFormFloat(fs(r, "shipping_cost")), parseFormFloat(fs(r, "misc_cost")),
		fs(r, "notes"), fs(r, "internal_notes"),
		parseFormDate(fs(r, "date_ordered")), parseFormDate(fs(r, "date_requested")), parseFormDate(fs(r, "date_closed")), parseFormDate(fs(r, "date_printed")),
		time.Now(), totalCost, num,
	); err != nil {
		h.renderError(w, "Error saving PO: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, "Error saving PO: "+err.Error())
		return
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
	sourceItems := h.fetchPOItems(w, r, num)

	// Clear fields that shouldn't carry over
	source.Number = ""
	source.DateOrdered = nil
	source.DateRequested = nil
	source.DateClosed = nil
	source.TotalCost = nil
	source.Status = "pending"
	source.IsActive = true

	supID := 0
	if source.SupplierID != nil {
		supID = *source.SupplierID
	}
	recID := 0
	if source.ReceiverID != nil {
		recID = *source.ReceiverID
	}
	h.render(w, "po_edit.html", map[string]any{
		"PO": source, "POItems": nil, "DuplicateItems": sourceItems,
		"IsNew": true, "IsDuplicate": true, "DuplicateFrom": num,
		"SupplierContacts": h.contactsForSupplier(r, supID),
		"ReceiverContacts": h.contactsForSupplier(r, recID),
		"ActiveTab": "pos", "TestMode": h.cfg.TestMode,
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
	h.render(w, "po_note.html", map[string]any{
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

	var supplierCode string
	if po.SupplierID != nil {
		var code sql.NullString
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT SUSupplierCode FROM %s WHERE id=@p1`, h.cfg.CompanyTable(),
		), *po.SupplierID).Scan(&code)
		supplierCode = code.String
	}

	items := h.fetchPOItems(w, r, num)
	var lineTotal float64
	for _, item := range items {
		lineTotal += item.POLQty * item.POLCost
	}
	h.renderPrint(w, "po_print.html", map[string]any{
		"PO": po, "POItems": items, "LineTotal": lineTotal,
		"SupplierCode": supplierCode, "TestMode": h.cfg.TestMode,
	})
}

// ── POMarkPrinted — POST /po/{id}/mark-printed ───────────────────────────────

func (h *Handler) POMarkPrinted(w http.ResponseWriter, r *http.Request) {
	num := chi.URLParam(r, "id")
	h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET date_printed=@p1 WHERE number=@p2`, h.cfg.POTable(),
	), time.Now(), num)
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
	h.render(w, "local_dir.html", map[string]any{
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

// statusIsActive returns true for statuses that represent an open/in-progress PO.
// is_active is kept in sync with this value; status is authoritative.
func statusIsActive(status string) bool {
	return status == "pending" || status == "placed" || status == "on_hold"
}

// ── shared helpers ───────────────────────────────────────────────────────────

func (h *Handler) fetchPO(w http.ResponseWriter, r *http.Request, num string) (models.PurchaseOrder, bool) {
	var po models.PurchaseOrder
	var (
		isActive                                        sql.NullBool
		supplierID, receiverID                          sql.NullInt64
		number, orderer, accountID, status              sql.NullString
		supName, supContact, supEmail                   sql.NullString
		supAddr, supCity, supState, supZip, supCountry  sql.NullString
		supPhone, supFax                                sql.NullString
		recName, recContact, recEmail                   sql.NullString
		recAddr, recCity, recState, recZip, recCountry  sql.NullString
		recPhone, recFax                                sql.NullString
		tax1, shipping, misc, totalCost                 sql.NullFloat64
		notes, internalNotes                            sql.NullString
		dateOrdered, dateRequested, dateClosed, datePrinted, dateMod sql.NullTime
	)
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ID, number, status, is_active, orderer, account_id,
		       supplier_id, supplier_name, supplier_contact, supplier_email,
		       supplier_address, supplier_city, supplier_state, supplier_zipcode, supplier_country,
		       supplier_phone_number, supplier_fax_number,
		       receiver_id, receiver_name, receiver_contact, receiver_email,
		       receiver_address, receiver_city, receiver_state, receiver_zipcode, receiver_country,
		       receiver_phone, receiver_fax,
		       tax1, shipping_cost, misc_cost, total_cost,
		       notes, internal_notes,
		       date_ordered, date_requested, date_closed, date_printed, date_modified
		FROM %s WHERE number = @p1
	`, h.cfg.POTable()), num).Scan(
		&po.ID, &number, &status, &isActive, &orderer, &accountID,
		&supplierID, &supName, &supContact, &supEmail,
		&supAddr, &supCity, &supState, &supZip, &supCountry, &supPhone, &supFax,
		&receiverID, &recName, &recContact, &recEmail,
		&recAddr, &recCity, &recState, &recZip, &recCountry, &recPhone, &recFax,
		&tax1, &shipping, &misc, &totalCost,
		&notes, &internalNotes,
		&dateOrdered, &dateRequested, &dateClosed, &datePrinted, &dateMod,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, "Purchase order not found")
		return po, false
	}
	if err != nil {
		h.renderError(w, "Error retrieving purchase order: "+err.Error())
		return po, false
	}
	po.Number = number.String
	po.Status = status.String
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

func (h *Handler) fetchPOItems(w http.ResponseWriter, r *http.Request, num string) []models.PurchaseOrderLine {
	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pol.POLID, pol.POLItem, pol.POLPNPartNumber, pol.POLRev, pol.POLDesc,
		       pol.POLQty, pol.POLCost, pol.VendorPN, pol.POLPNID
		FROM %s pol
		JOIN %s po ON pol.POLPOID = po.ID
		WHERE po.number = @p1
		ORDER BY pol.POLItem
	`, pol, po), num)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var items []models.PurchaseOrderLine
	for rows.Next() {
		var item models.PurchaseOrderLine
		var partNumber, rev, desc, vendorPN sql.NullString
		var polpnid sql.NullInt64
		if err := rows.Scan(&item.POLID, &item.POLItem, &partNumber, &rev, &desc,
			&item.POLQty, &item.POLCost, &vendorPN, &polpnid); err == nil {
			item.POLPNPartNumber = partNumber.String
			item.POLRev = rev.String
			item.POLDesc = desc.String
			item.VendorPN = vendorPN.String
			if polpnid.Valid {
				v := int(polpnid.Int64)
				item.POLPNID = &v
			}
			items = append(items, item)
		}
	}
	return items
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
		`SELECT revision FROM %s WHERE PNID=@p1`, h.cfg.PartsTable(),
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
