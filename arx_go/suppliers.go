package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)

// validateFolderStub ensures SUSupplierCode is safe to use as a single filesystem
// path component (see renderSupplierFolder / createPOFolder) — it must not contain
// path separators, "." / "..", or other characters that break folder names on
// Windows, macOS, or Linux.
func validateFolderStub(code string) error {
	if code == "" {
		return nil
	}
	if code == "." || code == ".." {
		return fmt.Errorf(`folder stub cannot be "." or ".."`)
	}
	if strings.ContainsAny(code, "/\\:*?\"<>|") {
		return fmt.Errorf(`folder stub cannot contain / \ : * ? " < > |`)
	}
	return nil
}

func (h *Handler) SuppliersList(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "suppliers/suppliers.html", map[string]any{
		"ActiveTab": "suppliers", "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SuppliersRows(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	type row struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Active  bool   `json:"active"`
		Country string `json:"country"`
		Links   int    `json:"links"`
		POs     int    `json:"pos"`
		Contact string `json:"contact"`
		Code    string `json:"code"`
	}
	su, cn := h.cfg.CompanyTable(), h.cfg.ContactTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT su.id, su.name, su.SUSupplierCode, su.SUNumOfLNKs, su.SUNumOfPOs,
		       su.is_active, CN.display_name, CN.country
		FROM %s su
		LEFT JOIN %s CN ON su.default_contact = CN.id
		ORDER BY su.name ASC
	`, su, cn))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := make([]row, 0)
	for rows.Next() {
		var s row
		var name, code, cnName, cnCountry sql.NullString
		var numLNKs, numPOs sql.NullInt64
		var isActive sql.NullBool
		if err := rows.Scan(&s.ID, &name, &code, &numLNKs, &numPOs, &isActive, &cnName, &cnCountry); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.Name = name.String
		s.Code = code.String
		s.Links = int(numLNKs.Int64)
		s.POs = int(numPOs.Int64)
		s.Active = isActive.Bool
		s.Contact = cnName.String
		s.Country = cnCountry.String
		out = append(out, s)
	}
	log.Printf("[rows] suppliers: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

func (h *Handler) SupplierDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	var primaryAtt *models.SupplierAttachment
	if s.PrimaryAttachmentID != nil {
		var att models.SupplierAttachment
		var fp, notes sql.NullString
		if err := h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT supplier_attachment_id, supplier_id, file_path, notes FROM %s WHERE supplier_attachment_id = @p1`,
			h.cfg.CompanyAttachmentsTable(),
		), *s.PrimaryAttachmentID).Scan(&att.SupplierAttachmentID, &att.SupplierID, &fp, &notes); err == nil {
			att.FilePath = fp.String
			att.Notes = notes.String
			primaryAtt = &att
		}
	}

	recentPOs := h.recentSupplierPOs(r.Context(), id, 5)
	topParts := h.topSupplierParts(r.Context(), id, 5)

	// Active contacts for this vendor, excluding the default (shown in its own card).
	var otherContacts []ContactSummary
	for _, c := range h.contactsForSupplier(r, s.ID) {
		if s.DefaultContact != nil && c.CNID == *s.DefaultContact {
			continue
		}
		otherContacts = append(otherContacts, c)
	}

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_detail.html", map[string]any{
		"Supplier": s, "PrimaryAtt": primaryAtt,
		"RecentPOs": recentPOs, "TopParts": topParts, "OtherContacts": otherContacts,
		"ActiveTab": "suppliers", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// supplierPOSummary is one row in the Supplier dashboard "Recent POs" card (#521).
type supplierPOSummary struct {
	Number      string
	Status      string
	DateOrdered *time.Time
	Total       float64
}

