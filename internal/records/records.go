// Package records is the test-record domain (#190, #223, #249): typed reads of forms, steps, records,
// results and the audit trail via the sqlc-generated queries in records.sql. Every method runs on the
// DBTX the Service was built over. The write paths (save, lock, approve, events) convert in #223's
// last slice.
package records

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"arx/internal/dbq"
)

var errBadScope = errors.New("records: unsupported scope")

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

// Row types that map one-to-one onto a query's columns.
type (
	FormListRow  = dbq.ListActiveFormsRow
	FormHeader   = dbq.GetFormHeaderRow
	FormStep     = dbq.ListFormStepsRow
	FormStepAt   = dbq.ListFormStepsAtRow
	HistoryStamp = dbq.ListFormHistoryStampsRow
	PartOption   = dbq.ListFormPartOptionsRow
	SourceForm   = dbq.ListSourceFormsRow
	StepInfo     = dbq.GetFormStepRow
	BOMPart      = dbq.ListBOMPartsRow
	PartTracking = dbq.GetPartTrackingRow
	ResultRow    = dbq.ListRecordResultsRow
	Event        = dbq.ListRecordEventsRow
	FailureMode  = dbq.ListFailureModesRow
)

// Scope picks which column the per-scope record reads filter on. Exactly one id is ever bound, so a
// caller can't ask for "all records" by accident.
type Scope int

const (
	ScopeForm Scope = iota
	ScopePart
	ScopeLot
	ScopeUnit
)

// Record is one form_record with NULL text flattened to "".
type Record struct {
	ID, FormID, PartID                                    int
	SerialNumber, SubjectPartNumber, SubjectPNDescription string
	RecordDate                                            *time.Time
	RecordType, Notes, InstrumentType                     string
	IsLocked, IsApproved, IsActive                        bool
	TestOrder                                             string
	LotID, BuildID, UnitID                                *int
}

// Listed is one row of a records table. FormID / FormPartNumber / FormDescription are only set for
// the cross-form (Part/Lot/Unit) tables.
type Listed struct {
	ID, PartID                                            int
	SerialNumber, SubjectPartNumber, SubjectPNDescription string
	RecordDate                                            *time.Time
	RecordType                                            string
	IsLocked, IsApproved                                  bool
	FormRevision                                          *int
	FormID                                                int
	FormPartNumber, FormDescription                       string
}

// StepResult is one active record's recorded result for a step.
type StepResult struct {
	ID                int
	SerialNumber      string
	SubjectPartNumber string
	PartID            int
	RecordDate        *time.Time
	Result            string
	PassFail          *bool
	Comment           string
	UpdatedAt         *time.Time
}

// EventResult is one result captured in a completion snapshot.
type EventResult struct {
	EventID, FormRowID                          int
	Parameter, Specification, SpecUnits, Result string
	PassFail                                    *bool
	Comment                                     string
}

