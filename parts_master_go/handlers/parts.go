package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/parts_master_go/models"
)

// ── Shared helpers ──────────────────────────────────────────────────────────

func (h *Handler) fetchPartBasic(ctx context.Context, id string) (models.Part, error) {
	var p models.Part
	var partNumber, title, category sql.NullString
	var hasBOM sql.NullBool
	var filIDPrimary sql.NullInt64
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT PNID, part_number, title, category, has_bom, PNFILIDPrimary FROM %s WHERE PNID = @p1`,
		h.cfg.PartsTable(),
	), id).Scan(&p.PNID, &partNumber, &title, &category, &hasBOM, &filIDPrimary)
	p.PartNumber = partNumber.String
	p.Title = title.String
	p.Category = category.String
	p.HasBOM = hasBOM.Bool
	p.PNFILIDPrimary = int(filIDPrimary.Int64)
	return p, err
}

func (h *Handler) partPageBase(w http.ResponseWriter, r *http.Request, id, subTab string) (models.Part, string, string, bool) {
	p, err := h.fetchPartBasic(r.Context(), id)
	if err == sql.ErrNoRows {
		h.renderError(w, "Part not found")
		return models.Part{}, "", "", false
	}
	if err != nil {
		h.renderError(w, "Error retrieving part: "+err.Error())
		return models.Part{}, "", "", false
	}
	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.PNID), p.PartNumber)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	return p, backURL, backLabel, true
}

func fs(r *http.Request, key string) string { return strings.TrimSpace(r.FormValue(key)) }

// ── PartsList — GET / ───────────────────────────────────────────────────────

func (h *Handler) PartsList(w http.ResponseWriter, r *http.Request) {
	h.CheckSchemaVersion(r.Context())
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT PNID, part_number, revision, title, detail,
		       PNReqBy, PNDate, category, PNDateModified
		FROM %s ORDER BY part_number
	`, h.cfg.PartsTable()))
	if err != nil {
		h.renderError(w, "Error connecting to database: "+err.Error())
		return
	}
	defer rows.Close()

	var parts []models.Part
	for rows.Next() {
		var p models.Part
		var partNumber, revision, title, detail, reqBy, category sql.NullString
		var pnDate, pnDateModified sql.NullTime
		if err := rows.Scan(
			&p.PNID, &partNumber, &revision, &title, &detail,
			&reqBy, &pnDate, &category, &pnDateModified,
		); err != nil {
			h.renderError(w, "Error reading parts: "+err.Error())
			return
		}
		p.PartNumber = partNumber.String
		p.Revision = revision.String
		p.Title = title.String
		p.Detail = detail.String
		p.PNReqBy = reqBy.String
		p.Category = category.String
		if pnDate.Valid {
			p.PNDate = &pnDate.Time
		}
		if pnDateModified.Valid {
			p.PNDateModified = &pnDateModified.Time
		}
		parts = append(parts, p)
	}
	if err := rows.Err(); err != nil {
		h.renderError(w, "Error iterating parts: "+err.Error())
		return
	}
	h.render(w, "index.html", map[string]any{
		"Parts": parts, "ActiveTab": "parts", "TestMode": h.cfg.TestMode,
	})
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
		qty, currentCost, lastRollupCost              sql.NullFloat64
	)
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT PNID, part_number, revision, title, detail, category, has_bom,
		       release_status, active, PNReqBy, PNNotes,
		       PNDate, PNDateModified, PNFILIDPrimary,
		       PNQty, PNCurrentCost, PNLastRollupCost, PNLastRollupAt, PNFILLinks, PNPOLinks,
		       PNUser1, PNUser2, PNUser3, PNUser4, PNUser5,
		       PNUser6, PNUser7, PNUser8, PNUser9, PNUser10
		FROM %s WHERE PNID = @p1
	`, h.cfg.PartsTable()), id).Scan(
		&p.PNID, &partNumber, &revision, &title, &detail, &category, &hasBOM,
		&status, &active, &reqBy, &notes,
		&pnDate, &pnDateModified, &filIDPrimary,
		&qty, &currentCost, &lastRollupCost, &lastRollupAt, &filLinks, &poLinks,
		&user1, &user2, &user3, &user4, &user5,
		&user6, &user7, &user8, &user9, &user10,
	)
	if err == sql.ErrNoRows {
		h.renderError(w, "Part not found")
		return
	}
	if err != nil {
		h.renderError(w, "Error retrieving part: "+err.Error())
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
	p.PNQty = qty.Float64
	p.PNCurrentCost = currentCost.Float64
	p.PNLastRollupCost = lastRollupCost.Float64
	if lastRollupAt.Valid {
		p.PNLastRollupAt = &lastRollupAt.Time
	}
	p.PNFILLinks = int(filLinks.Int64)
	p.PNPOLinks = int(poLinks.Int64)
	p.PNUser1, p.PNUser2, p.PNUser3, p.PNUser4, p.PNUser5 = user1.String, user2.String, user3.String, user4.String, user5.String
	p.PNUser6, p.PNUser7, p.PNUser8, p.PNUser9, p.PNUser10 = user6.String, user7.String, user8.String, user9.String, user10.String
	if pnDate.Valid {
		p.PNDate = &pnDate.Time
	}
	if pnDateModified.Valid {
		p.PNDateModified = &pnDateModified.Time
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
			`SELECT FILID, FILFileName, FILNotes, FILPNRev FROM %s WHERE FILID = @p1`,
			h.cfg.AttachmentsTable(),
		), p.PNFILIDPrimary).Scan(&att.FILID, &fname, &fnotes, &frev); err == nil {
			att.FILFileName = fname.String
			att.FILNotes = fnotes.String
			att.FILPNRev = frev.String
			primaryAtt = &att
		}
	}

	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.PNID), p.PartNumber)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)

	h.render(w, "part_detail.html", map[string]any{
		"Part": p, "PrimaryAtt": primaryAtt,
		"ActiveTab": "parts", "ActiveSubTab": "details",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode,
	})
}

// ── PartsNew — GET /parts/new ───────────────────────────────────────────────

func (h *Handler) PartsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, "part_edit.html", map[string]any{
		"Part": models.Part{}, "IsNew": true,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
	})
}

// ── PartsCreate — POST /parts ───────────────────────────────────────────────

func (h *Handler) PartsCreate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	partNumber := fs(r, "part_number")
	if partNumber == "" {
		h.render(w, "part_edit.html", map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Part Number is required",
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		})
		return
	}
	now := time.Now()
	var newID int
	err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		INSERT INTO %s (part_number, revision, title, detail, category, has_bom,
		                release_status, active, PNReqBy, PNNotes, PNDate, PNDateModified,
		                PNUser1, PNUser2, PNUser3, PNUser4, PNUser5,
		                PNUser6, PNUser7, PNUser8, PNUser9, PNUser10)
		OUTPUT INSERTED.PNID
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,
		        @p13,@p14,@p15,@p16,@p17,@p18,@p19,@p20,@p21,@p22)
	`, h.cfg.PartsTable()),
		partNumber, fs(r, "revision"), fs(r, "title"), fs(r, "detail"), fs(r, "category"), r.FormValue("has_bom") == "1",
		fs(r, "release_status"), r.FormValue("active") == "1", fs(r, "PNReqBy"), fs(r, "PNNotes"),
		now, now,
		fs(r, "PNUser1"), fs(r, "PNUser2"), fs(r, "PNUser3"), fs(r, "PNUser4"), fs(r, "PNUser5"),
		fs(r, "PNUser6"), fs(r, "PNUser7"), fs(r, "PNUser8"), fs(r, "PNUser9"), fs(r, "PNUser10"),
	).Scan(&newID)
	if err != nil {
		h.render(w, "part_edit.html", map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Error creating part: " + err.Error(),
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
		h.renderError(w, "Error retrieving part: "+err.Error())
		return
	}
	h.render(w, "part_edit.html", map[string]any{
		"Part": full, "IsNew": false,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
		// keep p.PNID available even though full has it too
		"PartBasic": p,
	})
}

