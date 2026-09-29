// Package records is the test-record domain (#190, #223). So far it holds only the record header
// read behind the paste-image API (#248); the rest of the records handlers convert in #223.
package records

import (
	"context"

	"arx/internal/dbq"
)

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// Header is a record's serial, lock state and its form's part number.
type Header struct {
	Serial     string
	Locked     bool
	PartNumber string
}

// GetHeader reads the record's header; a missing record yields sql.ErrNoRows.
func (s *Service) GetHeader(ctx context.Context, id int) (Header, error) {
	r, err := s.q.GetRecordHeader(ctx, id)
	if err != nil {
		return Header{}, err
	}
	return Header{Serial: r.SerialNumber, Locked: r.IsLocked, PartNumber: r.PartNumber}, nil
}
