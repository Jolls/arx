package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/internal/parts"
)

// ── PartSourcing — GET /part/{id}/suppliers ──────────────────────────────────

func (h *Handler) PartSourcing(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "suppliers")
	if !ok {
		return
	}
	links, err := h.fetchSupplierLinks(r, id)
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier links: "+err.Error())
		return
	}
	units, _ := h.fetchUnits(r.Context())
	digiKeyEnabled := h.cfg().DigiKeyEnabled()
	var manufacturers []parts.Manufacturer
	if digiKeyEnabled {
		// Only fetched for the DigiKey manufacturer picker — skip the query
		// entirely on every other Sourcing tab render.
		manufacturers, _ = h.fetchManufacturers(r)
	}
	h.render(w, r, "parts/part_sourcing.html", map[string]any{
		"Part":              p,
		"Links":             links,
		"PricesBySupplier":  h.fetchActivePricesBySupplier(r, id),
		"AttachmentsByLink": h.fetchAttachmentsByVendor(r, id, supplierScopeCol),
		"Units":             units,
		"Manufacturers":     manufacturers,
		"DigiKeyEnabled":    digiKeyEnabled,
		"ActiveTab":         "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── SupplierPartCreate — POST /part/{id}/suppliers ───────────────────────────

// SupplierPartCreate adds a supplier link. When the Add Supplier form was
// filled via the DigiKey lookup (issue #27), the optional dk_* fields carry
// prices/datasheet/photo/manufacturer data the user chose to import; those
// are written in the same transaction as the supplier link so a failure
// midway never leaves a supplier link with half-imported data.
func (h *Handler) SupplierPartCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "suppliers")
	if !ok {
		return
	}
	supplierIDStr := strings.TrimSpace(r.FormValue("supplier_id"))
	if supplierIDStr == "" {
		h.renderSourcingWithError(w, r, id, "Supplier is required", nil, supplierPartFromForm(r))
		return
	}
	supplierID, err := strconv.Atoi(supplierIDStr)
	if err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error(), nil, supplierPartFromForm(r))
		return
	}

	// Downloaded and written to DOC_CONTROL_ROOT before the transaction opens
	// (#62): each fetch can block for up to digikeyFileTimeout, and doing that
	// while holding a DB transaction open would tie up a pool connection for
	// no reason — the file writes aren't part of the SQL transaction anyway.
	preparedFiles, importedPart, err := h.prepareDigiKeyFiles(r.Context(), r, id)
	if err != nil {
		h.renderSourcingWithError(w, r, id, err.Error(), nil, supplierPartFromForm(r))
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error(), nil, supplierPartFromForm(r))
		return
	}
	defer tx.Rollback()

	sp := supplierPartFromForm(r)
	sp.PartID = p.ID
	if err := parts.New(tx).CreateSupplierPart(r.Context(), *sp); err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error(), nil, sp)
		return
	}

	pricesInserted, photoForThumbnail, err := h.applyDigiKeyImportExtras(r, tx, p.ID, supplierID, preparedFiles)
	if err != nil {
		h.renderSourcingWithError(w, r, id, err.Error(), nil, supplierPartFromForm(r))
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error(), nil, supplierPartFromForm(r))
		return
	}
	// Matches the trigger PriceCreate/PriceEdit use: fire only when a price
	// row actually landed, not merely when the user checked "import prices".
	if pricesInserted {
		h.ensureDefaultSupplier(r.Context(), p.ID, supplierID)
	}
	// Best-effort, like ensureDefaultSupplier above: runs after the supplier
	// link is already committed, so a thumbnail failure must not undo it.
	// importedPart is always populated here: photoForThumbnail is only set
	// when the Photo entry in preparedFiles was, which only happens after
	// prepareDigiKeyFiles has fetched the part.
	if photoForThumbnail != nil {
		h.generateThumbnailFromPhoto(r.Context(), id, *importedPart, photoForThumbnail)
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// supplierPartFromForm rebuilds a SupplierPart from submitted form values so a
// failed create/update can redisplay what the user typed instead of losing it.
func supplierPartFromForm(r *http.Request) *parts.SupplierPart {
	sp := &parts.SupplierPart{
		SupplierPN:   strings.TrimSpace(r.FormValue("supplier_pn")),
		SupplierDesc: strings.TrimSpace(r.FormValue("supplier_desc")),
		LeadTime:     strings.TrimSpace(r.FormValue("lead_time")),
		SupplierName: strings.TrimSpace(r.FormValue("supplier_name")),
	}
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("supplier_id"))); err == nil {
		sp.SupplierID = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("preference"))); err == nil {
		sp.Preference = &v
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("min_increment")), 64); err == nil {
		sp.MinIncrement = &v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("unit_id"))); err == nil {
		sp.UnitID = &v
	}
	return sp
}