// ── PartUpdate — POST /part/{id} ────────────────────────────────────────────

func (h *Handler) PartUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	partNumber := fs(r, "part_number")
	if partNumber == "" {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		h.render(w, "part_edit.html", map[string]any{
			"Part": partFromForm(r), "IsNew": false, "Error": "Part Number is required",
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"NavBackURL": backURL, "NavBackLabel": backLabel,
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg.TestMode,
			"PartBasic": p,
		})
		return
	}
	_, err := h.execContext(r.Context(), fmt.Sprintf(`
		UPDATE %s SET
		  part_number=@p1, revision=@p2, title=@p3, detail=@p4, category=@p5, has_bom=@p6,
		  release_status=@p7, active=@p8, PNReqBy=@p9, PNNotes=@p10, PNDateModified=@p11,
		  PNUser1=@p12, PNUser2=@p13, PNUser3=@p14, PNUser4=@p15, PNUser5=@p16,
		  PNUser6=@p17, PNUser7=@p18, PNUser8=@p19, PNUser9=@p20, PNUser10=@p21
		WHERE PNID=@p22
	`, h.cfg.PartsTable()),
		partNumber, fs(r, "revision"), fs(r, "title"), fs(r, "detail"), fs(r, "category"), r.FormValue("has_bom") == "1",
		fs(r, "release_status"), r.FormValue("active") == "1", fs(r, "PNReqBy"), fs(r, "PNNotes"),
		time.Now(),
		fs(r, "PNUser1"), fs(r, "PNUser2"), fs(r, "PNUser3"), fs(r, "PNUser4"), fs(r, "PNUser5"),
		fs(r, "PNUser6"), fs(r, "PNUser7"), fs(r, "PNUser8"), fs(r, "PNUser9"), fs(r, "PNUser10"),
		id,
	)
	if err != nil {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		h.render(w, "part_edit.html", map[string]any{
			"Part": partFromForm(r), "IsNew": false, "Error": "Error saving part: " + err.Error(),
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
	return models.Part{
		PartNumber: fs(r, "part_number"), Revision: fs(r, "revision"),
		Title: fs(r, "title"), Detail: fs(r, "detail"), Category: fs(r, "category"),
		HasBOM: r.FormValue("has_bom") == "1",
		ReleaseStatus: fs(r, "release_status"), Active: r.FormValue("active") == "1",
		PNReqBy: fs(r, "PNReqBy"), PNNotes: fs(r, "PNNotes"),
		PNUser1: fs(r, "PNUser1"), PNUser2: fs(r, "PNUser2"), PNUser3: fs(r, "PNUser3"),
		PNUser4: fs(r, "PNUser4"), PNUser5: fs(r, "PNUser5"), PNUser6: fs(r, "PNUser6"),
		PNUser7: fs(r, "PNUser7"), PNUser8: fs(r, "PNUser8"), PNUser9: fs(r, "PNUser9"),
		PNUser10: fs(r, "PNUser10"),
	}
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
	)
	err := h.queryRowContext(ctx, fmt.Sprintf(`
		SELECT PNID, part_number, revision, title, detail, category, has_bom,
		       release_status, active, PNReqBy, PNNotes,
		       PNUser1, PNUser2, PNUser3, PNUser4, PNUser5,
		       PNUser6, PNUser7, PNUser8, PNUser9, PNUser10
		FROM %s WHERE PNID = @p1
	`, h.cfg.PartsTable()), id).Scan(
		&p.PNID, &partNumber, &revision, &title, &detail, &category, &hasBOM,
		&status, &active, &reqBy, &notes,
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
	p.PNUser1, p.PNUser2, p.PNUser3, p.PNUser4, p.PNUser5 = user1.String, user2.String, user3.String, user4.String, user5.String
	p.PNUser6, p.PNUser7, p.PNUser8, p.PNUser9, p.PNUser10 = user6.String, user7.String, user8.String, user9.String, user10.String
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
		SELECT pl.PLItem, pl.PLQty, pl.PLPartID,
		       pn.part_number, pn.title, pn.revision, pn.category, pn.PNCurrentCost
		FROM %s pl
		JOIN %s pn ON pl.PLPartID = pn.PNID
		WHERE pl.PLListID = @p1
		ORDER BY pl.PLItem
	`, pl, pn), id)
	if err != nil {
		h.renderError(w, "Error retrieving BOM: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.BOMItem
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title, revision, category sql.NullString
		var currentCost sql.NullFloat64
		if err := rows.Scan(&item.PLItem, &item.PLQty, &item.PLPartID,
			&partNumber, &title, &revision, &category, &currentCost); err != nil {
			h.renderError(w, "Error reading BOM: "+err.Error())
			return
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		item.Revision = revision.String
		item.Category = category.String
		item.PNCurrentCost = currentCost.Float64
		items = append(items, item)
	}
	h.render(w, "part_bom.html", map[string]any{
		"Part": p, "BOMItems": items,
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
		SELECT pl.PLItem, pl.PLQty, pl.PLListID,
		       pn.part_number, pn.title, pn.revision, pn.category
		FROM %s pl
		JOIN %s pn ON pl.PLListID = pn.PNID
		WHERE pl.PLPartID = @p1
		ORDER BY pn.part_number
	`, pl, pn), id)
	if err != nil {
		h.renderError(w, "Error retrieving where-used: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.BOMItem
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title, revision, category sql.NullString
		if err := rows.Scan(&item.PLItem, &item.PLQty, &item.PLListID,
			&partNumber, &title, &revision, &category); err != nil {
			h.renderError(w, "Error reading where-used: "+err.Error())
			return
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		item.Revision = revision.String
		item.Category = category.String
		items = append(items, item)
	}
	h.render(w, "part_where_used.html", map[string]any{
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
		case "PLItem":       row.Item = val
		case "PLQty":        row.Qty = val
		case "PLPNID":       row.PNID = val
		case "PLPartNumber": row.PartPN = val
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
		SELECT pl.PLID, pl.PLItem, pl.PLQty, pl.PLPartID,
		       pn.part_number, pn.title
		FROM %s pl
		JOIN %s pn ON pl.PLPartID = pn.PNID
		WHERE pl.PLListID = @p1
		ORDER BY pl.PLItem
	`, pl, pn), id)
	if err != nil {
		h.renderError(w, "Error retrieving BOM: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.BOMItem
	for rows.Next() {
		var item models.BOMItem
		var partNumber, title sql.NullString
		if err := rows.Scan(&item.PLID, &item.PLItem, &item.PLQty, &item.PLPartID,
			&partNumber, &title); err != nil {
			h.renderError(w, "Error reading BOM: "+err.Error())
			return
		}
		item.PartNumber = partNumber.String
		item.Title = title.String
		items = append(items, item)
	}
	var lastRollupCost sql.NullFloat64
	var lastRollupAt sql.NullTime
	h.queryRowContext(r.Context(), fmt.Sprintf(
		`SELECT PNLastRollupCost, PNLastRollupAt FROM %s WHERE PNID = @p1`, pn,
	), id).Scan(&lastRollupCost, &lastRollupAt)
	p.PNLastRollupCost = lastRollupCost.Float64
	if lastRollupAt.Valid {
		p.PNLastRollupAt = &lastRollupAt.Time
	}
	h.render(w, "part_bom_edit.html", map[string]any{
		"Part": p, "BOMItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "bom",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg.TestMode, "CSRFToken": h.csrfToken(w, r),
	})
}

// ── PartBOMSave — POST /part/{id}/bom ───────────────────────────────────────

func (h *Handler) PartBOMSave(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, "Error parsing form: "+err.Error())
		return
	}

	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, "Error starting transaction: "+err.Error())
		return
	}
	defer tx.Rollback()

	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()

	deleteSet := map[string]bool{}
	for _, plidStr := range r.Form["delete_pl[]"] {
		deleteSet[plidStr] = true
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`DELETE FROM %s WHERE PLID=@p1`, pl,
		), plidStr); err != nil {
			h.renderError(w, "Error deleting BOM row: "+err.Error())
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
				`SELECT PNID FROM %s WHERE part_number = @p1`, pn,
			), row.PartPN).Scan(&pnid)
		}
		if pnid == 0 {
			continue
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			UPDATE %s SET PLItem=@p1, PLQty=@p2, PLPartID=@p3 WHERE PLID=@p4
		`, pl), item, qty, pnid, plidStr); err != nil {
			h.renderError(w, "Error updating BOM row: "+err.Error())
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
				`SELECT PNID FROM %s WHERE part_number = @p1`, pn,
			), row.PartPN).Scan(&pnid)
		}
		if pnid == 0 {
			continue
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
			INSERT INTO %s (PLListID, PLPartID, PLItem, PLQty) VALUES (@p1,@p2,@p3,@p4)
		`, pl), parentID, pnid, item, qty); err != nil {
			h.renderError(w, "Error inserting BOM row: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, "Error saving BOM: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusFound)
}

// ── PartRollupCost — POST /part/{id}/rollup-cost ─────────────────────────────

func (h *Handler) PartRollupCost(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	pl, pn := h.cfg.BOMTable(), h.cfg.PartsTable()
	var cost float64
	if err := h.queryRowContext(r.Context(), fmt.Sprintf(`
		SELECT ISNULL(SUM(pn.PNCurrentCost * pl.PLQty), 0)
		FROM %s pl
		JOIN %s pn ON pl.PLPartID = pn.PNID
		WHERE pl.PLListID = @p1
	`, pl, pn), id).Scan(&cost); err != nil {
		h.renderError(w, "Error computing rollup cost: "+err.Error())
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET PNLastRollupCost=@p1, PNLastRollupAt=@p2 WHERE PNID=@p3`, pn,
	), cost, time.Now(), id); err != nil {
		h.renderError(w, "Error saving rollup cost: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusSeeOther)
}

func (h *Handler) PartAttachments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "attachments")
	if !ok {
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT FILID, FILFileName, FILNotes, FILPNRev, order_id
		FROM %s WHERE FILPNID = @p1 AND is_active = 1 ORDER BY order_id, FILID
	`, h.cfg.AttachmentsTable()), id)
	if err != nil {
		h.renderError(w, "Error retrieving attachments: "+err.Error())
		return
	}
	defer rows.Close()
	var atts []models.Attachment
	for rows.Next() {
		var att models.Attachment
		var fname, fnotes, frev sql.NullString
		var orderID sql.NullInt64
		if err := rows.Scan(&att.FILID, &fname, &fnotes, &frev, &orderID); err != nil {
			h.renderError(w, "Error reading attachments: "+err.Error())
			return
		}
		att.FILFileName = fname.String
		att.FILNotes = fnotes.String
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
	h.render(w, "part_attachments.html", map[string]any{
		"Part": p, "Attachments": atts, "EditingAtt": editingAtt,
		"ActiveTab": "parts", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r),
		"AttachmentNotes": h.cfg.Settings.AttachmentNotes,
		"TestMode": h.cfg.TestMode,
	})
}

func (h *Handler) PartAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	var oID any
	if v := fs(r, "order_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			oID = n
		}
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`INSERT INTO %s (FILPNID, FILFileName, FILPNRev, FILNotes, order_id) VALUES (@p1,@p2,@p3,@p4,@p5)`,
		h.cfg.AttachmentsTable(),
	), id, fs(r, "FILFileName"), fs(r, "FILPNRev"), fs(r, "FILNotes"), oID); err != nil {
		h.renderError(w, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id, attID := chi.URLParam(r, "id"), chi.URLParam(r, "attID")
	var oID any
	if v := fs(r, "order_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			oID = n
		}
	}
	attIDInt, _ := strconv.Atoi(attID)
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET FILPNRev=@p1, FILNotes=@p2, order_id=@p3 WHERE FILID=@p4`,
		h.cfg.AttachmentsTable(),
	), fs(r, "FILPNRev"), fs(r, "FILNotes"), oID, attIDInt); err != nil {
		h.renderError(w, "Error updating attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	attIDInt, _ := strconv.Atoi(chi.URLParam(r, "attID"))
	if err := h.softDeleteAttachment(r.Context(), h.cfg.AttachmentsTable(), "FILID", attIDInt, "", 0); err != nil {
		h.renderError(w, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartSetPrimaryAttachment(w http.ResponseWriter, r *http.Request) {
	if !h.verifyCsrf(r) {
		http.Error(w, "Invalid form submission", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	idInt, _ := strconv.Atoi(id)
	filID := r.FormValue("filid")
	var val any
	if n, err2 := strconv.Atoi(filID); err2 == nil && n != 0 {
		val = n
	}
	if err := h.setPrimaryAttachment(r.Context(), h.cfg.PartsTable(), "PNID", "PNFILIDPrimary", idInt, val); err != nil {
		h.renderError(w, "Error setting primary attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartOrders(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "orders")
	if !ok {
		return
	}
	pol, po := h.cfg.POLineTable(), h.cfg.POTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT po.number, po.supplier_name, po.date_ordered, po.date_closed, po.is_active,
		       pol.POLItem, pol.POLQty, pol.POLCost, pol.POLDesc, pol.VendorPN
		FROM %s pol
		JOIN %s po ON pol.POLPOID = po.ID
		WHERE pol.POLPNID = @p1
		ORDER BY po.date_ordered DESC
	`, pol, po), id)
	if err != nil {
		h.renderError(w, "Error retrieving orders: "+err.Error())
		return
	}
	defer rows.Close()
	var items []models.PurchaseOrderLine
	for rows.Next() {
		var item models.PurchaseOrderLine
		var poNum, supplierName, desc, vendorPN sql.NullString
		var dateOrdered, dateClosed sql.NullTime
		var isActive sql.NullBool
		if err := rows.Scan(
			&poNum, &supplierName, &dateOrdered, &dateClosed, &isActive,
			&item.POLItem, &item.POLQty, &item.POLCost, &desc, &vendorPN,
		); err != nil {
			h.renderError(w, "Error reading orders: "+err.Error())
			return
		}
		item.PONumber = poNum.String
		item.SupplierName = supplierName.String
		item.IsActive = isActive.Bool
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
	h.render(w, "part_orders.html", map[string]any{
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
	pr, su := h.cfg.PriceTable(), h.cfg.SupplierTable()
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT p.id, p.price_ea, p.price_pack, p.pack_size, p.is_active, p.supplier_id, s.name
		FROM %s p
		LEFT JOIN %s s ON p.supplier_id = s.id
		WHERE p.part_id = @p1
		ORDER BY s.name, p.pack_size
	`, pr, su), id)
	if err != nil {
		h.renderError(w, "Error retrieving pricing: "+err.Error())
		return
	}
	defer rows.Close()
	var allRows []models.Price
	for rows.Next() {
		var price models.Price
		var priceEA, pricePack, packSize sql.NullFloat64
		var supplierID sql.NullInt64
		var isActive sql.NullBool
		var supplierName sql.NullString
		if err := rows.Scan(&price.ID, &priceEA, &pricePack, &packSize, &isActive, &supplierID, &supplierName); err != nil {
			h.renderError(w, "Error reading pricing: "+err.Error())
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
	h.render(w, "part_pricing.html", map[string]any{
		"Part": p, "PriceGroups": groups,
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}
