//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"arx/arx_go/models"
)

// createPO posts vals to POCreate with POFolderRoot set to root and returns the new PO's number
// and id, registering its cleanup.
func createPO(t *testing.T, h *Handler, root string, vals url.Values) (number string, id int) {
	t.Helper()
	saved := h.cfg().POFolderRoot
	h.cfg().POFolderRoot = root
	defer func() { h.cfg().POFolderRoot = saved }()
	rec := httptest.NewRecorder()
	h.POCreate(rec, postForm("/pos", vals))
	assert302(t, "POCreate", rec)
	loc := rec.Header().Get("Location")
	number = strings.TrimSuffix(strings.TrimPrefix(loc, "/po/"), "?suggest_links=1")
	if !strings.HasSuffix(loc, "?suggest_links=1") || number == "" {
		t.Fatalf("POCreate Location = %q", loc)
	}
	po, ok := h.fetchPO(httptest.NewRecorder(), getReq("/po/x"), number)
	if !ok {
		t.Fatalf("created PO %s not found", number)
	}
	t.Cleanup(func() { cleanupPO(context.Background(), h, po.ID) })
	return number, po.ID
}

func mustPO(t *testing.T, h *Handler, number string) models.PurchaseOrder {
	t.Helper()
	po, ok := h.fetchPO(httptest.NewRecorder(), getReq("/po/x"), number)
	if !ok {
		t.Fatalf("PO %s not found", number)
	}
	return po
}

func mustItems(t *testing.T, h *Handler, number string) []models.PurchaseOrderLine {
	t.Helper()
	items, err := h.fetchPOItems(getReq("/po/x"), number)
	if err != nil {
		t.Fatalf("fetchPOItems(%s): %v", number, err)
	}
	return items
}

// lineKey is the part of a PO line POCreate/POUpdate write.
type lineKey struct {
	Line          int
	PN, Rev, Desc string
	Qty, Cost     float64
	VPN           string
	PartID        int // 0 = NULL
}

func lineKeys(items []models.PurchaseOrderLine) []lineKey {
	out := make([]lineKey, len(items))
	for i, l := range items {
		out[i] = lineKey{l.LineNumber, l.PartNumberSnapshot, l.RevisionSnapshot, l.Description, l.Qty, l.UnitCost, l.VendorPN, 0}
		if l.PartID != nil {
			out[i].PartID = *l.PartID
		}
	}
	return out
}

func polVals(vals url.Values, prefix, key string, item, pn, rev, desc, vpn, qty, cost, pnid string) {
	for field, v := range map[string]string{"POLItem": item, "POLPNPartNumber": pn, "POLRev": rev, "POLDesc": desc,
		"VendorPN": vpn, "POLQty": qty, "POLCost": cost, "POLPNID": pnid} {
		vals.Set(fmt.Sprintf("%s[%s][%s]", prefix, key, field), v)
	}
}

