package models

import "time"

type PurchaseOrder struct {
	ID                  int
	Number              string
	Status              string
	ApprovalStatus      string
	IsActive            bool
	Orderer             string
	AccountID           string
	DateOrdered         *time.Time
	DateRequested       *time.Time
	DateClosed          *time.Time
	DatePrinted         *time.Time
	DateModified        *time.Time
	SupplierID          *int
	SupplierName        string
	SupplierContact     string
	SupplierContactID   *int
	SupplierEmail       string
	SupplierAddress     string
	SupplierCity        string
	SupplierState       string
	SupplierZipcode     string
	SupplierCountry     string
	SupplierPhoneNumber string
	SupplierFaxNumber   string
	ReceiverID          *int
	ReceiverName        string
	ReceiverContact     string
	ReceiverContactID   *int
	ReceiverEmail       string
	ReceiverAddress     string
	ReceiverCity        string
	ReceiverState       string
	ReceiverZipcode     string
	ReceiverCountry     string
	ReceiverPhone       string
	ReceiverFax         string
	Tax1                *float64
	ShippingCost        *float64
	MiscCost            *float64
	TotalCost           *float64
	Notes               string
	InternalNotes       string
	RFQGroupID          *int
}

type PurchaseOrderLine struct {
	POLID           int
	POLPOID         int
	POLItem         int
	POLPNPartNumber string
	POLRev          string
	POLDesc         string
	POLQty          float64
	POLCost         float64
	VendorPN        string
	POLPNID         *int
	LeadTimeDays    *int
	ReceivedQty     float64
	DateReceived    *time.Time
	IsLotTracked    bool // part.is_lot_tracked (#676): receiving this line creates a lot row
	PrimaryAtt      *Attachment
	// joined fields
	PONumber     string
	SupplierName string
	DateOrdered  *time.Time
	DateClosed   *time.Time
	Status       string
}
