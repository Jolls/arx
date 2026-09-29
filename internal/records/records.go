// Package records is the test-record domain (#190, #223, #249): typed access to forms, steps, records,
// results, the audit trail and the named_queries table via the sqlc-generated queries in records.sql.
// Every method runs on the DBTX the Service was built over: handlers that own a transaction build the
// Service over it (records.New(tx)), so statement order, row locks and rollback stay the handler's.
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

// nullText stores "" as NULL.
func nullText(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func optText(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func optBool(b *bool) sql.NullBool {
	if b == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *b, Valid: true}
}

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

// ── Record writes ────────────────────────────────────────────────────────────

// NewRecord is a record to create; PartID nil = no subject part.
type NewRecord = dbq.InsertRecordParams

// LockSerialAllocation takes the (namespace, formID) advisory lock that serializes auto serial allocation;
// it is held until the surrounding tx ends.
func (s *Service) LockSerialAllocation(ctx context.Context, namespace, formID int) error {
	return s.q.LockSerialAllocation(ctx, dbq.LockSerialAllocationParams{Namespace: namespace, FormID: formID})
}

// GetPartLabel returns a part's number and description; sql.ErrNoRows when it isn't a part.
func (s *Service) GetPartLabel(ctx context.Context, id int) (partNumber, description string, err error) {
	r, err := s.q.GetPartLabel(ctx, id)
	return r.PartNumber, r.Description, err
}

func (s *Service) InsertRecord(ctx context.Context, r NewRecord) (int, error) {
	return s.q.InsertRecord(ctx, r)
}

// DuplicateRecord creates a fresh WIP re-test of record id (see the query) and returns its id; sql.ErrNoRows
// when there is no such record. Results are copied separately (CopyResults).
func (s *Service) DuplicateRecord(ctx context.Context, id int) (int, error) {
	return s.q.DuplicateRecord(ctx, id)
}

// CopyResults copies every result row of one record onto another.
func (s *Service) CopyResults(ctx context.Context, fromRecordID, toRecordID int) error {
	return s.q.CopyRecordResults(ctx, dbq.CopyRecordResultsParams{FromRecordID: fromRecordID, ToRecordID: toRecordID})
}

// ResultDef is a step definition as frozen onto a result row (tokens already baked).
type ResultDef struct {
	Type                                                  int
	Parameter, Specification, SpecMin, SpecNom, SpecMax   string
	SpecUnits, PFType, Format, HideFormula, DefaultResult string
}

// InsertResult materializes a step's definition onto a record. result / comment / passFail nil = NULL (a
// fresh row).
func (s *Service) InsertResult(ctx context.Context, recordID, stepID int, d ResultDef, result, comment *string, passFail *bool) error {
	return s.q.InsertResult(ctx, dbq.InsertResultParams{
		RecordID: recordID, StepID: stepID, Type: d.Type, Parameter: d.Parameter, Specification: d.Specification,
		SpecMin: d.SpecMin, SpecNom: d.SpecNom, SpecMax: d.SpecMax, SpecUnits: d.SpecUnits, PfType: d.PFType,
		Format: d.Format, HideFormula: d.HideFormula, DefaultResult: d.DefaultResult,
		Result: optText(result), Comment: optText(comment), PassFail: optBool(passFail),
	})
}

// RefreshResult re-pulls a definition into an existing result row with a recomputed pass/fail.
func (s *Service) RefreshResult(ctx context.Context, id int, d ResultDef, passFail *bool) error {
	return s.q.RefreshResult(ctx, dbq.RefreshResultParams{
		ID: id, Type: d.Type, Parameter: d.Parameter, Specification: d.Specification, SpecMin: d.SpecMin,
		SpecNom: d.SpecNom, SpecMax: d.SpecMax, SpecUnits: d.SpecUnits, PfType: d.PFType, Format: d.Format,
		HideFormula: d.HideFormula, DefaultResult: d.DefaultResult, PassFail: optBool(passFail),
	})
}

func (s *Service) UpdateResultValue(ctx context.Context, id int, result, comment string, passFail *bool) error {
	return s.q.UpdateResultValue(ctx, dbq.UpdateResultValueParams{ID: id, Result: result, Comment: comment, PassFail: optBool(passFail)})
}

// ClaimRecord takes a WIP record's row lock for the rest of the tx (#191); false = locked or missing.
func (s *Service) ClaimRecord(ctx context.Context, id int) (bool, error) {
	n, err := s.q.ClaimRecord(ctx, id)
	return n > 0, err
}

// RecordSave is the record-level part of a results save. A nil RecordDate keeps the stored date; Notes ""
// stores NULL; nil ids store NULL.
type RecordSave struct {
	RecordDate                        *time.Time
	RecordType, Notes, InstrumentType string
	LotID, BuildID, UnitID            *int
}

func (s *Service) UpdateRecordAfterSave(ctx context.Context, id int, r RecordSave) error {
	p := dbq.UpdateRecordAfterSaveParams{ID: id, RecordType: r.RecordType, Notes: nullText(r.Notes),
		InstrumentType: r.InstrumentType, LotID: r.LotID, BuildID: r.BuildID, UnitID: r.UnitID}
	if r.RecordDate != nil {
		p.RecordDate = sql.NullTime{Time: *r.RecordDate, Valid: true}
	}
	return s.q.UpdateRecordAfterSave(ctx, p)
}

// ResyncRecordHeader re-stamps a record's step order and form revision after a resync.
func (s *Service) ResyncRecordHeader(ctx context.Context, id int, testOrder string, formRevision int) error {
	return s.q.ResyncRecordHeader(ctx, dbq.ResyncRecordHeaderParams{ID: id, TestOrder: testOrder, FormRevision: formRevision})
}

// CompleteRecord moves a WIP record to Complete, taking its row lock; formID > 0 also requires it to be one of
// that form's records. False = nothing changed.
func (s *Service) CompleteRecord(ctx context.Context, id, formID int) (bool, error) {
	p := dbq.CompleteRecordParams{ID: id}
	if formID > 0 {
		p.FormID = &formID
	}
	n, err := s.q.CompleteRecord(ctx, p)
	return n > 0, err
}

// ApproveRecord approves a Complete record; false = not Complete (or already approved).
func (s *Service) ApproveRecord(ctx context.Context, id int) (bool, error) {
	n, err := s.q.ApproveRecord(ctx, id)
	return n > 0, err
}

// UnlockRecord returns a locked record to WIP (clearing approval); an approved one only when
// mayUnlockApproved. False = nothing changed.
func (s *Service) UnlockRecord(ctx context.Context, id int, mayUnlockApproved bool) (bool, error) {
	n, err := s.q.UnlockRecord(ctx, dbq.UnlockRecordParams{ID: id, MayUnlockApproved: mayUnlockApproved})
	return n > 0, err
}

// InsertRecordEvent logs a lifecycle event and returns its id; comments "" stores NULL.
func (s *Service) InsertRecordEvent(ctx context.Context, recordID int, eventType, username, comments string) (int, error) {
	return s.q.InsertRecordEvent(ctx, dbq.InsertRecordEventParams{RecordID: recordID, EventType: eventType,
		Username: username, Comments: nullText(comments)})
}

// InsertEventResult adds one row to a completion snapshot.
func (s *Service) InsertEventResult(ctx context.Context, e EventResult) error {
	return s.q.InsertEventResult(ctx, dbq.InsertEventResultParams{EventID: e.EventID, FormRowID: e.FormRowID,
		Parameter: e.Parameter, Specification: e.Specification, SpecUnits: e.SpecUnits, Result: e.Result,
		PassFail: optBool(e.PassFail), Comment: e.Comment})
}

// ── Form writes ──────────────────────────────────────────────────────────────

// SetAuditUser sets the tx-local username the form_row history trigger (trg_form_row_history) attributes
// changes to. Callers treat it as best-effort: a failure only leaves the history row unattributed.
func (s *Service) SetAuditUser(ctx context.Context, username string) error {
	return s.q.SetAuditUser(ctx, username)
}

// LockForm releases a form (locks it and bumps its revision); false = it was already locked.
func (s *Service) LockForm(ctx context.Context, id int) (bool, error) {
	n, err := s.q.LockForm(ctx, id)
	return n > 0, err
}

// UnlockForm unlocks a form; false = it wasn't locked.
func (s *Service) UnlockForm(ctx context.Context, id int) (bool, error) {
	n, err := s.q.UnlockForm(ctx, id)
	return n > 0, err
}

// InsertFormEvent logs a form lifecycle event; comments "" stores NULL.
func (s *Service) InsertFormEvent(ctx context.Context, formID int, eventType, username, comments string) error {
	return s.q.InsertFormEvent(ctx, dbq.InsertFormEventParams{FormID: formID, EventType: eventType,
		Username: username, Comments: nullText(comments)})
}

// StepDef is a step's editable fields as posted. On write "" stores NULL, except where UpdateStep /
// InsertStep say otherwise.
type StepDef struct {
	Type                                                                   int
	Parameter, Specification, SpecNom, SpecMin, SpecMax, SpecUnits, PFType string
	DefaultResult, HideFormula, Category, SheetName, InstrumentTypes       string
	Format, Comment                                                        string
}

// UpdateStep saves one of formID's steps; parameter and specification are stored as given ("" stays "").
func (s *Service) UpdateStep(ctx context.Context, formID, id int, d StepDef) error {
	return s.q.UpdateStep(ctx, dbq.UpdateStepParams{
		ID: id, FormID: formID, Type: d.Type, Parameter: d.Parameter, Specification: d.Specification,
		SpecNom: nullText(d.SpecNom), SpecMin: nullText(d.SpecMin), SpecMax: nullText(d.SpecMax),
		SpecUnits: nullText(d.SpecUnits), PfType: nullText(d.PFType), DefaultResult: nullText(d.DefaultResult),
		HideFormula: nullText(d.HideFormula), Category: nullText(d.Category), SheetName: nullText(d.SheetName),
		InstrumentTypes: nullText(d.InstrumentTypes), Format: nullText(d.Format), Comment: nullText(d.Comment),
	})
}

// InsertStep adds a step to formID and returns its id; parameter is stored as given ("" stays "").
func (s *Service) InsertStep(ctx context.Context, formID int, d StepDef) (int, error) {
	return s.q.InsertStep(ctx, dbq.InsertStepParams{
		FormID: formID, Type: d.Type, Parameter: d.Parameter, Specification: nullText(d.Specification),
		SpecNom: nullText(d.SpecNom), SpecMin: nullText(d.SpecMin), SpecMax: nullText(d.SpecMax),
		SpecUnits: nullText(d.SpecUnits), PfType: nullText(d.PFType), DefaultResult: nullText(d.DefaultResult),
		HideFormula: nullText(d.HideFormula), Category: nullText(d.Category), SheetName: nullText(d.SheetName),
		InstrumentTypes: nullText(d.InstrumentTypes), Format: nullText(d.Format), Comment: nullText(d.Comment),
	})
}

// CopyStep copies one of fromFormID's steps into toFormID and returns the new id; sql.ErrNoRows when stepID
// isn't one of fromFormID's steps.
func (s *Service) CopyStep(ctx context.Context, fromFormID, stepID, toFormID int) (int, error) {
	return s.q.CopyStep(ctx, dbq.CopyStepParams{FromFormID: fromFormID, StepID: stepID, ToFormID: toFormID})
}

func (s *Service) SetStepArchived(ctx context.Context, formID, id int, archived bool) error {
	return s.q.SetStepArchived(ctx, dbq.SetStepArchivedParams{ID: id, FormID: formID, Archived: archived})
}

func (s *Service) SetFormTestOrder(ctx context.Context, id int, testOrder string) error {
	return s.q.SetFormTestOrder(ctx, dbq.SetFormTestOrderParams{ID: id, TestOrder: testOrder})
}

// SetFormTypes saves a form's allowed record / instrument types; "" stores NULL (free text).
func (s *Service) SetFormTypes(ctx context.Context, id int, recordTypes, instrumentTypes string) error {
	return s.q.SetFormTypes(ctx, dbq.SetFormTypesParams{ID: id, RecordTypes: nullText(recordTypes),
		InstrumentTypes: nullText(instrumentTypes)})
}

// IsFormPart reports whether partID is an active FORM-category part.
func (s *Service) IsFormPart(ctx context.Context, partID int) (bool, error) {
	return s.q.IsFormPart(ctx, partID)
}

// InsertForm creates an empty form for partID, copying record / instrument types from sourceID (0 = none).
func (s *Service) InsertForm(ctx context.Context, partID, sourceID int) (int, error) {
	return s.q.InsertForm(ctx, dbq.InsertFormParams{PartNumberID: partID, SourceID: sourceID})
}

// ── Named queries (#250) ─────────────────────────────────────────────────────

type (
	NamedQueryInfo  = dbq.ListActiveNamedQueriesRow
	NamedQuery      = dbq.ListNamedQueriesRow
	NamedQueryInput = dbq.InsertNamedQueryParams
)

// ListActiveNamedQueries returns the active named queries (no SQL) by name.
func (s *Service) ListActiveNamedQueries(ctx context.Context) ([]NamedQueryInfo, error) {
	return s.q.ListActiveNamedQueries(ctx)
}

// ListNamedQueries returns every named query, active or not, by name.
func (s *Service) ListNamedQueries(ctx context.Context) ([]NamedQuery, error) {
	return s.q.ListNamedQueries(ctx)
}

// GetActiveNamedQuery returns an active named query's SQL and result type; sql.ErrNoRows when there is none.
func (s *Service) GetActiveNamedQuery(ctx context.Context, name string) (sqlText, resultType string, err error) {
	r, err := s.q.GetActiveNamedQuery(ctx, name)
	return r.Sql, r.ResultType, err
}

func (s *Service) InsertNamedQuery(ctx context.Context, q NamedQueryInput) (int, error) {
	return s.q.InsertNamedQuery(ctx, q)
}

// UpdateNamedQuery saves a named query; false = no such id.
func (s *Service) UpdateNamedQuery(ctx context.Context, id int, q NamedQueryInput) (bool, error) {
	n, err := s.q.UpdateNamedQuery(ctx, dbq.UpdateNamedQueryParams{ID: id, Name: q.Name, Description: q.Description,
		Sql: q.Sql, Params: q.Params, ResultType: q.ResultType, IsActive: q.IsActive})
	return n > 0, err
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
