package models

import "time"

type Supplier struct {
	ID                   int
	Name                 string
	SUSupplierCode       string
	SUNotes              string
	IsActive             bool
	IsSupplier           bool
	IsManufacturer       bool
	DefaultContact       *int
	DateModified         *time.Time
	SUNumOfLNKs          int
	SUNumOfPOs           int
	PrimaryAttachmentID  *int
	// joined fields (contact)
	CNName    string
	CNWeb     string
	CNAddress string
	CNCity    string
	CNState   string
	CNZipcode string
	CNCountry string
	CNPhone1  string
	CNEmail   string
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
	Preference   string
	SupplierPN   string
	SupplierDesc string
	LeadTime     string
	MinIncrement *float64
	// joined — part info (supplier parts view)
	PNID       int
	PartNumber string
	Title      string
	Revision   string
	Category   string
	// joined — supplier + mfg info (part sourcing view)
	SupplierName  string
	MfgPartID     *int
	MfgPartNumber string
	MfgName       string
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
