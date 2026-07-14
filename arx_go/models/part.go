package models

import (
	"fmt"
	"time"
)

type Part struct {
	ID                  int
	PartNumber          string
	Revision            string
	Title               string
	Detail              string
	Category            string
	HasBOM              bool
	ReleaseStatus       string
	IsActive            bool
	RequestedBy         string
	Notes               string
	CreatedDate         *time.Time
	ModifiedDate        *time.Time
	PrimaryAttachmentID int
	StockOnHand         float64
	ReorderMin          *float64 // reorder point (#273); nil = none set, never flagged below-min
	IsLotTracked        bool     // lot/batch control (#676); receipt/build create a lot row when true
	CurrentCost         float64
	LastRollupCost      float64
	LastRollupAt        *time.Time
	AttachmentCount     int
	POLineCount         int
	DefaultSupplierID   *int // preferred supplier for cost rollup (#465); nil = none pinned
	UserField1          string
	UserField2          string
	UserField3          string
	UserField4          string
	UserField5          string
	UserField6          string
	UserField7          string
	UserField8          string
	UserField9          string
	UserField10         string
	UnitID              *int
	UnitAbbr            string       // joined from unit table; abbreviation of unit_id
	Tabs                CategoryTabs // resolved subtab visibility for Category; set by applyCategoryTabs
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
	Inventory bool `json:"inventory"`
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
var DefaultCategoryTabs = CategoryTabs{Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true}

// DefaultCategories is the seed list shown until an admin customizes it in
// Settings. It reproduces the original hardcoded behavior.
func DefaultCategories() []Category {
	proc := DefaultCategoryTabs                                                                                    // purchased + stocked: procurement tabs + inventory, BOM data-driven
	built := CategoryTabs{BOM: true, Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true} // made + stocked
	svc := proc                                                                                                    // purchased but not stocked (service, tooling)
	svc.Inventory = false
	return []Category{
		{"ASM", "Assembly", built},
		{"BUY", "Purchased", proc},
		{"DWG", "Drawing", CategoryTabs{}},
		{"DOC", "Document", CategoryTabs{}},
		{"FORM", "Test Form", CategoryTabs{BOM: true}},
		{"MFG", "Manufactured", built},
		{"OPS", "Operation / Labor", CategoryTabs{}},
		{"RAW", "Raw Material", proc},
		{"SVC", "Service", svc},
		{"TOOL", "Tooling", svc},
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

// BaseNumberConfig configures how the next-available "base number" is
// suggested for a new part number. Stored as JSON in app_config (key
// part_numbering); when nothing is saved, DefaultBaseNumberConfig is used.
// It parses existing part_number values by splitting on Separator and
// reading the segment at SegmentIndex (0-based) as an integer.
type BaseNumberConfig struct {
	Separator    string `json:"separator"`
	SegmentIndex int    `json:"segmentIndex"`
	Width        int    `json:"width"`
	Mode         string `json:"mode"` // "max_plus_one" | "next_open_after"
	Floor        int    `json:"floor"`
}

// DefaultBaseNumberConfig reproduces this shop's current xxx-yyyyy-zz
// convention: the base number is the second dash-separated segment,
// zero-padded to 5 digits, suggested as max(existing)+1.
func DefaultBaseNumberConfig() BaseNumberConfig {
	return BaseNumberConfig{Separator: "-", SegmentIndex: 1, Width: 5, Mode: "max_plus_one"}
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
func (p Part) ShowInventory() bool { return p.Tabs.Inventory }

// ShowBuild reports whether the Build subtab applies: the part has actual BOM
// lines to consume (#675) AND its category is inventory-tracked (building
// produces stock). This excludes FORM parts, whose BOM row is only the
// record-picker convention and whose category is not stocked.
func (p Part) ShowBuild() bool { return p.HasBOM && p.ShowInventory() }

// ShowLots reports whether the Lots subtab applies: the part is lot/batch
// controlled (#676), so it has (or will have) lot rows to list and trace.
func (p Part) ShowLots() bool { return p.IsLotTracked }

// BelowReorder reports whether on-hand stock has dropped below the part's reorder
// point (#273). False when no reorder point is set (ReorderMin == nil).
func (p Part) BelowReorder() bool {
	return p.ReorderMin != nil && p.StockOnHand < *p.ReorderMin
}

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
	ID              int
	LineNumber      int
	Qty             float64
	ComponentPartID int
	ParentPartID    int
	PartNumber      string
	Title           string
	Revision        string
	Category        string
	CurrentCost     float64
	AttachCount     int
	POLineCount     int
	// Rollup display fields — populated by PartBOM handler.
	LastRollupCost float64
	ChildHasBOM    bool
	LineUnitCost   float64
	LineExtCost    float64
	CostSource     string // "rollup" | "price" | "labor" | "current_cost" | "missing"
}

type Attachment struct {
	ID           int
	PartID       int
	FileName     string
	PartRevision string
	Category     string
	OrderID      *int
	Comment      string
}
