package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"arx/arx_go/models"
	"arx/internal/purchasing"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

// ── Create RFQs from an assembly's BOM (#99) ─────────────────────────────────

// rfqPart is the per-part data the RFQ planner needs.
type rfqPart struct {
	ID          int
	PartNumber  string
	Description string
	Revision    string
	Category    string
	Stock       decimal.Decimal
	ReorderMin  *decimal.Decimal
	SupplierID  sql.NullInt64
	HasBOM      bool
}

type bomEdge struct {
	child int
	qty   decimal.Decimal
}

// rfqLine is one purchased part to order: total Need across the BOM, and the
// computed order Qty after netting stock and reorder_min.
type rfqLine struct {
	Part rfqPart
	Need decimal.Decimal
	Qty  decimal.Decimal
}

type rfqSupplierGroup struct {
	SupplierID   int
	SupplierName string
	Lines        []rfqLine
}

type rfqPlan struct {
	Groups     []rfqSupplierGroup
	Unsupplied []rfqLine // need purchasing but have no default supplier
}

// planRFQLines flattens the BOM below root for n assemblies. Needs are summed
// across every path before netting, so a part used in several places is netted
// against stock once; non-purchased sub-assemblies are netted before exploding.
// Parts are visited parents-first (reverse DFS post-order) so each part's total
// need is final before it's netted. A BOM cycle is an error.
func planRFQLines(root int, n decimal.Decimal, edges map[int][]bomEdge, parts map[int]rfqPart, purchased func(category string) bool) ([]rfqLine, error) {
	const (
		visiting = 1
		done     = 2
	)
	state := map[int]int{}
	var order []int
	var visit func(id int) error
	visit = func(id int) error {
		switch state[id] {
		case visiting:
			if pn := parts[id].PartNumber; pn != "" {
				return fmt.Errorf("BOM cycle detected at part %s", pn)
			}
			return fmt.Errorf("BOM cycle detected at part id %d", id)
		case done:
			return nil
		}
		state[id] = visiting
		for _, e := range edges[id] {
			if err := visit(e.child); err != nil {
				return err
			}
		}
		state[id] = done
		order = append(order, id)
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}

	need := map[int]decimal.Decimal{root: n}
	var lines []rfqLine
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		x := need[id]
		if id != root {
			p := parts[id]
			if purchased(p.Category) {
				if x.GreaterThan(p.Stock) {
					qty := x.Sub(p.Stock)
					if p.ReorderMin != nil && p.ReorderMin.IsPositive() {
						qty = qty.Add(*p.ReorderMin)
					}
					lines = append(lines, rfqLine{Part: p, Need: x, Qty: qty})
				}
				continue
			}
			if !p.HasBOM {
				continue
			}
			x = decimal.Max(x.Sub(p.Stock), decimal.Zero)
		}
		for _, e := range edges[id] {
			need[e.child] = need[e.child].Add(x.Mul(e.qty))
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Part.PartNumber < lines[j].Part.PartNumber })
	return lines, nil
}

func (h *Handler) isPurchasedCategory(code string) bool {
	for _, c := range h.st().partCategories {
		if c.Code == code {
			return c.Purchased
		}
	}
	return false
}

// loadRFQGraph loads the BOM edges below root and the part data of every
// component. Purchased parts are leaves: their own BOMs are never loaded.
func (h *Handler) loadRFQGraph(ctx context.Context, root int) (map[int][]bomEdge, map[int]rfqPart, error) {
	edges := map[int][]bomEdge{}
	parts := map[int]rfqPart{}
	queue := []int{root}
	loaded := map[int]bool{root: true}
	svc := h.parts()
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		comps, err := svc.ListBOMComponents(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range comps {
			p := rfqPart{ID: c.ID, PartNumber: c.PartNumber, Description: c.Description, Revision: c.Revision,
				Category: c.Category, Stock: c.Stock, HasBOM: c.HasBOM}
			p.ReorderMin = c.ReorderMin
			if c.SupplierID != nil {
				p.SupplierID = sql.NullInt64{Int64: int64(*c.SupplierID), Valid: true}
			}
			parts[p.ID] = p
			edges[id] = append(edges[id], bomEdge{child: p.ID, qty: c.Qty})
			if p.HasBOM && !h.isPurchasedCategory(p.Category) && !loaded[p.ID] {
				loaded[p.ID] = true
				queue = append(queue, p.ID)
			}
		}
	}
	return edges, parts, nil
}

