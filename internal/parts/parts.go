// Package parts is the parts domain (#190, #220): typed access to the part
// tables via the sqlc-generated queries in parts.sql. So far only part
// categories are converted.
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
