// Package parts is the parts domain (#190, #220): typed access to the part
// tables via the sqlc-generated queries in parts.sql. So far part categories,
// manufacturer parts, sourcing (supplier links, their prices and the
// DigiKey import), the RFQ planner's BOM reads, the part-number list, the
// parts list/CSV export, the single-part read/create/update, the BOM
// lines (view, where-used, edit, paste preview, copy, export), the
// pricing tab's price CRUD, the BOM cost rollup/build cost, the part
// detail dashboard's cards and orders/price-history tabs, the part
// search / supplier-part autofill APIs, and the supplier-link/price writes
// behind the PO page's suggestions are converted.
package parts

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
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

// PartPrice is any price row of a part plus its supplier's name; NULL is_active reads as inactive.
type PartPrice struct {
	ID            int
	SupplierID    int
	PriceEA       *float64
	PricePack     *float64
	PackSize      *float64
	IsActive      bool
	EffectiveDate *time.Time
	// joined
	SupplierName string
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

// BOMLine is a bom line plus its component's part data; NULL text reads as "",
// NULL costs and counts as 0. PreferredPrice is the component's lowest active
// price from its default supplier, 0 when it has none. It is also the
// update/create input, which writes LineNumber, Qty and ComponentPartID.
type BOMLine struct {
	ID              int
	LineNumber      int
	Qty             float64
	ComponentPartID int
	PartNumber      string
	Description     string
	Revision        string
	Category        string
	CurrentCost     float64
	LastRollupCost  float64
	PreferredPrice  float64
	HasBOM          bool
	AttachmentCount int
	POLineCount     int
}

// WhereUsed is a bom line that uses a part, plus its parent's part data.
type WhereUsed struct {
	LineNumber   int
	Qty          float64
	ParentPartID int
	PartNumber   string
	Description  string
	Revision     string
	Category     string
}

// PartRef is a part's id, number and description.
type PartRef struct {
	ID          int
	PartNumber  string
	Description string
}

// BuildCostPart is what build cost needs of a leaf part: its number,
// description (NULL reads as "") and default supplier.
type BuildCostPart struct {
	PartNumber        string
	Description       string
	DefaultSupplierID *int
}

// PriceTier is an active price row's per-unit price at its pack size.
type PriceTier struct {
	PartID     int
	SupplierID int
	PriceEA    float64
	PackSize   float64
}

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// UOM is a unit of measure, for dropdowns.
type UOM struct {
	ID           int
	Abbreviation string
	DisplayName  string
	UnitType     string
}

// ListUOMs returns the uom reference list ordered by unit_type, abbreviation.
func (s *Service) ListUOMs(ctx context.Context) ([]UOM, error) {
	rows, err := s.q.ListUOMs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UOM, len(rows))
	for i, r := range rows {
		out[i] = UOM{ID: r.UomID, Abbreviation: r.Abbreviation, DisplayName: r.DisplayName, UnitType: r.UnitType}
	}
	return out, nil
}

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

// ListBOMLines returns parentID's BOM lines by line number.
func (s *Service) ListBOMLines(ctx context.Context, parentID int) ([]BOMLine, error) {
	rows, err := s.q.ListBOMLines(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]BOMLine, len(rows))
	for i, r := range rows {
		out[i] = BOMLine{ID: r.ID, LineNumber: r.LineNumber, Qty: r.Qty, ComponentPartID: r.ComponentPartID,
			PartNumber: r.PartNumber, Description: r.Description, Revision: r.Revision, Category: r.Category,
			CurrentCost: r.CurrentCost, LastRollupCost: r.LastRollupCost, PreferredPrice: r.PreferredPrice,
			HasBOM: r.HasBom, AttachmentCount: r.AttachmentCount, POLineCount: r.PoLineCount}
	}
	return out, nil
}

// ListWhereUsed returns the BOM lines that use partID, by parent part number.
func (s *Service) ListWhereUsed(ctx context.Context, partID int) ([]WhereUsed, error) {
	rows, err := s.q.ListWhereUsed(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]WhereUsed, len(rows))
	for i, r := range rows {
		out[i] = WhereUsed(r)
	}
	return out, nil
}

// GetPartRollup returns a part's last rollup cost (0 when never run) and time.
func (s *Service) GetPartRollup(ctx context.Context, id int) (float64, *time.Time, error) {
	r, err := s.q.GetPartRollup(ctx, id)
	return r.LastRollupCost, r.LastRollupAt, err
}

// GetPartByNumber returns sql.ErrNoRows when no part has the number.
func (s *Service) GetPartByNumber(ctx context.Context, partNumber string) (PartRef, error) {
	r, err := s.q.GetPartByNumber(ctx, partNumber)
	return PartRef(r), err
}

