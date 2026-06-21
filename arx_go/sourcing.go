package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
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
	h.render(w, r, "part_sourcing.html", map[string]any{
		"Part":             p,
		"Links":            links,
		"PricesBySupplier": h.fetchActivePricesBySupplier(r, id),
		"Suppliers":        h.fetchSuppliersOnly(r),
		"Units":            units,
		"ActiveTab": "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── SupplierPartCreate — POST /part/{id}/suppliers ───────────────────────────

func (h *Handler) SupplierPartCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	supplierID := strings.TrimSpace(r.FormValue("supplier_id"))
	if supplierID == "" {
		h.renderSourcingWithError(w, r, id, "Supplier is required")
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (supplier_id, part_id, preference, supplier_pn, supplier_desc, lead_time, min_increment, unit_id)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)
	`, h.cfg.SupplierPartTable()),
		supplierID, id,
		strings.TrimSpace(r.FormValue("preference")),
		strings.TrimSpace(r.FormValue("supplier_pn")),
		strings.TrimSpace(r.FormValue("supplier_desc")),
		strings.TrimSpace(r.FormValue("lead_time")),
		nullableFloat(r.FormValue("min_increment")),
		nullableInt(r.FormValue("unit_id")),
	)
	if err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// ── SupplierPartEdit — GET /part/{id}/suppliers/{spID}/edit ──────────────────

func (h *Handler) SupplierPartEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	spID := chi.URLParam(r, "spID")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "suppliers")
	if !ok {
		return
	}
	var sp models.SupplierPart
	var pref, supplierPN, supplierDesc, leadTime sql.NullString
	var minIncr sql.NullFloat64
	var unitID sql.NullInt64
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, supplier_id, part_id, preference, supplier_pn, supplier_desc, lead_time, min_increment, unit_id
		FROM %s WHERE id = @p1 AND part_id = @p2
	`, h.cfg.SupplierPartTable()), spID, id).Scan(
		&sp.ID, &sp.SupplierID, &sp.PartID, &pref, &supplierPN, &supplierDesc, &leadTime, &minIncr, &unitID,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Supplier link not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier link: "+err.Error())
		return
	}
	sp.Preference = pref.String
	sp.SupplierPN = supplierPN.String
	sp.SupplierDesc = supplierDesc.String
	sp.LeadTime = leadTime.String
	if minIncr.Valid {
		sp.MinIncrement = &minIncr.Float64
	}
	if unitID.Valid {
		v := int(unitID.Int64)
		sp.UnitID = &v
	}

	links, err := h.fetchSupplierLinks(r, id)
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier links: "+err.Error())
		return
	}
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "part_sourcing.html", map[string]any{
		"Part":        p,
		"Links":       links,
		"EditingLink": &sp,
		"Suppliers":   h.fetchSuppliersOnly(r),
		"Units":       units,
		"ActiveTab":   "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── SupplierPartUpdate — POST /part/{id}/suppliers/{spID} ────────────────────

func (h *Handler) SupplierPartUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	spID := chi.URLParam(r, "spID")
	supplierID := strings.TrimSpace(r.FormValue("supplier_id"))
	if supplierID == "" {
		h.renderSourcingWithError(w, r, id, "Supplier is required")
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET supplier_id=@p1, preference=@p2, supplier_pn=@p3, supplier_desc=@p4,
		              lead_time=@p5, min_increment=@p6, unit_id=@p7
		WHERE id=@p8 AND part_id=@p9
	`, h.cfg.SupplierPartTable()),
		supplierID,
		strings.TrimSpace(r.FormValue("preference")),
		strings.TrimSpace(r.FormValue("supplier_pn")),
		strings.TrimSpace(r.FormValue("supplier_desc")),
		strings.TrimSpace(r.FormValue("lead_time")),
		nullableFloat(r.FormValue("min_increment")),
		nullableInt(r.FormValue("unit_id")),
		spID, id,
	)
	if err != nil {
		h.renderError(w, r, "Error updating supplier link: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// ── SupplierPartDelete — POST /part/{id}/suppliers/{spID}/delete ─────────────

func (h *Handler) SupplierPartDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	spID := chi.URLParam(r, "spID")
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		DELETE FROM %s WHERE id=@p1 AND part_id=@p2
	`, h.cfg.SupplierPartTable()), spID, id)
	if err != nil {
		h.renderError(w, r, "Error deleting supplier link: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) fetchSupplierLinks(r *http.Request, partID string) ([]models.SupplierPart, error) {
	sp, co, ut, pn := h.cfg.SupplierPartTable(), h.cfg.CompanyTable(), h.cfg.UnitTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT sp.id, sp.supplier_id, sp.part_id, sp.preference, sp.supplier_pn, sp.supplier_desc,
		       sp.lead_time, sp.min_increment, sp.unit_id,
		       c.name AS supplier_name,
		       COALESCE(pu.abbreviation, bu.abbreviation) AS effective_unit,
		       CASE WHEN sp.unit_id IS NOT NULL THEN 1 ELSE 0 END AS unit_is_explicit
		FROM %s sp
		JOIN %s c  ON sp.supplier_id = c.id
		LEFT JOIN %s pu ON sp.unit_id  = pu.unit_id
		LEFT JOIN %s p  ON sp.part_id  = p.id
		LEFT JOIN %s bu ON p.unit_id   = bu.unit_id
		WHERE sp.part_id = @p1
		ORDER BY c.name, sp.supplier_pn
	`, sp, co, ut, pn, ut), partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.SupplierPart
	for rows.Next() {
		var lk models.SupplierPart
		var pref, supplierPN, supplierDesc, leadTime, supplierName, unitAbbr sql.NullString
		var minIncr sql.NullFloat64
		var unitID sql.NullInt64
		var unitIsExplicit bool
		if err := rows.Scan(
			&lk.ID, &lk.SupplierID, &lk.PartID, &pref, &supplierPN, &supplierDesc,
			&leadTime, &minIncr, &unitID,
			&supplierName, &unitAbbr, &unitIsExplicit,
		); err != nil {
			return nil, err
		}
		lk.Preference = pref.String
		lk.SupplierPN = supplierPN.String
		lk.SupplierDesc = supplierDesc.String
		lk.LeadTime = leadTime.String
		lk.SupplierName = supplierName.String
		lk.PurchaseUnitAbbr = unitAbbr.String
		lk.PurchaseUnitIsExplicit = unitIsExplicit
		if minIncr.Valid {
			lk.MinIncrement = &minIncr.Float64
		}
		if unitID.Valid {
			v := int(unitID.Int64)
			lk.UnitID = &v
		}
		list = append(list, lk)
	}
	return list, rows.Err()
}

// fetchActivePricesBySupplier returns active prices for a part keyed by supplier_id.
func (h *Handler) fetchActivePricesBySupplier(r *http.Request, partID string) map[int][]models.Price {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT supplier_id, price_ea, pack_size, effective_date
		FROM %s
		WHERE part_id = @p1 AND is_active = 1
		ORDER BY supplier_id, pack_size
	`, h.cfg.PriceTable()), partID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[int][]models.Price{}
	for rows.Next() {
		var suppID sql.NullInt64
		var priceEA, packSize sql.NullFloat64
		var effDate sql.NullTime
		if rows.Scan(&suppID, &priceEA, &packSize, &effDate) != nil || !suppID.Valid {
			continue
		}
		p := models.Price{}
		if priceEA.Valid {
			p.PriceEA = &priceEA.Float64
		}
		if packSize.Valid {
			p.PackSize = &packSize.Float64
		}
		if effDate.Valid {
			p.EffectiveDate = &effDate.Time
		}
		out[int(suppID.Int64)] = append(out[int(suppID.Int64)], p)
	}
	return out
}

// fetchSuppliersOnly returns active companies flagged as suppliers, for dropdowns.
func (h *Handler) fetchSuppliersOnly(r *http.Request) []supplierOption {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, name FROM %s
		WHERE is_supplier = 1 AND is_active = 1
		ORDER BY name
	`, h.cfg.CompanyTable()))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var list []supplierOption
	for rows.Next() {
		var s supplierOption
		var name sql.NullString
		if rows.Scan(&s.ID, &name) == nil {
			s.Name = name.String
			list = append(list, s)
		}
	}
	return list
}

func (h *Handler) renderSourcingWithError(w http.ResponseWriter, r *http.Request, partID, errMsg string) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "suppliers")
	if !ok {
		return
	}
	links, _ := h.fetchSupplierLinks(r, partID)
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "part_sourcing.html", map[string]any{
		"Part":      p,
		"Links":     links,
		"Suppliers": h.fetchSuppliersOnly(r),
		"Units":     units,
		"Error":     errMsg,
		"ActiveTab": "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// nullableFloat returns nil for empty/unparseable strings, otherwise the float64 value.
func nullableFloat(s string) any {
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
