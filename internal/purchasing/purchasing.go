// Package purchasing is the purchasing domain (#190, #221): typed access to
// the company, purchase_order and po_line tables via the sqlc-generated
// queries in purchasing.sql. So far the supplier pages (list, detail cards,
// create/update, parts and POs tabs), the supplier typeahead and the PO reads
// (grid, CSV export, header, lines, receipts, history, link/price suggestions,
// RFQ comparison grid), PO create/edit writes, the PO lifecycle (status
// transitions, approval, receiving's PO/line statements) and the RFQ writes
// (quote save, convert to PO, create from a BOM) are converted.
package purchasing

import (
	"context"
	"database/sql"
	"time"

	"arx/internal/dbq"
)

// SupplierRow is one row in the /suppliers grid.
type SupplierRow struct {
	ID                int
	Name              string
	SupplierCode      string
	SupplierPartCount int
	POCount           int
	IsActive          bool
	ContactName       string
	ContactCountry    string
}

// Supplier is a company row plus its default contact's name/phone/email/city.
// Create and Update ignore ID, the counts, DateModified, PrimaryAttachmentID
// and the contact fields; Create also ignores the bulk-order options.
type Supplier struct {
	ID                  int
	Name                string
	SupplierCode        string
	Notes               string
	DefaultContact      *int
	IsActive            bool
	IsSupplier          bool
	IsManufacturer      bool
	SupplierPartCount   int
	POCount             int
	DateModified        *time.Time
	PrimaryAttachmentID *int
	BulkOrderDelimiter  string
	BulkOrderPNSource   string
	ContactName         string
	ContactPhone        string
	ContactEmail        string
	ContactCity         string
}

// SupplierMatch is one supplier typeahead hit.
type SupplierMatch struct {
	ID   int
	Name string
	City string
}

// SupplierPO is one of a supplier's POs.
type SupplierPO struct {
	Number      string
	Status      string
	DateOrdered *time.Time
	Total       float64
}

// SupplierPartSummary is one row in the supplier dashboard's "Linked Parts" card.
type SupplierPartSummary struct {
	PartID      int
	PartNumber  string
	Description string
}

// LinkedPart is a supplier_part row plus its part's details and effective
// purchase unit, for the supplier's Parts tab.
type LinkedPart struct {
	ID             int
	PartID         int
	Preference     *int
	SupplierPN     string
	SupplierDesc   string
	LeadTime       string
	MinIncrement   *float64
	PartNumber     string
	Description    string
	Revision       string
	Category       string
	UnitID         *int
	UnitAbbr       string // explicit purchase unit, else the part's base unit
	UnitIsExplicit bool
	ThumbFile      string
}

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// ListSupplierRows returns every company by name, for the /suppliers grid.
func (s *Service) ListSupplierRows(ctx context.Context) ([]SupplierRow, error) {
	rows, err := s.q.ListSupplierRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SupplierRow, len(rows))
	for i, r := range rows {
		out[i] = SupplierRow{ID: r.ID, Name: r.Name, SupplierCode: r.SupplierCode,
			SupplierPartCount: r.SupplierPartCount, POCount: r.PoCount, IsActive: r.IsActive,
			ContactName: r.ContactName, ContactCountry: r.ContactCountry}
	}
	return out, nil
}

// GetSupplier returns one company; sql.ErrNoRows when it doesn't exist.
func (s *Service) GetSupplier(ctx context.Context, id int) (Supplier, error) {
	r, err := s.q.GetSupplier(ctx, id)
	return Supplier{ID: r.ID, Name: r.Name, SupplierCode: r.SupplierCode, Notes: r.Notes,
		DefaultContact: r.DefaultContact, IsActive: r.IsActive, IsSupplier: r.IsSupplier,
		IsManufacturer: r.IsManufacturer, SupplierPartCount: r.SupplierPartCount, POCount: r.PoCount,
		DateModified: r.DateModified, PrimaryAttachmentID: r.PrimaryAttachmentID,
		BulkOrderDelimiter: r.BulkOrderDelimiter, BulkOrderPNSource: r.BulkOrderPnSource,
		ContactName: r.ContactName, ContactPhone: r.ContactPhone, ContactEmail: r.ContactEmail,
		ContactCity: r.ContactCity}, err
}