// recentSupplierPOs returns up to limit POs for supplierID, most recent first.
// limit <= 0 means unlimited (used by the Order History sub-tab).
func (h *Handler) recentSupplierPOs(ctx context.Context, supplierID string, limit int) []supplierPOSummary {
	top, limitClause := "", ""
	args := []any{supplierID}
	if limit > 0 {
		top, limitClause = h.topLimit("@p2")
		args = append(args, limit)
	}
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %snumber, status, date_ordered, total_cost
		FROM %s WHERE supplier_id = @p1
		ORDER BY date_ordered DESC, ID DESC
	`, top, h.cfg.POTable())+limitClause, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []supplierPOSummary
	for rows.Next() {
		var s supplierPOSummary
		var num, status sql.NullString
		var d sql.NullTime
		var total sql.NullFloat64
		if rows.Scan(&num, &status, &d, &total) != nil {
			continue
		}
		s.Number, s.Status, s.Total = num.String, status.String, total.Float64
		if d.Valid {
			s.DateOrdered = &d.Time
		}
		out = append(out, s)
	}
	return out
}

// supplierPartSummary is one row in the Supplier dashboard "Linked Parts" card (#521).
type supplierPartSummary struct {
	PNID       int
	PartNumber string
	Title      string
}

func (h *Handler) topSupplierParts(ctx context.Context, supplierID string, limit int) []supplierPartSummary {
	sp, pn := h.cfg.SupplierPartTable(), h.cfg.PartsTable()
	top, limitClause := h.topLimit("@p2")
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %spn.id, pn.part_number, pn.title
		FROM %s sp JOIN %s pn ON sp.part_id = pn.id
		WHERE sp.supplier_id = @p1
		ORDER BY pn.part_number
	`+limitClause, top, sp, pn), supplierID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []supplierPartSummary
	for rows.Next() {
		var s supplierPartSummary
		var num, title sql.NullString
		if rows.Scan(&s.PNID, &num, &title) != nil {
			continue
		}
		s.PartNumber, s.Title = num.String, title.String
		out = append(out, s)
	}
	return out
}

