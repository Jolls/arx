package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // registers jpeg decoding for image.Decode (DigiKey photos, #62)
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/internal/attachments"
	"arx/internal/folderpick"
	"arx/internal/records"
	"arx/internal/urlutil"
)

func (h *Handler) APISupplierSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, []any{})
		return
	}
	matches, err := h.purchasing().SearchSuppliers(r.Context(), q, r.URL.Query().Get("supplier_only") == "1", 20)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	type result struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		City string `json:"city"`
	}
	out := make([]result, len(matches))
	for i, m := range matches {
		out[i] = result(m)
	}
	writeJSON(w, out)
}

func (h *Handler) APISupplierContacts(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id")) // a non-numeric id matches nothing
	contacts, err := h.contacts().ListActiveForCompany(r.Context(), id)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
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
	out := make([]result, len(contacts))
	for i, c := range contacts {
		out[i] = result{ID: c.ID, Name: c.DisplayName, Address: c.Address, City: c.City, State: c.State,
			Zipcode: c.Zipcode, Country: c.Country, Phone: c.Phone1, Fax: c.Fax, Email: c.Email}
	}
	writeJSON(w, out)
}

func (h *Handler) APIPartSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		writeJSON(w, []any{})
		return
	}
	matches, err := h.parts().SearchParts(r.Context(), q, r.URL.Query().Get("by") == "desc", 25)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	type result struct {
		ID          int    `json:"pnid"`
		PartNumber  string `json:"part_number"`
		Revision    string `json:"revision"`
		Description string `json:"description"`
		Detail      string `json:"detail"`
	}
	out := make([]result, len(matches))
	for i, m := range matches {
		out[i] = result(m)
	}
	writeJSON(w, out)
}

// APISupplierPN returns the supplier_pn, minimum order increment, and cheapest
// active unit price for a (part, supplier) pair, used to autofill a PO line
// (#76).
// GET /api/supplier-part?part_id=X&supplier_id=Y
func (h *Handler) APISupplierPN(w http.ResponseWriter, r *http.Request) {
	partID, err1 := strconv.Atoi(r.URL.Query().Get("part_id"))
	supplierID, err2 := strconv.Atoi(r.URL.Query().Get("supplier_id"))
	if err1 != nil || err2 != nil {
		writeJSON(w, map[string]string{"supplier_pn": ""})
		return
	}
	d, err := h.parts().GetSupplierPartDefaults(r.Context(), partID, supplierID)
	if err != nil {
		writeJSON(w, map[string]string{"supplier_pn": ""})
		return
	}

	resp := map[string]any{"supplier_pn": d.SupplierPN}
	if d.MinIncrement != nil && d.MinIncrement.IsPositive() {
		resp["min_increment"] = *d.MinIncrement
	}
	if d.PriceEA != nil {
		resp["price_ea"] = *d.PriceEA
	}
	writeJSON(w, resp)
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
	name := buildAttachmentFileName(p.PartNumber, q.Get("rev"), p.Description, q.Get("category"), q.Get("ext"))
	writeJSON(w, map[string]any{"name": name})
}

