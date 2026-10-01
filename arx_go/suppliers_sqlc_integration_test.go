//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// supFixture is a throwaway company with contacts, a primary attachment, six linked parts and
// eight POs (#221). Contacts sort as ConA < ConD (default) < ConZ; ConX is inactive.
type supFixture struct {
	ID                     int
	Name                   string
	ConA, ConD, ConZ, ConX string
	PN                     [6]string // part numbers, sorted
	Parts                  [6]int
	PO                     map[string]string // key → number
}

func seedSupFixture(t *testing.T, h *Handler) (f supFixture, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	base := smokeUniq("SQ") // purchase_order.number is VARCHAR(32)
	f.Name = base + "-co"
	f.PO = map[string]string{}
	var poIDs []int
	cleanup = func() {
		if f.ID == 0 {
			return
		}
		smokeExec(ctx, h, `UPDATE company SET default_contact=NULL, primary_attachment_id=NULL WHERE id=$1`, f.ID)
		for _, id := range poIDs {
			for _, tbl := range []string{"po_line", "purchase_order_history"} {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE po_id=$1`, tbl), id)
			}
			smokeExec(ctx, h, `DELETE FROM purchase_order WHERE id=$1`, id)
		}
		for _, id := range f.Parts {
			for _, tbl := range []string{"part_attachment", "supplier_part"} {
				smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE part_id=$1`, tbl), id)
			}
			smokeExec(ctx, h, `DELETE FROM part WHERE id=$1`, id)
		}
		smokeExec(ctx, h, `DELETE FROM company_attachment WHERE supplier_id=$1`, f.ID)
		smokeExec(ctx, h, `DELETE FROM contact WHERE company_id=$1`, f.ID)
		smokeExec(ctx, h, `DELETE FROM company WHERE id=$1`, f.ID)
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

	scan(&f.ID, `INSERT INTO company (name, supplier_code, notes, is_active, is_supplier, is_manufacturer,
		bulk_order_delimiter, bulk_order_pn_source) VALUES ($1,'SQ1','fixture notes',TRUE,TRUE,FALSE,'tab','vendor')
		RETURNING id`, f.Name)

	f.ConA, f.ConD, f.ConZ, f.ConX = base+"-cA", base+"-cD", base+"-cZ", base+"-cX"
	var conD int
	scan(&conD, `INSERT INTO contact (display_name, company_id, address, city, state, zipcode, country, phone_1, fax, email, is_active)
		VALUES ($1,$2,'1 Main St','Townd','ST','12345','Freedonia','555-0101','555-0102','d@example.com',TRUE) RETURNING id`, f.ConD, f.ID)
	exec(`INSERT INTO contact (display_name, company_id, address, city, state, zipcode, country, phone_1, fax, email, is_active) VALUES
		($1,$4,'2 Side St','Towna','AA','99999','Ruritania','555-0201','555-0202','a@example.com',TRUE),
		($2,$4,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,TRUE),
		($3,$4,'gone','gone','gone','gone','gone','gone','gone','gone',FALSE)`, f.ConA, f.ConZ, f.ConX, f.ID)
	var att int
	scan(&att, `INSERT INTO company_attachment (supplier_id, file_path, notes) VALUES ($1,'https://example.com/sq.pdf','Quote sheet')
		RETURNING supplier_attachment_id`, f.ID)
	exec(`UPDATE company SET default_contact=$2, primary_attachment_id=$3 WHERE id=$1`, f.ID, conD, att)

	// Parts 1-6 (inserted out of order): P1 has an explicit purchase unit, a thumbnail and a min
	// increment; P2 inherits the part's base unit; P3 has no unit at all.
	var ea, kg int
	scan(&ea, `SELECT uom_id FROM uom WHERE abbreviation='EA'`)
	scan(&kg, `SELECT uom_id FROM uom WHERE abbreviation='kg'`)
	for _, i := range []int{3, 0, 5, 1, 4, 2} {
		f.PN[i] = fmt.Sprintf("%s-P%d", base, i+1)
		var uom any
		if i == 1 {
			uom = kg
		}
		scan(&f.Parts[i], `INSERT INTO part (part_number, description, revision, category, uom_id)
			VALUES ($1,$2,'B','BUY',$3) RETURNING id`, f.PN[i], fmt.Sprintf("desc %d", i+1), uom)
		var spUom, minIncr any
		if i == 0 {
			spUom, minIncr = ea, 2.5
		}
		exec(`INSERT INTO supplier_part (part_id, supplier_id, supplier_pn, supplier_desc, lead_time, preference, uom_id, min_increment)
			VALUES ($1,$2,$3,'sd','3 wk',1,$4,$5)`,
			f.Parts[i], f.ID, fmt.Sprintf("SPN-%d", i+1), spUom, minIncr)
	}
	exec(`INSERT INTO part_attachment (part_id, file_name, category, is_active) VALUES ($1,'LOCAL:itest\sq-thumb.png',$2,TRUE)`,
		f.Parts[0], thumbnailCategory)

	// POs d1..d6 dated, nd undated, rq an RFQ quote dated last. Lines: P1 on d1, d2 and rq; P2 on nd.
	for _, c := range []struct {
		key, ordered string
		total        float64
		rfq          any
		part         int
	}{
		{"d1", "2026-01-01", 10, nil, 0}, {"d2", "2026-01-02", 20, nil, 0}, {"d3", "2026-01-03", 30, nil, -1},
		{"d4", "2026-01-04", 40, nil, -1}, {"d5", "2026-01-05", 50, nil, -1}, {"d6", "2026-01-06", 60.5, nil, -1},
		{"nd", "", 70, nil, 1}, {"rq", "2026-12-31", 80, 999999, 0},
	} {
		f.PO[c.key] = base + "-" + c.key
		var id int
		scan(&id, `INSERT INTO purchase_order (number, supplier_id, date_ordered, total_cost, status, rfq_group_id)
			VALUES ($1,$2,NULLIF($3,'')::date,$4,'open',$5) RETURNING id`, f.PO[c.key], f.ID, c.ordered, c.total, c.rfq)
		poIDs = append(poIDs, id)
		if c.part >= 0 {
			exec(`INSERT INTO po_line (po_id, part_id, line_number, qty, unit_cost, part_number_snapshot, vendor_part_number)
				VALUES ($1,$2,1,1,1,$3,'VPN-Q')`, id, f.Parts[c.part], f.PN[c.part])
		}
	}
	return f, cleanup
}

