package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/parts_master_go/models"
)

func (h *Handler) SuppliersList(w http.ResponseWriter, r *http.Request) {
	su, cn := h.cfg.CompanyTable(), h.cfg.ContactTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT su.id, su.name, su.SUSupplierCode, su.SUNumOfLNKs, su.SUNumOfPOs,
		       su.is_active, CN.CNName, CN.CNCountry
		FROM %s su
		LEFT JOIN %s CN ON su.default_contact = CN.CNID
		ORDER BY su.name ASC
	`, su, cn))
	if err != nil {
		h.renderError(w, "Error connecting to database: "+err.Error())
		return
	}
	defer rows.Close()
	var suppliers []models.Supplier
	for rows.Next() {
		var s models.Supplier
		var name, code, cnName, cnCountry sql.NullString
		var numLNKs, numPOs sql.NullInt64
		var isActive sql.NullBool
		if err := rows.Scan(&s.ID, &name, &code, &numLNKs, &numPOs, &isActive, &cnName, &cnCountry); err != nil {
			h.renderError(w, "Error reading suppliers: "+err.Error())
			return
		}
		s.Name = name.String
		s.SUSupplierCode = code.String
		s.SUNumOfLNKs = int(numLNKs.Int64)
		s.SUNumOfPOs = int(numPOs.Int64)
		s.IsActive = isActive.Bool
		s.CNName = cnName.String
		s.CNCountry = cnCountry.String
		suppliers = append(suppliers, s)
	}
	h.render(w, "suppliers.html", map[string]any{
		"Suppliers": suppliers, "ActiveTab": "suppliers", "TestMode": h.cfg.TestMode,
	})
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

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, "supplier_detail.html", map[string]any{
		"Supplier": s, "PrimaryAtt": primaryAtt,
		"ActiveTab": "suppliers", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SuppliersNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, "supplier_edit.html", map[string]any{
		"Supplier": models.Supplier{}, "IsNew": true, "Contacts": nil,
		"ActiveTab": "suppliers", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SuppliersCreate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	name := fs(r, "name")
	if name == "" {
		h.render(w, "supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Error": "Supplier name is required",
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	var newID int
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (name, SUSupplierCode, default_contact, is_active, is_supplier, is_manufacturer, SUNotes, date_modified)
		OUTPUT INSERTED.id
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8)
	`, h.cfg.CompanyTable()),
		name, fs(r, "SUSupplierCode"),
		nullableInt(fs(r, "default_contact")),
		r.FormValue("is_active") == "1",
		r.FormValue("is_supplier") == "1",
		r.FormValue("is_manufacturer") == "1",
		fs(r, "SUNotes"), time.Now(),
	).Scan(&newID)
	if err != nil {
		h.render(w, "supplier_edit.html", map[string]any{
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
	h.render(w, "supplier_edit.html", map[string]any{
		"Supplier": s, "IsNew": false, "Contacts": contacts,
		"ActiveTab": "suppliers", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SupplierUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	name := fs(r, "name")
	idInt := 0
	if v, err2 := strconv.Atoi(id); err2 == nil {
		idInt = v
	}
	contacts := h.contactsForSupplier(r, idInt)
	if name == "" {
		h.render(w, "supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error": "Supplier name is required",
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
		name, fs(r, "SUSupplierCode"),
		nullableInt(fs(r, "default_contact")),
		r.FormValue("is_active") == "1",
		r.FormValue("is_supplier") == "1",
		r.FormValue("is_manufacturer") == "1",
		fs(r, "SUNotes"), time.Now(), id,
	)
	if err != nil {
		h.render(w, "supplier_edit.html", map[string]any{
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
		h.renderError(w, "Supplier not found")
		return
	}
	if err != nil {
		h.renderError(w, "Error retrieving supplier: "+err.Error())
		return
	}
	s.Name = name.String

	sp, pn := h.cfg.SupplierPartTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT sp.id, sp.part_id, sp.preference, sp.supplier_pn, sp.supplier_desc,
		       sp.lead_time, sp.min_increment,
		       pn.PNID, pn.part_number, pn.title, pn.revision, pn.category
		FROM %s sp
		JOIN %s pn ON sp.part_id = pn.PNID
		WHERE sp.supplier_id = @p1
		ORDER BY pn.part_number
	`, sp, pn), id)
	if err != nil {
		h.renderError(w, "Error retrieving linked parts: "+err.Error())
		return
	}
	defer rows.Close()
	var links []models.SupplierPart
	for rows.Next() {
		var lk models.SupplierPart
		var preference, supplierPN, supplierDesc, leadTime sql.NullString
		var minIncr sql.NullFloat64
		var pnID sql.NullInt64
		var partNumber, title, revision, category sql.NullString
		if err := rows.Scan(
			&lk.ID, &lk.PartID, &preference, &supplierPN, &supplierDesc,
			&leadTime, &minIncr,
			&pnID, &partNumber, &title, &revision, &category,
		); err != nil {
			h.renderError(w, "Error reading linked parts: "+err.Error())
			return
		}
		lk.Preference = preference.String
		lk.SupplierPN = supplierPN.String
		lk.SupplierDesc = supplierDesc.String
		lk.LeadTime = leadTime.String
		if minIncr.Valid {
			lk.MinIncrement = &minIncr.Float64
		}
		lk.PNID = int(pnID.Int64)
		lk.PartNumber = partNumber.String
		lk.Title = title.String
		lk.Revision = revision.String
		lk.Category = category.String
		links = append(links, lk)
	}
	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, "supplier_parts.html", map[string]any{
		"Supplier": s, "Links": links,
		"ActiveTab": "suppliers", "ActiveSubTab": "parts",
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
		FROM %s WHERE supplier_id = @p1 AND is_active = 1
		ORDER BY sort_order, supplier_attachment_id
	`, tbl), s.ID)
	if err != nil {
		h.renderError(w, "Error retrieving attachments: "+err.Error())
		return
	}
	defer rows.Close()

	var attachments []models.SupplierAttachment
	for rows.Next() {
		var a models.SupplierAttachment
		var notes sql.NullString
		var sortOrder sql.NullInt64
		if err := rows.Scan(&a.SupplierAttachmentID, &a.SupplierID, &a.FilePath, &notes, &sortOrder); err != nil {
			h.renderError(w, "Error reading attachments: "+err.Error())
			return
		}
		a.Notes = notes.String
		if sortOrder.Valid {
			v := int(sortOrder.Int64)
			a.SortOrder = &v
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
	h.render(w, "supplier_attachments.html", map[string]any{
		"Supplier":    s,
		"Attachments": attachments,
		"EditingAtt":  editingAtt,
		"ActiveTab":   "suppliers", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) SupplierAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	filePath := strings.TrimSpace(r.FormValue("file_path"))
	if filePath == "" {
		http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
		return
	}
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
		h.renderError(w, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

func (h *Handler) SupplierAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	attID := chi.URLParam(r, "attID")
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET is_active = 0
		WHERE supplier_attachment_id = @p1 AND supplier_id = @p2
	`, h.cfg.CompanyAttachmentsTable()), attID, id)
	if err != nil {
		h.renderError(w, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

func (h *Handler) SupplierAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	attID := chi.URLParam(r, "attID")
	notes := strings.TrimSpace(r.FormValue("notes"))
	sortOrderStr := strings.TrimSpace(r.FormValue("sort_order"))

	var sortOrderVal any
	if sortOrderStr != "" {
		if v, err := strconv.Atoi(sortOrderStr); err == nil {
			sortOrderVal = v
		}
	}

	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET notes=@p1, sort_order=@p2
		WHERE supplier_attachment_id=@p3 AND supplier_id=@p4
	`, h.cfg.CompanyAttachmentsTable()), notes, sortOrderVal, attID, id)
	if err != nil {
		h.renderError(w, "Error updating attachment: "+err.Error())
		return
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
		       cn.CNName, cn.CNPhone1, cn.CNEmail, cn.CNCity
		FROM %s su
		LEFT JOIN %s cn ON su.default_contact = cn.CNID
		WHERE su.id = @p1
	`, h.cfg.CompanyTable(), h.cfg.ContactTable()), id).Scan(
		&s.ID, &name, &code, &notes,
		&defaultContact, &isActive, &isSupplier, &isManufacturer,
		&numLNKs, &numPOs, &dateModified,
		&primaryAttID,
		&cnName, &cnPhone, &cnEmail, &cnCity,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, "Supplier not found")
		return s, false
	}
	if err != nil {
		h.renderError(w, "Error retrieving supplier: "+err.Error())
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
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	idInt, _ := strconv.Atoi(id)
	attIDStr := r.FormValue("attachment_id")
	var val any
	if n, err2 := strconv.Atoi(attIDStr); err2 == nil && n != 0 {
		val = n
	}
	if err := h.setPrimaryAttachment(r.Context(), h.cfg.CompanyTable(), "id", "primary_attachment_id", idInt, val); err != nil {
		h.renderError(w, "Error setting primary attachment: "+err.Error())
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
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Error accessing folder", http.StatusInternalServerError)
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
	h.render(w, "local_dir.html", map[string]any{
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
		Name: fs(r, "name"), SUSupplierCode: fs(r, "SUSupplierCode"),
		SUNotes:        fs(r, "SUNotes"),
		IsActive:       r.FormValue("is_active") == "1",
		IsSupplier:     r.FormValue("is_supplier") == "1",
		IsManufacturer: r.FormValue("is_manufacturer") == "1",
	}
	if v := nullableInt(fs(r, "default_contact")); v != nil {
		i := v.(int)
		s.DefaultContact = &i
	}
	return s
}

func nullableInt(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