// APIPartPasteAttachment saves a clipboard-pasted image as a new part_attachment
// row with category "Photo". POST /api/part/{id}/paste-attachment.
func (h *Handler) APIPartPasteAttachment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if h.cfg().DocControlRoot == "" {
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

	name := buildAttachmentFileName(p.PartNumber, body.Rev, p.Description, "Photo", ext)
	finalName, err := writeIntoDocControlUnique(h.cfg().DocControlRoot, name, ext, data)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error saving image: "+err.Error())
		return
	}

	if err := h.insertAttachmentRow(r.Context(), id, "LOCAL:"+finalName, body.Rev, "Photo", intPtrOrNil(body.OrderID), body.Comment,
		nil, nil, hashBytes(data)); err != nil {
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
	if h.cfg().DocControlRoot == "" {
		writeJSONError(w, http.StatusBadRequest, "DOC_CONTROL_ROOT is not configured; cannot save pasted images.")
		return
	}

	partID, _ := strconv.Atoi(id)
	old, err := h.attachments().GetPartAttachment(r.Context(), attID, partID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSONError(w, http.StatusNotFound, "Attachment not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Error loading attachment: "+err.Error())
		return
	}
	oldFileName := old.FileName

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

	name := buildAttachmentFileName(p.PartNumber, body.Rev, p.Description, "Photo", ext)
	finalName, err := writeIntoDocControlUnique(h.cfg().DocControlRoot, name, ext, data)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error saving image: "+err.Error())
		return
	}

	if err := h.attachments().ReplacePartAttachmentPhoto(r.Context(), attachments.PartAttachment{ID: attID,
		PartRevision: body.Rev, Category: "Photo", SortOrder: intPtrOrNil(body.OrderID), Comment: body.Comment,
		FileName: "LOCAL:" + finalName, Hash: hashBytes(data)}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error updating attachment: "+err.Error())
		return
	}

	if urlutil.IsLocalFile(oldFileName) {
		if err := deleteAttachmentFileIfUnshared(r.Context(), h.partFileInUse,
			attID, oldFileName, h.cfg().DocControlRoot, urlutil.StripLocalPrefix(oldFileName)); err != nil {
			writeJSON(w, map[string]any{"ok": true, "warning": "Attachment updated, but the old file could not be removed: " + err.Error()})
			return
		}
	}

	writeJSON(w, map[string]any{"ok": true})
}

// Categories for the images generated from a PDF's first page (#696). Both are
// distinct from user-pasted "Photo" rows so regeneration can find and replace
// exactly these rows. previewCategory (large) surfaces in the part's Photos card;
// thumbnailCategory (small) drives the /parts part-number hover tooltip and is
// kept out of the Photos card.
const (
	previewCategory   = attachments.PreviewCategory
	thumbnailCategory = attachments.ThumbnailCategory
)

// isGeneratedCategory reports whether category is reserved for PDF-thumbnail
// generation (#696). The category field is otherwise free text (users can type
// any value via the Add/Edit Attachment form's custom-category option), so
// PartAttachmentCreate/PartAttachmentUpdate reject these two values on user
// submissions — otherwise a user's own attachment could later be silently
// overwritten and its file deleted the next time someone clicks "Generate
// Thumbnail" on an unrelated PDF for the same part, since upsertGeneratedAttachment
// finds its target by (part_id, category) alone.
func isGeneratedCategory(category string) bool {
	return category == previewCategory || category == thumbnailCategory
}

// generateThumbnailLocks serialises concurrent "Generate Thumbnail" requests for
// the same part (e.g. a double-click, or two requests racing) so the find-or-create
// logic in upsertGeneratedAttachment never runs twice in parallel for one part and
// can't create duplicate PDF Preview/Thumbnail rows.
var (
	generateThumbnailLocksMu sync.Mutex
	generateThumbnailLocks   = map[string]*sync.Mutex{}
)

func lockPartForThumbnail(partID string) func() {
	generateThumbnailLocksMu.Lock()
	mu, ok := generateThumbnailLocks[partID]
	if !ok {
		mu = &sync.Mutex{}
		generateThumbnailLocks[partID] = mu
	}
	generateThumbnailLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// APIPartGenerateThumbnail renders page 1 of a local PDF attachment into two
// images — a large "PDF Preview" (which surfaces in the part's Photos card) and a
// small "Thumbnail" (which drives the /parts hover tooltip) — each stored as its
// own part_attachment row. Re-running edits those rows in place rather than
// creating duplicates. POST /api/part/{id}/attachments/{attID}/generate-thumbnail.
func (h *Handler) APIPartGenerateThumbnail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID, err := strconv.Atoi(chi.URLParam(r, "attID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid attachment id")
		return
	}
	if h.cfg().DocControlRoot == "" {
		writeJSONError(w, http.StatusBadRequest, "DOC_CONTROL_ROOT is not configured; cannot generate thumbnails.")
		return
	}

	partID, _ := strconv.Atoi(id)
	src, err := h.attachments().GetActivePartAttachment(r.Context(), attID, partID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSONError(w, http.StatusNotFound, "Attachment not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Error loading attachment: "+err.Error())
		return
	}
	srcFile := src.FileName
	rev := src.PartRevision
	if !urlutil.IsLocalFile(srcFile) || urlutil.IsLocalDir(srcFile) || !urlutil.IsPDF(urlutil.FileBaseName(srcFile)) {
		writeJSONError(w, http.StatusBadRequest, "Thumbnails can only be generated from a local PDF attachment.")
		return
	}

	path, ok := safePath(h.cfg().DocControlRoot, urlutil.LocalFileURL(srcFile, ""))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid attachment path.")
		return
	}
	pdfBytes, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error reading PDF: "+err.Error())
		return
	}

	page, err := renderPDFFirstPage(pdfBytes, 150)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error rendering PDF: "+err.Error())
		return
	}

	p, err := h.fetchPartBasic(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Error loading part: "+err.Error())
		return
	}

	unlock := lockPartForThumbnail(id)
	defer unlock()

	for _, spec := range []struct {
		category string
		maxPx    int
	}{{previewCategory, 800}, {thumbnailCategory, 250}} {
		data, err := encodePNG(resizeLongEdge(page, spec.maxPx))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Error encoding image: "+err.Error())
			return
		}
		if err := h.saveGeneratedAttachment(r.Context(), id, rev, spec.category, p.PartNumber, p.Description, data); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	writeJSON(w, map[string]any{"ok": true})
}

