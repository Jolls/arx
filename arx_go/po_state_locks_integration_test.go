//go:build integration

package main

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Tests for #261: POStatusTransition, POApprovalAction and POUpdate lock the PO row
// and re-check its state inside their transaction (same pattern as POReceive, #191).
// Helpers (raceHandler, poStatus, ...) live in tx_boundaries_integration_test.go.

func poApproval(t *testing.T, h *Handler, ctx context.Context, poID int) string {
	t.Helper()
	var s string
	if err := h.queryRowContext(ctx, `SELECT approval_status FROM purchase_order WHERE ID=$1`, poID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func poHistoryCount(t *testing.T, h *Handler, ctx context.Context, poID int, eventType string) int {
	t.Helper()
	var n int
	if err := h.queryRowContext(ctx, `SELECT COUNT(*) FROM purchase_order_history WHERE po_id=$1 AND event_type=$2`, poID, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A cancel of a PO read as sent must not overwrite a close that commits first.
func TestIntegration_POStatusTransition_StatusRaceRejected(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	poID, number, cl := seedThrowawayPO(t, h, ctx)
	defer cl()
	setPOStatus(t, h, ctx, poID, "sent", "approved")
	before := poHistoryCount(t, h, ctx, poID, "status")

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{`UPDATE purchase_order SET status='closed' WHERE ID=$1`},
		[][]any{{poID}},
		func() {
			rec = httptest.NewRecorder()
			h.POStatusTransition(rec, withIDStr(postForm("/po/{id}/status", url.Values{"target": {"cancelled"}}), number))
		})

	if !strings.Contains(rec.Body.String(), "Cannot change status") {
		t.Errorf("body missing the wrong-status error")
	}
	if s := poStatus(t, h, ctx, poID); s != "closed" {
		t.Errorf("PO status = %q, want closed", s)
	}
	if n := poHistoryCount(t, h, ctx, poID, "status"); n != before {
		t.Errorf("status events = %d, want %d (a cancel that did not happen was logged)", n, before)
	}
}

// Marking Sent re-checks approval under the lock: an approval reset that commits first wins.
func TestIntegration_POStatusTransition_ApprovalResetRaceRejected(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	poID, number, cl := seedThrowawayPO(t, h, ctx)
	defer cl()
	setPOStatus(t, h, ctx, poID, "open", "approved")

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{`UPDATE purchase_order SET approval_status='not_submitted' WHERE ID=$1`},
		[][]any{{poID}},
		func() {
			rec = httptest.NewRecorder()
			h.POStatusTransition(rec, withIDStr(postForm("/po/{id}/status", url.Values{"target": {"sent"}}), number))
		})

	if !strings.Contains(rec.Body.String(), "must be approved") {
		t.Errorf("body missing the approval-gate error")
	}
	if s := poStatus(t, h, ctx, poID); s != "open" {
		t.Errorf("PO status = %q, want open (sent without approval)", s)
	}
}

// A submit that loses a race to another submit is rejected, not logged.
func TestIntegration_POApprovalAction_StateRaceRejected(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	poID, number, cl := seedThrowawayPO(t, h, ctx)
	defer cl()
	setPOStatus(t, h, ctx, poID, "draft", "not_submitted")

	var rec *httptest.ResponseRecorder
	raceHandler(t, h, ctx,
		[]string{`UPDATE purchase_order SET approval_status='pending' WHERE ID=$1`},
		[][]any{{poID}},
		func() {
			rec = httptest.NewRecorder()
			h.POApprovalAction(rec, withIDStr(postForm("/po/{id}/approval", url.Values{"action": {"submit"}}), number))
		})

	if !strings.Contains(rec.Body.String(), "Cannot submit") {
		t.Errorf("body missing the wrong-state error")
	}
	if n := poHistoryCount(t, h, ctx, poID, "approval"); n != 0 {
		t.Errorf("approval events = %d, want 0", n)
	}
}

// An edit resets an approval that committed after the edit started.
func TestIntegration_POUpdate_ApprovalRaceStillReset(t *testing.T) {
	h, cleanup := liveHandler(t)
	defer cleanup()
	ctx := context.Background()
	poID, number, cl := seedThrowawayPO(t, h, ctx)
	defer cl()
	setPOStatus(t, h, ctx, poID, "draft", "not_submitted")

	raceHandler(t, h, ctx,
		[]string{`UPDATE purchase_order SET approval_status='approved' WHERE ID=$1`},
		[][]any{{poID}},
		func() {
			h.POUpdate(httptest.NewRecorder(), withIDStr(postForm("/po/{id}", url.Values{"supplier_id": {"1001"}}), number))
		})

	if a := poApproval(t, h, ctx, poID); a != "not_submitted" {
		t.Errorf("approval = %q, want not_submitted (edited PO left approved)", a)
	}
}
