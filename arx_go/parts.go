package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"maps"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"arx/arx_go/models"
	"arx/internal/attachments"
	"arx/internal/parts"
	"arx/internal/records"
	"arx/internal/urlutil"
)

// ── Shared helpers ──────────────────────────────────────────────────────────

func (h *Handler) fetchPartBasic(ctx context.Context, id string) (models.Part, error) {
	pid, err := strconv.Atoi(id)
	if err != nil {
		return models.Part{}, err
	}
	// the thumbnail subquery rides this one query (rather than a separate
	// round-trip) since fetchPartBasic backs every part sub-tab page (#56).
	b, err := h.parts().GetPartBasic(ctx, pid, thumbnailCategory)
	if err != nil {
		return models.Part{}, err
	}
	p := models.Part{ID: b.ID, PartNumber: b.PartNumber, Description: b.Description, Category: b.Category,
		HasBOM: b.HasBOM, PrimaryAttachmentID: b.PrimaryAttachmentID, StockOnHand: b.StockOnHand,
		TrackingMode: b.TrackingMode, IsLotTracked: models.TracksLots(b.TrackingMode)}
	if urlutil.IsLocalFile(b.ThumbFile) {
		p.ThumbnailURL = urlutil.LocalFileURL(b.ThumbFile, "/local/")
	}
	return p, nil
}

func (h *Handler) partPageBase(w http.ResponseWriter, r *http.Request, id, subTab string) (models.Part, string, string, bool) {
	p, ok := h.partForMutation(w, r, id)
	if !ok {
		return models.Part{}, "", "", false
	}
	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.ID), p.PartNumber)
	if !tabVisible(p, subTab) {
		h.renderError(w, r, "The "+subTab+" section does not apply to "+p.Category+" parts.")
		return models.Part{}, "", "", false
	}
	sess := h.session(r)
	backURL, backLabel := navBack(sess)
	return p, backURL, backLabel, true
}

// tabVisible reports whether a part subtab applies to p's category. It is the
// single source of truth for subtab gating: partPageBase calls it to guard every
// GET view, and the POST mutation handlers call it (via the same Show* methods)
// to guard writes — the subtabs are hidden in the UI for categories that don't
// apply, but the routes stay reachable by direct URL. Sections not listed here
// (details, edit, where-used, attachments) apply to every category.
func tabVisible(p models.Part, subTab string) bool {
	switch subTab {
	case "bom":
		return p.ShowBOM()
	case "build":
		return p.ShowBuild()
	case "lots":
		return p.ShowLots()
	case "units":
		return p.ShowUnits()
	case "orders":
		return p.ShowOrders()
	case "transactions":
		return p.ShowInventory()
	case "pricing", "price-history":
		return p.ShowPricing()
	case "mfg-parts":
		return p.ShowMfgParts()
	case "suppliers":
		return p.ShowSuppliers()
	default:
		return true
	}
}

// partForMutation loads a part and resolves its category tabs, rendering an
// error (ok=false) if the part is missing. It is the shared "load part + resolve
// tabs" primitive: partPageBase builds the GET page context on top of it, and
// requireTab layers the write-side tab gate on top of it for POST handlers.
func (h *Handler) partForMutation(w http.ResponseWriter, r *http.Request, id string) (models.Part, bool) {
	p, err := h.fetchPartBasic(r.Context(), id)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Part not found")
		return models.Part{}, false
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return models.Part{}, false
	}
	h.applyCategoryTabs(r.Context(), &p)
	return p, true
}

// requireTab loads a part for a POST mutation handler and rejects the request
// (rendering an error, ok=false) when the given subtab does not apply to the
// part's category — the write-side counterpart to partPageBase's GET gate, using
// the same tabVisible mapping.
func (h *Handler) requireTab(w http.ResponseWriter, r *http.Request, id, subTab string) (models.Part, bool) {
	p, ok := h.partForMutation(w, r, id)
	if !ok {
		return p, false
	}
	if !tabVisible(p, subTab) {
		h.renderError(w, r, "The "+subTab+" section does not apply to "+p.Category+" parts.")
		return p, false
	}
	return p, true
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
		"ActiveTab": "parts", "TestMode": h.cfg().TestMode,
		"Categories": h.st().partCategories,
	})
}

func (h *Handler) PartsRows(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	type row struct {
		ID          int    `json:"id"`
		PN          string `json:"pn"`
		Rev         string `json:"rev"`
		Description string `json:"description"`
		Detail      string `json:"detail"`
		ReqBy       string `json:"reqBy"`
		Date        string `json:"date"`
		Cat         string `json:"cat"`
		Modified    string `json:"modified"`
		Active      bool   `json:"active"`
		Attach      int    `json:"attach"`
		POLines     int    `json:"poLines"`
		BelowMin    bool   `json:"belowMin"`
		Thumb       string `json:"thumb"`
	}

	// Thumb is the /parts hover-tooltip image from a part's generated PDF thumbnail
	// (#696), pulled with the parts rather than a separate round-trip.
	parts, err := h.parts().ListParts(r.Context(), thumbnailCategory)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	out := make([]row, len(parts))
	for i, lp := range parts {
		p := row{ID: lp.ID, PN: lp.PartNumber, Rev: lp.Revision, Description: lp.Description, Detail: lp.Detail,
			ReqBy: lp.RequestedBy, Date: recordsFormatDate(lp.CreatedDate), Cat: lp.Category, Modified: recordsFormatDate(lp.ModifiedDate),
			Active: lp.IsActive, Attach: lp.AttachmentCount, POLines: lp.POLineCount, BelowMin: lp.BelowMin}
		if urlutil.IsLocalFile(lp.ThumbFile) {
			p.Thumb = urlutil.LocalFileURL(lp.ThumbFile, "/local/")
		}
		out[i] = p
	}
	log.Printf("[rows] parts: %d rows in %v", len(out), time.Since(start))
	writeJSON(w, out)
}

// ── PartDetail — GET /part/{id} and /part/{id}/details ──────────────────────

func (h *Handler) PartDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	p, err := h.fetchPartFull(r.Context(), id)
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Part not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return
	}

	if p.HasBOM && r.URL.Path == fmt.Sprintf("/part/%s", id) {
		http.Redirect(w, r, fmt.Sprintf("/part/%s/bom", id), http.StatusFound)
		return
	}

	attSvc := h.attachments()
	var primaryAtt *models.Attachment
	if p.PrimaryAttachmentID != nil {
		if a, err := attSvc.GetPartAttachment(r.Context(), *p.PrimaryAttachmentID, p.ID); err == nil {
			primaryAtt = &models.Attachment{ID: a.ID, FileName: a.FileName, Category: a.Category, PartRevision: a.PartRevision}
		}
	}

	var topAtts, photoAtts []models.Attachment
	partAtts, err := attSvc.ListPartAttachments(r.Context(), p.ID)
	if err != nil {
		log.Printf("[part] attachments for part %d: %v", p.ID, err)
	}
	for _, a := range partAtts {
		att := models.Attachment{ID: a.ID, FileName: a.FileName, Category: a.Category, PartRevision: a.PartRevision, OrderID: a.SortOrder}
		if (p.PrimaryAttachmentID == nil || att.ID != *p.PrimaryAttachmentID) && len(topAtts) < 5 {
			topAtts = append(topAtts, att)
		}
		// The generated "Thumbnail" (#696) is purpose-built for the /parts
		// hover tooltip; its larger "PDF Preview" sibling represents the PDF
		// in the Photos card, so keep the tiny thumbnail out of it.
		if urlutil.IsLocalFile(att.FileName) && !urlutil.IsLocalDir(att.FileName) &&
			urlutil.IsImage(urlutil.FileBaseName(att.FileName)) && att.Category != thumbnailCategory {
			photoAtts = append(photoAtts, att)
		}
		// PartDetail runs its own attachment query rather than fetchPartBasic
		// (#56), so the breadcrumb hover thumbnail is resolved here off this
		// same loop instead of a second round-trip.
		if att.Category == thumbnailCategory && urlutil.IsLocalFile(att.FileName) {
			p.ThumbnailURL = urlutil.LocalFileURL(att.FileName, "/local/")
		}
	}

	h.setNavContext(w, r, fmt.Sprintf("/part/%d", p.ID), p.PartNumber)
	h.applyCategoryTabs(r.Context(), &p)
	sess := h.session(r)
	backURL, backLabel := navBack(sess)

	// Phase 2 (#465): the delta compares the rollup against the part's purchase
	// price — the cheapest active price from its preferred supplier, falling back
	// to current_cost when no preferred-supplier price exists.
	purchasePrice := p.CurrentCost
	prefPrice, _ := h.parts().PreferredSupplierPrice(r.Context(), p.ID)
	if prefPrice != nil && prefPrice.IsPositive() {
		purchasePrice = *prefPrice
	}

	var rollupDelta, rollupDeltaPct decimal.Decimal
	var rollupSignificant bool
	if p.LastRollupAt != nil && purchasePrice.IsPositive() {
		rollupDelta = p.LastRollupCost.Sub(purchasePrice)
		rollupDeltaPct = rollupDelta.Div(purchasePrice).Mul(decimal.NewFromInt(100))
		rollupSignificant = rollupDeltaPct.Abs().GreaterThanOrEqual(decimal.NewFromInt(5))
	}

	var recentPOs []parts.RecentPO
	var recentTxns []partTxnSummary
	if p.ShowOrders() {
		recentPOs, _ = h.parts().ListRecentPOs(r.Context(), p.ID, 5)
	}
	if p.ShowInventory() {
		recentTxns = h.recentPartTxns(r.Context(), p.ID, 5)
	}

	var recentLots []LotRow
	var lotCount int
	if p.ShowLots() {
		recentLots, _ = h.recentPartLots(r.Context(), p.ID, 5)
		lotCount, _ = h.lotCountForPart(r.Context(), p.ID)
	}
	var recentUnits []UnitRow
	var unitCount int
	if p.ShowUnits() {
		recentUnits, _ = h.recentPartUnits(r.Context(), p.ID, 5)
		unitCount, _ = h.unitCountForPart(r.Context(), p.ID)
	}

	var prefSupplier *preferredSupplierSummary
	if p.ShowSuppliers() {
		prefSupplier = h.preferredSupplier(r.Context(), p.ID)
		if prefSupplier != nil {
			prefSupplier.Price = prefPrice
		}
	}

	var priceJSON template.JS
	var hasPriceData bool
	if p.ShowPricing() {
		pts := h.partPricePoints(r.Context(), p.ID)
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
		"TestMode":          h.cfg().TestMode,
		"RollupDelta":       rollupDelta,
		"RollupDeltaPct":    rollupDeltaPct,
		"RollupSignificant": rollupSignificant,
		"RecentPOs":         recentPOs,
		"RecentTxns":        recentTxns,
		"RecentLots":        recentLots,
		"LotCount":          lotCount,
		"RecentUnits":       recentUnits,
		"UnitCount":         unitCount,
		"PreferredSupplier": prefSupplier,
		"PriceDataJSON":     priceJSON,
		"HasPriceData":      hasPriceData,
	})
}