// CreateSupplier inserts sup and returns the new id.
func (s *Service) CreateSupplier(ctx context.Context, sup Supplier) (int, error) {
	return s.q.CreateSupplier(ctx, dbq.CreateSupplierParams{
		Name: sup.Name, SupplierCode: sup.SupplierCode, DefaultContact: sup.DefaultContact,
		IsActive: sup.IsActive, IsSupplier: sup.IsSupplier, IsManufacturer: sup.IsManufacturer,
		Notes: sup.Notes,
	})
}

// UpdateSupplier overwrites company id with sup's editable fields and bumps date_modified.
func (s *Service) UpdateSupplier(ctx context.Context, id int, sup Supplier) error {
	return s.q.UpdateSupplier(ctx, dbq.UpdateSupplierParams{
		Name: sup.Name, SupplierCode: sup.SupplierCode, DefaultContact: sup.DefaultContact,
		IsActive: sup.IsActive, IsSupplier: sup.IsSupplier, IsManufacturer: sup.IsManufacturer,
		Notes: sup.Notes, BulkOrderDelimiter: sup.BulkOrderDelimiter, BulkOrderPnSource: sup.BulkOrderPNSource,
		ID: id,
	})
}

// SearchSuppliers returns up to n active companies whose name contains q
// (case-sensitive, not LIKE-escaped), by name; supplierOnly drops non-suppliers.
func (s *Service) SearchSuppliers(ctx context.Context, q string, supplierOnly bool, n int) ([]SupplierMatch, error) {
	rows, err := s.q.SearchSuppliers(ctx, dbq.SearchSuppliersParams{Pattern: "%" + q + "%", SupplierOnly: supplierOnly, N: n})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierMatch, len(rows))
	for i, r := range rows {
		out[i] = SupplierMatch(r)
	}
	return out, nil
}

// ListSupplierPOs returns supplierID's POs, most recent first (undated first);
// limit <= 0 means all of them.
func (s *Service) ListSupplierPOs(ctx context.Context, supplierID, limit int) ([]SupplierPO, error) {
	var n *int
	if limit > 0 {
		n = &limit
	}
	rows, err := s.q.ListSupplierPOs(ctx, dbq.ListSupplierPOsParams{SupplierID: supplierID, N: n})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierPO, len(rows))
	for i, r := range rows {
		out[i] = SupplierPO{Number: r.Number, Status: r.Status, DateOrdered: r.DateOrdered, Total: r.TotalCost}
	}
	return out, nil
}

// ListTopSupplierParts returns the first n parts linked to supplierID, by part number.
func (s *Service) ListTopSupplierParts(ctx context.Context, supplierID, n int) ([]SupplierPartSummary, error) {
	rows, err := s.q.ListTopSupplierParts(ctx, dbq.ListTopSupplierPartsParams{SupplierID: supplierID, N: n})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierPartSummary, len(rows))
	for i, r := range rows {
		out[i] = SupplierPartSummary{PartID: r.ID, PartNumber: r.PartNumber, Description: r.Description}
	}
	return out, nil
}

// ListLinkedParts returns supplierID's supplier_part links by part number;
// ThumbFile is the part's active thumbCategory attachment, if any.
func (s *Service) ListLinkedParts(ctx context.Context, supplierID int, thumbCategory string) ([]LinkedPart, error) {
	rows, err := s.q.ListSupplierLinkedParts(ctx, dbq.ListSupplierLinkedPartsParams{SupplierID: supplierID, ThumbCategory: thumbCategory})
	if err != nil {
		return nil, err
	}
	out := make([]LinkedPart, len(rows))
	for i, r := range rows {
		out[i] = LinkedPart{ID: r.ID, PartID: r.PartID, Preference: r.Preference, SupplierPN: r.SupplierPn,
			SupplierDesc: r.SupplierDesc, LeadTime: r.LeadTime, MinIncrement: r.MinIncrement,
			PartNumber: r.PartNumber, Description: r.Description, Revision: r.Revision, Category: r.Category,
			UnitID: r.UomID, UnitAbbr: r.EffectiveUnit, UnitIsExplicit: r.UnitIsExplicit, ThumbFile: r.ThumbFile}
	}
	return out, nil
}

