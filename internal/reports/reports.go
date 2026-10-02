// Package reports is the reporting domain (#190, #246, #225): typed access to the Reports
// dashboard/report queries and the Settings -> Utilities data-integrity checks, via the
// sqlc-generated queries in reports.sql. Everything here is read-only and spans several domains
// (POs, parts, forms/records), which is why it isn't split across their packages.
package reports

import (
	"context"
	"sort"
	"time"

	"arx/internal/dbq"
)

// Service wraps the generated queries over one DBTX.
type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// DateRange is a report's date filter. A zero From/To leaves that side open; To is an inclusive
// day (applied as an exclusive bound on the following day), like recordFilters.
type DateRange struct{ From, To time.Time }

func (r DateRange) bounds() (from, to *time.Time) {
	if !r.From.IsZero() {
		from = &r.From
	}
	if !r.To.IsZero() {
		next := r.To.AddDate(0, 0, 1)
		to = &next
	}
	return from, to
}

// ── Dashboard ──────────────────────────────────────────────────────────────────

// OpenPOCount counts POs in the 'open' lifecycle status.
func (s *Service) OpenPOCount(ctx context.Context) (int, error) { return s.q.CountOpenPOs(ctx) }

// POsReceivedThisMonth counts distinct POs with a line received since the first of today's month.
func (s *Service) POsReceivedThisMonth(ctx context.Context, today time.Time) (int, error) {
	return s.q.CountPOsReceivedThisMonth(ctx, today)
}

// FailureMode is one failing test step on one form.
type FailureMode struct {
	FormID       int
	PartNumber   string
	Parameter    string
	FailureCount int
}

func (s *Service) TopFailureModes(ctx context.Context, limit int) ([]FailureMode, error) {
	rows, err := s.q.ListTopFailureModes(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]FailureMode, len(rows))
	for i, r := range rows {
		out[i] = FailureMode(r)
	}
	return out, nil
}

// FormYield is one form's all-time first-pass yield.
type FormYield struct {
	FormID     int
	PartNumber string
	Total      int
	Passed     int
}

// FPYPct returns the first-pass yield percentage, or 0 if there are no records.
func (y FormYield) FPYPct() float64 {
	if y.Total == 0 {
		return 0
	}
	return float64(y.Passed) / float64(y.Total) * 100
}

