package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"arx/arxlib/folderpick"
	"arx/arxlib/urlutil"
)

func (h *Handler) APISupplierSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, []any{})
		return
	}
	supplierFilter := ""
	if r.URL.Query().Get("supplier_only") == "1" {
		supplierFilter = " AND su.is_supplier = 1"
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT su.id, su.name, cn.city
		FROM %s su
		LEFT JOIN %s cn ON su.default_contact = cn.id
		WHERE su.name LIKE @p1 AND su.is_active = 1`+supplierFilter+`
		ORDER BY su.name
		OFFSET 0 ROWS FETCH NEXT 20 ROWS ONLY
	`, h.cfg.CompanyTable(), h.cfg.ContactTable()), "%"+q+"%")
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	defer rows.Close()
	type result struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		City string `json:"city"`
	}
	var out []result
	for rows.Next() {
		var r result
		var name, city sql.NullString
		if rows.Scan(&r.ID, &name, &city) == nil {
			r.Name = name.String
			r.City = city.String
			out = append(out, r)
		}
	}
	writeJSON(w, out)
}

func (h *Handler) APISupplierContacts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, display_name, address, city, state, zipcode,
		       country, phone_1, fax, email
		FROM %s
		WHERE company_id = @p1 AND is_active = 1
		ORDER BY display_name
	`, h.cfg.ContactTable()), id)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	defer rows.Close()
	type result struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Address string `json:"address"`
		City    string `json:"city"`
		State   string `json:"state"`
		Zipcode string `json:"zipcode"`
		Country string `json:"country"`
		Phone   string `json:"phone"`
		Fax     string `json:"fax"`
		Email   string `json:"email"`
	}
	var out []result
	for rows.Next() {
		var c result
		var name, addr, city, state, zip, country, phone, fax, email sql.NullString
		if rows.Scan(&c.ID, &name, &addr, &city, &state, &zip, &country, &phone, &fax, &email) == nil {
			c.Name = name.String
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
	writeJSON(w, out)
}

func (h *Handler) APIPartSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, []any{})
		return
	}
	where := "part_number LIKE @p1"
	if r.URL.Query().Get("by") == "desc" {
		where = "title LIKE @p1 OR detail LIKE @p1"
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail FROM %s
		WHERE %s
		ORDER BY part_number
		OFFSET 0 ROWS FETCH NEXT 25 ROWS ONLY
	`, h.cfg.PartsTable(), where), "%"+q+"%")
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	defer rows.Close()
	type result struct {
		PNID       int    `json:"pnid"`
		PartNumber string `json:"part_number"`
		Revision   string `json:"revision"`
		Title      string `json:"title"`
		Detail     string `json:"detail"`
	}
	var out []result
	for rows.Next() {
		var p result
		var partNumber, revision, title, detail sql.NullString
		if rows.Scan(&p.PNID, &partNumber, &revision, &title, &detail) == nil {
			p.PartNumber = partNumber.String
			p.Revision = revision.String
			p.Title = title.String
			p.Detail = detail.String
			out = append(out, p)
		}
	}
	writeJSON(w, out)
}

// APISupplierPN returns the supplier_pn for a (part, supplier) pair.
// GET /api/supplier-part?part_id=X&supplier_id=Y
func (h *Handler) APISupplierPN(w http.ResponseWriter, r *http.Request) {
	partID := r.URL.Query().Get("part_id")
	supplierID := r.URL.Query().Get("supplier_id")
	if partID == "" || supplierID == "" {
		writeJSON(w, map[string]string{"supplier_pn": ""})
		return
	}
	var pn sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT TOP 1 supplier_pn
		FROM %s
		WHERE part_id = @p1 AND supplier_id = @p2
		ORDER BY preference ASC
	`, h.cfg.SupplierPartTable()), partID, supplierID).Scan(&pn)
	if err != nil {
		writeJSON(w, map[string]string{"supplier_pn": ""})
		return
	}
	writeJSON(w, map[string]string{"supplier_pn": pn.String})
}

// APIPartBOMChildren returns a part's direct BOM lines as JSON, used to
// lazily expand a sub-assembly row in the BOM view without a page reload.
func (h *Handler) APIPartBOMChildren(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	items, _, err := h.fetchBOMItems(r.Context(), id)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, items)
}

