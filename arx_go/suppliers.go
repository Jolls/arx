package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/internal/urlutil"
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
		serverError(w, "database error", err)
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
			serverError(w, "database error", err)
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
	if err := rows.Err(); err != nil {
		serverError(w, "database error", err)
		return
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
		if s.DefaultContact != nil && c.ID == *s.DefaultContact {
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
	PNID        int
	PartNumber  string
	Description string
}

func (h *Handler) topSupplierParts(ctx context.Context, supplierID string, limit int) []supplierPartSummary {
	sp, pn := h.cfg.SupplierPartTable(), h.cfg.PartsTable()
	top, limitClause := h.topLimit("@p2")
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT %spn.id, pn.part_number, pn.description
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
		var num, description sql.NullString
		if rows.Scan(&s.PNID, &num, &description) != nil {
			continue
		}
		s.PartNumber, s.Description = num.String, description.String
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
	insertSupplier := h.dia().InsertReturningID(h.cfg.CompanyTable(),
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
			"Error":     "Error creating supplier: " + err.Error(),
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
			"Error":     "Supplier name is required",
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	if err := validateFolderStub(fv(r, "SUSupplierCode")); err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error":     err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET name=@p1, SUSupplierCode=@p2, default_contact=@p3,
		              is_active=@p4, is_supplier=@p5, is_manufacturer=@p6,
		              SUNotes=@p7, date_modified=@p8,
		              bulk_order_delimiter=@p9, bulk_order_pn_source=@p10
		WHERE id=@p11
	`, h.cfg.CompanyTable()),
		name, fv(r, "SUSupplierCode"),
		nullableInt(fv(r, "default_contact")),
		r.FormValue("is_active") == "1",
		r.FormValue("is_supplier") == "1",
		r.FormValue("is_manufacturer") == "1",
		fv(r, "SUNotes"), time.Now(),
		bulkOrderDelimiterOrDefault(fv(r, "bulk_order_delimiter")),
		bulkOrderPNSourceOrDefault(fv(r, "bulk_order_pn_source")),
		id,
	)
	if err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error":     "Error saving supplier: " + err.Error(),
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

	sp, pn, ut, at := h.cfg.SupplierPartTable(), h.cfg.PartsTable(), h.cfg.UomTable(), h.cfg.AttachmentsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT sp.id, sp.part_id, sp.preference, sp.supplier_pn, sp.supplier_desc,
		       sp.lead_time, sp.min_increment,
		       pn.part_number, pn.description, pn.revision, pn.category,
		       sp.uom_id,
		       COALESCE(pu.abbreviation, bu.abbreviation) AS effective_unit,
		       %s AS unit_is_explicit,
		       (SELECT MIN(file_name) FROM %s a WHERE a.part_id = pn.id AND a.is_active = %s AND a.category = @p2) AS thumb_file
		FROM %s sp
		JOIN %s pn ON sp.part_id = pn.id
		LEFT JOIN %s pu ON sp.uom_id   = pu.uom_id   -- explicit purchase unit
		LEFT JOIN %s bu ON pn.uom_id   = bu.uom_id   -- base unit fallback
		WHERE sp.supplier_id = @p1
		ORDER BY pn.part_number
	`, h.dia().BoolFromCondition("sp.uom_id IS NOT NULL"), at, h.dia().BoolLiteral(true), sp, pn, ut, ut), id, thumbnailCategory)
	if err != nil {
		h.renderError(w, r, "Error retrieving linked parts: "+err.Error())
		return
	}
	defer rows.Close()
	var links []models.SupplierPart
	for rows.Next() {
		var lk models.SupplierPart
		var preference sql.NullInt64
		var supplierPN, supplierDesc, leadTime sql.NullString
		var minIncr sql.NullFloat64
		var unitID sql.NullInt64
		var partNumber, description, revision, category, unitAbbr, thumbFile sql.NullString
		var unitIsExplicit bool
		if err := rows.Scan(
			&lk.ID, &lk.PartID, &preference, &supplierPN, &supplierDesc,
			&leadTime, &minIncr,
			&partNumber, &description, &revision, &category,
			&unitID, &unitAbbr, &unitIsExplicit, &thumbFile,
		); err != nil {
			h.renderError(w, r, "Error reading linked parts: "+err.Error())
			return
		}
		if preference.Valid {
			v := int(preference.Int64)
			lk.Preference = &v
		}
		lk.SupplierPN = supplierPN.String
		lk.SupplierDesc = supplierDesc.String
		lk.LeadTime = leadTime.String
		if minIncr.Valid {
			lk.MinIncrement = &minIncr.Float64
		}
		lk.PartNumber = partNumber.String
		lk.Description = description.String
		lk.Revision = revision.String
		lk.Category = category.String
		if unitID.Valid {
			v := int(unitID.Int64)
			lk.UnitID = &v
		}
		lk.PurchaseUnitAbbr = unitAbbr.String
		lk.PurchaseUnitIsExplicit = unitIsExplicit
		if urlutil.IsLocalFile(thumbFile.String) {
			lk.Thumb = urlutil.LocalFileURL(thumbFile.String, "/local/")
		}
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
	h.renderSupplierAttachments(w, r, chi.URLParam(r, "id"), nil)
}

// renderSupplierAttachments loads a supplier's attachments and renders the
// attachments page. extra is merged into the template data (used to surface
// errors or a duplicate-hash warning on the POST path, #71).
func (h *Handler) renderSupplierAttachments(w http.ResponseWriter, r *http.Request, id string, extra map[string]any) {
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	tbl := h.cfg.CompanyAttachmentsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT supplier_attachment_id, supplier_id, file_path, notes, sort_order
		FROM %s WHERE supplier_id = @p1 AND is_active = %s
		ORDER BY sort_order, supplier_attachment_id
	`, tbl, h.dia().BoolLiteral(true)), s.ID)
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

	editID := r.URL.Query().Get("edit")
	if editID == "" {
		editID = attIDFromExtra(extra, "DuplicateWarning")
	}
	var editingAtt *models.SupplierAttachment
	if editID != "" {
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
	data := map[string]any{
		"Supplier":    s,
		"Attachments": attachments,
		"EditingAtt":  editingAtt,
		"ActiveTab":   "suppliers", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		"NextOrderID":              nextOrderID,
		"SupplierFilesConfigured": h.cfg.SupplierFilesRoot != "",
	}
	maps.Copy(data, extra)
	h.render(w, r, "suppliers/supplier_attachments.html", data)
}

// saveSupplierUpload writes an uploaded file into SupplierFilesRoot under its
// sanitized original name, auto-suffixing " (2)", " (3)", ... on collision
// (mirrors the clipboard-paste unique-name pattern; suppliers have no
// per-part naming convention to render a destination-name collision banner
// against). Returns the LOCAL:<name> value to store.
func (h *Handler) saveSupplierUpload(hdr *multipart.FileHeader) (filePath string, err error) {
	if h.cfg.SupplierFilesRoot == "" {
		return "", fmt.Errorf("Supplier Files Root is not configured; cannot import files")
	}
	ext := filepath.Ext(hdr.Filename)
	base := sanitizeFileNamePart(strings.TrimSuffix(filepath.Base(hdr.Filename), ext))
	if base == "" {
		base = "file"
	}
	f, err := hdr.Open()
	if err != nil {
		return "", fmt.Errorf("error reading upload: %w", err)
	}
	defer f.Close()
	name, err := copyReaderIntoDocControlUnique(h.cfg.SupplierFilesRoot, base+ext, ext, f)
	if err != nil {
		return "", fmt.Errorf("error copying file: %w", err)
	}
	return "LOCAL:" + name, nil
}

func (h *Handler) SupplierAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Cancel from a duplicate-hash warning (#71): the file was already copied
	// into SupplierFilesRoot by the time the duplicate was detected, so discard
	// it unless some other active row already links the same name.
	if link := fv(r, "discard_import"); link != "" {
		if urlutil.IsLocalFile(link) && !urlutil.IsLocalDir(link) {
			_ = h.deleteAttachmentFileIfUnshared(r.Context(), h.cfg.CompanyAttachmentsTable(), "supplier_attachment_id", "file_path",
				0, link, h.companyAttachmentRoot(), urlutil.StripLocalPrefix(link))
		}
		http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
		return
	}

	var filePath string
	var imported bool
	if ups := attachmentUploads(r, "upload_file"); len(ups) > 0 {
		fp, err := h.saveSupplierUpload(ups[0])
		if err != nil {
			h.renderError(w, r, "Error adding attachment: "+err.Error())
			return
		}
		filePath = fp
		imported = true
	} else {
		filePath = strings.TrimSpace(r.FormValue("file_path"))
		if filePath == "" {
			http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
			return
		}
		filePath = urlutil.NormalizeLink(filePath)
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	sortOrderStr := strings.TrimSpace(r.FormValue("sort_order"))

	var sortOrderVal any
	if sortOrderStr != "" {
		if v, err := strconv.Atoi(sortOrderStr); err == nil {
			sortOrderVal = v
		}
	}

	hash := computeAttachmentHash(h.companyAttachmentRoot(), filePath)
	if h.companyAttachmentDuplicateWarning(w, r, id, "", hash, 0, imported, filePath, notes, sortOrderStr) {
		return
	}

	err := h.execThenEnsurePrimary(r.Context(), h.ensureSupplierPrimary, id, fmt.Sprintf(`
		INSERT INTO %s (supplier_id, file_path, notes, sort_order, hash)
		VALUES (@p1, @p2, @p3, @p4, @p5)
	`, h.cfg.CompanyAttachmentsTable()), id, filePath, notes, sortOrderVal, hash)
	if err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

// companyAttachmentDuplicateWarning runs the #71 duplicate-hash check shared
// by SupplierAttachmentCreate and SupplierAttachmentUpdate. On a match it
// renders the dismissible warning banner and returns true (the caller must
// return immediately without writing); on no match, or confirm_duplicate=1,
// it returns false. attID is "" for the create path.
func (h *Handler) companyAttachmentDuplicateWarning(w http.ResponseWriter, r *http.Request, id, attID, hash string, excludeID int, imported bool, filePath, notes, sortOrderStr string) bool {
	if fv(r, "confirm_duplicate") == "1" {
		return false
	}
	dup, err := h.findDuplicateCompanyAttachment(r.Context(), hash, excludeID)
	if err != nil {
		h.renderError(w, r, "Error checking for duplicate attachments: "+err.Error())
		return true
	}
	if dup == nil {
		return false
	}
	importedFlag := ""
	if imported {
		importedFlag = "1"
	}
	h.renderSupplierAttachments(w, r, id, map[string]any{"DuplicateWarning": map[string]string{
		"FilePath": filePath,
		"DupLabel": dup.Label,
		"DupURL":   dup.URL,
		"Notes":    notes, "SortOrder": sortOrderStr,
		"Imported": importedFlag,
		"AttID":    attID,
	}})
	return true
}

func (h *Handler) SupplierAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID := chi.URLParam(r, "attID")
	err := h.execThenEnsurePrimary(r.Context(), h.ensureSupplierPrimary, id, fmt.Sprintf(`
		UPDATE %s SET is_active = %s
		WHERE supplier_attachment_id = @p1 AND supplier_id = @p2
	`, h.cfg.CompanyAttachmentsTable(), h.dia().BoolLiteral(false)), attID, id)
	if err != nil {
		h.renderError(w, r, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

func (h *Handler) SupplierAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID := chi.URLParam(r, "attID")
	attIDInt, _ := strconv.Atoi(attID)
	notes := strings.TrimSpace(r.FormValue("notes"))
	sortOrderStr := strings.TrimSpace(r.FormValue("sort_order"))
	var newFilePath string
	var imported bool
	if ups := attachmentUploads(r, "upload_file"); len(ups) > 0 {
		fp, err := h.saveSupplierUpload(ups[0])
		if err != nil {
			h.renderError(w, r, "Error updating attachment: "+err.Error())
			return
		}
		newFilePath = fp
		imported = true
	} else {
		newFilePath = urlutil.NormalizeLink(strings.TrimSpace(r.FormValue("file_path")))
	}

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
	var hash string
	if fileChanged {
		hash = computeAttachmentHash(h.companyAttachmentRoot(), newFilePath)
		if h.companyAttachmentDuplicateWarning(w, r, id, attID, hash, attIDInt, imported, newFilePath, notes, sortOrderStr) {
			return
		}
	}
	var err error
	if fileChanged {
		_, err = h.execContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET notes=@p1, sort_order=@p2, file_path=@p3, hash=@p4
			WHERE supplier_attachment_id=@p5 AND supplier_id=@p6
		`, h.cfg.CompanyAttachmentsTable()), notes, sortOrderVal, newFilePath, hash, attID, id)
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
	var bulkOrderDelimiter, bulkOrderPNSource sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT su.id, su.name, su.SUSupplierCode, su.SUNotes,
		       su.default_contact, su.is_active, su.is_supplier, su.is_manufacturer,
		       su.SUNumOfLNKs, su.SUNumOfPOs, su.date_modified,
		       su.primary_attachment_id,
		       su.bulk_order_delimiter, su.bulk_order_pn_source,
		       cn.display_name, cn.phone_1, cn.email, cn.city
		FROM %s su
		LEFT JOIN %s cn ON su.default_contact = cn.id
		WHERE su.id = @p1
	`, h.cfg.CompanyTable(), h.cfg.ContactTable()), id).Scan(
		&s.ID, &name, &code, &notes,
		&defaultContact, &isActive, &isSupplier, &isManufacturer,
		&numLNKs, &numPOs, &dateModified,
		&primaryAttID,
		&bulkOrderDelimiter, &bulkOrderPNSource,
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
	s.DisplayName = cnName.String
	s.Phone1 = cnPhone.String
	s.Email = cnEmail.String
	s.City = cnCity.String
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
	s.BulkOrderDelimiter = bulkOrderDelimiter.String
	s.BulkOrderPNSource = bulkOrderPNSource.String
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
	path, ok := safePath(base, strings.Join(subParts, "/"))
	if !ok {
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

	folderURL := fmt.Sprintf("/supplier/%d/folder", s.ID)
	parentURL := dirParentURL(folderURL, folderURL, subParts)

	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.renderDirListing(w, r, dirListingParams{
		Path: path, RelParts: subParts,
		DirURLPrefix:  folderURL,
		FileURLPrefix: fmt.Sprintf("/supplier/%d/file", s.ID),
		DirName:       dirName,
		ParentURL:     parentURL,
		Supplier:      &s,
		ActiveTab:     "suppliers", ActiveSubTab: "folder",
		NavBackURL: backURL, NavBackLabel: backLabel,
		UploadURLPrefix: fmt.Sprintf("/supplier/%d/folder-upload", s.ID),
	})
}

// SupplierFolderUpload — POST /supplier/{id}/folder-upload
func (h *Handler) SupplierFolderUpload(w http.ResponseWriter, r *http.Request) {
	h.supplierFolderUpload(w, r, nil)
}

// SupplierFolderUploadSub — POST /supplier/{id}/folder-upload/*
func (h *Handler) SupplierFolderUploadSub(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	splat := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/supplier/%s/folder-upload/", id))
	var subParts []string
	for seg := range strings.SplitSeq(splat, "/") {
		b := filepath.Base(seg)
		if b != "" && b != "." && b != ".." {
			subParts = append(subParts, b)
		}
	}
	h.supplierFolderUpload(w, r, subParts)
}

func (h *Handler) supplierFolderUpload(w http.ResponseWriter, r *http.Request, subParts []string) {
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
		http.Error(w, "Supplier has no supplier code — cannot determine folder name", http.StatusBadRequest)
		return
	}
	base := filepath.Join(root, s.SUSupplierCode)
	dir, ok := resolveUploadDir(base, strings.Join(subParts, "/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	redirectURL := fmt.Sprintf("/supplier/%d/folder", s.ID)
	if len(subParts) > 0 {
		redirectURL += "/" + strings.Join(subParts, "/")
	}
	h.handleDirUpload(w, r, dir, redirectURL)
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
	for seg := range strings.SplitSeq(splat, "/") {
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
	h.serveSupplierFile(w, r, s, id)
}

// serveSupplierFile holds SupplierFile's logic once the supplier row is in
// hand, split out so it can be exercised in tests without a DB (#863).
func (h *Handler) serveSupplierFile(w http.ResponseWriter, r *http.Request, s models.Supplier, id string) {
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
	h.serveLocalizedFile(w, r, fileServingParams{Root: base, Splat: splat})
}

func supplierFromForm(r *http.Request) models.Supplier {
	s := models.Supplier{
		Name: fv(r, "name"), SUSupplierCode: fv(r, "SUSupplierCode"),
		SUNotes:            fv(r, "SUNotes"),
		IsActive:           r.FormValue("is_active") == "1",
		IsSupplier:         r.FormValue("is_supplier") == "1",
		IsManufacturer:     r.FormValue("is_manufacturer") == "1",
		BulkOrderDelimiter: bulkOrderDelimiterOrDefault(fv(r, "bulk_order_delimiter")),
		BulkOrderPNSource:  bulkOrderPNSourceOrDefault(fv(r, "bulk_order_pn_source")),
	}
	if v := nullableInt(fv(r, "default_contact")); v != nil {
		i := v.(int)
		s.DefaultContact = &i
	}
	return s
}

// bulkOrderDelimiterOrDefault restricts the PO "Copy for Ordering" delimiter (#80) to known values.
func bulkOrderDelimiterOrDefault(v string) string {
	switch v {
	case "comma", "tab", "newline":
		return v
	default:
		return "comma"
	}
}

// bulkOrderPNSourceOrDefault restricts the PO "Copy for Ordering" PN source (#80) to known values.
func bulkOrderPNSourceOrDefault(v string) string {
	switch v {
	case "internal", "vendor":
		return v
	default:
		return "internal"
	}
}

func nullableInt(s string) any {
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
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