// SupplierPOLinks maps each part on supplierID's POs (RFQ quotes excluded) to
// those PO numbers, descending.
func (s *Service) SupplierPOLinks(ctx context.Context, supplierID int) (map[int][]string, error) {
	rows, err := s.q.ListSupplierPOLinks(ctx, supplierID)
	if err != nil {
		return nil, err
	}
	out := map[int][]string{}
	for _, r := range rows {
		if r.PartID != nil {
			out[*r.PartID] = append(out[*r.PartID], r.Number)
		}
	}
	return out, nil
}

// PORow is one row in the /pos grid.
type PORow struct {
	Number       string
	Status       string
	SupplierID   int
	RFQGroupID   *int
	SupplierName string
	DateOrdered  *time.Time
	DateClosed   *time.Time
	Orderer      string
	Total        float64
}

// POExportRow is one PO line (or a line-less PO, LineNumber nil) for the CSV export.
type POExportRow struct {
	Number       string
	Status       string
	SupplierName string
	DateOrdered  *time.Time
	DateClosed   *time.Time
	Orderer      string
	Total        float64
	LineNumber   *int
	PartNumber   string
	Description  string
	Qty          float64
	UnitCost     float64
	VendorPN     string
}

// PO is a purchase_order row; NULL text reads as "". Its fields match
// models.PurchaseOrder, so arx_go converts it directly.
type PO struct {
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

// POLine is a po_line row plus its part's tracking mode and primary
// attachment (AttID nil when there is none).
type POLine struct {
	ID                 int
	LineNumber         int
	PartNumberSnapshot string
	RevisionSnapshot   string
	Description        string
	Qty                float64
	UnitCost           float64
	VendorPartNumber   string
	PartID             *int
	LeadTimeDays       *int
	ReceivedQty        float64
	DateReceived       *time.Time
	TrackingMode       string
	AttID              *int
	AttFileName        string
	AttCategory        string
}

// POReceipt is one 'receipt' ledger row recorded against a PO line.
type POReceipt struct {
	TxnDate    time.Time
	PartID     *int
	PartNumber string
	Qty        float64
	Username   string
}

// POEvent is one purchase_order_history row.
type POEvent struct {
	EventType  string
	FromStatus string
	ToStatus   string
	Action     string
	Note       string
	ChangedBy  string
	ChangedAt  time.Time
}

// SuggestedLink is a PO line whose vendor part number isn't a supplier_part yet.
type SuggestedLink struct {
	PartID           int
	PartNumber       string
	VendorPartNumber string
}

// SuggestedPrice is a PO line price point no active price covers yet.
type SuggestedPrice struct {
	PartID     int
	PartNumber string
	UnitCost   float64
	Qty        float64
}

// RFQGroupLine is one (quote, line) row of an RFQ group; PolID is nil for a
// quote without lines.
type RFQGroupLine struct {
	Number       string
	SupplierName string
	SupplierID   int
	Status       string
	TotalCost    float64
	PolID        *int
	PartNumber   string
	Revision     string
	Description  string
	Qty          float64
	UnitCost     float64
	LeadTimeDays *int
}

// ListPORows returns every PO, number descending.
func (s *Service) ListPORows(ctx context.Context) ([]PORow, error) {
	rows, err := s.q.ListPORows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PORow, len(rows))
	for i, r := range rows {
		out[i] = PORow{Number: r.Number, Status: r.Status, SupplierID: r.SupplierID, RFQGroupID: r.RfqGroupID,
			SupplierName: r.SupplierName, DateOrdered: r.DateOrdered, DateClosed: r.DateClosed,
			Orderer: r.Orderer, Total: r.TotalCost}
	}
	return out, nil
}