func supplierPage(t *testing.T, h *Handler, fn http.HandlerFunc, path string, id int) string {
	t.Helper()
	rec := httptest.NewRecorder()
	fn(rec, withID(httptest.NewRequest(http.MethodGet, path, nil), id))
	assertStatus(t, path, rec, http.StatusOK)
	return rec.Body.String()
}

// assertInOrder fails unless every want appears in body, in order.
func assertInOrder(t *testing.T, label, body string, want ...string) {
	t.Helper()
	last := -1
	for _, w := range want {
		i := strings.Index(body, w)
		if i < 0 || i < last {
			t.Errorf("%s: %q missing or out of order", label, w)
			return
		}
		last = i
	}
}

// SuppliersRows: the fixture row carries its code, default contact/country and trigger counts.
func TestIntegration_SupplierSQLC_Rows(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.SuppliersRows(rec, httptest.NewRequest(http.MethodGet, "/suppliers/rows", nil))
	assertStatus(t, "SuppliersRows", rec, http.StatusOK)
	type row struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Active  bool   `json:"active"`
		Country string `json:"country"`
		Links   int    `json:"links"`
		POs     int    `json:"pos"`
		Contact string `json:"contact"`
		Code    string `json:"code"`
	}
	var rows []row
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := row{ID: f.ID, Name: f.Name, Active: true, Country: "Freedonia", Links: 6, POs: 8, Contact: f.ConD, Code: "SQ1"}
	for _, r := range rows {
		if r.ID == f.ID {
			if r != want {
				t.Errorf("row = %+v, want %+v", r, want)
			}
			return
		}
	}
	t.Fatalf("fixture company %d not in rows", f.ID)
}

