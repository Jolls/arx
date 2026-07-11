package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)

// ── Shared helpers ──────────────────────────────────────────────────────────

func (h *Handler) fetchPartBasic(ctx context.Context, id string) (models.Part, error) {
	var p models.Part
	var partNumber, title, category sql.NullString
	var hasBOM sql.NullBool
	var filIDPrimary sql.NullInt64
	var stockOnHand sql.NullFloat64
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, part_number, title, category, `+hasOwnBOMExpr+`, primary_attachment_id, stock_on_hand FROM %s p WHERE id = @p1`,
		h.cfg.BOMTable(), "p.id", h.cfg.PartsTable(),
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

// ── PartsList — GET /parts ──────────────────────────────────────────────────

// RootRedirect is the app root ("/"): it sends each user to their configured
// landing page (issue #282), defaulting to the parts list. The parts list
// itself is served at "/parts".
func (h *Handler) RootRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, landingRoute(h.currentUser(r)), http.StatusSeeOther)
}

func (h *Handler) PartsList(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "parts/index.html", map[string]any{
		"ActiveTab": "parts", "TestMode": h.cfg.TestMode,
		"Categories": h.partCategories,
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
		Attach   int    `json:"attach"`
		POLines  int    `json:"poLines"`
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail,
		       requested_by, created_date, category, modified_date, is_active,
		       attachment_count, po_line_count
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
		var attach, poLines sql.NullInt64
		if err := rows.Scan(&p.ID, &pn, &rev, &title, &detail, &reqBy, &date, &cat, &modified, &active, &attach, &poLines); err != nil {
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
		p.Attach = int(attach.Int64)
		p.POLines = int(poLines.Int64)
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
		SELECT id, part_number, revision, title, detail, category, `+hasOwnBOMExpr+`,
		       release_status, is_active, requested_by, notes,
		       created_date, modified_date, primary_attachment_id,
		       current_cost, last_rollup_cost, last_rollup_at, attachment_count, po_line_count,
		       unit_id, stock_on_hand,
		       user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		       user_field_6, user_field_7, user_field_8, user_field_9, user_field_10
		FROM %s p WHERE id = @p1
	`, h.cfg.BOMTable(), "p.id", h.cfg.PartsTable()), id).Scan(
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
	p.ReleaseStatus = releaseStatusOrUnderReview(status.String)
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

	var topAtts, photoAtts []models.Attachment
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(
		`SELECT id, file_name, category, part_revision, sort_order FROM %s
		 WHERE part_id = @p1 AND is_active = 1
		 ORDER BY sort_order, id`,
		h.cfg.AttachmentsTable()), p.PNID)
	if err == nil {
		for rows.Next() {
			var att models.Attachment
			var fname, fnotes, frev sql.NullString
			var sortOrder sql.NullInt64
			if err := rows.Scan(&att.FILID, &fname, &fnotes, &frev, &sortOrder); err == nil {
				att.FILFileName = fname.String
				att.Category = fnotes.String
				att.FILPNRev = frev.String
				if sortOrder.Valid {
					v := int(sortOrder.Int64)
					att.OrderID = &v
				}
				if att.FILID != p.PNFILIDPrimary && len(topAtts) < 5 {
					topAtts = append(topAtts, att)
				}
				if urlutil.IsLocalFile(att.FILFileName) && !urlutil.IsLocalDir(att.FILFileName) &&
					urlutil.IsImage(urlutil.FileBaseName(att.FILFileName)) {
					photoAtts = append(photoAtts, att)
				}
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("[part] attachments for part %d: %v", p.PNID, err)
		}
		rows.Close()
	}

	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.PNID), p.PartNumber)
	h.applyCategoryTabs(r.Context(), &p)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)

	// Phase 2 (#465): the delta compares the rollup against the part's purchase
	// price — the cheapest active price from its preferred supplier, falling back
	// to current_cost when no preferred-supplier price exists.
	purchasePrice := p.PNCurrentCost
	var prefPrice sql.NullFloat64
	h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT MIN(price_ea) FROM %s WHERE part_id=@p1 AND is_active=1
		 AND supplier_id=(SELECT default_supplier_id FROM %s WHERE id=@p1)`,
		h.cfg.PriceTable(), h.cfg.PartsTable()), p.PNID).Scan(&prefPrice)
	if prefPrice.Valid && prefPrice.Float64 > 0 {
		purchasePrice = prefPrice.Float64
	}

	var rollupDelta, rollupDeltaPct float64
	var rollupSignificant bool
	if p.PNLastRollupAt != nil && purchasePrice > 0 {
		rollupDelta = p.PNLastRollupCost - purchasePrice
		rollupDeltaPct = rollupDelta / purchasePrice * 100
		rollupSignificant = math.Abs(rollupDeltaPct) >= 5.0
	}

	var recentPOs []partPOSummary
	var recentTxns []partTxnSummary
	if p.ShowOrders() {
		recentPOs = h.recentPartPOs(r.Context(), id, 5)
	}
	if p.ShowInventory() {
		recentTxns = h.recentPartTxns(r.Context(), id, 5)
	}

	var priceJSON template.JS
	var hasPriceData bool
	if p.ShowPricing() {
		pts := h.partPricePoints(r.Context(), id)
		if len(pts) > 0 {
			data, _ := json.Marshal(pts)
			priceJSON = template.JS(data)
			hasPriceData = true
		}
	}

	h.render(w, r, "parts/part_detail.html", map[string]any{
		"Part": p, "PrimaryAtt": primaryAtt, "TopAtts": topAtts, "PhotoAtts": photoAtts,
		"ActiveTab": "parts", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode":          h.cfg.TestMode,
		"RollupDelta":       rollupDelta,
		"RollupDeltaPct":    rollupDeltaPct,
		"RollupSignificant": rollupSignificant,
		"RecentPOs":         recentPOs,
		"RecentTxns":        recentTxns,
		"PriceDataJSON":     priceJSON,
		"HasPriceData":      hasPriceData,
	})
}

// ── PartsNew — GET /parts/new ───────────────────────────────────────────────

func (h *Handler) PartsNew(w http.ResponseWriter, r *http.Request) {
	p := models.Part{}
	if u := h.currentUser(r); u != nil {
		p.PNReqBy = u.DisplayName
	}
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "parts/part_edit.html", map[string]any{
		"Part": p, "IsNew": true,
		"Units": units, "Categories": h.partCategories,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── PartDuplicate — GET /part/{id}/duplicate ────────────────────────────────

// PartDuplicate renders the create form pre-filled from an existing part so the
// user can clone it. The BOM is carried through via the duplicate_bom_from hidden
// field and copied by PartsCreate on save; everything else (attachments, pricing,
// suppliers, mfg parts, history) is intentionally excluded (#548).
func (h *Handler) PartDuplicate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := h.fetchPartFull(r.Context(), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return
	}
	sourcePN := src.PartNumber
	src.PartNumber = ""     // force the user to enter a new, unique number
	src.ReleaseStatus = "U" // a fresh clone starts Under Review
	// has_bom is not reliably maintained, so check for actual BOM lines to decide
	// whether to promise a BOM copy in the UI.
	var bomLines int
	_ = h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE parent_part_id=@p1`, h.cfg.BOMTable()), src.PNID).Scan(&bomLines)
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "parts/part_edit.html", map[string]any{
		"Part": src, "IsNew": true, "IsDuplicate": true,
		"DuplicateFrom": sourcePN, "DuplicateBOMFrom": src.PNID, "SourceHasBOM": bomLines > 0,
		"Units": units, "Categories": h.partCategories,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── PartsCreate — POST /parts ───────────────────────────────────────────────

func (h *Handler) PartsCreate(w http.ResponseWriter, r *http.Request) {
	partNumber := fv(r, "part_number")
	if partNumber == "" {
		units, _ := h.fetchUnits(r.Context())
		h.render(w, r, "parts/part_edit.html", dupContext(r, map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Part Number is required",
			"Units": units, "Categories": h.partCategories,
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		}))
		return
	}
	now := time.Now()
	var newID int
	insertPart := h.dialect.InsertReturningID(h.cfg.PartsTable(),
		`part_number, revision, title, detail, category,
		 release_status, is_active, requested_by, notes, created_date, modified_date,
		 unit_id, current_cost,
		 user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		 user_field_6, user_field_7, user_field_8, user_field_9, user_field_10`,
		`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,
		 @p12,@p13,
		 @p14,@p15,@p16,@p17,@p18,@p19,@p20,@p21,@p22,@p23`,
		false)
	err := h.queryRowContext(r.Context(), insertPart,
		partNumber, fv(r, "revision"), fv(r, "title"), fv(r, "detail"), fv(r, "category"),
		releaseStatusOrUnderReview(fv(r, "release_status")), activeFromStatus(r), fv(r, "PNReqBy"), fv(r, "PNNotes"),
		now, now,
		nullableInt(fv(r, "PNUNID")), floatOrZero(fv(r, "current_cost")),
		fv(r, "user_field_1"), fv(r, "user_field_2"), fv(r, "user_field_3"), fv(r, "user_field_4"), fv(r, "user_field_5"),
		fv(r, "user_field_6"), fv(r, "user_field_7"), fv(r, "user_field_8"), fv(r, "user_field_9"), fv(r, "user_field_10"),
	).Scan(&newID)
	if err != nil {
		units, _ := h.fetchUnits(r.Context())
		h.render(w, r, "parts/part_edit.html", dupContext(r, map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Error creating part: " + err.Error(),
			"Units": units, "Categories": h.partCategories,
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		}))
		return
	}
	// Duplicate flow: copy the source part's BOM onto the new part (#548).
	if srcID, e := strconv.Atoi(fv(r, "duplicate_bom_from")); e == nil && srcID > 0 {
		if err := h.copyBOM(r.Context(), srcID, newID); err != nil {
			h.renderError(w, r, "Part created but copying BOM failed: "+err.Error())
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%d", newID), http.StatusFound)
}

// copyBOM clones every BOM line from srcID onto dstID. Used by the duplicate-part flow (#548).
func (h *Handler) copyBOM(ctx context.Context, srcID, dstID int) error {
	_, err := h.execContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (parent_part_id, component_part_id, line_number, qty)
		SELECT @p1, component_part_id, line_number, qty FROM %s WHERE parent_part_id = @p2
	`, h.cfg.BOMTable(), h.cfg.BOMTable()), dstID, srcID)
	return err
}

// dupContext re-adds the duplicate banner/hidden-field context to a render map
// when a create request came from PartDuplicate, so the info banner and the BOM
// source survive validation re-renders (#548).
func dupContext(r *http.Request, m map[string]any) map[string]any {
	if v := fv(r, "duplicate_bom_from"); v != "" {
		m["IsDuplicate"] = true
		m["DuplicateBOMFrom"] = v
		m["DuplicateFrom"] = fv(r, "duplicate_from")
		m["SourceHasBOM"] = fv(r, "duplicate_has_bom") == "1"
	}
	return m
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
	h.render(w, r, "parts/part_edit.html", map[string]any{
		"Part": full, "IsNew": false,
		"Units": units, "Categories": h.partCategories,
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
		h.render(w, r, "parts/part_edit.html", map[string]any{
			"Part": pf, "IsNew": false, "Error": "Part Number is required",
			"Categories": h.partCategories,
			"ActiveTab":  "parts", "ActiveSubTab": "edit",
			"NavBackURL": backURL, "NavBackLabel": backLabel,
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
			"PartBasic": p,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  part_number=@p1, revision=@p2, title=@p3, detail=@p4, category=@p5,
		  release_status=@p6, is_active=@p7, requested_by=@p8, notes=@p9, modified_date=@p10,
		  unit_id=@p11, current_cost=@p12,
		  user_field_1=@p13, user_field_2=@p14, user_field_3=@p15, user_field_4=@p16, user_field_5=@p17,
		  user_field_6=@p18, user_field_7=@p19, user_field_8=@p20, user_field_9=@p21, user_field_10=@p22
		WHERE id=@p23
	`, h.cfg.PartsTable()),
		partNumber, fv(r, "revision"), fv(r, "title"), fv(r, "detail"), fv(r, "category"),
		releaseStatusOrUnderReview(fv(r, "release_status")), activeFromStatus(r), fv(r, "PNReqBy"), fv(r, "PNNotes"),
		time.Now(),
		nullableInt(fv(r, "PNUNID")), floatOrZero(fv(r, "current_cost")),
		fv(r, "user_field_1"), fv(r, "user_field_2"), fv(r, "user_field_3"), fv(r, "user_field_4"), fv(r, "user_field_5"),
		fv(r, "user_field_6"), fv(r, "user_field_7"), fv(r, "user_field_8"), fv(r, "user_field_9"), fv(r, "user_field_10"),
		id,
	)
	if err != nil {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		units, _ := h.fetchUnits(r.Context())
		pf := partFromForm(r)
		h.applyCategoryTabs(r.Context(), &pf)
		h.render(w, r, "parts/part_edit.html", map[string]any{
			"Part": pf, "IsNew": false, "Error": "Error saving part: " + err.Error(),
			"Units": units, "Categories": h.partCategories,
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

// activeFromStatus derives is_active from the release_status field: a Deprecated
// ("D") part is inactive, everything else is active. Release status is the single
// source of truth for a part's active state (#532).
func activeFromStatus(r *http.Request) bool { return fv(r, "release_status") != "D" }

// releaseStatusOrUnderReview coalesces a blank release_status to "U" (Under Review).
// A part with no explicit status is Under Review — never implicitly active/released.
// Applied on write (so the DB never receives '') and on read (so legacy/old-binary
// blank rows present as Under Review everywhere) (#542).
func releaseStatusOrUnderReview(s string) string {
	if s == "" {
		return "U"
	}
	return s
}

// partFromForm rebuilds a Part struct from POST form values (for re-displaying on error).
func partFromForm(r *http.Request) models.Part {
	p := models.Part{
		PartNumber: fv(r, "part_number"), Revision: fv(r, "revision"),
		Title: fv(r, "title"), Detail: fv(r, "detail"), Category: fv(r, "category"),
		ReleaseStatus: releaseStatusOrUnderReview(fv(r, "release_status")), Active: activeFromStatus(r),
		PNReqBy: fv(r, "PNReqBy"), PNNotes: fv(r, "PNNotes"),
		UserField1: fv(r, "user_field_1"), UserField2: fv(r, "user_field_2"), UserField3: fv(r, "user_field_3"),
		UserField4: fv(r, "user_field_4"), UserField5: fv(r, "user_field_5"), UserField6: fv(r, "user_field_6"),
		UserField7: fv(r, "user_field_7"), UserField8: fv(r, "user_field_8"), UserField9: fv(r, "user_field_9"),
		UserField10: fv(r, "user_field_10"),
	}
	if v := fv(r, "current_cost"); v != "" {
		if c, err := strconv.ParseFloat(v, 64); err == nil {
			p.PNCurrentCost = c
		}
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
		currentCost                                   sql.NullFloat64
	)
	err := h.queryRowContext(ctx, fmt.Sprintf(`
		SELECT id, part_number, revision, title, detail, category, `+hasOwnBOMExpr+`,
		       release_status, is_active, requested_by, notes,
		       unit_id, current_cost,
		       user_field_1, user_field_2, user_field_3, user_field_4, user_field_5,
		       user_field_6, user_field_7, user_field_8, user_field_9, user_field_10
		FROM %s p WHERE id = @p1
	`, h.cfg.BOMTable(), "p.id", h.cfg.PartsTable()), id).Scan(
		&p.PNID, &partNumber, &revision, &title, &detail, &category, &hasBOM,
		&status, &active, &reqBy, &notes,
		&unitID, &currentCost,
		&user1, &user2, &user3, &user4, &user5,
		&user6, &user7, &user8, &user9, &user10,
	)
	if err != nil {
		return p, err
	}
	p.PNCurrentCost = currentCost.Float64
	p.PartNumber = partNumber.String
	p.Revision = revision.String
	p.Title = title.String
	p.Detail = detail.String
	p.Category = category.String
	p.HasBOM = hasBOM.Bool
	p.ReleaseStatus = releaseStatusOrUnderReview(status.String)
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

// bomLeafCost picks the per-line unit cost and CostSource label for a BOM row,
// shared by the read-only BOM view (PartBOM) and CSV export (BOMExportCSV).
// Assembly rows use the stored rollup; leaf rows prefer the preferred-supplier
// price, else current_cost — labelled "labor" for OPS lines whose current_cost
// is an hourly rate. Mirrors the leaf-cost rule in rollupCost.
func bomLeafCost(childHasBOM bool, lastRollupCost float64, preferredPrice sql.NullFloat64, currentCost float64, category string) (float64, string) {
	if childHasBOM {
		if lastRollupCost > 0 {
			return lastRollupCost, "rollup"
		}
		return 0, "missing"
	}
	if preferredPrice.Valid && preferredPrice.Float64 > 0 {
		return preferredPrice.Float64, "price"
	}
	if currentCost > 0 {
		if category == "OPS" {
			return currentCost, "labor"
		}
		return currentCost, "current_cost"
	}
	return 0, "missing"
}

// fetchBOMItems runs the direct-children BOM query for partID, shared by the
// read-only BOM view (PartBOM) and its lazy-loaded children endpoint
// (APIPartBOMChildren).
func (h *Handler) fetchBOMItems(ctx context.Context, partID string) ([]models.BOMItem, float64, error) {
	pl, pn, prc := h.cfg.BOMTable(), h.cfg.PartsTable(), h.cfg.PriceTable()
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pl.line_number, pl.qty, pl.component_part_id,
		       pn.part_number, pn.title, pn.revision, pn.category,
		       pn.current_cost, pn.last_rollup_cost,
		       (SELECT MIN(p.price_ea) FROM %s p
		        WHERE p.part_id = pn.id AND p.is_active = 1 AND p.supplier_id = pn.default_supplier_id) AS preferred_price,
		       CAST(CASE WHEN EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = pn.id) THEN 1 ELSE 0 END AS BIT),
		       pn.attachment_count, pn.po_line_count
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
		ORDER BY pl.line_number
	`, prc, pl, pl, pn), partID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []models.BOMItem
	var bomTotal float64
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title, revision, category sql.NullString
		var currentCost, lastRollupCost, preferredPrice sql.NullFloat64
		var childHasBOM sql.NullBool
		var attachCount, poLineCount sql.NullInt64
		if err := rows.Scan(&item.PLItem, &item.PLQty, &item.PLPartID,
			&partNumber, &title, &revision, &category,
			&currentCost, &lastRollupCost, &preferredPrice, &childHasBOM,
			&attachCount, &poLineCount); err != nil {
			return nil, 0, err
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		item.Revision = revision.String
		item.Category = category.String
		item.PNCurrentCost = currentCost.Float64
		item.PNLastRollupCost = lastRollupCost.Float64
		item.ChildHasBOM = childHasBOM.Bool
		item.AttachCount = int(attachCount.Int64)
		item.POLineCount = int(poLineCount.Int64)

		item.LineUnitCost, item.CostSource = bomLeafCost(item.ChildHasBOM, item.PNLastRollupCost, preferredPrice, item.PNCurrentCost, item.Category)
		item.LineExtCost = item.LineUnitCost * item.PLQty
		bomTotal += item.LineExtCost
		items = append(items, item)
	}
	return items, bomTotal, nil
}

func (h *Handler) PartBOM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	items, bomTotal, err := h.fetchBOMItems(r.Context(), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_bom.html", map[string]any{
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
	h.render(w, r, "parts/part_where_used.html", map[string]any{
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
	h.render(w, r, "parts/part_bom_edit.html", map[string]any{
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

// hasOwnBOMExpr is the "does this part have its own BOM" EXISTS check shared by
// rollupCost and aggregateLeafQty — both walk the same bom table shape to decide
// whether to recurse into a sub-assembly or treat a component as a leaf.
const hasOwnBOMExpr = "CAST(CASE WHEN EXISTS(SELECT 1 FROM %[1]s c WHERE c.parent_part_id = %[2]s) THEN 1 ELSE 0 END AS BIT)"

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

	pl, pn, pr := h.cfg.BOMTable(), h.cfg.PartsTable(), h.cfg.PriceTable()
	hasBOM := fmt.Sprintf(hasOwnBOMExpr, pl, "pn.id")
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pl.component_part_id, pl.qty, pn.current_cost,
		       (SELECT MIN(p.price_ea) FROM %s p
		        WHERE p.part_id = pn.id AND p.is_active = 1 AND p.supplier_id = pn.default_supplier_id) AS preferred_price,
		       %s
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
	`, pr, hasBOM, pl, pn), pnid)
	if err != nil {
		return rollupResult{}, err
	}
	defer rows.Close()

	var total float64
	var hasCycle bool
	for rows.Next() {
		var childID int
		var qty float64
		var currentCost, preferredPrice sql.NullFloat64
		var childHasBOM sql.NullBool
		if err := rows.Scan(&childID, &qty, &currentCost, &preferredPrice, &childHasBOM); err != nil {
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
		} else if preferredPrice.Valid {
			unitCost = preferredPrice.Float64
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

// ── Cost to build N (qty-break-aware) ───────────────────────────────────────

// priceTier is one active price row for a part+supplier, used for tier selection.
type priceTier struct {
	PriceEA  float64
	PackSize float64
}

// pickTier selects the tier with the largest PackSize <= qty. Returns ok=false
// if qty is below every tier's PackSize (or there are no tiers at all).
func pickTier(tiers []priceTier, qty float64) (unitPrice, packSize float64, ok bool) {
	found := false
	for _, t := range tiers {
		if t.PackSize <= qty && (!found || t.PackSize > packSize) {
			unitPrice, packSize, found = t.PriceEA, t.PackSize, true
		}
	}
	return unitPrice, packSize, found
}

type buildCostLine struct {
	PNID       int
	PartNumber string
	Title      string
	QtyNeeded  float64
	PackSize   float64
	UnitPrice  float64
	ExtCost    float64
	Source     string // "price" | "missing"
}

type buildCostResult struct {
	Lines []buildCostLine
	Total float64
	Cycle bool
}

// aggregateLeafQty walks the BOM tree from pnid, multiplying qty by parentQty at
// each level, and sums extended quantity into leaves (parts with no BOM) by pnid.
// visited is path-scoped for cycle detection, matching rollupCost.
func (h *Handler) aggregateLeafQty(ctx context.Context, pnid int, parentQty float64, visited map[int]bool, leaves map[int]float64) (bool, error) {
	if visited[pnid] {
		return true, nil
	}
	visited[pnid] = true
	defer delete(visited, pnid)

	pl := h.cfg.BOMTable()
	hasBOM := fmt.Sprintf(hasOwnBOMExpr, pl, "pl.component_part_id")
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT pl.component_part_id, pl.qty, %s
		FROM %s pl
		WHERE pl.parent_part_id = @p1
	`, hasBOM, pl), pnid)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var hasCycle bool
	for rows.Next() {
		var childID int
		var qty float64
		var childHasBOM sql.NullBool
		if err := rows.Scan(&childID, &qty, &childHasBOM); err != nil {
			return false, err
		}
		extQty := qty * parentQty
		if childHasBOM.Bool {
			cycle, err := h.aggregateLeafQty(ctx, childID, extQty, visited, leaves)
			if err != nil {
				return false, err
			}
			if cycle {
				// A cycle anywhere in the tree makes the whole walk's result
				// discardable (buildCost returns Cycle:true without using
				// leaves), so stop issuing further queries for the rest of
				// this node's siblings instead of walking the remaining tree
				// for nothing.
				hasCycle = true
				break
			}
		} else {
			leaves[childID] += extQty
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return hasCycle, nil
}

// buildCost computes the consolidated cost to build qty units of pnid: it
// aggregates each leaf part's total demand across every occurrence in the tree
// (Pass 1), then prices each leaf once at its aggregated qty using the largest
// qualifying pack_size tier for the part's default supplier (Pass 2). Leaves
// below every tier's pack_size are reported as "missing" — no current_cost
// fallback, no extrapolation.
type buildCostPartInfo struct {
	PartNumber        string
	Title             string
	DefaultSupplierID sql.NullInt64
}

// fetchPartInfoByID batches a part_number/title/default_supplier_id lookup for
// every id in ids into a single query, keyed by id. Used by buildCost's Pass 2
// so pricing N leaves costs O(1) round trips instead of O(N).
func (h *Handler) fetchPartInfoByID(ctx context.Context, ids []int) (map[int]buildCostPartInfo, error) {
	out := map[int]buildCostPartInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	placeholders, args := sqlInClause(ids)
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT id, part_number, title, default_supplier_id FROM %s WHERE id IN (%s)`,
		h.cfg.PartsTable(), placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var partNumber, title sql.NullString
		var info buildCostPartInfo
		if err := rows.Scan(&id, &partNumber, &title, &info.DefaultSupplierID); err != nil {
			return nil, err
		}
		info.PartNumber, info.Title = partNumber.String, title.String
		out[id] = info
	}
	return out, rows.Err()
}

// partSupplierKey identifies one part+supplier pairing, used to key batched
// price-tier lookups in buildCost's Pass 2.
type partSupplierKey struct {
	PartID     int
	SupplierID int
}

// fetchPriceTiersByPart batches active price rows for every id in ids into a
// single query, keyed by (part_id, supplier_id) so buildCost's Pass 2 can look
// up just the tiers for each leaf's own default supplier.
func (h *Handler) fetchPriceTiersByPart(ctx context.Context, ids []int) (map[partSupplierKey][]priceTier, error) {
	out := map[partSupplierKey][]priceTier{}
	if len(ids) == 0 {
		return out, nil
	}
	placeholders, args := sqlInClause(ids)
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT part_id, supplier_id, price_ea, pack_size FROM %s WHERE is_active = 1 AND part_id IN (%s)`,
		h.cfg.PriceTable(), placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key partSupplierKey
		var t priceTier
		if err := rows.Scan(&key.PartID, &key.SupplierID, &t.PriceEA, &t.PackSize); err != nil {
			return nil, err
		}
		out[key] = append(out[key], t)
	}
	return out, rows.Err()
}

// sqlInClause builds a "@p1,@p2,..." placeholder list and matching args slice
// for a dynamic-length IN (...) clause.
func sqlInClause(ids []int) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("@p%d", i+1)
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

func (h *Handler) buildCost(ctx context.Context, pnid int, qty float64) (buildCostResult, error) {
	leaves := map[int]float64{}
	hasCycle, err := h.aggregateLeafQty(ctx, pnid, qty, map[int]bool{}, leaves)
	if err != nil {
		return buildCostResult{}, err
	}
	if hasCycle {
		return buildCostResult{Cycle: true}, nil
	}

	ids := make([]int, 0, len(leaves))
	for id := range leaves {
		ids = append(ids, id)
	}
	partInfo, err := h.fetchPartInfoByID(ctx, ids)
	if err != nil {
		return buildCostResult{}, err
	}
	priceTiers, err := h.fetchPriceTiersByPart(ctx, ids)
	if err != nil {
		return buildCostResult{}, err
	}

	var result buildCostResult
	for childID, totalQty := range leaves {
		info := partInfo[childID]
		line := buildCostLine{
			PNID: childID, PartNumber: info.PartNumber, Title: info.Title,
			QtyNeeded: totalQty, Source: "missing",
		}

		if info.DefaultSupplierID.Valid {
			tiers := priceTiers[partSupplierKey{PartID: childID, SupplierID: int(info.DefaultSupplierID.Int64)}]
			if unitPrice, packSize, ok := pickTier(tiers, totalQty); ok {
				line.UnitPrice = unitPrice
				line.PackSize = packSize
				line.ExtCost = unitPrice * totalQty
				line.Source = "price"
			}
		}

		result.Total += line.ExtCost
		result.Lines = append(result.Lines, line)
	}
	sort.Slice(result.Lines, func(i, j int) bool {
		return result.Lines[i].PartNumber < result.Lines[j].PartNumber
	})
	return result, nil
}

// ── PartBuildCost — GET /part/{id}/build-cost?qty=N ─────────────────────────

func (h *Handler) PartBuildCost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	pnid, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Invalid part ID")
		return
	}
	qty, err := strconv.ParseFloat(r.URL.Query().Get("qty"), 64)
	if err != nil || qty <= 0 {
		h.renderError(w, r, "Invalid build quantity")
		return
	}
	res, err := h.buildCost(r.Context(), pnid, qty)
	if err != nil {
		h.renderError(w, r, "Error computing build cost: "+err.Error())
		return
	}
	if res.Cycle {
		h.renderError(w, r, "BOM contains a cycle — fix the BOM before calculating build cost.")
		return
	}
	h.render(w, r, "parts/part_build_cost.html", map[string]any{
		"Part": p, "BuildQty": qty, "Lines": res.Lines, "Total": res.Total,
		"ActiveTab": "parts", "ActiveSubTab": "bom",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) PartAttachments(w http.ResponseWriter, r *http.Request) {
	h.renderPartAttachments(w, r, chi.URLParam(r, "id"), nil)
}

// renderPartAttachments loads a part's attachments and renders the attachments
// page. extra is merged into the template data (used to surface errors or an
// import-collision prompt on the POST path).
func (h *Handler) renderPartAttachments(w http.ResponseWriter, r *http.Request, id string, extra map[string]any) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "attachments")
	if !ok {
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, file_name, category, part_revision, sort_order, comment
		FROM %s WHERE part_id = @p1 AND is_active = 1 ORDER BY sort_order, id
	`, h.cfg.AttachmentsTable()), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving attachments: "+err.Error())
		return
	}
	defer rows.Close()
	var atts []models.Attachment
	nextOrderID := 1
	for rows.Next() {
		var att models.Attachment
		var fname, fnotes, frev, fcomment sql.NullString
		var orderID sql.NullInt64
		if err := rows.Scan(&att.FILID, &fname, &fnotes, &frev, &orderID, &fcomment); err != nil {
			h.renderError(w, r, "Error reading attachments: "+err.Error())
			return
		}
		att.FILFileName = fname.String
		att.Category = fnotes.String
		att.FILPNRev = frev.String
		att.Comment = fcomment.String
		if orderID.Valid {
			v := int(orderID.Int64)
			att.OrderID = &v
			if v+1 > nextOrderID {
				nextOrderID = v + 1
			}
		}
		atts = append(atts, att)
	}
	// Fall back to the ImportCollision's AttID when there's no ?edit= query
	// param, so an edit-triggered collision keeps showing the same row's
	// Edit form instead of it disappearing from this direct (non-redirect) render.
	editID := r.URL.Query().Get("edit")
	if editID == "" {
		if ic, ok := extra["ImportCollision"].(map[string]string); ok {
			editID = ic["AttID"]
		}
	}
	var editingAtt *models.Attachment
	if editID != "" {
		for i := range atts {
			if fmt.Sprintf("%d", atts[i].FILID) == editID {
				editingAtt = &atts[i]
				break
			}
		}
	}
	cats := splitCSV(h.appConfigGetOr(r.Context(), "attachment_categories", ""))
	data := map[string]any{
		"Part": p, "Attachments": atts, "EditingAtt": editingAtt,
		"ActiveTab": "parts", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken":            h.csrfToken(w, r),
		"AttachmentCategories": cats,
		"DocControlConfigured": h.cfg.DocControlRoot != "",
		"TestMode":             h.cfg.TestMode,
		"NextOrderID":          nextOrderID,
	}
	for k, v := range extra {
		data[k] = v
	}
	h.render(w, r, "parts/part_attachments.html", data)
}

func (h *Handler) PartAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var oID any
	if v := fv(r, "order_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			oID = n
		}
	}
	rev, category := fv(r, "FILPNRev"), fv(r, "category")
	comment := fv(r, "comment")

	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, "")
	if in.ErrMsg != "" {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}

	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`INSERT INTO %s (part_id, file_name, part_revision, category, sort_order, comment) VALUES (@p1,@p2,@p3,@p4,@p5,@p6)`,
		h.cfg.AttachmentsTable(),
	), id, in.FileName, rev, category, oID, comment); err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	// Move mode: remove the source now that the attachment is saved. The row
	// already exists, so a failure here is non-fatal — surface it as a warning.
	if in.MoveSrc != "" {
		if err := os.Remove(in.MoveSrc); err != nil {
			h.renderPartAttachments(w, r, id, map[string]any{
				"Error": "Attachment saved, but the source file could not be removed: " + err.Error()})
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	id, attID := chi.URLParam(r, "id"), chi.URLParam(r, "attID")
	attIDInt, _ := strconv.Atoi(attID)
	var oID any
	if v := fv(r, "order_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			oID = n
		}
	}
	rev, category := fv(r, "FILPNRev"), fv(r, "category")
	comment := fv(r, "comment")

	var oldFileNameNS sql.NullString
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT file_name FROM %s WHERE id=@p1 AND part_id=@p2`, h.cfg.AttachmentsTable(),
	), attIDInt, id).Scan(&oldFileNameNS); err != nil {
		h.renderError(w, r, "Error loading attachment: "+err.Error())
		return
	}
	oldFileName := oldFileNameNS.String
	var replaceName string
	if urlutil.IsLocalFile(oldFileName) {
		replaceName = urlutil.StripLocalPrefix(oldFileName)
	}

	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, replaceName)
	if in.ErrMsg != "" {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		in.Collision["AttID"] = attID
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}

	fileChanged := in.FileName != "" && in.FileName != oldFileName
	var err error
	if fileChanged {
		_, err = h.execContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET part_revision=@p1, category=@p2, sort_order=@p3, comment=@p4, file_name=@p5 WHERE id=@p6`,
			h.cfg.AttachmentsTable(),
		), rev, category, oID, comment, in.FileName, attIDInt)
	} else {
		_, err = h.execContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET part_revision=@p1, category=@p2, sort_order=@p3, comment=@p4 WHERE id=@p5`,
			h.cfg.AttachmentsTable(),
		), rev, category, oID, comment, attIDInt)
	}
	if err != nil {
		h.renderError(w, r, "Error updating attachment: "+err.Error())
		return
	}

	// Move-mode cleanup runs whenever a source was browsed and moved, whether
	// or not the stored file_name changed (an identical-name replace via
	// replaceLocalFile still consumed the browsed source and needs it removed).
	if in.MoveSrc != "" {
		if err := os.Remove(in.MoveSrc); err != nil {
			h.renderPartAttachments(w, r, id, map[string]any{
				"Error": "Attachment updated, but the source file could not be removed: " + err.Error()})
			return
		}
	}
	if fileChanged && replaceName != "" {
		if err := h.deleteAttachmentFileIfUnshared(r.Context(), h.cfg.AttachmentsTable(), "id", "file_name",
			attIDInt, oldFileName, h.cfg.DocControlRoot, replaceName); err != nil {
			h.renderPartAttachments(w, r, id, map[string]any{
				"Error": "Attachment updated, but the old file could not be removed: " + err.Error()})
			return
		}
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
	h.render(w, r, "parts/part_orders.html", map[string]any{
		"Part": p, "OrderItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "orders",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// partPOSummary is one row in the Part dashboard "Recent POs" card (#521).
type partPOSummary struct {
	Number       string
	SupplierName string
	Status       string
	DateOrdered  *time.Time
	Qty          float64
	UnitCost     float64
}

// recentPartPOs returns the most recent PO lines for a part, newest first,
// capped at limit. Returns nil on error so the caller can omit the card.
func (h *Handler) recentPartPOs(ctx context.Context, partID string, limit int) []partPOSummary {
	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p2) po.number, po.supplier_name, po.status, po.date_ordered,
		       pol.qty, pol.unit_cost
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE pol.part_id = @p1
		ORDER BY po.date_ordered DESC, po.ID DESC
	`, pol, po), partID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []partPOSummary
	for rows.Next() {
		var s partPOSummary
		var num, sup, status sql.NullString
		var d sql.NullTime
		var qty, cost sql.NullFloat64
		if rows.Scan(&num, &sup, &status, &d, &qty, &cost) != nil {
			continue
		}
		s.Number, s.SupplierName, s.Status = num.String, sup.String, status.String
		s.Qty, s.UnitCost = qty.Float64, cost.Float64
		if d.Valid {
			s.DateOrdered = &d.Time
		}
		out = append(out, s)
	}
	return out
}

