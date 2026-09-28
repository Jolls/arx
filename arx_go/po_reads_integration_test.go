//go:build integration

package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"arx/arx_go/models"
)

// poFixture is a throwaway company with two contacts, two parts, a supplier_part link, two prices,
// a fully populated PO (six lines, history, receipts), a bare PO with NULL header fields and a
// two-quote RFQ group (#221).
type poFixture struct {
	Co, ConD, ConO int // company, its default contact, another contact
	CoName         string
	ConDName       string
	ConOName       string
	P1, P2         int // P1: revision C, lot-tracked, primary attachment; P2: NULL revision
	PN1, PN2       string
	Att            int
	Full, Bare     string
	FullID, BareID int
	Lines          [6]int // Full's po_line ids by line number
	Q1, Q2         string // RFQ quotes; Q2 has no lines
	Group          int
	Rcpt           [3]int // receipt ledger ids, in insert order
}

func seedPOFixture(t *testing.T, h *Handler) (f poFixture, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	co, cn, pn, po, pol := h.cfg().CompanyTable(), h.cfg().ContactTable(), h.cfg().PartsTable(), h.cfg().POTable(), h.cfg().POLineTable()
	base := smokeUniq("PR") // purchase_order.number is VARCHAR(32)
	f.CoName, f.ConDName, f.ConOName = base+"-co", base+"-cD", base+"-cO"
	f.PN1, f.PN2 = base+"-P1", base+"-P2"
	f.Full, f.Bare, f.Q1, f.Q2 = base+"-f", base+"-b", base+"-q1", base+"-q2"
	var poIDs, partIDs []int
	cleanup = func() {
		for _, id := range poIDs {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE po_line_id IN (SELECT id FROM %s WHERE po_id=$1)`, h.cfg().InventoryTxnTable(), pol), id)
			for _, tbl := range []string{pol, h.cfg().POHistoryTable()} {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE po_id=$1`, tbl), id)
			}
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, po), id)
		}
		for _, id := range partIDs {
			smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET primary_attachment_id=NULL WHERE id=$1`, pn), id)
			for _, tbl := range []string{h.cfg().AttachmentsTable(), h.cfg().SupplierPartTable(), h.cfg().PriceTable()} {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id=$1`, tbl), id)
			}
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, pn), id)
		}
		if f.Co != 0 {
			smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET default_contact=NULL WHERE id=$1`, co), f.Co)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE company_id=$1`, cn), f.Co)
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, co), f.Co)
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

	scan(&f.Co, fmt.Sprintf(`INSERT INTO %s (name, supplier_code, is_active, is_supplier) VALUES ($1,'PQ1',TRUE,TRUE) RETURNING id`, co), f.CoName)
	scan(&f.ConD, fmt.Sprintf(`INSERT INTO %s (display_name, company_id, address, city, state, zipcode, country, phone_1, fax, email, is_active)
		VALUES ($1,$2,'1 Dock Rd','Dockton','DS','11111','Freedonia','555-0301','555-0302','d@example.com',TRUE) RETURNING id`, cn), f.ConDName, f.Co)
	scan(&f.ConO, fmt.Sprintf(`INSERT INTO %s (display_name, company_id, address, city, state, zipcode, country, phone_1, fax, email, is_active)
		VALUES ($1,$2,'2 Other Ave','Otherton','OS','22222','Ruritania','555-0401','555-0402','o@example.com',TRUE) RETURNING id`, cn), f.ConOName, f.Co)
	exec(fmt.Sprintf(`UPDATE %s SET default_contact=$2 WHERE id=$1`, co), f.Co, f.ConD)

	scan(&f.P1, fmt.Sprintf(`INSERT INTO %s (part_number, description, revision, category, tracking_mode) VALUES ($1,'p1','C','BUY','lot') RETURNING id`, pn), f.PN1)
	partIDs = append(partIDs, f.P1)
	scan(&f.P2, fmt.Sprintf(`INSERT INTO %s (part_number, description, revision, category) VALUES ($1,'p2',NULL,'BUY') RETURNING id`, pn), f.PN2)
	partIDs = append(partIDs, f.P2)
	scan(&f.Att, fmt.Sprintf(`INSERT INTO %s (part_id, file_name, category, is_active) VALUES ($1,'LOCAL:itest\pr-dwg.pdf','Drawing',TRUE) RETURNING id`,
		h.cfg().AttachmentsTable()), f.P1)
	exec(fmt.Sprintf(`UPDATE %s SET primary_attachment_id=$2 WHERE id=$1`, pn), f.P1, f.Att)
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, supplier_id, supplier_pn) VALUES ($1,$2,'SPN-1')`, h.cfg().SupplierPartTable()), f.P1, f.Co)
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, supplier_id, price_ea, price_pack, pack_size, is_active, effective_date) VALUES
		($1,$3,2.5,25,10,TRUE,'2026-01-01'), ($2,$3,4,4,1,FALSE,'2026-01-01')`, h.cfg().PriceTable()), f.P1, f.P2, f.Co)

	scan(&f.FullID, fmt.Sprintf(`INSERT INTO %s (number, status, approval_status, is_active, orderer, account_id,
		supplier_id, supplier_name, supplier_contact, supplier_email, supplier_address, supplier_city, supplier_state,
		supplier_zipcode, supplier_country, supplier_phone_number, supplier_fax_number,
		receiver_id, receiver_name, receiver_contact, receiver_email, receiver_address, receiver_city, receiver_state,
		receiver_zipcode, receiver_country, receiver_phone, receiver_fax,
		tax1, shipping_cost, misc_cost, total_cost, notes, internal_notes,
		date_ordered, date_requested, date_closed, date_printed, date_modified, supplier_contact_id, receiver_contact_id)
		VALUES ($1,'partially_received','approved',TRUE,'Olive Orderer','ACCT-9',
		$2,$3,'Sam Sup','s@example.com','3 Sup St','Supton','SS','33333','Freedonia','555-0501','555-0502',
		$2,'Recv Co','Rae Recv','r@example.com','4 Recv Rd','Recvton','RS','44444','Ruritania','555-0601','555-0602',
		1.25,2.5,3.75,123.456,'print notes','internal notes',
		'2026-01-15','2026-01-20','2026-03-01','2026-01-16','2026-02-03 04:05:06+00',$4,$5) RETURNING id`, po),
		f.Full, f.Co, f.CoName, f.ConO, f.ConD)
	poIDs = append(poIDs, f.FullID)
	scan(&f.BareID, fmt.Sprintf(`INSERT INTO %s (number, supplier_id, status, approval_status, is_active, internal_notes, date_modified)
		VALUES ($1,$2,NULL,NULL,NULL,NULL,NULL) RETURNING id`, po), f.Bare, f.Co)
	poIDs = append(poIDs, f.BareID)

	// Lines, inserted out of line order. 1: linked vendor PN, price covered (pack 10 <= qty 10);
	// 2: new vendor PN, price not covered (pack 10 > qty 5); 3: inactive price only, NULL rev/desc;
	// 4: freeform; 5: zero cost; 6: duplicate of 2's price point.
	for _, i := range []int{2, 0, 5, 3, 1, 4} {
		l := []struct {
			part                 any
			snap, rev, desc, vpn any
			qty, cost            float64
			lead, recv           any
			dateRecv             any
		}{
			{f.P1, f.PN1, "C", "first", "SPN-1", 10, 2.5, 7, 4, "2026-02-10"},
			{f.P1, f.PN1, "C", "second", "SPN-NEW", 5, 2.5, nil, 0, nil},
			{f.P2, f.PN2, nil, nil, "", 1, 4, nil, 0, nil},
			{nil, nil, nil, "freeform", "FREE-V", 2, 9, nil, 0, nil},
			{f.P2, f.PN2, "", "zero", nil, 3, 0, nil, 0, nil},
			{f.P1, f.PN1, "C", "dup", nil, 5, 2.5, nil, 0, nil},
		}[i]
		scan(&f.Lines[i], fmt.Sprintf(`INSERT INTO %s (po_id, part_id, part_number_snapshot, revision_snapshot, description,
			vendor_part_number, line_number, qty, unit_cost, lead_time_days, received_qty, date_received)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date) RETURNING id`, pol),
			f.FullID, l.part, l.snap, l.rev, l.desc, l.vpn, i+1, l.qty, l.cost, l.lead, l.recv, l.dateRecv)
	}

	// History: h3 ties h2's changed_at, so the higher id sorts first.
	exec(fmt.Sprintf(`INSERT INTO %s (po_id, event_type, from_status, to_status, action, note, changed_by, changed_at) VALUES
		($1,'status',NULL,'draft',NULL,NULL,'alice','2026-01-01 10:00+00')`, h.cfg().POHistoryTable()), f.FullID)
	exec(fmt.Sprintf(`INSERT INTO %s (po_id, event_type, from_status, to_status, action, note, changed_by, changed_at) VALUES
		($1,'approval',NULL,NULL,'approved','ok','bob','2026-01-02 10:00+00')`, h.cfg().POHistoryTable()), f.FullID)
	exec(fmt.Sprintf(`INSERT INTO %s (po_id, event_type, from_status, to_status, changed_at) VALUES
		($1,'status','draft','open','2026-01-02 10:00+00')`, h.cfg().POHistoryTable()), f.FullID)

	// Receipts on line 1: r[1] and r[2] share a date (higher id first); the adjustment is excluded.
	for i, r := range []struct {
		qty        float64
		date, user string
	}{{3, "2026-02-05", "alice"}, {1, "2026-02-10", ""}, {2, "2026-02-10", "bob"}} {
		scan(&f.Rcpt[i], fmt.Sprintf(`INSERT INTO %s (part_id, txn_type, qty, txn_date, username, po_line_id)
			VALUES ($1,'receipt',$2,$3::date,$4,$5) RETURNING id`, h.cfg().InventoryTxnTable()), f.P1, r.qty, r.date, r.user, f.Lines[0])
	}
	exec(fmt.Sprintf(`INSERT INTO %s (part_id, txn_type, qty, txn_date, username, po_line_id)
		VALUES ($1,'adjustment',-1,'2026-02-11','carol',$2)`, h.cfg().InventoryTxnTable()), f.P1, f.Lines[0])

	// RFQ group: Q1 anchors it (group id = its own id) and has one quoted line; Q2 has no lines
	// and no supplier name.
	var q2 int
	scan(&f.Group, fmt.Sprintf(`INSERT INTO %s (number, status, supplier_id, supplier_name, total_cost) VALUES ($1,'rfq',$2,$3,30) RETURNING id`, po), f.Q1, f.Co, f.CoName)
	poIDs = append(poIDs, f.Group)
	exec(fmt.Sprintf(`UPDATE %s SET rfq_group_id=id WHERE id=$1`, po), f.Group)
	scan(&q2, fmt.Sprintf(`INSERT INTO %s (number, status, supplier_id, rfq_group_id) VALUES ($1,'rfq',$2,$3) RETURNING id`, po), f.Q2, f.Co, f.Group)
	poIDs = append(poIDs, q2)
	exec(fmt.Sprintf(`INSERT INTO %s (po_id, part_id, part_number_snapshot, revision_snapshot, description, line_number, qty, unit_cost, lead_time_days)
		VALUES ($1,$2,$3,'C','quoted',1,10,3,12)`, pol), f.Group, f.P1, f.PN1)
	return f, cleanup
}

