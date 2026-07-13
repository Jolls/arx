package main

import (
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

// buildShortage is one consumed component left below zero on hand after a build,
// shown as a non-blocking warning on the Build tab (builds are allowed to go
// negative; this just nudges the user to build or restock the component). PartID
// links each row to the component's page so the user can go fix it.
type buildShortage struct {
	PartID     int
	PartNumber string
	Stock      float64
}

// ── PartBuild — GET /part/{id}/build ─────────────────────────────────────────
//
// Build consumes the part's BOM component lines (inventory_transaction issues)
// and produces the output part (a receipt) in one transaction — the generic
// consume/produce flow of the #568 lot epic, with no lot awareness yet (#675).

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

	// After a build (redirected here with ?built=<qty>), list any inventory-tracked
	// component this part consumes that is now below zero on hand, as a nudge to
	// build or restock it.
	builtQty := r.URL.Query().Get("built")
	var shortages []buildShortage
	if builtQty != "" {
		srows, err := h.queryContext(r.Context(), fmt.Sprintf(`
			SELECT DISTINCT p.id, p.part_number, p.stock_on_hand, p.category
			FROM %s b JOIN %s p ON b.component_part_id = p.id
			WHERE b.parent_part_id = @p1 AND p.stock_on_hand < 0
		`, h.cfg.BOMTable(), h.cfg.PartsTable()), id)
		if err != nil {
			h.renderError(w, r, "Error checking component stock: "+err.Error())
			return
		}
		for srows.Next() {
			var s buildShortage
			var cat string
			if err := srows.Scan(&s.PartID, &s.PartNumber, &s.Stock, &cat); err != nil {
				srows.Close()
				h.renderError(w, r, "Error reading component stock: "+err.Error())
				return
			}
			if models.TabsForCategory(h.partCategories, cat).Inventory {
				shortages = append(shortages, s)
			}
		}
		srows.Close()
	}

	h.render(w, r, "parts/part_build.html", map[string]any{
		"Part": p, "Builds": builds, "Today": time.Now().Format("2006-01-02"),
		"BuiltQty": builtQty, "Shortages": shortages,
		"ActiveTab": "parts", "ActiveSubTab": "build",
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
	partID := p.PNID
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

	// Load the output part's BOM component lines with each component's category.
	// No lines → nothing to build.
	type bomLine struct {
		componentPartID int
		qty             float64
		category        string
	}
	bomRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT b.component_part_id, b.qty, p.category
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
		if err := bomRows.Scan(&l.componentPartID, &l.qty, &l.category); err != nil {
			bomRows.Close()
			h.renderError(w, r, "Error reading BOM: "+err.Error())
			return
		}
		lines = append(lines, l)
	}
	bomRows.Close()
	if len(lines) == 0 {
		h.renderError(w, r, "This part has no BOM, so there is nothing to build.")
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

	// Record the build event first so its id can label the ledger rows.
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
		if err := h.recordInventoryTxn(r, tx, l.componentPartID, "issue", -consumed, *buildDate, "", ledgerNote, nil); err != nil {
			h.renderError(w, r, "Error issuing component stock: "+err.Error())
			return
		}
	}

	// Produce the output part: receipt of the built quantity.
	if err := h.recordInventoryTxn(r, tx, partID, "receipt", qty, *buildDate, "", ledgerNote, nil); err != nil {
		h.renderError(w, r, "Error receiving built stock: "+err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving build: "+err.Error())
		return
	}
	committed = true
	http.Redirect(w, r, fmt.Sprintf("/part/%s/build?built=%g", id, qty), http.StatusFound)
}
