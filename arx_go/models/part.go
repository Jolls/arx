package models

import (
	"fmt"
	"time"
)

type Part struct {
	ID                  int
	PartNumber          string
	Revision            string
	Description         string
	Detail              string
	Category            string
	HasBOM              bool
	ReleaseStatus       string
	IsActive            bool
	RequestedBy         string
	Notes               string
	CreatedDate         *time.Time
	ModifiedDate        *time.Time
	PrimaryAttachmentID *int // part_attachment.id of the primary attachment; nil = none set
	StockOnHand         float64
	ReorderMin          *float64 // reorder point (#273); nil = none set, never flagged below-min
	IsLotTracked        bool     // derived from TrackingMode via TracksLots (#745); not a DB column
	TrackingMode        string   // lot/serial control (#743): none|lot|serial|lot_serial; drives reads as of slice 8 (#745)
	CurrentCost         float64
	LastRollupCost      float64
	LastRollupAt        *time.Time
	AttachmentCount     int
	ThumbnailURL        string // resolved URL of the active "Thumbnail"-category attachment, if any (#56); empty when none generated
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
	UnitAbbr            string       // joined from uom table; abbreviation of uom_id
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
// subtabs it exposes. Stored in the part_category table (#194); DefaultCategories
// is the fallback when no DB is connected.
type Category struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	// Purchased marks parts bought from a vendor: "Create RFQs" (#99) orders
	// them rather than exploding their BOM. Absent in older saved JSON = false.
	Purchased bool `json:"purchased"`
	CategoryTabs
}

// DefaultCategoryTabs is the permissive fallback for categories that aren't in
// the configured list (and empty/unknown codes): every procurement tab shown,
// BOM left data-driven. We only hide tabs for categories explicitly configured.
var DefaultCategoryTabs = CategoryTabs{Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true}

// DefaultCategories is the built-in list used when no DB is connected; it
// mirrors the part_category seed rows and reproduces the original hardcoded behavior.
func DefaultCategories() []Category {
	proc := DefaultCategoryTabs                                                                                     // purchased + stocked: procurement tabs + inventory, BOM data-driven
	built := CategoryTabs{BOM: true, Orders: true, Pricing: true, MfgParts: true, Suppliers: true, Inventory: true} // made + stocked
	svc := proc                                                                                                     // purchased but not stocked (service, tooling)
	svc.Inventory = false
	return []Category{
		{"ASM", "Assembly", false, built},
		{"BUY", "Purchased", true, proc},
		{"DWG", "Drawing", false, CategoryTabs{}},
		{"DOC", "Document", false, CategoryTabs{}},
		{"FORM", "Test Form", false, CategoryTabs{BOM: true}},
		{"MFG", "Manufactured", false, built},
		{"OPS", "Operation / Labor", false, CategoryTabs{}},
		{"RAW", "Raw Material", true, proc},
		{"SVC", "Service", true, svc},
		{"TOOL", "Tooling", true, svc},
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

// ShowUnits reports whether the Units subtab applies: the part is serial-tracked
// (#736 slice 9), so it has (or will have) serialized unit rows to list and trace.
func (p Part) ShowUnits() bool { return TracksSerials(p.TrackingMode) }

// TracksLots reports whether a tracking_mode value implies lot/batch control:
// receipt/build create a lot row.
func TracksLots(mode string) bool { return mode == "lot" || mode == "lot_serial" }

// TracksSerials reports whether a tracking_mode value implies serialized units:
// a unit row is created lazily at test time (#745, Q5).
func TracksSerials(mode string) bool { return mode == "serial" || mode == "lot_serial" }

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
	Description     string
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
	// Vendor scope (#56): "s:<supplier_part_id>" or "m:<mfg_part_id>", empty when the
	// attachment is part-level. VendorName is the joined supplier/manufacturer name.
	VendorScope string
	VendorName  string
}
