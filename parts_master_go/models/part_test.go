package models

import "testing"

func TestUserFieldsForEdit(t *testing.T) {
	p := Part{
		PNUser1: "Alpha", PNUser2: "", PNUser3: "Gamma",
	}
	fields := p.UserFieldsForEdit()

	if len(fields) != 10 {
		t.Fatalf("expected 10 fields, got %d", len(fields))
	}

	// All 10 returned regardless of empty/non-empty
	cases := []struct {
		idx        int
		wantName   string
		wantLabel  string
		wantValue  string
	}{
		{0, "PNUser1", "User 1", "Alpha"},
		{1, "PNUser2", "User 2", ""},
		{2, "PNUser3", "User 3", "Gamma"},
		{9, "PNUser10", "User 10", ""},
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
		p := Part{PNUser1: "Alpha", PNUser3: "Gamma"}
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
			PNUser1: "a", PNUser2: "b", PNUser3: "c", PNUser4: "d", PNUser5: "e",
			PNUser6: "f", PNUser7: "g", PNUser8: "h", PNUser9: "i", PNUser10: "j",
		}
		if fields := p.UserFields(); len(fields) != 10 {
			t.Errorf("expected 10, got %d", len(fields))
		}
	})
}