// LowestYieldForms lists the forms with the lowest first-pass yield, worst first. A record's
// outcome depends on all of its result rows (any failure fails it), so the per-record flags
// are tallied per form here.
func (s *Service) LowestYieldForms(ctx context.Context, limit int) ([]FormYield, error) {
	rows, err := s.q.ListRecordFailFlags(ctx)
	if err != nil {
		return nil, err
	}
	byForm := make(map[int]*FormYield)
	var order []int
	for _, r := range rows {
		y, ok := byForm[r.FormID]
		if !ok {
			y = &FormYield{FormID: r.FormID, PartNumber: r.PartNumber}
			byForm[r.FormID] = y
			order = append(order, r.FormID)
		}
		y.Total++
		if r.AnyFail == 0 {
			y.Passed++
		}
	}
	out := make([]FormYield, 0, len(order))
	for _, id := range order {
		out = append(out, *byForm[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FPYPct() < out[j].FPYPct() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// StaleWIP is an unlocked record older than the stale threshold.
type StaleWIP struct {
	RecordID   int
	FormID     int
	PartNumber string
	CreatedAt  time.Time
}

// StaleWIPRecords lists unlocked records created at least staleDays ago, oldest first.
func (s *Service) StaleWIPRecords(ctx context.Context, limit, staleDays int) ([]StaleWIP, error) {
	rows, err := s.q.ListStaleWIPRecords(ctx, dbq.ListStaleWIPRecordsParams{StaleDays: staleDays, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]StaleWIP, len(rows))
	for i, r := range rows {
		out[i] = StaleWIP{RecordID: r.ID, FormID: r.FormID, PartNumber: r.PartNumber}
		if r.CreatedAt != nil { // the query's created_at filter excludes NULL
			out[i].CreatedAt = *r.CreatedAt
		}
	}
	return out, nil
}

// PendingApproval is a PO awaiting approval; SubmittedAt is nil when it has no 'submitted' event.
type PendingApproval struct {
	Number      string
	SubmittedAt *time.Time
}

func (s *Service) PendingApprovalPOs(ctx context.Context, limit int) ([]PendingApproval, error) {
	rows, err := s.q.ListPendingApprovalPOs(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PendingApproval, len(rows))
	for i, r := range rows {
		out[i] = PendingApproval{Number: r.Number}
		if r.HasSubmitted {
			at := r.SubmittedAt
			out[i].SubmittedAt = &at
		}
	}
	return out, nil
}

// BelowReorder is a part whose on-hand stock is under its reorder minimum.
type BelowReorder struct {
	PartID      int
	PartNumber  string
	StockOnHand float64
	ReorderMin  float64
}

func (s *Service) BelowReorderParts(ctx context.Context, limit int) ([]BelowReorder, error) {
	rows, err := s.q.ListBelowReorderParts(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]BelowReorder, len(rows))
	for i, r := range rows {
		out[i] = BelowReorder(r)
	}
	return out, nil
}

// RecentPart is a recently modified part (modified_date is a DATE).
type RecentPart struct {
	ID          int
	PartNumber  string
	Description string
	Modified    time.Time
}

func (s *Service) RecentModifiedParts(ctx context.Context, limit int) ([]RecentPart, error) {
	rows, err := s.q.ListRecentModifiedParts(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RecentPart, len(rows))
	for i, r := range rows {
		out[i] = RecentPart{ID: r.ID, PartNumber: r.PartNumber, Description: r.Description}
		if r.ModifiedDate != nil { // the query filters modified_date IS NOT NULL
			out[i].Modified = *r.ModifiedDate
		}
	}
	return out, nil
}

// POEvent is one PO history row with its PO number.
type POEvent struct {
	POID      int
	Number    string
	EventType string
	ToStatus  string
	Action    string
	ChangedAt time.Time
}

func (s *Service) RecentPOEvents(ctx context.Context, limit int) ([]POEvent, error) {
	rows, err := s.q.ListRecentPOEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]POEvent, len(rows))
	for i, r := range rows {
		out[i] = POEvent{POID: r.PoID, Number: r.Number, EventType: r.EventType, ToStatus: r.ToStatus, Action: r.Action, ChangedAt: r.ChangedAt}
	}
	return out, nil
}

// ── Spend / on-time / cycle time ───────────────────────────────────────────────

// SupplierSpend is PO line spend (qty * unit_cost) for one supplier name.
type SupplierSpend struct {
	SupplierName string
	TotalSpend   float64
}

// SpendBySupplier totals spend by supplier over the range (on PO date_ordered), biggest first.
func (s *Service) SpendBySupplier(ctx context.Context, r DateRange) ([]SupplierSpend, error) {
	from, to := r.bounds()
	rows, err := s.q.SpendBySupplier(ctx, dbq.SpendBySupplierParams{DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierSpend, len(rows))
	for i, row := range rows {
		out[i] = SupplierSpend(row)
	}
	return out, nil
}

// PartSpend is PO line spend for one part (or one freeform part-number snapshot).
type PartSpend struct {
	PartNumber  string
	Description string
	TotalSpend  float64
}

func (s *Service) SpendByPart(ctx context.Context, r DateRange) ([]PartSpend, error) {
	from, to := r.bounds()
	rows, err := s.q.SpendByPart(ctx, dbq.SpendByPartParams{DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	out := make([]PartSpend, len(rows))
	for i, row := range rows {
		out[i] = PartSpend(row)
	}
	return out, nil
}

// OnTimeSupplier is one supplier's on-time delivery aggregate. Only lines with both a quoted
// lead time and a receipt date count; OnTimePct is OnTimeLines/TotalLines*100 and AvgDaysLate is
// signed (positive = late).
type OnTimeSupplier struct {
	SupplierName string
	TotalLines   int
	OnTimeLines  int
	OnTimePct    float64
	AvgDaysLate  float64
}

// OnTimeDelivery ranks suppliers worst on-time % first (ties by name): a watchlist report.
func (s *Service) OnTimeDelivery(ctx context.Context, r DateRange) ([]OnTimeSupplier, error) {
	from, to := r.bounds()
	rows, err := s.q.OnTimeDeliveryBySupplier(ctx, dbq.OnTimeDeliveryBySupplierParams{DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	out := make([]OnTimeSupplier, len(rows))
	for i, row := range rows {
		// TotalLines is a COUNT(*) under GROUP BY, so it's always >= 1 here.
		out[i] = OnTimeSupplier{
			SupplierName: row.SupplierName, TotalLines: row.TotalLines, OnTimeLines: row.OnTimeLines,
			OnTimePct:   float64(row.OnTimeLines) / float64(row.TotalLines) * 100,
			AvgDaysLate: row.AvgDaysLate,
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OnTimePct != out[j].OnTimePct {
			return out[i].OnTimePct < out[j].OnTimePct
		}
		return out[i].SupplierName < out[j].SupplierName
	})
	return out, nil
}

// CycleStage is one lifecycle stage's average dwell time (only transitions with a recorded exit).
type CycleStage struct {
	Stage   string
	POCount int
	AvgDays float64
}

// POCycleTime averages the time POs spend in each stage, filtered on stage-entry time.
func (s *Service) POCycleTime(ctx context.Context, r DateRange) ([]CycleStage, error) {
	from, to := r.bounds()
	rows, err := s.q.POCycleTimeByStage(ctx, dbq.POCycleTimeByStageParams{EnteredFrom: from, EnteredTo: to})
	if err != nil {
		return nil, err
	}
	out := make([]CycleStage, len(rows))
	for i, row := range rows {
		out[i] = CycleStage{Stage: row.Stage, POCount: row.PoCount, AvgDays: row.AvgDays}
	}
	return out, nil
}

// ── Data-quality gaps ──────────────────────────────────────────────────────────

// DataQualityPart is one part flagged by a data-quality gap check.
type DataQualityPart struct {
	PartNumber  string
	Description string
	Category    string
}

// PartsNoAttachments lists active BUY/ASM/DWG parts with no attachments.
func (s *Service) PartsNoAttachments(ctx context.Context) ([]DataQualityPart, error) {
	rows, err := s.q.ListPartsNoAttachments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DataQualityPart, len(rows))
	for i, r := range rows {
		out[i] = DataQualityPart(r)
	}
	return out, nil
}

// PartsMissingDefaultSupplier lists active BUY parts with no default supplier.
func (s *Service) PartsMissingDefaultSupplier(ctx context.Context) ([]DataQualityPart, error) {
	rows, err := s.q.ListPartsMissingDefaultSupplier(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DataQualityPart, len(rows))
	for i, r := range rows {
		out[i] = DataQualityPart(r)
	}
	return out, nil
}

// PartsStaleRollup lists active parts whose cost rollup was never computed.
func (s *Service) PartsStaleRollup(ctx context.Context) ([]DataQualityPart, error) {
	rows, err := s.q.ListPartsStaleRollup(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DataQualityPart, len(rows))
	for i, r := range rows {
		out[i] = DataQualityPart(r)
	}
	return out, nil
}

// FormOption is one active form on a per-form report picker.
type FormOption struct {
	ID          int
	PartNumber  string
	Description string
}

func (s *Service) ActiveFormOptions(ctx context.Context) ([]FormOption, error) {
	rows, err := s.q.ListActiveFormOptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FormOption, len(rows))
	for i, r := range rows {
		out[i] = FormOption(r)
	}
	return out, nil
}

// ── Utilities ──────────────────────────────────────────────────────────────────

// AttachmentLink is an active attachment's link (file name / path) with its owner.
type AttachmentLink struct {
	Link  string
	ID    int
	Label string
}

// PartAttachmentLinks lists active part attachments with their part.
func (s *Service) PartAttachmentLinks(ctx context.Context) ([]AttachmentLink, error) {
	rows, err := s.q.ListPartAttachmentLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentLink, len(rows))
	for i, r := range rows {
		out[i] = AttachmentLink(r)
	}
	return out, nil
}

// CompanyAttachmentLinks lists active company attachments with their company.
func (s *Service) CompanyAttachmentLinks(ctx context.Context) ([]AttachmentLink, error) {
	rows, err := s.q.ListCompanyAttachmentLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentLink, len(rows))
	for i, r := range rows {
		out[i] = AttachmentLink(r)
	}
	return out, nil
}

// Orphan is a part whose soft-FK pointer (Value) references a row that no longer exists.
type Orphan struct {
	ID         int
	PartNumber string
	Value      int
}

func (s *Service) OrphanDefaultSuppliers(ctx context.Context) ([]Orphan, error) {
	rows, err := s.q.ListOrphanDefaultSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Orphan, len(rows))
	for i, r := range rows {
		out[i] = Orphan(r)
	}
	return out, nil
}

func (s *Service) OrphanPrices(ctx context.Context) ([]Orphan, error) {
	rows, err := s.q.ListOrphanPrices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Orphan, len(rows))
	for i, r := range rows {
		out[i] = Orphan(r)
	}
	return out, nil
}