// buildRFQPlan computes the purchase lines for n assemblies of root and groups
// them by default supplier.
func (h *Handler) buildRFQPlan(ctx context.Context, root int, n decimal.Decimal) (rfqPlan, error) {
	edges, parts, err := h.loadRFQGraph(ctx, root)
	if err != nil {
		return rfqPlan{}, err
	}
	lines, err := planRFQLines(root, n, edges, parts, h.isPurchasedCategory)
	if err != nil {
		return rfqPlan{}, err
	}
	var plan rfqPlan
	bySupplier := map[int]*rfqSupplierGroup{}
	for _, l := range lines {
		if !l.Part.SupplierID.Valid {
			plan.Unsupplied = append(plan.Unsupplied, l)
			continue
		}
		sid := int(l.Part.SupplierID.Int64)
		g := bySupplier[sid]
		if g == nil {
			g = &rfqSupplierGroup{SupplierID: sid}
			bySupplier[sid] = g
			if name, err := h.purchasing().GetSupplierName(ctx, sid); err == nil {
				g.SupplierName = name
			}
		}
		g.Lines = append(g.Lines, l)
	}
	for _, g := range bySupplier {
		plan.Groups = append(plan.Groups, *g)
	}
	sort.Slice(plan.Groups, func(i, j int) bool { return plan.Groups[i].SupplierName < plan.Groups[j].SupplierName })
	return plan, nil
}

// ── PartCreateRFQs — GET /part/{id}/create-rfqs ──────────────────────────────
// Without ?n= shows the assembly-count prompt; with it, the editable preview.

func (h *Handler) PartCreateRFQs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, backURL, backLabel, ok := h.partPageBase(w, r, id, "bom")
	if !ok {
		return
	}
	data := map[string]any{
		"Part": p, "ActiveTab": "parts", "ActiveSubTab": "bom",
		"NavBackURL": backURL, "NavBackLabel": backLabel, "TestMode": h.cfg().TestMode,
		"CSRFToken": h.csrfToken(w, r),
	}
	if nStr := fv(r, "n"); nStr != "" {
		n, err := parseDecimal(nStr)
		if err != nil || !n.IsPositive() {
			data["Error"] = "Enter a number of assemblies greater than zero."
		} else if plan, err := h.buildRFQPlan(r.Context(), p.ID, n); err != nil {
			data["Error"] = err.Error()
		} else {
			data["N"], data["Plan"] = nStr, plan
		}
	}
	h.render(w, r, "parts/part_create_rfqs.html", data)
}

// ── PartCreateRFQsConfirm — POST /part/{id}/create-rfqs ──────────────────────
// Creates one RFQ per supplier from the previewed lines (parallel pid/qty
// fields; removed lines are simply absent). The plan is recomputed server-side,
// so only parts it produces can be ordered.