func (h *Handler) SuppliersNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
		"Supplier": models.Supplier{}, "IsNew": true, "Contacts": nil,
		"ActiveTab": "suppliers", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SuppliersCreate(w http.ResponseWriter, r *http.Request) {
	name := fv(r, "name")
	if name == "" {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Error": "Supplier name is required",
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	if err := validateFolderStub(fv(r, "SUSupplierCode")); err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Error": err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	var newID int
	insertSupplier := h.dialect.InsertReturningID(h.cfg.CompanyTable(),
		`name, SUSupplierCode, default_contact, is_active, is_supplier, is_manufacturer, SUNotes, date_modified`,
		`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8`,
		false)
	err := h.queryRowContext(r.Context(), insertSupplier,
		name, fv(r, "SUSupplierCode"),
		nullableInt(fv(r, "default_contact")),
		r.FormValue("is_active") == "1",
		r.FormValue("is_supplier") == "1",
		r.FormValue("is_manufacturer") == "1",
		fv(r, "SUNotes"), time.Now(),
	).Scan(&newID)
	if err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Contacts": nil,
			"Error": "Error creating supplier: " + err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%d", newID), http.StatusFound)
}

func (h *Handler) SupplierEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	contacts := h.contactsForSupplier(r, s.ID)
	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
		"Supplier": s, "IsNew": false, "Contacts": contacts,
		"ActiveTab": "suppliers", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SupplierUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := fv(r, "name")
	idInt := 0
	if v, err2 := strconv.Atoi(id); err2 == nil {
		idInt = v
	}
	contacts := h.contactsForSupplier(r, idInt)
	if name == "" {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error": "Supplier name is required",
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	if err := validateFolderStub(fv(r, "SUSupplierCode")); err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error": err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET name=@p1, SUSupplierCode=@p2, default_contact=@p3,
		              is_active=@p4, is_supplier=@p5, is_manufacturer=@p6,
		              SUNotes=@p7, date_modified=@p8
		WHERE id=@p9
	`, h.cfg.CompanyTable()),
		name, fv(r, "SUSupplierCode"),
		nullableInt(fv(r, "default_contact")),
		r.FormValue("is_active") == "1",
		r.FormValue("is_supplier") == "1",
		r.FormValue("is_manufacturer") == "1",
		fv(r, "SUNotes"), time.Now(), id,
	)
	if err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error": "Error saving supplier: " + err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s", id), http.StatusFound)
}

func (h *Handler) SupplierParts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var s models.Supplier
	var name sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT id, name FROM %s WHERE id = @p1`, h.cfg.CompanyTable(),
	), id).Scan(&s.ID, &name)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Supplier not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier: "+err.Error())
		return
	}
	s.Name = name.String

	sp, pn, ut := h.cfg.SupplierPartTable(), h.cfg.PartsTable(), h.cfg.UnitTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT sp.id, sp.part_id, sp.preference, sp.supplier_pn, sp.supplier_desc,
		       sp.lead_time, sp.min_increment,
		       pn.part_number, pn.title, pn.revision, pn.category,
		       sp.unit_id,
		       COALESCE(pu.abbreviation, bu.abbreviation) AS effective_unit,
		       CASE WHEN sp.unit_id IS NOT NULL THEN 1 ELSE 0 END AS unit_is_explicit
		FROM %s sp
		JOIN %s pn ON sp.part_id = pn.id
		LEFT JOIN %s pu ON sp.unit_id  = pu.unit_id   -- explicit purchase unit
		LEFT JOIN %s bu ON pn.unit_id  = bu.unit_id   -- base unit fallback
		WHERE sp.supplier_id = @p1
		ORDER BY pn.part_number
	`, sp, pn, ut, ut), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving linked parts: "+err.Error())
		return
	}
	defer rows.Close()
	var links []models.SupplierPart
	for rows.Next() {
		var lk models.SupplierPart
		var preference, supplierPN, supplierDesc, leadTime sql.NullString
		var minIncr sql.NullFloat64
		var unitID sql.NullInt64
		var partNumber, title, revision, category, unitAbbr sql.NullString
		var unitIsExplicit bool
		if err := rows.Scan(
			&lk.ID, &lk.PartID, &preference, &supplierPN, &supplierDesc,
			&leadTime, &minIncr,
			&partNumber, &title, &revision, &category,
			&unitID, &unitAbbr, &unitIsExplicit,
		); err != nil {
			h.renderError(w, r, "Error reading linked parts: "+err.Error())
			return
		}
		lk.Preference = preference.String
		lk.SupplierPN = supplierPN.String
		lk.SupplierDesc = supplierDesc.String
		lk.LeadTime = leadTime.String
		if minIncr.Valid {
			lk.MinIncrement = &minIncr.Float64
		}
		lk.PartNumber = partNumber.String
		lk.Title = title.String
		lk.Revision = revision.String
		lk.Category = category.String
		if unitID.Valid {
			v := int(unitID.Int64)
			lk.UnitID = &v
		}
		lk.PurchaseUnitAbbr = unitAbbr.String
		lk.PurchaseUnitIsExplicit = unitIsExplicit
		links = append(links, lk)
	}

	// PO links: numbers of POs placed with this vendor for each part (RFQ quotes excluded).
	poByPart := map[int][]string{}
	poRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pol.part_id, po.number
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE po.supplier_id = @p1 AND po.rfq_group_id IS NULL
		ORDER BY po.number DESC
	`, h.cfg.POLineTable(), h.cfg.POTable()), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving PO links: "+err.Error())
		return
	}
	defer poRows.Close()
	for poRows.Next() {
		var partID sql.NullInt64
		var number sql.NullString
		if err := poRows.Scan(&partID, &number); err != nil {
			h.renderError(w, r, "Error reading PO links: "+err.Error())
			return
		}
		if partID.Valid && number.Valid {
			pid := int(partID.Int64)
			poByPart[pid] = append(poByPart[pid], number.String)
		}
	}
	if err := poRows.Err(); err != nil {
		h.renderError(w, r, "Error reading PO links: "+err.Error())
		return
	}
	for i := range links {
		links[i].POLinks = poByPart[links[i].PartID]
	}

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_parts.html", map[string]any{
		"Supplier": s, "Links": links,
		"ActiveTab": "suppliers", "ActiveSubTab": "parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SupplierPOs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	orders := h.recentSupplierPOs(r.Context(), id, 0)

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_pos.html", map[string]any{
		"Supplier": s, "Orders": orders,
		"ActiveTab": "suppliers", "ActiveSubTab": "pos",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SupplierAttachments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	tbl := h.cfg.CompanyAttachmentsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT supplier_attachment_id, supplier_id, file_path, notes, sort_order
		FROM %s WHERE supplier_id = @p1 AND is_active = %s
		ORDER BY sort_order, supplier_attachment_id
	`, tbl, h.dialect.BoolLiteral(true)), s.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving attachments: "+err.Error())
		return
	}
	defer rows.Close()

	var attachments []models.SupplierAttachment
	nextOrderID := 1
	for rows.Next() {
		var a models.SupplierAttachment
		var notes sql.NullString
		var sortOrder sql.NullInt64
		if err := rows.Scan(&a.SupplierAttachmentID, &a.SupplierID, &a.FilePath, &notes, &sortOrder); err != nil {
			h.renderError(w, r, "Error reading attachments: "+err.Error())
			return
		}
		a.Notes = notes.String
		if sortOrder.Valid {
			v := int(sortOrder.Int64)
			a.SortOrder = &v
			if v+1 > nextOrderID {
				nextOrderID = v + 1
			}
		}
		attachments = append(attachments, a)
	}

	var editingAtt *models.SupplierAttachment
	if editID := r.URL.Query().Get("edit"); editID != "" {
		for i := range attachments {
			if strconv.Itoa(attachments[i].SupplierAttachmentID) == editID {
				editingAtt = &attachments[i]
				break
			}
		}
	}

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_attachments.html", map[string]any{
		"Supplier":    s,
		"Attachments": attachments,
		"EditingAtt":  editingAtt,
		"ActiveTab":   "suppliers", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		"NextOrderID": nextOrderID,
	})
}

func (h *Handler) SupplierAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	filePath := strings.TrimSpace(r.FormValue("file_path"))
	if filePath == "" {
		http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
		return
	}
	filePath = urlutil.NormalizeLink(filePath)
	notes := strings.TrimSpace(r.FormValue("notes"))
	sortOrderStr := strings.TrimSpace(r.FormValue("sort_order"))

	var sortOrderVal any
	if sortOrderStr != "" {
		if v, err := strconv.Atoi(sortOrderStr); err == nil {
			sortOrderVal = v
		}
	}

	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (supplier_id, file_path, notes, sort_order)
		VALUES (@p1, @p2, @p3, @p4)
	`, h.cfg.CompanyAttachmentsTable()), id, filePath, notes, sortOrderVal)
	if err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