// DeleteBOMLine deletes line id of parentID; a line of another parent is left alone.
func (s *Service) DeleteBOMLine(ctx context.Context, id, parentID int) error {
	return s.q.DeleteBOMLine(ctx, dbq.DeleteBOMLineParams{ID: id, ParentPartID: parentID})
}

// UpdateBOMLine rewrites line l.ID of parentID; a line of another parent is left alone.
func (s *Service) UpdateBOMLine(ctx context.Context, parentID int, l BOMLine) error {
	return s.q.UpdateBOMLine(ctx, dbq.UpdateBOMLineParams{LineNumber: l.LineNumber, Qty: l.Qty,
		ComponentPartID: l.ComponentPartID, ID: l.ID, ParentPartID: parentID})
}

// CreateBOMLine adds l to parentID's BOM.
func (s *Service) CreateBOMLine(ctx context.Context, parentID int, l BOMLine) error {
	return s.q.CreateBOMLine(ctx, dbq.CreateBOMLineParams{ParentPartID: parentID,
		ComponentPartID: l.ComponentPartID, LineNumber: l.LineNumber, Qty: l.Qty})
}

// CopyBOM adds every BOM line of srcID to dstID.
func (s *Service) CopyBOM(ctx context.Context, srcID, dstID int) error {
	return s.q.CopyBOM(ctx, dbq.CopyBOMParams{DstID: dstID, SrcID: srcID})
}

// ListPartPrices returns every price row of partID, by supplier name, newest
// effective date first, then pack size.
func (s *Service) ListPartPrices(ctx context.Context, partID int) ([]PartPrice, error) {
	rows, err := s.q.ListPartPrices(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]PartPrice, len(rows))
	for i, r := range rows {
		out[i] = PartPrice{ID: r.ID, SupplierID: r.SupplierID, PriceEA: r.PriceEa, PricePack: r.PricePack,
			PackSize: r.PackSize, IsActive: r.IsActive, EffectiveDate: r.EffectiveDate, SupplierName: r.SupplierName}
	}
	return out, nil
}

// GetPartPrice returns price id of partID; sql.ErrNoRows when it doesn't exist
// or belongs to another part.
func (s *Service) GetPartPrice(ctx context.Context, id, partID int) (PartPrice, error) {
	r, err := s.q.GetPartPrice(ctx, dbq.GetPartPriceParams{ID: id, PartID: partID})
	return PartPrice{ID: r.ID, SupplierID: r.SupplierID, PriceEA: r.PriceEa, PricePack: r.PricePack,
		PackSize: r.PackSize, IsActive: r.IsActive, EffectiveDate: r.EffectiveDate, SupplierName: r.SupplierName}, err
}

// GetDefaultSupplier returns a part's preferred supplier for cost rollup, nil when unset.
func (s *Service) GetDefaultSupplier(ctx context.Context, partID int) (*int, error) {
	return s.q.GetDefaultSupplier(ctx, partID)
}

// SetDefaultSupplier sets a part's preferred supplier for cost rollup.
func (s *Service) SetDefaultSupplier(ctx context.Context, partID, supplierID int) error {
	return s.q.SetDefaultSupplier(ctx, dbq.SetDefaultSupplierParams{SupplierID: &supplierID, ID: partID})
}

// CreatePrice inserts an active price dated effectiveDate (YYYY-MM-DD).
func (s *Service) CreatePrice(ctx context.Context, partID, supplierID int, packSize, priceEA, pricePack *float64, effectiveDate string) error {
	return s.q.CreatePrice(ctx, dbq.CreatePriceParams{PartID: partID, SupplierID: supplierID,
		PackSize: packSize, PriceEa: priceEA, PricePack: pricePack, EffectiveDate: effectiveDate})
}

// SetPriceActive (de)activates price id of partID; a price of another part is left alone.
func (s *Service) SetPriceActive(ctx context.Context, id, partID int, active bool) error {
	return s.q.SetPriceActive(ctx, dbq.SetPriceActiveParams{IsActive: active, ID: id, PartID: partID})
}

// DeleteInactivePrice hard-deletes price id of partID; an active price is left alone.
func (s *Service) DeleteInactivePrice(ctx context.Context, id, partID int) error {
	return s.q.DeleteInactivePrice(ctx, dbq.DeleteInactivePriceParams{ID: id, PartID: partID})
}

// SetPartRollup stores cost as part id's rolled-up cost, stamped now.
func (s *Service) SetPartRollup(ctx context.Context, id int, cost float64) error {
	return s.q.SetPartRollup(ctx, dbq.SetPartRollupParams{Cost: cost, ID: id})
}

