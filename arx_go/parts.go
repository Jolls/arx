package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
)

// ── Shared helpers ──────────────────────────────────────────────────────────

func (h *Handler) fetchPartBasic(ctx context.Context, id string) (models.Part, error) {
	var p models.Part
	var partNumber, title, category sql.NullString
	var hasBOM sql.NullBool
	var filIDPrimary sql.NullInt64
	var stockOnHand sql.NullFloat64
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, part_number, title, category, has_bom, primary_attachment_id, stock_on_hand FROM %s WHERE id = @p1`,
		h.cfg.PartsTable(),
	), id).Scan(&p.PNID, &partNumber, &title, &category, &hasBOM, &filIDPrimary, &stockOnHand)
	p.PartNumber = partNumber.String
	p.Title = title.String
	p.Category = category.String
	p.HasBOM = hasBOM.Bool
	p.PNFILIDPrimary = int(filIDPrimary.Int64)
	p.StockOnHand = stockOnHand.Float64
	return p, err
}

func (h *Handler) partPageBase(w http.ResponseWriter, r *http.Request, id, subTab string) (models.Part, string, string, bool) {
	p, err := h.fetchPartBasic(r.Context(), id)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Part not found")
		return models.Part{}, "", "", false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return models.Part{}, "", "", false
	}
	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.PNID), p.PartNumber)
	h.applyCategoryTabs(r.Context(), &p)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	return p, backURL, backLabel, true
}

func fv(r *http.Request, key string) string { return strings.TrimSpace(r.FormValue(key)) }

// ── PartsList — GET / ───────────────────────────────────────────────────────

func (h *Handler) PartsList(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "index.html", map[string]any{
		"ActiveTab": "parts", "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) PartsRows(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	type row struct {
		ID       int    `json:"id"`
		PN       string `json:"pn"`
		Rev      string `json:"rev"`
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		ReqBy    string `json:"reqBy"`
		Date     string `json:"date"`
		Cat      string `json:"cat"`
		Modified string `json:"modified"`
		Active   bool   `json:"active"`
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail,
		       requested_by, created_date, category, modified_date, is_active
		FROM %s ORDER BY part_number
	`, h.cfg.PartsTable()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := make([]row, 0)
	for rows.Next() {
		var p row
		var pn, rev, title, detail, reqBy, cat sql.NullString
		var date, modified sql.NullTime
		var active sql.NullBool
		if err := rows.Scan(&p.ID, &pn, &rev, &title, &detail, &reqBy, &date, &cat, &modified, &active); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		p.PN = pn.String
		p.Active = !active.Valid || active.Bool
		p.Rev = rev.String
		p.Title = title.String
		p.Detail = detail.String
		p.ReqBy = reqBy.String
		p.Cat = cat.String
		if date.Valid {
			p.Date = date.Time.Format("2006-01-02")
		}
		if modified.Valid {
			p.Modified = modified.Time.Format("2006-01-02")
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[rows] parts: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

// ── PartDetail — GET /part/{id} and /part/{id}/details ──────────────────────

func (h *Handler) PartDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var p models.Part
	var (
		partNumber, revision, title, detail, category sql.NullString
		status, reqBy, notes                          sql.NullString
		user1, user2, user3, user4, user5             sql.NullString
		user6, user7, user8, user9, user10            sql.NullString
		pnDate, pnDateModified, lastRollupAt          sql.NullTime
		active, hasBOM                                sql.NullBool
		filIDPrimary, filLinks, poLinks               sql.NullInt64
		currentCost, lastRollupCost                   sql.NullFloat64
		stockOnHand                                   sql.NullFloat64
		unitID                                        sql.NullInt64
	)
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail, category, has_bom,
		       release_status, is_active, requested_by, notes,
		       created_date, modified_date, primary_attachment_id,
		       current_cost, last_rollup_cost, last_rollup_at, attachment_count, po_line_count,
		       unit_id, stock_on_hand,
		       user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		       user_field_6, user_field_7, user_field_8, user_field_9, user_field_10
		FROM %s WHERE id = @p1
	`, h.cfg.PartsTable()), id).Scan(
		&p.PNID, &partNumber, &revision, &title, &detail, &category, &hasBOM,
		&status, &active, &reqBy, &notes,
		&pnDate, &pnDateModified, &filIDPrimary,
		&currentCost, &lastRollupCost, &lastRollupAt, &filLinks, &poLinks,
		&unitID, &stockOnHand,
		&user1, &user2, &user3, &user4, &user5,
		&user6, &user7, &user8, &user9, &user10,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Part not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return
	}

	p.PartNumber = partNumber.String
	p.Revision = revision.String
	p.Title = title.String
	p.Detail = detail.String
	p.Category = category.String
	p.HasBOM = hasBOM.Bool
	p.ReleaseStatus = status.String
	p.Active = active.Bool
	p.PNReqBy = reqBy.String
	p.PNNotes = notes.String
	p.PNFILIDPrimary = int(filIDPrimary.Int64)
	p.StockOnHand = stockOnHand.Float64
	p.PNCurrentCost = currentCost.Float64
	p.PNLastRollupCost = lastRollupCost.Float64
	if lastRollupAt.Valid {
		p.PNLastRollupAt = &lastRollupAt.Time
	}
	p.PNFILLinks = int(filLinks.Int64)
	p.PNPOLinks = int(poLinks.Int64)
	p.UserField1, p.UserField2, p.UserField3, p.UserField4, p.UserField5 = user1.String, user2.String, user3.String, user4.String, user5.String
	p.UserField6, p.UserField7, p.UserField8, p.UserField9, p.UserField10 = user6.String, user7.String, user8.String, user9.String, user10.String
	if pnDate.Valid {
		p.PNDate = &pnDate.Time
	}
	if pnDateModified.Valid {
		p.PNDateModified = &pnDateModified.Time
	}
	if unitID.Valid {
		v := int(unitID.Int64)
		p.UnitID = &v
		var abbr sql.NullString
		h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT abbreviation FROM %s WHERE unit_id = @p1`, h.cfg.UnitTable(),
		), v).Scan(&abbr)
		p.UnitAbbr = abbr.String
	}

	if p.HasBOM && r.URL.Path == fmt.Sprintf("/part/%s", id) {
		http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusFound)
		return
	}

	var primaryAtt *models.Attachment
	if p.PNFILIDPrimary > 0 {
		var att models.Attachment
		var fname, fnotes, frev sql.NullString
		if err := h.queryRowContext(r.Context(), fmt.Sprintf(
			`SELECT id, file_name, category, part_revision FROM %s WHERE id = @p1`,
			h.cfg.AttachmentsTable(),
		), p.PNFILIDPrimary).Scan(&att.FILID, &fname, &fnotes, &frev); err == nil {
			att.FILFileName = fname.String
			att.Category = fnotes.String
			att.FILPNRev = frev.String
			primaryAtt = &att
		}
	}

	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.PNID), p.PartNumber)
	h.applyCategoryTabs(r.Context(), &p)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)

	var rollupDelta, rollupDeltaPct float64
	var rollupSignificant bool
	if p.PNLastRollupAt != nil && p.PNCurrentCost > 0 {
		rollupDelta = p.PNLastRollupCost - p.PNCurrentCost
		rollupDeltaPct = rollupDelta / p.PNCurrentCost * 100
		rollupSignificant = math.Abs(rollupDeltaPct) >= 5.0
	}

	h.render(w, r, "part_detail.html", map[string]any{
		"Part": p, "PrimaryAtt": primaryAtt,
		"ActiveTab": "parts", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode":          h.cfg.TestMode,
		"RollupDelta":       rollupDelta,
		"RollupDeltaPct":    rollupDeltaPct,
		"RollupSignificant": rollupSignificant,
	})
}

