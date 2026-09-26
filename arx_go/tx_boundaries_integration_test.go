//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests for #191: each business operation writes inside one transaction, and its
// lock/state check is a conditional write (or row lock) inside that transaction.
// Race tests hold a competing change open in lockTx, start the handler, wait until
// it blocks on the row lock, then commit lockTx.

func requirePostgres(t *testing.T, h *Handler) {
	t.Helper()
	if h.dia().Name() != "postgres" {
		t.Skip("postgres-only")
	}
}

// waitForLockWaiter waits until some session in this database is blocked on a lock.
func waitForLockWaiter(t *testing.T, h *Handler, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := h.queryRowContext(ctx,
			`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("handler never blocked on the competing lock")
}

// raceHandler runs fn while lockTx holds a competing uncommitted change, commits
// lockTx once fn is blocked on it, and waits for fn to finish.
func raceHandler(t *testing.T, h *Handler, ctx context.Context, lockSQL []string, args [][]any, fn func()) {
	t.Helper()
	lockTx, err := h.beginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback()
	for i, q := range lockSQL {
		if _, err := lockTx.ExecContext(ctx, q, args[i]...); err != nil {
			t.Fatalf("lockTx %q: %v", q, err)
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	waitForLockWaiter(t, h, ctx)
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not finish after the competing tx committed")
	}
}

// seedSRRecord creates a throwaway part, form, one step and a WIP record with one
// result "5.00". The record has no part_id, so it has no BOM and owns no lots.
func seedSRRecord(t *testing.T, h *Handler, ctx context.Context) (recordID, testID int, cleanup func()) {
	t.Helper()
	d := h.dia()
	var partID, formID int
	if err := h.queryRowContext(ctx, d.InsertReturningID(h.cfg().PartsTable(),
		"part_number, revision, description, release_status, is_active",
		"@p1, 'A', 'tx boundary test', 'U', "+d.BoolLiteral(true), false), smokeUniq("ITEST-TX")).Scan(&partID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	if err := h.queryRowContext(ctx, d.InsertReturningID(h.cfg().FormsTable(),
		"part_number_id, test_order, is_locked, is_active",
		"@p1, '', "+d.BoolLiteral(false)+", "+d.BoolLiteral(true), false), partID).Scan(&formID); err != nil {
		t.Fatalf("seed form: %v", err)
	}
	if err := h.queryRowContext(ctx, d.InsertReturningID(h.cfg().StepsTable(),
		"form_id, type, parameter", "@p1, 0, 'Output Voltage'", false), formID).Scan(&testID); err != nil {
		t.Fatalf("seed form_row: %v", err)
	}
	if _, err := h.execContext(ctx, fmt.Sprintf(`UPDATE %s SET test_order=@p1 WHERE id=@p2`, h.cfg().FormsTable()),
		strconv.Itoa(testID), formID); err != nil {
		t.Fatal(err)
	}
	if err := h.queryRowContext(ctx, d.InsertReturningID(h.cfg().RecordsTable(),
		"form_id, record_date, serial_number, subject_part_number, subject_pn_description, test_order, record_type, is_locked, is_approved, is_active",
		"@p1, '2026-07-01', @p2, '', '', @p3, 'New Release', "+d.BoolLiteral(false)+", "+d.BoolLiteral(false)+", "+d.BoolLiteral(true), false),
		formID, smokeUniq("TX"), strconv.Itoa(testID)).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	if _, err := h.execContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (form_record_id, form_row_id, result, parameter) VALUES (@p1, @p2, '5.00', 'Output Voltage')`,
		h.cfg().ResultsTable()), recordID, testID); err != nil {
		t.Fatalf("seed result: %v", err)
	}
	return recordID, testID, func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE form_record_id=@p1`, h.cfg().ResultsTable()), recordID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().RecordsTable()), recordID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().StepsTable()), testID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().FormsTable()), formID)
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().PartsTable()), partID)
	}
}

func resultOf(t *testing.T, h *Handler, ctx context.Context, recordID, testID int) string {
	t.Helper()
	var v string
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COALESCE(result,'') FROM %s WHERE form_record_id=@p1 AND form_row_id=@p2`, h.cfg().ResultsTable()),
		recordID, testID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func setRecordLocked(t *testing.T, h *Handler, ctx context.Context, recordID int) {
	t.Helper()
	if _, err := h.execContext(ctx, fmt.Sprintf(`UPDATE %s SET is_locked=%s WHERE id=@p1`,
		h.cfg().RecordsTable(), h.dia().BoolLiteral(true)), recordID); err != nil {
		t.Fatal(err)
	}
}