// APIBrowseFolder opens a native Windows folder-picker dialog and returns
// the selected path as JSON. Used by the Settings page browse buttons.
func (h *Handler) APIBrowseFolder(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"path": folderpick.BrowseFolderContext(r.Context())})
}

// APIBrowseFile opens a native Windows file-picker dialog and returns the
// selected absolute path as JSON. Used by the attachment Browse button.
func (h *Handler) APIBrowseFile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"path": folderpick.BrowseFileContext(r.Context())})
}

var pasteImageExts = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// decodePastedImage parses a "data:<mime>;base64,<data>" URL (as produced by
// FileReader.readAsDataURL in the clipboard-paste JS) into a file extension
// and the raw decoded bytes.
func decodePastedImage(dataURL string) (ext string, data []byte, err error) {
	commaIdx := strings.Index(dataURL, ",")
	if commaIdx < 0 || !strings.HasPrefix(dataURL, "data:") {
		return "", nil, fmt.Errorf("image_data must be a data: URL")
	}
	header := dataURL[len("data:"):commaIdx]
	mime := strings.TrimSuffix(header, ";base64")
	ext, ok := pasteImageExts[mime]
	if !ok {
		return "", nil, fmt.Errorf("Unsupported image type: %s", mime)
	}
	data, err = base64.StdEncoding.DecodeString(dataURL[commaIdx+1:])
	if err != nil {
		return "", nil, fmt.Errorf("Invalid base64 image data: %w", err)
	}
	return ext, data, nil
}

// decodeJSONBody applies the paste-image size cap and decodes the JSON
// request body into dst, writing a 400 response and returning false on
// failure. Shared by every clipboard-paste endpoint.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20) // 8MB cap
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return false
	}
	return true
}

// APIPartAttachmentName — GET /api/part/{id}/attachment-name?rev=&category=&ext=
// Returns the filename buildAttachmentFileName would produce, so the Browse
// live preview in part_attachments.html matches the saved name exactly (#558).
func (h *Handler) APIPartAttachmentName(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := h.fetchPartBasic(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Error loading part: "+err.Error())
		return
	}
	q := r.URL.Query()
	name := buildAttachmentFileName(p.PartNumber, q.Get("rev"), p.Title, q.Get("category"), q.Get("ext"))
	writeJSON(w, map[string]any{"name": name})
}

// APIPartPasteAttachment saves a clipboard-pasted image as a new part_attachment
// row with category "Photo". POST /api/part/{id}/paste-attachment.
func (h *Handler) APIPartPasteAttachment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if h.cfg.DocControlRoot == "" {
		writeJSONError(w, http.StatusBadRequest, "DOC_CONTROL_ROOT is not configured; cannot save pasted images.")
		return
	}

	var body struct {
		ImageData string `json:"image_data"`
		Rev       string `json:"rev"`
		OrderID   string `json:"order_id"`
		Comment   string `json:"comment"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}

	ext, data, err := decodePastedImage(body.ImageData)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	p, err := h.fetchPartBasic(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Error loading part: "+err.Error())
		return
	}

	name := buildAttachmentFileName(p.PartNumber, body.Rev, p.Title, "Photo", ext)
	finalName, err := writeIntoDocControlUnique(h.cfg.DocControlRoot, name, ext, data)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error saving image: "+err.Error())
		return
	}

	var oID any
	if n, err := strconv.Atoi(body.OrderID); err == nil {
		oID = n
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`INSERT INTO %s (part_id, file_name, part_revision, category, sort_order, comment) VALUES (@p1,@p2,@p3,@p4,@p5,@p6)`,
		h.cfg.AttachmentsTable(),
	), id, "LOCAL:"+finalName, body.Rev, "Photo", oID, body.Comment); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error adding attachment: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{"ok": true})
}

// APIPartPasteAttachmentReplace saves a clipboard-pasted image as the new
// file for an existing part_attachment row, replacing its current file.
// POST /api/part/{id}/attachments/{attID}/paste-attachment.
func (h *Handler) APIPartPasteAttachmentReplace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID, err := strconv.Atoi(chi.URLParam(r, "attID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid attachment id")
		return
	}
	if h.cfg.DocControlRoot == "" {
		writeJSONError(w, http.StatusBadRequest, "DOC_CONTROL_ROOT is not configured; cannot save pasted images.")
		return
	}

	var oldFileNameNS sql.NullString
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT file_name FROM %s WHERE id=@p1 AND part_id=@p2`, h.cfg.AttachmentsTable(),
	), attID, id).Scan(&oldFileNameNS); err != nil {
		if err == sql.ErrNoRows {
			writeJSONError(w, http.StatusNotFound, "Attachment not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Error loading attachment: "+err.Error())
		return
	}
	oldFileName := oldFileNameNS.String

	var body struct {
		ImageData string `json:"image_data"`
		Rev       string `json:"rev"`
		OrderID   string `json:"order_id"`
		Comment   string `json:"comment"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}

	ext, data, err := decodePastedImage(body.ImageData)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	p, err := h.fetchPartBasic(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Error loading part: "+err.Error())
		return
	}

	name := buildAttachmentFileName(p.PartNumber, body.Rev, p.Title, "Photo", ext)
	finalName, err := writeIntoDocControlUnique(h.cfg.DocControlRoot, name, ext, data)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error saving image: "+err.Error())
		return
	}

	var oID any
	if n, err := strconv.Atoi(body.OrderID); err == nil {
		oID = n
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET part_revision=@p1, category=@p2, sort_order=@p3, comment=@p4, file_name=@p5 WHERE id=@p6`,
		h.cfg.AttachmentsTable(),
	), body.Rev, "Photo", oID, body.Comment, "LOCAL:"+finalName, attID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error updating attachment: "+err.Error())
		return
	}

	if urlutil.IsLocalFile(oldFileName) {
		if err := h.deleteAttachmentFileIfUnshared(r.Context(), h.cfg.AttachmentsTable(), "id", "file_name",
			attID, oldFileName, h.cfg.DocControlRoot, urlutil.StripLocalPrefix(oldFileName)); err != nil {
			writeJSON(w, map[string]any{"ok": true, "warning": "Attachment updated, but the old file could not be removed: " + err.Error()})
			return
		}
	}

	writeJSON(w, map[string]any{"ok": true})
}