// SupplierDetail: primary attachment label, recent POs (5, date_ordered DESC so the undated PO
// leads), top parts (5, by part number), other contacts minus the default and inactive ones.
func TestIntegration_SupplierSQLC_Detail(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()

	body := supplierPage(t, h, h.SupplierDetail, fmt.Sprintf("/supplier/%d", f.ID), f.ID)
	for _, want := range []string{"Quote sheet", "fixture notes", "SQ1", "Townd"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	assertInOrder(t, "recent POs", body, ">"+f.PO["nd"]+"<", ">"+f.PO["rq"]+"<", ">"+f.PO["d6"]+"<", ">"+f.PO["d5"]+"<", ">"+f.PO["d4"]+"<")
	if strings.Contains(body, f.PO["d3"]) {
		t.Errorf("recent POs: %s shown past the 5-row limit", f.PO["d3"])
	}
	if !strings.Contains(body, "$60.50") {
		t.Errorf("recent POs: total $60.50 missing")
	}
	assertInOrder(t, "top parts", body, ">"+f.PN[0]+"<", ">"+f.PN[1]+"<", ">"+f.PN[2]+"<", ">"+f.PN[3]+"<", ">"+f.PN[4]+"<")
	if strings.Contains(body, f.PN[5]) {
		t.Errorf("top parts: %s shown past the 5-row limit", f.PN[5])
	}
	assertInOrder(t, "other contacts", body, f.ConA, f.ConZ)
	if n := strings.Count(body, f.ConD); n != 1 {
		t.Errorf("default contact appears %d times, want 1", n)
	}
	if strings.Contains(body, f.ConX) {
		t.Errorf("inactive contact shown")
	}
}

// SupplierPOs lists every PO (RFQ quotes included), date_ordered DESC.
func TestIntegration_SupplierSQLC_POs(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()

	body := supplierPage(t, h, h.SupplierPOs, fmt.Sprintf("/supplier/%d/pos", f.ID), f.ID)
	var want []string
	for _, k := range []string{"nd", "rq", "d6", "d5", "d4", "d3", "d2", "d1"} {
		want = append(want, ">"+f.PO[k]+"</a>")
	}
	assertInOrder(t, "POs tab", body, want...)
	if !strings.Contains(body, "$60.50") {
		t.Errorf("POs tab: total $60.50 missing")
	}
}

// SupplierParts: links by part number with thumb, explicit/inherited/no unit, min increment, and
// the part's non-RFQ PO numbers DESC; unknown supplier → not found.
func TestIntegration_SupplierSQLC_Parts(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()

	body := supplierPage(t, h, h.SupplierParts, fmt.Sprintf("/supplier/%d/parts", f.ID), f.ID)
	var pns []string
	for _, p := range f.PN {
		pns = append(pns, ">"+p+"</a>")
	}
	assertInOrder(t, "parts tab", body, pns...)
	if n := strings.Count(body, "data-thumb="); n != 1 {
		t.Errorf("parts tab: %d thumbnails, want 1", n)
	}
	for _, want := range []string{
		"SPN-1", "sd", "3 wk", ">2.5<", "desc 1",
		`title="Base unit (inherited from part)">kg<`,
		f.PO["d2"] + `</a>, <a href="/po/` + f.PO["d1"] + `"`,
		`>` + f.PO["nd"] + `</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("parts tab missing %q", want)
		}
	}
	if !regexp.MustCompile(`<td>\s*EA\s*</td>`).MatchString(body) {
		t.Errorf("parts tab: explicit EA unit missing")
	}
	if strings.Contains(body, f.PO["rq"]) {
		t.Errorf("parts tab: RFQ quote %s listed as a PO link", f.PO["rq"])
	}

	rec := httptest.NewRecorder()
	h.SupplierParts(rec, withID(httptest.NewRequest(http.MethodGet, "/supplier/999999999/parts", nil), 999999999))
	if !strings.Contains(rec.Body.String(), "Supplier not found") {
		t.Errorf("unknown supplier: body missing %q", "Supplier not found")
	}
}

// SuppliersCreate persists every field with the bulk-order defaults; a duplicate name re-renders
// the error.
func TestIntegration_SupplierSQLC_Create(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()
	var conD int
	if err := h.queryRowContext(ctx, `SELECT default_contact FROM company WHERE id=$1`, f.ID).Scan(&conD); err != nil {
		t.Fatal(err)
	}

	name := smokeUniq("SQC")
	rec := httptest.NewRecorder()
	h.SuppliersCreate(rec, postForm("/suppliers", url.Values{
		"name": {name}, "supplier_code": {"SQC"}, "default_contact": {strconv.Itoa(conD)},
		"is_active": {"1"}, "is_manufacturer": {"1"}, "notes": {"created"},
	}))
	id := locID(t, rec, "/supplier/")
	defer smokeExec(ctx, h, "DELETE FROM company WHERE id=$1", id)

	var gotName, code, notes, delim, pnSrc string
	var active, supplier, mfg bool
	var dc *int
	if err := h.queryRowContext(ctx, `SELECT name, supplier_code, notes, is_active, is_supplier, is_manufacturer,
		default_contact, bulk_order_delimiter, bulk_order_pn_source FROM company WHERE id=$1`, id,
	).Scan(&gotName, &code, &notes, &active, &supplier, &mfg, &dc, &delim, &pnSrc); err != nil {
		t.Fatal(err)
	}
	if gotName != name || code != "SQC" || notes != "created" || !active || supplier || !mfg ||
		dc == nil || *dc != conD || delim != "comma" || pnSrc != "internal" {
		t.Errorf("created = %q %q %q active=%v supplier=%v mfg=%v dc=%v %q %q", gotName, code, notes, active, supplier, mfg, dc, delim, pnSrc)
	}

	rec = httptest.NewRecorder()
	h.SuppliersCreate(rec, postForm("/suppliers", url.Values{"name": {name}}))
	assertStatus(t, "SuppliersCreate duplicate", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Error creating supplier") {
		t.Errorf("duplicate name: body missing %q", "Error creating supplier")
	}
}

// SupplierUpdate stores the bulk-order options, falling back to the defaults for unknown values.
func TestIntegration_SupplierSQLC_UpdateBulkOrder(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()
	id, cl := seedSupplier(t, h, ctx)
	defer cl()

	for _, c := range []struct{ delim, src, wantDelim, wantSrc string }{
		{"newline", "vendor", "newline", "vendor"},
		{"pipe", "mfg", "comma", "internal"},
	} {
		rec := httptest.NewRecorder()
		h.SupplierUpdate(rec, withID(postForm(fmt.Sprintf("/supplier/%d", id), url.Values{
			"name": {smokeUniq("SQU")}, "bulk_order_delimiter": {c.delim}, "bulk_order_pn_source": {c.src},
		}), id))
		assert302(t, "SupplierUpdate", rec)
		var delim, src string
		if err := h.queryRowContext(ctx, `SELECT bulk_order_delimiter, bulk_order_pn_source FROM company WHERE id=$1`,
			id).Scan(&delim, &src); err != nil {
			t.Fatal(err)
		}
		if delim != c.wantDelim || src != c.wantSrc {
			t.Errorf("posted %s/%s: stored %s/%s, want %s/%s", c.delim, c.src, delim, src, c.wantDelim, c.wantSrc)
		}
	}
}

// PODetail's "Copy for Ordering" uses the supplier's live bulk-order settings.
func TestIntegration_SupplierSQLC_POBulkOrderOptions(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()

	rec := httptest.NewRecorder()
	h.PODetail(rec, withIDStr(httptest.NewRequest(http.MethodGet, "/po/"+f.PO["d1"], nil), f.PO["d1"]))
	assertStatus(t, "PODetail", rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{`'copy-bulk-order-btn', 'tab')`, `data-bulk-pn="VPN-Q"`} {
		if !strings.Contains(body, want) {
			t.Errorf("PODetail missing %q", want)
		}
	}

	_, number, cl := seedThrowawayPO(t, h, context.Background()) // seed supplier 1001: column defaults
	defer cl()
	rec = httptest.NewRecorder()
	h.PODetail(rec, withIDStr(httptest.NewRequest(http.MethodGet, "/po/"+number, nil), number))
	assertStatus(t, "PODetail 1001", rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `'copy-bulk-order-btn', 'comma')`) {
		t.Errorf("PODetail for 1001: default comma delimiter missing")
	}
}

type supplierHit struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	City string `json:"city"`
}

func supplierSearch(t *testing.T, h *Handler, query string) (raw string, hits []supplierHit) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.APISupplierSearch(rec, httptest.NewRequest(http.MethodGet, "/api/suppliers/search?"+query, nil))
	assertStatus(t, "APISupplierSearch", rec, http.StatusOK)
	raw = strings.TrimSpace(rec.Body.String())
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	return raw, hits
}

// seedCompanies inserts companies (name, is_active, is_supplier) and returns their ids + cleanup.
func seedCompanies(t *testing.T, h *Handler, rows [][3]any) (ids []int, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	cleanup = func() {
		for _, id := range ids {
			smokeExec(ctx, h, `UPDATE company SET default_contact=NULL WHERE id=$1`, id)
			smokeExec(ctx, h, `DELETE FROM contact WHERE company_id=$1`, id)
			smokeExec(ctx, h, `DELETE FROM company WHERE id=$1`, id)
		}
	}
	for _, r := range rows {
		var id int
		if err := h.queryRowContext(ctx, `INSERT INTO company (name, is_active, is_supplier) VALUES ($1,$2,$3) RETURNING id`,
			r[0], r[1], r[2]).Scan(&id); err != nil {
			cleanup()
			t.Fatalf("seed company %v: %v", r[0], err)
		}
		ids = append(ids, id)
	}
	return ids, cleanup
}

// APISupplierSearch: case-insensitive name substring over active companies by name, with the default
// contact's city; supplier_only drops non-suppliers.
func TestIntegration_SupplierSQLC_Search(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	ctx := context.Background()
	base := smokeUniq("SRC")
	ids, cleanup := seedCompanies(t, h, [][3]any{
		{base + "-b", true, true}, {base + "-a", true, true}, {base + "-c", true, false}, {base + "-x", false, true},
	})
	defer cleanup()
	var con int
	if err := h.queryRowContext(ctx, `INSERT INTO contact (display_name, company_id, city) VALUES ('SRC contact',$1,'Townb') RETURNING id`,
		ids[0]).Scan(&con); err != nil {
		t.Fatal(err)
	}
	smokeExec(ctx, h, `UPDATE company SET default_contact=$2 WHERE id=$1`, ids[0], con)

	if raw, _ := supplierSearch(t, h, "q=S"); raw != "[]" {
		t.Errorf("short q body = %s, want []", raw)
	}
	a := supplierHit{ids[1], base + "-a", ""}
	b := supplierHit{ids[0], base + "-b", "Townb"}
	c := supplierHit{ids[2], base + "-c", ""}
	if _, hits := supplierSearch(t, h, "q="+base); !reflect.DeepEqual(hits, []supplierHit{a, b, c}) {
		t.Errorf("search = %+v, want %+v", hits, []supplierHit{a, b, c})
	}
	if _, hits := supplierSearch(t, h, "supplier_only=1&q="+base); !reflect.DeepEqual(hits, []supplierHit{a, b}) {
		t.Errorf("supplier_only = %+v, want %+v", hits, []supplierHit{a, b})
	}
	// Case-insensitive: the lowercased query matches the same rows.
	if _, hits := supplierSearch(t, h, "q="+strings.ToLower(base)); !reflect.DeepEqual(hits, []supplierHit{a, b, c}) {
		t.Errorf("lowercase search = %+v, want %+v", hits, []supplierHit{a, b, c})
	}
	// No match is [] (was null, which broke the typeahead's suppliers.length).
	if raw, _ := supplierSearch(t, h, "q="+base+"-zzz"); raw != "[]" {
		t.Errorf("no-match body = %s, want []", raw)
	}
}

// APISupplierSearch returns at most 20 rows, the first by name.
func TestIntegration_SupplierSQLC_SearchLimit(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	base := smokeUniq("SRL")
	var rows [][3]any
	for i := 21; i >= 1; i-- {
		rows = append(rows, [3]any{fmt.Sprintf("%s-%02d", base, i), true, true})
	}
	_, cleanup := seedCompanies(t, h, rows)
	defer cleanup()

	_, hits := supplierSearch(t, h, "q="+base)
	if len(hits) != 20 || hits[0].Name != base+"-01" || hits[19].Name != base+"-20" {
		t.Fatalf("got %d hits (%+v), want %s-01..20", len(hits), hits, base)
	}
}

type supplierContact struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zipcode string `json:"zipcode"`
	Country string `json:"country"`
	Phone   string `json:"phone"`
	Fax     string `json:"fax"`
	Email   string `json:"email"`
}

// APISupplierContacts: the company's active contacts by name, NULL fields as ""; none → [].
func TestIntegration_SupplierSQLC_Contacts(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	f, cleanup := seedSupFixture(t, h)
	defer cleanup()

	get := func(id int) (string, []supplierContact) {
		rec := httptest.NewRecorder()
		h.APISupplierContacts(rec, withID(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/suppliers/%d/contacts", id), nil), id))
		assertStatus(t, "APISupplierContacts", rec, http.StatusOK)
		var out []supplierContact
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return strings.TrimSpace(rec.Body.String()), out
	}
	_, got := get(f.ID)
	if len(got) != 3 {
		t.Fatalf("contacts = %+v, want 3", got)
	}
	want := []supplierContact{
		{got[0].ID, f.ConA, "2 Side St", "Towna", "AA", "99999", "Ruritania", "555-0201", "555-0202", "a@example.com"},
		{got[1].ID, f.ConD, "1 Main St", "Townd", "ST", "12345", "Freedonia", "555-0101", "555-0102", "d@example.com"},
		{ID: got[2].ID, Name: f.ConZ},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("contacts = %+v, want %+v", got, want)
	}

	ids, cl := seedCompanies(t, h, [][3]any{{smokeUniq("SQN"), true, true}})
	defer cl()
	// No contacts is [] (was null, which broke the PO form's contacts.forEach).
	if raw, _ := get(ids[0]); raw != "[]" {
		t.Errorf("no contacts body = %s, want []", raw)
	}
}

// % and _ in the query match themselves; * is the wildcard (#266).
func TestIntegration_SupplierSQLC_Search_LiteralAndGlob(t *testing.T) {
	h, done := liveHandler(t)
	defer done()
	base := smokeUniq("SLE")
	ids, cleanup := seedCompanies(t, h, [][3]any{
		{base + " 50%", true, true}, {base + " 505", true, true}, {base + " A_1", true, true}, {base + " AX1", true, true},
	})
	defer cleanup()

	if _, hits := supplierSearch(t, h, "q="+url.QueryEscape(base+" 50%")); len(hits) != 1 || hits[0].ID != ids[0] {
		t.Errorf("q=%%: hits = %+v, want only id %d", hits, ids[0])
	}
	if _, hits := supplierSearch(t, h, "q="+url.QueryEscape(base+" A_1")); len(hits) != 1 || hits[0].ID != ids[2] {
		t.Errorf("q=_: hits = %+v, want only id %d", hits, ids[2])
	}
	// * matches any run of characters: " A*1" finds both " A_1" and " AX1".
	if _, hits := supplierSearch(t, h, "q="+url.QueryEscape(base+" A*1")); len(hits) != 2 || hits[0].ID != ids[2] || hits[1].ID != ids[3] {
		t.Errorf("q=*: hits = %+v, want ids %d and %d", hits, ids[2], ids[3])
	}
}
