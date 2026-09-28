// Package attachments is typed access to part_attachment and company_attachment
// (#190, #220) via the sqlc-generated queries in attachments.sql. The two tables
// share the same shapes: soft delete, a parent primary kept in step (#121) and
// duplicate-hash detection (#71).
package attachments

import (
	"context"
	"database/sql"

	"arx/internal/dbq"
)

// Categories of the images generated from a PDF's first page (#696). They never
// become a part's auto-set primary.
const (
	PreviewCategory   = "PDF Preview"
	ThumbnailCategory = "Thumbnail"
)

// PartAttachment is a part_attachment row. SupplierPartID / MfgPartID are its
// vendor scope (#56), at most one set.
type PartAttachment struct {
	ID             int
	PartID         int
	FileName       string
	Category       string
	PartRevision   string
	SortOrder      *int
	Comment        string
	SupplierPartID *int
	MfgPartID      *int
	Hash           string
	// ListPartAttachments only: the scoped supplier's or manufacturer's name.
	VendorName string
	// GetPartAttachment only.
	IsActive bool
}

// CompanyAttachment is a company_attachment row.
type CompanyAttachment struct {
	ID         int
	SupplierID int
	FilePath   string
	Notes      string
	SortOrder  *int
	Hash       string
}

// Duplicate is the existing active attachment a new one's hash matches, with its
// owner (part or company) id and label (part number or company name).
type Duplicate struct {
	ID      int
	OwnerID int
	Label   string
}

// Usage is one owner of a link in WhereUsed. Kind is "part" or "supplier"; Code is
// the part number (empty for suppliers), Label the part description or company name.
type Usage struct {
	Kind    string
	OwnerID int
	Code    string
	Label   string
}

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// ── part_attachment ─────────────────────────────────────────────────────────

// ListPartAttachments returns a part's active attachments by sort order, then id.
func (s *Service) ListPartAttachments(ctx context.Context, partID int) ([]PartAttachment, error) {
	rows, err := s.q.ListPartAttachments(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]PartAttachment, len(rows))
	for i, r := range rows {
		out[i] = PartAttachment{ID: r.ID, PartID: partID, FileName: r.FileName, Category: r.Category,
			PartRevision: r.PartRevision, SortOrder: r.SortOrder, Comment: r.Comment,
			SupplierPartID: r.SupplierPartID, MfgPartID: r.MfgPartID, VendorName: r.VendorName}
	}
	return out, nil
}

// GetPartAttachment returns one of partID's attachments, active or not;
// sql.ErrNoRows when there's none.
func (s *Service) GetPartAttachment(ctx context.Context, id, partID int) (PartAttachment, error) {
	r, err := s.q.GetPartAttachment(ctx, dbq.GetPartAttachmentParams{ID: id, PartID: partID})
	if err != nil {
		return PartAttachment{}, err
	}
	return PartAttachment{ID: id, PartID: partID, FileName: r.FileName, Category: r.Category,
		PartRevision: r.PartRevision, IsActive: r.IsActive}, nil
}

// GetActivePartAttachment is GetPartAttachment for an active row only; sql.ErrNoRows
// when it's missing or soft-deleted.
func (s *Service) GetActivePartAttachment(ctx context.Context, id, partID int) (PartAttachment, error) {
	a, err := s.GetPartAttachment(ctx, id, partID)
	if err == nil && !a.IsActive {
		return PartAttachment{}, sql.ErrNoRows
	}
	return a, err
}

// CreatePartAttachment inserts a; follow it with EnsurePartPrimary in the same tx.
func (s *Service) CreatePartAttachment(ctx context.Context, a PartAttachment) error {
	return s.q.CreatePartAttachment(ctx, dbq.CreatePartAttachmentParams{
		PartID: a.PartID, FileName: a.FileName, PartRevision: a.PartRevision, Category: a.Category,
		SortOrder: a.SortOrder, Comment: a.Comment, SupplierPartID: a.SupplierPartID, MfgPartID: a.MfgPartID,
		Hash: a.Hash,
	})
}

// UpdatePartAttachment saves a's metadata and vendor scope, keeping its file and hash.
func (s *Service) UpdatePartAttachment(ctx context.Context, a PartAttachment) error {
	return s.q.UpdatePartAttachment(ctx, dbq.UpdatePartAttachmentParams{
		PartRevision: a.PartRevision, Category: a.Category, SortOrder: a.SortOrder, Comment: a.Comment,
		SupplierPartID: a.SupplierPartID, MfgPartID: a.MfgPartID, ID: a.ID,
	})
}

// UpdatePartAttachmentFile is UpdatePartAttachment plus a new file and hash.
func (s *Service) UpdatePartAttachmentFile(ctx context.Context, a PartAttachment) error {
	return s.q.UpdatePartAttachmentFile(ctx, dbq.UpdatePartAttachmentFileParams{
		PartRevision: a.PartRevision, Category: a.Category, SortOrder: a.SortOrder, Comment: a.Comment,
		SupplierPartID: a.SupplierPartID, MfgPartID: a.MfgPartID, FileName: a.FileName, Hash: a.Hash, ID: a.ID,
	})
}