func saveResults(h *Handler, recordID int, vals url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.SaveResults(rec, withID(postForm(fmt.Sprintf("/records/%d/save", recordID), vals), recordID))
	return rec
}

// ── SaveResults ─────────────────────────────────────────────────────────────

func TestIntegration_SaveResults_HappyPathUpdatesResultAndRecord(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	recordID, testID, cl := seedSRRecord(t, h, ctx)
	defer cl()

	rec := saveResults(h, recordID, url.Values{fmt.Sprintf("result_%d", testID): {"9.99"}, "record_type": {"Changed"}})
	assertStatus(t, "SaveResults", rec, http.StatusSeeOther)
	if got := resultOf(t, h, ctx, recordID, testID); got != "9.99" {
		t.Errorf("result = %q, want 9.99", got)
	}
	var rt string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT record_type FROM %s WHERE id=@p1`, h.cfg().RecordsTable()), recordID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "Changed" {
		t.Errorf("record_type = %q, want Changed", rt)
	}
}

func TestIntegration_SaveResults_LockedRecordWritesNothing(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	recordID, testID, cl := seedSRRecord(t, h, ctx)
	defer cl()
	setRecordLocked(t, h, ctx, recordID)

	rec := saveResults(h, recordID, url.Values{fmt.Sprintf("result_%d", testID): {"9.99"}, "record_type": {"Changed"}})
	assertStatus(t, "SaveResults", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/records/%d", recordID) {
		t.Errorf("Location = %q, want /records/%d", loc, recordID)
	}
	if got := resultOf(t, h, ctx, recordID, testID); got != "5.00" {
		t.Errorf("result = %q, want unchanged 5.00", got)
	}
	var rt string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT record_type FROM %s WHERE id=@p1`, h.cfg().RecordsTable()), recordID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "New Release" {
		t.Errorf("record_type = %q, want unchanged New Release", rt)
	}
}

func TestIntegration_SaveResults_NotFound(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	assertStatus(t, "SaveResults", saveResults(h, -1, url.Values{}), http.StatusNotFound)
}

// A failure after the result writes must roll them back (#191): today the
// result rows are written before the transaction begins.
func TestIntegration_SaveResults_FailureAfterResultWritesRollsBack(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()

	for name, extra := range map[string]url.Values{
		"foreign lot":     {"lot_id": {"8301"}},                         // 8301 belongs to another part
		"build, no BOM":   {"build_panel": {"1"}, "qty": {"1"}},         // the record has no part/BOM
	} {
		t.Run(name, func(t *testing.T) {
			recordID, testID, cl := seedSRRecord(t, h, ctx)
			defer cl()
			vals := url.Values{fmt.Sprintf("result_%d", testID): {"9.99"}}
			for k, v := range extra {
				vals[k] = v
			}
			assertStatus(t, "SaveResults", saveResults(h, recordID, vals), http.StatusBadRequest)
			if got := resultOf(t, h, ctx, recordID, testID); got != "5.00" {
				t.Errorf("result = %q, want 5.00 (the failed save must not persist results)", got)
			}
		})
	}
}

// A lock that lands while the save is running wins: nothing is written.
func TestIntegration_SaveResults_LockRaceWritesNothing(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	requirePostgres(t, h)
	ctx := context.Background()
	recordID, testID, cl := seedSRRecord(t, h, ctx)
	defer cl()

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{fmt.Sprintf(`UPDATE %s SET is_locked=TRUE WHERE id=$1`, h.cfg().RecordsTable())},
		[][]any{{recordID}},
		func() { rec = saveResults(h, recordID, url.Values{fmt.Sprintf("result_%d", testID): {"9.99"}}) })

	assertStatus(t, "SaveResults", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/records/%d", recordID) {
		t.Errorf("Location = %q, want /records/%d", loc, recordID)
	}
	if got := resultOf(t, h, ctx, recordID, testID); got != "5.00" {
		t.Errorf("result = %q, want 5.00 (record was locked mid-save)", got)
	}
}