// ── PartsNew — GET /parts/new ───────────────────────────────────────────────

func (h *Handler) PartsNew(w http.ResponseWriter, r *http.Request) {
	p := models.Part{
		PartNumber:  r.URL.Query().Get("part_number"),
		Description: r.URL.Query().Get("description"),
	}
	if u := h.currentUser(r); u != nil {
		p.RequestedBy = u.DisplayName
	}
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "parts/part_edit.html", map[string]any{
		"Part": p, "IsNew": true,
		"Units": units, "Categories": h.st().partCategories,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── PartDuplicate — GET /part/{id}/duplicate ────────────────────────────────

// PartDuplicate renders the create form pre-filled from an existing part so the
// user can clone it. The BOM is carried through via the duplicate_bom_from hidden
// field and copied by PartsCreate on save; everything else (attachments, pricing,
// suppliers, mfg parts, history) is intentionally excluded (#548).
func (h *Handler) PartDuplicate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := h.fetchPartFull(r.Context(), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving part: "+err.Error())
		return
	}
	sourcePN := src.PartNumber
	src.PartNumber = ""     // force the user to enter a new, unique number
	src.ReleaseStatus = "U" // a fresh clone starts Under Review
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "parts/part_edit.html", map[string]any{
		"Part": src, "IsNew": true, "IsDuplicate": true,
		"DuplicateFrom": sourcePN, "DuplicateBOMFrom": src.ID, "SourceHasBOM": src.HasBOM,
		"Units": units, "Categories": h.st().partCategories,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── PartsCreate — POST /parts ───────────────────────────────────────────────

func (h *Handler) PartsCreate(w http.ResponseWriter, r *http.Request) {
	partNumber := fv(r, "part_number")
	if partNumber == "" {
		units, _ := h.fetchUnits(r.Context())
		h.render(w, r, "parts/part_edit.html", dupContext(r, map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Part Number is required",
			"Units": units, "Categories": h.st().partCategories,
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		}))
		return
	}
	newID, err := h.parts().CreatePart(r.Context(), partInput(partFromForm(r)), h.userNow(r))
	if err != nil {
		units, _ := h.fetchUnits(r.Context())
		h.render(w, r, "parts/part_edit.html", dupContext(r, map[string]any{
			"Part": partFromForm(r), "IsNew": true, "Error": "Error creating part: " + err.Error(),
			"Units": units, "Categories": h.st().partCategories,
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		}))
		return
	}
	// Duplicate flow: copy the source part's BOM onto the new part (#548).
	if srcID, e := strconv.Atoi(fv(r, "duplicate_bom_from")); e == nil && srcID > 0 {
		if err := h.parts().CopyBOM(r.Context(), srcID, newID); err != nil {
			h.renderError(w, r, "Part created but copying BOM failed: "+err.Error())
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%d", newID), http.StatusFound)
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
	id := r.PathValue("id")
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
	// fetchPartFull doesn't select this (#56); carry it over from the
	// fetchPartBasic-backed p above rather than a third query.
	full.ThumbnailURL = p.ThumbnailURL
	units, _ := h.fetchUnits(r.Context())
	h.render(w, r, "parts/part_edit.html", map[string]any{
		"Part": full, "IsNew": false,
		"Units": units, "Categories": h.st().partCategories,
		"ActiveTab": "parts", "ActiveSubTab": "edit",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
		// keep p.ID available even though full has it too
		"PartBasic": p,
	})
}

// ── PartUpdate — POST /part/{id} ────────────────────────────────────────────

func (h *Handler) PartUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	partNumber := fv(r, "part_number")
	if partNumber == "" {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		pf := partFromForm(r)
		h.applyCategoryTabs(r.Context(), &pf)
		pf.ThumbnailURL = p.ThumbnailURL
		h.render(w, r, "parts/part_edit.html", map[string]any{
			"Part": pf, "IsNew": false, "Error": "Part Number is required",
			"Categories": h.st().partCategories,
			"ActiveTab":  "parts", "ActiveSubTab": "edit",
			"NavBackURL": backURL, "NavBackLabel": backLabel,
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
			"PartBasic": p,
		})
		return
	}
	in := partInput(partFromForm(r))
	var err error
	if in.ID, err = strconv.Atoi(id); err == nil {
		err = h.parts().UpdatePart(r.Context(), in, h.userNow(r))
	}
	if err != nil {
		p, backURL, backLabel, _ := h.partPageBase(w, r, id, "edit")
		units, _ := h.fetchUnits(r.Context())
		pf := partFromForm(r)
		h.applyCategoryTabs(r.Context(), &pf)
		pf.ThumbnailURL = p.ThumbnailURL
		h.render(w, r, "parts/part_edit.html", map[string]any{
			"Part": pf, "IsNew": false, "Error": "Error saving part: " + err.Error(),
			"Units": units, "Categories": h.st().partCategories,
			"ActiveTab": "parts", "ActiveSubTab": "edit",
			"NavBackURL": backURL, "NavBackLabel": backLabel,
			"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
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

// releaseStatusOrUnderReview coerces a release_status to "U" (Under Review) unless it
// is one of the valid codes A/D. A part with no explicit (or an out-of-range) status is
// Under Review — never implicitly active/released. Applied on write (so the DB never
// receives a blank or a value the CK_part_number_release_status CHECK would reject) and
// on read (so legacy/old-binary blank rows present as Under Review everywhere) (#542).
func releaseStatusOrUnderReview(s string) string {
	if s == "A" || s == "D" {
		return s
	}
	return "U"
}

// trackingModeFromForm reads the tracking_mode form value, defaulting to "none"
// and clamping to the four valid values so the CK_part_number_tracking_mode CHECK
// can never be violated by an unexpected submission (#745).
func trackingModeFromForm(r *http.Request) string {
	switch fv(r, "tracking_mode") {
	case "lot", "serial", "lot_serial":
		return fv(r, "tracking_mode")
	default:
		return "none"
	}
}

// partFromForm rebuilds a Part struct from POST form values (for re-displaying on error).
func partFromForm(r *http.Request) models.Part {
	mode := trackingModeFromForm(r)
	p := models.Part{
		PartNumber: fv(r, "part_number"), Revision: fv(r, "revision"),
		Description: fv(r, "description"), Detail: fv(r, "detail"), Category: fv(r, "category"),
		ReleaseStatus: releaseStatusOrUnderReview(fv(r, "release_status")), IsActive: activeFromStatus(r),
		RequestedBy: fv(r, "PNReqBy"), Notes: fv(r, "PNNotes"),
		UserField1: fv(r, "user_field_1"), UserField2: fv(r, "user_field_2"), UserField3: fv(r, "user_field_3"),
		UserField4: fv(r, "user_field_4"), UserField5: fv(r, "user_field_5"), UserField6: fv(r, "user_field_6"),
		UserField7: fv(r, "user_field_7"), UserField8: fv(r, "user_field_8"), UserField9: fv(r, "user_field_9"),
		UserField10:  fv(r, "user_field_10"),
		TrackingMode: mode,
		IsLotTracked: models.TracksLots(mode),
	}
	if v := fv(r, "current_cost"); v != "" {
		if c, err := parseDecimal(v); err == nil {
			p.CurrentCost = c
		}
	}
	if v := fv(r, "PNUNID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.UnitID = &n
		}
	}
	if v := fv(r, "reorder_min"); v != "" {
		if f, err := parseDecimal(v); err == nil {
			p.ReorderMin = &f
		}
	}
	return p
}

// fetchPartFull fetches the whole part row: the detail page and the edit form.
func (h *Handler) fetchPartFull(ctx context.Context, id string) (models.Part, error) {
	pid, err := strconv.Atoi(id)
	if err != nil {
		return models.Part{}, err
	}
	p, err := h.parts().GetPart(ctx, pid)
	if err != nil {
		return models.Part{}, err
	}
	return models.Part{ID: p.ID, PartNumber: p.PartNumber, Revision: p.Revision, Description: p.Description,
		Detail: p.Detail, Category: p.Category, HasBOM: p.HasBOM,
		ReleaseStatus: releaseStatusOrUnderReview(p.ReleaseStatus), IsActive: p.IsActive,
		RequestedBy: p.RequestedBy, Notes: p.Notes, CreatedDate: p.CreatedDate, ModifiedDate: p.ModifiedDate,
		PrimaryAttachmentID: p.PrimaryAttachmentID, CurrentCost: p.CurrentCost, LastRollupCost: p.LastRollupCost,
		LastRollupAt: p.LastRollupAt, AttachmentCount: p.AttachmentCount, POLineCount: p.POLineCount,
		UnitID: p.UnitID, UnitAbbr: p.UnitAbbr, StockOnHand: p.StockOnHand, ReorderMin: p.ReorderMin,
		TrackingMode: p.TrackingMode, IsLotTracked: models.TracksLots(p.TrackingMode),
		UserField1: p.UserField1, UserField2: p.UserField2, UserField3: p.UserField3, UserField4: p.UserField4,
		UserField5: p.UserField5, UserField6: p.UserField6, UserField7: p.UserField7, UserField8: p.UserField8,
		UserField9: p.UserField9, UserField10: p.UserField10}, nil
}

// partInput is the create/update input for the editable fields of p (from partFromForm).
func partInput(p models.Part) parts.Part {
	return parts.Part{PartNumber: p.PartNumber, Revision: p.Revision, Description: p.Description,
		Detail: p.Detail, Category: p.Category, ReleaseStatus: p.ReleaseStatus, IsActive: p.IsActive,
		RequestedBy: p.RequestedBy, Notes: p.Notes, UnitID: p.UnitID, CurrentCost: p.CurrentCost,
		ReorderMin: p.ReorderMin, TrackingMode: p.TrackingMode,
		UserField1: p.UserField1, UserField2: p.UserField2, UserField3: p.UserField3, UserField4: p.UserField4,
		UserField5: p.UserField5, UserField6: p.UserField6, UserField7: p.UserField7, UserField8: p.UserField8,
		UserField9: p.UserField9, UserField10: p.UserField10}
}

// ── Sub-tab handlers ────────────────────────────────────────────────────────

// bomLeafCost picks the per-line unit cost and CostSource label for a BOM row,
// shared by the read-only BOM view (PartBOM) and CSV export (BOMExportCSV).
// Assembly rows use the stored rollup; leaf rows prefer the preferred-supplier
// price (0 = none), else current_cost — labelled "labor" for OPS lines whose current_cost
// is an hourly rate. Mirrors the leaf-cost rule in rollupCost.
func bomLeafCost(childHasBOM bool, lastRollupCost decimal.Decimal, preferredPrice decimal.Decimal, currentCost decimal.Decimal, category string) (decimal.Decimal, string) {
	if childHasBOM {
		if lastRollupCost.IsPositive() {
			return lastRollupCost, "rollup"
		}
		return decimal.Zero, "missing"
	}
	if preferredPrice.IsPositive() {
		return preferredPrice, "price"
	}
	if currentCost.IsPositive() {
		if category == "OPS" {
			return currentCost, "labor"
		}
		return currentCost, "current_cost"
	}
	return decimal.Zero, "missing"
}

// fetchBOMItems returns partID's BOM rows, with line costs, and their total, shared by the
// read-only BOM view (PartBOM), its lazy-loaded children endpoint
// (APIPartBOMChildren), the BOM editor and the CSV export.
func (h *Handler) fetchBOMItems(ctx context.Context, partID string) ([]models.BOMItem, decimal.Decimal, error) {
	pid, err := strconv.Atoi(partID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	lines, err := h.parts().ListBOMLines(ctx, pid)
	if err != nil {
		return nil, decimal.Zero, err
	}
	var items []models.BOMItem
	var bomTotal decimal.Decimal
	for _, l := range lines {
		item := models.BOMItem{ID: l.ID, LineNumber: l.LineNumber, Qty: l.Qty, ComponentPartID: l.ComponentPartID,
			PartNumber: l.PartNumber, Description: l.Description, Revision: l.Revision, Category: l.Category,
			CurrentCost: l.CurrentCost, AttachCount: l.AttachmentCount, POLineCount: l.POLineCount,
			LastRollupCost: l.LastRollupCost, ChildHasBOM: l.HasBOM}
		item.LineUnitCost, item.CostSource = bomLeafCost(l.HasBOM, l.LastRollupCost, l.PreferredPrice, l.CurrentCost, l.Category)
		item.LineExtCost = item.LineUnitCost.Mul(item.Qty)
		bomTotal = bomTotal.Add(item.LineExtCost)
		items = append(items, item)
	}
	return items, bomTotal, nil
}

func (h *Handler) PartBOM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) PartWhereUsed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "where-used")
	if !ok {
		return
	}
	used, err := h.parts().ListWhereUsed(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving where-used: "+err.Error())
		return
	}
	var items []models.BOMItem
	for _, u := range used {
		items = append(items, models.BOMItem{LineNumber: u.LineNumber, Qty: u.Qty, ParentPartID: u.ParentPartID,
			PartNumber: u.PartNumber, Description: u.Description, Revision: u.Revision, Category: u.Category})
	}
	h.render(w, r, "parts/part_where_used.html", map[string]any{
		"Part": p, "WhereUsedItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "where-used",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
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
		before, after, ok := strings.Cut(rest, "][")
		if !ok {
			continue
		}
		id := before
		field := strings.TrimSuffix(after, "]")
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
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	edges, err := h.parts().ListBOMEdges(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	items := make([]models.BOMItem, len(edges))
	for i, e := range edges {
		items[i] = models.BOMItem{ID: e.ID, LineNumber: e.LineNumber, Qty: e.Qty, ComponentPartID: e.ComponentPartID,
			PartNumber: e.PartNumber, Description: e.Description}
	}
	p.LastRollupCost, p.LastRollupAt, _ = h.parts().GetPartRollup(r.Context(), p.ID)
	h.render(w, r, "parts/part_bom_edit.html", map[string]any{
		"Part": p, "BOMItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "bom",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"TestMode": h.cfg().TestMode, "CSRFToken": h.csrfToken(w, r),
	})
}

// ── PartBOMSave — POST /part/{id}/bom ───────────────────────────────────────

func (h *Handler) PartBOMSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := h.requireTab(w, r, id, "bom")
	if !ok {
		return
	}
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

	svc := parts.New(tx)
	// line resolves a form row to a BOM line; ComponentPartID 0 means the
	// component didn't resolve (no id and no matching part number).
	line := func(row bomRow) parts.BOMLine {
		item, _ := strconv.Atoi(row.Item)
		qty, _ := parseDecimal(row.Qty)
		var pnid int
		if row.PNID != "" {
			pnid, _ = strconv.Atoi(row.PNID)
		}
		if pnid == 0 && row.PartPN != "" {
			ref, _ := h.parts().GetPartByNumber(r.Context(), row.PartPN)
			pnid = ref.ID
		}
		return parts.BOMLine{LineNumber: item, Qty: qty, ComponentPartID: pnid}
	}

	deleteSet := map[string]bool{}
	for _, plidStr := range r.Form["delete_pl[]"] {
		deleteSet[plidStr] = true
		plid, err := strconv.Atoi(plidStr)
		if err == nil {
			err = svc.DeleteBOMLine(r.Context(), plid, p.ID)
		}
		if err != nil {
			h.renderError(w, r, "Error deleting BOM row: "+err.Error())
			return
		}
	}

	for plidStr, row := range extractBOMRows(r.Form, "pl") {
		if deleteSet[plidStr] {
			continue
		}
		l := line(row)
		if l.ComponentPartID == 0 {
			continue
		}
		var err error
		if l.ID, err = strconv.Atoi(plidStr); err == nil {
			err = svc.UpdateBOMLine(r.Context(), p.ID, l)
		}
		if err != nil {
			h.renderError(w, r, "Error updating BOM row: "+err.Error())
			return
		}
	}

	for _, row := range extractBOMRows(r.Form, "new_pl") {
		if row.PartPN == "" && row.PNID == "" {
			continue
		}
		l := line(row)
		if l.ComponentPartID == 0 {
			continue
		}
		if err := svc.CreateBOMLine(r.Context(), p.ID, l); err != nil {
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

// ── BOM paste-import preview ────────────────────────────────────────────────

// bomPastePreviewRow is one parsed/resolved row of a pasted BOM paste,
// carrying everything the preview template and the Confirm-Import JS need.
type bomPastePreviewRow struct {
	PartNumber  string  // canonical part_number from the DB match, or the raw pasted text on error
	Description string
	Qty         decimal.Decimal
	Status      string // "new" | "update" | "noop" | "error"
	RowClass    string // Bootstrap row class for the status
	StatusLabel string
	PLID        int // existing bom.id for "update"/"noop" rows, 0 otherwise
	PNID        int // resolved component_part_id, 0 on error
	RawText     string // original pasted line, shown for error rows
}

// parseBOMPasteText splits pasted TSV text into (partNumber, qtyText, rawLine)
// triples, sniffing off row 1 as a header when its qty column doesn't parse
// as a number (issue #53). Blank lines are skipped. Pure/no I/O — kept
// separate from PartBOMPastePreview so the parsing rule is unit-testable
// without a DB.
type bomPasteLine struct {
	PartNumber string
	QtyText    string
	Qty        decimal.Decimal
	QtyOK      bool
	RawText    string
}

func parseBOMPasteText(text string) []bomPasteLine {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var trimmedLines []string
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			trimmedLines = append(trimmedLines, t)
		}
	}
	var out []bomPasteLine
	for i, trimmed := range trimmedLines {
		cols := strings.SplitN(trimmed, "\t", 2)
		partNumber := strings.TrimSpace(cols[0])
		qtyText := ""
		if len(cols) > 1 {
			qtyText = strings.TrimSpace(cols[1])
		}
		qty, err := parseDecimal(qtyText)
		// Row 1 is sniffed as a header and skipped only when there's at least
		// one more row to import — a single-line paste is always treated as
		// data, even with a malformed qty column, so a one-line typo surfaces
		// as an error row instead of silently vanishing as a "header".
		if i == 0 && len(trimmedLines) > 1 && err != nil {
			continue
		}
		out = append(out, bomPasteLine{
			PartNumber: partNumber, QtyText: qtyText, Qty: qty, QtyOK: err == nil, RawText: trimmed,
		})
	}
	return out
}

// PartBOMPastePreview — POST /part/{id}/bom/preview. Parses pasted TSV
// part-number+qty rows, resolves each part number against the parts table,
// and diffs against the part's current BOM by component_part_id. Writes
// nothing; returns an HTML preview fragment (parts/part_bom_paste_preview.html).
func (h *Handler) PartBOMPastePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Not h.requireTab: that renders a full page via h.renderError on failure,
	// but this handler is fetched by JS and its response is dropped straight
	// into a small preview <div> — a full-page error response would end up
	// dumping the whole app shell into that div instead of a clean message.
	p, err := h.fetchPartBasic(r.Context(), id)
	if err == sql.ErrNoRows {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "Error retrieving part", err)
		return
	}
	h.applyCategoryTabs(r.Context(), &p)
	if !tabVisible(p, "bom") {
		http.Error(w, "The bom section does not apply to "+p.Category+" parts.", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		log.Printf("Error parsing form: %v", err)
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	lines, err := h.parts().ListBOMEdges(r.Context(), p.ID)
	if err != nil {
		serverError(w, "Error retrieving BOM", err)
		return
	}
	existing := map[int]parts.BOMEdge{}
	for _, l := range lines {
		existing[l.ComponentPartID] = l
	}

	var preview []bomPastePreviewRow
	hasError := false
	for _, line := range parseBOMPasteText(r.FormValue("paste_text")) {
		row := bomPastePreviewRow{PartNumber: line.PartNumber, RawText: line.RawText}

		var ref parts.PartRef
		if line.PartNumber != "" {
			if ref, err = h.parts().GetPartByNumber(r.Context(), line.PartNumber); err != nil && err != sql.ErrNoRows {
				serverError(w, "Error looking up part number", err)
				return
			}
		}
		pnid := ref.ID

		switch {
		case pnid == 0:
			row.Status, row.RowClass, row.StatusLabel = "error", "table-danger", "Error"
			hasError = true
		case !line.QtyOK:
			row.Status, row.RowClass, row.StatusLabel = "error", "table-danger", "Error"
			hasError = true
		default:
			row.PartNumber = ref.PartNumber
			row.Description = ref.Description
			row.Qty = line.Qty
			row.PNID = pnid
			if ex, ok := existing[pnid]; ok {
				row.PLID = ex.ID
				if ex.Qty.Equal(line.Qty) {
					row.Status, row.RowClass, row.StatusLabel = "noop", "table-secondary text-muted", "No change"
				} else {
					row.Status, row.RowClass, row.StatusLabel = "update", "table-warning", "Update"
				}
			} else {
				row.Status, row.RowClass, row.StatusLabel = "new", "table-success", "New"
			}
		}
		preview = append(preview, row)
	}

	h.renderPrint(w, "parts/part_bom_paste_preview.html", map[string]any{
		"Rows": preview, "HasError": hasError,
	})
}

// ── BOM cost rollup ──────────────────────────────────────────────────────────

type rollupResult struct {
	cost  decimal.Decimal
	cycle bool
}

// rollupCost computes the rolled-up cost for part pnid from the whole BOM tree,
// loaded in one query. visited is path-scoped for cycle detection; memo is global
// to the walk, and once a node is computed its result is reused.
func (h *Handler) rollupCost(ctx context.Context, pnid int, visited map[int]bool, memo map[int]rollupResult) (rollupResult, error) {
	tree, err := h.parts().ListBOMTree(ctx, pnid)
	if err != nil {
		return rollupResult{}, err
	}
	return rollupWalk(tree, pnid, visited, memo), nil
}

// rollupWalk is rollupCost's in-memory recursion over a ListBOMTree result.
// visited is path-scoped (defer-deleted on return) for cycle detection.
func rollupWalk(tree map[int][]parts.BOMTreeEdge, pnid int, visited map[int]bool, memo map[int]rollupResult) rollupResult {
	if visited[pnid] {
		return rollupResult{cycle: true}
	}
	if res, ok := memo[pnid]; ok {
		return res
	}
	visited[pnid] = true
	defer delete(visited, pnid)

	var total decimal.Decimal
	var hasCycle bool
	for _, e := range tree[pnid] {
		var unitCost decimal.Decimal
		if e.HasBOM {
			res := rollupWalk(tree, e.ComponentID, visited, memo)
			if res.cycle {
				hasCycle = true
			}
			unitCost = res.cost
		} else {
			unitCost, _ = bomLeafCost(false, decimal.Zero, e.PreferredPrice, e.CurrentCost, "")
		}
		total = total.Add(unitCost.Mul(e.Qty))
	}

	result := rollupResult{cost: total, cycle: hasCycle}
	memo[pnid] = result
	return result
}

// ── PartRollupCost — POST /part/{id}/rollup-cost ─────────────────────────────

func (h *Handler) PartRollupCost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.requireTab(w, r, id, "bom"); !ok {
		return
	}
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
	// sub-assemblies) in one statement, so now() gives every assembly the same timestamp.
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
	costs := make(map[int]decimal.Decimal, len(memo))
	for partID, result := range memo {
		costs[partID] = result.cost
	}
	if err := parts.New(tx).SetPartRollups(r.Context(), costs); err != nil {
		h.renderError(w, r, "Error saving rollup cost: "+err.Error())
		return
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
	PriceEA  decimal.Decimal
	PackSize decimal.Decimal
}

// pickTier selects the tier with the largest PackSize <= qty. Returns ok=false
// if qty is below every tier's PackSize (or there are no tiers at all).
func pickTier(tiers []priceTier, qty decimal.Decimal) (unitPrice, packSize decimal.Decimal, ok bool) {
	found := false
	for _, t := range tiers {
		if t.PackSize.LessThanOrEqual(qty) && (!found || t.PackSize.GreaterThan(packSize)) {
			unitPrice, packSize, found = t.PriceEA, t.PackSize, true
		}
	}
	return unitPrice, packSize, found
}

type buildCostLine struct {
	PNID        int
	PartNumber  string
	Description string
	QtyNeeded   decimal.Decimal
	PackSize    decimal.Decimal
	UnitPrice   decimal.Decimal
	ExtCost     decimal.Decimal
	Source      string // "price" | "missing"
}

type buildCostResult struct {
	Lines []buildCostLine
	Total decimal.Decimal
	Cycle bool
}

// aggregateLeafQty walks the BOM tree from pnid, multiplying qty by parentQty at
// each level, and sums extended quantity into leaves (parts with no BOM) by pnid.
// The tree is loaded in one query; visited is path-scoped for cycle detection,
// matching rollupCost.
func (h *Handler) aggregateLeafQty(ctx context.Context, pnid int, parentQty decimal.Decimal, visited map[int]bool, leaves map[int]decimal.Decimal) (bool, error) {
	tree, err := h.parts().ListBOMTree(ctx, pnid)
	if err != nil {
		return false, err
	}
	return leafQtyWalk(tree, pnid, parentQty, visited, leaves), nil
}

// leafQtyWalk is aggregateLeafQty's in-memory recursion over a ListBOMTree result.
func leafQtyWalk(tree map[int][]parts.BOMTreeEdge, pnid int, parentQty decimal.Decimal, visited map[int]bool, leaves map[int]decimal.Decimal) bool {
	if visited[pnid] {
		return true
	}
	visited[pnid] = true
	defer delete(visited, pnid)

	var hasCycle bool
	for _, e := range tree[pnid] {
		extQty := e.Qty.Mul(parentQty)
		if e.HasBOM {
			if leafQtyWalk(tree, e.ComponentID, extQty, visited, leaves) {
				// A cycle anywhere in the tree makes the whole walk's result
				// discardable (buildCost returns Cycle:true without using
				// leaves), so skip the rest of this node's siblings.
				hasCycle = true
				break
			}
		} else {
			leaves[e.ComponentID] = leaves[e.ComponentID].Add(extQty)
		}
	}
	return hasCycle
}

// partSupplierKey identifies one part+supplier pairing, used to key batched
// price-tier lookups in buildCost's Pass 2.
type partSupplierKey struct {
	PartID     int
	SupplierID int
}

// buildCost computes the consolidated cost to build qty units of pnid: it
// aggregates each leaf part's total demand across every occurrence in the tree
// (Pass 1), then prices each leaf once at its aggregated qty using the largest
// qualifying pack_size tier for the part's default supplier (Pass 2). Leaves
// below every tier's pack_size are reported as "missing" — no current_cost
// fallback, no extrapolation. Pass 2 batches its part and price lookups, so
// pricing N leaves costs O(1) round trips instead of O(N).
func (h *Handler) buildCost(ctx context.Context, pnid int, qty decimal.Decimal) (buildCostResult, error) {
	leaves := map[int]decimal.Decimal{}
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
	partInfo, err := h.parts().BuildCostParts(ctx, ids)
	if err != nil {
		return buildCostResult{}, err
	}
	tierRows, err := h.parts().ActivePriceTiers(ctx, ids)
	if err != nil {
		return buildCostResult{}, err
	}
	priceTiers := map[partSupplierKey][]priceTier{}
	for _, t := range tierRows {
		key := partSupplierKey{PartID: t.PartID, SupplierID: t.SupplierID}
		priceTiers[key] = append(priceTiers[key], priceTier{PriceEA: t.PriceEA, PackSize: t.PackSize})
	}

	var result buildCostResult
	for childID, totalQty := range leaves {
		info := partInfo[childID]
		line := buildCostLine{
			PNID: childID, PartNumber: info.PartNumber, Description: info.Description,
			QtyNeeded: totalQty, Source: "missing",
		}

		if info.DefaultSupplierID != nil {
			tiers := priceTiers[partSupplierKey{PartID: childID, SupplierID: *info.DefaultSupplierID}]
			if unitPrice, packSize, ok := pickTier(tiers, totalQty); ok {
				line.UnitPrice = unitPrice
				line.PackSize = packSize
				line.ExtCost = unitPrice.Mul(totalQty)
				line.Source = "price"
			}
		}

		result.Total = result.Total.Add(line.ExtCost)
		result.Lines = append(result.Lines, line)
	}
	sort.Slice(result.Lines, func(i, j int) bool {
		return result.Lines[i].PartNumber < result.Lines[j].PartNumber
	})
	return result, nil
}

// ── PartBuildCost — GET /part/{id}/build-cost?qty=N ─────────────────────────

func (h *Handler) PartBuildCost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	pnid, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Invalid part ID")
		return
	}
	qty, err := parseDecimal(r.URL.Query().Get("qty"))
	if err != nil || !qty.IsPositive() {
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
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) PartAttachments(w http.ResponseWriter, r *http.Request) {
	h.renderPartAttachments(w, r, r.PathValue("id"), nil)
}

// renderPartAttachments loads a part's attachments and renders the attachments
// page. extra is merged into the template data (used to surface errors or an
// import-collision prompt on the POST path).
func (h *Handler) renderPartAttachments(w http.ResponseWriter, r *http.Request, id string, extra map[string]any) {
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "attachments")
	if !ok {
		return
	}
	rows, err := h.attachments().ListPartAttachments(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving attachments: "+err.Error())
		return
	}
	var atts []models.Attachment
	nextOrderID := 1
	for _, a := range rows {
		att := models.Attachment{ID: a.ID, FileName: a.FileName, Category: a.Category,
			PartRevision: a.PartRevision, OrderID: a.SortOrder, Comment: a.Comment, VendorName: a.VendorName}
		switch {
		case a.SupplierPartID != nil:
			att.VendorScope = fmt.Sprintf("s:%d", *a.SupplierPartID)
		case a.MfgPartID != nil:
			att.VendorScope = fmt.Sprintf("m:%d", *a.MfgPartID)
		}
		if a.SortOrder != nil && *a.SortOrder+1 > nextOrderID {
			nextOrderID = *a.SortOrder + 1
		}
		atts = append(atts, att)
	}
	// Fall back to the ImportCollision's AttID when there's no ?edit= query
	// param, so an edit-triggered collision keeps showing the same row's
	// Edit form instead of it disappearing from this direct (non-redirect) render.
	editID := r.URL.Query().Get("edit")
	if editID == "" {
		editID = attIDFromExtra(extra, "ImportCollision", "DuplicateWarning")
	}
	var editingAtt *models.Attachment
	if editID != "" {
		for i := range atts {
			if fmt.Sprintf("%d", atts[i].ID) == editID {
				editingAtt = &atts[i]
				break
			}
		}
	}
	var hasThumbnail bool
	for i := range atts {
		if atts[i].Category == thumbnailCategory {
			hasThumbnail = true
			break
		}
	}
	cats := h.loadAttachmentCategories(r.Context())
	if cats == nil {
		cats = []string{} // keep the JS ATT_CATEGORIES an array
	}
	catsJSON, _ := json.Marshal(cats)
	vendorOptions, err := h.fetchVendorScopeOptions(r, id)
	if err != nil {
		h.renderError(w, r, "Error retrieving linked vendors: "+err.Error())
		return
	}
	data := map[string]any{
		"Part": p, "Attachments": atts, "EditingAtt": editingAtt,
		"ActiveTab": "parts", "ActiveSubTab": "attachments",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken":                h.csrfToken(w, r),
		"AttachmentCategories":     cats,
		"AttachmentCategoriesJSON": template.JS(catsJSON),
		"VendorScopeOptions":       vendorOptions,
		"DocControlConfigured": h.cfg().DocControlRoot != "",
		"HasThumbnail":         hasThumbnail,
		"TestMode":             h.cfg().TestMode,
		"NextOrderID":          nextOrderID,
	}
	maps.Copy(data, extra)
	h.render(w, r, "parts/part_attachments.html", data)
}

// insertAttachmentRow inserts a single part_attachment row. Shared by
// PartAttachmentCreate's single-file path and importAttachmentBatch (#70).
func (h *Handler) insertAttachmentRow(ctx context.Context, partID, fileName, rev, category string, oID *int, comment string, supplierPartID, mfgPartID *int, hash string) error {
	pid, err := strconv.Atoi(partID)
	if err != nil {
		return err
	}
	return h.attachmentTx(ctx, func(s *attachments.Service) error {
		if err := s.CreatePartAttachment(ctx, attachments.PartAttachment{PartID: pid, FileName: fileName,
			PartRevision: rev, Category: category, SortOrder: oID, Comment: comment,
			SupplierPartID: supplierPartID, MfgPartID: mfgPartID, Hash: hash}); err != nil {
			return err
		}
		return s.EnsurePartPrimary(ctx, pid)
	})
}

func (h *Handler) PartAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Cancel from a duplicate-hash warning (#71): the file was already copied
	// into Doc Control by the time the duplicate was detected, so discard it
	// unless some other active row already links the same name.
	if link := fv(r, "discard_import"); link != "" {
		if urlutil.IsLocalFile(link) && !urlutil.IsLocalDir(link) {
			_ = deleteAttachmentFileIfUnshared(r.Context(), h.partFileInUse,
				0, link, h.cfg().DocControlRoot, urlutil.StripLocalPrefix(link))
		}
		http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
		return
	}

	oID := intPtrOrNil(fv(r, "order_id"))
	rev := fv(r, "FILPNRev")
	comment := fv(r, "comment")

	supplierPartID, mfgPartID, err := h.resolveVendorScope(r.Context(), id, fv(r, "vendor_scope"))
	if err != nil {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": err.Error()})
		return
	}

	// Batch resume: a collision decision ("Link to existing file" or Cancel)
	// posted back from a mid-batch collision page (#70). batch_dir identifies
	// the temp staging directory startAttachmentBatch created for the
	// original multi-file POST. Must run before the len(ups) check below: the
	// resume POST from Cancel carries no upload_file at all, and must not
	// fall into the plain zero-file/manual-link path.
	if dir := fv(r, "batch_dir"); dir != "" {
		h.resumeAttachmentBatch(w, r, id, rev, comment, oID, supplierPartID, mfgPartID, dir)
		return
	}

	ups := attachmentUploads(r, "upload_file")
	if len(ups) > 1 {
		// Each file in a batch gets its own category (rather than the
		// single-file form's shared Category field): buildAttachmentFileName
		// only varies by category/rev/ext, so files sharing a category (and
		// extension) would generate identical names and collide with each
		// other, not just with pre-existing files (#70).
		var categories []string
		if r.MultipartForm != nil {
			categories = r.MultipartForm.Value["batch_category"]
		}
		if len(categories) != len(ups) {
			h.renderPartAttachments(w, r, id, map[string]any{"Error": "Please select a category for every file."})
			return
		}
		for _, cat := range categories {
			if cat == "" {
				h.renderPartAttachments(w, r, id, map[string]any{"Error": "Please select a category for every file."})
				return
			}
			if isGeneratedCategory(cat) {
				h.renderPartAttachments(w, r, id, map[string]any{"Error": fmt.Sprintf(
					"Category %q is reserved for generated PDF thumbnails; please choose a different category.", cat)})
				return
			}
		}
		h.startAttachmentBatch(w, r, id, rev, comment, oID, supplierPartID, mfgPartID, ups, categories)
		return
	}

	category := fv(r, "category")
	if isGeneratedCategory(category) {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": fmt.Sprintf(
			"Category %q is reserved for generated PDF thumbnails; please choose a different category.", category)})
		return
	}

	var upload *attachmentUploadSource
	if len(ups) == 1 {
		u := multipartUploadSource(ups[0])
		upload = &u
	}
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, "", upload, true)
	if in.ErrMsg != "" {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}

	hash := computeAttachmentHash(h.cfg().DocControlRoot, in.FileName)
	if h.partAttachmentDuplicateWarning(w, r, id, "", hash, 0, upload, in.FileName, category, rev, comment) {
		return
	}

	if err := h.insertAttachmentRow(r.Context(), id, in.FileName, rev, category, oID, comment, supplierPartID, mfgPartID, hash); err != nil {
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

// partAttachmentDuplicateWarning runs the #71 duplicate-hash check shared by
// PartAttachmentCreate and PartAttachmentUpdate. On a match it renders the
// dismissible warning banner and returns true (the caller must return
// immediately without writing); on no match, or confirm_duplicate=1, it
// returns false. attID is "" for the create path.
func (h *Handler) partAttachmentDuplicateWarning(w http.ResponseWriter, r *http.Request, id, attID, hash string, excludeID int, upload *attachmentUploadSource, fileName, category, rev, comment string) bool {
	if fv(r, "confirm_duplicate") == "1" {
		return false
	}
	dup, err := h.findDuplicatePartAttachment(r.Context(), hash, excludeID)
	if err != nil {
		h.renderError(w, r, "Error checking for duplicate attachments: "+err.Error())
		return true
	}
	if dup == nil {
		return false
	}
	imported := ""
	if upload != nil && urlutil.IsLocalFile(fileName) {
		imported = "1"
	}
	h.renderPartAttachments(w, r, id, map[string]any{"DuplicateWarning": map[string]string{
		"FileName": fileName,
		"DupLabel": dup.Label,
		"DupURL":   dup.URL,
		"Category": category, "Rev": rev, "OrderID": fv(r, "order_id"),
		"Comment": comment, "VendorScope": fv(r, "vendor_scope"),
		"Imported": imported,
		"AttID":    attID,
	}})
	return true
}

func (h *Handler) PartAttachmentUpdate(w http.ResponseWriter, r *http.Request) {
	id, attID := r.PathValue("id"), r.PathValue("attID")
	attIDInt, _ := strconv.Atoi(attID)
	oID := intPtrOrNil(fv(r, "order_id"))
	rev, category := fv(r, "FILPNRev"), fv(r, "category")
	comment := fv(r, "comment")

	supplierPartID, mfgPartID, scopeErr := h.resolveVendorScope(r.Context(), id, fv(r, "vendor_scope"))
	if scopeErr != nil {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": scopeErr.Error()})
		return
	}

	partID, err := strconv.Atoi(id)
	if err != nil {
		h.renderError(w, r, "Error loading attachment: "+err.Error())
		return
	}
	old, err := h.attachments().GetPartAttachment(r.Context(), attIDInt, partID)
	if err != nil {
		h.renderError(w, r, "Error loading attachment: "+err.Error())
		return
	}
	// Only reject when the category is actually changing into a reserved value —
	// re-saving a row that's already the generated one (e.g. editing its comment)
	// must keep working, since that's not a new collision.
	if isGeneratedCategory(category) && category != old.Category {
		h.renderPartAttachments(w, r, id, map[string]any{"Error": fmt.Sprintf(
			"Category %q is reserved for generated PDF thumbnails; please choose a different category.", category)})
		return
	}
	oldFileName := old.FileName
	var replaceName string
	if urlutil.IsLocalFile(oldFileName) {
		replaceName = urlutil.StripLocalPrefix(oldFileName)
	}

	var upload *attachmentUploadSource
	if ups := attachmentUploads(r, "upload_file"); len(ups) > 0 {
		u := multipartUploadSource(ups[0])
		upload = &u
	}
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, replaceName, upload, true)
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
	// contentChanged also covers replacing a file in place under the same
	// generated name (resolveAttachmentFileInput's replaceLocalFileFrom
	// branch): fileChanged is false there since in.FileName == oldFileName,
	// but the bytes on disk did change, so hash must still be recomputed.
	contentChanged := fileChanged || upload != nil
	var hash string
	if contentChanged {
		hash = computeAttachmentHash(h.cfg().DocControlRoot, in.FileName)
		if h.partAttachmentDuplicateWarning(w, r, id, attID, hash, attIDInt, upload, in.FileName, category, rev, comment) {
			return
		}
	}
	att := attachments.PartAttachment{ID: attIDInt, PartRevision: rev, Category: category, SortOrder: oID,
		Comment: comment, SupplierPartID: supplierPartID, MfgPartID: mfgPartID, FileName: in.FileName, Hash: hash}
	if contentChanged {
		err = h.attachments().UpdatePartAttachmentFile(r.Context(), att)
	} else {
		err = h.attachments().UpdatePartAttachment(r.Context(), att)
	}
	if err != nil {
		h.renderError(w, r, "Error updating attachment: "+err.Error())
		return
	}

	if fileChanged && replaceName != "" {
		if err := deleteAttachmentFileIfUnshared(r.Context(), h.partFileInUse,
			attIDInt, oldFileName, h.cfg().DocControlRoot, replaceName); err != nil {
			h.renderPartAttachments(w, r, id, map[string]any{
				"Error": "Attachment updated, but the old file could not be removed: " + err.Error()})
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	attIDInt, _ := strconv.Atoi(r.PathValue("attID"))
	partID, err := strconv.Atoi(id)
	if err == nil {
		err = h.attachmentTx(r.Context(), func(s *attachments.Service) error {
			if err := s.DeletePartAttachment(r.Context(), attIDInt, partID); err != nil {
				return err
			}
			return s.EnsurePartPrimary(r.Context(), partID)
		})
	}
	if err != nil {
		h.renderError(w, r, "Error deleting attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

func (h *Handler) PartSetPrimaryAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idInt, _ := strconv.Atoi(id)
	filID := r.FormValue("filid")
	var val *int
	if n, err2 := strconv.Atoi(filID); err2 == nil && n != 0 {
		val = &n
	}
	if err := h.attachments().SetPartPrimary(r.Context(), idInt, val); err != nil {
		h.renderError(w, r, "Error setting primary attachment: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
}

// APIPartLocalAttachments — GET /api/part/{id}/local-attachments (#156)
// Returns LOCAL: file (not directory) attachments for a part, for the PO import picker.
func (h *Handler) APIPartLocalAttachments(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	rows, err := h.attachments().ListPartAttachments(r.Context(), id)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	type att struct {
		ID       int    `json:"id"`
		BaseName string `json:"base_name"`
	}
	out := []att{}
	for _, row := range rows {
		a := att{ID: row.ID}
		fn := row.FileName
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
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "orders")
	if !ok {
		return
	}
	orders, err := h.parts().ListPartOrders(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving orders: "+err.Error())
		return
	}
	var items []models.PurchaseOrderLine
	for _, o := range orders {
		items = append(items, models.PurchaseOrderLine{PONumber: o.PONumber, SupplierName: o.SupplierName,
			DateOrdered: o.DateOrdered, DateClosed: o.DateClosed, Status: o.Status, LineNumber: o.LineNumber,
			Qty: o.Qty, UnitCost: o.UnitCost, Description: o.Description, VendorPN: o.VendorPN})
	}
	h.render(w, r, "parts/part_orders.html", map[string]any{
		"Part": p, "OrderItems": items,
		"ActiveTab": "parts", "ActiveSubTab": "orders",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// PartRecords — GET /part/{id}/records. Lists every active test record across
// every form where subject part_id = this part (#875).
func (h *Handler) PartRecords(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "records")
	if !ok {
		return
	}
	typeOptions, err := h.scopedRecordTypeOptions(r.Context(), records.ScopePart, p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving record types: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_records.html", map[string]any{
		"Part": p, "TypeOptions": typeOptions,
		"ActiveTab": "parts", "ActiveSubTab": "records",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// PartRecordsRows — GET /api/part/{id}/records/rows. JSON rows for PartRecords'
// table (#875).
func (h *Handler) PartRecordsRows(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := h.scopedRecordsRows(r.Context(), records.ScopePart, id)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	writeJSON(w, out)
}

// partTxnSummary is one row in the Part dashboard "Inventory" card (#521).
type partTxnSummary struct {
	Type string
	Qty  decimal.Decimal
	Date string
}

// recentPartTxns returns the most recent inventory transactions for a part,
// newest first, capped at limit. Returns nil on error.
func (h *Handler) recentPartTxns(ctx context.Context, partID int, limit int) []partTxnSummary {
	rows, err := h.parts().ListRecentTxns(ctx, partID, limit)
	if err != nil {
		return nil
	}
	var out []partTxnSummary
	for _, t := range rows {
		out = append(out, partTxnSummary{Type: t.Type, Qty: t.Qty, Date: t.Date.Format("2006-01-02")})
	}
	return out
}

// preferredSupplierSummary is the part detail dashboard's Preferred Supplier
// card (#55): the part's preferred supplier (part.default_supplier_id, #465)
// plus its supplier_part reference fields. HasLink is false when the supplier
// is pinned but no supplier_part row exists for it yet.
type preferredSupplierSummary struct {
	SupplierID   int
	SupplierName string
	SupplierPN   string
	SupplierDesc string
	HasLink      bool
	Price        *decimal.Decimal // cheapest active price from this supplier; nil = none
}

// preferredSupplier loads the part's preferred supplier and its supplier_part
// reference row for the Preferred Supplier card (#55). Returns nil when no
// preferred supplier is pinned (the JOIN drops the row on a NULL
// default_supplier_id). Price is filled in by the caller.
func (h *Handler) preferredSupplier(ctx context.Context, partID int) *preferredSupplierSummary {
	s, err := h.parts().GetPreferredSupplier(ctx, partID)
	if err != nil {
		return nil
	}
	return &preferredSupplierSummary{SupplierID: s.SupplierID, SupplierName: s.SupplierName,
		SupplierPN: s.SupplierPN, SupplierDesc: s.SupplierDesc, HasLink: s.HasLink}
}

// pricePoint is one unit-cost-over-time sample for the price-history chart (#284),
// shared by the Price History tab and the Part dashboard trend card (#521).
type pricePoint struct {
	Date     string   `json:"date"` // YYYY-MM-DD
	Cost     decimal.Decimal  `json:"cost"`
	PO       string           `json:"po"`
	Supplier string           `json:"supplier"`
	Source   string           `json:"source"`             // "po" | "price"
	PackSize *decimal.Decimal `json:"packSize,omitempty"` // qty-break tier, "price" source only (#612)
}

// partPricePoints assembles the unit-cost-over-time samples for a part from its
// PO lines and active price-list entries, chronological within each source.
// Shared by PartPriceHistory and PartDetail (#521).
func (h *Handler) partPricePoints(ctx context.Context, partID int) []pricePoint {
	var points []pricePoint
	svc := h.parts()

	if rows, err := svc.ListPOPricePoints(ctx, partID); err == nil {
		for _, r := range rows {
			points = append(points, pricePoint{
				Date: r.DateOrdered.Format("2006-01-02"), Cost: r.UnitCost,
				PO: r.PONumber, Supplier: r.SupplierName, Source: "po",
			})
		}
	}

	if rows, err := svc.ListPriceListPoints(ctx, partID); err == nil {
		for _, r := range rows {
			points = append(points, pricePoint{
				Date: r.EffectiveDate.Format("2006-01-02"), Cost: r.PriceEA,
				Supplier: r.SupplierName, Source: "price", PackSize: r.PackSize,
			})
		}
	}
	return points
}

// PartPriceHistory renders the Price History tab: a unit-cost-over-time chart
// sourced from this part's PO lines (one point per line) plus any active
// price-list entries (#284). Points are emitted as JSON for the SVG renderer in
// static/parts/price_history.js.
func (h *Handler) PartPriceHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "price-history")
	if !ok {
		return
	}

	points := h.partPricePoints(r.Context(), p.ID)

	data, _ := json.Marshal(points)
	h.render(w, r, "parts/part_price_history.html", map[string]any{
		"Part": p, "PriceDataJSON": template.JS(data), "HasData": len(points) > 0,
		"ActiveTab": "parts", "ActiveSubTab": "price-history",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
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
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "pricing")
	if !ok {
		return
	}
	prices, err := h.parts().ListPartPrices(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving pricing: "+err.Error())
		return
	}
	seen := map[int]int{}
	var groups []SupplierPriceGroup
	for _, pp := range prices {
		row := modelPrice(pp)
		if idx, ok := seen[pp.SupplierID]; ok {
			groups[idx].Rows = append(groups[idx].Rows, row)
		} else {
			seen[pp.SupplierID] = len(groups)
			groups = append(groups, SupplierPriceGroup{SupplierID: pp.SupplierID, SupplierName: pp.SupplierName, Rows: []models.Price{row}})
		}
	}
	defSup, _ := h.parts().GetDefaultSupplier(r.Context(), p.ID)
	for i := range groups {
		allInactive := true
		for _, r := range groups[i].Rows {
			if r.IsActive {
				allInactive = false
				break
			}
		}
		groups[i].AllInactive = allInactive
		groups[i].IsPreferred = defSup != nil && groups[i].SupplierID == *defSup
	}
	h.render(w, r, "parts/part_pricing.html", map[string]any{
		"Part": p, "PriceGroups": groups,
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ── Price CRUD ───────────────────────────────────────────────────────────────

// modelPrice maps a price row to the pricing templates' model.
func modelPrice(pp parts.PartPrice) models.Price {
	sid := pp.SupplierID
	return models.Price{ID: pp.ID, SupplierID: &sid, PriceEA: pp.PriceEA, PricePack: pp.PricePack,
		PackSize: pp.PackSize, IsActive: pp.IsActive, EffectiveDate: pp.EffectiveDate, SupplierName: pp.SupplierName}
}

// isDuplicatePrice reports whether err is UQ_price_active_combo rejecting a
// second active price for a supplier and pack size. Postgres folds the index
// name to lowercase, so match case-insensitively.
func isDuplicatePrice(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "uq_price")
}

func (h *Handler) PriceNew(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "pricing")
	if !ok {
		return
	}
	h.render(w, r, "parts/part_pricing_form.html", map[string]any{
		"Part": p, "Price": models.Price{}, "IsNew": true,
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

// ensureDefaultSupplier pins supplierID as the part's preferred supplier for cost
// rollup the first time a price is added (#465). No-op once one is already set.
func (h *Handler) ensureDefaultSupplier(ctx context.Context, partID, supplierID int) {
	_ = h.parts().EnsureDefaultSupplier(ctx, partID, supplierID)
}

// PricePreferred — POST /part/{id}/pricing/preferred. Sets the preferred supplier
// used for cost rollup (the multi-supplier review case from migration #484).
func (h *Handler) PricePreferred(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	p, ok := h.requireTab(w, r, partID, "pricing")
	if !ok {
		return
	}
	supplierID, err := strconv.Atoi(r.FormValue("supplier_id"))
	if err != nil || supplierID == 0 {
		h.renderError(w, r, "Invalid supplier")
		return
	}
	if err := h.parts().SetDefaultSupplier(r.Context(), p.ID, supplierID); err != nil {
		h.renderError(w, r, "Error setting preferred supplier: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceCreate(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	p, ok := h.requireTab(w, r, partID, "pricing")
	if !ok {
		return
	}
	supplierID, err := strconv.Atoi(r.FormValue("supplier_id"))
	if err != nil || supplierID == 0 {
		h.renderError(w, r, "Invalid supplier")
		return
	}
	effectiveDate := r.FormValue("effective_date")
	if effectiveDate == "" {
		effectiveDate = h.userNow(r).Format("2006-01-02")
	}
	priceEA, pricePack := resolvePriceFields(r)
	err = h.parts().CreatePrice(r.Context(), p.ID, supplierID, nullableDecimal(r.FormValue("pack_size")), priceEA, pricePack, effectiveDate)
	if err != nil {
		if isDuplicatePrice(err) {
			h.renderError(w, r, "A price already exists for this supplier and pack size. Deactivate the existing row first.")
			return
		}
		h.renderError(w, r, "Error saving price: "+err.Error())
		return
	}
	h.ensureDefaultSupplier(r.Context(), p.ID, supplierID)
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceEdit(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	priceID := r.PathValue("priceID")
	p, backURL, backLabel, ok := h.partPageBase(w, r, partID, "pricing")
	if !ok {
		return
	}
	var pp parts.PartPrice
	pid, err := strconv.Atoi(priceID)
	if err == nil {
		pp, err = h.parts().GetPartPrice(r.Context(), pid, p.ID)
	}
	if err == sql.ErrNoRows {
		h.renderError(w, r, "Price not found")
		return
	}
	if err != nil {
		h.renderError(w, r, "Error retrieving price: "+err.Error())
		return
	}
	h.render(w, r, "parts/part_pricing_form.html", map[string]any{
		"Part": p, "Price": modelPrice(pp), "IsNew": false,
		"ActiveTab": "parts", "ActiveSubTab": "pricing",
		"NavBackURL": backURL, "NavBackLabel": backLabel,
		"CSRFToken": h.csrfToken(w, r), "TestMode": h.cfg().TestMode,
	})
}

func (h *Handler) PriceUpdate(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	p, ok := h.requireTab(w, r, partID, "pricing")
	if !ok {
		return
	}
	supplierID, err := strconv.Atoi(r.FormValue("supplier_id"))
	if err != nil || supplierID == 0 {
		h.renderError(w, r, "Invalid supplier")
		return
	}
	effectiveDate := r.FormValue("effective_date")
	if effectiveDate == "" {
		effectiveDate = h.userNow(r).Format("2006-01-02")
	}
	tx, err := h.beginTx(r.Context())
	if err != nil {
		h.renderError(w, r, "Error starting transaction: "+err.Error())
		return
	}
	svc := parts.New(tx)
	priceID, err := strconv.Atoi(r.PathValue("priceID"))
	if err == nil {
		err = svc.SetPriceActive(r.Context(), priceID, p.ID, false)
	}
	if err != nil {
		tx.Rollback()
		h.renderError(w, r, "Error updating price: "+err.Error())
		return
	}
	priceEA, pricePack := resolvePriceFields(r)
	err = svc.CreatePrice(r.Context(), p.ID, supplierID, nullableDecimal(r.FormValue("pack_size")), priceEA, pricePack, effectiveDate)
	if err != nil {
		tx.Rollback()
		if isDuplicatePrice(err) {
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
	h.ensureDefaultSupplier(r.Context(), p.ID, supplierID)
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceDeactivate(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	p, ok := h.requireTab(w, r, partID, "pricing")
	if !ok {
		return
	}
	priceID, err := strconv.Atoi(r.PathValue("priceID"))
	if err == nil {
		err = h.parts().SetPriceActive(r.Context(), priceID, p.ID, false)
	}
	if err != nil {
		h.renderError(w, r, "Error deactivating price: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

// PriceDelete hard-deletes a price row. Only deactivated rows may be deleted —
// active pricing must be deactivated first (#57).
func (h *Handler) PriceDelete(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	p, ok := h.requireTab(w, r, partID, "pricing")
	if !ok {
		return
	}
	priceID, err := strconv.Atoi(r.PathValue("priceID"))
	if err == nil {
		err = h.parts().DeleteInactivePrice(r.Context(), priceID, p.ID)
	}
	if err != nil {
		h.renderError(w, r, "Error deleting price: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/pricing", partID), http.StatusSeeOther)
}

func (h *Handler) PriceActivate(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	p, ok := h.requireTab(w, r, partID, "pricing")
	if !ok {
		return
	}
	priceID, err := strconv.Atoi(r.PathValue("priceID"))
	if err == nil {
		err = h.parts().SetPriceActive(r.Context(), priceID, p.ID, true)
	}
	if err != nil {
		if isDuplicatePrice(err) {
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
	parts, err := h.parts().ListPartsExport(r.Context())
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="parts.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Part Number", "Revision", "Description", "Detail", "Requested By", "Created Date", "Category", "Modified Date", "Active"})
	for _, p := range parts {
		_ = cw.Write([]string{p.PartNumber, p.Revision, p.Description, p.Detail, p.RequestedBy,
			recordsFormatDate(p.CreatedDate), p.Category, recordsFormatDate(p.ModifiedDate), strconv.FormatBool(p.IsActive)})
	}
	cw.Flush()
}

// ── BOMExportCSV — GET /part/{id}/bom/export.csv ────────────────────────────

func (h *Handler) BOMExportCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := h.requireTab(w, r, id, "bom")
	if !ok {
		return
	}
	parentPN := p.PartNumber
	if parentPN == "" {
		parentPN = id
	}
	items, _, err := h.fetchBOMItems(r.Context(), id)
	if err != nil {
		serverError(w, "database error", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+parentPN+`-bom.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Line #", "Qty", "Part Number", "Description", "Revision", "Category", "Unit Cost", "Ext Cost", "Cost Source"})
	for _, item := range items {
		_ = cw.Write([]string{
			fmt.Sprintf("%d", item.LineNumber),
			item.Qty.String(),
			item.PartNumber, item.Description, item.Revision, item.Category,
			item.LineUnitCost.StringFixed(2),
			item.LineExtCost.StringFixed(2),
			item.CostSource,
		})
	}
	cw.Flush()
}
