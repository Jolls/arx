package models

import (
	"fmt"
	"time"
)

type Part struct {
	PNID             int
	PartNumber       string
	Revision         string
	Title            string
	Detail           string
	Category         string
	HasBOM           bool
	ReleaseStatus    string
	Active           bool
	PNReqBy          string
	PNNotes          string
	PNDate           *time.Time
	PNDateModified   *time.Time
	PNFILIDPrimary   int
	PNQty            float64
	PNCurrentCost    float64
	PNLastRollupCost float64
	PNLastRollupAt   *time.Time
	PNFILLinks       int
	PNPOLinks        int
	UserField1       string
	UserField2       string
	UserField3       string
	UserField4       string
	UserField5       string
	UserField6       string
	UserField7       string
	UserField8       string
	UserField9       string
	UserField10      string
	UnitID           *int
	UnitAbbr         string       // joined from unit table; abbreviation of PNUNID
	Tabs             CategoryTabs // resolved subtab visibility for Category; set by applyCategoryTabs
}

// CategoryTabs holds the optional-subtab visibility for a part category.
// Universal tabs (Details, Where Used, Attachments, Edit) are always shown.
// BOM = true means the category authors a BOM, so the tab stays enabled even
// before any lines exist (see ShowBOM); the procurement flags gray their tabs
// out when false.
type CategoryTabs struct {
	BOM       bool `json:"bom"`
	Orders    bool `json:"orders"`
	Pricing   bool `json:"pricing"`
	MfgParts  bool `json:"mfgParts"`
	Suppliers bool `json:"suppliers"`
}

// Category is a configurable part category: a code, a display label, and the
// subtabs it exposes. Stored as JSON in app_config (key part_categories); when
// nothing is saved the built-in DefaultCategories are used.
type Category struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	CategoryTabs
}

// DefaultCategoryTabs is the permissive fallback for categories that aren't in
// the configured list (and empty/unknown codes): every procurement tab shown,
// BOM left data-driven. We only hide tabs for categories explicitly configured.
var DefaultCategoryTabs = CategoryTabs{Orders: true, Pricing: true, MfgParts: true, Suppliers: true}

// DefaultCategories is the seed list shown until an admin customizes it in
// Settings. It reproduces the original hardcoded behavior.
func DefaultCategories() []Category {
	proc := DefaultCategoryTabs                                                                    // purchased: procurement tabs, BOM data-driven
	built := CategoryTabs{BOM: true, Orders: true, Pricing: true, MfgParts: true, Suppliers: true} // made + possibly outsourced
	return []Category{
		{"ASM", "Assembly", built},
		{"BUY", "Purchased", proc},
		{"DWG", "Drawing", CategoryTabs{}},
		{"DOC", "Document", CategoryTabs{}},
		{"FORM", "Test Form", CategoryTabs{BOM: true}},
		{"MFG", "Manufactured", built},
		{"RAW", "Raw Material", proc},
		{"SVC", "Service", proc},
		{"TOOL", "Tooling", proc},
	}
}

// TabsForCategory returns the tab visibility configured for code, or the
// permissive default when the code isn't in cats.
func TabsForCategory(cats []Category, code string) CategoryTabs {
	for _, c := range cats {
		if c.Code == code {
			return c.CategoryTabs
		}
	}
	return DefaultCategoryTabs
}

// ShowBOM reports whether the BOM subtab applies: the part already has a BOM
// (data), OR its category authors BOMs and should allow entry even when empty
// (e.g. ASM). The OR keeps the tab live for an assembly before any lines exist.
func (p Part) ShowBOM() bool { return p.HasBOM || p.Tabs.BOM }

// ShowOrders, ShowPricing, ShowMfgParts, ShowSuppliers report whether the part's
// category makes each procurement subtab applicable. Tabs is resolved by the
// handler (applyCategoryTabs) from the configured categories.
func (p Part) ShowOrders() bool    { return p.Tabs.Orders }
func (p Part) ShowPricing() bool   { return p.Tabs.Pricing }
func (p Part) ShowMfgParts() bool  { return p.Tabs.MfgParts }
func (p Part) ShowSuppliers() bool { return p.Tabs.Suppliers }

// UserFieldsForEdit returns all 10 PNUser fields for the edit form (including empty ones).
func (p Part) UserFieldsForEdit() []struct{ Name, Label, Value string } {
	raw := [10]struct{ name, val string }{
		{"user_field_1", p.UserField1}, {"user_field_2", p.UserField2}, {"user_field_3", p.UserField3},
		{"user_field_4", p.UserField4}, {"user_field_5", p.UserField5}, {"user_field_6", p.UserField6},
		{"user_field_7", p.UserField7}, {"user_field_8", p.UserField8}, {"user_field_9", p.UserField9},
		{"user_field_10", p.UserField10},
	}
	out := make([]struct{ Name, Label, Value string }, 10)
	for i, f := range raw {
		out[i] = struct{ Name, Label, Value string }{f.name, fmt.Sprintf("User %d", i+1), f.val}
	}
	return out
}

// UserFields returns the non-empty user_field_1–10 values with their labels,
// ready for template iteration.
func (p Part) UserFields() []struct{ Label, Value string } {
	raw := [10]string{
		p.UserField1, p.UserField2, p.UserField3, p.UserField4, p.UserField5,
		p.UserField6, p.UserField7, p.UserField8, p.UserField9, p.UserField10,
	}
	var out []struct{ Label, Value string }
	for i, v := range raw {
		if v != "" {
			out = append(out, struct{ Label, Value string }{
				Label: fmt.Sprintf("User %d", i+1),
				Value: v,
			})
		}
	}
	return out
}

type BOMItem struct {
	PLID          int
	PLItem        int
	PLQty         float64
	PLPartID      int
	PLListID      int
	PartNumber    string
	Title         string
	Revision      string
	Category      string
	PNCurrentCost float64
}

type Attachment struct {
	FILID       int
	FILPNID     int
	FILFileName string
	FILPNRev    string
	Category    string
	OrderID     *int
}
