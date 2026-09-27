// Package parts is the parts domain (#190, #220): typed access to the part
// tables via the sqlc-generated queries in parts.sql. So far part categories
// and manufacturer parts are converted.
package parts

import (
	"context"

	"arx/internal/dbq"
)

// Category is a part_category row: a code, a display label, the Purchased
// flag, and the optional subtabs it shows.
type Category struct {
	Code      string
	Label     string
	Purchased bool
	BOM       bool
	Orders    bool
	Pricing   bool
	MfgParts  bool
	Suppliers bool
	Inventory bool
}

// MfgPart is a mfg_part row plus its manufacturer's name.
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

// Manufacturer is an active company flagged is_manufacturer.
type Manufacturer struct {
	ID   int
	Name string
}

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// ListCategories returns part_category in editor order (#194).
func (s *Service) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.q.ListPartCategories(ctx)
	if err != nil {
		return nil, err
	}
	cats := make([]Category, 0, len(rows))
	for _, r := range rows {
		cats = append(cats, Category{
			Code: r.Code, Label: r.Label, Purchased: r.IsPurchased,
			BOM: r.IsBomVisible, Orders: r.IsOrdersVisible, Pricing: r.IsPricingVisible,
			MfgParts: r.IsMfgPartsVisible, Suppliers: r.IsSuppliersVisible, Inventory: r.IsInventoryVisible,
		})
	}
	return cats, nil
}

// CategoryUsage counts parts per category code.
func (s *Service) CategoryUsage(ctx context.Context) (map[string]int, error) {
	rows, err := s.q.CountPartsByCategory(ctx)
	if err != nil {
		return nil, err
	}
	usage := make(map[string]int, len(rows))
	for _, r := range rows {
		usage[r.Category] = int(r.PartCount)
	}
	return usage, nil
}

// SaveCategories upserts cats (in order) and deletes codes not in cats. Run it
// on a transaction; FK_part_category rejects deleting a code a part still uses.
func (s *Service) SaveCategories(ctx context.Context, cats []Category) error {
	existing, err := s.q.ListPartCategoryCodes(ctx)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for i, c := range cats {
		keep[c.Code] = true
		if err := s.q.UpsertPartCategory(ctx, dbq.UpsertPartCategoryParams{
			Code: c.Code, Label: c.Label, IsPurchased: c.Purchased,
			IsBomVisible: c.BOM, IsOrdersVisible: c.Orders, IsPricingVisible: c.Pricing,
			IsMfgPartsVisible: c.MfgParts, IsSuppliersVisible: c.Suppliers, IsInventoryVisible: c.Inventory,
			SortOrder: i,
		}); err != nil {
			return err
		}
	}
	for _, code := range existing {
		if !keep[code] {
			if err := s.q.DeletePartCategory(ctx, code); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListMfgParts returns a part's active manufacturer parts, by manufacturer name then MPN.
func (s *Service) ListMfgParts(ctx context.Context, partID int) ([]MfgPart, error) {
	rows, err := s.q.ListMfgParts(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]MfgPart, 0, len(rows))
	for _, r := range rows {
		// ListMfgParts selects MfgPart's fields in order, so the row converts directly.
		out = append(out, MfgPart(r))
	}
	return out, nil
}

// GetMfgPart returns one active manufacturer part of partID (IsActive and
// MfgName unset); sql.ErrNoRows when it doesn't exist or belongs to another part.
func (s *Service) GetMfgPart(ctx context.Context, id, partID int) (MfgPart, error) {
	r, err := s.q.GetMfgPart(ctx, dbq.GetMfgPartParams{ID: id, PartID: partID})
	return MfgPart{
		ID: r.ID, PartID: r.PartID, MfgID: r.MfgID,
		MfgPartNumber: r.MfgPartNumber, Description: r.Description,
	}, err
}

// CreateMfgPart inserts an active mfg_part from mp's PartID, MfgID, MfgPartNumber and Description.
func (s *Service) CreateMfgPart(ctx context.Context, mp MfgPart) error {
	return s.q.CreateMfgPart(ctx, dbq.CreateMfgPartParams{
		PartID: mp.PartID, MfgID: mp.MfgID, MfgPartNumber: mp.MfgPartNumber, Description: mp.Description,
	})
}

// UpdateMfgPart rewrites mp's manufacturer, MPN and description. A row that
// isn't active under mp.PartID is left alone without error.
func (s *Service) UpdateMfgPart(ctx context.Context, mp MfgPart) error {
	return s.q.UpdateMfgPart(ctx, dbq.UpdateMfgPartParams{
		MfgID: mp.MfgID, MfgPartNumber: mp.MfgPartNumber, Description: mp.Description,
		ID: mp.ID, PartID: mp.PartID,
	})
}

// DeleteMfgPart soft-deletes manufacturer part id of partID.
func (s *Service) DeleteMfgPart(ctx context.Context, id, partID int) error {
	return s.q.DeleteMfgPart(ctx, dbq.DeleteMfgPartParams{ID: id, PartID: partID})
}

// ListManufacturers returns active manufacturers by name, for pickers.
func (s *Service) ListManufacturers(ctx context.Context) ([]Manufacturer, error) {
	rows, err := s.q.ListManufacturers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Manufacturer, 0, len(rows))
	for _, r := range rows {
		out = append(out, Manufacturer(r))
	}
	return out, nil
}
