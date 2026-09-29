//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// lifecycleSetup opens a live handler and seedPOFixture, both torn down with t.Cleanup so the
// POs seedLifecyclePO registers are removed first.
func lifecycleSetup(t *testing.T) (*Handler, poFixture) {
	t.Helper()
	h, done := liveHandler(t)
	t.Cleanup(done)
	f, cleanup := seedPOFixture(t, h)
	t.Cleanup(cleanup)
	return h, f
}

type lcLine struct {
	part any // nil = freeform
	qty  float64
}

// seedLifecyclePO inserts a PO on the fixture company with the given state and lines; its lines'
// ledger rows, lines, history and the PO are deleted on cleanup.
func seedLifecyclePO(t *testing.T, h *Handler, f poFixture, suffix, status, approval, dateClosed string, lines ...lcLine) (num string, id int, lineIDs []int) {
	t.Helper()
	ctx := context.Background()
	num = strings.TrimSuffix(f.Full, "-f") + "-" + suffix
	if err := h.queryRowContext(ctx, `INSERT INTO purchase_order (number, supplier_id, status, approval_status, is_active, date_closed, date_modified)
		VALUES ($1,$2,$3,$4,TRUE,NULLIF($5,'')::date,'2026-01-01') RETURNING id`,
		num, f.Co, status, approval, dateClosed).Scan(&id); err != nil {
		t.Fatalf("seed PO: %v", err)
	}
	t.Cleanup(func() {
		smokeExec(ctx, h, `DELETE FROM inventory_transaction WHERE po_line_id IN (SELECT id FROM po_line WHERE po_id=$1)`, id)
		cleanupPO(ctx, h, id)
	})
	for i, l := range lines {
		var lid int
		if err := h.queryRowContext(ctx, `INSERT INTO po_line (po_id, part_id, line_number, description, qty, unit_cost)
			VALUES ($1,$2,$3,'lc',$4,1) RETURNING id`, id, l.part, i+1, l.qty).Scan(&lid); err != nil {
			t.Fatalf("seed line: %v", err)
		}
		lineIDs = append(lineIDs, lid)
	}
	return num, id, lineIDs
}

type poRowState struct {
	Status, Approval string
	Active           bool
	Closed, Modified string
}

func readPOState(t *testing.T, h *Handler, id int) poRowState {
	t.Helper()
	var s poRowState
	if err := h.queryRowContext(context.Background(), `SELECT COALESCE(status,''), COALESCE(approval_status,''), is_active,
		COALESCE(date_closed::text,''), COALESCE(date_modified::date::text,'') FROM purchase_order WHERE id=$1`, id).
		Scan(&s.Status, &s.Approval, &s.Active, &s.Closed, &s.Modified); err != nil {
		t.Fatalf("read PO %d: %v", id, err)
	}
	return s
}

// lastEvent returns the newest history row of eventType as "from>to|action|note|by" (NULL → "∅").
func lastEvent(t *testing.T, h *Handler, id int, eventType string) string {
	t.Helper()
	var s string
	err := h.queryRowContext(context.Background(), `SELECT COALESCE(from_status,'∅') || '>' || COALESCE(to_status,'∅') || '|' ||
		COALESCE(action,'∅') || '|' || COALESCE(note,'∅') || '|' || changed_by FROM purchase_order_history WHERE po_id=$1 AND event_type=$2 ORDER BY id DESC LIMIT 1`,
		id, eventType).Scan(&s)
	if err != nil {
		return ""
	}
	return s
}

