package main

import "testing"

func barsOf(t *testing.T, m map[string]any) []sectionBarView {
	t.Helper()
	bars, ok := m["SectionBars"].([]sectionBarView)
	if !ok {
		t.Fatalf("SectionBars not set: %v", m)
	}
	return bars
}

func findBar(t *testing.T, bars []sectionBarView, key string) sectionBarView {
	t.Helper()
	for _, b := range bars {
		if b.Key == key {
			return b
		}
	}
	t.Fatalf("no bar %q", key)
	return sectionBarView{}
}

func activeLabels(b sectionBarView) []string {
	var out []string
	for _, v := range b.Tabs {
		if v.Active {
			out = append(out, v.Label)
		}
	}
	return out
}

func TestApplySectionBar_DefaultsToFirstTab(t *testing.T) {
	m := map[string]any{"ActiveTab": "records"}
	applySectionBar(m)
	b := findBar(t, barsOf(t, m), "records")
	if !b.Current {
		t.Error("records bar not Current on a records page")
	}
	if got := activeLabels(b); len(got) != 1 || got[0] != "Records" {
		t.Errorf("active = %v, want [Records]", got)
	}
	if b.Index != 3 {
		t.Errorf("Index = %d, want 3", b.Index)
	}
}

func TestApplySectionBar_SectionTabSelectsActiveTab(t *testing.T) {
	m := map[string]any{"ActiveTab": "suppliers", "SectionTab": "contacts"}
	applySectionBar(m)
	b := findBar(t, barsOf(t, m), "suppliers")
	if got := activeLabels(b); len(got) != 1 || got[0] != "Contacts" {
		t.Errorf("active = %v, want [Contacts]", got)
	}
}

// The main tab is relabelled with the selected section tab, only inside a section.
func TestApplySectionBar_SectionLabel(t *testing.T) {
	m := map[string]any{"ActiveTab": "records", "SectionTab": "units"}
	applySectionBar(m)
	if m["SectionLabel"] != "Units" || m["SectionIcon"] != "units" {
		t.Errorf("SectionLabel/Icon = %v/%v, want Units/units", m["SectionLabel"], m["SectionIcon"])
	}
	m = map[string]any{"ActiveTab": "parts"}
	applySectionBar(m)
	if _, ok := m["SectionLabel"]; ok {
		t.Error("SectionLabel set outside a section")
	}
}

// Other sections still render (as hover popdowns) but never show an active section tab,
// even when SectionTab names a key that exists in them.
func TestApplySectionBar_OtherSectionsArePopdowns(t *testing.T) {
	m := map[string]any{"ActiveTab": "parts", "SectionTab": "contacts"}
	applySectionBar(m)
	bars := barsOf(t, m)
	if len(bars) != len(sectionBars) {
		t.Fatalf("got %d bars, want %d", len(bars), len(sectionBars))
	}
	for _, b := range bars {
		if b.Current {
			t.Errorf("%s: Current on a parts page", b.Key)
		}
		if got := activeLabels(b); len(got) != 0 {
			t.Errorf("%s: active = %v, want none", b.Key, got)
		}
	}
}

func TestApplySectionBar_OrderedByMainTabPosition(t *testing.T) {
	m := map[string]any{"ActiveTab": "parts"}
	applySectionBar(m)
	bars := barsOf(t, m)
	for i := 1; i < len(bars); i++ {
		if bars[i-1].Index >= bars[i].Index {
			t.Errorf("bars not ordered by Index: %v", bars)
		}
	}
}

func TestSectionBars_MainTabsKnown(t *testing.T) {
	for tab, bar := range sectionBars {
		if len(bar.Tabs) == 0 {
			t.Errorf("%s: empty section bar", tab)
		}
		if bar.Index < 0 || bar.Index > 4 {
			t.Errorf("%s: Index %d outside the 5 main tabs", tab, bar.Index)
		}
	}
}
