package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	digiKeyEnabled := h.cfg.DigiKeyEnabled()
	var manufacturers []manufacturerOption
	if digiKeyEnabled {
		// Only fetched for the DigiKey manufacturer picker — skip the query
		// entirely on every other Sourcing tab render.
		manufacturers, _ = h.fetchManufacturers(r)
	}
	h.render(w, r, "parts/part_sourcing.html", map[string]any{
		"Part":             p,
		"Links":            links,
		"PricesBySupplier": h.fetchActivePricesBySupplier(r, id),
		"Units":            units,
		"Manufacturers":    manufacturers,
		"DigiKeyEnabled":   digiKeyEnabled,
		"ActiveTab":        "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
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
	if _, ok := h.requireTab(w, r, id, "suppliers"); !ok {
		return
	}
	supplierID := strings.TrimSpace(r.FormValue("supplier_id"))
	if supplierID == "" {
		h.renderSourcingWithError(w, r, id, "Supplier is required", nil, supplierPartFromForm(r))
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error(), nil, supplierPartFromForm(r))
		return
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (supplier_id, part_id, preference, supplier_pn, supplier_desc, lead_time, min_increment, uom_id)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)
	`, h.cfg.SupplierPartTable()),
		supplierID, id,
		nullableInt(r.FormValue("preference")),
		strings.TrimSpace(r.FormValue("supplier_pn")),
		strings.TrimSpace(r.FormValue("supplier_desc")),
		strings.TrimSpace(r.FormValue("lead_time")),
		nullableFloat(r.FormValue("min_increment")),
		nullableInt(r.FormValue("unit_id")),
	)
	if err != nil {
		h.renderSourcingWithError(w, r, id, "Error adding supplier link: "+err.Error(), nil, supplierPartFromForm(r))
		return
	}

	pricesInserted, err := h.applyDigiKeyImportExtras(r, tx, id, supplierID)
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
		h.ensureDefaultSupplier(r.Context(), id, supplierID)
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// supplierPartFromForm rebuilds a SupplierPart from submitted form values so a
// failed create/update can redisplay what the user typed instead of losing it.
func supplierPartFromForm(r *http.Request) *models.SupplierPart {
	sp := &models.SupplierPart{
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

// applyDigiKeyImportExtras writes the optional DigiKey-imported price breaks,
// datasheet/photo attachments, and manufacturer link submitted alongside a new
// supplier link. A no-op when none of the dk_* fields are present, so a plain
// (non-imported) Add Supplier submit is unaffected. Returns whether at least
// one price row was actually inserted (as opposed to skipped as a pre-existing
// active price), which the caller uses to decide whether to run
// ensureDefaultSupplier.
func (h *Handler) applyDigiKeyImportExtras(r *http.Request, tx *txLogger, partID, supplierID string) (pricesInserted bool, err error) {
	ctx := r.Context()

	if r.FormValue("dk_import_prices") == "1" {
		var breaks []digikeyPriceBreak
		if raw := r.FormValue("dk_prices_json"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &breaks); err != nil {
				return false, fmt.Errorf("could not read imported prices: %w", err)
			}
		}
		effectiveDate := time.Now().Format("2006-01-02")
		for _, b := range breaks {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
				INSERT INTO %s (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
				VALUES (@p1, @p2, @p3, @p4, @p5, @p6, %s)
			`, h.cfg.PriceTable(), h.dia().BoolLiteral(true)),
				partID, supplierID, b.BreakQuantity, b.UnitPrice, b.TotalPrice, effectiveDate,
			); err != nil {
				if strings.Contains(err.Error(), "UQ_price") {
					continue // an active price already exists at this pack size; leave it alone
				}
				return false, fmt.Errorf("could not save imported price: %w", err)
			}
			pricesInserted = true
		}
	}

	for _, imp := range []struct{ flag, urlField, category string }{
		{"dk_import_datasheet", "dk_datasheet_url", "Datasheet"},
		{"dk_import_photo", "dk_photo_url", "Photo"},
	} {
		if r.FormValue(imp.flag) != "1" {
			continue
		}
		fileURL := strings.TrimSpace(r.FormValue(imp.urlField))
		if fileURL == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (part_id, file_name, part_revision, category, comment) VALUES (@p1,@p2,@p3,@p4,@p5)`,
			h.cfg.AttachmentsTable(),
		), partID, fileURL, "", imp.category, "Imported from DigiKey"); err != nil {
			return false, fmt.Errorf("could not save imported %s: %w", strings.ToLower(imp.category), err)
		}
	}

	mfgChoice := strings.TrimSpace(r.FormValue("dk_mfg_choice"))
	mfgPartNumber := strings.TrimSpace(r.FormValue("dk_mfg_part_number"))
	if mfgChoice != "" && mfgChoice != "skip" && mfgPartNumber != "" {
		mfgID := mfgChoice
		if mfgChoice == "create" {
			mfgName := strings.TrimSpace(r.FormValue("dk_mfg_name"))
			if mfgName == "" {
				return pricesInserted, fmt.Errorf("manufacturer name is required to create a new manufacturer")
			}
			insertMfg := h.dia().InsertReturningID(h.cfg.CompanyTable(),
				`name, is_supplier, is_manufacturer`, `@p1,@p2,@p3`, false)
			var newID int
			if err := tx.QueryRowContext(ctx, insertMfg, mfgName, false, true).Scan(&newID); err != nil {
				if strings.Contains(err.Error(), "UQ_company_name") {
					return pricesInserted, fmt.Errorf("a company named %q already exists — pick it from the manufacturer list instead", mfgName)
				}
				return pricesInserted, fmt.Errorf("could not create manufacturer: %w", err)
			}
			mfgID = strconv.Itoa(newID)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (part_id, mfg_id, mfg_part_number, is_active) VALUES (@p1,@p2,@p3,%s)`,
			h.cfg.MfgPartTable(), h.dia().BoolLiteral(true),
		), partID, mfgID, mfgPartNumber); err != nil && !strings.Contains(err.Error(), "UQ_mfg_part") {
			return pricesInserted, fmt.Errorf("could not save manufacturer part: %w", err)
		}
	}

	return pricesInserted, nil
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
	var pref sql.NullInt64
	var supplierPN, supplierDesc, leadTime, supplierName sql.NullString
	var minIncr sql.NullFloat64
	var unitID sql.NullInt64
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT sp.id, sp.supplier_id, sp.part_id, sp.preference, sp.supplier_pn, sp.supplier_desc,
		       sp.lead_time, sp.min_increment, sp.uom_id, c.name
		FROM %s sp
		JOIN %s c ON sp.supplier_id = c.id
		WHERE sp.id = @p1 AND sp.part_id = @p2
	`, h.cfg.SupplierPartTable(), h.cfg.CompanyTable()), spID, id).Scan(
		&sp.ID, &sp.SupplierID, &sp.PartID, &pref, &supplierPN, &supplierDesc, &leadTime, &minIncr, &unitID, &supplierName,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Supplier link not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving supplier link: "+err.Error())
		return
	}
	if pref.Valid {
		v := int(pref.Int64)
		sp.Preference = &v
	}
	sp.SupplierPN = supplierPN.String
	sp.SupplierDesc = supplierDesc.String
	sp.LeadTime = leadTime.String
	sp.SupplierName = supplierName.String
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
	h.render(w, r, "parts/part_sourcing.html", map[string]any{
		"Part":             p,
		"Links":            links,
		"EditingLink":      &sp,
		"PricesBySupplier": h.fetchActivePricesBySupplier(r, id),
		"Units":            units,
		"ActiveTab":        "parts", "ActiveSubTab": "suppliers",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── SupplierPartUpdate — POST /part/{id}/suppliers/{spID} ────────────────────

func (h *Handler) SupplierPartUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.requireTab(w, r, id, "suppliers"); !ok {
		return
	}
	spID := chi.URLParam(r, "spID")
	spIDInt, _ := strconv.Atoi(spID)
	supplierID := strings.TrimSpace(r.FormValue("supplier_id"))
	if supplierID == "" {
		draft := supplierPartFromForm(r)
		draft.ID = spIDInt
		h.renderSourcingWithError(w, r, id, "Supplier is required", draft, nil)
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET supplier_id=@p1, preference=@p2, supplier_pn=@p3, supplier_desc=@p4,
		              lead_time=@p5, min_increment=@p6, uom_id=@p7
		WHERE id=@p8 AND part_id=@p9
	`, h.cfg.SupplierPartTable()),
		supplierID,
		nullableInt(r.FormValue("preference")),
		strings.TrimSpace(r.FormValue("supplier_pn")),
		strings.TrimSpace(r.FormValue("supplier_desc")),
		strings.TrimSpace(r.FormValue("lead_time")),
		nullableFloat(r.FormValue("min_increment")),
		nullableInt(r.FormValue("unit_id")),
		spID, id,
	)
	if err != nil {
		draft := supplierPartFromForm(r)
		draft.ID = spIDInt
		h.renderSourcingWithError(w, r, id, "Error updating supplier link: "+err.Error(), draft, nil)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/suppliers", id), http.StatusFound)
}