func dbToday(t *testing.T, h *Handler) string {
	t.Helper()
	var s string
	if err := h.queryRowContext(context.Background(), `SELECT CURRENT_DATE::text`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// POStatusTransition: closing keeps an existing date_closed, reopening clears it; is_active,
// date_modified and the history actor; cancel resets only a live approval.
func TestIntegration_POLifecycle_StatusTransition(t *testing.T) {
	h, f := lifecycleSetup(t)
	today := dbToday(t, h)
	transition := func(num, target string, asApprover bool) *httptest.ResponseRecorder {
		req := withIDStr(postForm("/po/x/status", url.Values{"target": {target}}), num)
		if asApprover {
			req = approverCtx(req)
		}
		rec := httptest.NewRecorder()
		h.POStatusTransition(rec, req)
		return rec
	}

	num, id, _ := seedLifecyclePO(t, h, f, "s1", "sent", "approved", "2026-01-05")
	assert302(t, "sent→closed", transition(num, "closed", true))
	if got, want := readPOState(t, h, id), (poRowState{"closed", "approved", false, "2026-01-05", today}); got != want {
		t.Errorf("closed: %+v, want %+v", got, want)
	}
	if got := lastEvent(t, h, id, "status"); got != "sent>closed|∅|∅|admin" {
		t.Errorf("close event = %q", got)
	}
	assert302(t, "closed→open", transition(num, "open", false))
	if got, want := readPOState(t, h, id), (poRowState{"open", "approved", true, "", today}); got != want {
		t.Errorf("reopened: %+v, want %+v", got, want)
	}
	if got := lastEvent(t, h, id, "status"); got != "closed>open|∅|∅|system" {
		t.Errorf("reopen event = %q", got)
	}

	num, id, _ = seedLifecyclePO(t, h, f, "s2", "open", "not_submitted", "")
	assert302(t, "cancel unsubmitted", transition(num, "cancelled", false))
	if got := readPOState(t, h, id); got.Status != "cancelled" || got.Active || got.Approval != "not_submitted" {
		t.Errorf("cancelled: %+v", got)
	}
	if got := lastEvent(t, h, id, "approval"); got != "" {
		t.Errorf("unsubmitted cancel wrote approval event %q", got)
	}

	num, id, _ = seedLifecyclePO(t, h, f, "s3", "open", "pending", "")
	assert302(t, "cancel pending", transition(num, "cancelled", false))
	if got := readPOState(t, h, id).Approval; got != "not_submitted" {
		t.Errorf("pending cancel approval = %q", got)
	}
	if got := lastEvent(t, h, id, "approval"); got != "∅>∅|reset|PO cancelled|system" {
		t.Errorf("reset event = %q", got)
	}

	for _, c := range []struct{ num, target, want string }{
		{num, "bogus", "Unknown status: bogus"},
		{num + "x", "open", "Purchase order not found"},
	} {
		if body := transition(c.num, c.target, false).Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("transition %s→%s: body missing %q", c.num, c.target, c.want)
		}
	}
}

// POApprovalAction: an empty note is stored as NULL, the actor is recorded, status is untouched;
// disallowed actions and unknown POs are rejected.
func TestIntegration_POLifecycle_ApprovalAction(t *testing.T) {
	h, f := lifecycleSetup(t)
	act := func(num, action, note string, asApprover bool) *httptest.ResponseRecorder {
		req := withIDStr(postForm("/po/x/approval", url.Values{"action": {action}, "note": {note}}), num)
		if asApprover {
			req = approverCtx(req)
		}
		rec := httptest.NewRecorder()
		h.POApprovalAction(rec, req)
		return rec
	}

	num, id, _ := seedLifecyclePO(t, h, f, "a1", "open", "not_submitted", "")
	assert302(t, "submit", act(num, "submit", "", false))
	if got := readPOState(t, h, id); got.Approval != "pending" || got.Status != "open" || got.Modified != "2026-01-01" {
		t.Errorf("submitted: %+v", got)
	}
	if got := lastEvent(t, h, id, "approval"); got != "∅>∅|submitted|∅|system" {
		t.Errorf("submit event = %q", got)
	}
	assert302(t, "approve", act(num, "approve", "ok", true))
	if got := readPOState(t, h, id).Approval; got != "approved" {
		t.Errorf("approved: %q", got)
	}
	if got := lastEvent(t, h, id, "approval"); got != "∅>∅|approved|ok|admin" {
		t.Errorf("approve event = %q", got)
	}

	for _, c := range []struct{ num, action, want string }{
		{num, "approve", "Cannot approve a PO whose approval status is"},
		{num, "bogus", "Unknown approval action: bogus"},
		{num + "x", "submit", "Purchase order not found"},
	} {
		if body := act(c.num, c.action, "", true).Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("%s %s: body missing %q", c.action, c.num, c.want)
		}
	}
}

