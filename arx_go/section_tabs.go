package main

import "sort"

// Section sub-nav bars: a row of tabs that pops down under a main nav tab on hover.
// Keyed by the page's ActiveTab (the main tab). A page picks its sub-tab with
// "SectionTab"; without it the section's first entry is active.

type sectionTab struct {
	Key, Href, Label, Icon string // Icon: base name in static/shared/icons (no ext/-inv)
}

type sectionBar struct {
	Index int // 0-based position of the parent tab in the main nav (layout.html); update both together
	Tabs  []sectionTab
}

var sectionBars = map[string]sectionBar{
	"suppliers": {Index: 1, Tabs: []sectionTab{
		{"vendors", "/suppliers", "Vendors", "vendors"},
		{"contacts", "/contacts", "Contacts", "contacts"},
	}},
	"records": {Index: 3, Tabs: []sectionTab{
		{"records", "/records", "Records", "records"},
		{"lots", "/lots", "Lots", "lots"},
		{"units", "/units", "Units", "units"},
		{"builds", "/builds", "Builds", "builds"},
	}},
}

type sectionTabView struct {
	Href, Label, Icon string
	Active            bool
}

type sectionBarView struct {
	Key     string
	Index   int
	Current bool // the page's own section: highlights its active sub-tab, and stays visible on touch devices
	Tabs    []sectionTabView
}

// applySectionBar adds SectionBars (every section, ordered by main-tab position)
// to the render data. The bar for the page's own main tab is Current and
// highlights its active sub-tab; the others are popdowns.
func applySectionBar(m map[string]any) {
	tab, _ := m["ActiveTab"].(string)
	active, _ := m["SectionTab"].(string)
	bars := make([]sectionBarView, 0, len(sectionBars))
	for key, bar := range sectionBars {
		current := key == tab
		if current && active == "" {
			active = bar.Tabs[0].Key
		}
		views := make([]sectionTabView, len(bar.Tabs))
		for i, t := range bar.Tabs {
			isActive := current && t.Key == active
			views[i] = sectionTabView{Href: t.Href, Label: t.Label, Icon: t.Icon, Active: isActive}
			if isActive {
				// the main tab shows the selected sub-tab's name and icon
				m["SectionLabel"], m["SectionIcon"] = t.Label, t.Icon
			}
		}
		bars = append(bars, sectionBarView{Key: key, Index: bar.Index, Current: current, Tabs: views})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Index < bars[j].Index })
	m["SectionBars"] = bars
}
