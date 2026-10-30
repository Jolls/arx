package main

// Main nav and section tab bars. mainTabs drives the top nav (layout.html ranges over
// MainTabs). A main tab with an entry in sectionBars also gets a row of section tabs
// that pops down under it on hover. A page picks its main tab with "ActiveTab" and its
// section tab with "SectionTab"; without it the section's first entry is active.

type mainTab struct {
	Key, Href, Label, Icon string // Key is the ActiveTab value; Icon: base name in static/shared/icons
}

// The order here is the nav order, and sets where each section's bar sits.
var mainTabs = []mainTab{
	{"parts", "/parts", "Parts", "parts"},
	{"suppliers", "/suppliers", "Vendors", "vendors"},
	{"pos", "/pos", "POs", "pos"},
	{"records", "/records", "Records", "records"},
	{"reports", "/reports", "Reports", "reports"},
}

type sectionTab struct {
	Key, Href, Label, Icon string // Icon: base name in static/shared/icons (no ext/-inv)
}

// sectionBars is keyed by a mainTabs Key.
var sectionBars = map[string][]sectionTab{
	"suppliers": {
		{"vendors", "/suppliers", "Vendors", "vendors"},
		{"contacts", "/contacts", "Contacts", "contacts"},
	},
	"pos": {
		{"pos", "/pos", "POs", "pos"},
		{"rfqs", "/rfqs", "RFQs", "rfqs"},
	},
	"records": {
		{"records", "/records", "Records", "records"},
		{"lots", "/lots", "Lots", "lots"},
		{"units", "/units", "Units", "units"},
		{"builds", "/builds", "Builds", "builds"},
	},
}

type mainTabView struct {
	Key, Href, Label, Icon string
	Active                 bool
	HasSection             bool // has a pop-down row of section tabs
}

type sectionTabView struct {
	Href, Label, Icon string
	Active            bool
}

type sectionBarView struct {
	Key     string
	Index   int // position of the parent in mainTabs
	Current bool // the page's own section: highlights its active section tab, and stays visible on touch devices
	Tabs    []sectionTabView
}

// applySectionBar adds MainTabs and SectionBars to the render data. SectionBars has one
// bar per section, in main-tab order; the bar for the page's own main tab is
// Current and highlights its active section tab, the others are popdowns. In its own
// section the main tab shows the selected section tab's name and icon.
func applySectionBar(m map[string]any) {
	tab, _ := m["ActiveTab"].(string)
	active, _ := m["SectionTab"].(string)
	bars := make([]sectionBarView, 0, len(sectionBars))
	label, icon := "", ""
	for i, mt := range mainTabs {
		tabs, ok := sectionBars[mt.Key]
		if !ok {
			continue
		}
		current := mt.Key == tab
		if current && active == "" {
			active = tabs[0].Key
		}
		views := make([]sectionTabView, len(tabs))
		for j, t := range tabs {
			isActive := current && t.Key == active
			views[j] = sectionTabView{Href: t.Href, Label: t.Label, Icon: t.Icon, Active: isActive}
			if isActive {
				label, icon = t.Label, t.Icon
			}
		}
		bars = append(bars, sectionBarView{Key: mt.Key, Index: i, Current: current, Tabs: views})
	}
	m["SectionBars"] = bars

	views := make([]mainTabView, len(mainTabs))
	for i, mt := range mainTabs {
		v := mainTabView{Key: mt.Key, Href: mt.Href, Label: mt.Label, Icon: mt.Icon,
			Active: mt.Key == tab || (mt.Key == "parts" && tab == "")}
		_, v.HasSection = sectionBars[mt.Key]
		if v.Active && label != "" {
			v.Label, v.Icon = label, icon
		}
		views[i] = v
	}
	m["MainTabs"] = views
}