func ymd(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func getReq(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }

// fetchPO: every header field on the full PO, NULLs on the bare one, unknown number.
func TestIntegration_POReads_FetchPO(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	got, ok := h.fetchPO(httptest.NewRecorder(), getReq("/po/x"), f.Full)
	if !ok {
		t.Fatalf("fetchPO(%s) not found", f.Full)
	}
	dates := []string{ymd(got.DateOrdered), ymd(got.DateRequested), ymd(got.DateClosed), ymd(got.DatePrinted)}
	if want := []string{"2026-01-15", "2026-01-20", "2026-03-01", "2026-01-16"}; !reflect.DeepEqual(dates, want) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
	if got.DateModified == nil || !got.DateModified.Equal(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)) {
		t.Errorf("DateModified = %v", got.DateModified)
	}
	got.DateOrdered, got.DateRequested, got.DateClosed, got.DatePrinted, got.DateModified = nil, nil, nil, nil, nil
	fl := func(v float64) *float64 { return &v }
	in := func(v int) *int { return &v }
	want := models.PurchaseOrder{ID: f.FullID, Number: f.Full, Status: "partially_received", ApprovalStatus: "approved",
		IsActive: true, Orderer: "Olive Orderer", AccountID: "ACCT-9",
		SupplierID: in(f.Co), SupplierName: f.CoName, SupplierContact: "Sam Sup", SupplierContactID: in(f.ConO),
		SupplierEmail: "s@example.com", SupplierAddress: "3 Sup St", SupplierCity: "Supton", SupplierState: "SS",
		SupplierZipcode: "33333", SupplierCountry: "Freedonia", SupplierPhoneNumber: "555-0501", SupplierFaxNumber: "555-0502",
		ReceiverID: in(f.Co), ReceiverName: "Recv Co", ReceiverContact: "Rae Recv", ReceiverContactID: in(f.ConD),
		ReceiverEmail: "r@example.com", ReceiverAddress: "4 Recv Rd", ReceiverCity: "Recvton", ReceiverState: "RS",
		ReceiverZipcode: "44444", ReceiverCountry: "Ruritania", ReceiverPhone: "555-0601", ReceiverFax: "555-0602",
		Tax1: fl(1.25), ShippingCost: fl(2.5), MiscCost: fl(3.75), TotalCost: fl(123.456),
		Notes: "print notes", InternalNotes: "internal notes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("full PO =\n%+v\nwant\n%+v", got, want)
	}

	bare, ok := h.fetchPO(httptest.NewRecorder(), getReq("/po/x"), f.Bare)
	if !ok {
		t.Fatalf("fetchPO(%s) not found", f.Bare)
	}
	if want := (models.PurchaseOrder{ID: f.BareID, Number: f.Bare, SupplierID: in(f.Co)}); !reflect.DeepEqual(bare, want) {
		t.Errorf("bare PO = %+v, want %+v", bare, want)
	}

	rec := httptest.NewRecorder()
	if _, ok := h.fetchPO(rec, getReq("/po/x"), f.Bare+"x"); ok || !strings.Contains(rec.Body.String(), "Purchase order not found") {
		t.Errorf("unknown PO: ok=%v body missing not-found message", ok)
	}
}

