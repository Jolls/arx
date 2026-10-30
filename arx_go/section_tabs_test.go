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
	if b.Index != 3 { // Parts, Vendors, POs, Records
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

func mainTabByKey(t *testing.T, m map[string]any, key string) mainTabView {
	t.Helper()
	views, ok := m["MainTabs"].([]mainTabView)
	if !ok {
		t.Fatalf("MainTabs not set: %v", m)
	}
	for _, v := range views {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("no main tab %q", key)
	return mainTabView{}
}

// The page's own main tab is relabelled with the selected section tab; others keep their names.
func TestApplySectionBar_MainTabShowsSelectedSectionTab(t *testing.T) {
	m := map[string]any{"ActiveTab": "records", "SectionTab": "units"}
	applySectionBar(m)
	if v := mainTabByKey(t, m, "records"); v.Label != "Units" || v.Icon != "units" || !v.Active {
		t.Errorf("records tab = %+v, want active Units/units", v)
	}
	if v := mainTabByKey(t, m, "pos"); v.Label != "POs" || v.Icon != "pos" || v.Active {
		t.Errorf("pos tab = %+v, want inactive POs/pos", v)
	}
}

func TestApplySectionBar_MainTabs(t *testing.T) {
	m := map[string]any{"ActiveTab": ""}
	applySectionBar(m)
	if v := mainTabByKey(t, m, "parts"); !v.Active || v.HasSection {
		t.Errorf("parts tab = %+v, want active (empty ActiveTab) with no section", v)
	}
	if v := mainTabByKey(t, m, "suppliers"); v.Active || !v.HasSection {
		t.Errorf("suppliers tab = %+v, want inactive with a section", v)
	}
	m = map[string]any{"ActiveTab": "settings"}
	applySectionBar(m)
	for _, v := range m["MainTabs"].([]mainTabView) {
		if v.Active {
			t.Errorf("%s active on a page outside the main tabs", v.Key)
		}
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

// A section bar's key must be a main tab, or its bar is built but never reachable.
func TestSectionBars_MainTabsKnown(t *testing.T) {
	keys := map[string]bool{}
	for _, mt := range mainTabs {
		keys[mt.Key] = true
	}
	for tab, tabs := range sectionBars {
		if !keys[tab] {
			t.Errorf("section bar %q has no main tab", tab)
		}
		if len(tabs) == 0 {
			t.Errorf("%s: empty section bar", tab)
		}
	}
}