// ReplacePartAttachmentPhoto repoints a.ID at a pasted image, keeping its vendor scope.
func (s *Service) ReplacePartAttachmentPhoto(ctx context.Context, a PartAttachment) error {
	return s.q.ReplacePartAttachmentPhoto(ctx, dbq.ReplacePartAttachmentPhotoParams{
		PartRevision: a.PartRevision, Category: a.Category, SortOrder: a.SortOrder, Comment: a.Comment,
		FileName: a.FileName, Hash: a.Hash, ID: a.ID,
	})
}

// DeletePartAttachment soft-deletes one of partID's attachments; follow it with
// EnsurePartPrimary in the same tx.
func (s *Service) DeletePartAttachment(ctx context.Context, id, partID int) error {
	return s.q.SoftDeletePartAttachment(ctx, dbq.SoftDeletePartAttachmentParams{ID: id, PartID: partID})
}

// PartFileInUse reports whether an active part_attachment other than excludeID
// links fileName.
func (s *Service) PartFileInUse(ctx context.Context, fileName string, excludeID int) (bool, error) {
	return s.q.PartFileInUse(ctx, dbq.PartFileInUseParams{FileName: fileName, ExcludeID: excludeID})
}

// EnsurePartPrimary repoints the part's primary when it's unset or no longer active (#121).
func (s *Service) EnsurePartPrimary(ctx context.Context, partID int) error {
	return s.q.EnsurePartPrimary(ctx, dbq.EnsurePartPrimaryParams{
		PreviewCategory: PreviewCategory, ThumbnailCategory: ThumbnailCategory, PartID: partID,
	})
}

// SetPartPrimary sets the part's primary attachment; nil clears it.
func (s *Service) SetPartPrimary(ctx context.Context, partID int, attachmentID *int) error {
	return s.q.SetPartPrimary(ctx, dbq.SetPartPrimaryParams{AttachmentID: attachmentID, PartID: partID})
}

// FindDuplicatePartAttachment returns the oldest active part_attachment other than
// excludeID (0 excludes nothing) whose hash matches, or nil when hash is empty or
// nothing matches (#71).
func (s *Service) FindDuplicatePartAttachment(ctx context.Context, hash string, excludeID int) (*Duplicate, error) {
	if hash == "" {
		return nil, nil
	}
	r, err := s.q.FindDuplicatePartAttachment(ctx, dbq.FindDuplicatePartAttachmentParams{Hash: hash, ExcludeID: excludeID})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Duplicate{ID: r.ID, OwnerID: r.OwnerID, Label: r.Label}, nil
}

// SupplierPartOfPart reports whether supplier_part id belongs to partID.
func (s *Service) SupplierPartOfPart(ctx context.Context, id, partID int) (bool, error) {
	return s.q.SupplierPartOfPart(ctx, dbq.SupplierPartOfPartParams{ID: id, PartID: partID})
}

// MfgPartOfPart reports whether mfg_part id belongs to partID.
func (s *Service) MfgPartOfPart(ctx context.Context, id, partID int) (bool, error) {
	return s.q.MfgPartOfPart(ctx, dbq.MfgPartOfPartParams{ID: id, PartID: partID})
}

// GetGeneratedAttachment returns the part's oldest active attachment of a generated
// category (ID and FileName only); sql.ErrNoRows when there's none.
func (s *Service) GetGeneratedAttachment(ctx context.Context, partID int, category string) (PartAttachment, error) {
	r, err := s.q.GetGeneratedAttachment(ctx, dbq.GetGeneratedAttachmentParams{PartID: partID, Category: category})
	if err != nil {
		return PartAttachment{}, err
	}
	return PartAttachment{ID: r.ID, PartID: partID, FileName: r.FileName, Category: category}, nil
}

// CreateGeneratedAttachment inserts a generated-image row (default sort order, no comment).
func (s *Service) CreateGeneratedAttachment(ctx context.Context, a PartAttachment) error {
	return s.q.CreateGeneratedAttachment(ctx, dbq.CreateGeneratedAttachmentParams{
		PartID: a.PartID, FileName: a.FileName, PartRevision: a.PartRevision, Category: a.Category, Hash: a.Hash,
	})
}

// UpdateGeneratedAttachment repoints a.ID at a new file, revision and hash.
func (s *Service) UpdateGeneratedAttachment(ctx context.Context, a PartAttachment) error {
	return s.q.UpdateGeneratedAttachment(ctx, dbq.UpdateGeneratedAttachmentParams{
		FileName: a.FileName, PartRevision: a.PartRevision, Hash: a.Hash, ID: a.ID,
	})
}

// ── company_attachment ──────────────────────────────────────────────────────