func (h *Handler) SupplierAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID := chi.URLParam(r, "attID")
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET is_active = %s
		WHERE supplier_attachment_id = @p1 AND supplier_id = @p2
	`, h.cfg.CompanyAttachmentsTable(), h.dialect.BoolLiteral(false)), attID, id)
	if err != nil {
		h.renderError(w, r, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

func (h *Handler) SupplierAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID := chi.URLParam(r, "attID")
	notes := strings.TrimSpace(r.FormValue("notes"))
	sortOrderStr := strings.TrimSpace(r.FormValue("sort_order"))
	newFilePath := urlutil.NormalizeLink(strings.TrimSpace(r.FormValue("file_path")))

	var sortOrderVal any
	if sortOrderStr != "" {
		if v, err := strconv.Atoi(sortOrderStr); err == nil {
			sortOrderVal = v
		}
	}

	var oldFilePath string
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT file_path FROM %s WHERE supplier_attachment_id=@p1 AND supplier_id=@p2`, h.cfg.CompanyAttachmentsTable(),
	), attID, id).Scan(&oldFilePath); err != nil {
		h.renderError(w, r, "Error loading attachment: "+err.Error())
		return
	}

	fileChanged := newFilePath != "" && newFilePath != oldFilePath
	var err error
	if fileChanged {
		_, err = h.execContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET notes=@p1, sort_order=@p2, file_path=@p3
			WHERE supplier_attachment_id=@p4 AND supplier_id=@p5
		`, h.cfg.CompanyAttachmentsTable()), notes, sortOrderVal, newFilePath, attID, id)
	} else {
		_, err = h.execContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET notes=@p1, sort_order=@p2
			WHERE supplier_attachment_id=@p3 AND supplier_id=@p4
		`, h.cfg.CompanyAttachmentsTable()), notes, sortOrderVal, attID, id)
	}
	if err != nil {
		h.renderError(w, r, "Error updating attachment: "+err.Error())
		return
	}

	if fileChanged && urlutil.IsLocalFile(oldFilePath) {
		if err := h.deleteAttachmentFileIfUnshared(r.Context(), h.cfg.CompanyAttachmentsTable(), "supplier_attachment_id", "file_path",
			attID, oldFilePath, h.cfg.DocControlRoot, urlutil.StripLocalPrefix(oldFilePath)); err != nil {
			h.renderError(w, r, "Attachment updated, but the old file could not be removed: "+err.Error())
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) fetchSupplier(w http.ResponseWriter, r *http.Request, id string) (models.Supplier, bool) {
	var s models.Supplier
	var name, code, notes sql.NullString
	var defaultContact sql.NullInt64
	var isActive, isSupplier, isManufacturer sql.NullBool
	var numLNKs, numPOs sql.NullInt64
	var dateModified sql.NullTime
	var primaryAttID sql.NullInt64
	var cnName, cnPhone, cnEmail, cnCity sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT su.id, su.name, su.SUSupplierCode, su.SUNotes,
		       su.default_contact, su.is_active, su.is_supplier, su.is_manufacturer,
		       su.SUNumOfLNKs, su.SUNumOfPOs, su.date_modified,
		       su.primary_attachment_id,
		       cn.display_name, cn.phone_1, cn.email, cn.city
		FROM %s su
		LEFT JOIN %s cn ON su.default_contact = cn.id
		WHERE su.id = @p1
	`, h.cfg.CompanyTable(), h.cfg.ContactTable()), id).Scan(
		&s.ID, &name, &code, &notes,
		&defaultContact, &isActive, &isSupplier, &isManufacturer,
		&numLNKs, &numPOs, &dateModified,
		&primaryAttID,
		&cnName, &cnPhone, &cnEmail, &cnCity,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Supplier not found")
		return s, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier: "+err.Error())
		return s, false
	}
	s.Name = name.String
	s.SUSupplierCode = code.String
	s.SUNotes = notes.String
	s.CNName = cnName.String
	s.CNPhone1 = cnPhone.String
	s.CNEmail = cnEmail.String
	s.CNCity = cnCity.String
	s.IsActive = isActive.Bool
	s.IsSupplier = isSupplier.Bool
	s.IsManufacturer = isManufacturer.Bool
	s.SUNumOfLNKs = int(numLNKs.Int64)
	s.SUNumOfPOs = int(numPOs.Int64)
	if defaultContact.Valid {
		v := int(defaultContact.Int64)
		s.DefaultContact = &v
	}
	if dateModified.Valid {
		s.DateModified = &dateModified.Time
	}
	if primaryAttID.Valid {
		v := int(primaryAttID.Int64)
		s.PrimaryAttachmentID = &v
	}
	return s, true
}

func (h *Handler) SupplierSetPrimaryAttachment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	idInt, _ := strconv.Atoi(id)
	attIDStr := r.FormValue("attachment_id")
	var val any
	if n, err2 := strconv.Atoi(attIDStr); err2 == nil && n != 0 {
		val = n
	}
	if err := h.setPrimaryAttachment(r.Context(), h.cfg.CompanyTable(), "id", "primary_attachment_id", idInt, val); err != nil {
		h.renderError(w, r, "Error setting primary attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

// ── Supplier folder ──────────────────────────────────────────────────────────

func (h *Handler) renderSupplierFolder(w http.ResponseWriter, r *http.Request, s models.Supplier, subParts []string) {
	root := h.cfg.SupplierFilesRoot
	if root == "" {
		root = h.cfg.DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	if s.SUSupplierCode == "" {
		http.Error(w, "Supplier has no supplier code — cannot determine folder name", http.StatusBadRequest)
		return
	}

	base := filepath.Join(root, s.SUSupplierCode)
	path := base
	if len(subParts) > 0 {
		path = filepath.Join(append([]string{base}, subParts...)...)
	}

	absBase, _ := filepath.Abs(base)
	absPath, _ := filepath.Abs(path)
	if absPath != absBase && !strings.HasPrefix(absPath+string(filepath.Separator), absBase+string(filepath.Separator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		h.renderError(w, r, "Vendor folder not found: "+path)
		return
	}
	if err == nil && !info.IsDir() {
		h.renderError(w, r, "Vendor folder not found: "+path+" is not a directory")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error accessing vendor folder "+path+": "+err.Error())
		return
	}

	dirName := s.SUSupplierCode
	if len(subParts) > 0 {
		dirName = subParts[len(subParts)-1]
	}

	var parentURL string
	if len(subParts) > 1 {
		parentURL = fmt.Sprintf("/supplier/%d/folder/%s", s.ID, strings.Join(subParts[:len(subParts)-1], "/"))
	} else if len(subParts) == 1 {
		parentURL = fmt.Sprintf("/supplier/%d/folder", s.ID)
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
		rel := append(subParts, name)
		var relURL string
		if isDir {
			relURL = fmt.Sprintf("/supplier/%d/folder/%s", s.ID, strings.Join(rel, "/"))
			numDirs++
		} else {
			relURL = fmt.Sprintf("/supplier/%d/file/%s", s.ID, strings.Join(rel, "/"))
			numFiles++
		}
		entry := DirEntry{Name: name, IsDir: isDir, URL: relURL}
		if !isDir {
			entry.Ext = strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
			if fi, err2 := e.Info(); err2 == nil {
				entry.Size = formatFileSize(fi.Size())
			}
		}
		entries = append(entries, entry)
	}

	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "shared/local_dir.html", map[string]any{
		"Supplier":  &s,
		"DirName":   dirName,
		"FullPath":  path,
		"ParentURL": parentURL,
		"Entries":   entries,
		"NumDirs":   numDirs,
		"NumFiles":  numFiles,
		"ActiveTab": "suppliers", "ActiveSubTab": "folder",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SupplierFolder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	h.renderSupplierFolder(w, r, s, nil)
}

func (h *Handler) SupplierFolderSub(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/supplier/%s/folder/", id))
	var subParts []string
	for _, seg := range strings.Split(splat, "/") {
		b := filepath.Base(seg)
		if b != "" && b != "." && b != ".." {
			subParts = append(subParts, b)
		}
	}
	h.renderSupplierFolder(w, r, s, subParts)
}

func (h *Handler) SupplierFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	root := h.cfg.SupplierFilesRoot
	if root == "" {
		root = h.cfg.DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	if s.SUSupplierCode == "" {
		http.Error(w, "Supplier has no supplier code", http.StatusBadRequest)
		return
	}

	base := filepath.Join(root, s.SUSupplierCode)
	splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/supplier/%s/file/", id))
	path, ok2 := safePath(base, splat)
	if !ok2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && info.IsDir()) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Error accessing file", http.StatusInternalServerError)
		return
	}

	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	http.ServeFile(w, r, path)
}

func supplierFromForm(r *http.Request) models.Supplier {
	s := models.Supplier{
		Name: fv(r, "name"), SUSupplierCode: fv(r, "SUSupplierCode"),
		SUNotes:        fv(r, "SUNotes"),
		IsActive:       r.FormValue("is_active") == "1",
		IsSupplier:     r.FormValue("is_supplier") == "1",
		IsManufacturer: r.FormValue("is_manufacturer") == "1",
	}
	if v := nullableInt(fv(r, "default_contact")); v != nil {
		i := v.(int)
		s.DefaultContact = &i
	}
	return s
}

func nullableInt(s string) interface{} {
	if s == "" {
		return nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return nil
}

// floatOrZero parses s as a float, returning 0 for empty or invalid input.
func floatOrZero(s string) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return 0
}

// nullableText returns nil for an empty string so the column is stored as NULL.
func nullableText(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