// YieldRecord is an active record's date and whether any of its results failed.
type YieldRecord struct {
	RecordDate *time.Time
	AnyFail    bool
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func nullBool(b sql.NullBool) *bool {
	if !b.Valid {
		return nil
	}
	return &b.Bool
}

// optDate is a filter bound: the zero time means "no bound".
func optDate(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func optTimestamp(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: !t.IsZero()} }

// ── Forms ────────────────────────────────────────────────────────────────────

func (s *Service) ListForms(ctx context.Context) ([]FormListRow, error) {
	return s.q.ListActiveForms(ctx)
}

// GetFormHeader yields sql.ErrNoRows for a missing form.
func (s *Service) GetFormHeader(ctx context.Context, id int) (FormHeader, error) {
	return s.q.GetFormHeader(ctx, id)
}

func (s *Service) ListFormSteps(ctx context.Context, formID int) ([]FormStep, error) {
	return s.q.ListFormSteps(ctx, formID)
}

func (s *Service) ListFormHistoryStamps(ctx context.Context, formID int) ([]HistoryStamp, error) {
	return s.q.ListFormHistoryStamps(ctx, formID)
}

// ListFormStepsAt returns the form's steps as they stood in [dayStart, dayEnd).
func (s *Service) ListFormStepsAt(ctx context.Context, formID int, dayStart, dayEnd time.Time) ([]FormStepAt, error) {
	return s.q.ListFormStepsAt(ctx, dbq.ListFormStepsAtParams{FormID: formID, DayStart: dayStart, DayEnd: dayEnd})
}

func (s *Service) ListFormPartOptions(ctx context.Context) ([]PartOption, error) {
	return s.q.ListFormPartOptions(ctx)
}

func (s *Service) ListSourceForms(ctx context.Context) ([]SourceForm, error) {
	return s.q.ListSourceForms(ctx)
}

func (s *Service) CountFormSteps(ctx context.Context, formID int) (int, error) {
	return s.q.CountFormSteps(ctx, formID)
}

// GetStep yields sql.ErrNoRows for a missing step; the caller checks it belongs to the form.
func (s *Service) GetStep(ctx context.Context, id int) (StepInfo, error) {
	return s.q.GetFormStep(ctx, id)
}

// GetStepFormat yields sql.ErrNoRows when the step isn't one of the form's.
func (s *Service) GetStepFormat(ctx context.Context, stepID, formID int) (string, error) {
	return s.q.GetFormStepFormat(ctx, dbq.GetFormStepFormatParams{ID: stepID, FormID: formID})
}

// ── Records ──────────────────────────────────────────────────────────────────

// GetRecord yields sql.ErrNoRows for a missing record.
func (s *Service) GetRecord(ctx context.Context, id int) (Record, error) {
	r, err := s.q.GetRecord(ctx, id)
	if err != nil {
		return Record{}, err
	}
	return Record{
		ID: r.ID, FormID: r.FormID, PartID: r.PartID, SerialNumber: r.SerialNumber,
		SubjectPartNumber: r.SubjectPartNumber, SubjectPNDescription: r.SubjectPnDescription,
		RecordDate: nullTime(r.RecordDate), RecordType: r.RecordType, Notes: r.Notes,
		InstrumentType: r.InstrumentType, IsLocked: r.IsLocked, IsApproved: r.IsApproved, IsActive: r.IsActive,
		TestOrder: r.TestOrder, LotID: r.LotID, BuildID: r.BuildID, UnitID: r.UnitID,
	}, nil
}

// ListFormRecords returns a form's active records in the records-table order.
func (s *Service) ListFormRecords(ctx context.Context, formID int) ([]Listed, error) {
	rows, err := s.q.ListFormRecords(ctx, formID)
	if err != nil {
		return nil, err
	}
	out := make([]Listed, len(rows))
	for i, r := range rows {
		out[i] = Listed{
			ID: r.ID, PartID: r.PartID, SerialNumber: r.SerialNumber, SubjectPartNumber: r.SubjectPartNumber,
			SubjectPNDescription: r.SubjectPnDescription, RecordDate: nullTime(r.RecordDate),
			RecordType: r.RecordType, IsLocked: r.IsLocked, IsApproved: r.IsApproved, FormRevision: r.FormRevision,
		}
	}
	return out, nil
}

// ListScopedRecords returns the active records of a part, lot or unit across all forms, newest first.
func (s *Service) ListScopedRecords(ctx context.Context, scope Scope, id int) ([]Listed, error) {
	p := dbq.ListScopedRecordsParams{}
	switch scope {
	case ScopePart:
		p.PartID = &id
	case ScopeLot:
		p.LotID = &id
	case ScopeUnit:
		p.UnitID = &id
	default:
		return nil, errBadScope
	}
	rows, err := s.q.ListScopedRecords(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]Listed, len(rows))
	for i, r := range rows {
		out[i] = Listed{
			ID: r.ID, PartID: r.PartID, SerialNumber: r.SerialNumber, SubjectPartNumber: r.SubjectPartNumber,
			SubjectPNDescription: r.SubjectPnDescription, RecordDate: nullTime(r.RecordDate),
			RecordType: r.RecordType, IsLocked: r.IsLocked, IsApproved: r.IsApproved, FormRevision: r.FormRevision,
			FormID: r.FormID, FormPartNumber: r.FormPartNumber, FormDescription: r.FormDescription,
		}
	}
	return out, nil
}