// ListCompanyAttachments returns a company's active attachments by sort order, then id.
func (s *Service) ListCompanyAttachments(ctx context.Context, supplierID int) ([]CompanyAttachment, error) {
	rows, err := s.q.ListCompanyAttachments(ctx, supplierID)
	if err != nil {
		return nil, err
	}
	out := make([]CompanyAttachment, len(rows))
	for i, r := range rows {
		out[i] = CompanyAttachment{ID: r.SupplierAttachmentID, SupplierID: r.SupplierID, FilePath: r.FilePath,
			Notes: r.Notes, SortOrder: r.SortOrder}
	}
	return out, nil
}

// CompanyAttachmentPath returns one of supplierID's attachment links; sql.ErrNoRows
// when there's none.
func (s *Service) CompanyAttachmentPath(ctx context.Context, id, supplierID int) (string, error) {
	return s.q.GetCompanyAttachmentPath(ctx, dbq.GetCompanyAttachmentPathParams{SupplierAttachmentID: id, SupplierID: supplierID})
}

// CreateCompanyAttachment inserts a; follow it with EnsureCompanyPrimary in the same tx.
func (s *Service) CreateCompanyAttachment(ctx context.Context, a CompanyAttachment) error {
	return s.q.CreateCompanyAttachment(ctx, dbq.CreateCompanyAttachmentParams{
		SupplierID: a.SupplierID, FilePath: a.FilePath, Notes: a.Notes, SortOrder: a.SortOrder, Hash: a.Hash,
	})
}

// UpdateCompanyAttachment saves a's notes and sort order, keeping its file and hash.
func (s *Service) UpdateCompanyAttachment(ctx context.Context, a CompanyAttachment) error {
	return s.q.UpdateCompanyAttachment(ctx, dbq.UpdateCompanyAttachmentParams{
		Notes: a.Notes, SortOrder: a.SortOrder, ID: a.ID, SupplierID: a.SupplierID,
	})
}

// UpdateCompanyAttachmentFile is UpdateCompanyAttachment plus a new file and hash.
func (s *Service) UpdateCompanyAttachmentFile(ctx context.Context, a CompanyAttachment) error {
	return s.q.UpdateCompanyAttachmentFile(ctx, dbq.UpdateCompanyAttachmentFileParams{
		Notes: a.Notes, SortOrder: a.SortOrder, FilePath: a.FilePath, Hash: a.Hash, ID: a.ID, SupplierID: a.SupplierID,
	})
}

// DeleteCompanyAttachment soft-deletes one of supplierID's attachments; follow it
// with EnsureCompanyPrimary in the same tx.
func (s *Service) DeleteCompanyAttachment(ctx context.Context, id, supplierID int) error {
	return s.q.SoftDeleteCompanyAttachment(ctx, dbq.SoftDeleteCompanyAttachmentParams{SupplierAttachmentID: id, SupplierID: supplierID})
}

// CompanyFileInUse reports whether an active company_attachment other than
// excludeID links filePath.
func (s *Service) CompanyFileInUse(ctx context.Context, filePath string, excludeID int) (bool, error) {
	return s.q.CompanyFileInUse(ctx, dbq.CompanyFileInUseParams{FilePath: filePath, ExcludeID: excludeID})
}

// EnsureCompanyPrimary repoints the company's primary when it's unset or no longer active (#121).
func (s *Service) EnsureCompanyPrimary(ctx context.Context, supplierID int) error {
	return s.q.EnsureCompanyPrimary(ctx, supplierID)
}

// SetCompanyPrimary sets the company's primary attachment; nil clears it.
func (s *Service) SetCompanyPrimary(ctx context.Context, supplierID int, attachmentID *int) error {
	return s.q.SetCompanyPrimary(ctx, dbq.SetCompanyPrimaryParams{AttachmentID: attachmentID, SupplierID: supplierID})
}

// FindDuplicateCompanyAttachment is FindDuplicatePartAttachment for company_attachment;
// the two tables are checked independently (#71).
func (s *Service) FindDuplicateCompanyAttachment(ctx context.Context, hash string, excludeID int) (*Duplicate, error) {
	if hash == "" {
		return nil, nil
	}
	r, err := s.q.FindDuplicateCompanyAttachment(ctx, dbq.FindDuplicateCompanyAttachmentParams{Hash: hash, ExcludeID: excludeID})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Duplicate{ID: r.ID, OwnerID: r.OwnerID, Label: r.Label}, nil
}

// ── both ────────────────────────────────────────────────────────────────────

// WhereUsed lists the parts, then the suppliers, with an active attachment of exactly
// link, each by label.
func (s *Service) WhereUsed(ctx context.Context, link string) ([]Usage, error) {
	rows, err := s.q.WhereUsed(ctx, link)
	if err != nil {
		return nil, err
	}
	out := make([]Usage, len(rows))
	for i, r := range rows {
		out[i] = Usage{Kind: r.Kind, OwnerID: r.OwnerID, Code: r.Code, Label: r.Label}
	}
	return out, nil
}
