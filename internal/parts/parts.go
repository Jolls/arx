// Package parts is the parts domain (#190, #220): typed access to the part
// tables via the sqlc-generated queries in parts.sql. So far part categories,
// manufacturer parts, sourcing (supplier links, their prices and the
// DigiKey import), the RFQ planner's BOM reads, the part-number list, the
// parts list/CSV export and the single-part read/create/update are converted.
package parts

import (
	"context"
	"database/sql"
	"errors"
	"time"

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

// SupplierPart is a supplier_part row (a supplier link) plus its supplier's name.
type SupplierPart struct {
	ID           int
	SupplierID   int
	PartID       int
	Preference   *int
	SupplierPN   string
	SupplierDesc string
	LeadTime     string
	MinIncrement *float64
	UnitID       *int
	// joined
	SupplierName string
	// ListSupplierParts only: the link's own unit, else the part's base unit.
	PurchaseUnitAbbr       string
	PurchaseUnitIsExplicit bool
}

// Price is an active price row of a part.
type Price struct {
	SupplierID    int
	PriceEA       *float64
	PricePack     *float64
	PackSize      *float64
	EffectiveDate *time.Time
}

// BOMComponent is one bom line below a parent, with the component's part data.
type BOMComponent struct {
	ID          int
	Qty         float64
	PartNumber  string
	Description string
	Revision    string
	Category    string
	Stock       float64
	ReorderMin  *float64
	SupplierID  *int
	HasBOM      bool
}

// ListedPart is one part row of the /parts grid and CSV export; NULL text
// reads as "", NULL counts as 0, NULL is_active as active.
type ListedPart struct {
	ID              int
	PartNumber      string
	Revision        string
	Description     string
	Detail          string
	RequestedBy     string
	Category        string
	CreatedDate     *time.Time
	ModifiedDate    *time.Time
	IsActive        bool
	AttachmentCount int
	POLineCount     int
	BelowMin        bool
	ThumbFile       string
}

// PartBasic is the part header behind every part sub-tab page.
type PartBasic struct {
	ID                  int
	PartNumber          string
	Description         string
	Category            string
	HasBOM              bool
	PrimaryAttachmentID *int
	StockOnHand         float64
	TrackingMode        string
	ThumbFile           string
}

// Part is a whole part row plus its unit abbreviation; NULL text reads as "",
// NULL is_active as inactive, NULL costs and counts as 0. It is also the
// create/update input, which writes the editable fields (a blank Category as NULL).
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
	PrimaryAttachmentID *int
	CurrentCost         float64
	LastRollupCost      float64
	LastRollupAt        *time.Time
	AttachmentCount     int
	POLineCount         int
	UnitID              *int
	UnitAbbr            string
	StockOnHand         float64
	ReorderMin          *float64
	TrackingMode        string
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

// ListSupplierParts returns a part's supplier links by supplier name then supplier PN.
func (s *Service) ListSupplierParts(ctx context.Context, partID int) ([]SupplierPart, error) {
	rows, err := s.q.ListSupplierParts(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]SupplierPart, 0, len(rows))
	for _, r := range rows {
		out = append(out, SupplierPart{
			ID: r.ID, SupplierID: r.SupplierID, PartID: r.PartID, Preference: r.Preference,
			SupplierPN: r.SupplierPn, SupplierDesc: r.SupplierDesc, LeadTime: r.LeadTime,
			MinIncrement: r.MinIncrement, UnitID: r.UomID, SupplierName: r.SupplierName,
			PurchaseUnitAbbr: r.PurchaseUnitAbbr, PurchaseUnitIsExplicit: r.PurchaseUnitIsExplicit,
		})
	}
	return out, nil
}

// GetSupplierPart returns supplier link id of partID; sql.ErrNoRows when it
// doesn't exist or belongs to another part.
func (s *Service) GetSupplierPart(ctx context.Context, id, partID int) (SupplierPart, error) {
	r, err := s.q.GetSupplierPart(ctx, dbq.GetSupplierPartParams{ID: id, PartID: partID})
	return SupplierPart{
		ID: r.ID, SupplierID: r.SupplierID, PartID: r.PartID, Preference: r.Preference,
		SupplierPN: r.SupplierPn, SupplierDesc: r.SupplierDesc, LeadTime: r.LeadTime,
		MinIncrement: r.MinIncrement, UnitID: r.UomID, SupplierName: r.SupplierName,
	}, err
}

// CreateSupplierPart inserts sp's link (the joined fields are ignored).
func (s *Service) CreateSupplierPart(ctx context.Context, sp SupplierPart) error {
	return s.q.CreateSupplierPart(ctx, dbq.CreateSupplierPartParams{
		SupplierID: sp.SupplierID, PartID: sp.PartID, Preference: sp.Preference,
		SupplierPn: sp.SupplierPN, SupplierDesc: sp.SupplierDesc, LeadTime: sp.LeadTime,
		MinIncrement: sp.MinIncrement, UomID: sp.UnitID,
	})
}