// APIRecordPasteResultImage saves a clipboard-pasted image to disk for a
// test-record result step (pf_type = "attach") and returns its filename.
// It does not touch test_result — the caller drops the returned filename into
// the step's result input, and the existing SaveResults handler persists it
// along with the rest of the record's edits.
// POST /api/record/{id}/step/{tid}/paste-image.
func (h *Handler) APIRecordPasteResultImage(w http.ResponseWriter, r *http.Request) {
	recordID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid record id")
		return
	}
	testID, err := strconv.Atoi(chi.URLParam(r, "tid"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid test id")
		return
	}
	if h.cfg.ImageRoot == "" {
		writeJSONError(w, http.StatusBadRequest, "IMAGE_ROOT is not configured; cannot save pasted images.")
		return
	}

	// Check the record before doing any decode work, so a locked/missing record
	// is rejected cheaply rather than after paying for the base64 decode.
	var serial, partNumber string
	var locked bool
	err = h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT r.serial_number, r.is_locked, pn.part_number
		FROM %s r
		JOIN %s f ON r.form_id = f.id
		JOIN %s pn ON f.part_number_id = pn.id
		WHERE r.id = @p1`,
		h.cfg.RecordsTable(), h.cfg.FormsTable(), h.cfg.PartsTable()), recordID).
		Scan(&serial, &locked, &partNumber)
	if err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error loading record: "+err.Error())
		return
	}
	if locked {
		writeJSONError(w, http.StatusConflict, "Record is locked")
		return
	}

	var body struct {
		ImageData string `json:"image_data"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}

	ext, data, err := decodePastedImage(body.ImageData)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// sanitizeFileNamePart here must match the folder name the "imageURL" and
	// "sanitizedPartNumber" template funcs build for display (render_tr.go), so
	// both sides of the write/read path use the same folder.
	dir := filepath.Join(h.cfg.ImageRoot, sanitizeFileNamePart(partNumber))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error creating image folder: "+err.Error())
		return
	}

	name := buildResultImageName(serial, recordID, testID, ext)
	existed, err := writeIntoDocControl(dir, name, data)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error saving image: "+err.Error())
		return
	}
	if existed {
		// buildResultImageName's uniqueness comes from a 1-second-resolution
		// timestamp; on the rare collision (e.g. a double-click), fail loudly
		// instead of silently keeping the old file but reporting success.
		writeJSONError(w, http.StatusConflict, "A file with this name was just created; please try again.")
		return
	}

	writeJSON(w, map[string]any{"ok": true, "filename": name})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if v == nil {
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(v)
}