// ListPOExportRows returns every PO line (number descending, then line
// number), with a line-less PO as one row.
func (s *Service) ListPOExportRows(ctx context.Context) ([]POExportRow, error) {
	rows, err := s.q.ListPOExportRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]POExportRow, len(rows))
	for i, r := range rows {
		out[i] = POExportRow{Number: r.Number, Status: r.Status, SupplierName: r.SupplierName,
			DateOrdered: r.DateOrdered, DateClosed: r.DateClosed, Orderer: r.Orderer, Total: r.TotalCost,
			LineNumber: r.LineNumber, PartNumber: r.PartNumber, Description: r.Description, Qty: r.Qty,
			UnitCost: r.UnitCost, VendorPN: r.VendorPartNumber}
	}
	return out, nil
}

// GetPO returns the PO with number; sql.ErrNoRows when there is none.
func (s *Service) GetPO(ctx context.Context, number string) (PO, error) {
	r, err := s.q.GetPO(ctx, number)
	if err != nil {
		return PO{}, err
	}
	return PO{ID: r.ID, Number: r.Number, Status: r.Status, ApprovalStatus: r.ApprovalStatus, IsActive: r.IsActive,
		Orderer: r.Orderer, AccountID: r.AccountID, DateOrdered: r.DateOrdered, DateRequested: r.DateRequested,
		DateClosed: r.DateClosed, DatePrinted: r.DatePrinted, DateModified: r.DateModified,
		SupplierID: &r.SupplierID, SupplierName: r.SupplierName, SupplierContact: r.SupplierContact,
		SupplierContactID: r.SupplierContactID, SupplierEmail: r.SupplierEmail, SupplierAddress: r.SupplierAddress,
		SupplierCity: r.SupplierCity, SupplierState: r.SupplierState, SupplierZipcode: r.SupplierZipcode,
		SupplierCountry: r.SupplierCountry, SupplierPhoneNumber: r.SupplierPhoneNumber,
		SupplierFaxNumber: r.SupplierFaxNumber, ReceiverID: r.ReceiverID, ReceiverName: r.ReceiverName,
		ReceiverContact: r.ReceiverContact, ReceiverContactID: r.ReceiverContactID, ReceiverEmail: r.ReceiverEmail,
		ReceiverAddress: r.ReceiverAddress, ReceiverCity: r.ReceiverCity, ReceiverState: r.ReceiverState,
		ReceiverZipcode: r.ReceiverZipcode, ReceiverCountry: r.ReceiverCountry, ReceiverPhone: r.ReceiverPhone,
		ReceiverFax: r.ReceiverFax, Tax1: r.Tax1, ShippingCost: r.ShippingCost, MiscCost: r.MiscCost,
		TotalCost: r.TotalCost, Notes: r.Notes, InternalNotes: r.InternalNotes, RFQGroupID: r.RfqGroupID}, nil
}

// GetPOSupplierID returns the supplier of the PO with number; sql.ErrNoRows when there is none.
func (s *Service) GetPOSupplierID(ctx context.Context, number string) (int, error) {
	return s.q.GetPOSupplierID(ctx, number)
}

// ListPOLines returns the lines of the PO with number, by line number.
func (s *Service) ListPOLines(ctx context.Context, number string) ([]POLine, error) {
	rows, err := s.q.ListPOLines(ctx, number)
	if err != nil {
		return nil, err
	}
	out := make([]POLine, len(rows))
	for i, r := range rows {
		out[i] = POLine(r)
	}
	return out, nil
}

// ListPOReceipts returns the receipts recorded against poID's lines, newest first.
func (s *Service) ListPOReceipts(ctx context.Context, poID int) ([]POReceipt, error) {
	rows, err := s.q.ListPOReceipts(ctx, poID)
	if err != nil {
		return nil, err
	}
	out := make([]POReceipt, len(rows))
	for i, r := range rows {
		out[i] = POReceipt(r)
	}
	return out, nil
}

