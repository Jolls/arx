//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// Characterization of the RFQ writes (#221 slice 5): RFQCompareSave, RFQConvert and the BOM RFQ
// creator. Complements the RFQ tests in integration_test.go and the #191 races in
// tx_boundaries_integration_test.go.

// rfqCols renders cols as one '|'-joined string with NULL shown as "∅".
func rfqCols(cols ...string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = "COALESCE((" + c + ")::text,'∅')"
	}
	return "concat_ws('|'," + strings.Join(parts, ",") + ")"
}

func rfqStr(t *testing.T, h *Handler, q string, args ...any) string {
	t.Helper()
	var s string
	if err := h.queryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s
}

// seedRFQPO inserts a bare quote on the fixture company; group 0 anchors it to its own id, -1 leaves
// it out of any group.
func seedRFQPO(t *testing.T, h *Handler, f poFixture, num, status string, group int) int {
	t.Helper()
	var id int
	if err := h.queryRowContext(context.Background(), `INSERT INTO purchase_order (number, supplier_id, status, is_active, date_modified)
		VALUES ($1,$2,$3,TRUE,'2026-01-01') RETURNING id`, num, f.Co, status).Scan(&id); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	t.Cleanup(func() { cleanupPO(context.Background(), h, id) })
	if group == 0 {
		group = id
	}
	if group > 0 {
		if _, err := h.execContext(context.Background(), `UPDATE purchase_order SET rfq_group_id=$2 WHERE id=$1`, id, group); err != nil {
			t.Fatalf("seed quote group: %v", err)
		}
	}
	return id
}

func seedRFQLine(t *testing.T, h *Handler, poID, n int, part any, qty, cost float64, lead any) int {
	t.Helper()
	var id int
	if err := h.queryRowContext(context.Background(), `INSERT INTO po_line (po_id, line_number, part_id, part_number_snapshot,
		revision_snapshot, description, vendor_part_number, qty, unit_cost, lead_time_days, received_qty)
		VALUES ($1,$2,$3,$4,'B',$5,$9,$6,$7,$8,3) RETURNING id`,
		poID, n, part, "SNAP-"+strconv.Itoa(n), "line "+strconv.Itoa(n), qty, cost, lead, "VPN-"+strconv.Itoa(n)).Scan(&id); err != nil {
		t.Fatalf("seed line: %v", err)
	}
	return id
}

func rfqNoFolder(h *Handler, t *testing.T) {
	saved := h.cfg().POFolderRoot
	h.cfg().POFolderRoot = ""
	t.Cleanup(func() { h.cfg().POFolderRoot = saved })
}