// ── PartsNew — GET /parts/new ───────────────────────────────────────────────

func (h *Handler) PartsNew(w http.ResponseWriter, r *http.Request) {
	p := models.Part{}
	if u := h.currentUser(r); u != nil {
		p.PNReqBy = u.DisplayName
	}
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "part_edit.html", map[string]any{
		"Part": p, "IsNew": true,
		"Units": units, "Categories": h.loadCategories(r.Context()),
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── PartsCreate — POST /parts ───────────────────────────────────────────────

func (h *Handler) PartsCreate(w http.ResponseWriter, r *http.Request) {
	partNumber := fv(r, "part_number")
	if partNumber == "" {
		h.render(w, r, "part_edit.html", map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Part Number is required",
			"Categories": h.loadCategories(r.Context()),
			"ActiveTab":  "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	now := time.Now()
	var newID int
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (part_number, revision, title, detail, category, has_bom,
		                release_status, is_active, requested_by, notes, created_date, modified_date,
		                unit_id,
		                user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		                user_field_6, user_field_7, user_field_8, user_field_9, user_field_10)
		OUTPUT INSERTED.id
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,
		        @p13,
		        @p14,@p15,@p16,@p17,@p18,@p19,@p20,@p21,@p22,@p23)
	`, h.cfg.PartsTable()),
		partNumber, fv(r, "revision"), fv(r, "title"), fv(r, "detail"), fv(r, "category"), r.FormValue("has_bom") == "1",
		fv(r, "release_status"), r.FormValue("active") == "1", fv(r, "PNReqBy"), fv(r, "PNNotes"),
		now, now,
		nullableInt(fv(r, "PNUNID")),
		fv(r, "user_field_1"), fv(r, "user_field_2"), fv(r, "user_field_3"), fv(r, "user_field_4"), fv(r, "user_field_5"),
		fv(r, "user_field_6"), fv(r, "user_field_7"), fv(r, "user_field_8"), fv(r, "user_field_9"), fv(r, "user_field_10"),
	).Scan(&newID)
	if err != nil {
		units, _ := h.fetchUnits(r.Context())
		h.render(w, r, "part_edit.html", map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Error creating part: " + err.Error(),
			"Units": units, "Categories": h.loadCategories(r.Context()),
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%d", newID), http.StatusFound)
}

// ── PartEdit — GET /part/{id}/edit ──────────────────────────────────────────

func (h *Handler) PartEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "edit")
	if !ok {
		return
	}
	// fetch full part for form values
	full, err := h.fetchPartFull(r.Context(), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return
	}
	h.applyCategoryTabs(r.Context(), &full) // resolve tabs for the part_tabs partial
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "part_edit.html", map[string]any{
		"Part": full, "IsNew": false,
		"Units": units, "Categories": h.loadCategories(r.Context()),
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		// keep p.PNID available even though full has it too
		"PartBasic": p,
	})
}

// ── PartUpdate — POST /part/{id} ────────────────────────────────────────────

func (h *Handler) PartUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	partNumber := fv(r, "part_number")
	if partNumber == "" {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		pf := partFromForm(r)
		h.applyCategoryTabs(r.Context(), &pf)
		h.render(w, r, "part_edit.html", map[string]any{
			"Part": pf, "IsNew": false, "Error": "Part Number is required",
			"Categories": h.loadCategories(r.Context()),
			"ActiveTab":  "parts", "ActiveSubTab": "edit",
			"NavBackURL": backURL, "NavBackLabel": backLabel,
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
			"PartBasic": p,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  part_number=@p1, revision=@p2, title=@p3, detail=@p4, category=@p5, has_bom=@p6,
		  release_status=@p7, is_active=@p8, requested_by=@p9, notes=@p10, modified_date=@p11,
		  unit_id=@p12,
		  user_field_1=@p13, user_field_2=@p14, user_field_3=@p15, user_field_4=@p16, user_field_5=@p17,
		  user_field_6=@p18, user_field_7=@p19, user_field_8=@p20, user_field_9=@p21, user_field_10=@p22
		WHERE id=@p23
	`, h.cfg.PartsTable()),
		partNumber, fv(r, "revision"), fv(r, "title"), fv(r, "detail"), fv(r, "category"), r.FormValue("has_bom") == "1",
		fv(r, "release_status"), r.FormValue("active") == "1", fv(r, "PNReqBy"), fv(r, "PNNotes"),
		time.Now(),
		nullableInt(fv(r, "PNUNID")),
		fv(r, "user_field_1"), fv(r, "user_field_2"), fv(r, "user_field_3"), fv(r, "user_field_4"), fv(r, "user_field_5"),
		fv(r, "user_field_6"), fv(r, "user_field_7"), fv(r, "user_field_8"), fv(r, "user_field_9"), fv(r, "user_field_10"),
		id,
	)
	if err != nil {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		units, _ := h.fetchUnits(r.Context())
		pf := partFromForm(r)
		h.applyCategoryTabs(r.Context(), &pf)
		h.render(w, r, "part_edit.html", map[string]any{
			"Part": pf, "IsNew": false, "Error": "Error saving part: " + err.Error(),
			"Units": units, "Categories": h.loadCategories(r.Context()),
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"NavBackURL": backURL, "NavBackLabel": backLabel,
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
			"PartBasic": p,
		})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s", id), http.StatusFound)
}

// ── helpers ─────────────────────────────────────────────────────────────────

// partFromForm rebuilds a Part struct from POST form values (for re-displaying on error).
func partFromForm(r *http.Request) models.Part {
	p := models.Part{
		PartNumber: fv(r, "part_number"), Revision: fv(r, "revision"),
		Title: fv(r, "title"), Detail: fv(r, "detail"), Category: fv(r, "category"),
		HasBOM:        r.FormValue("has_bom") == "1",
		ReleaseStatus: fv(r, "release_status"), Active: r.FormValue("active") == "1",
		PNReqBy: fv(r, "PNReqBy"), PNNotes: fv(r, "PNNotes"),
		UserField1: fv(r, "user_field_1"), UserField2: fv(r, "user_field_2"), UserField3: fv(r, "user_field_3"),
		UserField4: fv(r, "user_field_4"), UserField5: fv(r, "user_field_5"), UserField6: fv(r, "user_field_6"),
		UserField7: fv(r, "user_field_7"), UserField8: fv(r, "user_field_8"), UserField9: fv(r, "user_field_9"),
		UserField10: fv(r, "user_field_10"),
	}
	if v := fv(r, "PNUNID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.UnitID = &n
		}
	}
	return p
}

// fetchPartFull fetches all editable fields for the edit form.
func (h *Handler) fetchPartFull(ctx context.Context, id string) (models.Part, error) {
	var p models.Part
	var (
		partNumber, revision, title, detail, category sql.NullString
		status, reqBy, notes                          sql.NullString
		user1, user2, user3, user4, user5             sql.NullString
		user6, user7, user8, user9, user10            sql.NullString
		active, hasBOM                                sql.NullBool
		unitID                                        sql.NullInt64
	)
	err := h.queryRowContext(ctx, fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail, category, has_bom,
		       release_status, is_active, requested_by, notes,
		       unit_id,
		       user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		       user_field_6, user_field_7, user_field_8, user_field_9, user_field_10
		FROM %s WHERE id = @p1
	`, h.cfg.PartsTable()), id).Scan(
		&p.PNID, &partNumber, &revision, &title, &detail, &category, &hasBOM,
		&status, &active, &reqBy, &notes,
		&unitID,
		&user1, &user2, &user3, &user4, &user5,
		&user6, &user7, &user8, &user9, &user10,
	)
	if err != nil {
		return p, err
	}
	p.PartNumber = partNumber.String
	p.Revision = revision.String
	p.Title = title.String
	p.Detail = detail.String
	p.Category = category.String
	p.HasBOM = hasBOM.Bool
	p.ReleaseStatus = status.String
	p.Active = active.Bool
	p.PNReqBy = reqBy.String
	p.PNNotes = notes.String
	if unitID.Valid {
		v := int(unitID.Int64)
		p.UnitID = &v
	}
	p.UserField1, p.UserField2, p.UserField3, p.UserField4, p.UserField5 = user1.String, user2.String, user3.String, user4.String, user5.String
	p.UserField6, p.UserField7, p.UserField8, p.UserField9, p.UserField10 = user6.String, user7.String, user8.String, user9.String, user10.String
	return p, nil
}

// ── Sub-tab handlers ────────────────────────────────────────────────────────

func (h *Handler) PartBOM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pl.line_number, pl.qty, pl.component_part_id,
		       pn.part_number, pn.title, pn.revision, pn.category,
		       pn.current_cost, pn.last_rollup_cost,
		       CAST(CASE WHEN EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = pn.id) THEN 1 ELSE 0 END AS BIT)
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
		ORDER BY pl.line_number
	`, pl, pl, pn), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.BOMItem
	var bomTotal float64
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title, revision, category sql.NullString
		var currentCost, lastRollupCost sql.NullFloat64
		var childHasBOM sql.NullBool
		if err := rows.Scan(&item.PLItem, &item.PLQty, &item.PLPartID,
			&partNumber, &title, &revision, &category,
			&currentCost, &lastRollupCost, &childHasBOM); err != nil {
			h.renderError(w, r, "Error reading BOM: "+err.Error())
			return
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		item.Revision = revision.String
		item.Category = category.String
		item.PNCurrentCost = currentCost.Float64
		item.PNLastRollupCost = lastRollupCost.Float64
		item.ChildHasBOM = childHasBOM.Bool

		if item.ChildHasBOM {
			item.LineUnitCost = item.PNLastRollupCost
			if item.PNLastRollupCost > 0 {
				item.CostSource = "rollup"
			} else {
				item.CostSource = "missing"
			}
		} else {
			item.LineUnitCost = item.PNCurrentCost
			if item.PNCurrentCost > 0 {
				item.CostSource = "current_cost"
			} else {
				item.CostSource = "missing"
			}
		}
		item.LineExtCost = item.LineUnitCost * item.PLQty
		bomTotal += item.LineExtCost
		items = append(items, item)
	}
	h.render(w, r, "part_bom.html", map[string]any{
		"Part": p, "BOMItems": items, "BOMTotal": bomTotal,
		"ActiveTab": "parts", "ActiveSubTab": "bom",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) PartWhereUsed(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "where-used")
	if !ok {
		return
	}
	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pl.line_number, pl.qty, pl.parent_part_id,
		       pn.part_number, pn.title, pn.revision, pn.category
		FROM %s pl
		JOIN %s pn ON pl.parent_part_id = pn.id
		WHERE pl.component_part_id = @p1
		ORDER BY pn.part_number
	`, pl, pn), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving where-used: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.BOMItem
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title, revision, category sql.NullString
		if err := rows.Scan(&item.PLItem, &item.PLQty, &item.PLListID,
			&partNumber, &title, &revision, &category); err != nil {
			h.renderError(w, r, "Error reading where-used: "+err.Error())
			return
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		item.Revision = revision.String
		item.Category = category.String
		items = append(items, item)
	}
	h.render(w, r, "part_where_used.html", map[string]any{
		"Part": p, "WhereUsedItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "where-used",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// ── BOM edit helpers ─────────────────────────────────────────────────────────

type bomRow struct {
	Item   string
	Qty    string
	PNID   string
	PartPN string
}

func extractBOMRows(form map[string][]string, prefix string) map[string]bomRow {
	rows := map[string]bomRow{}
	for key, vals := range form {
		if !strings.HasPrefix(key, prefix+"[") {
			continue
		}
		rest := key[len(prefix)+1:]
		sep := strings.Index(rest, "][")
		if sep < 0 {
			continue
		}
		id := rest[:sep]
		field := strings.TrimSuffix(rest[sep+2:], "]")
		val := ""
		if len(vals) > 0 {
			val = strings.TrimSpace(vals[0])
		}
		row := rows[id]
		switch field {
		case "PLItem":
			row.Item = val
		case "PLQty":
			row.Qty = val
		case "PLPNID":
			row.PNID = val
		case "PLPartNumber":
			row.PartPN = val
		}
		rows[id] = row
	}
	return rows
}

// ── PartBOMEdit — GET /part/{id}/bom/edit ───────────────────────────────────

func (h *Handler) PartBOMEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pl.id, pl.line_number, pl.qty, pl.component_part_id,
		       pn.part_number, pn.title
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
		ORDER BY pl.line_number
	`, pl, pn), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.BOMItem
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title sql.NullString
		if err := rows.Scan(&item.PLID, &item.PLItem, &item.PLQty, &item.PLPartID,
			&partNumber, &title); err != nil {
			h.renderError(w, r, "Error reading BOM: "+err.Error())
			return
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		items = append(items, item)
	}
	var lastRollupCost sql.NullFloat64
	var lastRollupAt sql.NullTime
	h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT last_rollup_cost, last_rollup_at FROM %s WHERE id = @p1`, pn,
	), id).Scan(&lastRollupCost, &lastRollupAt)
	p.PNLastRollupCost = lastRollupCost.Float64
	if lastRollupAt.Valid {
		p.PNLastRollupAt = &lastRollupAt.Time
	}
	h.render(w, r, "part_bom_edit.html", map[string]any{
		"Part": p, "BOMItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "bom",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode, "CSRFToken": h.csrfToken(w, r),
	})
}

// ── PartBOMSave — POST /part/{id}/bom ───────────────────────────────────────

func (h *Handler) PartBOMSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, r, "Error starting transaction: "+err.Error())
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()

	deleteSet := map[string]bool{}
	for _, plidStr := range r.Form["delete_pl[]"] {
		deleteSet[plidStr] = true
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`DELETE FROM %s WHERE id=@p1`, pl,
		), plidStr); err != nil {
			h.renderError(w, r, "Error deleting BOM row: "+err.Error())
			return
		}
	}

	for plidStr, row := range extractBOMRows(r.Form, "pl") {
		if deleteSet[plidStr] {
			continue
		}
		item, _ := strconv.Atoi(row.Item)
		qty, _ := strconv.ParseFloat(row.Qty, 64)
		var pnid int
		if row.PNID != "" {
			pnid, _ = strconv.Atoi(row.PNID)
		}
		if pnid == 0 && row.PartPN != "" {
			h.queryRowContext(r.Context(), fmt.Sprintf(
				`SELECT id FROM %s WHERE part_number = @p1`, pn,
			), row.PartPN).Scan(&pnid)
		}
		if pnid == 0 {
			continue
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET line_number=@p1, qty=@p2, component_part_id=@p3 WHERE id=@p4
		`, pl), item, qty, pnid, plidStr); err != nil {
			h.renderError(w, r, "Error updating BOM row: "+err.Error())
			return
		}
	}

	parentID, _ := strconv.Atoi(id)
	for _, row := range extractBOMRows(r.Form, "new_pl") {
		if row.PartPN == "" && row.PNID == "" {
			continue
		}
		item, _ := strconv.Atoi(row.Item)
		qty, _ := strconv.ParseFloat(row.Qty, 64)
		var pnid int
		if row.PNID != "" {
			pnid, _ = strconv.Atoi(row.PNID)
		}
		if pnid == 0 && row.PartPN != "" {
			h.queryRowContext(r.Context(), fmt.Sprintf(
				`SELECT id FROM %s WHERE part_number = @p1`, pn,
			), row.PartPN).Scan(&pnid)
		}
		if pnid == 0 {
			continue
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s (parent_part_id, component_part_id, line_number, qty) VALUES (@p1,@p2,@p3,@p4)
		`, pl), parentID, pnid, item, qty); err != nil {
			h.renderError(w, r, "Error inserting BOM row: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving BOM: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusFound)
}

// ── BOM cost rollup ──────────────────────────────────────────────────────────

type rollupResult struct {
	cost  float64
	cycle bool
}

// rollupCost recursively computes the rolled-up cost for part pnid.
// visited is path-scoped (defer-deleted on return) for cycle detection.
// memo is global to the walk; once a node is computed its result is reused.
func (h *Handler) rollupCost(ctx context.Context, pnid int, visited map[int]bool, memo map[int]rollupResult) (rollupResult, error) {
	if visited[pnid] {
		return rollupResult{cycle: true}, nil
	}
	if res, ok := memo[pnid]; ok {
		return res, nil
	}
	visited[pnid] = true
	defer delete(visited, pnid)

	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pl.component_part_id, pl.qty, pn.current_cost,
		       CAST(CASE WHEN EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = pn.id) THEN 1 ELSE 0 END AS BIT)
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
	`, pl, pl, pn), pnid)
	if err != nil {
		return rollupResult{}, err
	}
	defer rows.Close()

	var total float64
	var hasCycle bool
	for rows.Next() {
		var childID int
		var qty float64
		var currentCost sql.NullFloat64
		var childHasBOM sql.NullBool
		if err := rows.Scan(&childID, &qty, &currentCost, &childHasBOM); err != nil {
			return rollupResult{}, err
		}
		var unitCost float64
		if childHasBOM.Bool {
			res, err := h.rollupCost(ctx, childID, visited, memo)
			if err != nil {
				return rollupResult{}, err
			}
			if res.cycle {
				hasCycle = true
			}
			unitCost = res.cost
		} else {
			unitCost = currentCost.Float64
		}
		total += unitCost * qty
	}
	if err := rows.Err(); err != nil {
		return rollupResult{}, err
	}

	result := rollupResult{cost: total, cycle: hasCycle}
	memo[pnid] = result
	return result, nil
}

