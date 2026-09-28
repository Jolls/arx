// Package contacts is the contacts domain (#190): typed access to the contact
// table via the sqlc-generated queries in contacts.sql.
package contacts

import (
	"context"
	"time"

	"arx/internal/dbq"
)

// Contact is a contact row plus its company's name. List fills only the
// columns the list page shows.
type Contact struct {
	ID          int
	CompanyID   *int
	DisplayName string
	Email       string
	Phone1      string
	Phone2      string
	Fax         string
	Address     string
	City        string
	State       string
	Zipcode     string
	Country     string
	Website     string
	IsActive    bool
	Notes       string
	UpdatedAt   *time.Time
	// joined
	SupplierName string
}

// PO is one row in the Contact dashboard "Purchase Orders" card (#597).
type PO struct {
	Number       string
	Status       string
	Role         string // "Supplier" or "Receiver" — how this contact is linked to the PO
	Counterparty string // supplier name snapshot, for context
	DateOrdered  *time.Time
	Total        float64
}

// Sibling is one row in the Contact dashboard "Related" card (#521).
type Sibling struct {
	ID          int
	DisplayName string
}

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// List returns every contact, ordered by company name then contact name.
func (s *Service) List(ctx context.Context) ([]Contact, error) {
	rows, err := s.q.ListContacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(rows))
	for _, r := range rows {
		out = append(out, Contact{
			ID: r.ID, CompanyID: r.CompanyID, DisplayName: r.DisplayName,
			Email: r.Email, Phone1: r.Phone1, City: r.City, State: r.State,
			Country: r.Country, Website: r.Website, IsActive: r.IsActive, Notes: r.Notes,
			UpdatedAt: r.UpdatedAt, SupplierName: r.SupplierName,
		})
	}
	return out, nil
}

// Get returns one contact; sql.ErrNoRows when it doesn't exist.
func (s *Service) Get(ctx context.Context, id int) (Contact, error) {
	r, err := s.q.GetContact(ctx, id)
	// GetContact selects Contact's fields in order, so the row converts directly.
	return Contact(r), err
}

// Create inserts c (ID, UpdatedAt and SupplierName are ignored) and returns the new id.
func (s *Service) Create(ctx context.Context, c Contact) (int, error) {
	return s.q.CreateContact(ctx, dbq.CreateContactParams{
		DisplayName: c.DisplayName, CompanyID: c.CompanyID,
		Email: c.Email, Phone1: c.Phone1, Phone2: c.Phone2, Fax: c.Fax,
		Address: c.Address, City: c.City, State: c.State, Zipcode: c.Zipcode, Country: c.Country,
		Website: c.Website, IsActive: c.IsActive, Notes: c.Notes,
	})
}

// Update overwrites contact id with c's editable fields and bumps updated_at.
func (s *Service) Update(ctx context.Context, id int, c Contact) error {
	return s.q.UpdateContact(ctx, dbq.UpdateContactParams{
		DisplayName: c.DisplayName, CompanyID: c.CompanyID,
		Email: c.Email, Phone1: c.Phone1, Phone2: c.Phone2, Fax: c.Fax,
		Address: c.Address, City: c.City, State: c.State, Zipcode: c.Zipcode, Country: c.Country,
		Website: c.Website, IsActive: c.IsActive, Notes: c.Notes,
		ID: id,
	})
}

// Siblings returns the other active contacts at companyID, excluding excludeID.
func (s *Service) Siblings(ctx context.Context, companyID, excludeID int) ([]Sibling, error) {
	rows, err := s.q.ListSiblingContacts(ctx, dbq.ListSiblingContactsParams{
		CompanyID: companyID, ExcludeID: excludeID,
	})
	if err != nil {
		return nil, err
	}
	var out []Sibling
	for _, r := range rows {
		out = append(out, Sibling(r))
	}
	return out, nil
}

// POs returns the POs linked to contactID as supplier or receiver contact, most recent first.
func (s *Service) POs(ctx context.Context, contactID int) ([]PO, error) {
	rows, err := s.q.ListContactPOs(ctx, contactID)
	if err != nil {
		return nil, err
	}
	var out []PO
	for _, r := range rows {
		out = append(out, PO{
			Number: r.Number, Status: r.Status, Role: r.Role, Counterparty: r.SupplierName,
			DateOrdered: r.DateOrdered, Total: r.TotalCost,
		})
	}
	return out, nil
}

// ListActiveForCompany returns companyID's active contacts by name, with the
// address/phone/fax/email fields a PO snapshots.
func (s *Service) ListActiveForCompany(ctx context.Context, companyID int) ([]Contact, error) {
	rows, err := s.q.ListActiveCompanyContacts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, len(rows))
	for i, r := range rows {
		out[i] = Contact{ID: r.ID, DisplayName: r.DisplayName, Address: r.Address, City: r.City,
			State: r.State, Zipcode: r.Zipcode, Country: r.Country, Phone1: r.Phone1, Fax: r.Fax, Email: r.Email}
	}
	return out, nil
}