// UpdateSupplierPart rewrites link sp.ID of sp.PartID; a link of another part
// is left alone without error.
func (s *Service) UpdateSupplierPart(ctx context.Context, sp SupplierPart) error {
	return s.q.UpdateSupplierPart(ctx, dbq.UpdateSupplierPartParams{
		SupplierID: sp.SupplierID, Preference: sp.Preference,
		SupplierPn: sp.SupplierPN, SupplierDesc: sp.SupplierDesc, LeadTime: sp.LeadTime,
		MinIncrement: sp.MinIncrement, UomID: sp.UnitID, ID: sp.ID, PartID: sp.PartID,
	})
}

// DeleteSupplierPart hard-deletes supplier link id of partID.
func (s *Service) DeleteSupplierPart(ctx context.Context, id, partID int) error {
	return s.q.DeleteSupplierPart(ctx, dbq.DeleteSupplierPartParams{ID: id, PartID: partID})
}

// ActivePricesBySupplier returns a part's active prices keyed by supplier, by pack size.
func (s *Service) ActivePricesBySupplier(ctx context.Context, partID int) (map[int][]Price, error) {
	rows, err := s.q.ListActivePrices(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := map[int][]Price{}
	for _, r := range rows {
		out[r.SupplierID] = append(out[r.SupplierID], Price{
			SupplierID: r.SupplierID, PriceEA: r.PriceEa, PricePack: r.PricePack,
			PackSize: r.PackSize, EffectiveDate: r.EffectiveDate,
		})
	}
	return out, nil
}

// ImportPrice inserts an active price break dated effectiveDate (YYYY-MM-DD).
// It reports false, without error, when the pack size already has an active price.
func (s *Service) ImportPrice(ctx context.Context, partID, supplierID int, packSize, priceEA, pricePack float64, effectiveDate string) (bool, error) {
	n, err := s.q.ImportPrice(ctx, dbq.ImportPriceParams{
		PartID: partID, SupplierID: supplierID, PackSize: packSize,
		PriceEa: priceEA, PricePack: pricePack, EffectiveDate: effectiveDate,
	})
	return n > 0, err
}

// CreateImportedAttachment inserts an active, revision-less part_attachment.
func (s *Service) CreateImportedAttachment(ctx context.Context, partID int, fileName, category, comment, hash string) error {
	return s.q.CreateImportedAttachment(ctx, dbq.CreateImportedAttachmentParams{
		PartID: partID, FileName: fileName, Category: category, Comment: comment, Hash: hash,
	})
}

// ErrCompanyNameTaken is CreateManufacturer's error when a company already has the name.
var ErrCompanyNameTaken = errors.New("company name already exists")

// CreateManufacturer inserts a manufacturer-only company and returns its id.
func (s *Service) CreateManufacturer(ctx context.Context, name string) (int, error) {
	id, err := s.q.CreateManufacturer(ctx, name)
	if err == sql.ErrNoRows {
		return 0, ErrCompanyNameTaken
	}
	return id, err
}

// ImportMfgPart inserts an active mfg_part with no description, unless the
// part already has that manufacturer and MPN.
func (s *Service) ImportMfgPart(ctx context.Context, partID, mfgID int, mfgPartNumber string) error {
	return s.q.ImportMfgPart(ctx, dbq.ImportMfgPartParams{PartID: partID, MfgID: mfgID, MfgPartNumber: mfgPartNumber})
}

// ListBOMComponents returns the components one BOM level below parentID.
func (s *Service) ListBOMComponents(ctx context.Context, parentID int) ([]BOMComponent, error) {
	rows, err := s.q.ListBOMComponents(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]BOMComponent, len(rows))
	for i, r := range rows {
		out[i] = BOMComponent{ID: r.ID, Qty: r.Qty, PartNumber: r.PartNumber, Description: r.Description,
			Revision: r.Revision, Category: r.Category, Stock: r.StockOnHand, ReorderMin: r.ReorderMin,
			SupplierID: r.DefaultSupplierID, HasBOM: r.HasBom}
	}
	return out, nil
}

// ListPartNumbers returns every part's part_number.
func (s *Service) ListPartNumbers(ctx context.Context) ([]string, error) {
	return s.q.ListPartNumbers(ctx)
}