// ── PartBuildCreate return_record ───────────────────────────────────────────

const (
	brOutputPart  = 3012 // ASM-1002, lot-tracked + has a BOM in seed
	brTrackedComp = 3007 // lot-tracked component; seed lot 8301
	brCompLot     = 8301
)

// seedBuildReturnRecord creates a WIP (or locked) record on the buildable part and
// returns a cleanup that also undoes any build the test ran.
func seedBuildReturnRecord(t *testing.T, h *Handler, ctx context.Context, locked bool) (int, func()) {
	t.Helper()
	var recordID int
	if err := h.queryRowContext(ctx, h.dia().InsertReturningID(h.cfg().RecordsTable(),
		"form_id, part_id, serial_number, is_locked, is_active",
		"6001, @p1, @p2, "+h.dia().BoolLiteral(locked)+", "+h.dia().BoolLiteral(true), false),
		brOutputPart, smokeUniq("BRR")).Scan(&recordID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	var before int
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(id),0) FROM %s`, h.cfg().BuildTable())).Scan(&before); err != nil {
		t.Fatal(err)
	}
	return recordID, func() {
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().RecordsTable()), recordID)
		var buildID, lotID int
		_ = h.queryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(id),0) FROM %s WHERE part_id=@p1 AND id>@p2`,
			h.cfg().BuildTable()), brOutputPart, before).Scan(&buildID)
		if buildID == 0 {
			return
		}
		_ = h.queryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(output_lot_id,0) FROM %s WHERE id=@p1`, h.cfg().BuildTable()), buildID).Scan(&lotID)
		if lotID != 0 {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE child_lot_id=@p1`, h.cfg().GenealogyTable()), lotID)
		}
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE build_id=@p1`, h.cfg().InventoryTxnTable()), buildID)
		smokeExec(ctx, h, fmt.Sprintf(`UPDATE %s SET output_lot_id=NULL WHERE id=@p1`, h.cfg().BuildTable()), buildID)
		if lotID != 0 {
			smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().LotTable()), lotID)
		}
		smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE id=@p1`, h.cfg().BuildTable()), buildID)
		for _, id := range []int{3001, brTrackedComp, 3002, brOutputPart} {
			smokeExec(ctx, h, fmt.Sprintf(
				`UPDATE %s SET stock_on_hand = (SELECT COALESCE(SUM(qty),0) FROM %s WHERE part_id = @p1) WHERE id = @p1`,
				h.cfg().PartsTable(), h.cfg().InventoryTxnTable()), id)
		}
	}
}

func buildWithReturn(h *Handler, recordID int) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.PartBuildCreate(rec, withID(postForm(fmt.Sprintf("/part/%d/build", brOutputPart), url.Values{
		"qty":                                 {"1"},
		"return_record":                       {strconv.Itoa(recordID)},
		fmt.Sprintf("lot[%d]", brTrackedComp): {strconv.Itoa(brCompLot)},
	}), brOutputPart))
	return rec
}

