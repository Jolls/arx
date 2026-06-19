package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
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

	h.render(w, r, "mfg_parts.html", map[string]any{
		"Part":          p,
		"MfgParts":      mfgParts,
		"Manufacturers": manufacturers,
		"ActiveTab":     "parts", "ActiveSubTab": "mfg-parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── MfgPartCreate — POST /part/{id}/mfg-parts ───────────────────────────────

func (h *Handler) MfgPartCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mfgID := strings.TrimSpace(r.FormValue("mfg_id"))
	mpn := strings.TrimSpace(r.FormValue("mfg_part_number"))

	if mfgID == "" || mpn == "" {
		h.renderMfgPartsWithError(w, r, id, "Manufacturer and MPN are required")
		return
	}

	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (part_id, mfg_id, mfg_part_number, description, is_active)
		VALUES (@p1, @p2, @p3, @p4, 1)
	`, h.cfg.MfgPartTable()),
		id, mfgID, mpn, strings.TrimSpace(r.FormValue("description")),
	)
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

	var mp models.MfgPart
	var mpn, desc sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, part_id, mfg_id, mfg_part_number, description
		FROM %s WHERE id = @p1 AND part_id = @p2 AND is_active = 1
	`, h.cfg.MfgPartTable()), mid, id).Scan(
		&mp.ID, &mp.PartID, &mp.MfgID, &mpn, &desc,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Manufacturer part not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving manufacturer part: "+err.Error())
		return
	}
	mp.MfgPartNumber = mpn.String
	mp.Description = desc.String

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

	h.render(w, r, "mfg_parts.html", map[string]any{
		"Part":           p,
		"MfgParts":       mfgParts,
		"EditingMfgPart": &mp,
		"Manufacturers":  manufacturers,
		"ActiveTab":      "parts", "ActiveSubTab": "mfg-parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── MfgPartUpdate — POST /part/{id}/mfg-parts/{mid} ─────────────────────────

func (h *Handler) MfgPartUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mid := chi.URLParam(r, "mid")
	mfgID := strings.TrimSpace(r.FormValue("mfg_id"))
	mpn := strings.TrimSpace(r.FormValue("mfg_part_number"))

	if mfgID == "" || mpn == "" {
		h.renderMfgPartsWithError(w, r, id, "Manufacturer and MPN are required")
		return
	}

	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET mfg_id=@p1, mfg_part_number=@p2, description=@p3
		WHERE id=@p4 AND part_id=@p5 AND is_active=1
	`, h.cfg.MfgPartTable()),
		mfgID, mpn, strings.TrimSpace(r.FormValue("description")), mid, id,
	)
	if err != nil {
		h.renderError(w, r, "Error updating manufacturer part: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/mfg-parts", id), http.StatusFound)
}

// ── MfgPartDelete — POST /part/{id}/mfg-parts/{mid}/delete ──────────────────

func (h *Handler) MfgPartDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mid := chi.URLParam(r, "mid")

	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET is_active=0 WHERE id=@p1 AND part_id=@p2
	`, h.cfg.MfgPartTable()), mid, id)
	if err != nil {
		h.renderError(w, r, "Error deleting manufacturer part: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/mfg-parts", id), http.StatusFound)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) fetchMfgParts(r *http.Request, partID string) ([]models.MfgPart, error) {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT mp.id, mp.part_id, mp.mfg_id, mp.mfg_part_number, mp.description, c.name
		FROM %s mp
		JOIN %s c ON mp.mfg_id = c.id
		WHERE mp.part_id = @p1 AND mp.is_active = 1
		ORDER BY c.name, mp.mfg_part_number
	`, h.cfg.MfgPartTable(), h.cfg.CompanyTable()), partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.MfgPart
	for rows.Next() {
		var mp models.MfgPart
		var mpn, desc, mfgName sql.NullString
		if err := rows.Scan(&mp.ID, &mp.PartID, &mp.MfgID, &mpn, &desc, &mfgName); err != nil {
			return nil, err
		}
		mp.MfgPartNumber = mpn.String
		mp.Description = desc.String
		mp.MfgName = mfgName.String
		mp.IsActive = true
		list = append(list, mp)
	}
	return list, rows.Err()
}

type manufacturerOption struct {
	ID   int
	Name string
}

func (h *Handler) fetchManufacturers(r *http.Request) ([]manufacturerOption, error) {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, name FROM %s
		WHERE is_manufacturer = 1 AND is_active = 1
		ORDER BY name
	`, h.cfg.CompanyTable()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []manufacturerOption
	for rows.Next() {
		var m manufacturerOption
		var name sql.NullString
		if err := rows.Scan(&m.ID, &name); err != nil {
			return nil, err
		}
		m.Name = name.String
		list = append(list, m)
	}
	return list, rows.Err()
}

// renderMfgPartsWithError re-renders the mfg_parts page with an error message.
func (h *Handler) renderMfgPartsWithError(w http.ResponseWriter, r *http.Request, partID, errMsg string) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "mfg-parts")
	if !ok {
		return
	}
	mfgParts, _ := h.fetchMfgParts(r, partID)
	manufacturers, _ := h.fetchManufacturers(r)
	h.render(w, r, "mfg_parts.html", map[string]any{
		"Part":          p,
		"MfgParts":      mfgParts,
		"Manufacturers": manufacturers,
		"Error":         errMsg,
		"ActiveTab":     "parts", "ActiveSubTab": "mfg-parts",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}