// POCreate: every header field, lines (blank skipped, revision fallback, freeform), total, the
// creation history event; empty/invalid optional numbers are stored as NULL.
func TestIntegration_POWrites_Create(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	co := strconv.Itoa(f.Co)

	vals := url.Values{
		"orderer": {"Olive"}, "account_id": {"ACCT-1"},
		"supplier_id": {co}, "supplier_name": {"Sup Name"}, "supplier_contact": {"Sam"}, "supplier_email": {"s@x"},
		"supplier_address": {"1 A St"}, "supplier_city": {"Acity"}, "supplier_state": {"AS"}, "supplier_zipcode": {"111"},
		"supplier_country": {"Aland"}, "supplier_phone_number": {"555-1"}, "supplier_fax_number": {"555-2"},
		"receiver_id": {co}, "receiver_name": {"Recv Name"}, "receiver_contact": {"Rae"}, "receiver_email": {"r@x"},
		"receiver_address": {"2 B St"}, "receiver_city": {"Bcity"}, "receiver_state": {"BS"}, "receiver_zipcode": {"222"},
		"receiver_country": {"Bland"}, "receiver_phone": {"555-3"}, "receiver_fax": {"555-4"},
		"tax1": {"1.5"}, "shipping_cost": {"2"}, "misc_cost": {"0.25"}, "notes": {"pn"}, "internal_notes": {"in"},
		"date_ordered": {"2026-04-01"}, "date_requested": {"2026-04-05"}, "date_closed": {""}, "date_printed": {"2026-04-02"},
		"supplier_contact_id": {strconv.Itoa(f.ConO)}, "receiver_contact_id": {strconv.Itoa(f.ConD)},
	}
	polVals(vals, "new_pol", "a", "1", f.PN1, "", "d1", "V1", "3", "2", strconv.Itoa(f.P1))
	polVals(vals, "new_pol", "b", "2", "", "Z", "free", "", "1.5", "4", "")
	polVals(vals, "new_pol", "c", "", "", "", "", "", "", "", "")
	num, id := createPO(t, h, "", vals)
	if ok, _ := regexp.MatchString(`^\d+$`, num); !ok {
		t.Errorf("PO number %q not numeric", num)
	}

	po := mustPO(t, h, num)
	if ymd(po.DateOrdered) != "2026-04-01" || ymd(po.DateRequested) != "2026-04-05" || po.DateClosed != nil || po.DatePrinted != nil || po.DateModified == nil {
		t.Errorf("dates: ordered %v requested %v closed %v printed %v modified %v", po.DateOrdered, po.DateRequested, po.DateClosed, po.DatePrinted, po.DateModified)
	}
	po.DateOrdered, po.DateRequested, po.DateModified = nil, nil, nil
	fl := func(v float64) *float64 { return &v }
	in := func(v int) *int { return &v }
	want := models.PurchaseOrder{ID: id, Number: num, Status: "draft", ApprovalStatus: "not_submitted", IsActive: true,
		Orderer: "Olive", AccountID: "ACCT-1",
		SupplierID: in(f.Co), SupplierName: "Sup Name", SupplierContact: "Sam", SupplierContactID: in(f.ConO), SupplierEmail: "s@x",
		SupplierAddress: "1 A St", SupplierCity: "Acity", SupplierState: "AS", SupplierZipcode: "111", SupplierCountry: "Aland",
		SupplierPhoneNumber: "555-1", SupplierFaxNumber: "555-2",
		ReceiverID: in(f.Co), ReceiverName: "Recv Name", ReceiverContact: "Rae", ReceiverContactID: in(f.ConD), ReceiverEmail: "r@x",
		ReceiverAddress: "2 B St", ReceiverCity: "Bcity", ReceiverState: "BS", ReceiverZipcode: "222", ReceiverCountry: "Bland",
		ReceiverPhone: "555-3", ReceiverFax: "555-4",
		Tax1: fl(1.5), ShippingCost: fl(2), MiscCost: fl(0.25), TotalCost: fl(15.75), Notes: "pn", InternalNotes: "in"}
	if !reflect.DeepEqual(po, want) {
		t.Errorf("PO =\n%+v\nwant\n%+v", po, want)
	}

	wantL := []lineKey{{1, f.PN1, "C", "d1", 3, 2, "V1", f.P1}, {2, "", "Z", "free", 1.5, 4, "", 0}}
	if got := lineKeys(mustItems(t, h, num)); !reflect.DeepEqual(got, wantL) {
		t.Errorf("lines = %+v, want %+v", got, wantL)
	}

	hist := h.fetchPOHistory(getReq("/po/x"), id)
	if len(hist) != 1 || hist[0].EventType != "status" || hist[0].FromStatus != "" || hist[0].ToStatus != "draft" || hist[0].ChangedBy != "system" {
		t.Errorf("history = %+v", hist)
	}

	// Minimal form: empty/invalid optional numbers and dates → NULL, total 0.
	num2, id2 := createPO(t, h, "", url.Values{"supplier_id": {co}, "receiver_id": {""}, "tax1": {""}, "supplier_contact_id": {"x"}})
	want2 := models.PurchaseOrder{ID: id2, Number: num2, Status: "draft", ApprovalStatus: "not_submitted", IsActive: true,
		SupplierID: in(f.Co), TotalCost: fl(0)}
	po2 := mustPO(t, h, num2)
	po2.DateModified = nil
	if !reflect.DeepEqual(po2, want2) {
		t.Errorf("minimal PO = %+v, want %+v", po2, want2)
	}
}