func TestIntegration_RFQ_CompareSave(t *testing.T) {
	h, f := lifecycleSetup(t)
	today := dbToday(t, h)
	base := strings.TrimSuffix(f.Full, "-f")
	qa := seedRFQPO(t, h, f, base+"-aR1", "rfq", 0)
	qb := seedRFQPO(t, h, f, base+"-bR1", "rfq", 0)
	if _, err := h.execContext(context.Background(), `UPDATE purchase_order SET tax1=1.5, shipping_cost=2, misc_cost=0.5 WHERE id=$1`, qa); err != nil {
		t.Fatal(err)
	}
	if _, err := h.execContext(context.Background(), `UPDATE purchase_order SET total_cost=45 WHERE id=$1`, qb); err != nil {
		t.Fatal(err)
	}
	l1 := seedRFQLine(t, h, qa, 1, f.P1, 10, 9, 3)
	l2 := seedRFQLine(t, h, qa, 2, f.P2, 4, 3, 5)
	lb := seedRFQLine(t, h, qb, 1, f.P1, 5, 9, 2)

	quote := func(id int) string {
		return rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM po_line WHERE id=$1`, rfqCols("unit_cost::float8", "lead_time_days")), id)
	}
	post := func(group string, vals url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.RFQCompareSave(rec, withGroupParam(postForm("/rfq/"+group+"/compare", vals), group))
		return rec
	}

	// cost/lead posted for a foreign group's line are ignored; the unposted line resets to 0 / NULL.
	rec := post(strconv.Itoa(qa), url.Values{
		fmt.Sprintf("cost_%d", l1): {"2.5"}, fmt.Sprintf("lead_%d", l1): {"7"},
		fmt.Sprintf("cost_%d", lb): {"1"}, fmt.Sprintf("lead_%d", lb): {"1"},
	})
	assert302(t, "RFQCompareSave", rec)
	if got := quote(l1); got != "2.5|7" {
		t.Errorf("posted line = %q, want 2.5|7", got)
	}
	if got := quote(l2); got != "0|∅" {
		t.Errorf("unposted line = %q, want 0|∅", got)
	}
	if got := quote(lb); got != "9|2" {
		t.Errorf("foreign-group line = %q, want 9|2 (untouched)", got)
	}
	if got := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE id=$1`, rfqCols("total_cost::float8", "date_modified::date")), qa); got != "29|"+today {
		t.Errorf("group quote total|modified = %q, want 29|%s (25 lines + 1.5 tax + 2 shipping + 0.5 misc)", got, today)
	}
	if got := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE id=$1`, rfqCols("total_cost::float8", "date_modified::date")), qb); got != "45|2026-01-01" {
		t.Errorf("foreign-group quote total|modified = %q, want 45|2026-01-01 (untouched)", got)
	}

	// Blank cost → 0, junk lead → NULL.
	assert302(t, "RFQCompareSave blank", post(strconv.Itoa(qa), url.Values{
		fmt.Sprintf("cost_%d", l1): {""}, fmt.Sprintf("lead_%d", l1): {"soon"},
	}))
	if got := quote(l1); got != "0|∅" {
		t.Errorf("blank line = %q, want 0|∅", got)
	}

	// A group with no quotes saves nothing but still redirects; a non-numeric group errors.
	assert302(t, "RFQCompareSave unknown group", post("2147483000", url.Values{}))
	rec = post("abc", url.Values{})
	assertStatus(t, "RFQCompareSave non-numeric group", rec, 200)
	if !strings.Contains(rec.Body.String(), "Error loading RFQ lines") {
		t.Errorf("non-numeric group: expected load error, got: %s", rec.Body.String())
	}
}

func TestIntegration_RFQ_ConvertCopiesQuote(t *testing.T) {
	h, f := lifecycleSetup(t)
	rfqNoFolder(h, t)
	ctx := context.Background()
	today := dbToday(t, h)
	base := strings.TrimSuffix(f.Full, "-f") + "-w"
	num := base + "R1"

	var win int
	if err := h.queryRowContext(ctx, `INSERT INTO purchase_order (number, status, approval_status, is_active, orderer, account_id,
		supplier_id, supplier_name, supplier_contact, supplier_email, supplier_address, supplier_city, supplier_state,
		supplier_zipcode, supplier_country, supplier_phone_number, supplier_fax_number,
		receiver_id, receiver_name, receiver_contact, receiver_email, receiver_address, receiver_city, receiver_state,
		receiver_zipcode, receiver_country, receiver_phone, receiver_fax,
		tax1, shipping_cost, misc_cost, total_cost, notes, internal_notes,
		date_ordered, date_requested, date_closed, date_printed, date_modified, supplier_contact_id, receiver_contact_id)
		VALUES ($1,'rfq','approved',TRUE,'Olive Orderer','ACCT-9',
		$2,$3,'Sam Sup','s@example.com','3 Sup St','Supton','SS','33333','Freedonia','555-0501','555-0502',
		$2,'Recv Co','Rae Recv','r@example.com','4 Recv Rd','Recvton','RS','44444','Ruritania','555-0601','555-0602',
		1.25,2.5,3.75,100.5,'quote notes','quote internal',
		'2026-01-15','2026-01-20','2026-03-01','2026-01-16','2026-01-01',$4,$5) RETURNING id`,
		num, f.Co, f.CoName, f.ConO, f.ConD).Scan(&win); err != nil {
		t.Fatalf("seed winner: %v", err)
	}
	t.Cleanup(func() { cleanupPO(ctx, h, win) })
	if _, err := h.execContext(ctx, `UPDATE purchase_order SET rfq_group_id=id WHERE id=$1`, win); err != nil {
		t.Fatal(err)
	}
	seedRFQLine(t, h, win, 1, f.P1, 10, 2.5, 7)
	seedRFQLine(t, h, win, 2, nil, 4, 3, nil)
	sib1 := seedRFQPO(t, h, f, base+"R2", "rfq", win)
	sib2 := seedRFQPO(t, h, f, base+"R3", "cancelled", win) // not open any more: left alone
	sib3 := seedRFQPO(t, h, f, base+"R4", "rfq", win)
	other := seedRFQPO(t, h, f, base+"-oR1", "rfq", 0) // another group: untouched
	t.Cleanup(func() {
		id, err := strconv.Atoi(rfqStr(t, h, `SELECT COALESCE((SELECT id FROM purchase_order WHERE number=$1),0)`, base))
		if err == nil && id != 0 {
			cleanupPO(ctx, h, id)
		}
	})

	req := withIDStr(postForm("/rfq/{id}/convert", url.Values{}), num)
	actor := h.actorName(req)
	rec := httptest.NewRecorder()
	h.RFQConvert(rec, req)
	assert302(t, "RFQConvert", rec)
	if loc := rec.Header().Get("Location"); loc != "/po/"+base {
		t.Fatalf("Location = %q, want /po/%s", loc, base)
	}

	copied := rfqCols("orderer", "account_id", "supplier_id", "supplier_name", "supplier_contact", "supplier_email",
		"supplier_address", "supplier_city", "supplier_state", "supplier_zipcode", "supplier_country",
		"supplier_phone_number", "supplier_fax_number", "receiver_id", "receiver_name", "receiver_contact",
		"receiver_email", "receiver_address", "receiver_city", "receiver_state", "receiver_zipcode",
		"receiver_country", "receiver_phone", "receiver_fax", "tax1::float8", "shipping_cost::float8",
		"misc_cost::float8", "total_cost::float8", "notes", "internal_notes", "date_requested",
		"supplier_contact_id", "receiver_contact_id")
	want := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE id=$1`, copied), win)
	if !strings.Contains(want, "Olive Orderer") || !strings.Contains(want, "Recv Co") || !strings.Contains(want, "quote internal") {
		t.Fatalf("source header looks wrong: %s", want)
	}
	if got := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE number=$1`, copied), base); got != want {
		t.Errorf("copied header = %q, want %q", got, want)
	}
	newID, _ := strconv.Atoi(rfqStr(t, h, `SELECT id FROM purchase_order WHERE number=$1`, base))
	if got := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE id=$1`, rfqCols("status", "approval_status", "is_active", "rfq_group_id",
		"date_ordered", "date_closed", "date_printed", "date_modified::date")), newID); got != "draft|not_submitted|true|∅|"+userToday(h)+"|∅|∅|"+today {
		t.Errorf("new PO state = %q", got)
	}

	lines := fmt.Sprintf(`SELECT COALESCE(string_agg(%s, ';' ORDER BY line_number),'') FROM po_line WHERE po_id=$1`,
		rfqCols("line_number", "part_number_snapshot", "revision_snapshot", "description", "qty::float8", "unit_cost::float8",
			"vendor_part_number", "part_id", "lead_time_days"))
	if got, w := rfqStr(t, h, lines, newID), rfqStr(t, h, lines, win); got != w || !strings.Contains(w, ";") {
		t.Errorf("copied lines = %q, want %q (two lines)", got, w)
	}
	if got := rfqStr(t, h, `SELECT COALESCE(SUM(received_qty),0)::float8::text FROM po_line WHERE po_id=$1`, newID); got != "0" {
		t.Errorf("new lines received_qty sum = %s, want 0 (not copied)", got)
	}

	if got, w := lastEvent(t, h, newID, "status"), "∅>draft|∅|Converted from RFQ "+num+"|"+actor; got != w {
		t.Errorf("new PO history = %q, want %q", got, w)
	}
	if s := readPOState(t, h, win); s.Status != "closed" || s.Active || s.Modified != today {
		t.Errorf("winner = %+v, want closed/inactive/modified today", s)
	}
	if got, w := lastEvent(t, h, win, "status"), "rfq>closed|∅|Awarded — converted to PO #"+base+"|"+actor; got != w {
		t.Errorf("winner history = %q, want %q", got, w)
	}
	for _, id := range []int{sib1, sib3} {
		if s := readPOState(t, h, id); s.Status != "cancelled" || s.Active || s.Modified != today {
			t.Errorf("sibling %d = %+v, want cancelled/inactive/modified today", id, s)
		}
		if got, w := lastEvent(t, h, id, "status"), "rfq>cancelled|∅|Not awarded — PO #"+base+" issued|"+actor; got != w {
			t.Errorf("sibling %d history = %q, want %q", id, got, w)
		}
	}
	if s := readPOState(t, h, sib2); s.Status != "cancelled" || s.Modified != "2026-01-01" || lastEvent(t, h, sib2, "status") != "" {
		t.Errorf("already-cancelled sibling was touched: %+v", s)
	}
	if s := readPOState(t, h, other); s.Status != "rfq" || !s.Active || lastEvent(t, h, other, "status") != "" {
		t.Errorf("other group's quote was touched: %+v", s)
	}
}