// fetchPOItems: line order and every mapped field, including the primary attachment and lot tracking.
func TestIntegration_POReads_FetchPOItems(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	items, err := h.fetchPOItems(getReq("/po/x"), f.Full)
	if err != nil {
		t.Fatalf("fetchPOItems: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("got %d items, want 6", len(items))
	}
	if got := ymd(items[0].DateReceived); got != "2026-02-10" {
		t.Errorf("line 1 DateReceived = %q", got)
	}
	items[0].DateReceived = nil
	in := func(v int) *int { return &v }
	att := &models.Attachment{ID: f.Att, FileName: `LOCAL:itest\pr-dwg.pdf`, Category: "Drawing"}
	want := []models.PurchaseOrderLine{
		{ID: f.Lines[0], LineNumber: 1, PartNumberSnapshot: f.PN1, RevisionSnapshot: "C", Description: "first", Qty: 10, UnitCost: 2.5,
			VendorPN: "SPN-1", PartID: in(f.P1), LeadTimeDays: in(7), ReceivedQty: 4, IsLotTracked: true, PrimaryAtt: att},
		{ID: f.Lines[1], LineNumber: 2, PartNumberSnapshot: f.PN1, RevisionSnapshot: "C", Description: "second", Qty: 5, UnitCost: 2.5,
			VendorPN: "SPN-NEW", PartID: in(f.P1), IsLotTracked: true, PrimaryAtt: att},
		{ID: f.Lines[2], LineNumber: 3, PartNumberSnapshot: f.PN2, Qty: 1, UnitCost: 4, PartID: in(f.P2)},
		{ID: f.Lines[3], LineNumber: 4, Description: "freeform", Qty: 2, UnitCost: 9, VendorPN: "FREE-V"},
		{ID: f.Lines[4], LineNumber: 5, PartNumberSnapshot: f.PN2, Description: "zero", Qty: 3, PartID: in(f.P2)},
		{ID: f.Lines[5], LineNumber: 6, PartNumberSnapshot: f.PN1, RevisionSnapshot: "C", Description: "dup", Qty: 5, UnitCost: 2.5,
			PartID: in(f.P1), IsLotTracked: true, PrimaryAtt: att},
	}
	for i := range want {
		if !reflect.DeepEqual(items[i], want[i]) {
			t.Errorf("line %d =\n%+v\nwant\n%+v", i+1, items[i], want[i])
		}
	}

	if items, err := h.fetchPOItems(getReq("/po/x"), f.Bare); err != nil || len(items) != 0 {
		t.Errorf("bare PO items = %v, %v; want none", items, err)
	}
}

// fetchPOReceipts (receipts only, newest first, id DESC on ties) and fetchPOHistory (changed_at
// DESC, id DESC; NULLs read as "").
func TestIntegration_POReads_ReceiptsAndHistory(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()
	req := getReq("/po/x")

	p1 := f.P1
	wantR := []POReceiptView{
		{Date: "2026-02-10", PartID: &p1, PartNumber: f.PN1, Qty: 2, Username: "bob"},
		{Date: "2026-02-10", PartID: &p1, PartNumber: f.PN1, Qty: 1, Username: ""},
		{Date: "2026-02-05", PartID: &p1, PartNumber: f.PN1, Qty: 3, Username: "alice"},
	}
	if got := h.fetchPOReceipts(req, f.FullID); !reflect.DeepEqual(got, wantR) {
		t.Errorf("receipts =\n%+v\nwant\n%+v", got, wantR)
	}
	if got := h.fetchPOReceipts(req, f.BareID); len(got) != 0 {
		t.Errorf("bare PO receipts = %+v", got)
	}

	got := h.fetchPOHistory(req, f.FullID)
	if len(got) != 3 {
		t.Fatalf("history = %+v, want 3 events", got)
	}
	at := []time.Time{time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	for i := range got {
		if !got[i].ChangedAt.Equal(at[i]) {
			t.Errorf("event %d ChangedAt = %v, want %v", i, got[i].ChangedAt, at[i])
		}
		got[i].ChangedAt = time.Time{}
	}
	wantH := []POHistoryEvent{
		{EventType: "status", FromStatus: "draft", ToStatus: "open"},
		{EventType: "approval", Action: "approved", Note: "ok", ChangedBy: "bob"},
		{EventType: "status", ToStatus: "draft", ChangedBy: "alice"},
	}
	if !reflect.DeepEqual(got, wantH) {
		t.Errorf("history =\n%+v\nwant\n%+v", got, wantH)
	}
	if got := h.fetchPOHistory(req, f.BareID); len(got) != 0 {
		t.Errorf("bare PO history = %+v", got)
	}
}

// Suggest links: a vendor PN on a catalog line with no matching supplier_part. Suggest prices: a
// catalog line with a cost no active price covers at or below its qty, DISTINCT.
func TestIntegration_POReads_Suggestions(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()
	req := getReq("/po/x")

	wantL := []SuggestLink{{Index: 0, PartID: f.P1, PartNumber: f.PN1, VendorPN: "SPN-NEW"}}
	if got := h.fetchSuggestLinks(req, f.Full); !reflect.DeepEqual(got, wantL) {
		t.Errorf("links = %+v, want %+v", got, wantL)
	}

	prices := h.fetchSuggestPrices(req, f.Full) // no ORDER BY: compare as a set
	got := map[SuggestPrice]bool{}
	for i, p := range prices {
		if p.Index != i {
			t.Errorf("price %d has Index %d", i, p.Index)
		}
		p.Index = 0
		got[p] = true
	}
	wantP := map[SuggestPrice]bool{
		{PartID: f.P1, PartNumber: f.PN1, Cost: 2.5, PackSize: 5}: true,
		{PartID: f.P2, PartNumber: f.PN2, Cost: 4, PackSize: 1}:   true,
	}
	if len(prices) != len(wantP) || !reflect.DeepEqual(got, wantP) {
		t.Errorf("prices = %+v, want %v", prices, wantP)
	}

	if l, p := h.fetchSuggestLinks(req, f.Bare), h.fetchSuggestPrices(req, f.Bare); len(l)+len(p) != 0 {
		t.Errorf("bare PO suggestions = %+v / %+v", l, p)
	}
}

// PORows: the fixture's rows in number DESC order, NULL fields empty, RFQ group id set.
func TestIntegration_POReads_Rows(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PORows(rec, getReq("/api/pos/rows"))
	assertStatus(t, "PORows", rec, http.StatusOK)
	type row struct {
		Num      string  `json:"num"`
		Status   string  `json:"status"`
		SID      *int    `json:"sid"`
		GID      *int    `json:"gid"`
		Supplier string  `json:"supplier"`
		Ordered  string  `json:"ordered"`
		Closed   string  `json:"closed"`
		Orderer  string  `json:"orderer"`
		Cost     float64 `json:"cost"`
	}
	var rows []row
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	mine := map[string]bool{f.Full: true, f.Bare: true, f.Q1: true, f.Q2: true}
	var got []row
	for _, r := range rows {
		if mine[r.Num] {
			got = append(got, r)
		}
	}
	co, g := f.Co, f.Group
	want := []row{
		{Num: f.Q2, Status: "rfq", SID: &co, GID: &g},
		{Num: f.Q1, Status: "rfq", SID: &co, GID: &g, Supplier: f.CoName, Cost: 30},
		{Num: f.Full, Status: "partially_received", SID: &co, Supplier: f.CoName, Ordered: "2026-01-15",
			Closed: "2026-03-01", Orderer: "Olive Orderer", Cost: 123.456},
		{Num: f.Bare, SID: &co},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%+v\nwant\n%+v", got, want)
	}
}

// POsExportCSV: one record per line (number DESC, line_number), a PO without lines as one record
// with blank line columns. Failed before #221 (joined po_line on nonexistent columns).
func TestIntegration_POReads_ExportCSV(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.POsExportCSV(rec, getReq("/pos/export.csv"))
	assertStatus(t, "POsExportCSV", rec, http.StatusOK)
	recs, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(recs) == 0 || recs[0][0] != "PO Number" || recs[0][12] != "Vendor PN" {
		t.Fatalf("header = %v", recs[0])
	}
	mine := map[string]bool{f.Full: true, f.Bare: true, f.Q1: true, f.Q2: true}
	var got [][]string
	for _, r := range recs[1:] {
		if mine[r[0]] {
			got = append(got, r)
		}
	}
	full := func(line, pn, desc, qty, cost, vpn string) []string {
		return []string{f.Full, "partially_received", f.CoName, "2026-01-15", "2026-03-01", "Olive Orderer", "123.46", line, pn, desc, qty, cost, vpn}
	}
	want := [][]string{
		{f.Q2, "rfq", "", "", "", "", "0.00", "", "", "", "0", "0.00", ""},
		{f.Q1, "rfq", f.CoName, "", "", "", "30.00", "1", f.PN1, "quoted", "10", "3.00", ""},
		full("1", f.PN1, "first", "10", "2.50", "SPN-1"),
		full("2", f.PN1, "second", "5", "2.50", "SPN-NEW"),
		full("3", f.PN2, "", "1", "4.00", ""),
		full("4", "", "freeform", "2", "9.00", "FREE-V"),
		full("5", f.PN2, "zero", "3", "0.00", ""),
		full("6", f.PN1, "dup", "5", "2.50", ""),
		{f.Bare, "", "", "", "", "", "0.00", "", "", "", "0", "0.00", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records =\n%q\nwant\n%q", got, want)
	}
}

// applyPODefaults: the user's default contact wins, else the receiver company's default contact;
// no receiver → noDefaultReceiver; an unknown receiver company still sets ReceiverID.
func TestIntegration_POReads_ApplyPODefaults(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	run := func(receiver, contact int) (models.PurchaseOrder, []ContactSummary, bool) {
		req := getReq("/pos/new")
		req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, &User{ID: 8001, DefaultPOReceiverID: receiver, DefaultPOContactID: contact}))
		var po models.PurchaseOrder
		sup, recv, noDefault := h.applyPODefaults(req, &po)
		if sup != nil {
			t.Errorf("supplierContacts = %+v, want nil", sup)
		}
		return po, recv, noDefault
	}
	co := f.Co
	dock := models.PurchaseOrder{ReceiverID: &co, ReceiverName: f.CoName, ReceiverContact: f.ConDName, ReceiverEmail: "d@example.com",
		ReceiverAddress: "1 Dock Rd", ReceiverCity: "Dockton", ReceiverState: "DS", ReceiverZipcode: "11111",
		ReceiverCountry: "Freedonia", ReceiverPhone: "555-0301", ReceiverFax: "555-0302"}

	po, recv, noDefault := run(f.Co, 0)
	if !reflect.DeepEqual(po, dock) || noDefault {
		t.Errorf("fallback contact: po = %+v noDefault=%v", po, noDefault)
	}
	if len(recv) != 2 || recv[0].DisplayName != f.ConDName || recv[1].DisplayName != f.ConOName {
		t.Errorf("receiverContacts = %+v", recv)
	}

	po, _, _ = run(f.Co, f.ConO)
	if po.ReceiverContact != f.ConOName || po.ReceiverCity != "Otherton" || po.ReceiverName != f.CoName {
		t.Errorf("explicit contact: po = %+v", po)
	}

	if po, recv, noDefault = run(0, f.ConO); !noDefault || po.ReceiverID != nil || recv != nil {
		t.Errorf("no receiver: po = %+v recv = %+v noDefault=%v", po, recv, noDefault)
	}

	const unknown = 2147483000
	if po, recv, _ = run(unknown, 0); po.ReceiverID == nil || *po.ReceiverID != unknown || po.ReceiverName != "" || len(recv) != 0 {
		t.Errorf("unknown receiver: po = %+v recv = %+v", po, recv)
	}
}

// resolvePolRev: the form value wins; otherwise the part's revision, "" when unknown or NULL.
func TestIntegration_POReads_ResolvePolRev(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()
	req := getReq("/po/x")

	for _, c := range []struct{ form, pnid, want string }{
		{"X", strconv.Itoa(f.P1), "X"},
		{"", "", ""},
		{"", strconv.Itoa(f.P1), "C"},
		{"", strconv.Itoa(f.P2), ""},
		{"", "2147483000", ""},
		{"", "abc", ""},
	} {
		if got := h.resolvePolRev(req, c.form, c.pnid); got != c.want {
			t.Errorf("resolvePolRev(%q, %q) = %q, want %q", c.form, c.pnid, got, c.want)
		}
	}
}

// POPrint puts the supplier's code in the title; POOpenFolder 404s for an unknown PO.
func TestIntegration_POReads_PrintAndOpenFolder(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.POPrint(rec, withIDStr(getReq("/po/x/print"), f.Full))
	assertStatus(t, "POPrint", rec, http.StatusOK)
	if want := "<title>" + f.Full + " PQ1</title>"; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("POPrint missing %q", want)
	}

	saved := h.cfg().POFolderRoot
	h.cfg().POFolderRoot = t.TempDir()
	defer func() { h.cfg().POFolderRoot = saved }()
	rec = httptest.NewRecorder()
	h.POOpenFolder(rec, withIDStr(httptest.NewRequest(http.MethodPost, "/po/x/open-folder", nil), f.Bare+"x"))
	assertStatus(t, "POOpenFolder unknown", rec, http.StatusNotFound)
}

// RFQCompare: one column per quote in id order (Q2 has no lines and no supplier name), the quoted
// line's revision and lead time.
func TestIntegration_POReads_RFQCompare(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedPOFixture(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.RFQCompare(rec, withGroupParam(getReq("/rfq/x/compare"), strconv.Itoa(f.Group)))
	assertStatus(t, "RFQCompare", rec, http.StatusOK)
	body := rec.Body.String()
	assertInOrder(t, "quote columns", body, "PO #"+f.Q1+"<", "PO #"+f.Q2+"<")
	for _, want := range []string{"(no supplier)", ">" + f.PN1 + "<", "<td>C</td>", `value="12"`} {
		if !strings.Contains(body, want) {
			t.Errorf("RFQCompare missing %q", want)
		}
	}
}