// POCreate for RFQs: the first quote takes <seq>R1 and anchors its own group without a folder; a
// second quote takes R2 in that group. A non-numeric group or missing supplier fails.
func TestIntegration_POWrites_CreateRFQ(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	root := t.TempDir()
	co := strconv.Itoa(f.Co)

	q1, id1 := createPO(t, h, root, url.Values{"rfq": {"1"}, "supplier_id": {co}})
	if ok, _ := regexp.MatchString(`^\d+R1$`, q1); !ok {
		t.Fatalf("first quote number %q, want <n>R1", q1)
	}
	po := mustPO(t, h, q1)
	if po.Status != "rfq" || !po.IsActive || po.RFQGroupID == nil || *po.RFQGroupID != id1 {
		t.Errorf("first quote: status %q active %v group %v", po.Status, po.IsActive, po.RFQGroupID)
	}
	if hist := h.fetchPOHistory(getReq("/po/x"), id1); len(hist) != 1 || hist[0].ToStatus != "rfq" {
		t.Errorf("first quote history = %+v", hist)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("RFQ created folders: %v", entries)
	}

	q2, _ := createPO(t, h, root, url.Values{"rfq": {"1"}, "rfq_group_id": {strconv.Itoa(id1)}, "supplier_id": {co}})
	if want := strings.TrimSuffix(q1, "R1") + "R2"; q2 != want {
		t.Errorf("second quote number %q, want %q", q2, want)
	}
	if po := mustPO(t, h, q2); po.RFQGroupID == nil || *po.RFQGroupID != id1 {
		t.Errorf("second quote group = %v, want %d", po.RFQGroupID, id1)
	}

	for _, c := range []struct {
		vals url.Values
		want string
	}{
		{url.Values{"rfq": {"1"}, "rfq_group_id": {"abc"}, "supplier_id": {co}}, "Error loading RFQ group"},
		{url.Values{"orderer": {"nobody"}}, "Error creating PO"},
	} {
		rec := httptest.NewRecorder()
		h.POCreate(rec, postForm("/pos", c.vals))
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("POCreate %v: body missing %q (status %d)", c.vals, c.want, rec.Code)
		}
	}
}