func TestIntegration_RFQ_ConvertEdges(t *testing.T) {
	h, f := lifecycleSetup(t)
	rfqNoFolder(h, t)
	ctx := context.Background()
	base := strings.TrimSuffix(f.Full, "-f") + "-e"

	rec := httptest.NewRecorder()
	h.RFQConvert(rec, withIDStr(postForm("/rfq/{id}/convert", url.Values{}), base+"R9"))
	assertStatus(t, "RFQConvert unknown", rec, 200)
	if !strings.Contains(rec.Body.String(), "Purchase order not found") {
		t.Errorf("unknown quote: expected not-found error, got: %s", rec.Body.String())
	}

	// A quote outside any group converts without a group lock.
	id := seedRFQPO(t, h, f, base+"R1", "rfq", -1)
	seedRFQLine(t, h, id, 1, f.P1, 2, 1, nil)
	t.Cleanup(func() {
		if n, err := strconv.Atoi(rfqStr(t, h, `SELECT COALESCE((SELECT id FROM purchase_order WHERE number=$1),0)`, base)); err == nil && n != 0 {
			cleanupPO(ctx, h, n)
		}
	})
	rec = httptest.NewRecorder()
	h.RFQConvert(rec, withIDStr(postForm("/rfq/{id}/convert", url.Values{}), base+"R1"))
	assert302(t, "RFQConvert ungrouped", rec)
	if s := readPOState(t, h, id); s.Status != "closed" || s.Active {
		t.Errorf("ungrouped quote = %+v, want closed/inactive", s)
	}
	if got := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE number=$1`, rfqCols("status", "supplier_id")), base); got != fmt.Sprintf("draft|%d", f.Co) {
		t.Errorf("new PO = %q", got)
	}
}

func TestIntegration_RFQ_BOMConfirmCreatesQuotes(t *testing.T) {
	h, f := lifecycleSetup(t)
	ctx := context.Background()

	root := smokeUniq("ITEST-RFQ-ROOT")
	var rootID, p3 int
	if err := h.queryRowContext(ctx, `INSERT INTO part (part_number, category, description, revision) VALUES ($1,'ASM','root','A') RETURNING id`, root).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if err := h.queryRowContext(ctx, `INSERT INTO part (part_number, category, description, revision) VALUES ($1,'BUY','p3','D') RETURNING id`, root+"-P3").Scan(&p3); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, id := range []int{rootID, p3} {
			smokeExec(ctx, h, `DELETE FROM bom WHERE parent_part_id=$1 OR component_part_id=$1`, id)
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
		var ids []int
		rows, err := h.queryContext(ctx, `SELECT id FROM purchase_order WHERE internal_notes=$1`, "Created from BOM of "+root)
		if err != nil {
			return
		}
		for rows.Next() {
			var id int
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			cleanupPO(ctx, h, id)
		}
	})
	// P1 → fixture company, P2 → seeded Acme (1001), P3 → fixture company but never posted.
	for _, s := range []struct{ part, supplier int }{{f.P1, f.Co}, {f.P2, 1001}, {p3, f.Co}} {
		if _, err := h.execContext(ctx, `UPDATE part SET default_supplier_id=$2 WHERE id=$1`, s.part, s.supplier); err != nil {
			t.Fatal(err)
		}
		if _, err := h.execContext(ctx, `INSERT INTO bom (parent_part_id, component_part_id, qty) VALUES ($1,$2,1)`, rootID, s.part); err != nil {
			t.Fatal(err)
		}
	}

	req := withID(postForm("/part/x/create-rfqs", url.Values{
		"n": {"2"}, "pid": {strconv.Itoa(f.P1), strconv.Itoa(f.P2)}, "qty": {"7", "2"},
	}), rootID)
	actor := h.actorName(req)
	rec := httptest.NewRecorder()
	h.PartCreateRFQsConfirm(rec, req)
	assertStatus(t, "PartCreateRFQsConfirm", rec, 200)

	rows, err := h.queryContext(ctx, `SELECT id, number FROM purchase_order WHERE internal_notes=$1 ORDER BY id`, "Created from BOM of "+root)
	if err != nil {
		t.Fatal(err)
	}
	type quote struct {
		id  int
		num string
	}
	var qs []quote
	for rows.Next() {
		var q quote
		rows.Scan(&q.id, &q.num)
		qs = append(qs, q)
	}
	rows.Close()
	if len(qs) != 2 {
		t.Fatalf("created %d quotes, want 2 (Acme, fixture company): %s", len(qs), rec.Body.String())
	}
	for _, q := range qs {
		if !strings.HasSuffix(q.num, "R1") || strings.Trim(strings.TrimSuffix(q.num, "R1"), "0123456789") != "" {
			t.Errorf("quote number %q, want <sequence>R1", q.num)
		}
	}

	header := func(id int) string {
		return rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE id=$1`, rfqCols("status", "is_active", "approval_status", "orderer", "account_id",
			"supplier_id", "supplier_name", "supplier_contact", "supplier_email", "supplier_address", "supplier_city", "supplier_state",
			"supplier_zipcode", "supplier_country", "supplier_phone_number", "supplier_fax_number",
			"receiver_id", "receiver_name", "receiver_contact", "receiver_email", "receiver_address", "receiver_city", "receiver_state",
			"receiver_zipcode", "receiver_country", "receiver_phone", "receiver_fax",
			"tax1::float8", "shipping_cost::float8", "misc_cost::float8", "total_cost::float8", "notes", "internal_notes",
			"date_ordered", "date_requested", "date_closed", "rfq_group_id = id", "supplier_contact_id", "receiver_contact_id")), id)
	}
	lineStr := func(id int) string {
		return rfqStr(t, h, fmt.Sprintf(`SELECT COALESCE(string_agg(%s, ';' ORDER BY line_number),'') FROM po_line WHERE po_id=$1`,
			rfqCols("line_number", "part_number_snapshot", "revision_snapshot", "description", "qty::float8", "unit_cost::float8",
				"vendor_part_number", "part_id", "lead_time_days")), id)
	}
	notes := "Created from BOM of " + root
	wantCo := strings.Join([]string{"rfq", "true", "not_submitted", "", "", strconv.Itoa(f.Co), f.CoName, f.ConDName, "d@example.com",
		"1 Dock Rd", "Dockton", "DS", "11111", "Freedonia", "555-0301", "555-0302",
		"∅", "", "", "", "", "", "", "", "", "", "",
		"0", "0", "0", "0", "", notes, "∅", userToday(h), "∅", "true", strconv.Itoa(f.ConD), "∅"}, "|")
	if got := header(qs[1].id); got != wantCo {
		t.Errorf("fixture-company quote header:\n got %q\nwant %q", got, wantCo)
	}
	if got := header(qs[0].id); !strings.HasPrefix(got, "rfq|true|not_submitted|||1001|Acme Fasteners|") || !strings.Contains(got, "|"+notes+"|∅|"+userToday(h)+"|∅|true|") {
		t.Errorf("Acme quote header = %q", got)
	}
	if got, w := lineStr(qs[1].id), fmt.Sprintf("1|%s|C|p1|7|0||%d|∅", f.PN1, f.P1); got != w {
		t.Errorf("fixture-company lines = %q, want %q (only the posted line)", got, w)
	}
	if got, w := lineStr(qs[0].id), fmt.Sprintf("1|%s||p2|2|0||%d|∅", f.PN2, f.P2); got != w {
		t.Errorf("Acme lines = %q, want %q (NULL revision snapshots as blank)", got, w)
	}
	for _, q := range qs {
		if got, w := lastEvent(t, h, q.id, "status"), "∅>rfq|∅|∅|"+actor; got != w {
			t.Errorf("quote %s history = %q, want %q", q.num, got, w)
		}
	}
}

