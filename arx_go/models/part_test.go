package models

import "testing"

func TestBelowReorder(t *testing.T) {
	min := func(v float64) *float64 { return &v }
	cases := []struct {
		name       string
		stock      float64
		reorderMin *float64
		want       bool
	}{
		{"no reorder point set", 0, nil, false},
		{"stock above min", 20, min(10), false},
		{"stock equal to min (boundary)", 10, min(10), false},
		{"stock below min", 9, min(10), true},
		{"zero stock, min set", 0, min(5), true},
		{"zero stock, zero min", 0, min(0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Part{StockOnHand: c.stock, ReorderMin: c.reorderMin}
			if got := p.BelowReorder(); got != c.want {
				t.Errorf("BelowReorder() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserFieldsForEdit(t *testing.T) {
	p := Part{
		UserField1: "Alpha", UserField2: "", UserField3: "Gamma",
	}
	fields := p.UserFieldsForEdit()

	if len(fields) != 10 {
		t.Fatalf("expected 10 fields, got %d", len(fields))
	}

	// All 10 returned regardless of empty/non-empty
	cases := []struct {
		idx       int
		wantName  string
		wantLabel string
		wantValue string
	}{
		{0, "user_field_1", "User 1", "Alpha"},
		{1, "user_field_2", "User 2", ""},
		{2, "user_field_3", "User 3", "Gamma"},
		{9, "user_field_10", "User 10", ""},
	}
	for _, c := range cases {
		f := fields[c.idx]
		if f.Name != c.wantName || f.Label != c.wantLabel || f.Value != c.wantValue {
			t.Errorf("fields[%d] = {%q, %q, %q}, want {%q, %q, %q}",
				c.idx, f.Name, f.Label, f.Value, c.wantName, c.wantLabel, c.wantValue)
		}
	}
}

func TestUserFields(t *testing.T) {
	t.Run("only non-empty fields returned", func(t *testing.T) {
		p := Part{UserField1: "Alpha", UserField3: "Gamma"}
		fields := p.UserFields()
		if len(fields) != 2 {
			t.Fatalf("expected 2 fields, got %d", len(fields))
		}
		if fields[0].Label != "User 1" || fields[0].Value != "Alpha" {
			t.Errorf("fields[0] = %+v, want {User 1, Alpha}", fields[0])
		}
		if fields[1].Label != "User 3" || fields[1].Value != "Gamma" {
			t.Errorf("fields[1] = %+v, want {User 3, Gamma}", fields[1])
		}
	})

	t.Run("all empty returns nil slice", func(t *testing.T) {
		p := Part{}
		if fields := p.UserFields(); len(fields) != 0 {
			t.Errorf("expected empty, got %v", fields)
		}
	})

	t.Run("all filled returns 10", func(t *testing.T) {
		p := Part{
			UserField1: "a", UserField2: "b", UserField3: "c", UserField4: "d", UserField5: "e",
			UserField6: "f", UserField7: "g", UserField8: "h", UserField9: "i", UserField10: "j",
		}
		if fields := p.UserFields(); len(fields) != 10 {
			t.Errorf("expected 10, got %d", len(fields))
		}
	})
}
