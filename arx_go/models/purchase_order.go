package models

import (
	"time"

	"github.com/shopspring/decimal"
)

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
	Tax1                *decimal.Decimal
	ShippingCost        *decimal.Decimal
	MiscCost            *decimal.Decimal
	TotalCost           *decimal.Decimal
	Notes               string
	InternalNotes       string
	RFQGroupID          *int
}

type PurchaseOrderLine struct {
	ID                 int
	POID               int
	LineNumber         int
	PartNumberSnapshot string
	RevisionSnapshot   string
	Description        string
	Qty                decimal.Decimal
	UnitCost           decimal.Decimal
	VendorPN           string
	PartID             *int
	LeadTimeDays       *int
	ReceivedQty        decimal.Decimal
	DateReceived       *time.Time
	IsLotTracked       bool // derived from part.tracking_mode (TracksLots, #745): receiving this line creates a lot row
	PrimaryAtt         *Attachment
	// joined fields
	PONumber     string
	SupplierName string
	DateOrdered  *time.Time
	DateClosed   *time.Time
	Status       string
}