// Saving a stale compare page must not rewrite quotes already awarded or cancelled (#262).
func TestIntegration_RFQ_CompareSave_SkipsNonRFQQuotes(t *testing.T) {
	h, f := lifecycleSetup(t)
	base := strings.TrimSuffix(f.Full, "-f")
	qa := seedRFQPO(t, h, f, base+"-aR2", "rfq", 0)
	qb := seedRFQPO(t, h, f, base+"-bR2", "closed", qa)
	qc := seedRFQPO(t, h, f, base+"-cR2", "cancelled", qa)
	la := seedRFQLine(t, h, qa, 1, f.P1, 10, 9, 2)
	lb := seedRFQLine(t, h, qb, 1, f.P1, 10, 9, 2)
	lc := seedRFQLine(t, h, qc, 1, f.P1, 10, 9, 2)
	for _, id := range []int{qa, qb, qc} {
		if _, err := h.execContext(context.Background(), `UPDATE purchase_order SET total_cost=45 WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	group := strconv.Itoa(qa)
	h.RFQCompareSave(rec, withGroupParam(postForm("/rfq/"+group+"/compare", url.Values{
		fmt.Sprintf("cost_%d", la): {"1"}, fmt.Sprintf("lead_%d", la): {"4"},
		fmt.Sprintf("cost_%d", lb): {"1"}, fmt.Sprintf("lead_%d", lb): {"4"},
		fmt.Sprintf("cost_%d", lc): {"1"}, fmt.Sprintf("lead_%d", lc): {"4"},
	}), group))
	assert302(t, "RFQCompareSave", rec)

	line := func(id int) string {
		return rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM po_line WHERE id=$1`, rfqCols("unit_cost::float8", "lead_time_days")), id)
	}
	total := func(id int) string {
		return rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM purchase_order WHERE id=$1`, rfqCols("total_cost::float8", "date_modified::date")), id)
	}
	if got := line(la); got != "1|4" {
		t.Errorf("open quote line = %q, want 1|4", got)
	}
	if got := total(qa); !strings.HasPrefix(got, "10|") {
		t.Errorf("open quote total|modified = %q, want recomputed 10|...", got)
	}
	for name, id := range map[string]int{"closed": lb, "cancelled": lc} {
		if got := line(id); got != "9|2" {
			t.Errorf("%s quote line = %q, want 9|2 (untouched)", name, got)
		}
	}
	for name, id := range map[string]int{"closed": qb, "cancelled": qc} {
		if got := total(id); got != "45|2026-01-01" {
			t.Errorf("%s quote total|modified = %q, want 45|2026-01-01 (untouched)", name, got)
		}
	}
}

// A group whose quotes are all closed or cancelled rejects the save instead of rewriting them (#262).
func TestIntegration_RFQ_CompareSave_RejectsWhenNoOpenQuote(t *testing.T) {
	h, f := lifecycleSetup(t)
	base := strings.TrimSuffix(f.Full, "-f")
	qa := seedRFQPO(t, h, f, base+"-aR3", "closed", 0)
	qb := seedRFQPO(t, h, f, base+"-bR3", "cancelled", qa)
	la := seedRFQLine(t, h, qa, 1, f.P1, 10, 9, 2)
	lb := seedRFQLine(t, h, qb, 1, f.P1, 10, 9, 2)

	rec := httptest.NewRecorder()
	group := strconv.Itoa(qa)
	h.RFQCompareSave(rec, withGroupParam(postForm("/rfq/"+group+"/compare", url.Values{
		fmt.Sprintf("cost_%d", la): {"1"}, fmt.Sprintf("cost_%d", lb): {"1"},
	}), group))
	assertStatus(t, "RFQCompareSave", rec, 200)
	if !strings.Contains(rec.Body.String(), "nothing was saved") {
		t.Errorf("body missing the no-open-quote error: %s", rec.Body.String())
	}
	for _, id := range []int{la, lb} {
		if got := rfqStr(t, h, fmt.Sprintf(`SELECT %s FROM po_line WHERE id=$1`, rfqCols("unit_cost::float8", "lead_time_days")), id); got != "9|2" {
			t.Errorf("line %d = %q, want 9|2 (untouched)", id, got)
		}
	}
}
