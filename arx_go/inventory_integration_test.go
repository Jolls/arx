//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"arx/internal/inventory"
)

// Characterization tests for the inventory domain (#222): gaps the older integration tests
// left, written against the pre-sqlc code. Read-only against seed rows 3005/3007/3012/3013 and
// lots 8301–8306; anything written is rolled back or removed on cleanup.

// invTx opens a tx that is rolled back at cleanup (registered after the handler's, so it
// rolls back first).
func invTx(t *testing.T, h *Handler) *txLogger {
	t.Helper()
	tx, err := h.beginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

func TestIntegration_InventoryLedgerView(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)

	rec := httptest.NewRecorder()
	h.PartTransactions(rec, withID(httptest.NewRequest(http.MethodGet, "/part/3007/transactions", nil), 3007))
	assertStatus(t, "PartTransactions", rec, http.StatusOK)
	body := rec.Body.String()

	// Seed ledger for 3007 (oldest→newest): receipt +20, issue -5, adjustment -1, count +2.
	// Shown newest first, each with the running balance as of that row.
	var last int
	for _, want := range []string{"Physical count - found 2 extra", "Cycle count correction", "Pulled for build", "Receipt against PO 5003"} {
		i := strings.Index(body, want)
		if i < 0 {
			t.Fatalf("ledger missing %q", want)
		}
		if i < last {
			t.Errorf("ledger row %q out of newest-first order", want)
		}
		last = i
	}
	for _, bal := range []string{">16.00<", ">14.00<", ">15.00<", ">20.00<"} {
		if !strings.Contains(body, bal) {
			t.Errorf("ledger missing running balance %s", bal)
		}
	}
	if !strings.Contains(body, "/part/3007/lots/8301") {
		t.Error("receipt row should link to its lot 8301")
	}
}

