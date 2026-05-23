package models

import "time"

type Supplier struct {
	ID                   int
	Name                 string
	SUSupplierCode       string
	SUNotes              string
	IsActive             bool
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

type SupplierLink struct {
	LNKID          int
	LNKPNID        int
	LNKSUID        int
	LNKChoice      string
	LNKVendorPN    string
	LNKVendorDesc  string
	LNKLeadtime    string
	LNKCurrentCost *float64
	LNKAtQty       *float64
	LNKMinIncrement *float64
	LNKUse         bool
	LNKRFQDate     *time.Time
	// joined fields
	PNID         int
	PNPartNumber string
	PNTitle      string
	Revision     string
	Category     string
}