// ListPOHistory returns poID's status + approval events, newest first.
func (s *Service) ListPOHistory(ctx context.Context, poID int) ([]POEvent, error) {
	rows, err := s.q.ListPOHistory(ctx, poID)
	if err != nil {
		return nil, err
	}
	out := make([]POEvent, len(rows))
	for i, r := range rows {
		out[i] = POEvent(r)
	}
	return out, nil
}

// ListSuggestedLinks returns the catalog lines on PO number whose vendor part
// number has no supplier_part row for the PO's supplier.
func (s *Service) ListSuggestedLinks(ctx context.Context, number string) ([]SuggestedLink, error) {
	rows, err := s.q.ListSuggestedLinks(ctx, number)
	if err != nil {
		return nil, err
	}
	out := make([]SuggestedLink, len(rows))
	for i, r := range rows {
		out[i] = SuggestedLink(r)
	}
	return out, nil
}

// ListSuggestedPrices returns the distinct catalog-line price points on PO
// number that no active price of the PO's supplier covers at or below the line qty.
func (s *Service) ListSuggestedPrices(ctx context.Context, number string) ([]SuggestedPrice, error) {
	rows, err := s.q.ListSuggestedPrices(ctx, number)
	if err != nil {
		return nil, err
	}
	out := make([]SuggestedPrice, len(rows))
	for i, r := range rows {
		out[i] = SuggestedPrice(r)
	}
	return out, nil
}

// ListRFQGroupLines returns RFQ group groupID's (quote, line) rows, quotes in id order.
func (s *Service) ListRFQGroupLines(ctx context.Context, groupID int) ([]RFQGroupLine, error) {
	rows, err := s.q.ListRFQGroupLines(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]RFQGroupLine, len(rows))
	for i, r := range rows {
		out[i] = RFQGroupLine(r)
	}
	return out, nil
}

// POState is a PO's id and workflow state; NULLs read as "".
type POState struct {
	ID             int
	Status         string
	ApprovalStatus string
}

// GetPOState returns the PO with number's state; sql.ErrNoRows when there is none.
func (s *Service) GetPOState(ctx context.Context, number string) (POState, error) {
	r, err := s.q.GetPOState(ctx, number)
	return POState(r), err
}

// GetPONumber returns PO id's number; sql.ErrNoRows when there is none.
func (s *Service) GetPONumber(ctx context.Context, id int) (string, error) {
	return s.q.GetPONumber(ctx, id)
}

// CountRFQQuotes returns how many quotes RFQ group groupID has.
func (s *Service) CountRFQQuotes(ctx context.Context, groupID int) (int, error) {
	return s.q.CountRFQQuotes(ctx, groupID)
}

// NextPONumber takes the next po_number_seq value. Call it outside the PO's
// transaction: the sequence never rolls back, so a failed PO never reuses it.
func (s *Service) NextPONumber(ctx context.Context) (string, error) {
	return s.q.NextPONumber(ctx)
}

// CreatePO inserts po (number, status, is_active and the editable header
// fields; DatePrinted is ignored) with a zero total and returns its id.
func (s *Service) CreatePO(ctx context.Context, po PO) (int, error) {
	return s.q.CreatePO(ctx, dbq.CreatePOParams{
		Number: po.Number, Status: po.Status, IsActive: po.IsActive, Orderer: po.Orderer, AccountID: po.AccountID,
		SupplierID: po.SupplierID, SupplierName: po.SupplierName, SupplierContact: po.SupplierContact,
		SupplierEmail: po.SupplierEmail, SupplierAddress: po.SupplierAddress, SupplierCity: po.SupplierCity,
		SupplierState: po.SupplierState, SupplierZipcode: po.SupplierZipcode, SupplierCountry: po.SupplierCountry,
		SupplierPhoneNumber: po.SupplierPhoneNumber, SupplierFaxNumber: po.SupplierFaxNumber,
		ReceiverID: po.ReceiverID, ReceiverName: po.ReceiverName, ReceiverContact: po.ReceiverContact,
		ReceiverEmail: po.ReceiverEmail, ReceiverAddress: po.ReceiverAddress, ReceiverCity: po.ReceiverCity,
		ReceiverState: po.ReceiverState, ReceiverZipcode: po.ReceiverZipcode, ReceiverCountry: po.ReceiverCountry,
		ReceiverPhone: po.ReceiverPhone, ReceiverFax: po.ReceiverFax,
		Tax1: po.Tax1, ShippingCost: po.ShippingCost, MiscCost: po.MiscCost, Notes: po.Notes, InternalNotes: po.InternalNotes,
		DateOrdered: po.DateOrdered, DateRequested: po.DateRequested, DateClosed: po.DateClosed,
		SupplierContactID: po.SupplierContactID, ReceiverContactID: po.ReceiverContactID,
	})
}

