package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"arx/arx_go/models"
	"arx/internal/inventory"
	"github.com/shopspring/decimal"
)

// BuildView is one past-build row for the Build tab's history table.
type BuildView struct {
	ID          int
	Qty         decimal.Decimal
	Date        string
	Username    string
	Note        string
	TestedCount int // # of serialized units created for this build so far (#745, Q6 completeness numerator; denominator is Qty)
}

// BuildOption is one past build of a part, offered when linking a test record to
// the build that produced its unit (#677). Label is a human-readable identifier.
type BuildOption = inventory.BuildOption

// activeBuildsForPart returns a part's builds, newest first, for the test-record
// build picker (#677). Empty (not an error) when the part has no builds.
func (h *Handler) activeBuildsForPart(ctx context.Context, partID int) ([]BuildOption, error) {
	return h.inventory().ListBuildOptions(ctx, partID)
}

// fetchBuildOption loads a single build for a linked-build display. Returns nil
// (no error) when the build does not exist.
func (h *Handler) fetchBuildOption(ctx context.Context, buildID int) (*BuildOption, error) {
	return h.inventory().GetBuildOption(ctx, buildID)
}

// buildComponent is one inventory-tracked BOM line of the output part, shown on the
// Build tab so the user sees what a build will consume before running it: quantity
// per assembly, current on-hand, and (for lot-tracked components, when the output is
// itself lot-tracked) a picker of active lots to consume.
type buildComponent struct {
	PartID       int
	PartNumber   string
	Description  string
	Category     string
	QtyPer       decimal.Decimal
	StockOnHand  decimal.Decimal
	IsLotTracked bool
	Lots         []LotOption // active lots to choose from; only populated when a lot pick is needed
}