// POUpdate: header fields (status untouched), line update/delete/insert guarded by po_id, total,
// approval reset; errors roll back.
func TestIntegration_POWrites_Update(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	ctx := context.Background()
	co := strconv.Itoa(f.Co)

	vals := url.Values{"supplier_id": {co}, "receiver_id": {co}, "tax1": {"1"}, "supplier_contact_id": {strconv.Itoa(f.ConO)}}
	polVals(vals, "new_pol", "a", "1", f.PN1, "C", "l1", "", "1", "1", strconv.Itoa(f.P1))
	polVals(vals, "new_pol", "b", "2", f.PN2, "", "l2", "", "1", "1", strconv.Itoa(f.P2))
	polVals(vals, "new_pol", "c", "3", "", "", "l3", "", "2", "5", "")
	num, id := createPO(t, h, "", vals)
	other := url.Values{"supplier_id": {co}}
	polVals(other, "new_pol", "a", "1", "", "", "oA", "", "1", "1", "")
	polVals(other, "new_pol", "b", "2", "", "", "oB", "", "1", "1", "")
	otherNum, _ := createPO(t, h, "", other)
	lines, oLines := mustItems(t, h, num), mustItems(t, h, otherNum)
	setPOStatus(t, h, ctx, id, "draft", "approved")
	before := mustPO(t, h, num)

	upd := url.Values{
		"orderer": {"New Orderer"}, "account_id": {""}, "supplier_id": {co}, "supplier_name": {"Sup2"},
		"receiver_id": {""}, "tax1": {""}, "shipping_cost": {"3"}, "misc_cost": {""}, "notes": {"n2"},
		"date_ordered": {"2026-05-01"}, "date_closed": {"2026-06-01"}, "date_printed": {"2026-05-02"},
		"supplier_contact_id": {""}, "receiver_contact_id": {strconv.Itoa(f.ConD)},
		"delete_pol[]": {strconv.Itoa(lines[1].ID), strconv.Itoa(oLines[0].ID)},
	}
	polVals(upd, "pol", strconv.Itoa(lines[0].ID), "5", f.PN1, "", "upd", "V9", "4", "2.5", strconv.Itoa(f.P1))
	polVals(upd, "pol", strconv.Itoa(lines[1].ID), "6", "", "", "deleted anyway", "", "1", "1", "")
	polVals(upd, "pol", strconv.Itoa(oLines[1].ID), "9", "", "", "hijack", "", "9", "9", "")
	polVals(upd, "new_pol", "0", "7", "", "", "added", "", "2", "1", "")
	polVals(upd, "new_pol", "1", "", "", "", "", "", "", "", "")
	rec := httptest.NewRecorder()
	h.POUpdate(rec, withIDStr(postForm("/po/x", upd), num))
	assert302(t, "POUpdate", rec)
	if loc := rec.Header().Get("Location"); loc != "/po/"+num+"?suggest_links=1" {
		t.Errorf("Location = %q", loc)
	}

	po := mustPO(t, h, num)
	if ymd(po.DateOrdered) != "2026-05-01" || po.DateRequested != nil || ymd(po.DateClosed) != "2026-06-01" || ymd(po.DatePrinted) != "2026-05-02" {
		t.Errorf("dates: %v %v %v %v", po.DateOrdered, po.DateRequested, po.DateClosed, po.DatePrinted)
	}
	if po.DateModified == nil || before.DateModified == nil || !po.DateModified.After(*before.DateModified) {
		t.Errorf("date_modified not bumped: %v → %v", before.DateModified, po.DateModified)
	}
	po.DateOrdered, po.DateClosed, po.DatePrinted, po.DateModified = nil, nil, nil, nil
	fl := func(v float64) *float64 { return &v }
	in := func(v int) *int { return &v }
	want := models.PurchaseOrder{ID: id, Number: num, Status: "draft", ApprovalStatus: "not_submitted", IsActive: true,
		Orderer: "New Orderer", SupplierID: in(f.Co), SupplierName: "Sup2", ReceiverContactID: in(f.ConD),
		ShippingCost: fl(3), TotalCost: fl(10 + 10 + 2 + 3), Notes: "n2"}
	if !reflect.DeepEqual(po, want) {
		t.Errorf("PO =\n%+v\nwant\n%+v", po, want)
	}
	wantL := []lineKey{{3, "", "", "l3", 2, 5, "", 0}, {5, f.PN1, "C", "upd", 4, 2.5, "V9", f.P1}, {7, "", "", "added", 2, 1, "", 0}}
	if got := lineKeys(mustItems(t, h, num)); !reflect.DeepEqual(got, wantL) {
		t.Errorf("lines = %+v, want %+v", got, wantL)
	}
	if got := lineKeys(mustItems(t, h, otherNum)); !reflect.DeepEqual(got, lineKeys(oLines)) {
		t.Errorf("other PO lines changed: %+v", got)
	}
	hist := h.fetchPOHistory(getReq("/po/x"), id)
	if len(hist) == 0 || hist[0].EventType != "approval" || hist[0].Action != "reset" || hist[0].Note != "PO edited after approved" || hist[0].ChangedBy != "system" {
		t.Errorf("latest history = %+v", hist)
	}

	for _, c := range []struct {
		num  string
		vals url.Values
		want string
	}{
		{num + "x", url.Values{"orderer": {"X"}}, "Error loading PO"},
		{num, url.Values{"orderer": {"X"}, "supplier_id": {co}, "delete_pol[]": {"abc"}}, "Error deleting PO line"},
	} {
		rec := httptest.NewRecorder()
		h.POUpdate(rec, withIDStr(postForm("/po/x", c.vals), c.num))
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("POUpdate %s: body missing %q", c.num, c.want)
		}
	}
	if po := mustPO(t, h, num); po.Orderer != "New Orderer" {
		t.Errorf("failed update not rolled back: orderer %q", po.Orderer)
	}
}

