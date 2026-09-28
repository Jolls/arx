// Package purchasing is the purchasing domain (#190, #221): typed access to
// the company, purchase_order and po_line tables via the sqlc-generated
// queries in purchasing.sql. So far the supplier pages (list, detail cards,
// create/update, parts and POs tabs) and the supplier typeahead are converted.
package purchasing

import (
	"context"
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