// loadBuildComponents returns the output part's inventory-tracked BOM lines for the
// Build tab. Non-stocked categories (OPS labor, DOC, …) are skipped — they never
// draw stock, mirroring the build handler's own filter. Each lot-tracked component's
// active lots are loaded for the picker, since consuming a lot-tracked component
// always draws from a specific lot regardless of whether the output is lot-tracked.
func (h *Handler) loadBuildComponents(ctx context.Context, outputPartID int) ([]buildComponent, error) {
	rows, err := h.inventory().ListBuildComponents(ctx, outputPartID)
	if err != nil {
		return nil, err
	}
	var comps []buildComponent
	for _, row := range rows {
		if !models.TabsForCategory(h.st().partCategories, row.Category).Inventory {
			continue // non-stocked line (labor/doc/…) — not consumed from stock.
		}
		comps = append(comps, buildComponent{
			PartID: row.PartID, PartNumber: row.PartNumber, Description: row.Description,
			Category: row.Category, QtyPer: row.QtyPer, StockOnHand: row.StockOnHand,
			IsLotTracked: models.TracksLots(row.TrackingMode),
		})
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
	id := r.PathValue("id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "build")
	if !ok {
		return
	}
	// tested_count is the # of serialized units created for each build so far —
	// the Q6 completeness numerator (#745); the denominator is the build qty. Manual
	// units (#799: back-filled, no test record) are excluded — they were never tested.
	history, err := h.inventory().ListBuildHistory(r.Context(), p.ID)
	if err != nil {
		h.renderError(w, r, "Error retrieving builds: "+err.Error())
		return
	}
	var builds []BuildView
	for _, b := range history {
		builds = append(builds, BuildView{
			ID: b.ID, Qty: b.Qty, Date: b.Date.Format("2006-01-02"), Username: b.Username,
			Note: b.Note, TestedCount: b.TestedCount,
		})
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
		"Today":        h.userNow(r).Format("2006-01-02"),
		"BuiltQty":     r.URL.Query().Get("built"),
		"ReturnRecord": r.URL.Query().Get("return_record"), // #677: link build back to the test record that launched it
		"ActiveTab":    "parts", "ActiveSubTab": "build",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
	})
}

// bomLine is one BOM component line of an output part, loaded for a build: how much
// per assembly, its category (to skip non-stocked lines), and whether it's lot-tracked
// (so a specific lot must be consumed). Shared by the standalone Build tab and the
// build-at-test-time inline panel (#747).
type bomLine struct {
	componentPartID int
	partNumber      string
	qty             decimal.Decimal
	category        string
	isLotTracked    bool
}

// loadBuildLines returns the output part's BOM component lines for a build (#747
// extraction). Empty result (nil error) when the part has no BOM.
func (h *Handler) loadBuildLines(ctx context.Context, partID int) ([]bomLine, error) {
	rows, err := h.inventory().ListBuildLines(ctx, partID)
	if err != nil {
		return nil, err
	}
	var lines []bomLine
	for _, l := range rows {
		lines = append(lines, bomLine{
			componentPartID: l.ComponentPartID, partNumber: l.PartNumber, qty: l.Qty,
			category: l.Category, isLotTracked: models.TracksLots(l.TrackingMode),
		})
	}
	return lines, nil
}

// collectLotPicks gathers the selected lot per stocked lot-tracked component from the
// form field lot[<componentPartID>]. The returned string is a user-facing error
// message naming the first component missing a pick ("" when every pick is present);
// each lot's belongs-to-part validity is re-checked inside performBuild.
func (h *Handler) collectLotPicks(r *http.Request, lines []bomLine) (map[int]int, string) {
	lotPicks := map[int]int{}
	for _, l := range lines {
		if !l.isLotTracked || !models.TabsForCategory(h.st().partCategories, l.category).Inventory {
			continue
		}
		lotID, err := strconv.Atoi(fv(r, fmt.Sprintf("lot[%d]", l.componentPartID)))
		if err != nil || lotID <= 0 {
			return nil, fmt.Sprintf("Select a lot to consume for lot-controlled component %s.", l.partNumber)
		}
		lotPicks[l.componentPartID] = lotID
	}
	return lotPicks, ""
}

// performBuild writes one build inside an already-open transaction (#747 extraction of
// the Build tab's core, so build-at-test-time reuses the exact same
// consumption/receipt/genealogy path): records the build event, creates the output lot
// when the output part is lot-tracked, issues each stocked component (qty per assembly
// × qty, recording the picked lot and — into a lot-tracked output — a genealogy edge),
// and receipts the built quantity. Returns the new build id and (when lot-tracked) the
// output lot id. It does NOT begin/commit the tx or touch any test record — the caller
// owns those, so the same helper serves both the standalone build and the record save.
func (h *Handler) performBuild(r *http.Request, tx *txLogger, partID int, outputLotTracked bool, qty decimal.Decimal, buildDate time.Time, note string, lines []bomLine, lotPicks map[int]int) (buildID, outputLotID int, err error) {
	// Record the build event first so its id can label the ledger rows and output lot.
	inv := inventory.New(tx)
	if buildID, err = inv.CreateBuild(r.Context(), partID, qty, buildDate, h.actorName(r), note); err != nil {
		return 0, 0, fmt.Errorf("recording build: %w", err)
	}

	// Lot-tracked output (#676): create the produced lot and link the build to it.
	if outputLotTracked {
		outputLotID, err = h.createLot(r.Context(), tx, partID,
			lotCreateArgs{Description: fmt.Sprintf("Build #%d", buildID)}, nil)
		if err != nil {
			return 0, 0, fmt.Errorf("creating output lot: %w", err)
		}
		if err = inv.SetBuildOutputLot(r.Context(), buildID, outputLotID); err != nil {
			return 0, 0, fmt.Errorf("linking output lot: %w", err)
		}
	}

	ledgerNote := fmt.Sprintf("Build #%d", buildID)
	if note != "" {
		ledgerNote += " — " + note
	}

	// Consume each stocked component: issue (qty per assembly × build qty). Shortages
	// go negative (matching manual adjustments). Non-stocked lines (OPS/TOOL/SVC/DOC)
	// draw no stock, so no ledger row.
	for _, l := range lines {
		if !models.TabsForCategory(h.st().partCategories, l.category).Inventory {
			continue
		}
		consumed := l.qty.Mul(qty)
		if consumed.IsZero() {
			continue // degenerate BOM line (qty 0) — nothing to issue.
		}
		var lotID *int
		if l.isLotTracked {
			picked := lotPicks[l.componentPartID]
			okLot, lerr := h.lotBelongsToPart(r.Context(), tx, picked, l.componentPartID)
			if lerr != nil {
				return 0, 0, fmt.Errorf("validating component lot: %w", lerr)
			}
			if !okLot {
				return 0, 0, fmt.Errorf("selected lot is not an active lot of component %s", l.partNumber)
			}
			lotID = &picked
		}
		if err := h.recordInventoryTxn(r, tx, l.componentPartID, "issue", consumed.Neg(), buildDate, "", ledgerNote, nil, lotID, &buildID); err != nil {
			return 0, 0, fmt.Errorf("issuing component stock: %w", err)
		}
		if outputLotTracked && l.isLotTracked {
			if err := h.recordGenealogy(r.Context(), tx, *lotID, outputLotID, consumed); err != nil {
				return 0, 0, fmt.Errorf("recording lot genealogy: %w", err)
			}
		}
	}

	// Produce the output part: receipt of the built quantity, stamped with the output
	// lot when the output part is lot-tracked.
	var outputLotArg *int
	if outputLotTracked {
		outputLotArg = &outputLotID
	}
	if err := h.recordInventoryTxn(r, tx, partID, "receipt", qty, buildDate, "", ledgerNote, nil, outputLotArg, &buildID); err != nil {
		return 0, 0, fmt.Errorf("receiving built stock: %w", err)
	}
	return buildID, outputLotID, nil
}

// parseBuildQty parses and validates a positive build quantity from the request's "qty"
// field, shared by the standalone Build tab and the inline build-at-test-time panel (#867).
func parseBuildQty(r *http.Request) (decimal.Decimal, error) {
	qty, err := parseDecimal(fv(r, "qty"))
	if err != nil || !qty.IsPositive() {
		return decimal.Zero, fmt.Errorf("Enter a positive build quantity.")
	}
	return qty, nil
}

// ── PartBuildCreate — POST /part/{id}/build ──────────────────────────────────

func (h *Handler) PartBuildCreate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	qty, err := parseBuildQty(r)
	if err != nil {
		h.renderError(w, r, err.Error())
		return
	}
	note := fv(r, "note")
	buildDate := parseFormDate(fv(r, "build_date"))
	if buildDate == nil {
		now := h.userNow(r)
		buildDate = &now
	}

	lines, err := h.loadBuildLines(r.Context(), partID)
	if err != nil {
		h.renderError(w, r, "Error retrieving BOM: "+err.Error())
		return
	}
	if len(lines) == 0 {
		h.renderError(w, r, "This part has no BOM, so there is nothing to build.")
		return
	}
	lotPicks, pickErr := h.collectLotPicks(r, lines)
	if pickErr != "" {
		h.renderError(w, r, pickErr)
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

	// #191: lock the return record before performBuild locks part rows — the same
	// order SaveResults uses (record, then parts), so the two can't deadlock.
	if recID, convErr := strconv.Atoi(fv(r, "return_record")); convErr == nil {
		if err := inventory.New(tx).LockRecord(r.Context(), recID); err != nil {
			h.renderError(w, r, "Error locking test record: "+err.Error())
			return
		}
	}

	buildID, outputLotID, err := h.performBuild(r, tx, partID, outputLotTracked, qty, *buildDate, note, lines, lotPicks)
	if err != nil {
		h.renderError(w, r, "Error recording build: "+err.Error())
		return
	}

	// #677: when this build was launched from a test record ("Build this unit"),
	// link the record to the build (and its output lot, if any) in the same tx so
	// the tested unit traces back to what it consumed. A stale/forged/locked
	// return_record (or one whose part doesn't match) is skipped silently — it must
	// never block a build.
	linkedRecord := 0
	if rr := fv(r, "return_record"); rr != "" {
		if recID, convErr := strconv.Atoi(rr); convErr == nil {
			var lotArg *int
			if outputLotTracked {
				lotArg = &outputLotID
			}
			// #191: one conditional write, so a lock landing mid-build is honored.
			linked, err := inventory.New(tx).LinkRecordToBuild(r.Context(), recID, partID, lotArg, buildID)
			if err != nil {
				h.renderError(w, r, "Error linking build to test record: "+err.Error())
				return
			}
			if linked {
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
	http.Redirect(w, r, fmt.Sprintf("/part/%s/build?built=%s", id, qty), http.StatusFound)
}