// POAddSuggestions: new supplier_part links (no duplicates, unchecked rows skipped), a new price
// replacing the active one at that pack size, default pack size 1, default supplier set.
func TestIntegration_POWrites_AddSuggestions(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	ctx := context.Background()
	co, p1, p2 := strconv.Itoa(f.Co), strconv.Itoa(f.P1), strconv.Itoa(f.P2)

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := h.queryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	links := func() string {
		t.Helper()
		var s string
		if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(string_agg(part_id::text || ':' || supplier_pn, ',' ORDER BY part_id, supplier_pn), '')
			FROM %s WHERE supplier_id=$1`, "supplier_part"), f.Co).Scan(&s); err != nil {
			t.Fatalf("links: %v", err)
		}
		return s
	}
	prices := func() string {
		t.Helper()
		var s string
		if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT string_agg(concat_ws(':', part_id, pack_size::float8, price_ea::float8,
			price_pack::float8, is_active, effective_date = $2::date), ',' ORDER BY part_id, is_active, id) FROM %s WHERE supplier_id=$1`,
			"price"), f.Co, time.Now().Format("2006-01-02")).Scan(&s); err != nil {
			t.Fatalf("prices: %v", err)
		}
		return s
	}
	defaults := func() string {
		t.Helper()
		var s string
		if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT string_agg(COALESCE(default_supplier_id::text, '-'), ',' ORDER BY id) FROM %s WHERE id IN ($1,$2)`,
			h.cfg().PartsTable()), f.P1, f.P2).Scan(&s); err != nil {
			t.Fatalf("defaults: %v", err)
		}
		return s
	}
	post := func(vals url.Values) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.POAddSuggestions(rec, withIDStr(postForm("/po/x/add-suggestions", vals), f.Full))
		assert302(t, "POAddSuggestions", rec)
		if loc := rec.Header().Get("Location"); loc != "/po/"+f.Full {
			t.Errorf("Location = %q", loc)
		}
	}
	all := url.Values{
		"links_count": {"3"},
		"add_0":       {"1"}, "part_id_0": {p1}, "supplier_pn_0": {"SPN-NEW"},
		"add_1": {"1"}, "part_id_1": {p1}, "supplier_pn_1": {"SPN-1"},
		"part_id_2": {p2}, "supplier_pn_2": {"X"},
		"prices_count": {"2"},
		"add_price_0":  {"1"}, "price_part_id_0": {p1}, "price_cost_0": {"3"}, "price_pack_size_0": {"10"},
		"add_price_1": {"1"}, "price_part_id_1": {p2}, "price_cost_1": {"4.5"}, "price_pack_size_1": {""},
	}

	beforeLinks, beforePrices := links(), prices()
	post(all) // no supplier_id: every row is skipped
	if links() != beforeLinks || prices() != beforePrices || defaults() != "-,-" {
		t.Errorf("no-supplier post changed data: %s / %s / %s", links(), prices(), defaults())
	}

	all.Set("supplier_id", co)
	post(all)
	if want := fmt.Sprintf("%d:SPN-1,%d:SPN-NEW", f.P1, f.P1); links() != want {
		t.Errorf("links = %s, want %s", links(), want)
	}
	if n := count(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE part_id=$1 AND supplier_id=$2 AND supplier_pn='SPN-1'`, "supplier_part"), f.P1, f.Co); n != 1 {
		t.Errorf("SPN-1 links = %d, want 1", n)
	}
	wantP := fmt.Sprintf("%[1]d:10:2.5:25:f:f,%[1]d:10:3:30:t:t,%[2]d:1:4:4:f:f,%[2]d:1:4.5:4.5:t:t", f.P1, f.P2)
	if got := prices(); got != wantP {
		t.Errorf("prices = %s, want %s", got, wantP)
	}
	if got, want := defaults(), co+","+co; got != want {
		t.Errorf("default suppliers = %s, want %s", got, want)
	}
}