// partTxnSummary is one row in the Part dashboard "Inventory" card (#521).
type partTxnSummary struct {
	Type string
	Qty  float64
	Date string
}

// recentPartTxns returns the most recent inventory transactions for a part,
// newest first, capped at limit. Returns nil on error.
func (h *Handler) recentPartTxns(ctx context.Context, partID string, limit int) []partTxnSummary {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT TOP (@p2) txn_type, qty, txn_date
		FROM %s WHERE part_id = @p1 ORDER BY txn_date DESC, id DESC
	`, h.cfg.InventoryTxnTable()), partID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []partTxnSummary
	for rows.Next() {
		var s partTxnSummary
		var d sql.NullTime
		var qty sql.NullFloat64
		if rows.Scan(&s.Type, &qty, &d) != nil {
			continue
		}
		s.Qty = qty.Float64
		if d.Valid {
			s.Date = d.Time.Format("2006-01-02")
		}
		out = append(out, s)
	}
	return out
}

// pricePoint is one unit-cost-over-time sample for the price-history chart (#284),
// shared by the Price History tab and the Part dashboard trend card (#521).
type pricePoint struct {
	Date     string   `json:"date"` // YYYY-MM-DD
	Cost     float64  `json:"cost"`
	PO       string   `json:"po"`
	Supplier string   `json:"supplier"`
	Source   string   `json:"source"`            // "po" | "price"
	PackSize *float64 `json:"packSize,omitempty"` // qty-break tier, "price" source only (#612)
}

// partPricePoints assembles the unit-cost-over-time samples for a part from its
// PO lines and active price-list entries, chronological within each source.
// Shared by PartPriceHistory and PartDetail (#521).
func (h *Handler) partPricePoints(ctx context.Context, partID string) []pricePoint {
	var points []pricePoint

	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	if rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT po.number, po.supplier_name, po.date_ordered, pol.unit_cost
		FROM %s pol
		JOIN %s po ON pol.po_id = po.ID
		WHERE pol.part_id = @p1 AND po.date_ordered IS NOT NULL
		ORDER BY po.date_ordered
	`, pol, po), partID); err == nil {
		for rows.Next() {
			var num, sup sql.NullString
			var d sql.NullTime
			var cost float64
			if rows.Scan(&num, &sup, &d, &cost) == nil && d.Valid {
				points = append(points, pricePoint{
					Date: d.Time.Format("2006-01-02"), Cost: cost,
					PO: num.String, Supplier: sup.String, Source: "po",
				})
			}
		}
		rows.Close()
	}

	pr, comp := h.cfg.PriceTable(), h.cfg.CompanyTable()
	if prRows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT c.name, p.effective_date, p.price_ea, p.pack_size
		FROM %s p
		LEFT JOIN %s c ON p.supplier_id = c.id
		WHERE p.part_id = @p1 AND p.is_active = 1 AND p.effective_date IS NOT NULL
		ORDER BY p.effective_date
	`, pr, comp), partID); err == nil {
		for prRows.Next() {
			var sup sql.NullString
			var d sql.NullTime
			var ea, packSize sql.NullFloat64
			if prRows.Scan(&sup, &d, &ea, &packSize) == nil && d.Valid && ea.Valid {
				pt := pricePoint{
					Date: d.Time.Format("2006-01-02"), Cost: ea.Float64,
					PO: "", Supplier: sup.String, Source: "price",
				}
				if packSize.Valid {
					pt.PackSize = &packSize.Float64
				}
				points = append(points, pt)
			}
		}
		prRows.Close()
	}
	return points
}

// PartPriceHistory renders the Price History tab: a unit-cost-over-time chart
// sourced from this part's PO lines (one point per line) plus any active
// price-list entries (#284). Points are emitted as JSON for the SVG renderer in
// static/parts/price_history.js.
func (h *Handler) PartPriceHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "price-history")
	if !ok {
		return
	}

	points := h.partPricePoints(r.Context(), id)

	data, _ := json.Marshal(points)
	h.render(w, r, "parts/part_price_history.html", map[string]any{
		"Part": p, "PriceDataJSON": template.JS(data), "HasData": len(points) > 0,
		"ActiveTab": "parts", "ActiveSubTab": "price-history",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

type SupplierPriceGroup struct {
	SupplierID   int
	SupplierName string
	AllInactive  bool
	IsPreferred  bool // this supplier is the part's preferred source for cost rollup (#465)
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
	var defSup sql.NullInt64
	h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT default_supplier_id FROM %s WHERE id=@p1`, h.cfg.PartsTable()), id).Scan(&defSup)
	for i := range groups {
		allInactive := true
		for _, r := range groups[i].Rows {
			if r.IsActive {
				allInactive = false
				break
			}
		}
		groups[i].AllInactive = allInactive
		groups[i].IsPreferred = defSup.Valid && groups[i].SupplierID == int(defSup.Int64)
	}
	h.render(w, r, "parts/part_pricing.html", map[string]any{
		"Part": p, "PriceGroups": groups,
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
	h.render(w, r, "parts/part_pricing_form.html", map[string]any{
		"Part": p, "Price": models.Price{}, "IsNew": true,
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ensureDefaultSupplier pins supplierID as the part's preferred supplier for cost
// rollup the first time a price is added (#465). No-op once one is already set.
func (h *Handler) ensureDefaultSupplier(ctx context.Context, partID, supplierID any) {
	_, _ = h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET default_supplier_id=@p1 WHERE id=@p2 AND default_supplier_id IS NULL`,
		h.cfg.PartsTable()), supplierID, partID)
}

// PricePreferred — POST /part/{id}/pricing/preferred. Sets the preferred supplier
// used for cost rollup (the multi-supplier review case from migration #484).
func (h *Handler) PricePreferred(w http.ResponseWriter, r *http.Request) {
	partID := chi.URLParam(r, "id")
	supplierID, err := strconv.Atoi(r.FormValue("supplier_id"))
	if err != nil || supplierID == 0 {
		h.renderError(w, r, "Invalid supplier")
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET default_supplier_id=@p1 WHERE id=@p2`, h.cfg.PartsTable(),
	), supplierID, partID); err != nil {
		h.renderError(w, r, "Error setting preferred supplier: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
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
	h.ensureDefaultSupplier(r.Context(), partID, supplierID)
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
	var supplierName sql.NullString
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT p.id, p.price_ea, p.price_pack, p.pack_size, p.is_active, p.effective_date, p.supplier_id, s.name
		FROM %s p
		LEFT JOIN %s s ON p.supplier_id = s.id
		WHERE p.id = @p1 AND p.part_id = @p2
	`, h.cfg.PriceTable(), h.cfg.CompanyTable()), priceID, partID).Scan(
		&price.ID, &priceEA, &pricePack, &packSize, &isActive, &effectiveDate, &supplierID, &supplierName,
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
	price.SupplierName = supplierName.String
	h.render(w, r, "parts/part_pricing_form.html", map[string]any{
		"Part": p, "Price": price, "IsNew": false,
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
	h.ensureDefaultSupplier(r.Context(), partID, supplierID)
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
	prc := h.cfg.PriceTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT pl.line_number, pl.qty, pn.part_number, pn.title, pn.revision, pn.category,
		       pn.current_cost, pn.last_rollup_cost,
		       (SELECT MIN(p.price_ea) FROM %s p
		        WHERE p.part_id = pn.id AND p.is_active = 1 AND p.supplier_id = pn.default_supplier_id) AS preferred_price,
		       CAST(CASE WHEN EXISTS(SELECT 1 FROM %s c WHERE c.parent_part_id = pn.id) THEN 1 ELSE 0 END AS BIT)
		FROM %s pl
		JOIN %s pn ON pl.component_part_id = pn.id
		WHERE pl.parent_part_id = @p1
		ORDER BY pl.line_number
	`, prc, pl, pl, pn), id)
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
		var currentCost, rollupCost, preferredPrice sql.NullFloat64
		var childHasBOM sql.NullBool
		if err := rows.Scan(&lineNum, &qty, &partNum, &title, &rev, &cat,
			&currentCost, &rollupCost, &preferredPrice, &childHasBOM); err != nil {
			return
		}
		unitCost, costSrc := bomLeafCost(childHasBOM.Bool, rollupCost.Float64, preferredPrice, currentCost.Float64, cat.String)
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