// ── PartRollupCost — POST /part/{id}/rollup-cost ─────────────────────────────

func (h *Handler) PartRollupCost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pnid, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Invalid part ID")
		return
	}
	memo := map[int]rollupResult{}
	res, err := h.rollupCost(r.Context(), pnid, map[int]bool{}, memo)
	if err != nil {
		h.renderError(w, r, "Error computing rollup cost: "+err.Error())
		return
	}
	if res.cycle {
		h.renderError(w, r, "BOM contains a cycle — fix the BOM before running rollup.")
		return
	}
	// Write rollup cost back to every assembly visited during the walk (root + all
	// sub-assemblies), using a single timestamp so the BOM view is consistent.
	now := time.Now()
	pn := h.cfg.PartsTable()
	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, r, "Error saving rollup cost: "+err.Error())
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()
	for partID, result := range memo {
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET last_rollup_cost=@p1, last_rollup_at=@p2 WHERE id=@p3`, pn,
		), result.cost, now, partID); err != nil {
			h.renderError(w, r, "Error saving rollup cost: "+err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving rollup cost: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusSeeOther)
}

func (h *Handler) PartAttachments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "attachments")
	if !ok {
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, file_name, category, part_revision, sort_order
		FROM %s WHERE part_id = @p1 AND is_active = 1 ORDER BY sort_order, id
	`, h.cfg.AttachmentsTable()), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving attachments: "+err.Error())
		return
	}
	defer rows.Close()
	var atts []models.Attachment
	for rows.Next() {
		var att models.Attachment
		var fname, fnotes, frev sql.NullString
		var orderID sql.NullInt64
		if err := rows.Scan(&att.FILID, &fname, &fnotes, &frev, &orderID); err != nil {
			h.renderError(w, r, "Error reading attachments: "+err.Error())
			return
		}
		att.FILFileName = fname.String
		att.Category = fnotes.String
		att.FILPNRev = frev.String
		if orderID.Valid {
			v := int(orderID.Int64)
			att.OrderID = &v
		}
		atts = append(atts, att)
	}
	var editingAtt *models.Attachment
	if editID := r.URL.Query().Get("edit"); editID != "" {
		for i := range atts {
			if fmt.Sprintf("%d", atts[i].FILID) == editID {
				editingAtt = &atts[i]
				break
			}
		}
	}
	cats := splitCSV(h.appConfigGetOr(r.Context(), "attachment_categories", ""))
	h.render(w, r, "part_attachments.html", map[string]any{
		"Part": p, "Attachments": atts, "EditingAtt": editingAtt,
		"ActiveTab": "parts", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken":            h.csrfToken(w, r),
		"AttachmentCategories": cats,
		"TestMode":             h.cfg.TestMode,
	})
}