// ── SupplierPartDelete — POST /part/{id}/suppliers/{spID}/delete ─────────────

func (h *Handler) SupplierPartDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.requireTab(w, r, id, "suppliers"); !ok {
		return
	}
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
	sp, co, ut, pn := h.cfg.SupplierPartTable(), h.cfg.CompanyTable(), h.cfg.UomTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT sp.id, sp.supplier_id, sp.part_id, sp.preference, sp.supplier_pn, sp.supplier_desc,
		       sp.lead_time, sp.min_increment, sp.uom_id,
		       c.name AS supplier_name,
		       COALESCE(pu.abbreviation, bu.abbreviation) AS effective_unit,
		       %s AS unit_is_explicit
		FROM %s sp
		JOIN %s c  ON sp.supplier_id = c.id
		LEFT JOIN %s pu ON sp.uom_id   = pu.uom_id
		LEFT JOIN %s p  ON sp.part_id  = p.id
		LEFT JOIN %s bu ON p.uom_id    = bu.uom_id
		WHERE sp.part_id = @p1
		ORDER BY c.name, sp.supplier_pn
	`, h.dia().BoolFromCondition("sp.uom_id IS NOT NULL"), sp, co, ut, pn, ut), partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.SupplierPart
	for rows.Next() {
		var lk models.SupplierPart
		var pref sql.NullInt64
		var supplierPN, supplierDesc, leadTime, supplierName, unitAbbr sql.NullString
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
		if pref.Valid {
			v := int(pref.Int64)
			lk.Preference = &v
		}
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
		WHERE part_id = @p1 AND is_active = %s
		ORDER BY supplier_id, pack_size
	`, h.cfg.PriceTable(), h.dia().BoolLiteral(true)), partID)
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