// digikeyPreparedFile is a DigiKey datasheet/photo already downloaded and
// written into DOC_CONTROL_ROOT by prepareDigiKeyFiles, ready for its
// part_attachment row to be inserted once a transaction is open. data carries
// the raw bytes only for category "Photo", so generateThumbnailFromPhoto can
// build a Thumbnail from it without fetching the URL a second time. hash
// (#71) is computed from the downloaded bytes for every category, since data
// itself is discarded for anything other than Photo.
type digikeyPreparedFile struct {
	category string
	fileName string // "LOCAL:<name>"
	data     []byte
	hash     string
}

// prepareDigiKeyFiles downloads the datasheet/photo the user chose to import
// (issue #27) and copies them into DOC_CONTROL_ROOT like every other
// attachment (#62) rather than storing DigiKey's bare remote URL — a remote
// file_name isn't recognised as local by urlutil.IsLocalFile, so it silently
// never showed up in the part detail page's Photos card, and a remote
// "Datasheet" couldn't be used with Generate Thumbnail either.
//
// Runs before SupplierPartCreate opens its DB transaction: each download can
// block for up to digikeyFileTimeout, and the file writes aren't part of the
// SQL transaction anyway, so there's no reason to hold a transaction open
// across them. Returns the part it had to look up (for buildAttachmentFileName),
// or nil if neither dk_import_datasheet nor dk_import_photo was requested.
func (h *Handler) prepareDigiKeyFiles(ctx context.Context, r *http.Request, partID string) (files []digikeyPreparedFile, part *models.Part, err error) {
	for _, imp := range []struct{ flag, urlField, category, defaultExt string }{
		{"dk_import_datasheet", "dk_datasheet_url", "Datasheet", ".pdf"},
		{"dk_import_photo", "dk_photo_url", "Photo", ".jpg"},
	} {
		if r.FormValue(imp.flag) != "1" {
			continue
		}
		fileURL := strings.TrimSpace(r.FormValue(imp.urlField))
		if fileURL == "" {
			continue
		}
		if h.cfg().DocControlRoot == "" {
			return nil, nil, fmt.Errorf("DOC_CONTROL_ROOT is not configured; cannot import DigiKey files")
		}
		if part == nil {
			fetched, ferr := h.fetchPartBasic(ctx, partID)
			if ferr != nil {
				return nil, nil, fmt.Errorf("could not load part for DigiKey import: %w", ferr)
			}
			part = &fetched
		}
		data, ferr := fetchDigiKeyFile(ctx, fileURL)
		if ferr != nil {
			return nil, nil, fmt.Errorf("could not download DigiKey %s: %w", strings.ToLower(imp.category), ferr)
		}
		ext := imp.defaultExt
		if u, perr := url.Parse(fileURL); perr == nil {
			if e := path.Ext(u.Path); e != "" {
				ext = e
			}
		}
		name := buildAttachmentFileName(part.PartNumber, "", part.Description, imp.category, ext)
		finalName, werr := writeIntoDocControlUnique(h.cfg().DocControlRoot, name, ext, data)
		if werr != nil {
			return nil, nil, fmt.Errorf("could not save imported %s: %w", strings.ToLower(imp.category), werr)
		}
		pf := digikeyPreparedFile{category: imp.category, fileName: "LOCAL:" + finalName, hash: hashBytes(data)}
		if imp.category == "Photo" {
			pf.data = data
		}
		files = append(files, pf)
	}
	return files, part, nil
}

