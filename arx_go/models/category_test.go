package models

import "testing"

func TestTabsForCategory(t *testing.T) {
	cats := DefaultCategories()

	cases := []struct {
		code string
		want CategoryTabs
	}{
		// Assemblies and manufactured parts author a BOM + full procurement + inventory.
		{"ASM", CategoryTabs{BOM: true, Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true}},
		{"MFG", CategoryTabs{BOM: true, Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true}},
		// Test Form authors a BOM but isn't procured.
		{"FORM", CategoryTabs{BOM: true}},
		// Purchased + stocked: procurement tabs + inventory, BOM left data-driven.
		{"BUY", CategoryTabs{Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true}},
		// Purchased but not stocked: procurement tabs, no inventory.
		{"SVC", CategoryTabs{Orders: true, Pricing: true, MfgParts: true, Suppliers: true}},
		{"TOOL", CategoryTabs{Orders: true, Pricing: true, MfgParts: true, Suppliers: true}},
		// Paperwork: nothing optional.
		{"DOC", CategoryTabs{}},
		// Unknown and empty codes fall back to the permissive default.
		{"ZZZ", DefaultCategoryTabs},
		{"", DefaultCategoryTabs},
	}
	for _, c := range cases {
		if got := TabsForCategory(cats, c.code); got != c.want {
			t.Errorf("TabsForCategory(%q) = %+v, want %+v", c.code, got, c.want)
		}
	}
}

func TestDefaultCategories(t *testing.T) {
	cats := DefaultCategories()
	if len(cats) == 0 {
		t.Fatal("DefaultCategories() is empty")
	}

	tabs := make(map[string]CategoryTabs, len(cats))
	for _, c := range cats {
		if c.Code == "" || c.Label == "" {
			t.Errorf("category has empty code or label: %+v", c)
		}
		tabs[c.Code] = c.CategoryTabs
	}

	if !tabs["ASM"].BOM {
		t.Error("ASM should author a BOM")
	}
	if (tabs["DOC"] != CategoryTabs{}) {
		t.Errorf("DOC should expose no optional tabs, got %+v", tabs["DOC"])
	}
}

func TestShowInventory(t *testing.T) {
	cats := DefaultCategories()
	stocked := []string{"BUY", "RAW", "MFG", "ASM"}
	notStocked := []string{"DOC", "DWG", "FORM", "SVC", "TOOL"}
	for _, code := range stocked {
		if !TabsForCategory(cats, code).Inventory {
			t.Errorf("%s should be stockable (Inventory=true)", code)
		}
	}
	for _, code := range notStocked {
		if TabsForCategory(cats, code).Inventory {
			t.Errorf("%s should not be stockable (Inventory=false)", code)
		}
	}
	// ShowInventory reads the resolved flag.
	if !(Part{Tabs: CategoryTabs{Inventory: true}}).ShowInventory() {
		t.Error("ShowInventory() = false when Tabs.Inventory is true")
	}
	if (Part{Tabs: CategoryTabs{}}).ShowInventory() {
		t.Error("ShowInventory() = true when Tabs.Inventory is false")
	}
}

func TestShowBOM(t *testing.T) {
	// Data-driven: a part with a BOM shows the tab even if its category doesn't author one.
	if p := (Part{HasBOM: true, Tabs: CategoryTabs{}}); !p.ShowBOM() {
		t.Error("ShowBOM() = false for a part that has a BOM")
	}
	// Category-driven: an empty assembly still shows the BOM tab so one can be entered.
	if p := (Part{HasBOM: false, Tabs: CategoryTabs{BOM: true}}); !p.ShowBOM() {
		t.Error("ShowBOM() = false for a BOM-authoring category with no BOM yet")
	}
	// Neither: grayed out.
	if p := (Part{HasBOM: false, Tabs: CategoryTabs{}}); p.ShowBOM() {
		t.Error("ShowBOM() = true with no BOM and a non-authoring category")
	}
}
