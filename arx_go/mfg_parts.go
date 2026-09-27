package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"arx/internal/parts"
)

// ── PartMfgParts — GET /part/{id}/mfg-parts ─────────────────────────────────

func (h *Handler) PartMfgParts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "mfg-parts")
	if !ok {
		return
	}

	mfgParts, err := h.fetchMfgParts(r, id)
	if err != nil {
		h.renderError(w, r, "Error retrieving manufacturer parts: "+err.Error())
		return
	}

	manufacturers, err := h.fetchManufacturers(r)
	if err != nil {
		h.renderError(w, r, "Error retrieving manufacturers: "+err.Error())
		return
	}

	h.render(w, r, "parts/mfg_parts.html", map[string]any{
		"Part":              p,
		"MfgParts":          mfgParts,
		"Manufacturers":     manufacturers,
		"AttachmentsByLink": h.fetchAttachmentsByVendor(r, id, mfgScopeCol),
		"ActiveTab":         "parts", "ActiveSubTab": "mfg-parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── MfgPartCreate — POST /part/{id}/mfg-parts ───────────────────────────────

func (h *Handler) MfgPartCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "mfg-parts")
	if !ok {
		return
	}
	mfgID := strings.TrimSpace(r.FormValue("mfg_id"))
	mpn := strings.TrimSpace(r.FormValue("mfg_part_number"))

	if mfgID == "" || mpn == "" {
		h.renderMfgPartsWithError(w, r, id, "Manufacturer and MPN are required")
		return
	}

	mfg, err := strconv.Atoi(mfgID)
	if err == nil {
		err = h.parts().CreateMfgPart(r.Context(), parts.MfgPart{
			PartID: p.ID, MfgID: mfg, MfgPartNumber: mpn, Description: strings.TrimSpace(r.FormValue("description")),
		})
	}
	if err != nil {
		h.renderMfgPartsWithError(w, r, id, "Error adding manufacturer part: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/mfg-parts", id), http.StatusFound)
}

// ── MfgPartEdit — GET /part/{id}/mfg-parts/{mid}/edit ───────────────────────

func (h *Handler) MfgPartEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mid := chi.URLParam(r, "mid")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "mfg-parts")
	if !ok {
		return
	}

	mfgPartID, err := strconv.Atoi(mid)
	if err != nil {
		h.renderError(w, r, "Manufacturer part not found")
		return
	}
	mp, err := h.parts().GetMfgPart(r.Context(), mfgPartID, p.ID)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Manufacturer part not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving manufacturer part: "+err.Error())
		return
	}

	mfgParts, err := h.fetchMfgParts(r, id)
	if err != nil {
		h.renderError(w, r, "Error retrieving manufacturer parts: "+err.Error())
		return
	}

	manufacturers, err := h.fetchManufacturers(r)
	if err != nil {
		h.renderError(w, r, "Error retrieving manufacturers: "+err.Error())
		return
	}

	h.render(w, r, "parts/mfg_parts.html", map[string]any{
		"Part":              p,
		"MfgParts":          mfgParts,
		"EditingMfgPart":    &mp,
		"Manufacturers":     manufacturers,
		"AttachmentsByLink": h.fetchAttachmentsByVendor(r, id, mfgScopeCol),
		"ActiveTab":         "parts", "ActiveSubTab": "mfg-parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── MfgPartUpdate — POST /part/{id}/mfg-parts/{mid} ─────────────────────────

func (h *Handler) MfgPartUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "mfg-parts")
	if !ok {
		return
	}
	mfgPartID, err := strconv.Atoi(chi.URLParam(r, "mid"))
	if err != nil {
		h.renderError(w, r, "Manufacturer part not found")
		return
	}
	mfgID := strings.TrimSpace(r.FormValue("mfg_id"))
	mpn := strings.TrimSpace(r.FormValue("mfg_part_number"))

	if mfgID == "" || mpn == "" {
		h.renderMfgPartsWithError(w, r, id, "Manufacturer and MPN are required")
		return
	}

	mfg, err := strconv.Atoi(mfgID)
	if err == nil {
		err = h.parts().UpdateMfgPart(r.Context(), parts.MfgPart{
			ID: mfgPartID, PartID: p.ID, MfgID: mfg, MfgPartNumber: mpn,
			Description: strings.TrimSpace(r.FormValue("description")),
		})
	}
	if err != nil {
		h.renderError(w, r, "Error updating manufacturer part: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/mfg-parts", id), http.StatusFound)
}

// ── MfgPartDelete — POST /part/{id}/mfg-parts/{mid}/delete ──────────────────

func (h *Handler) MfgPartDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "mfg-parts")
	if !ok {
		return
	}
	mfgPartID, err := strconv.Atoi(chi.URLParam(r, "mid"))
	if err != nil {
		h.renderError(w, r, "Manufacturer part not found")
		return
	}

	if err := h.parts().DeleteMfgPart(r.Context(), mfgPartID, p.ID); err != nil {
		h.renderError(w, r, "Error deleting manufacturer part: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/mfg-parts", id), http.StatusFound)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) fetchMfgParts(r *http.Request, partID string) ([]parts.MfgPart, error) {
	id, err := strconv.Atoi(partID)
	if err != nil {
		return nil, err
	}
	return h.parts().ListMfgParts(r.Context(), id)
}

func (h *Handler) fetchManufacturers(r *http.Request) ([]parts.Manufacturer, error) {
	return h.parts().ListManufacturers(r.Context())
}

// renderMfgPartsWithError re-renders the mfg_parts page with an error message.
func (h *Handler) renderMfgPartsWithError(w http.ResponseWriter, r *http.Request, partID, errMsg string) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "mfg-parts")
	if !ok {
		return
	}
	mfgParts, _ := h.fetchMfgParts(r, partID)
	manufacturers, _ := h.fetchManufacturers(r)
	h.render(w, r, "parts/mfg_parts.html", map[string]any{
		"Part":              p,
		"MfgParts":          mfgParts,
		"Manufacturers":     manufacturers,
		"AttachmentsByLink": h.fetchAttachmentsByVendor(r, partID, mfgScopeCol),
		"Error":             errMsg,
		"ActiveTab":         "parts", "ActiveSubTab": "mfg-parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}