// applyDigiKeyImportExtras writes the optional DigiKey-imported price breaks,
// datasheet/photo attachment rows (already downloaded by prepareDigiKeyFiles),
// and manufacturer link submitted alongside a new supplier link. A no-op when
// none of the dk_* fields are present, so a plain (non-imported) Add Supplier
// submit is unaffected. Returns whether at least one price row was actually
// inserted (as opposed to skipped as a pre-existing active price), which the
// caller uses to decide whether to run ensureDefaultSupplier.
// photoForThumbnail is the raw bytes of a "dk_import_photo" download, returned
// only when the user also checked "dk_generate_thumbnail" — the caller uses it
// to build the /parts hover-tooltip Thumbnail after the transaction commits.
func (h *Handler) applyDigiKeyImportExtras(r *http.Request, tx *txLogger, partID, supplierID int, preparedFiles []digikeyPreparedFile) (pricesInserted bool, photoForThumbnail []byte, err error) {
	ctx := r.Context()
	svc := parts.New(tx)

	if r.FormValue("dk_import_prices") == "1" {
		var breaks []digikeyPriceBreak
		if raw := r.FormValue("dk_prices_json"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &breaks); err != nil {
				return false, nil, fmt.Errorf("could not read imported prices: %w", err)
			}
		}
		effectiveDate := h.userNow(r).Format("2006-01-02")
		for _, b := range breaks {
			// An active price already at this pack size is left alone (inserted=false).
			inserted, err := svc.ImportPrice(ctx, partID, supplierID, b.BreakQuantity, b.UnitPrice, b.TotalPrice, effectiveDate)
			if err != nil {
				return false, nil, fmt.Errorf("could not save imported price: %w", err)
			}
			pricesInserted = pricesInserted || inserted
		}
	}

	for _, pf := range preparedFiles {
		if err := svc.CreateImportedAttachment(ctx, partID, pf.fileName, pf.category, "Imported from DigiKey", pf.hash); err != nil {
			return false, nil, fmt.Errorf("could not save imported %s: %w", strings.ToLower(pf.category), err)
		}
		if pf.category == "Photo" && r.FormValue("dk_generate_thumbnail") == "1" {
			photoForThumbnail = pf.data
		}
	}

	mfgChoice := strings.TrimSpace(r.FormValue("dk_mfg_choice"))
	mfgPartNumber := strings.TrimSpace(r.FormValue("dk_mfg_part_number"))
	if mfgChoice != "" && mfgChoice != "skip" && mfgPartNumber != "" {
		var mfgID int
		if mfgChoice == "create" {
			mfgName := strings.TrimSpace(r.FormValue("dk_mfg_name"))
			if mfgName == "" {
				return pricesInserted, nil, fmt.Errorf("manufacturer name is required to create a new manufacturer")
			}
			newID, err := svc.CreateManufacturer(ctx, mfgName)
			if err == parts.ErrCompanyNameTaken {
				return pricesInserted, nil, fmt.Errorf("a company named %q already exists — pick it from the manufacturer list instead", mfgName)
			}
			if err != nil {
				return pricesInserted, nil, fmt.Errorf("could not create manufacturer: %w", err)
			}
			mfgID = newID
		} else if mfgID, err = strconv.Atoi(mfgChoice); err != nil {
			return pricesInserted, nil, fmt.Errorf("could not save manufacturer part: %w", err)
		}
		// An MPN the part already has from this manufacturer is left alone.
		if err := svc.ImportMfgPart(ctx, partID, mfgID, mfgPartNumber); err != nil {
			return pricesInserted, nil, fmt.Errorf("could not save manufacturer part: %w", err)
		}
	}

	return pricesInserted, photoForThumbnail, nil
}

// ── SupplierPartEdit — GET /part/{id}/suppliers/{spID}/edit ──────────────────