// idList joins ids with commas for a string_to_array(...)::int[] param.
func idList(ids []int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.Itoa(id)
	}
	return strings.Join(s, ",")
}

// BuildCostParts returns the parts in ids keyed by id; unknown ids are absent.
func (s *Service) BuildCostParts(ctx context.Context, ids []int) (map[int]BuildCostPart, error) {
	rows, err := s.q.ListBuildCostParts(ctx, idList(ids))
	if err != nil {
		return nil, err
	}
	out := make(map[int]BuildCostPart, len(rows))
	for _, r := range rows {
		out[r.ID] = BuildCostPart{PartNumber: r.PartNumber, Description: r.Description, DefaultSupplierID: r.DefaultSupplierID}
	}
	return out, nil
}

// ActivePriceTiers returns the active price rows of the parts in partIDs,
// skipping any without a price or pack size (as MIN(price_ea) does for rollup).
func (s *Service) ActivePriceTiers(ctx context.Context, partIDs []int) ([]PriceTier, error) {
	rows, err := s.q.ListActivePriceTiers(ctx, idList(partIDs))
	if err != nil {
		return nil, err
	}
	var out []PriceTier
	for _, r := range rows {
		if r.PriceEa == nil || r.PackSize == nil {
			continue
		}
		out = append(out, PriceTier{PartID: r.PartID, SupplierID: r.SupplierID, PriceEA: *r.PriceEa, PackSize: *r.PackSize})
	}
	return out, nil
}

// PartOrder is a PO line of a part plus its PO header.
type PartOrder struct {
	PONumber     string
	SupplierName string
	DateOrdered  *time.Time
	DateClosed   *time.Time
	Status       string
	LineNumber   int
	Qty          float64
	UnitCost     float64
	Description  string
	VendorPN     string
}

// ListPartOrders returns every PO line of a part, newest order first (undated first).
func (s *Service) ListPartOrders(ctx context.Context, partID int) ([]PartOrder, error) {
	rows, err := s.q.ListPartOrders(ctx, dbq.ListPartOrdersParams{PartID: partID})
	if err != nil {
		return nil, err
	}
	out := make([]PartOrder, len(rows))
	for i, r := range rows {
		out[i] = PartOrder{PONumber: r.Number, SupplierName: r.SupplierName, DateOrdered: r.DateOrdered, DateClosed: r.DateClosed,
			Status: r.Status, LineNumber: r.LineNumber, Qty: r.Qty, UnitCost: r.UnitCost, Description: r.Description, VendorPN: r.VendorPartNumber}
	}
	return out, nil
}

// RecentPO is a PO line of a part for the part dashboard.
type RecentPO struct {
	Number       string
	SupplierName string
	Status       string
	DateOrdered  *time.Time
	Qty          float64
	UnitCost     float64
}

// ListRecentPOs returns a part's n newest PO lines (undated first).
func (s *Service) ListRecentPOs(ctx context.Context, partID, n int) ([]RecentPO, error) {
	rows, err := s.q.ListPartOrders(ctx, dbq.ListPartOrdersParams{PartID: partID, N: &n})
	if err != nil {
		return nil, err
	}
	out := make([]RecentPO, len(rows))
	for i, r := range rows {
		out[i] = RecentPO{Number: r.Number, SupplierName: r.SupplierName, Status: r.Status,
			DateOrdered: r.DateOrdered, Qty: r.Qty, UnitCost: r.UnitCost}
	}
	return out, nil
}

// InventoryTxn is an inventory movement of a part.
type InventoryTxn struct {
	Type string
	Qty  float64
	Date time.Time
}

// ListRecentTxns returns a part's n newest inventory movements.
func (s *Service) ListRecentTxns(ctx context.Context, partID, n int) ([]InventoryTxn, error) {
	rows, err := s.q.ListRecentPartTxns(ctx, dbq.ListRecentPartTxnsParams{PartID: partID, N: n})
	if err != nil {
		return nil, err
	}
	out := make([]InventoryTxn, len(rows))
	for i, r := range rows {
		out[i] = InventoryTxn{Type: r.TxnType, Qty: r.Qty, Date: r.TxnDate}
	}
	return out, nil
}

// PreferredSupplier is a part's preferred supplier plus its most-preferred
// supplier_part reference fields; HasLink is false when there is no such row.
type PreferredSupplier struct {
	SupplierID   int
	SupplierName string
	SupplierPN   string
	SupplierDesc string
	HasLink      bool
}

// GetPreferredSupplier returns a part's preferred supplier; sql.ErrNoRows when
// none is pinned.
func (s *Service) GetPreferredSupplier(ctx context.Context, partID int) (PreferredSupplier, error) {
	r, err := s.q.GetPreferredSupplier(ctx, partID)
	return PreferredSupplier{SupplierID: r.ID, SupplierName: r.Name, SupplierPN: r.SupplierPn,
		SupplierDesc: r.SupplierDesc, HasLink: r.SupplierPartID != nil}, err
}