// upsertGeneratedAttachment points the part's single active row of the given
// generated category (PDF Preview / Thumbnail) at newFile: it edits the existing
// row in place — preserving its id so any primary-attachment pointer stays valid —
// or inserts one if none exists. Callers must hold lockPartForThumbnail(partID) so
// the find-or-create check below can't race with another request for the same
// part. partID is the URL string form used elsewhere in this file.
func (h *Handler) upsertGeneratedAttachment(ctx context.Context, partID, rev, category, newFile, hash string) error {
	pid, err := strconv.Atoi(partID)
	if err != nil {
		return err
	}
	svc := h.attachments()
	existing, err := svc.GetGeneratedAttachment(ctx, pid, category)
	if err == sql.ErrNoRows {
		return svc.CreateGeneratedAttachment(ctx, attachments.PartAttachment{PartID: pid, FileName: newFile,
			PartRevision: rev, Category: category, Hash: hash})
	}
	if err != nil {
		return err
	}
	existingID := existing.ID
	if err := svc.UpdateGeneratedAttachment(ctx, attachments.PartAttachment{ID: existingID, FileName: newFile,
		PartRevision: rev, Hash: hash}); err != nil {
		return err
	}
	// The DB row is already correctly repointed at newFile at this point, so a
	// failure removing the now-superseded old file is a cleanup miss, not a
	// request failure — matches APIPartPasteAttachmentReplace's soft-warning
	// treatment of the same failure mode instead of hard-failing the request.
	oldFile := existing.FileName
	if urlutil.IsLocalFile(oldFile) && oldFile != newFile {
		if err := deleteAttachmentFileIfUnshared(ctx, h.partFileInUse,
			existingID, oldFile, h.cfg().DocControlRoot, urlutil.StripLocalPrefix(oldFile)); err != nil {
			log.Printf("[thumbnail] part %s: attachment %d updated, but old file %q could not be removed: %v", partID, existingID, oldFile, err)
		}
	}
	return nil
}

// saveGeneratedAttachment writes data as a PNG for partID/category, then
// upserts the attachment row via upsertGeneratedAttachment. Regenerating
// produces the same name as last time (same part/rev/description/category),
// so it replaces that file in place rather than writing a fresh
// "(2)"-suffixed copy and deleting the original out from under it (#839).
// Callers must hold lockPartForThumbnail(partID), same as upsertGeneratedAttachment.
func (h *Handler) saveGeneratedAttachment(ctx context.Context, partID, rev, category, partNumber, description string, data []byte) error {
	name := buildAttachmentFileName(partNumber, rev, description, category, ".png")

	pid, _ := strconv.Atoi(partID)
	existing, err := h.attachments().GetGeneratedAttachment(ctx, pid, category)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("Error loading existing attachment: %w", err)
	}

	finalName := name
	if urlutil.IsLocalFile(existing.FileName) && strings.EqualFold(urlutil.StripLocalPrefix(existing.FileName), name) {
		if err := replaceDocControlData(h.cfg().DocControlRoot, name, data); err != nil {
			return fmt.Errorf("Error saving image: %w", err)
		}
	} else {
		var err error
		finalName, err = writeIntoDocControlUnique(h.cfg().DocControlRoot, name, ".png", data)
		if err != nil {
			return fmt.Errorf("Error saving image: %w", err)
		}
	}
	if err := h.upsertGeneratedAttachment(ctx, partID, rev, category, "LOCAL:"+finalName, hashBytes(data)); err != nil {
		return fmt.Errorf("Error saving attachment: %w", err)
	}
	return nil
}