func (h *Handler) SupplierPartEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	spID := chi.URLParam(r, "spID")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "suppliers")
	if !ok {
		return
	}
	spIDInt, err := strconv.Atoi(spID)
	if err != nil {
		h.renderError(w, r, "Supplier link not found")
		return
	}
	sp, err := h.parts().GetSupplierPart(r.Context(), spIDInt, p.ID)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Supplier link not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier link: "+err.Error())
		return
	}

	links, err := h.fetchSupplierLinks(r, id)
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier links: "+err.Error())
		return
	}
	units, _ := h.fetchUnits(r.Context())
	digiKeyEnabled := h.cfg().DigiKeyEnabled()
	var manufacturers []parts.Manufacturer
	if digiKeyEnabled {
		manufacturers, _ = h.fetchManufacturers(r)
	}
	h.render(w, r, "parts/part_sourcing.html", map[string]any{
		"Part":              p,
		"Links":             links,
		"EditingLink":       &sp,
		"PricesBySupplier":  h.fetchActivePricesBySupplier(r, id),
		"AttachmentsByLink": h.fetchAttachmentsByVendor(r, id, supplierScopeCol),
		"Units":             units,
		"Manufacturers":     manufacturers,
		"DigiKeyEnabled":    digiKeyEnabled,
		"ActiveTab":         "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── SupplierPartUpdate — POST /part/{id}/suppliers/{spID} ────────────────────

func (h *Handler) SupplierPartUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "suppliers")
	if !ok {
		return
	}
	spIDInt, spIDErr := strconv.Atoi(chi.URLParam(r, "spID"))
	fail := func(msg string) {
		draft := supplierPartFromForm(r)
		draft.ID = spIDInt
		h.renderSourcingWithError(w, r, id, msg, draft, nil)
	}
	supplierIDStr := strings.TrimSpace(r.FormValue("supplier_id"))
	if supplierIDStr == "" {
		fail("Supplier is required")
		return
	}
	if spIDErr != nil {
		fail("Error updating supplier link: " + spIDErr.Error())
		return
	}
	supplierID, err := strconv.Atoi(supplierIDStr)
	if err != nil {
		fail("Error updating supplier link: " + err.Error())
		return
	}
	// Mirrors SupplierPartCreate: downloaded and written to DOC_CONTROL_ROOT
	// before the transaction opens (#62), since the fetch isn't part of the
	// SQL transaction anyway.
	preparedFiles, importedPart, err := h.prepareDigiKeyFiles(r.Context(), r, id)
	if err != nil {
		fail(err.Error())
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		fail("Error updating supplier link: " + err.Error())
		return
	}
	defer tx.Rollback()

	sp := supplierPartFromForm(r)
	sp.ID, sp.PartID = spIDInt, p.ID
	if err := parts.New(tx).UpdateSupplierPart(r.Context(), *sp); err != nil {
		fail("Error updating supplier link: " + err.Error())
		return
	}

	pricesInserted, photoForThumbnail, err := h.applyDigiKeyImportExtras(r, tx, p.ID, supplierID, preparedFiles)
	if err != nil {
		fail(err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		fail("Error updating supplier link: " + err.Error())
		return
	}
	if pricesInserted {
		h.ensureDefaultSupplier(r.Context(), p.ID, supplierID)
	}
	if photoForThumbnail != nil {
		h.generateThumbnailFromPhoto(r.Context(), id, *importedPart, photoForThumbnail)
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// ── SupplierPartDelete — POST /part/{id}/suppliers/{spID}/delete ─────────────

func (h *Handler) SupplierPartDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "suppliers")
	if !ok {
		return
	}
	spID, err := strconv.Atoi(chi.URLParam(r, "spID"))
	if err == nil {
		err = h.parts().DeleteSupplierPart(r.Context(), spID, p.ID)
	}
	if err != nil {
		h.renderError(w, r, "Error deleting supplier link: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) fetchSupplierLinks(r *http.Request, partID string) ([]parts.SupplierPart, error) {
	id, err := strconv.Atoi(partID)
	if err != nil {
		return nil, err
	}
	return h.parts().ListSupplierParts(r.Context(), id)
}

// fetchActivePricesBySupplier returns active prices for a part keyed by
// supplier_id, or nil on error.
func (h *Handler) fetchActivePricesBySupplier(r *http.Request, partID string) map[int][]parts.Price {
	id, err := strconv.Atoi(partID)
	if err != nil {
		return nil
	}
	out, _ := h.parts().ActivePricesBySupplier(r.Context(), id)
	return out
}

// renderSourcingWithError re-renders the sourcing page in place after a failed
// create/update, so the user's input isn't lost. editing repopulates the Edit
// Supplier form (an in-progress edit of an existing link); draft repopulates
// the Add Supplier form. At most one of the two is non-nil.
func (h *Handler) renderSourcingWithError(w http.ResponseWriter, r *http.Request, partID, errMsg string, editing, draft *parts.SupplierPart) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "suppliers")
	if !ok {
		return
	}
	links, _ := h.fetchSupplierLinks(r, partID)
	units, _ := h.fetchUnits(r.Context())
	digiKeyEnabled := h.cfg().DigiKeyEnabled()
	var manufacturers []parts.Manufacturer
	if digiKeyEnabled {
		manufacturers, _ = h.fetchManufacturers(r)
	}
	h.render(w, r, "parts/part_sourcing.html", map[string]any{
		"Part":              p,
		"Links":             links,
		"EditingLink":       editing,
		"AddDraft":          draft,
		"PricesBySupplier":  h.fetchActivePricesBySupplier(r, partID),
		"AttachmentsByLink": h.fetchAttachmentsByVendor(r, partID, supplierScopeCol),
		"Units":             units,
		"Manufacturers":     manufacturers,
		"DigiKeyEnabled":    digiKeyEnabled,
		"Error":             errMsg,
		"ActiveTab":         "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// nullableFloat returns nil for empty/unparseable strings, otherwise the float64 value.
func nullableFloat(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// resolvePriceFields fills in a missing price_ea/price_pack from the other
// using pack_size, when only one was submitted (#75).
func resolvePriceFields(r *http.Request) (priceEA, pricePack *float64) {
	priceEA = nullableFloat(r.FormValue("price_ea"))
	pricePack = nullableFloat(r.FormValue("price_pack"))
	packSize, err := strconv.ParseFloat(r.FormValue("pack_size"), 64)
	if err != nil || packSize <= 0 {
		return priceEA, pricePack
	}
	if priceEA == nil && pricePack != nil {
		v := *pricePack / packSize
		priceEA = &v
	} else if pricePack == nil && priceEA != nil {
		v := *priceEA * packSize
		pricePack = &v
	}
	return priceEA, pricePack
}