// ListParts returns every part for the /parts grid and CSV export, by
// part_number; ThumbFile is its lowest active attachment in thumbCategory.
func (s *Service) ListParts(ctx context.Context, thumbCategory string) ([]ListedPart, error) {
	rows, err := s.q.ListParts(ctx, thumbCategory)
	if err != nil {
		return nil, err
	}
	out := make([]ListedPart, len(rows))
	for i, r := range rows {
		out[i] = ListedPart{ID: r.ID, PartNumber: r.PartNumber, Revision: r.Revision, Description: r.Description,
			Detail: r.Detail, RequestedBy: r.RequestedBy, Category: r.Category, CreatedDate: r.CreatedDate,
			ModifiedDate: r.ModifiedDate, IsActive: r.IsActive, AttachmentCount: r.AttachmentCount,
			POLineCount: r.PoLineCount, BelowMin: r.BelowMin.Bool, ThumbFile: r.ThumbFile}
	}
	return out, nil
}

// GetPartBasic returns sql.ErrNoRows when no part has the id.
func (s *Service) GetPartBasic(ctx context.Context, id int, thumbCategory string) (PartBasic, error) {
	r, err := s.q.GetPartBasic(ctx, dbq.GetPartBasicParams{ID: id, ThumbCategory: thumbCategory})
	if err != nil {
		return PartBasic{}, err
	}
	return PartBasic{ID: r.ID, PartNumber: r.PartNumber, Description: r.Description, Category: r.Category,
		HasBOM: r.HasBom, PrimaryAttachmentID: r.PrimaryAttachmentID, StockOnHand: r.StockOnHand,
		TrackingMode: r.TrackingMode, ThumbFile: r.ThumbFile}, nil
}

// GetPart returns sql.ErrNoRows when no part has the id.
func (s *Service) GetPart(ctx context.Context, id int) (Part, error) {
	r, err := s.q.GetPart(ctx, id)
	if err != nil {
		return Part{}, err
	}
	return Part{ID: r.ID, PartNumber: r.PartNumber, Revision: r.Revision, Description: r.Description,
		Detail: r.Detail, Category: r.Category, HasBOM: r.HasBom, ReleaseStatus: r.ReleaseStatus,
		IsActive: r.IsActive, RequestedBy: r.RequestedBy, Notes: r.Notes, CreatedDate: r.CreatedDate,
		ModifiedDate: r.ModifiedDate, PrimaryAttachmentID: r.PrimaryAttachmentID, CurrentCost: r.CurrentCost,
		LastRollupCost: r.LastRollupCost, LastRollupAt: r.LastRollupAt, AttachmentCount: r.AttachmentCount,
		POLineCount: r.PoLineCount, UnitID: r.UomID, UnitAbbr: r.UnitAbbr, StockOnHand: r.StockOnHand,
		ReorderMin: r.ReorderMin, TrackingMode: r.TrackingMode,
		UserField1: r.UserField1, UserField2: r.UserField2, UserField3: r.UserField3, UserField4: r.UserField4,
		UserField5: r.UserField5, UserField6: r.UserField6, UserField7: r.UserField7, UserField8: r.UserField8,
		UserField9: r.UserField9, UserField10: r.UserField10}, nil
}

// CreatePart inserts p, dated now, and returns its id.
func (s *Service) CreatePart(ctx context.Context, p Part, now time.Time) (int, error) {
	return s.q.CreatePart(ctx, dbq.CreatePartParams{PartNumber: p.PartNumber, Revision: p.Revision,
		Description: p.Description, Detail: p.Detail, Category: p.Category, ReleaseStatus: p.ReleaseStatus,
		IsActive: p.IsActive, RequestedBy: p.RequestedBy, Notes: p.Notes, Now: now, UomID: p.UnitID,
		CurrentCost: p.CurrentCost, ReorderMin: p.ReorderMin,
		UserField1: p.UserField1, UserField2: p.UserField2, UserField3: p.UserField3, UserField4: p.UserField4,
		UserField5: p.UserField5, UserField6: p.UserField6, UserField7: p.UserField7, UserField8: p.UserField8,
		UserField9: p.UserField9, UserField10: p.UserField10, TrackingMode: p.TrackingMode})
}

// UpdatePart overwrites part p.ID's editable fields and sets its modified date to now.
func (s *Service) UpdatePart(ctx context.Context, p Part, now time.Time) error {
	return s.q.UpdatePart(ctx, dbq.UpdatePartParams{PartNumber: p.PartNumber, Revision: p.Revision,
		Description: p.Description, Detail: p.Detail, Category: p.Category, ReleaseStatus: p.ReleaseStatus,
		IsActive: p.IsActive, RequestedBy: p.RequestedBy, Notes: p.Notes, Now: now, UomID: p.UnitID,
		CurrentCost: p.CurrentCost, ReorderMin: p.ReorderMin,
		UserField1: p.UserField1, UserField2: p.UserField2, UserField3: p.UserField3, UserField4: p.UserField4,
		UserField5: p.UserField5, UserField6: p.UserField6, UserField7: p.UserField7, UserField8: p.UserField8,
		UserField9: p.UserField9, UserField10: p.UserField10, TrackingMode: p.TrackingMode, ID: p.ID})
}
