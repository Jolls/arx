//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// partOrdersFixture: P is a BUY part pinned to supplier 1002 with three POs (A dated, B dated with
// a NULL supplier name, C undated), three inventory moves, two 1002 supplier links, a price list
// mixing active/inactive/undated/NULL-price rows, and a spread of attachments. R is pinned to 1003
// with no supplier link; Q has no history at all.
type partOrdersFixture struct {
	P, Q, R    int
	PO         map[string]string // "A"/"B"/"C" → PO number
	Sup        map[int]string    // company id → name
	PrimaryAtt int
}

func seedPartOrders(t *testing.T, h *Handler) (f partOrdersFixture, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	pn, po, pol := "part", "purchase_order", "po_line"
	base := smokeUniq("IPO") // purchase_order.number is VARCHAR(32)
	f.PO = map[string]string{"A": base + "-A", "B": base + "-B", "C": base + "-C"}
	f.Sup = map[int]string{}
	var poIDs []int
	cleanup = func() {
		for _, id := range poIDs {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE po_id=$1`, pol), id)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, po), id)
		}
		for _, id := range []int{f.P, f.Q, f.R} {
			smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET primary_attachment_id=NULL WHERE id=$1`, pn), id)
			for _, tbl := range []string{"part_attachment", "inventory_transaction", "price", "supplier_part"} {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id=$1`, tbl), id)
			}
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, pn), id)
		}
	}
	scan := func(dst *int, q string, args ...any) {
		t.Helper()
		if err := h.queryRowContext(ctx, q, args...).Scan(dst); err != nil {
			cleanup()
			t.Fatalf("seed: %v", err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := h.execContext(ctx, q, args...); err != nil {
			cleanup()
			t.Fatalf("seed: %v", err)
		}
	}
	for _, id := range []int{1002, 1003, 1004} {
		var name string
		if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT name FROM %s WHERE id=$1`, "company"), id).Scan(&name); err != nil {
			t.Fatalf("company %d: %v", id, err)
		}
		f.Sup[id] = name
	}

	scan(&f.P, fmt.Sprintf(`INSERT INTO %s (part_number, category, default_supplier_id, current_cost, last_rollup_cost, last_rollup_at)
		VALUES ($1,'BUY',1002,2,1.8,'2026-02-03T04:05:06Z') RETURNING id`, pn), base+"-P")
	scan(&f.Q, fmt.Sprintf(`INSERT INTO %s (part_number, category) VALUES ($1,'BUY') RETURNING id`, pn), base+"-Q")
	scan(&f.R, fmt.Sprintf(`INSERT INTO %s (part_number, category, default_supplier_id) VALUES ($1,'BUY',1003) RETURNING id`, pn), base+"-R")

	for _, c := range []struct {
		key, sup, ordered, closed, status string
		supID                             int
		qty, cost                         float64
		desc, vpn                         any
	}{
		{"A", "Sup A", "2026-01-10", "2026-01-20", "closed", 1002, 5, 1.25, "first", "VPN1"},
		{"B", "", "2026-02-01", "", "open", 1003, 10, 1.1, nil, nil},
		{"C", "Sup C", "", "", "draft", 1002, 1, 9, "undated", "VPN3"},
	} {
		var id int
		scan(&id, fmt.Sprintf(`INSERT INTO %s (number, supplier_id, supplier_name, date_ordered, date_closed, status)
			VALUES ($1,$2,NULLIF($3,''),NULLIF($4,'')::date,NULLIF($5,'')::date,$6) RETURNING id`, po),
			f.PO[c.key], c.supID, c.sup, c.ordered, c.closed, c.status)
		poIDs = append(poIDs, id)
		exec(fmt.Sprintf(`INSERT INTO %s (po_id, part_id, line_number, qty, unit_cost, description, vendor_part_number)
			VALUES ($1,$2,1,$3,$4,$5,$6)`, pol), id, f.P, c.qty, c.cost, c.desc, c.vpn)
	}

	txn := "inventory_transaction"
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, txn_type, qty, txn_date) VALUES
		($1,'receipt',5,'2026-01-15'), ($1,'issue',-2,'2026-01-20'), ($1,'adjustment',1.5,'2026-01-20')`, txn), f.P)

	sp := "supplier_part"
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, supplier_id, supplier_pn, supplier_desc, preference) VALUES
		($1,1002,'PN-B',NULL,2), ($1,1002,'PN-A','Desc A',1), ($1,1003,'PN-X','other',0)`, sp), f.P)

	exec(fmt.Sprintf(`INSERT INTO %s (part_id, supplier_id, price_ea, pack_size, is_active, effective_date) VALUES
		($1,1002,2,1,TRUE,'2026-01-05'), ($1,1002,1.5,10,TRUE,'2026-01-06'), ($1,1002,0.5,100,FALSE,'2026-01-07'),
		($1,1003,1,1,TRUE,NULL), ($1,1004,NULL,NULL,TRUE,'2026-01-08')`, "price"), f.P)

	att := "part_attachment"
	scan(&f.PrimaryAtt, fmt.Sprintf(`INSERT INTO %s (part_id, file_name, category, part_revision, sort_order)
		VALUES ($1,'https://example.com/p.pdf','Drawing','B',1) RETURNING id`, att), f.P)
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, file_name, category, sort_order, is_active) VALUES
		($1,'LOCAL:itest\photo1.jpg','Photo',2,TRUE), ($1,'LOCAL:itest\thumb.png',$2,3,TRUE),
		($1,'LOCAL:itest\dir\',NULL,4,TRUE), ($1,'https://example.com/x',NULL,5,TRUE),
		($1,'LOCAL:itest\photo2.png',NULL,NULL,TRUE), ($1,'plain text',NULL,6,TRUE),
		($1,'LOCAL:itest\gone.jpg','Photo',0,FALSE)`, att), f.P, thumbnailCategory)
	exec(fmt.Sprintf(`UPDATE %s SET primary_attachment_id=$2 WHERE id=$1`, pn), f.P, f.PrimaryAtt)
	return f, cleanup
}

// TestIntegration_PartOrders_Renders: every PO line of the part, newest first with the undated PO
// on top, NULL text as blank; a part without lines shows the empty state.
func TestIntegration_PartOrders_Renders(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPartOrders(t, h)
	defer cleanup()

	get := func(id int) string {
		rec := httptest.NewRecorder()
		h.PartOrders(rec, withID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/part/%d/orders", id), nil), id))
		assertStatus(t, "PartOrders", rec, http.StatusOK)
		return rec.Body.String()
	}
	body := get(f.P)
	last := -1
	for _, k := range []string{"C", "B", "A"} {
		i := strings.Index(body, ">"+f.PO[k]+"</a>")
		if i < 0 || i < last {
			t.Errorf("PartOrders: PO %s missing or out of order", k)
		}
		last = i
	}
	for _, want := range []string{
		"<td>Sup A</td>", "<td>2026-01-10</td>", "<td>2026-01-20</td>", ">5.00</td>", ">$1.2500</td>", "<td>first</td>", "<td>VPN1</td>",
		"<td></td>\n                    <td>2026-02-01</td>", "<td>N/A</td>", ">$1.1000</td>",
		"3 order lines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PartOrders body missing %q", want)
		}
	}
	if !strings.Contains(get(f.Q), "No purchase order history found for this part.") {
		t.Error("PartOrders(Q): want empty state")
	}
}

// TestIntegration_PartDashboardCards pins the data behind the dashboard's Recent POs, Inventory,
// Preferred Supplier and price-trend cards.
func TestIntegration_PartDashboardCards(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPartOrders(t, h)
	defer cleanup()
	ctx := context.Background()

	type po struct {
		Number, Supplier, Status, Date string
		Qty, Cost                      float64
	}
	recent, err := h.parts().ListRecentPOs(ctx, f.P, 2)
	if err != nil {
		t.Fatal(err)
	}
	var gotPOs []po
	for _, s := range recent {
		d := ""
		if s.DateOrdered != nil {
			d = s.DateOrdered.Format("2006-01-02")
		}
		gotPOs = append(gotPOs, po{s.Number, s.SupplierName, s.Status, d, s.Qty, s.UnitCost})
	}
	wantPOs := []po{{f.PO["C"], "Sup C", "draft", "", 1, 9}, {f.PO["B"], "", "open", "2026-02-01", 10, 1.1}}
	if !reflect.DeepEqual(gotPOs, wantPOs) {
		t.Errorf("ListRecentPOs(P, 2):\n got %+v\nwant %+v", gotPOs, wantPOs)
	}
	if got, _ := h.parts().ListRecentPOs(ctx, f.P, 5); len(got) != 3 {
		t.Errorf("ListRecentPOs(P, 5) = %d rows, want 3", len(got))
	}
	if got, _ := h.parts().ListRecentPOs(ctx, f.Q, 5); len(got) != 0 {
		t.Errorf("ListRecentPOs(Q) = %+v, want none", got)
	}

	wantTxns := []partTxnSummary{{"adjustment", 1.5, "2026-01-20"}, {"issue", -2, "2026-01-20"}}
	if got := h.recentPartTxns(ctx, f.P, 2); !reflect.DeepEqual(got, wantTxns) {
		t.Errorf("recentPartTxns(P, 2):\n got %+v\nwant %+v", got, wantTxns)
	}
	if got := h.recentPartTxns(ctx, f.Q, 5); got != nil {
		t.Errorf("recentPartTxns(Q) = %+v, want nil", got)
	}

	for name, c := range map[string]struct {
		id   int
		want *preferredSupplierSummary
	}{
		"linked":   {f.P, &preferredSupplierSummary{SupplierID: 1002, SupplierName: f.Sup[1002], SupplierPN: "PN-A", SupplierDesc: "Desc A", HasLink: true}},
		"unlinked": {f.R, &preferredSupplierSummary{SupplierID: 1003, SupplierName: f.Sup[1003]}},
		"unpinned": {f.Q, nil},
	} {
		if got := h.preferredSupplier(ctx, c.id); !reflect.DeepEqual(got, c.want) {
			t.Errorf("preferredSupplier(%s):\n got %+v\nwant %+v", name, got, c.want)
		}
	}

	wantPts := []pricePoint{
		{Date: "2026-01-10", Cost: 1.25, PO: f.PO["A"], Supplier: "Sup A", Source: "po"},
		{Date: "2026-02-01", Cost: 1.1, PO: f.PO["B"], Supplier: "", Source: "po"},
		{Date: "2026-01-05", Cost: 2, Supplier: f.Sup[1002], Source: "price", PackSize: floatPtr(1)},
		{Date: "2026-01-06", Cost: 1.5, Supplier: f.Sup[1002], Source: "price", PackSize: floatPtr(10)},
	}
	if got := h.partPricePoints(ctx, f.P); !reflect.DeepEqual(got, wantPts) {
		t.Errorf("partPricePoints(P):\n got %+v\nwant %+v", got, wantPts)
	}
	if got := h.partPricePoints(ctx, f.Q); got != nil {
		t.Errorf("partPricePoints(Q) = %+v, want nil", got)
	}
}

// TestIntegration_PartDetail_AttachmentsAndPrice pins PartDetail's own queries: the primary
// attachment, the top five other active attachments in sort order, the photo grid (local images,
// not the thumbnail), and the preferred supplier's lowest active price feeding its card and the
// rollup delta.
func TestIntegration_PartDetail_AttachmentsAndPrice(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPartOrders(t, h)
	defer cleanup()

	get := func(id int) string {
		rec := httptest.NewRecorder()
		h.PartDetail(rec, withID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/part/%d/details", id), nil), id))
		assertStatus(t, "PartDetail", rec, http.StatusOK)
		return rec.Body.String()
	}
	body := get(f.P)
	section := func(from, to string) string {
		t.Helper()
		i := strings.Index(body, from)
		j := strings.Index(body[i+1:], to)
		if i < 0 || j < 0 {
			t.Fatalf("PartDetail: section %q..%q not found", from, to)
		}
		return body[i : i+1+j]
	}

	photos := section("photo-thumb-grid", "<span>Pricing</span>")
	for want, in := range map[string]bool{`photo1.jpg"`: true, `photo2.png"`: true, "thumb.png": false, "gone.jpg": false} {
		if strings.Contains(photos, want) != in {
			t.Errorf("photo grid contains %q = %v, want %v", want, !in, in)
		}
	}

	atts := section("<span>Attachments</span>", "</div>\n        </div>\n\n")
	if n := strings.Count(atts, `class="detail-row"`); n != 6 {
		t.Errorf("attachments card rows = %d, want 6 (primary + top 5)", n)
	}
	last := -1
	for _, want := range []string{"Primary</strong>", "https://example.com/p.pdf", "(Drawing)", "photo1.jpg", "thumb.png", "/local-dir/", "https://example.com/x", "plain text"} {
		i := strings.Index(atts, want)
		if i < 0 || i < last {
			t.Errorf("attachments card: %q missing or out of order", want)
		}
		last = i
	}
	for _, not := range []string{"photo2.png", "gone.jpg"} {
		if strings.Contains(atts, not) {
			t.Errorf("attachments card contains %q", not)
		}
	}

	for _, want := range []string{
		"<strong>Supplier Price:</strong><span>$1.5000</span>",
		"+$0.3000 (+20.0%)", `<span class="badge bg-warning text-dark">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PartDetail(P) body missing %q", want)
		}
	}
	if !strings.Contains(get(f.R), "<strong>Supplier Price:</strong><span>—</span>") {
		t.Error("PartDetail(R): want no supplier price")
	}
	if !strings.Contains(get(f.Q), "No primary attachment set.") {
		t.Error("PartDetail(Q): want no primary attachment")
	}
}