// generateThumbnailFromPhoto builds a "Thumbnail" attachment (#62) from an
// already-downloaded DigiKey photo, mirroring APIPartGenerateThumbnail's
// PDF-page path but starting from image bytes instead of a rendered PDF page.
// part is passed in rather than re-fetched: the caller (SupplierPartCreate,
// via prepareDigiKeyFiles) already looked it up earlier in the same request.
// Called after the supplier-link transaction has already committed, so
// unlike the rest of the DigiKey import this is best-effort: a failure here
// must not undo a supplier link that already saved successfully, so it only
// logs instead of surfacing an error to the user.
func (h *Handler) generateThumbnailFromPhoto(ctx context.Context, partID string, part models.Part, photoData []byte) {
	if h.cfg().DocControlRoot == "" {
		return
	}
	img, _, err := image.Decode(bytes.NewReader(photoData))
	if err != nil {
		log.Printf("[thumbnail] part %s: could not decode DigiKey photo for thumbnail: %v", partID, err)
		return
	}
	data, err := encodePNG(resizeLongEdge(img, 250))
	if err != nil {
		log.Printf("[thumbnail] part %s: could not encode DigiKey thumbnail: %v", partID, err)
		return
	}

	unlock := lockPartForThumbnail(partID)
	defer unlock()

	if err := h.saveGeneratedAttachment(ctx, partID, "", thumbnailCategory, part.PartNumber, part.Description, data); err != nil {
		log.Printf("[thumbnail] part %s: could not save DigiKey thumbnail: %v", partID, err)
	}
}

// APIRecordPasteResultImage saves a clipboard-pasted image to disk for a
// test-record result step (pf_type = "attach") and returns its filename.
// It does not touch result — the caller drops the returned filename into
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
	if h.cfg().ImageRoot == "" {
		writeJSONError(w, http.StatusBadRequest, "IMAGE_ROOT is not configured; cannot save pasted images.")
		return
	}

	// Check the record before doing any decode work, so a locked/missing record
	// is rejected cheaply rather than after paying for the base64 decode.
	rec, err := records.New(handlerDB{h}).GetHeader(r.Context(), recordID)
	serial, partNumber, locked := rec.Serial, rec.PartNumber, rec.Locked
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
	dir := filepath.Join(h.cfg().ImageRoot, sanitizeFileNamePart(partNumber))
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

// APIDigiKeyLookup — GET /api/digikey/lookup?pn=<DigiKey PN> — fetches product
// metadata to autofill the Add Supplier form on the Sourcing tab (#27). Never
// writes anything; the user's Save on that form is the only commit point.
func (h *Handler) APIDigiKeyLookup(w http.ResponseWriter, r *http.Request) {
	pn := strings.TrimSpace(r.URL.Query().Get("pn"))
	if pn == "" {
		writeJSONError(w, http.StatusBadRequest, "DigiKey part number is required")
		return
	}

	result, err := h.fetchDigiKeyProduct(r.Context(), pn)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	out := map[string]any{
		"supplier_desc":   result.SupplierDesc,
		"lead_time":       result.LeadTime,
		"mfg_part_number": result.MfgPartNumber,
		"mfg_name":        result.MfgName,
		"datasheet_url":   result.DatasheetURL,
		"photo_url":       result.PhotoURL,
		"prices":          result.Prices,
	}
	if result.MinIncrement != nil {
		out["min_increment"] = *result.MinIncrement
	}
	// Matching result.MfgName against an existing company is left to the
	// caller: the Sourcing page already has the full manufacturer list
	// rendered into the <select>'s <option>s, so the client can do the
	// case-insensitive match against that instead of a second DB round trip.

	writeJSON(w, out)
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