func (h *Handler) PartAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var oID any
	if v := fv(r, "order_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			oID = n
		}
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`INSERT INTO %s (part_id, file_name, part_revision, category, sort_order) VALUES (@p1,@p2,@p3,@p4,@p5)`,
		h.cfg.AttachmentsTable(),
	), id, fv(r, "FILFileName"), fv(r, "FILPNRev"), fv(r, "FILNotes"), oID); err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	id, attID := chi.URLParam(r, "id"), chi.URLParam(r, "attID")
	var oID any
	if v := fv(r, "order_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			oID = n
		}
	}
	attIDInt, _ := strconv.Atoi(attID)
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET part_revision=@p1, category=@p2, sort_order=@p3 WHERE id=@p4`,
		h.cfg.AttachmentsTable(),
	), fv(r, "FILPNRev"), fv(r, "category"), oID, attIDInt); err != nil {
		h.renderError(w, r, "Error updating attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attIDInt, _ := strconv.Atoi(chi.URLParam(r, "attID"))
	if err := h.softDeleteAttachment(r.Context(), h.cfg.AttachmentsTable(), "id", attIDInt, "", 0); err != nil {
		h.renderError(w, r, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartSetPrimaryAttachment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	idInt, _ := strconv.Atoi(id)
	filID := r.FormValue("filid")
	var val any
	if n, err2 := strconv.Atoi(filID); err2 == nil && n != 0 {
		val = n
	}
	if err := h.setPrimaryAttachment(r.Context(), h.cfg.PartsTable(), "id", "primary_attachment_id", idInt, val); err != nil {
		h.renderError(w, r, "Error setting primary attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

// APIPartLocalAttachments — GET /api/part/{id}/local-attachments (#156)
// Returns LOCAL: file (not directory) attachments for a part, for the PO import picker.
func (h *Handler) APIPartLocalAttachments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(
		`SELECT id, file_name FROM %s WHERE part_id = @p1 AND is_active = 1 ORDER BY sort_order, id`,
		h.cfg.AttachmentsTable()), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type att struct {
		ID       int    `json:"id"`
		BaseName string `json:"base_name"`
	}
	out := []att{}
	for rows.Next() {
		var a att
		var fname sql.NullString
		if rows.Scan(&a.ID, &fname) != nil {
			continue
		}
		fn := fname.String
		if !strings.HasPrefix(strings.ToUpper(fn), "LOCAL:") {
			continue
		}
		stripped := fn[6:]
		if strings.HasSuffix(stripped, "/") || strings.HasSuffix(stripped, "\\") {
			continue
		}
		a.BaseName = filepath.Base(strings.ReplaceAll(stripped, "\\", "/"))
		out = append(out, a)
	}
	writeJSON(w, out)
}

func (h *Handler) PartOrders(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "orders")
	if !ok {
		return
	}
	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT po.number, po.supplier_name, po.date_ordered, po.date_closed, po.status,
		       pol.line_number, pol.qty, pol.unit_cost, pol.description, pol.vendor_part_number
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE pol.part_id = @p1
		ORDER BY po.date_ordered DESC
	`, pol, po), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving orders: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.PurchaseOrderLine
	for rows.Next() {
		var item models.PurchaseOrderLine
		var poNum, supplierName, desc, vendorPN, status sql.NullString
		var dateOrdered, dateClosed sql.NullTime
		if err := rows.Scan(
			&poNum, &supplierName, &dateOrdered, &dateClosed, &status,
			&item.POLItem, &item.POLQty, &item.POLCost, &desc, &vendorPN,
		); err != nil {
			h.renderError(w, r, "Error reading orders: "+err.Error())
			return
		}
		item.PONumber = poNum.String
		item.SupplierName = supplierName.String
		item.Status = status.String
		item.POLDesc = desc.String
		item.VendorPN = vendorPN.String
		if dateOrdered.Valid {
			item.DateOrdered = &dateOrdered.Time
		}
		if dateClosed.Valid {
			item.DateClosed = &dateClosed.Time
		}
		items = append(items, item)
	}
	h.render(w, r, "part_orders.html", map[string]any{
		"Part": p, "OrderItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "orders",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

type SupplierPriceGroup struct {
	SupplierID   int
	SupplierName string
	AllInactive  bool
	Rows         []models.Price
}

func (h *Handler) PartPricing(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "pricing")
	if !ok {
		return
	}
	pr, su := h.cfg.PriceTable(), h.cfg.CompanyTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT p.id, p.price_ea, p.price_pack, p.pack_size, p.is_active, p.effective_date, p.supplier_id, s.name
		FROM %s p
		LEFT JOIN %s s ON p.supplier_id = s.id
		WHERE p.part_id = @p1
		ORDER BY s.name, p.effective_date DESC, p.pack_size
	`, pr, su), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving pricing: "+err.Error())
		return
	}
	defer rows.Close()
	var allRows []models.Price
	for rows.Next() {
		var price models.Price
		var priceEA, pricePack, packSize sql.NullFloat64
		var supplierID sql.NullInt64
		var isActive sql.NullBool
		var effectiveDate sql.NullTime
		var supplierName sql.NullString
		if err := rows.Scan(&price.ID, &priceEA, &pricePack, &packSize, &isActive, &effectiveDate, &supplierID, &supplierName); err != nil {
			h.renderError(w, r, "Error reading pricing: "+err.Error())
			return
		}
		if priceEA.Valid {
			price.PriceEA = &priceEA.Float64
		}
		if pricePack.Valid {
			price.PricePack = &pricePack.Float64
		}
		if packSize.Valid {
			price.PackSize = &packSize.Float64
		}
		price.IsActive = isActive.Bool
		if effectiveDate.Valid {
			price.EffectiveDate = &effectiveDate.Time
		}
		if supplierID.Valid {
			v := int(supplierID.Int64)
			price.SupplierID = &v
		}
		price.SupplierName = supplierName.String
		allRows = append(allRows, price)
	}
	seen := map[int]int{}
	var groups []SupplierPriceGroup
	for _, row := range allRows {
		sid := 0
		if row.SupplierID != nil {
			sid = *row.SupplierID
		}
		if idx, ok := seen[sid]; ok {
			groups[idx].Rows = append(groups[idx].Rows, row)
		} else {
			seen[sid] = len(groups)
			groups = append(groups, SupplierPriceGroup{SupplierID: sid, SupplierName: row.SupplierName, Rows: []models.Price{row}})
		}
	}
	for i := range groups {
		allInactive := true
		for _, r := range groups[i].Rows {
			if r.IsActive {
				allInactive = false
				break
			}
		}
		groups[i].AllInactive = allInactive
	}
	h.render(w, r, "part_pricing.html", map[string]any{
		"Part": p, "PriceGroups": groups,
		"Suppliers": h.fetchSupplierOptions(r),
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── Price CRUD ───────────────────────────────────────────────────────────────

func (h *Handler) PriceNew(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "pricing")
	if !ok {
		return
	}
	h.render(w, r, "part_pricing_form.html", map[string]any{
		"Part": p, "Price": models.Price{}, "IsNew": true,
		"Suppliers": h.fetchSupplierOptions(r),
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) PriceCreate(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	supplierID, err := strconv.Atoi(r.FormValue("supplier_id"))
	if err != nil || supplierID == 0 {
		h.renderError(w, r, "Invalid supplier")
		return
	}
	effectiveDate := r.FormValue("effective_date")
	if effectiveDate == "" {
		effectiveDate = time.Now().Format("2006-01-02")
	}
	_, err = h.execContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, 1)
	`, h.cfg.PriceTable()),
		partID, supplierID,
		r.FormValue("pack_size"), r.FormValue("price_ea"), r.FormValue("price_pack"),
		effectiveDate,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UQ_price") {
			h.renderError(w, r, "A price already exists for this supplier and pack size. Deactivate the existing row first.")
			return
		}
		h.renderError(w, r, "Error saving price: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceEdit(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	priceID := chi.URLParam(r, "priceID")
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "pricing")
	if !ok {
		return
	}
	var price models.Price
	var priceEA, pricePack, packSize sql.NullFloat64
	var isActive sql.NullBool
	var effectiveDate sql.NullTime
	var supplierID sql.NullInt64
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT id, price_ea, price_pack, pack_size, is_active, effective_date, supplier_id
		FROM %s WHERE id = @p1 AND part_id = @p2
	`, h.cfg.PriceTable()), priceID, partID).Scan(
		&price.ID, &priceEA, &pricePack, &packSize, &isActive, &effectiveDate, &supplierID,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Price not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving price: "+err.Error())
		return
	}
	if priceEA.Valid {
		price.PriceEA = &priceEA.Float64
	}
	if pricePack.Valid {
		price.PricePack = &pricePack.Float64
	}
	if packSize.Valid {
		price.PackSize = &packSize.Float64
	}
	price.IsActive = isActive.Bool
	if effectiveDate.Valid {
		price.EffectiveDate = &effectiveDate.Time
	}
	if supplierID.Valid {
		v := int(supplierID.Int64)
		price.SupplierID = &v
	}
	h.render(w, r, "part_pricing_form.html", map[string]any{
		"Part": p, "Price": price, "IsNew": false,
		"Suppliers": h.fetchSupplierOptions(r),
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) PriceUpdate(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	priceID := chi.URLParam(r, "priceID")
	supplierID, err := strconv.Atoi(r.FormValue("supplier_id"))
	if err != nil || supplierID == 0 {
		h.renderError(w, r, "Invalid supplier")
		return
	}
	effectiveDate := r.FormValue("effective_date")
	if effectiveDate == "" {
		effectiveDate = time.Now().Format("2006-01-02")
	}
	pr := h.cfg.PriceTable()
	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, r, "Error starting transaction: "+err.Error())
		return
	}
	_, err = tx.ExecContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET is_active = 0 WHERE id = @p1 AND part_id = @p2`, pr,
	), priceID, partID)
	if err != nil {
		tx.Rollback()
		h.renderError(w, r, "Error updating price: "+err.Error())
		return
	}
	_, err = tx.ExecContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (part_id, supplier_id, pack_size, price_ea, price_pack, effective_date, is_active)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, 1)
	`, pr),
		partID, supplierID,
		r.FormValue("pack_size"), r.FormValue("price_ea"), r.FormValue("price_pack"),
		effectiveDate,
	)
	if err != nil {
		tx.Rollback()
		if strings.Contains(err.Error(), "UQ_price") {
			h.renderError(w, r, "A price already exists for this supplier and pack size. Deactivate the existing row first.")
			return
		}
		h.renderError(w, r, "Error saving price: "+err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error committing price update: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceDeactivate(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	priceID := chi.URLParam(r, "priceID")
	_, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET is_active = 0 WHERE id = @p1 AND part_id = @p2`, h.cfg.PriceTable(),
	), priceID, partID)
	if err != nil {
		h.renderError(w, r, "Error deactivating price: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceActivate(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	priceID := chi.URLParam(r, "priceID")
	_, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET is_active = 1 WHERE id = @p1 AND part_id = @p2`, h.cfg.PriceTable(),
	), priceID, partID)
	if err != nil {
		if strings.Contains(err.Error(), "UQ_price") {
			h.renderError(w, r, "Cannot activate: another active price exists for this supplier and pack size.")
			return
		}
		h.renderError(w, r, "Error activating price: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

// ── PartsExportCSV — GET /parts/export.csv ──────────────────────────────────

func (h *Handler) PartsExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT part_number, revision, title, detail,
		       requested_by, created_date, category, modified_date, is_active
		FROM %s ORDER BY part_number
	`, h.cfg.PartsTable()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="parts.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Part Number", "Revision", "Title", "Detail", "Requested By", "Created Date", "Category", "Modified Date", "Active"})
	for rows.Next() {
		var pn, rev, title, detail, reqBy, cat sql.NullString
		var created, modified sql.NullTime
		var active sql.NullBool
		if err := rows.Scan(&pn, &rev, &title, &detail, &reqBy, &created, &cat, &modified, &active); err != nil {
			return
		}
		activeStr := "true"
		if active.Valid && !active.Bool {
			activeStr = "false"
		}
		createdStr := ""
		if created.Valid {
			createdStr = created.Time.Format("2006-01-02")
		}
		modifiedStr := ""
		if modified.Valid {
			modifiedStr = modified.Time.Format("2006-01-02")
		}
		_ = cw.Write([]string{pn.String, rev.String, title.String, detail.String, reqBy.String, createdStr, cat.String, modifiedStr, activeStr})
	}
	cw.Flush()
}