// renderSourcingWithError re-renders the sourcing page in place after a failed
// create/update, so the user's input isn't lost. editing repopulates the Edit
// Supplier form (an in-progress edit of an existing link); draft repopulates
// the Add Supplier form. At most one of the two is non-nil.
func (h *Handler) renderSourcingWithError(w http.ResponseWriter, r *http.Request, partID, errMsg string, editing, draft *models.SupplierPart) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "suppliers")
	if !ok {
		return
	}
	links, _ := h.fetchSupplierLinks(r, partID)
	units, _ := h.fetchUnits(r.Context())
	digiKeyEnabled := h.cfg.DigiKeyEnabled()
	var manufacturers []manufacturerOption
	if digiKeyEnabled {
		manufacturers, _ = h.fetchManufacturers(r)
	}
	h.render(w, r, "parts/part_sourcing.html", map[string]any{
		"Part":             p,
		"Links":            links,
		"EditingLink":      editing,
		"AddDraft":         draft,
		"PricesBySupplier": h.fetchActivePricesBySupplier(r, partID),
		"Units":            units,
		"Manufacturers":    manufacturers,
		"DigiKeyEnabled":   digiKeyEnabled,
		"Error":            errMsg,
		"ActiveTab":        "parts", "ActiveSubTab": "suppliers",
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