func TestIntegration_InventoryCreateLot(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	tx := invTx(t, h)

	type lotState struct {
		Part         int
		Number, Desc string
		Vendor       sql.NullString
		POLine       sql.NullInt64
		Source       sql.NullString
		Active       bool
		Notes        sql.NullString
		created      time.Time
	}
	read := func(id int) lotState {
		var s lotState
		if err := tx.QueryRowContext(ctx,
			`SELECT part_id, lot_number, lot_description, vendor_lot_number, po_line_id, source, is_active, notes, created_at FROM lot WHERE id=$1`,
			id).Scan(&s.Part, &s.Number, &s.Desc, &s.Vendor, &s.POLine, &s.Source, &s.Active, &s.Notes, &s.created); err != nil {
			t.Fatal(err)
		}
		return s
	}

	id, err := h.createLot(ctx, tx, 3007, lotCreateArgs{LotNumber: "ITEST-222", VendorLot: "V-9", Description: "PO ITEST"}, &[]int{5504}[0])
	if err != nil {
		t.Fatal(err)
	}
	s := read(id)
	if s.Part != 3007 || s.Number != "ITEST-222" || s.Desc != "PO ITEST" || s.Vendor.String != "V-9" || s.POLine.Int64 != 5504 || !s.Active || s.Source.Valid || s.Notes.Valid {
		t.Errorf("explicit lot = %+v", s)
	}

	auto, err := h.createLot(ctx, tx, 3012, lotCreateArgs{Description: "Build #1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s = read(auto)
	if s.Number != fmt.Sprint(auto) || s.Vendor.Valid || s.POLine.Valid || s.Desc != "Build #1" {
		t.Errorf("auto lot = %+v, want lot_number %d, NULL vendor/po_line", s, auto)
	}
}

func TestIntegration_InventoryRecordTxn(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	tx := invTx(t, h)
	req := userCtxTZ(httptest.NewRequest(http.MethodPost, "/", nil), "America/Los_Angeles")

	stock := func() float64 {
		var v float64
		if err := tx.QueryRowContext(ctx, `SELECT stock_on_hand FROM part WHERE id=3002`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := stock()
	type row struct {
		Type, User     string
		Qty            float64
		Date           string
		Ref, Note      sql.NullString
		PO, Lot, Build sql.NullInt64
	}
	last := func() (r row) {
		if err := tx.QueryRowContext(ctx,
			`SELECT txn_type, username, qty::float8, txn_date::text, reference, note, po_line_id, lot_id, build_id
			 FROM inventory_transaction WHERE part_id=3002 ORDER BY id DESC LIMIT 1`).
			Scan(&r.Type, &r.User, &r.Qty, &r.Date, &r.Ref, &r.Note, &r.PO, &r.Lot, &r.Build); err != nil {
			t.Fatal(err)
		}
		return
	}

	day := time.Date(2026, 3, 4, 0, 0, 0, 0, time.Local)
	if err := h.recordInventoryTxn(req, tx, 3002, "adjustment", -2.5, day, "", "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	r := last()
	if r.Type != "adjustment" || r.User != h.actorName(req) || r.Qty != -2.5 || r.Date != "2026-03-04" || r.Ref.Valid || r.Note.Valid || r.PO.Valid || r.Lot.Valid || r.Build.Valid {
		t.Errorf("blank-optionals row = %+v", r)
	}
	if got := stock(); got != before-2.5 {
		t.Errorf("stock_on_hand = %v, want %v", got, before-2.5)
	}

	po, lot, build := 5504, 8301, 8201
	if err := h.recordInventoryTxn(req, tx, 3002, "receipt", 4, day, "REF", "a note", &po, &lot, &build); err != nil {
		t.Fatal(err)
	}
	r = last()
	if r.Ref.String != "REF" || r.Note.String != "a note" || r.PO.Int64 != 5504 || r.Lot.Int64 != 8301 || r.Build.Int64 != 8201 {
		t.Errorf("full row = %+v", r)
	}
	if got := stock(); got != before+1.5 {
		t.Errorf("stock_on_hand = %v, want %v", got, before+1.5)
	}
}

func TestIntegration_InventoryAppendLotNote(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	tx := invTx(t, h)

	id, err := h.createLot(ctx, tx, 3007, lotCreateArgs{Description: "itest-222 note"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	notes := func() sql.NullString {
		var n sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT notes FROM lot WHERE id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The raw CONCAT(notes, $2) left $2 untyped, which Postgres rejected, so no lot note could
	// ever be appended before this conversion (#222).
	today := userToday(h)
	if err := h.appendLotNote(ctx, tx, id, "  first  ", "bob"); err != nil {
		t.Fatal(err)
	}
	first := fmt.Sprintf("[bob %s] first", today)
	if got := notes(); got.String != first {
		t.Errorf("after first append notes = %q, want %q", got.String, first)
	}
	if err := h.appendLotNote(ctx, tx, id, "second", "amy"); err != nil {
		t.Fatal(err)
	}
	if got, want := notes().String, first+"\n\n"+fmt.Sprintf("[amy %s] second", today); got != want {
		t.Errorf("after second append notes = %q, want %q", got, want)
	}
}

func TestIntegration_InventoryDescendantTrace(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	type node struct {
		Type   string
		ID     int
		Number string
		Depth  int
		Qty    float64
		Vendor bool
	}
	flat := func(ns []TraceNode) (out []node) {
		for _, n := range ns {
			out = append(out, node{n.NodeType, n.ID, n.Number, n.Depth, n.Qty, n.IsVendorLot})
		}
		return
	}
	// Seed edges: lot 8301 → lot 8302 (1) → lot 8306 (1); lot 8301 → unit 8501 (2).
	got, err := h.genealogyTrace(ctx, 8301, "lot", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []node{{"lot", 8302, "8302", 0, 1, false}, {"lot", 8306, "8306", 1, 1, false}, {"unit", 8501, "SN-3013-001", 0, 2, false}}
	if fmt.Sprint(flat(got)) != fmt.Sprint(want) {
		t.Errorf("descendants of lot 8301:\n got %v\nwant %v", flat(got), want)
	}
	// Unit 8503 (unit endpoint) feeds unit 8501.
	got, err = h.genealogyTrace(ctx, 8503, "unit", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []node{{"unit", 8501, "SN-3013-001", 0, 1, false}}; fmt.Sprint(flat(got)) != fmt.Sprint(want) {
		t.Errorf("descendants of unit 8503: got %v want %v", flat(got), want)
	}
	// Ancestors of unit 8501 walked together with its lot 8306: parent unit 8503, raw lot 8301
	// (a vendor lot, leaf), and lot 8306's own chain, each node expanded once.
	got, err = h.genealogyTraceRoots(ctx, []traceRoot{{8501, "unit"}, {8306, "lot"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	want = []node{
		{"lot", 8301, "8301", 0, 2, true}, {"unit", 8503, "SN-3005-001", 0, 1, false},
		{"lot", 8302, "8302", 0, 1, false}, {"lot", 8301, "8301", 1, 1, true},
		{"lot", 8303, "8303", 0, 1, false},
	}
	if fmt.Sprint(flat(got)) != fmt.Sprint(want) {
		t.Errorf("multi-root ancestors:\n got %v\nwant %v", flat(got), want)
	}
	// Nothing feeds a raw vendor lot.
	if got, err := h.genealogyTrace(ctx, 8301, "lot", true); err != nil || len(got) != 0 {
		t.Errorf("ancestors of vendor lot 8301 = %v, %v; want none", got, err)
	}
}

func TestIntegration_InventoryPartCards(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	lots, err := h.recentPartLots(ctx, 3007, 1)
	if err != nil || len(lots) != 1 || lots[0].ID != 8303 || lots[0].PartNumber == "" || lots[0].LotDescription != "Cycle count - unlabeled found lot" || lots[0].VendorLot != "" {
		t.Errorf("recentPartLots(3007,1) = %+v, %v; want just lot 8303 with part fields", lots, err)
	}
	if n, err := h.lotCountForPart(ctx, 3007); err != nil || n != 2 {
		t.Errorf("lotCountForPart(3007) = %d, %v; want 2", n, err)
	}
	units, err := h.recentPartUnits(ctx, 3005, 5)
	if err != nil || len(units) != 2 || units[0].ID != 8504 || units[1].ID != 8503 {
		t.Fatalf("recentPartUnits(3005,5) = %+v, %v; want [8504 8503]", units, err)
	}
	if u := units[0]; !u.IsManual() || u.LotID != nil || u.BuildID != nil || u.LotNumber != "" || u.SerialNumber != "SN-3005-MANUAL-1" {
		t.Errorf("manual unit 8504 = %+v", u)
	}
	if u := units[1]; u.IsManual() || u.LotID != nil || u.BuildID == nil || *u.BuildID != 8201 {
		t.Errorf("test unit 8503 = %+v", u)
	}
	if n, err := h.unitCountForPart(ctx, 3005); err != nil || n != 2 {
		t.Errorf("unitCountForPart(3005) = %d, %v; want 2", n, err)
	}
	u, found, err := h.fetchUnitRow(ctx, 8501)
	if err != nil || !found || u.LotID == nil || *u.LotID != 8306 || u.LotNumber != "8306" || u.PartNumber == "" {
		t.Errorf("fetchUnitRow(8501) = %+v, %v, %v; want lot 8306 joined", u, found, err)
	}
	if _, found, err := h.fetchUnitRow(ctx, 999999999); err != nil || found {
		t.Errorf("fetchUnitRow(missing) found=%v err=%v", found, err)
	}
}

func TestIntegration_InventoryBuildOptions(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	builds, err := h.activeBuildsForPart(ctx, 3005)
	if err != nil {
		t.Fatal(err)
	}
	var label string
	for _, b := range builds {
		if b.ID == 8201 {
			label = b.Label
		}
	}
	if label != "Build #8201 — qty 1 — 2026-05-25" {
		t.Errorf("activeBuildsForPart(3005) label for 8201 = %q", label)
	}
	b, err := h.fetchBuildOption(ctx, 8202)
	if err != nil || b == nil || b.Label != "Build #8202 — qty 1 — 2026-05-25" {
		t.Errorf("fetchBuildOption(8202) = %+v, %v", b, err)
	}
	if b, err := h.fetchBuildOption(ctx, 999999999); err != nil || b != nil {
		t.Errorf("fetchBuildOption(missing) = %+v, %v; want nil, nil", b, err)
	}
}

func TestIntegration_InventoryUnitCreateUpdate(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	// Manual unit linked to build 8201 (a build of part 3005), no lot.
	serial := smokeUniq("SN-222")
	rec := httptest.NewRecorder()
	h.UnitCreate(rec, withID(postForm("/part/3005/units", url.Values{"serial_number": {serial}, "build_id": {"8201"}}), 3005))
	assertStatus(t, "UnitCreate(build link)", rec, http.StatusSeeOther)
	var unitID int
	var lot, build sql.NullInt64
	var source string
	var active bool
	if err := h.queryRowContext(ctx, `SELECT id, lot_id, build_id, source, is_active FROM unit WHERE part_id=3005 AND serial_number=$1`, serial).
		Scan(&unitID, &lot, &build, &source, &active); err != nil {
		t.Fatalf("unit not created: %v", err)
	}
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM unit WHERE id=$1`, unitID) })
	if lot.Valid || build.Int64 != 8201 || source != "manual" || !active {
		t.Errorf("unit = lot %v build %v source %q active %v", lot, build, source, active)
	}
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/part/3005/units/%d", unitID) {
		t.Errorf("redirect = %q", loc)
	}

	// A lot of a different part is rejected before anything is written.
	rec = httptest.NewRecorder()
	h.UnitCreate(rec, withID(postForm("/part/3005/units", url.Values{"serial_number": {serial + "-x"}, "lot_id": {"8301"}}), 3005))
	assertStatus(t, "UnitCreate(foreign lot)", rec, http.StatusBadRequest)
	// Blank serial.
	rec = httptest.NewRecorder()
	h.UnitCreate(rec, withID(postForm("/part/3005/units", url.Values{"serial_number": {""}}), 3005))
	if !strings.Contains(rec.Body.String(), "Serial # is required.") {
		t.Errorf("blank serial: %s", rec.Body.String())
	}

	state := func() (string, bool) {
		var s string
		var a bool
		if err := h.queryRowContext(ctx, `SELECT serial_number, is_active FROM unit WHERE id=$1`, unitID).Scan(&s, &a); err != nil {
			t.Fatal(err)
		}
		return s, a
	}
	update := func(part int, vals url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.UnitUpdate(rec, withIDAndUnitID(postForm(fmt.Sprintf("/part/%d/units/%d", part, unitID), vals), part, unitID))
		return rec
	}

	// Unlocked: serial and scrap toggle both save (is_active absent = scrapped).
	assertStatus(t, "UnitUpdate(unlocked)", update(3005, url.Values{"serial_number": {serial + "-r"}}), http.StatusSeeOther)
	if s, a := state(); s != serial+"-r" || a {
		t.Errorf("after unlocked update: serial %q active %v", s, a)
	}
	// Blank serial refused while unlocked.
	if body := update(3005, url.Values{"serial_number": {""}, "is_active": {"1"}}).Body.String(); !strings.Contains(body, "Serial # is required.") {
		t.Errorf("unlocked blank serial: %s", body)
	}
	// Wrong part.
	if body := update(3013, url.Values{"serial_number": {"nope"}}).Body.String(); !strings.Contains(body, "Unit not found for this part") {
		t.Errorf("wrong-part update: %s", body)
	}
	// Duplicate serial → friendly message.
	if body := update(3005, url.Values{"serial_number": {"SN-3005-001"}}).Body.String(); !strings.Contains(body, "A unit with this serial already exists for this part.") {
		t.Errorf("duplicate serial: %s", body)
	}

	// Locked: a locked record now points at the unit, so the serial is frozen but scrap still saves.
	var recID int
	if err := h.queryRowContext(ctx,
		`INSERT INTO form_record (form_id, part_id, serial_number, subject_part_number, subject_pn_description, record_type, test_order, is_locked, is_active, unit_id) VALUES (6001, 3005, $1, 'x', 'x', '', '', TRUE, TRUE, $2) RETURNING id`,
		serial, unitID).Scan(&recID); err != nil {
		t.Fatalf("seed locked record (ArxDev may need reseed): %v", err)
	}
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM form_record WHERE id=$1`, recID) })
	assertStatus(t, "UnitUpdate(locked)", update(3005, url.Values{"serial_number": {"changed"}, "is_active": {"1"}}), http.StatusSeeOther)
	if s, a := state(); s != serial+"-r" || !a {
		t.Errorf("after locked update: serial %q active %v; want serial unchanged, active", s, a)
	}
}

func TestIntegration_InventoryLotUpdateWrongPart(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)

	rec := httptest.NewRecorder()
	h.LotUpdate(rec, withIDAndLotID(postForm("/part/3012/lots/8301", url.Values{"lot_description": {"hijack"}}), 3012, 8301))
	if !strings.Contains(rec.Body.String(), "Lot not found for this part") {
		t.Errorf("LotUpdate wrong part: %s", rec.Body.String())
	}
	var desc string
	if err := h.queryRowContext(context.Background(), `SELECT lot_description FROM lot WHERE id=8301`).Scan(&desc); err != nil || desc != "PO 5003" {
		t.Errorf("lot 8301 description = %q, %v; want unchanged", desc, err)
	}
}

func TestIntegration_InventoryRecordLinkage(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	var retired int
	if err := h.queryRowContext(ctx,
		`INSERT INTO lot (part_id, lot_number, lot_description, is_active) VALUES (3007, 'ITEST-RETIRED', 'itest-222', FALSE) RETURNING id`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM lot WHERE id=$1`, retired) })

	args := func(part int, vals url.Values) (*int, *int, error) {
		r := postForm("/x", vals)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		return h.recordLinkageArgs(r, part)
	}
	// A retired lot of the part is still accepted; blank fields stay nil.
	lot, build, err := args(3007, url.Values{"lot_id": {fmt.Sprint(retired)}})
	if err != nil || lot == nil || *lot != retired || build != nil {
		t.Errorf("retired lot: got %v, %v, %v", lot, build, err)
	}
	lot, build, err = args(3005, url.Values{"build_id": {"8201"}})
	if err != nil || lot != nil || build == nil || *build != 8201 {
		t.Errorf("build: got %v, %v, %v", lot, build, err)
	}
	if _, _, err := args(3005, url.Values{"lot_id": {"8301"}}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("foreign lot: err = %v", err)
	}
	if _, _, err := args(3005, url.Values{"build_id": {"8202"}}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("foreign build: err = %v", err)
	}
	if _, _, err := args(3005, url.Values{"lot_id": {"abc"}}); err == nil || !strings.Contains(err.Error(), "invalid selection") {
		t.Errorf("non-numeric lot: err = %v", err)
	}
}

func TestIntegration_InventoryUpsertUnitForRecord(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	tx := invTx(t, h)

	serial := smokeUniq("SN-222U")
	build8201 := 8201
	first, err := inventory.New(tx).UpsertTestUnit(ctx, 3005, serial, &build8201, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lot, build sql.NullInt64
	var source string
	if err := tx.QueryRowContext(ctx, `SELECT lot_id, build_id, source FROM unit WHERE id=$1`, first).Scan(&lot, &build, &source); err != nil {
		t.Fatal(err)
	}
	if lot.Valid || build.Int64 != 8201 || source != "test" {
		t.Errorf("new unit = lot %v build %v source %q", lot, build, source)
	}
	// A retest with different provenance reuses the unit and leaves its provenance alone.
	again, err := inventory.New(tx).UpsertTestUnit(ctx, 3005, serial, nil, nil)
	if err != nil || again != first {
		t.Errorf("second upsert = %d, %v; want %d", again, err, first)
	}
}

// An existing unit is re-linked with its provenance untouched (#263).
func TestIntegration_InventoryUpsertTestUnit_ExistingSeedSerial(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	tx := invTx(t, h)

	row := func() (build, lot sql.NullInt64, source string) {
		t.Helper()
		if err := tx.QueryRowContext(ctx, `SELECT build_id, lot_id, source FROM unit WHERE id=8503`).Scan(&build, &lot, &source); err != nil {
			t.Fatal(err)
		}
		return
	}
	b0, l0, s0 := row()
	id, err := inventory.New(tx).UpsertTestUnit(ctx, 3005, "SN-3005-001", nil, nil)
	if err != nil || id != 8503 {
		t.Fatalf("upsert seed serial = %d, %v; want 8503", id, err)
	}
	if b1, l1, s1 := row(); b1 != b0 || l1 != l0 || s1 != s0 {
		t.Errorf("seed unit changed: build %v->%v lot %v->%v source %q->%q", b0, b1, l0, l1, s0, s1)
	}
}

// The same serial on two parts is two units (#263).
func TestIntegration_InventoryUpsertTestUnit_SamePartSerialDifferentPart(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	tx := invTx(t, h)

	serial := smokeUniq("SN-263P")
	b1, b2 := 8201, 8202
	a, err := inventory.New(tx).UpsertTestUnit(ctx, 3005, serial, &b1, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := inventory.New(tx).UpsertTestUnit(ctx, 3012, serial, &b2, nil)
	if err != nil || a == b {
		t.Errorf("units = %d, %d (err %v); want two different ids", a, b, err)
	}
}

// Two transactions saving the same new serial at once: the second links to the unit the first
// created instead of failing on uq_unit_serial (#263). Returns both ids and the second's error.
func concurrentUpsertSameSerial(t *testing.T, h *Handler, serial string, build1, build2 *int) (id1, id2 int, err2 error) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM unit WHERE part_id=3005 AND serial_number=$1`, serial) })

	tx1, err := h.beginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback()
	tx2, err := h.beginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback()

	if id1, err = inventory.New(tx1).UpsertTestUnit(ctx, 3005, serial, build1, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		id2, err2 = inventory.New(tx2).UpsertTestUnit(ctx, 3005, serial, build2, nil)
	}()
	time.Sleep(300 * time.Millisecond) // let tx2 run past any existence check and block on tx1's row
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}
	<-done
	if err2 == nil {
		if err := tx2.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return id1, id2, err2
}

func TestIntegration_InventoryUpsertTestUnit_Concurrent(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	serial := smokeUniq("SN-263C")
	b := 8201

	id1, id2, err2 := concurrentUpsertSameSerial(t, h, serial, &b, &b)
	if err2 != nil || id2 != id1 {
		t.Fatalf("second save = %d, %v; want %d, nil", id2, err2, id1)
	}
	var n int
	if err := h.queryRowContext(context.Background(), `SELECT count(*) FROM unit WHERE part_id=3005 AND serial_number=$1`, serial).Scan(&n); err != nil || n != 1 {
		t.Errorf("units for serial = %d, %v; want 1", n, err)
	}
}

func TestIntegration_InventoryUpsertTestUnit_ConcurrentProvenanceKept(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	serial := smokeUniq("SN-263K")
	b := 8201

	id1, id2, err2 := concurrentUpsertSameSerial(t, h, serial, &b, nil)
	if err2 != nil || id2 != id1 {
		t.Fatalf("second save = %d, %v; want %d, nil", id2, err2, id1)
	}
	var build sql.NullInt64
	var source string
	if err := h.queryRowContext(context.Background(), `SELECT build_id, source FROM unit WHERE id=$1`, id1).Scan(&build, &source); err != nil {
		t.Fatal(err)
	}
	if build.Int64 != 8201 || source != "test" {
		t.Errorf("unit build %v source %q; want 8201, test", build, source)
	}
}
