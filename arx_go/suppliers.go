package main

import (
	"database/sql"
	"errors"
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

	"arx/arx_go/models"
	"arx/internal/attachments"
	"arx/internal/purchasing"
	"arx/internal/urlutil"
)

// validateFolderStub ensures supplier_code is safe to use as a single filesystem
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
		"ActiveTab": "suppliers", "TestMode": h.cfg().TestMode,
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
	suppliers, err := h.purchasing().ListSupplierRows(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	out := make([]row, len(suppliers))
	for i, s := range suppliers {
		out[i] = row{ID: s.ID, Name: s.Name, Active: s.IsActive, Country: s.ContactCountry,
			Links: s.SupplierPartCount, POs: s.POCount, Contact: s.ContactName, Code: s.SupplierCode}
	}
	log.Printf("[rows] suppliers: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

func (h *Handler) SupplierDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	var primaryAtt *models.SupplierAttachment
	if s.PrimaryAttachmentID != nil {
		if a, err := h.attachments().GetCompanyAttachment(r.Context(), *s.PrimaryAttachmentID); err == nil {
			primaryAtt = &models.SupplierAttachment{SupplierAttachmentID: a.ID, SupplierID: a.SupplierID,
				FilePath: a.FilePath, Notes: a.Notes}
		}
	}

	// The dashboard cards are best-effort: a failed read just hides the card.
	recentPOs, _ := h.purchasing().ListSupplierPOs(r.Context(), s.ID, 5)
	topParts, _ := h.purchasing().ListTopSupplierParts(r.Context(), s.ID, 5)

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
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) purchasing() *purchasing.Service { return purchasing.New(handlerDB{h}) }

func (h *Handler) SuppliersNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
		"Supplier": models.Supplier{}, "IsNew": true, "Contacts": nil,
		"ActiveTab": "suppliers", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) SuppliersCreate(w http.ResponseWriter, r *http.Request) {
	name := fv(r, "name")
	if name == "" {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Error": "Supplier name is required",
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	if err := validateFolderStub(fv(r, "supplier_code")); err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Error": err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	newID, err := h.purchasing().CreateSupplier(r.Context(), purchasingSupplierFromForm(r))
	if err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": true, "Contacts": nil,
			"Error":     "Error creating supplier: " + err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%d", newID), http.StatusFound)
}

func (h *Handler) SupplierEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) SupplierUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	if err := validateFolderStub(fv(r, "supplier_code")); err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error":     err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	if err := h.purchasing().UpdateSupplier(r.Context(), idInt, purchasingSupplierFromForm(r)); err != nil {
		h.render(w, r, "suppliers/supplier_edit.html", map[string]any{
			"Supplier": supplierFromForm(r), "IsNew": false, "Contacts": contacts,
			"Error":     "Error saving supplier: " + err.Error(),
			"ActiveTab": "suppliers", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s", id), http.StatusFound)
}

func (h *Handler) SupplierParts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	parts, err := h.purchasing().ListLinkedParts(r.Context(), s.ID, thumbnailCategory)
	if err != nil {
		h.renderError(w, r, "Error retrieving linked parts: "+err.Error())
		return
	}
	// PO links: numbers of POs placed with this vendor for each part (RFQ quotes excluded).
	poByPart, err := h.purchasing().SupplierPOLinks(r.Context(), s.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving PO links: "+err.Error())
		return
	}
	links := make([]models.SupplierPart, len(parts))
	for i, p := range parts {
		links[i] = models.SupplierPart{
			ID: p.ID, PartID: p.PartID, Preference: p.Preference, SupplierPN: p.SupplierPN,
			SupplierDesc: p.SupplierDesc, LeadTime: p.LeadTime, MinIncrement: p.MinIncrement,
			PartNumber: p.PartNumber, Description: p.Description, Revision: p.Revision, Category: p.Category,
			UnitID: p.UnitID, PurchaseUnitAbbr: p.UnitAbbr, PurchaseUnitIsExplicit: p.UnitIsExplicit,
			POLinks: poByPart[p.PartID],
		}
		if urlutil.IsLocalFile(p.ThumbFile) {
			links[i].Thumb = urlutil.LocalFileURL(p.ThumbFile, "/local/")
		}
	}

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_parts.html", map[string]any{
		"Supplier": s, "Links": links,
		"ActiveTab": "suppliers", "ActiveSubTab": "parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) SupplierPOs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	orders, _ := h.purchasing().ListSupplierPOs(r.Context(), s.ID, 0)

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	h.render(w, r, "suppliers/supplier_pos.html", map[string]any{
		"Supplier": s, "Orders": orders,
		"ActiveTab": "suppliers", "ActiveSubTab": "pos",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) SupplierAttachments(w http.ResponseWriter, r *http.Request) {
	h.renderSupplierAttachments(w, r, r.PathValue("id"), nil)
}

// renderSupplierAttachments loads a supplier's attachments and renders the
// attachments page. extra is merged into the template data (used to surface
// errors or a duplicate-hash warning on the POST path, #71).
func (h *Handler) renderSupplierAttachments(w http.ResponseWriter, r *http.Request, id string, extra map[string]any) {
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}

	rows, err := h.attachments().ListCompanyAttachments(r.Context(), s.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving attachments: "+err.Error())
		return
	}

	var atts []models.SupplierAttachment
	nextOrderID := 1
	for _, row := range rows {
		atts = append(atts, models.SupplierAttachment{SupplierAttachmentID: row.ID,
			SupplierID: row.SupplierID, FilePath: row.FilePath, Notes: row.Notes, SortOrder: row.SortOrder})
		if row.SortOrder != nil && *row.SortOrder+1 > nextOrderID {
			nextOrderID = *row.SortOrder + 1
		}
	}

	editID := r.URL.Query().Get("edit")
	if editID == "" {
		editID = attIDFromExtra(extra, "DuplicateWarning")
	}
	var editingAtt *models.SupplierAttachment
	if editID != "" {
		for i := range atts {
			if strconv.Itoa(atts[i].SupplierAttachmentID) == editID {
				editingAtt = &atts[i]
				break
			}
		}
	}

	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	data := map[string]any{
		"Supplier":    s,
		"Attachments": atts,
		"EditingAtt":  editingAtt,
		"ActiveTab":   "suppliers", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		"NextOrderID":              nextOrderID,
		"SupplierFilesConfigured": h.cfg().SupplierFilesRoot != "",
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
	if h.cfg().SupplierFilesRoot == "" {
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
	name, err := copyReaderIntoDocControlUnique(h.cfg().SupplierFilesRoot, base+ext, ext, f)
	if err != nil {
		return "", fmt.Errorf("error copying file: %w", err)
	}
	return "LOCAL:" + name, nil
}

func (h *Handler) SupplierAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Cancel from a duplicate-hash warning (#71): the file was already copied
	// into SupplierFilesRoot by the time the duplicate was detected, so discard
	// it unless some other active row already links the same name.
	if link := fv(r, "discard_import"); link != "" {
		if urlutil.IsLocalFile(link) && !urlutil.IsLocalDir(link) {
			_ = deleteAttachmentFileIfUnshared(r.Context(), h.companyFileInUse,
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

	sortOrderVal := intPtrOrNil(sortOrderStr)

	hash := computeAttachmentHash(h.companyAttachmentRoot(), filePath)
	if h.companyAttachmentDuplicateWarning(w, r, id, "", hash, 0, imported, filePath, notes, sortOrderStr) {
		return
	}

	supplierID, err := strconv.Atoi(id)
	if err == nil {
		err = h.attachmentTx(r.Context(), func(s *attachments.Service) error {
			if err := s.CreateCompanyAttachment(r.Context(), attachments.CompanyAttachment{SupplierID: supplierID,
				FilePath: filePath, Notes: notes, SortOrder: sortOrderVal, Hash: hash}); err != nil {
				return err
			}
			return s.EnsureCompanyPrimary(r.Context(), supplierID)
		})
	}
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
	id := r.PathValue("id")
	supplierID, err := strconv.Atoi(id)
	var attID int
	if err == nil {
		attID, err = strconv.Atoi(r.PathValue("attID"))
	}
	if err == nil {
		err = h.attachmentTx(r.Context(), func(s *attachments.Service) error {
			if err := s.DeleteCompanyAttachment(r.Context(), attID, supplierID); err != nil {
				return err
			}
			return s.EnsureCompanyPrimary(r.Context(), supplierID)
		})
	}
	if err != nil {
		h.renderError(w, r, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

func (h *Handler) SupplierAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	attID := r.PathValue("attID")
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

	sortOrderVal := intPtrOrNil(sortOrderStr)

	supplierID, err := strconv.Atoi(id)
	var oldFilePath string
	if err == nil {
		oldFilePath, err = h.attachments().CompanyAttachmentPath(r.Context(), attIDInt, supplierID)
	}
	if err != nil {
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
	att := attachments.CompanyAttachment{ID: attIDInt, SupplierID: supplierID, FilePath: newFilePath,
		Notes: notes, SortOrder: sortOrderVal, Hash: hash}
	if fileChanged {
		err = h.attachments().UpdateCompanyAttachmentFile(r.Context(), att)
	} else {
		err = h.attachments().UpdateCompanyAttachment(r.Context(), att)
	}
	if err != nil {
		h.renderError(w, r, "Error updating attachment: "+err.Error())
		return
	}

	if fileChanged && urlutil.IsLocalFile(oldFilePath) {
		if err := deleteAttachmentFileIfUnshared(r.Context(), h.companyFileInUse,
			attIDInt, oldFilePath, h.companyAttachmentRoot(), urlutil.StripLocalPrefix(oldFilePath)); err != nil {
			h.renderError(w, r, "Attachment updated, but the old file could not be removed: "+err.Error())
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) fetchSupplier(w http.ResponseWriter, r *http.Request, id string) (models.Supplier, bool) {
	n, _ := strconv.Atoi(id) // a non-numeric id reads as 0: not found
	su, err := h.purchasing().GetSupplier(r.Context(), n)
	if errors.Is(err, sql.ErrNoRows) {
		h.renderError(w, r, "Supplier not found")
		return models.Supplier{}, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier: "+err.Error())
		return models.Supplier{}, false
	}
	return models.Supplier{
		ID: su.ID, Name: su.Name, SupplierCode: su.SupplierCode, Notes: su.Notes,
		IsActive: su.IsActive, IsSupplier: su.IsSupplier, IsManufacturer: su.IsManufacturer,
		DefaultContact: su.DefaultContact, DateModified: su.DateModified,
		SupplierPartCount: su.SupplierPartCount, POCount: su.POCount,
		PrimaryAttachmentID: su.PrimaryAttachmentID,
		BulkOrderDelimiter:  su.BulkOrderDelimiter, BulkOrderPNSource: su.BulkOrderPNSource,
		DisplayName: su.ContactName, Phone1: su.ContactPhone, Email: su.ContactEmail, City: su.ContactCity,
	}, true
}

func (h *Handler) SupplierSetPrimaryAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idInt, _ := strconv.Atoi(id)
	attIDStr := r.FormValue("attachment_id")
	var val *int
	if n, err2 := strconv.Atoi(attIDStr); err2 == nil && n != 0 {
		val = &n
	}
	if err := h.attachments().SetCompanyPrimary(r.Context(), idInt, val); err != nil {
		h.renderError(w, r, "Error setting primary attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/supplier/%s/attachments", id), http.StatusFound)
}

// ── Supplier folder ──────────────────────────────────────────────────────────

func (h *Handler) renderSupplierFolder(w http.ResponseWriter, r *http.Request, s models.Supplier, subParts []string) {
	root := h.cfg().SupplierFilesRoot
	if root == "" {
		root = h.cfg().DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	if s.SupplierCode == "" {
		http.Error(w, "Supplier has no supplier code — cannot determine folder name", http.StatusBadRequest)
		return
	}

	base := filepath.Join(root, s.SupplierCode)
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

	dirName := s.SupplierCode
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
	id := r.PathValue("id")
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
	id := r.PathValue("id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	root := h.cfg().SupplierFilesRoot
	if root == "" {
		root = h.cfg().DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	if s.SupplierCode == "" {
		http.Error(w, "Supplier has no supplier code — cannot determine folder name", http.StatusBadRequest)
		return
	}
	base := filepath.Join(root, s.SupplierCode)
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
	id := r.PathValue("id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	h.setNavContext(w, r, fmt.Sprintf("/supplier/%d", s.ID), s.Name)
	h.renderSupplierFolder(w, r, s, nil)
}

func (h *Handler) SupplierFolderSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	id := r.PathValue("id")
	s, ok := h.fetchSupplier(w, r, id)
	if !ok {
		return
	}
	h.serveSupplierFile(w, r, s, id)
}

// serveSupplierFile holds SupplierFile's logic once the supplier row is in
// hand, split out so it can be exercised in tests without a DB (#863).
func (h *Handler) serveSupplierFile(w http.ResponseWriter, r *http.Request, s models.Supplier, id string) {
	root := h.cfg().SupplierFilesRoot
	if root == "" {
		root = h.cfg().DocControlRoot
	}
	if root == "" {
		http.Error(w, "SUPPLIER_FILES_ROOT is not configured", http.StatusServiceUnavailable)
		return
	}
	if s.SupplierCode == "" {
		http.Error(w, "Supplier has no supplier code", http.StatusBadRequest)
		return
	}

	base := filepath.Join(root, s.SupplierCode)
	prefix := fmt.Sprintf("/supplier/%s/file/", id)
	// A bare "/supplier/{id}/file" with no trailing slash still matches this
	// route's "{rest...}" wildcard (unlike chi's old "/supplier/{id}/file/*",
	// which required the literal slash) — reject it the same way chi did (#319).
	if !strings.HasPrefix(r.URL.Path, prefix) {
		h.NotFound(w, r)
		return
	}
	splat := strings.TrimPrefix(r.URL.Path, prefix)
	h.serveLocalizedFile(w, r, fileServingParams{Root: base, Splat: splat})
}

func supplierFromForm(r *http.Request) models.Supplier {
	s := models.Supplier{
		Name: fv(r, "name"), SupplierCode: fv(r, "supplier_code"),
		Notes:              fv(r, "notes"),
		IsActive:           r.FormValue("is_active") == "1",
		IsSupplier:         r.FormValue("is_supplier") == "1",
		IsManufacturer:     r.FormValue("is_manufacturer") == "1",
		BulkOrderDelimiter: bulkOrderDelimiterOrDefault(fv(r, "bulk_order_delimiter")),
		BulkOrderPNSource:  bulkOrderPNSourceOrDefault(fv(r, "bulk_order_pn_source")),
	}
	s.DefaultContact = intPtrOrNil(fv(r, "default_contact"))
	return s
}

// purchasingSupplierFromForm reads the supplier edit form's editable fields.
func purchasingSupplierFromForm(r *http.Request) purchasing.Supplier {
	return purchasing.Supplier{
		Name: fv(r, "name"), SupplierCode: fv(r, "supplier_code"),
		DefaultContact:     intPtrOrNil(fv(r, "default_contact")),
		IsActive:           r.FormValue("is_active") == "1",
		IsSupplier:         r.FormValue("is_supplier") == "1",
		IsManufacturer:     r.FormValue("is_manufacturer") == "1",
		Notes:              fv(r, "notes"),
		BulkOrderDelimiter: bulkOrderDelimiterOrDefault(fv(r, "bulk_order_delimiter")),
		BulkOrderPNSource:  bulkOrderPNSourceOrDefault(fv(r, "bulk_order_pn_source")),
	}
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

// intPtrOrNil parses s as an int for a nullable sqlc param: nil for empty or invalid input.
func intPtrOrNil(s string) *int {
	if n, err := strconv.Atoi(s); err == nil {
		return &n
	}
	return nil
}