func (h *Handler) PartCreateRFQsConfirm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.requireTab(w, r, id, "bom")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, "Error parsing form: "+err.Error())
		return
	}
	n, err := parseDecimal(fv(r, "n"))
	if err != nil || !n.IsPositive() {
		h.renderError(w, r, "Invalid number of assemblies")
		return
	}
	plan, err := h.buildRFQPlan(r.Context(), p.ID, n)
	if err != nil {
		h.renderError(w, r, "Error planning RFQs: "+err.Error())
		return
	}
	qtys := map[int]decimal.Decimal{}
	for i, pid := range r.Form["pid"] {
		pidN, _ := strconv.Atoi(pid)
		if i < len(r.Form["qty"]) {
			if q, _ := parseDecimal(r.Form["qty"][i]); q.IsPositive() {
				qtys[pidN] = q
			}
		}
	}

	type created struct{ Number, SupplierName string }
	var made []created
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
	po := models.PurchaseOrder{}
	if u := h.currentUser(r); u != nil {
		po.Orderer = u.DisplayName
	}
	h.applyPODefaults(r, &po)
	for _, g := range plan.Groups {
		var lines []rfqLine
		for _, l := range g.Lines {
			if q, ok := qtys[l.Part.ID]; ok {
				l.Qty = q
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			continue
		}
		number, err := h.insertBOMRFQ(r, tx, g, lines, p, po)
		if err != nil {
			h.renderError(w, r, "Error creating RFQ for "+g.SupplierName+": "+err.Error())
			return
		}
		made = append(made, created{number, g.SupplierName})
	}
	if err := tx.Commit(); err != nil {
		h.renderError(w, r, "Error saving RFQs: "+err.Error())
		return
	}
	committed = true

	h.render(w, r, "parts/part_create_rfqs.html", map[string]any{
		"Part": p, "ActiveTab": "parts", "ActiveSubTab": "bom", "TestMode": h.cfg().TestMode,
		"Created": made, "Unsupplied": plan.Unsupplied,
	})
}

// insertBOMRFQ inserts one RFQ (status 'rfq', its own sequence number, group =
// itself) for supplier group g, with its lines and status-history row.
func (h *Handler) insertBOMRFQ(r *http.Request, tx *txLogger, g rfqSupplierGroup, lines []rfqLine, root models.Part, po models.PurchaseOrder) (string, error) {
	ctx := r.Context()
	base, err := h.purchasing().NextPONumber(ctx)
	if err != nil {
		return "", err
	}
	number := base + "R1"

	_, defaultContact, _ := h.purchasing().GetSupplierContactDefault(ctx, g.SupplierID)
	var sc ContactSummary
	if defaultContact != nil {
		for _, c := range h.contactsForSupplier(r, g.SupplierID) {
			if c.ID == *defaultContact {
				sc = c
				break
			}
		}
	}

	now := h.userNow(r)
	zero := decimal.Zero
	rfq := purchasing.PO{Number: number, Status: "rfq", IsActive: statusIsActive("rfq"), Orderer: po.Orderer,
		SupplierID: &g.SupplierID, SupplierName: g.SupplierName, SupplierContact: sc.DisplayName, SupplierEmail: sc.Email,
		SupplierAddress: sc.Address, SupplierCity: sc.City, SupplierState: sc.State, SupplierZipcode: sc.Zipcode,
		SupplierCountry: sc.Country, SupplierPhoneNumber: sc.Phone, SupplierFaxNumber: sc.Fax,
		ReceiverID: po.ReceiverID, ReceiverName: po.ReceiverName, ReceiverContact: po.ReceiverContact,
		ReceiverEmail: po.ReceiverEmail, ReceiverAddress: po.ReceiverAddress, ReceiverCity: po.ReceiverCity,
		ReceiverState: po.ReceiverState, ReceiverZipcode: po.ReceiverZipcode, ReceiverCountry: po.ReceiverCountry,
		ReceiverPhone: po.ReceiverPhone, ReceiverFax: po.ReceiverFax,
		Tax1: &zero, ShippingCost: &zero, MiscCost: &zero, InternalNotes: "Created from BOM of " + root.PartNumber,
		DateRequested: &now}
	if sc.ID > 0 {
		rfq.SupplierContactID = &sc.ID
	}
	pur := purchasing.New(tx)
	poID, err := pur.CreatePO(ctx, rfq)
	if err != nil {
		return "", err
	}
	if err := pur.SetRFQGroup(ctx, poID, poID); err != nil {
		return "", err
	}
	if err := pur.CreatePOStatusEvent(ctx, poID, nil, "rfq", h.actorName(r)); err != nil {
		return "", err
	}
	for i, l := range lines {
		partID := l.Part.ID
		if err := pur.CreatePOLine(ctx, poID, purchasing.POLine{LineNumber: i + 1, PartNumberSnapshot: l.Part.PartNumber,
			RevisionSnapshot: l.Part.Revision, Description: l.Part.Description, Qty: l.Qty, PartID: &partID}); err != nil {
			return "", err
		}
	}
	return number, nil
}
