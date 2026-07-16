package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"arx/arx_go/models"
	"github.com/go-chi/chi/v5"
)

// BuildView is one past-build row for the Build tab's history table.
type BuildView struct {
	ID       int
	Qty      float64
	Date     string
	Username string
	Note     string
}

// BuildOption is one past build of a part, offered when linking a test record to
// the build that produced its unit (#677). Label is a human-readable identifier.
type BuildOption struct {
	ID    int
	Label string
}

// buildOptionLabel formats a build for a picker/badge, e.g. "Build #12 — qty 1 — 2026-05-25".
func buildOptionLabel(id int, qty float64, date sql.NullTime) string {
	label := fmt.Sprintf("Build #%d — qty %g", id, qty)
	if date.Valid {
		label += " — " + date.Time.Format("2006-01-02")
	}
	return label
}

// activeBuildsForPart returns a part's builds, newest first, for the test-record
// build picker (#677). Empty (not an error) when the part has no builds.
func (h *Handler) activeBuildsForPart(ctx context.Context, partID int) ([]BuildOption, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT id, qty, build_date FROM %s WHERE part_id = @p1 ORDER BY build_date DESC, id DESC
	`, h.cfg.BuildTable()), partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var builds []BuildOption
	for rows.Next() {
		var b BuildOption
		var qty float64
		var date sql.NullTime
		if err := rows.Scan(&b.ID, &qty, &date); err != nil {
			return nil, err
		}
		b.Label = buildOptionLabel(b.ID, qty, date)
		builds = append(builds, b)
	}
	return builds, rows.Err()
}

// fetchBuildOption loads a single build for a linked-build display. Returns nil
// (no error) when the build does not exist.
func (h *Handler) fetchBuildOption(ctx context.Context, buildID int) (*BuildOption, error) {
	var b BuildOption
	var qty float64
	var date sql.NullTime
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, qty, build_date FROM %s WHERE id = @p1`, h.cfg.BuildTable()), buildID).
		Scan(&b.ID, &qty, &date)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.Label = buildOptionLabel(b.ID, qty, date)
	return &b, nil
}

// buildComponent is one inventory-tracked BOM line of the output part, shown on the
// Build tab so the user sees what a build will consume before running it: quantity
// per assembly, current on-hand, and (for lot-tracked components, when the output is
// itself lot-tracked) a picker of active lots to consume.
type buildComponent struct {
	PartID       int
	PartNumber   string
	Title        string
	Category     string
	QtyPer       float64
	StockOnHand  float64
	IsLotTracked bool
	Lots         []LotOption // active lots to choose from; only populated when a lot pick is needed
}