// UpdatePOHeader overwrites the editable header fields of PO number with po's
// (including DatePrinted), sets total_cost and bumps date_modified. Status,
// is_active and approval are left alone.
func (s *Service) UpdatePOHeader(ctx context.Context, number string, total float64, po PO) error {
	return s.q.UpdatePOHeader(ctx, dbq.UpdatePOHeaderParams{
		Orderer: po.Orderer, AccountID: po.AccountID,
		SupplierID: po.SupplierID, SupplierName: po.SupplierName, SupplierContact: po.SupplierContact,
		SupplierEmail: po.SupplierEmail, SupplierAddress: po.SupplierAddress, SupplierCity: po.SupplierCity,
		SupplierState: po.SupplierState, SupplierZipcode: po.SupplierZipcode, SupplierCountry: po.SupplierCountry,
		SupplierPhoneNumber: po.SupplierPhoneNumber, SupplierFaxNumber: po.SupplierFaxNumber,
		ReceiverID: po.ReceiverID, ReceiverName: po.ReceiverName, ReceiverContact: po.ReceiverContact,
		ReceiverEmail: po.ReceiverEmail, ReceiverAddress: po.ReceiverAddress, ReceiverCity: po.ReceiverCity,
		ReceiverState: po.ReceiverState, ReceiverZipcode: po.ReceiverZipcode, ReceiverCountry: po.ReceiverCountry,
		ReceiverPhone: po.ReceiverPhone, ReceiverFax: po.ReceiverFax,
		Tax1: po.Tax1, ShippingCost: po.ShippingCost, MiscCost: po.MiscCost, Notes: po.Notes, InternalNotes: po.InternalNotes,
		DateOrdered: po.DateOrdered, DateRequested: po.DateRequested, DateClosed: po.DateClosed, DatePrinted: po.DatePrinted,
		TotalCost: total, SupplierContactID: po.SupplierContactID, ReceiverContactID: po.ReceiverContactID,
		Number: number,
	})
}

// SetPOTotal sets PO id's total_cost.
func (s *Service) SetPOTotal(ctx context.Context, id int, total float64) error {
	return s.q.SetPOTotal(ctx, dbq.SetPOTotalParams{TotalCost: total, ID: id})
}

// SetRFQGroup puts PO id in RFQ group groupID.
func (s *Service) SetRFQGroup(ctx context.Context, id, groupID int) error {
	return s.q.SetRFQGroup(ctx, dbq.SetRFQGroupParams{GroupID: groupID, ID: id})
}

// MarkPOPrinted sets PO number's date_printed to on (YYYY-MM-DD).
func (s *Service) MarkPOPrinted(ctx context.Context, number, on string) error {
	return s.q.MarkPOPrinted(ctx, dbq.MarkPOPrintedParams{PrintedOn: on, Number: number})
}

// CreatePOStatusEvent records a status change on poID; from is nil on creation.
func (s *Service) CreatePOStatusEvent(ctx context.Context, poID int, from *string, to, changedBy string) error {
	var f sql.NullString
	if from != nil {
		f = sql.NullString{String: *from, Valid: true}
	}
	return s.q.CreatePOStatusEvent(ctx, dbq.CreatePOStatusEventParams{PoID: poID, FromStatus: f, ToStatus: to, ChangedBy: changedBy})
}

