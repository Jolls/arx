package main

import "testing"

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
