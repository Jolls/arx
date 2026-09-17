package models

import "time"

type Supplier struct {
	ID                  int
	Name                string
	SUSupplierCode      string
	SUNotes             string
	IsActive            bool
	IsSupplier          bool
	IsManufacturer      bool
	DefaultContact      *int
	DateModified        *time.Time
	SUNumOfLNKs         int
	SUNumOfPOs          int
	PrimaryAttachmentID *int
	BulkOrderDelimiter  string // "comma" | "tab" | "newline" — PO "Copy for Ordering" clipboard format (#80)
	BulkOrderPNSource   string // "internal" | "vendor" — which PN the bulk-order copy uses (#80)
	// joined fields (contact)
	DisplayName string
	Website     string
	Address     string
	City        string
	State       string
	Zipcode     string
	Country     string
	Phone1      string
	Email       string
}

type SupplierAttachment struct {
	SupplierAttachmentID int
	SupplierID           int
	FilePath             string
	Notes                string
	SortOrder            *int
}

type SupplierPart struct {
	ID           int
	SupplierID   int
	PartID       int
	Preference   *int
	SupplierPN   string
	SupplierDesc string
	LeadTime     string
	MinIncrement *float64
	// joined — part info (supplier parts view)
	PartNumber  string
	Description string
	Revision    string
	Category    string
	Thumb       string // joined — /parts-style hover thumbnail URL (#63), empty when part has none
	// joined — supplier + mfg info (part sourcing view)
	SupplierName           string
	MfgPartID              *int
	MfgPartNumber          string
	MfgName                string
	UnitID                 *int
	PurchaseUnitAbbr       string   // joined from uom table; COALESCE(purchase unit, part base unit)
	PurchaseUnitIsExplicit bool     // true = uom_id set on supplier_part; false = inherited from part.uom_id
	POLinks                []string // populated post-query — PO numbers placed with this vendor for the part (supplier parts view)
}

type MfgPart struct {
	ID            int
	PartID        int
	MfgID         int
	MfgPartNumber string
	Description   string
	IsActive      bool
	// joined
	MfgName string
}
