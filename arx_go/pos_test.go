package main

import "testing"

func TestPOApprovalNext(t *testing.T) {
	ok := []struct{ action, from, want string }{
		{"submit", "not_submitted", "pending"},
		{"submit", "rejected", "pending"},
		{"approve", "pending", "approved"},
		{"reject", "pending", "rejected"},
	}
	for _, c := range ok {
		got, valid := poApprovalNext(c.action, c.from)
		if !valid || got != c.want {
			t.Errorf("poApprovalNext(%q,%q) = (%q,%v), want (%q,true)", c.action, c.from, got, valid, c.want)
		}
	}

	bad := []struct{ action, from string }{
		{"submit", "pending"},   // already submitted
		{"submit", "approved"},  // already approved
		{"approve", "not_submitted"},
		{"approve", "approved"}, // not pending
		{"approve", "rejected"},
		{"reject", "not_submitted"},
		{"reject", "approved"},
		{"bogus", "pending"}, // unknown action
	}
	for _, c := range bad {
		if got, valid := poApprovalNext(c.action, c.from); valid {
			t.Errorf("poApprovalNext(%q,%q) = (%q,true), want invalid", c.action, c.from, got)
		}
	}
}

func TestPOApprovalAllowsSend(t *testing.T) {
	if !poApprovalAllowsSend("approved") {
		t.Error("poApprovalAllowsSend(approved) = false, want true")
	}
	for _, s := range []string{"not_submitted", "pending", "rejected", ""} {
		if poApprovalAllowsSend(s) {
			t.Errorf("poApprovalAllowsSend(%q) = true, want false", s)
		}
	}
}

func TestPOApprovalActions(t *testing.T) {
	// A non-approver can submit (when not_submitted/rejected) but never approve/reject.
	for _, status := range []string{"not_submitted", "rejected"} {
		acts := poApprovalActions(status, false)
		if len(acts) != 1 || acts[0].Action != "submit" {
			t.Errorf("poApprovalActions(%q,false) = %+v, want a single submit", status, acts)
		}
	}
	// Pending offers nothing to a non-approver, approve+reject to an approver.
	if acts := poApprovalActions("pending", false); len(acts) != 0 {
		t.Errorf("poApprovalActions(pending,false) = %+v, want none", acts)
	}
	if acts := poApprovalActions("pending", true); len(acts) != 2 {
		t.Errorf("poApprovalActions(pending,true) = %+v, want approve+reject", acts)
	}
	// Approved is terminal — no further actions even for an approver.
	if acts := poApprovalActions("approved", true); len(acts) != 0 {
		t.Errorf("poApprovalActions(approved,true) = %+v, want none", acts)
	}
}

func TestPOCanTransition(t *testing.T) {
	allowed := []struct{ from, to string }{
		{"draft", "open"},
		{"draft", "cancelled"},
		{"open", "sent"},
		{"sent", "partially_received"},
		{"sent", "closed"},
		{"partially_received", "closed"},
		{"closed", "open"},     // reopen
		{"cancelled", "draft"}, // reopen
	}
	for _, c := range allowed {
		if !poCanTransition(c.from, c.to) {
			t.Errorf("poCanTransition(%q,%q) = false, want true", c.from, c.to)
		}
	}

	disallowed := []struct{ from, to string }{
		{"draft", "sent"},               // must go through open
		{"draft", "closed"},             // skips the chain
		{"open", "partially_received"},  // must be sent first
		{"closed", "cancelled"},         // closed only reopens to open
		{"cancelled", "open"},           // cancelled only reopens to draft
		{"sent", "draft"},               // no backward jump
		{"draft", "draft"},              // no self-transition
		{"bogus", "open"},               // unknown source
	}
	for _, c := range disallowed {
		if poCanTransition(c.from, c.to) {
			t.Errorf("poCanTransition(%q,%q) = true, want false", c.from, c.to)
		}
	}
}

func TestPOStatusActions(t *testing.T) {
	// Every action target must be a valid transition from the current status,
	// and labels/classes must be populated.
	for _, status := range []string{"draft", "open", "sent", "partially_received", "closed", "cancelled"} {
		actions := poStatusActions(status)
		if len(actions) == 0 {
			t.Errorf("poStatusActions(%q) returned no actions", status)
		}
		for _, a := range actions {
			if !poCanTransition(status, a.Target) {
				t.Errorf("poStatusActions(%q) offered invalid target %q", status, a.Target)
			}
			if a.Label == "" || a.Class == "" {
				t.Errorf("poStatusActions(%q) target %q missing label/class", status, a.Target)
			}
		}
	}
}