// ── BOMExportCSV — GET /part/{id}/bom/export.csv ────────────────────────────

func (h *Handler) BOMExportCSV(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
	var parentPN string
	_ = h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT part_number FROM %s WHERE id = @p1`, h.cfg.PartsTable()), id).Scan(&parentPN)
	if parentPN == "" {
		parentPN = id
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pl.line_number, pl.qty, pn.part_number, pn.title, pn.revision, pn.category,
		       pn.current_cost, pn.last_rollup_cost,
		       CAST(CASE WHEN EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = pn.id) THEN 1 ELSE 0 END AS BIT)
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
		ORDER BY pl.line_number
	`, pl, pl, pn), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+parentPN+`-bom.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Line #", "Qty", "Part Number", "Title", "Revision", "Category", "Unit Cost", "Ext Cost", "Cost Source"})
	for rows.Next() {
		var lineNum sql.NullInt64
		var qty sql.NullFloat64
		var partNum, title, rev, cat sql.NullString
		var currentCost, rollupCost sql.NullFloat64
		var childHasBOM sql.NullBool
		if err := rows.Scan(&lineNum, &qty, &partNum, &title, &rev, &cat,
			&currentCost, &rollupCost, &childHasBOM); err != nil {
			return
		}
		var unitCost float64
		var costSrc string
		if childHasBOM.Bool {
			unitCost = rollupCost.Float64
			if rollupCost.Float64 > 0 {
				costSrc = "rollup"
			} else {
				costSrc = "missing"
			}
		} else {
			unitCost = currentCost.Float64
			if currentCost.Float64 > 0 {
				costSrc = "current_cost"
			} else {
				costSrc = "missing"
			}
		}
		extCost := unitCost * qty.Float64
		_ = cw.Write([]string{
			fmt.Sprintf("%d", lineNum.Int64),
			fmt.Sprintf("%.4g", qty.Float64),
			partNum.String, title.String, rev.String, cat.String,
			fmt.Sprintf("%.2f", unitCost),
			fmt.Sprintf("%.2f", extCost),
			costSrc,
		})
	}
	cw.Flush()
}