func recordBuildLinked(t *testing.T, h *Handler, ctx context.Context, recordID int) bool {
	t.Helper()
	var linked bool
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT build_id IS NOT NULL FROM %s WHERE id=@p1`, h.cfg().RecordsTable()), recordID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	return linked
}

func TestIntegration_BuildReturnRecord_LockedRecordNotLinked(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	recordID, cl := seedBuildReturnRecord(t, h, ctx, true)
	defer cl()

	rec := buildWithReturn(h, recordID)
	assert302(t, "PartBuildCreate", rec)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/part/%d/build?built=1", brOutputPart) {
		t.Errorf("Location = %q, want /part/%d/build?built=1", loc, brOutputPart)
	}
	if recordBuildLinked(t, h, ctx, recordID) {
		t.Error("locked record was linked to the build")
	}
}

func TestIntegration_BuildReturnRecord_LockRaceNotLinked(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	requirePostgres(t, h)
	ctx := context.Background()
	recordID, cl := seedBuildReturnRecord(t, h, ctx, false)
	defer cl()

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{fmt.Sprintf(`UPDATE %s SET is_locked=TRUE WHERE id=$1`, h.cfg().RecordsTable())},
		[][]any{{recordID}},
		func() { rec = buildWithReturn(h, recordID) })

	if recordBuildLinked(t, h, ctx, recordID) {
		t.Error("record locked mid-build was linked to the build")
	}
	if loc := rec.Header().Get("Location"); loc == fmt.Sprintf("/records/%d/edit?built=1", recordID) {
		t.Errorf("Location = %q, want the part build page (not linked)", loc)
	}
}

// ── POReceive ───────────────────────────────────────────────────────────────

func poStatus(t *testing.T, h *Handler, ctx context.Context, poID int) string {
	t.Helper()
	var s string
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT status FROM %s WHERE ID=@p1`, h.cfg().POTable()), poID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func poReceive(h *Handler, number string, vals url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.POReceive(rec, withIDStr(postForm("/po/{id}/receive", vals), number))
	return rec
}

// A cancel that lands while receiving is running wins: nothing is received.
func TestIntegration_POReceive_StatusRaceRejected(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	requirePostgres(t, h)
	ctx := context.Background()
	poID, number, cl := seedThrowawayPO(t, h, ctx)
	defer cl()
	lineID := seedPOLine(t, h, ctx, poID, 3001, "RAW-1001", 10, 2.50)
	setPOStatus(t, h, ctx, poID, "sent", "approved")
	defer smokeExec(ctx, h, fmt.Sprintf(
		`UPDATE %s SET stock_on_hand = (SELECT COALESCE(SUM(qty),0) FROM %s WHERE part_id = @p1) WHERE id = @p1`,
		h.cfg().PartsTable(), h.cfg().InventoryTxnTable()), 3001)
	defer smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE po_line_id=@p1`, h.cfg().InventoryTxnTable()), lineID)

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{fmt.Sprintf(`UPDATE %s SET status='cancelled' WHERE ID=$1`, h.cfg().POTable())},
		[][]any{{poID}},
		func() { rec = poReceive(h, number, url.Values{fmt.Sprintf("recv[%d]", lineID): {"4"}}) })

	if !strings.Contains(rec.Body.String(), "Only a sent or partially-received PO") {
		t.Errorf("body missing the wrong-status error")
	}
	if s := poStatus(t, h, ctx, poID); s != "cancelled" {
		t.Errorf("PO status = %q, want cancelled", s)
	}
	var received float64
	var txns int
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT received_qty FROM %s WHERE id=@p1`, h.cfg().POLineTable()), lineID).Scan(&received); err != nil {
		t.Fatal(err)
	}
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE po_line_id=@p1`, h.cfg().InventoryTxnTable()), lineID).Scan(&txns); err != nil {
		t.Fatal(err)
	}
	if received != 0 || txns != 0 {
		t.Errorf("received_qty=%v inventory rows=%d, want 0/0 on a cancelled PO", received, txns)
	}
}

// The PO's new status is derived from the committed line receipts, not the
// handler's stale pre-transaction copy.
func TestIntegration_POReceive_StatusDerivedFromCommittedLines(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	requirePostgres(t, h)
	ctx := context.Background()
	poID, number, cl := seedThrowawayPO(t, h, ctx)
	defer cl()
	line1 := seedPOLine(t, h, ctx, poID, 3001, "RAW-1001", 10, 2.50)
	line2 := seedPOLine(t, h, ctx, poID, 3001, "RAW-1001", 10, 2.50)
	setPOStatus(t, h, ctx, poID, "sent", "approved")
	defer smokeExec(ctx, h, fmt.Sprintf(
		`UPDATE %s SET stock_on_hand = (SELECT COALESCE(SUM(qty),0) FROM %s WHERE part_id = @p1) WHERE id = @p1`,
		h.cfg().PartsTable(), h.cfg().InventoryTxnTable()), 3001)
	defer smokeExec(ctx, h, fmt.Sprintf(`DELETE FROM %s WHERE po_line_id IN (@p1, @p2)`, h.cfg().InventoryTxnTable()), line1, line2)

	raceHandler(t, h, ctx,
		[]string{
			fmt.Sprintf(`SELECT ID FROM %s WHERE ID=$1 FOR UPDATE`, h.cfg().POTable()),
			fmt.Sprintf(`UPDATE %s SET received_qty=10 WHERE id=$1`, h.cfg().POLineTable()),
		},
		[][]any{{poID}, {line2}},
		func() { poReceive(h, number, url.Values{fmt.Sprintf("recv[%d]", line1): {"10"}}) })

	if s := poStatus(t, h, ctx, poID); s != "closed" {
		t.Errorf("PO status = %q, want closed (both lines fully received)", s)
	}
}

// ── RFQConvert ──────────────────────────────────────────────────────────────

func rfqConvert(h *Handler, number string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.RFQConvert(rec, withIDStr(postForm("/rfq/{id}/convert", url.Values{}), number))
	return rec
}

func poIDByNumber(t *testing.T, h *Handler, ctx context.Context, number string) int {
	t.Helper()
	var id int
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(ID),0) FROM %s WHERE number=@p1`, h.cfg().POTable()), number).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A cancel that lands while the quote is being awarded wins: no PO is created.
func TestIntegration_RFQConvert_StatusRaceRejected(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	requirePostgres(t, h)
	ctx := context.Background()
	quoteID, quoteNumber := seedRFQQuote(t, h, ctx, 1001, 0, 10, 2.50)
	defer cleanupPO(ctx, h, quoteID)
	base := rfqBaseNumber(quoteNumber)
	defer func() {
		if id := poIDByNumber(t, h, ctx, base); id != 0 {
			cleanupPO(ctx, h, id)
		}
	}()

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{fmt.Sprintf(`UPDATE %s SET status='cancelled' WHERE ID=$1`, h.cfg().POTable())},
		[][]any{{quoteID}},
		func() { rec = rfqConvert(h, quoteNumber) })

	if !strings.Contains(rec.Body.String(), "Only an RFQ can be converted to a PO.") {
		t.Errorf("body missing the not-an-RFQ error")
	}
	if id := poIDByNumber(t, h, ctx, base); id != 0 {
		t.Errorf("PO %s was created from a cancelled quote", base)
	}
	if s := poStatus(t, h, ctx, quoteID); s != "cancelled" {
		t.Errorf("quote status = %q, want cancelled", s)
	}
}