// CreatePOLine adds l to poID. Only the line number, snapshots, description,
// qty, unit cost, vendor PN and part id are written.
func (s *Service) CreatePOLine(ctx context.Context, poID int, l POLine) error {
	return s.q.CreatePOLine(ctx, dbq.CreatePOLineParams{PoID: poID, LineNumber: l.LineNumber,
		PartNumber: l.PartNumberSnapshot, Revision: l.RevisionSnapshot, Description: l.Description,
		Qty: l.Qty, UnitCost: l.UnitCost, VendorPartNumber: l.VendorPartNumber, PartID: l.PartID})
}

// UpdatePOLine overwrites line l.ID of poID with the fields CreatePOLine writes;
// a line on another PO is left alone.
func (s *Service) UpdatePOLine(ctx context.Context, poID int, l POLine) error {
	return s.q.UpdatePOLine(ctx, dbq.UpdatePOLineParams{LineNumber: l.LineNumber,
		PartNumber: l.PartNumberSnapshot, Revision: l.RevisionSnapshot, Description: l.Description,
		Qty: l.Qty, UnitCost: l.UnitCost, VendorPartNumber: l.VendorPartNumber, PartID: l.PartID,
		ID: l.ID, PoID: poID})
}

// DeletePOLine deletes line id if it belongs to poID.
func (s *Service) DeletePOLine(ctx context.Context, poID, id int) error {
	return s.q.DeletePOLine(ctx, dbq.DeletePOLineParams{ID: id, PoID: poID})
}

// SetPOStatus moves PO id from from to to (with isActive) and bumps
// date_modified; closing sets date_closed if unset, reopening from closed clears it.
func (s *Service) SetPOStatus(ctx context.Context, id int, from, to string, isActive bool) error {
	return s.q.SetPOStatus(ctx, dbq.SetPOStatusParams{ToStatus: to, IsActive: isActive, FromStatus: from, ID: id})
}

// SetPOApproval sets PO id's approval_status.
func (s *Service) SetPOApproval(ctx context.Context, id int, approval string) error {
	return s.q.SetPOApproval(ctx, dbq.SetPOApprovalParams{ApprovalStatus: approval, ID: id})
}

// CreatePOApprovalEvent records an approval action on poID; a nil note is stored as NULL.
func (s *Service) CreatePOApprovalEvent(ctx context.Context, poID int, action string, note *string, changedBy string) error {
	var n sql.NullString
	if note != nil {
		n = sql.NullString{String: *note, Valid: true}
	}
	return s.q.CreatePOApprovalEvent(ctx, dbq.CreatePOApprovalEventParams{PoID: poID, Action: action, Note: n, ChangedBy: changedBy})
}

// LockPOStatus locks PO id's row for the rest of the transaction and returns its status.
func (s *Service) LockPOStatus(ctx context.Context, id int) (string, error) {
	return s.q.LockPOStatus(ctx, id)
}

// ReceivePOLine adds qty to line id's received_qty and sets date_received to on.
func (s *Service) ReceivePOLine(ctx context.Context, id int, qty float64, on time.Time) error {
	return s.q.ReceivePOLine(ctx, dbq.ReceivePOLineParams{Qty: qty, DateReceived: &on, ID: id})
}

// ListPOLineQtys returns poID's lines with only Qty and ReceivedQty set.
func (s *Service) ListPOLineQtys(ctx context.Context, poID int) ([]POLine, error) {
	rows, err := s.q.ListPOLineQtys(ctx, poID)
	if err != nil {
		return nil, err
	}
	out := make([]POLine, len(rows))
	for i, r := range rows {
		out[i] = POLine{Qty: r.Qty, ReceivedQty: r.ReceivedQty}
	}
	return out, nil
}

// SumPOLines returns the sum of qty × unit cost over poID's lines.
func (s *Service) SumPOLines(ctx context.Context, poID int) (float64, error) {
	return s.q.SumPOLines(ctx, poID)
}