// POMarkPrinted: approved or RFQ → date_printed today (204); otherwise 403 and unchanged.
func TestIntegration_POWrites_MarkPrinted(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	today := time.Now().Format("2006-01-02")

	for _, c := range []struct {
		num       string
		code      int
		wantPrint string
	}{
		{f.Full, http.StatusNoContent, today},
		{f.Bare, http.StatusForbidden, ""},
		{f.Q1, http.StatusNoContent, today},
		{f.Bare + "x", http.StatusForbidden, ""},
	} {
		rec := httptest.NewRecorder()
		h.POMarkPrinted(rec, withIDStr(postForm("/po/x/mark-printed", url.Values{}), c.num))
		assertStatus(t, "POMarkPrinted "+c.num, rec, c.code)
		if c.num == f.Bare+"x" {
			continue
		}
		if got := ymd(mustPO(t, h, c.num).DatePrinted); got != c.wantPrint {
			t.Errorf("%s date_printed = %q, want %q", c.num, got, c.wantPrint)
		}
	}
}

// createPOFolder: "<number> <supplier code>" plus the test-mode suffix; no or unknown supplier → bare number.
func TestIntegration_POWrites_CreatePOFolder(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	root := t.TempDir()
	saved := h.cfg().POFolderRoot
	h.cfg().POFolderRoot = root
	defer func() { h.cfg().POFolderRoot = saved }()
	suffix := ""
	if h.cfg().TestMode {
		suffix = "-testmode"
	}

	for _, c := range []struct{ num, sup, want string }{
		{"N1", strconv.Itoa(f.Co), "N1 PQ1" + suffix},
		{"N2", "", "N2" + suffix},
		{"N3", "abc", "N3" + suffix},
		{"N4", "2147483000", "N4" + suffix},
	} {
		h.createPOFolder(getReq("/po/x"), c.num, c.sup)
		if st, err := os.Stat(filepath.Join(root, c.want)); err != nil || !st.IsDir() {
			t.Errorf("createPOFolder(%q, %q): folder %q missing", c.num, c.sup, c.want)
		}
	}
}

// POImportPartFile copies a part's active LOCAL: attachment into the PO folder.
func TestIntegration_POWrites_ImportPartFile(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done) // t.Cleanup, not defer: the POs createPO registers must go before the fixture
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	ctx := context.Background()
	root, docRoot := t.TempDir(), t.TempDir()
	savedRoot, savedDoc := h.cfg().POFolderRoot, h.cfg().DocControlRoot
	h.cfg().POFolderRoot, h.cfg().DocControlRoot = root, docRoot
	defer func() { h.cfg().POFolderRoot, h.cfg().DocControlRoot = savedRoot, savedDoc }()
	if err := os.MkdirAll(filepath.Join(docRoot, "itest"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "itest", "pr-dwg.pdf"), []byte("dwg"), 0644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, f.Full+" PQ1")
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	post := func(num, att, part string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.POImportPartFile(rec, withIDStr(postForm("/po/x/import-part-file", url.Values{"att_id": {att}, "part_id": {part}}), num))
		return rec
	}
	att, p1, p2 := strconv.Itoa(f.Att), strconv.Itoa(f.P1), strconv.Itoa(f.P2)

	rec := post(f.Full, att, p1)
	assert302(t, "POImportPartFile", rec)
	if b, err := os.ReadFile(filepath.Join(folder, "pr-dwg.pdf")); err != nil || string(b) != "dwg" {
		t.Errorf("copied file = %q, %v", b, err)
	}

	for _, c := range []struct{ num, att, part, want string }{
		{f.Full + "x", att, p1, "Purchase order not found."},
		{f.Full, att, p2, "Attachment not found."},
		{f.Full, "abc", p1, "Attachment not found."},
		{f.Full, "", p1, "Missing attachment or part."},
	} {
		if body := post(c.num, c.att, c.part).Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("POImportPartFile(%s, %s, %s): body missing %q", c.num, c.att, c.part, c.want)
		}
	}
	smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET is_active=FALSE WHERE id=$1`, h.cfg().AttachmentsTable()), f.Att)
	if body := post(f.Full, att, p1).Body.String(); !strings.Contains(body, "Attachment not found.") {
		t.Errorf("inactive attachment: body missing not-found message")
	}
}