// A sibling quote changed while the award runs is left alone, not cancelled.
func TestIntegration_RFQConvert_SiblingChangedConcurrentlyNotCancelled(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	requirePostgres(t, h)
	ctx := context.Background()
	acmeID, _ := seedRFQQuote(t, h, ctx, 1001, 0, 10, 2.50)
	defer cleanupPO(ctx, h, acmeID)
	pmcID, pmcNumber := seedRFQQuote(t, h, ctx, 1002, acmeID, 10, 2.25)
	defer cleanupPO(ctx, h, pmcID)
	base := rfqBaseNumber(pmcNumber)
	defer func() {
		if id := poIDByNumber(t, h, ctx, base); id != 0 {
			cleanupPO(ctx, h, id)
		}
	}()

	raceHandler(t, h, ctx,
		[]string{fmt.Sprintf(`UPDATE %s SET status='draft' WHERE ID=$1`, h.cfg().POTable())},
		[][]any{{acmeID}},
		func() { rfqConvert(h, pmcNumber) })

	if s := poStatus(t, h, ctx, acmeID); s != "draft" {
		t.Errorf("sibling status = %q, want draft (changed concurrently, must not be cancelled)", s)
	}
	var n int
	if err := h.queryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE po_id=@p1 AND to_status='cancelled'`,
		h.cfg().POHistoryTable()), acmeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d cancel history rows for the sibling, want 0", n)
	}
}