// ListRecordTypes returns the distinct non-empty types of the active records in a scope.
func (s *Service) ListRecordTypes(ctx context.Context, scope Scope, id int) ([]string, error) {
	p := dbq.ListRecordTypesParams{}
	switch scope {
	case ScopeForm:
		p.FormID = &id
	case ScopePart:
		p.PartID = &id
	case ScopeLot:
		p.LotID = &id
	case ScopeUnit:
		p.UnitID = &id
	default:
		return nil, errBadScope
	}
	return s.q.ListRecordTypes(ctx, p)
}

// Neighbors returns the previous / next active record ids (0 = none) of recordID within its form's
// list order; sql.ErrNoRows when recordID isn't an active record of the form.
func (s *Service) Neighbors(ctx context.Context, formID, recordID int) (prev, next int, err error) {
	r, err := s.q.GetRecordNeighbors(ctx, dbq.GetRecordNeighborsParams{FormID: formID, RecordID: recordID})
	return r.PrevID, r.NextID, err
}

// NextSerial is the suggested next serial for a form: the largest all-digit serial + 1, or 1.
func (s *Service) NextSerial(ctx context.Context, formID int) (int, error) {
	return s.q.NextFormSerial(ctx, formID)
}

func (s *Service) ListResults(ctx context.Context, recordID int) ([]ResultRow, error) {
	return s.q.ListRecordResults(ctx, recordID)
}

func (s *Service) ListEvents(ctx context.Context, recordID int) ([]Event, error) {
	return s.q.ListRecordEvents(ctx, recordID)
}

// ListEventResults returns the completion snapshots' result rows of a record in event order.
func (s *Service) ListEventResults(ctx context.Context, recordID int) ([]EventResult, error) {
	rows, err := s.q.ListRecordEventResults(ctx, recordID)
	if err != nil {
		return nil, err
	}
	out := make([]EventResult, len(rows))
	for i, r := range rows {
		out[i] = EventResult{
			EventID: r.EventID, FormRowID: r.FormRowID, Parameter: r.Parameter, Specification: r.Specification,
			SpecUnits: r.SpecUnits, Result: r.Result, PassFail: nullBool(r.PassFail), Comment: r.Comment,
		}
	}
	return out, nil
}

// GetPartTracking yields sql.ErrNoRows when the id isn't a part.
func (s *Service) GetPartTracking(ctx context.Context, partID int) (PartTracking, error) {
	return s.q.GetPartTracking(ctx, partID)
}

func (s *Service) ListBOMParts(ctx context.Context, parentPartID int) ([]BOMPart, error) {
	return s.q.ListBOMParts(ctx, parentPartID)
}

// ListStepResults returns every active record's result for one step of the form, in list order.
func (s *Service) ListStepResults(ctx context.Context, formID, stepID int) ([]StepResult, error) {
	rows, err := s.q.ListStepReportRows(ctx, dbq.ListStepReportRowsParams{FormID: formID, StepID: stepID})
	if err != nil {
		return nil, err
	}
	out := make([]StepResult, len(rows))
	for i, r := range rows {
		out[i] = StepResult{
			ID: r.ID, SerialNumber: r.SerialNumber, SubjectPartNumber: r.SubjectPartNumber, PartID: r.PartID,
			RecordDate: nullTime(r.RecordDate), Result: r.Result, PassFail: nullBool(r.PassFail),
			Comment: r.Comment, UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

// ── Reports ──────────────────────────────────────────────────────────────────

// ListYieldRecords returns the form's active records in [from, to] (a zero bound is open; to covers its
// whole day) with whether any result failed.
func (s *Service) ListYieldRecords(ctx context.Context, formID int, from, to time.Time) ([]YieldRecord, error) {
	rows, err := s.q.ListYieldRecords(ctx, dbq.ListYieldRecordsParams{FormID: formID, FromDate: optTimestamp(from), ToDate: optDate(to)})
	if err != nil {
		return nil, err
	}
	out := make([]YieldRecord, len(rows))
	for i, r := range rows {
		out[i] = YieldRecord{RecordDate: nullTime(r.RecordDate), AnyFail: r.AnyFail}
	}
	return out, nil
}

// ListFailureModes returns each step's failure tally over the same date range, most failures first.
func (s *Service) ListFailureModes(ctx context.Context, formID int, from, to time.Time) ([]FailureMode, error) {
	return s.q.ListFailureModes(ctx, dbq.ListFailureModesParams{FormID: formID, FromDate: optTimestamp(from), ToDate: optDate(to)})
}