// loadBuildComponents returns the output part's inventory-tracked BOM lines for the
// Build tab. Non-stocked categories (OPS labor, DOC, …) are skipped — they never
// draw stock, mirroring the build handler's own filter. Each lot-tracked component's
// active lots are loaded for the picker, since consuming a lot-tracked component
// always draws from a specific lot regardless of whether the output is lot-tracked.
func (h *Handler) loadBuildComponents(ctx context.Context, outputPartID int) ([]buildComponent, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(`
		SELECT b.component_part_id, p.part_number, p.title, p.category, b.qty, p.stock_on_hand, p.is_lot_tracked
		FROM %s b JOIN %s p ON b.component_part_id = p.id
		WHERE b.parent_part_id = @p1
		ORDER BY b.line_number
	`, h.cfg.BOMTable(), h.cfg.PartsTable()), outputPartID)
	if err != nil {
		return nil, err
	}
	var comps []buildComponent
	for rows.Next() {
		var c buildComponent
		var title sql.NullString
		var isLotTracked sql.NullBool
		if err := rows.Scan(&c.PartID, &c.PartNumber, &title, &c.Category, &c.QtyPer, &c.StockOnHand, &isLotTracked); err != nil {
			rows.Close()
			return nil, err
		}
		if !models.TabsForCategory(h.partCategories, c.Category).Inventory {
			continue // non-stocked line (labor/doc/…) — not consumed from stock.
		}
		c.Title = title.String
		c.IsLotTracked = isLotTracked.Bool
		comps = append(comps, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Load each lot-tracked component's active lots for the picker.
	for i := range comps {
		if !comps[i].IsLotTracked {
			continue
		}
		lots, err := h.activeLotsForPart(ctx, comps[i].PartID)
		if err != nil {
			return nil, err
		}
		comps[i].Lots = lots
	}
	return comps, nil
}

// ── PartBuild — GET /part/{id}/build ─────────────────────────────────────────
//
// Build consumes the part's BOM component lines (inventory_transaction issues)
// and produces the output part (a receipt) in one transaction (#675). When the
// output part is lot-tracked (#676) the form also picks a lot per lot-tracked
// component so the build can record lot genealogy.

func (h *Handler) PartBuild(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "build")
	if !ok {
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT id, qty, build_date, username, note
		FROM %s WHERE part_id = @p1 ORDER BY build_date DESC, id DESC
	`, h.cfg.BuildTable()), id)
	if err != nil {
		h.renderError(w, r, "Error retrieving builds: "+err.Error())
		return
	}
	defer rows.Close()
	var builds []BuildView
	for rows.Next() {
		var b BuildView
		var username, note sql.NullString
		var date sql.NullTime
		if err := rows.Scan(&b.ID, &b.Qty, &date, &username, &note); err != nil {
			h.renderError(w, r, "Error reading builds: "+err.Error())
			return
		}
		b.Username = username.String
		b.Note = note.String
		if date.Valid {
			b.Date = date.Time.Format("2006-01-02")
		}
		builds = append(builds, b)
	}

	// The BOM lines this build will consume, with current on-hand and a lot picker per
	// lot-tracked component. The lot column shows whenever any consumed component is
	// lot-tracked (independent of whether the output part is), since those components
	// are always drawn from a specific lot.
	comps, err := h.loadBuildComponents(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	showLotColumn := false
	for _, c := range comps {
		if c.IsLotTracked {
			showLotColumn = true
			break
		}
	}

	h.render(w, r, "parts/part_build.html", map[string]any{
		"Part": p, "Builds": builds, "Components": comps, "ShowLotColumn": showLotColumn,
		"Today":        time.Now().Format("2006-01-02"),
		"BuiltQty":     r.URL.Query().Get("built"),
		"ReturnRecord": r.URL.Query().Get("return_record"), // #677: link build back to the test record that launched it
		"ActiveTab":    "parts", "ActiveSubTab": "build",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg.TestMode,
	})
}

// ── PartBuildCreate — POST /part/{id}/build ──────────────────────────────────

func (h *Handler) PartBuildCreate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// Guard mirroring the Build tab's visibility: only inventory-tracked parts
	// with actual BOM lines can be built (blocks a direct POST to a FORM/other
	// non-stocked part).
	p, ok := h.requireTab(w, r, id, "build")
	if !ok {
		return
	}
	partID := p.ID
	outputLotTracked := p.IsLotTracked
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	qty, err := strconv.ParseFloat(fv(r, "qty"), 64)
	if err != nil || qty <= 0 {
		h.renderError(w, r, "Enter a positive build quantity.")
		return
	}
	note := fv(r, "note")
	buildDate := parseFormDate(fv(r, "build_date"))
	if buildDate == nil {
		now := time.Now()
		buildDate = &now
	}

	// Load the output part's BOM component lines with each component's category and
	// lot-tracking. No lines → nothing to build.
	type bomLine struct {
		componentPartID int
		partNumber      string
		qty             float64
		category        string
		isLotTracked    bool
	}
	bomRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT b.component_part_id, p.part_number, b.qty, p.category, p.is_lot_tracked
		FROM %s b JOIN %s p ON b.component_part_id = p.id
		WHERE b.parent_part_id = @p1
	`, h.cfg.BOMTable(), h.cfg.PartsTable()), partID)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	var lines []bomLine
	for bomRows.Next() {
		var l bomLine
		var isLotTracked sql.NullBool
		if err := bomRows.Scan(&l.componentPartID, &l.partNumber, &l.qty, &l.category, &isLotTracked); err != nil {
			bomRows.Close()
			h.renderError(w, r, "Error reading BOM: "+err.Error())
			return
		}
		l.isLotTracked = isLotTracked.Bool
		lines = append(lines, l)
	}
	bomRows.Close()
	if len(lines) == 0 {
		h.renderError(w, r, "This part has no BOM, so there is nothing to build.")
		return
	}

	// Every stocked lot-tracked component must have a valid active lot selected —
	// independent of whether the output is lot-tracked, since consuming a lot-tracked
	// component always draws from a specific lot. The picked lot is recorded on the
	// component's issue ledger row (inventory_transaction.lot_id), and — when the
	// output part is also lot-tracked — additionally as a lot_genealogy edge into the
	// output lot. Validate the picks up front, before writing anything.
	lotPicks := map[int]int{} // componentPartID → selected lot id
	for _, l := range lines {
		if !l.isLotTracked || !models.TabsForCategory(h.partCategories, l.category).Inventory {
			continue
		}
		lotID, err := strconv.Atoi(fv(r, fmt.Sprintf("lot[%d]", l.componentPartID)))
		if err != nil || lotID <= 0 {
			h.renderError(w, r, fmt.Sprintf("Select a lot to consume for lot-controlled component %s.", l.partNumber))
			return
		}
		lotPicks[l.componentPartID] = lotID
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

	// Record the build event first so its id can label the ledger rows and output lot.
	var buildID int
	insertBuild := h.dialect.InsertReturningID(h.cfg.BuildTable(),
		`part_id, output_lot_id, qty, build_date, username, note, created_at`,
		`@p1, @p2, @p3, @p4, @p5, @p6, @p7`,
		false)
	if err := tx.QueryRowContext(r.Context(), insertBuild,
		partID, nil, qty, *buildDate, h.actorName(r), nullableText(note), time.Now(),
	).Scan(&buildID); err != nil {
		h.renderError(w, r, "Error recording build: "+err.Error())
		return
	}

	// Lot-tracked output (#676): create the produced lot and link the build to it.
	// lot_number auto-defaults to the lot's own id (#687); lot_description records
	// the build as provenance.
	var outputLotID int
	if outputLotTracked {
		outputLotID, err = h.createLot(r.Context(), tx, partID,
			lotCreateArgs{Description: fmt.Sprintf("Build #%d", buildID)}, nil)
		if err != nil {
			h.renderError(w, r, "Error creating output lot: "+err.Error())
			return
		}
		if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
			`UPDATE %s SET output_lot_id = @p1 WHERE id = @p2`, h.cfg.BuildTable()),
			outputLotID, buildID); err != nil {
			h.renderError(w, r, "Error linking output lot: "+err.Error())
			return
		}
	}

	ledgerNote := fmt.Sprintf("Build #%d", buildID)
	if note != "" {
		ledgerNote += " — " + note
	}

	// Consume each component: issue (qty per assembly × build qty). Shortages are
	// allowed to go negative, matching manual stock adjustments (#675 scope).
	// Skip components whose category is not inventory-tracked (OPS labor, TOOL,
	// SVC, DOC, …) — they are not drawn from stock, so no ledger row is written.
	for _, l := range lines {
		if !models.TabsForCategory(h.partCategories, l.category).Inventory {
			continue
		}
		consumed := l.qty * qty
		if consumed == 0 {
			continue // degenerate BOM line (qty 0) — nothing to issue.
		}
		// Lot-tracked component: validate the picked lot and record it on the issue.
		var lotID *int
		if l.isLotTracked {
			picked := lotPicks[l.componentPartID]
			okLot, err := h.lotBelongsToPart(r.Context(), tx, picked, l.componentPartID)
			if err != nil {
				h.renderError(w, r, "Error validating component lot: "+err.Error())
				return
			}
			if !okLot {
				h.renderError(w, r, fmt.Sprintf("Selected lot is not an active lot of component %s.", l.partNumber))
				return
			}
			lotID = &picked
		}
		if err := h.recordInventoryTxn(r, tx, l.componentPartID, "issue", -consumed, *buildDate, "", ledgerNote, nil, lotID, &buildID); err != nil {
			h.renderError(w, r, "Error issuing component stock: "+err.Error())
			return
		}
		// When the output part is lot-tracked too, additionally link the consumed
		// component lot to the output lot as a genealogy edge (#676).
		if outputLotTracked && l.isLotTracked {
			if err := h.recordLotGenealogy(r.Context(), tx, *lotID, outputLotID, consumed); err != nil {
				h.renderError(w, r, "Error recording lot genealogy: "+err.Error())
				return
			}
		}
	}

	// Produce the output part: receipt of the built quantity, stamped with the output
	// lot when the output part is lot-tracked.
	var outputLotArg *int
	if outputLotTracked {
		outputLotArg = &outputLotID
	}
	if err := h.recordInventoryTxn(r, tx, partID, "receipt", qty, *buildDate, "", ledgerNote, nil, outputLotArg, &buildID); err != nil {
		h.renderError(w, r, "Error receiving built stock: "+err.Error())
		return
	}

	// #677: when this build was launched from a test record ("Build this unit"),
	// link the record to the build (and its output lot, if any) in the same tx so
	// the tested unit traces back to what it consumed. A stale/forged return_record
	// (or one whose part doesn't match) is skipped silently — it must never block a
	// build.
	linkedRecord := 0
	if rr := fv(r, "return_record"); rr != "" {
		if recID, convErr := strconv.Atoi(rr); convErr == nil {
			// Only link a WIP record of this same part; a stale/forged/locked
			// return_record is skipped silently — it must never block the build.
			var recPart int
			var recLocked bool
			err := tx.QueryRowContext(r.Context(), fmt.Sprintf(
				`SELECT COALESCE(part_number_id,0), is_locked FROM %s WHERE id = @p1`, h.cfg.RecordsTable()), recID).
				Scan(&recPart, &recLocked)
			if err == nil && recPart == partID && !recLocked {
				var lotArg interface{}
				if outputLotArg != nil {
					lotArg = *outputLotArg
				}
				if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(
					`UPDATE %s SET lot_id = @p1, build_id = @p2, updated_at = GETDATE() WHERE id = @p3`,
					h.cfg.RecordsTable()), lotArg, buildID, recID); err != nil {
					h.renderError(w, r, "Error linking build to test record: "+err.Error())
					return
				}
				linkedRecord = recID
			}
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving build: "+err.Error())
		return
	}
	committed = true
	if linkedRecord != 0 {
		// ?built=1 tells the record editor this is the return leg of "Build this unit"
		// so it can auto-restore the in-progress draft instead of prompting (#677).
		http.Redirect(w, r, fmt.Sprintf("/records/%d/edit?built=1", linkedRecord), http.StatusFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/part/%s/build?built=%g", id, qty), http.StatusFound)
}