// PreferredSupplierPrice returns the cheapest active price_ea from a part's
// preferred supplier, nil when there is none.
func (s *Service) PreferredSupplierPrice(ctx context.Context, partID int) (*float64, error) {
	p, err := s.q.PreferredSupplierMinPrice(ctx, partID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// EnsureDefaultSupplier pins supplierID as a part's preferred supplier unless
// one is already set.
func (s *Service) EnsureDefaultSupplier(ctx context.Context, partID, supplierID int) error {
	return s.q.EnsureDefaultSupplier(ctx, dbq.EnsureDefaultSupplierParams{SupplierID: supplierID, ID: partID})
}

// LinkSupplierPN links partID to supplierID's part number pn unless that exact link exists.
func (s *Service) LinkSupplierPN(ctx context.Context, partID, supplierID int, pn string) error {
	return s.q.LinkSupplierPN(ctx, dbq.LinkSupplierPNParams{PartID: partID, SupplierID: supplierID, SupplierPn: pn})
}

// DeactivatePrices deactivates partID's active supplierID prices at packSize.
func (s *Service) DeactivatePrices(ctx context.Context, partID, supplierID int, packSize float64) error {
	return s.q.DeactivatePrices(ctx, dbq.DeactivatePricesParams{PartID: partID, SupplierID: supplierID, PackSize: packSize})
}

// POPricePoint is a dated PO line's unit cost.
type POPricePoint struct {
	PONumber     string
	SupplierName string
	DateOrdered  time.Time
	UnitCost     float64
}

// ListPOPricePoints returns a part's dated PO lines, oldest first.
func (s *Service) ListPOPricePoints(ctx context.Context, partID int) ([]POPricePoint, error) {
	rows, err := s.q.ListPOPricePoints(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]POPricePoint, len(rows))
	for i, r := range rows {
		out[i] = POPricePoint{PONumber: r.Number, SupplierName: r.SupplierName, DateOrdered: *r.DateOrdered, UnitCost: r.UnitCost}
	}
	return out, nil
}

// PriceListPoint is a dated active price's per-unit price and pack size.
type PriceListPoint struct {
	SupplierName  string
	EffectiveDate time.Time
	PriceEA       float64
	PackSize      *float64
}

// ListPriceListPoints returns a part's dated active prices, oldest first,
// skipping any without a per-unit price.
func (s *Service) ListPriceListPoints(ctx context.Context, partID int) ([]PriceListPoint, error) {
	rows, err := s.q.ListPriceListPoints(ctx, partID)
	if err != nil {
		return nil, err
	}
	var out []PriceListPoint
	for _, r := range rows {
		if r.PriceEa == nil {
			continue
		}
		out = append(out, PriceListPoint{SupplierName: r.SupplierName, EffectiveDate: *r.EffectiveDate, PriceEA: *r.PriceEa, PackSize: r.PackSize})
	}
	return out, nil
}

// PartMatch is a part search hit.
type PartMatch struct {
	ID          int
	PartNumber  string
	Revision    string
	Description string
	Detail      string
}

// SearchParts returns up to n parts whose part_number contains q, or whose
// description/detail does when byDesc, ordered by part_number. q is not
// LIKE-escaped.
func (s *Service) SearchParts(ctx context.Context, q string, byDesc bool, n int) ([]PartMatch, error) {
	rows, err := s.q.SearchParts(ctx, dbq.SearchPartsParams{ByDesc: byDesc, Pattern: "%" + q + "%", N: n})
	if err != nil {
		return nil, err
	}
	out := make([]PartMatch, len(rows))
	for i, r := range rows {
		out[i] = PartMatch(r)
	}
	return out, nil
}

// SupplierPartDefaults autofills a PO line: the supplier's part number and
// minimum order increment, and its smallest-pack active unit price.
type SupplierPartDefaults struct {
	SupplierPN   string
	MinIncrement *float64
	PriceEA      *float64
}

// GetSupplierPartDefaults returns a (part, supplier) pair's PO-line defaults;
// sql.ErrNoRows when the pair has no supplier_part link.
func (s *Service) GetSupplierPartDefaults(ctx context.Context, partID, supplierID int) (SupplierPartDefaults, error) {
	r, err := s.q.GetSupplierPartDefaults(ctx, dbq.GetSupplierPartDefaultsParams{PartID: partID, SupplierID: supplierID})
	return SupplierPartDefaults{SupplierPN: r.SupplierPn, MinIncrement: r.MinIncrement, PriceEA: r.PriceEa}, err
}
