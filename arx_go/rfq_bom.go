package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"arx/arx_go/models"

	"github.com/go-chi/chi/v5"
)

// ── Create RFQs from an assembly's BOM (#99) ─────────────────────────────────

// rfqPart is the per-part data the RFQ planner needs.
type rfqPart struct {
	ID          int
	PartNumber  string
	Description string
	Revision    string
	Category    string
	Stock       float64
	ReorderMin  sql.NullFloat64
	SupplierID  sql.NullInt64
	HasBOM      bool
}

type bomEdge struct {
	child int
	qty   float64
}

// rfqLine is one purchased part to order: total Need across the BOM, and the
// computed order Qty after netting stock and reorder_min.
type rfqLine struct {
	Part rfqPart
	Need float64
	Qty  float64
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
func planRFQLines(root int, n float64, edges map[int][]bomEdge, parts map[int]rfqPart, purchased func(category string) bool) ([]rfqLine, error) {
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

	need := map[int]float64{root: n}
	var lines []rfqLine
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		x := need[id]
		if id != root {
			p := parts[id]
			if purchased(p.Category) {
				if x > p.Stock {
					qty := x - p.Stock
					if p.ReorderMin.Valid && p.ReorderMin.Float64 > 0 {
						qty += p.ReorderMin.Float64
					}
					lines = append(lines, rfqLine{Part: p, Need: x, Qty: qty})
				}
				continue
			}
			if !p.HasBOM {
				continue
			}
			x = math.Max(x-p.Stock, 0)
		}
		for _, e := range edges[id] {
			need[e.child] += x * e.qty
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
	pl, pn := h.cfg().BOMTable(), h.cfg().PartsTable()
	hasBOM := hasOwnBOMExpr(h.dia(), pl, "pn.id")
	edges := map[int][]bomEdge{}
	parts := map[int]rfqPart{}
	queue := []int{root}
	loaded := map[int]bool{root: true}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		rows, err := h.queryContext(ctx, fmt.Sprintf(`
			SELECT pn.id, pl.qty, pn.part_number, pn.description, pn.revision, pn.category,
			       pn.stock_on_hand, pn.reorder_min, pn.default_supplier_id, %s
			FROM %s pl
			JOIN %s pn ON pl.component_part_id = pn.id
			WHERE pl.parent_part_id = @p1
		`, hasBOM, pl, pn), id)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var p rfqPart
			var qty float64
			var desc, rev, cat sql.NullString
			var stock sql.NullFloat64
			var hb sql.NullBool
			if err := rows.Scan(&p.ID, &qty, &p.PartNumber, &desc, &rev, &cat, &stock, &p.ReorderMin, &p.SupplierID, &hb); err != nil {
				rows.Close()
				return nil, nil, err
			}
			p.Description, p.Revision, p.Category = desc.String, rev.String, cat.String
			p.Stock, p.HasBOM = stock.Float64, hb.Bool
			parts[p.ID] = p
			edges[id] = append(edges[id], bomEdge{child: p.ID, qty: qty})
			if p.HasBOM && !h.isPurchasedCategory(p.Category) && !loaded[p.ID] {
				loaded[p.ID] = true
				queue = append(queue, p.ID)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return edges, parts, nil
}

// buildRFQPlan computes the purchase lines for n assemblies of root and groups
// them by default supplier.
func (h *Handler) buildRFQPlan(ctx context.Context, root int, n float64) (rfqPlan, error) {
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
			h.queryRowContext(ctx, fmt.Sprintf(`SELECT name FROM %s WHERE id = @p1`, h.cfg().CompanyTable()), sid).Scan(&g.SupplierName)
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
		n, err := strconv.ParseFloat(nStr, 64)
		if err != nil || n <= 0 {
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
	n, err := strconv.ParseFloat(fv(r, "n"), 64)
	if err != nil || n <= 0 {
		h.renderError(w, r, "Invalid number of assemblies")
		return
	}
	plan, err := h.buildRFQPlan(r.Context(), p.ID, n)
	if err != nil {
		h.renderError(w, r, "Error planning RFQs: "+err.Error())
		return
	}
	qtys := map[int]float64{}
	for i, pid := range r.Form["pid"] {
		pidN, _ := strconv.Atoi(pid)
		if i < len(r.Form["qty"]) {
			if q, _ := strconv.ParseFloat(r.Form["qty"][i], 64); q > 0 {
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
	var base string
	if err := h.queryRowContext(ctx,
		fmt.Sprintf("SELECT CAST(%s AS VARCHAR)", h.dia().NextSequenceValueExpr("po_number_seq")),
	).Scan(&base); err != nil {
		return "", err
	}
	number := base + "R1"

	var defaultContact sql.NullInt64
	h.queryRowContext(ctx, fmt.Sprintf(`SELECT default_contact FROM %s WHERE id = @p1`, h.cfg().CompanyTable()), g.SupplierID).Scan(&defaultContact)
	var sc ContactSummary
	if defaultContact.Valid {
		for _, c := range h.contactsForSupplier(r, g.SupplierID) {
			if c.ID == int(defaultContact.Int64) {
				sc = c
				break
			}
		}
	}

	now := time.Now()
	var poID int
	insertPO := h.dia().InsertReturningID(h.cfg().POTable(),
		`number, status, is_active, orderer, account_id,
		 supplier_id, supplier_name, supplier_contact, supplier_email,
		 supplier_address, supplier_city, supplier_state, supplier_zipcode,
		 supplier_country, supplier_phone_number, supplier_fax_number,
		 receiver_id, receiver_name, receiver_contact, receiver_email,
		 receiver_address, receiver_city, receiver_state, receiver_zipcode,
		 receiver_country, receiver_phone, receiver_fax,
		 tax1, shipping_cost, misc_cost, notes, internal_notes, date_ordered,
		 date_requested, date_closed, total_cost,
		 supplier_contact_id, receiver_contact_id`,
		`@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,@p11,@p12,@p13,@p14,@p15,@p16,
		 @p17,@p18,@p19,@p20,@p21,@p22,@p23,@p24,@p25,@p26,@p27,
		 @p28,@p29,@p30,@p31,@p32,@p33,@p34,@p35,@p36,@p37,@p38`,
		true)
	var supplierContactID any
	if sc.ID > 0 {
		supplierContactID = sc.ID
	}
	var receiverID any
	if po.ReceiverID != nil {
		receiverID = *po.ReceiverID
	}
	if err := tx.QueryRowContext(ctx, insertPO,
		number, "rfq", statusIsActive("rfq"), po.Orderer, "",
		g.SupplierID, g.SupplierName, sc.DisplayName, sc.Email,
		sc.Address, sc.City, sc.State, sc.Zipcode,
		sc.Country, sc.Phone, sc.Fax,
		receiverID, po.ReceiverName, po.ReceiverContact, po.ReceiverEmail,
		po.ReceiverAddress, po.ReceiverCity, po.ReceiverState, po.ReceiverZipcode,
		po.ReceiverCountry, po.ReceiverPhone, po.ReceiverFax,
		0.0, 0.0, 0.0, "", "Created from BOM of "+root.PartNumber, nil,
		now, nil, 0.0,
		supplierContactID, nil,
	).Scan(&poID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET rfq_group_id=@p1 WHERE ID=@p2`, h.cfg().POTable()), poID, poID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (po_id, event_type, from_status, to_status, changed_by)
		VALUES (@p1, 'status', NULL, 'rfq', @p2)
	`, h.cfg().POHistoryTable()), poID, h.actorName(r)); err != nil {
		return "", err
	}
	for i, l := range lines {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (po_id, line_number, part_number_snapshot, revision_snapshot, description, qty, unit_cost, vendor_part_number, part_id)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9)
		`, h.cfg().POLineTable()), poID, i+1, l.Part.PartNumber, l.Part.Revision, l.Part.Description, l.Qty, 0.0, "", l.Part.ID); err != nil {
			return "", err
		}
	}
	return number, nil
}