// POReceive: a freeform line records receipt without a ledger row; date_received is the txn date
// (today when blank); untouched lines stay put; a full receipt closes the PO.
func TestIntegration_POLifecycle_Receive(t *testing.T) {
	h, f := lifecycleSetup(t)
	ctx := context.Background()
	today := dbToday(t, h)
	num, id, lines := seedLifecyclePO(t, h, f, "r1", "sent", "approved", "",
		lcLine{f.P2, 4}, lcLine{nil, 2}, lcLine{f.P2, 5})
	receive := func(n string, vals url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.POReceive(rec, withIDStr(postForm("/po/x/receive", vals), n))
		return rec
	}
	recv := func(i int) string { return fmt.Sprintf("recv[%d]", lines[i]) }
	lineState := func() string {
		t.Helper()
		var s string
		if err := h.queryRowContext(ctx, `SELECT string_agg(received_qty::float8 || '@' || COALESCE(date_received::text,'∅'), ',' ORDER BY line_number)
			FROM po_line WHERE po_id=$1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	ledger := func() string {
		t.Helper()
		var s string
		if err := h.queryRowContext(ctx, `SELECT COALESCE(string_agg(po_line_id || ':' || qty::float8 || '@' || txn_date, ',' ORDER BY id), '')
			FROM inventory_transaction WHERE po_line_id IN (SELECT id FROM po_line WHERE po_id=$1)`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	assert302(t, "partial", receive(num, url.Values{recv(0): {"4"}, recv(1): {"1"}, recv(2): {""}, "txn_date": {"2026-03-10"}}))
	if got, want := lineState(), "4@2026-03-10,1@2026-03-10,0@∅"; got != want {
		t.Errorf("lines = %s, want %s", got, want)
	}
	if got, want := ledger(), fmt.Sprintf("%d:4@2026-03-10", lines[0]); got != want {
		t.Errorf("ledger = %s, want %s", got, want)
	}
	if got := readPOState(t, h, id); got.Status != "partially_received" || !got.Active || got.Closed != "" {
		t.Errorf("after partial: %+v", got)
	}
	if got := lastEvent(t, h, id, "status"); got != "sent>partially_received|∅|∅|system" {
		t.Errorf("partial event = %q", got)
	}

	localToday := time.Now().Format("2006-01-02")
	assert302(t, "full", receive(num, url.Values{recv(1): {"1"}, recv(2): {"5"}}))
	if got, want := lineState(), fmt.Sprintf("4@2026-03-10,2@%[1]s,5@%[1]s", localToday); got != want {
		t.Errorf("lines = %s, want %s", got, want)
	}
	if got := readPOState(t, h, id); got.Status != "closed" || got.Active || got.Closed != today {
		t.Errorf("after full: %+v", got)
	}
	if got := lastEvent(t, h, id, "status"); got != "partially_received>closed|∅|∅|system" {
		t.Errorf("close event = %q", got)
	}

	num2, _, lines2 := seedLifecyclePO(t, h, f, "r2", "sent", "approved", "", lcLine{f.P2, 1})
	for _, c := range []struct {
		num  string
		vals url.Values
		want string
	}{
		{num2, url.Values{fmt.Sprintf("recv[%d]", lines2[0]): {"abc"}}, "Invalid quantity for a line item."},
		{num2, url.Values{fmt.Sprintf("recv[%d]", lines2[0]): {"0"}}, "Enter a quantity to receive on at least one line."},
		{num2 + "x", url.Values{}, "Purchase order not found"},
		{num, url.Values{recv(0): {"1"}}, "Only a sent or partially-received PO can receive goods."},
	} {
		if body := receive(c.num, c.vals).Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("receive %s %v: body missing %q", c.num, c.vals, c.want)
		}
	}
}