// RFQQuote is a quote's id, status and links; a NULL status reads as "".
type RFQQuote struct {
	ID         int
	Status     string
	RFQGroupID *int
	SupplierID int
}

// GetRFQQuote returns the PO with number's id, status and links; sql.ErrNoRows when there is none.
func (s *Service) GetRFQQuote(ctx context.Context, number string) (RFQQuote, error) {
	r, err := s.q.GetRFQQuote(ctx, number)
	return RFQQuote{ID: r.ID, Status: r.Status, RFQGroupID: r.RfqGroupID, SupplierID: r.SupplierID}, err
}

// ListRFQLineIDs returns the ids of every line of every quote in RFQ group groupID.
func (s *Service) ListRFQLineIDs(ctx context.Context, groupID int) ([]int, error) {
	return s.q.ListRFQLineIDs(ctx, groupID)
}

// SetRFQLineQuote sets line id's quoted unit cost and lead time (nil clears it).
func (s *Service) SetRFQLineQuote(ctx context.Context, id int, unitCost float64, leadDays *int) error {
	return s.q.SetRFQLineQuote(ctx, dbq.SetRFQLineQuoteParams{UnitCost: unitCost, LeadTimeDays: leadDays, ID: id})
}

// RecomputeRFQTotals sets every quote's total in group groupID to its line sum plus its own
// tax/shipping/misc.
func (s *Service) RecomputeRFQTotals(ctx context.Context, groupID int) error {
	return s.q.RecomputeRFQTotals(ctx, groupID)
}

// LockRFQGroup locks group groupID's quotes in id order for the rest of the transaction.
func (s *Service) LockRFQGroup(ctx context.Context, groupID int) error {
	_, err := s.q.LockRFQGroup(ctx, groupID)
	return err
}

// AwardRFQQuote closes quote id out as the winner; false when it is no longer in 'rfq'.
func (s *Service) AwardRFQQuote(ctx context.Context, id int) (bool, error) {
	n, err := s.q.AwardRFQQuote(ctx, id)
	return n > 0, err
}

// DeclineRFQQuote cancels quote id; false when it is no longer in 'rfq' (left untouched).
func (s *Service) DeclineRFQQuote(ctx context.Context, id int) (bool, error) {
	n, err := s.q.DeclineRFQQuote(ctx, id)
	return n > 0, err
}

// ListOpenRFQSiblings returns the ids of group groupID's quotes other than exceptID still in 'rfq'.
func (s *Service) ListOpenRFQSiblings(ctx context.Context, groupID, exceptID int) ([]int, error) {
	return s.q.ListOpenRFQSiblings(ctx, dbq.ListOpenRFQSiblingsParams{GroupID: groupID, ID: exceptID})
}

// CopyPOForConversion duplicates PO sourceID's header as a draft PO at number (no RFQ group,
// ordered today, approval not submitted) and returns its id.
func (s *Service) CopyPOForConversion(ctx context.Context, sourceID int, number string) (int, error) {
	return s.q.CopyPOForConversion(ctx, dbq.CopyPOForConversionParams{Number: number, SourceID: sourceID})
}

// CopyPOLines copies PO sourceID's lines onto PO poID.
func (s *Service) CopyPOLines(ctx context.Context, sourceID, poID int) error {
	return s.q.CopyPOLines(ctx, dbq.CopyPOLinesParams{PoID: poID, SourceID: sourceID})
}

// CreatePOStatusEventNote is CreatePOStatusEvent with a note.
func (s *Service) CreatePOStatusEventNote(ctx context.Context, poID int, from *string, to, note, changedBy string) error {
	var f sql.NullString
	if from != nil {
		f = sql.NullString{String: *from, Valid: true}
	}
	return s.q.CreatePOStatusEventNote(ctx, dbq.CreatePOStatusEventNoteParams{PoID: poID, FromStatus: f, ToStatus: to, Note: note, ChangedBy: changedBy})
}