func (s *Service) OrphanPrimaryAttachments(ctx context.Context) ([]Orphan, error) {
	rows, err := s.q.ListOrphanPrimaryAttachments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Orphan, len(rows))
	for i, r := range rows {
		out[i] = Orphan(r)
	}
	return out, nil
}

// Labeled is an owner row (part or company) with its display label.
type Labeled struct {
	ID    int
	Label string
}

// PartsWithDeletedPrimary lists parts whose primary attachment is soft-deleted.
func (s *Service) PartsWithDeletedPrimary(ctx context.Context) ([]Labeled, error) {
	rows, err := s.q.ListPartsWithDeletedPrimary(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Labeled, len(rows))
	for i, r := range rows {
		out[i] = Labeled(r)
	}
	return out, nil
}

// CompaniesWithDeletedPrimary lists companies whose primary attachment is soft-deleted.
func (s *Service) CompaniesWithDeletedPrimary(ctx context.Context) ([]Labeled, error) {
	rows, err := s.q.ListCompaniesWithDeletedPrimary(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Labeled, len(rows))
	for i, r := range rows {
		out[i] = Labeled(r)
	}
	return out, nil
}

// POActiveState is a PO's stored status and is_active flag (NULL reads as "" / false).
type POActiveState struct {
	ID       int
	Number   string
	Status   string
	IsActive bool
}

func (s *Service) POActiveStates(ctx context.Context) ([]POActiveState, error) {
	rows, err := s.q.ListPOActiveStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]POActiveState, len(rows))
	for i, r := range rows {
		out[i] = POActiveState(r)
	}
	return out, nil
}
