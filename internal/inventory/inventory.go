// Package inventory is the inventory domain (#190, #222): typed access to the stock ledger
// (inventory_transaction and part.stock_on_hand), lots, builds, serialized units and the
// genealogy edges between them, via the sqlc-generated queries in inventory.sql. Every method
// runs on the DBTX the Service was built over, so a caller inside a transaction (PO receiving,
// stock adjustment, a build) passes its tx and keeps one atomic unit of work.
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"arx/internal/dbq"
)

// Service wraps the generated queries over one DBTX (a connection wrapper or a tx).
type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

func nullText(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// ── Ledger ─────────────────────────────────────────────────────────────────────

// Txn is one ledger movement. Reference and Note are stored NULL when blank; the
// optional ids are nil for movements that don't involve a PO line, lot or build.
type Txn struct {
	PartID    int
	Type      string
	Qty       float64
	Date      time.Time
	Username  string
	Reference string
	Note      string
	POLineID  *int
	LotID     *int
	BuildID   *int
}

// RecordTxn appends the ledger row, then moves the part's cached stock_on_hand by the same
// signed qty.
func (s *Service) RecordTxn(ctx context.Context, t Txn) error {
	if err := s.q.CreateInventoryTxn(ctx, dbq.CreateInventoryTxnParams{
		PartID: t.PartID, TxnType: t.Type, Qty: t.Qty, TxnDate: t.Date, Username: t.Username,
		Reference: nullText(t.Reference), Note: nullText(t.Note),
		PoLineID: t.POLineID, LotID: t.LotID, BuildID: t.BuildID,
	}); err != nil {
		return err
	}
	return s.q.AddPartStock(ctx, dbq.AddPartStockParams{Qty: t.Qty, ID: t.PartID})
}

// LedgerRow is one ledger row with its lot (LotID 0 / LotNumber "" when none).
type LedgerRow struct {
	Type      string
	Qty       float64
	Date      time.Time
	Username  string
	Reference string
	Note      string
	LotID     int
	LotNumber string
}

// ListLedger returns a part's ledger oldest first.
func (s *Service) ListLedger(ctx context.Context, partID int) ([]LedgerRow, error) {
	rows, err := s.q.ListPartLedger(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]LedgerRow, len(rows))
	for i, r := range rows {
		out[i] = LedgerRow{r.TxnType, r.Qty, r.TxnDate, r.Username, r.Reference, r.Note, r.LotID, r.LotNumber}
	}
	return out, nil
}

// ── Lots ───────────────────────────────────────────────────────────────────────

// LotOption is one active lot of a part, for a lot picker. Label is the lot number plus a
// vendor-lot hint.
type LotOption struct {
	ID    int
	Label string
}

// LotCreate groups CreateLot's free-text fields so a positional call can't swap VendorLot
// and Description.
type LotCreate struct {
	LotNumber   string // "" for auto-issued lots: defaults to the lot's own id (#687)
	VendorLot   string // supplier's own lot/batch id; "" when unknown
	Description string // human-readable provenance stored in lot_description
}

// CreateLot inserts one lot and returns its id. A blank LotNumber is then set to the lot's own
// id, which is unique by construction.
func (s *Service) CreateLot(ctx context.Context, partID int, c LotCreate, poLineID *int) (int, error) {
	id, err := s.q.CreateLot(ctx, dbq.CreateLotParams{
		PartID: partID, LotNumber: c.LotNumber, LotDescription: c.Description,
		VendorLotNumber: nullText(c.VendorLot), PoLineID: poLineID,
	})
	if err != nil || c.LotNumber != "" {
		return id, err
	}
	return id, s.q.SetLotNumber(ctx, dbq.SetLotNumberParams{LotNumber: strconv.Itoa(id), ID: id})
}

// ListActiveLots returns a part's active lots, newest first. Empty (not an error) when none.
func (s *Service) ListActiveLots(ctx context.Context, partID int) ([]LotOption, error) {
	rows, err := s.q.ListActiveLots(ctx, partID)
	if err != nil {
		return nil, err
	}
	var out []LotOption
	for _, r := range rows {
		label := r.LotNumber
		if r.VendorLot != "" {
			label += " (vendor " + r.VendorLot + ")"
		}
		out = append(out, LotOption{ID: r.ID, Label: label})
	}
	return out, nil
}

// LotBelongsToPart reports whether lotID is an active lot of partID.
func (s *Service) LotBelongsToPart(ctx context.Context, lotID, partID int) (bool, error) {
	n, err := s.q.CountActivePartLot(ctx, dbq.CountActivePartLotParams{ID: lotID, PartID: partID})
	return n == 1, err
}

// PartHasLot reports whether lotID is a lot of partID, retired or not.
func (s *Service) PartHasLot(ctx context.Context, lotID, partID int) (bool, error) {
	n, err := s.q.CountPartLot(ctx, dbq.CountPartLotParams{ID: lotID, PartID: partID})
	return n == 1, err
}

// PartHasBuild reports whether buildID is a build of partID.
func (s *Service) PartHasBuild(ctx context.Context, buildID, partID int) (bool, error) {
	n, err := s.q.CountPartBuild(ctx, dbq.CountPartBuildParams{ID: buildID, PartID: partID})
	return n == 1, err
}

// RecordGenealogy inserts one lot→lot edge: parentLotID was consumed (qty) into childLotID.
func (s *Service) RecordGenealogy(ctx context.Context, parentLotID, childLotID int, qty float64) error {
	return s.q.CreateGenealogyEdge(ctx, dbq.CreateGenealogyEdgeParams{
		ParentLotID: &parentLotID, ChildLotID: &childLotID, QtyConsumed: qty,
	})
}

// LotRow is one lot joined to its part, for the lot lists and a trace header. LotDescription
// is the provenance stored at creation ("PO <n>", "Build #<id>", "Manual entry").
type LotRow struct {
	ID              int
	LotNumber       string
	VendorLot       string
	PartID          int
	PartNumber      string
	PartDescription string
	LotDescription  string
	Notes           string
	CreatedAt       time.Time
	IsActive        bool
}

// ListPartLots returns every lot of a part, newest first.
func (s *Service) ListPartLots(ctx context.Context, partID int) ([]LotRow, error) {
	rows, err := s.q.ListPartLots(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]LotRow, len(rows))
	for i, r := range rows {
		out[i] = LotRow(r)
	}
	return out, nil
}

// ListRecentPartLots returns a part's n newest lots.
func (s *Service) ListRecentPartLots(ctx context.Context, partID, n int) ([]LotRow, error) {
	rows, err := s.q.ListRecentPartLots(ctx, dbq.ListRecentPartLotsParams{PartID: partID, N: n})
	if err != nil {
		return nil, err
	}
	out := make([]LotRow, len(rows))
	for i, r := range rows {
		out[i] = LotRow(r)
	}
	return out, nil
}

// ListAllLots returns every lot of every part, newest first.
func (s *Service) ListAllLots(ctx context.Context) ([]LotRow, error) {
	rows, err := s.q.ListAllLots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LotRow, len(rows))
	for i, r := range rows {
		out[i] = LotRow(r)
	}
	return out, nil
}

// GetLot loads one lot; found is false (nil error) when it does not exist.
func (s *Service) GetLot(ctx context.Context, id int) (lot LotRow, found bool, err error) {
	r, err := s.q.GetLot(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return LotRow{}, false, nil
	}
	if err != nil {
		return LotRow{}, false, err
	}
	return LotRow(r), true, nil
}

// CountPartLots returns the number of lots of a part.
func (s *Service) CountPartLots(ctx context.Context, partID int) (int, error) {
	n, err := s.q.CountPartLots(ctx, partID)
	return int(n), err
}

// UpdateLot saves a lot's description, vendor lot and notes (blank vendor lot / notes → NULL).
func (s *Service) UpdateLot(ctx context.Context, lotID, partID int, description, vendorLot, notes string) error {
	return s.q.UpdateLot(ctx, dbq.UpdateLotParams{
		LotDescription: description, VendorLotNumber: nullText(vendorLot), Notes: nullText(notes),
		ID: lotID, PartID: partID,
	})
}

// AppendLotNote appends one "[username date] text" entry to a lot's notes. The concatenation
// happens in SQL from just the new text, so a caller holding a stale copy of the field can't
// overwrite entries added meanwhile.
func (s *Service) AppendLotNote(ctx context.Context, lotID int, text, username string) error {
	entry := fmt.Sprintf("[%s %s] %s", username, time.Now().Format("2006-01-02"), strings.TrimSpace(text))
	return s.q.AppendLotNote(ctx, dbq.AppendLotNoteParams{Entry: entry, SeparatedEntry: "\n\n" + entry, ID: lotID})
}

// ── Genealogy trace ────────────────────────────────────────────────────────────

// TraceNode is one lot or unit in a genealogy trace, flattened with Depth. NodeType
// ("lot"|"unit") says which; Number is the lot_number or the unit's serial_number. Qty is
// qty_consumed on the edge connecting this node to its predecessor.
type TraceNode struct {
	NodeType        string
	ID              int
	Number          string
	VendorLot       string // lot nodes only
	Notes           string // lot nodes only
	PartID          int
	PartNumber      string
	PartDescription string
	IsVendorLot     bool // lot nodes only: po_line_id set, a purchased raw lot (a genealogy leaf)
	Qty             float64
	Depth           int
}

// IsUnit reports whether this node is a serialized unit (vs a lot).
func (n TraceNode) IsUnit() bool { return n.NodeType == "unit" }

// TraceRoot is one starting node for a genealogy walk.
type TraceRoot struct {
	ID       int
	NodeType string // "lot" | "unit"
}

// neighbors returns the immediate parents (ancestors) or children of one node, lots then
// units, each ordered by id. The cursor is drained before returning so the caller can recurse.
func (s *Service) neighbors(ctx context.Context, id int, nodeType string, ancestors bool) ([]TraceNode, error) {
	var rows []dbq.ListTraceAncestorsRow
	var err error
	if ancestors {
		rows, err = s.q.ListTraceAncestors(ctx, dbq.ListTraceAncestorsParams{NearType: nodeType, ID: id})
	} else {
		var desc []dbq.ListTraceDescendantsRow
		desc, err = s.q.ListTraceDescendants(ctx, dbq.ListTraceDescendantsParams{NearType: nodeType, ID: id})
		for _, r := range desc {
			rows = append(rows, dbq.ListTraceAncestorsRow(r))
		}
	}
	if err != nil {
		return nil, err
	}
	var out []TraceNode
	for _, r := range rows {
		out = append(out, TraceNode{r.NodeType, r.ID, r.Number, r.VendorLot, r.Notes, r.PartID, r.PartNumber, r.PartDescription, r.IsVendorLot, r.Qty, 0})
	}
	return out, nil
}

// Trace walks the genealogy from one or more roots and returns the reachable nodes flattened
// depth-first (ancestors: parents down to raw vendor lots / root units; otherwise children).
// One visited set, keyed by (NodeType, ID) since lot and unit ids are independent spaces, is
// shared across roots so a node reachable from several is expanded once.
func (s *Service) Trace(ctx context.Context, roots []TraceRoot, ancestors bool) ([]TraceNode, error) {
	type key struct {
		nodeType string
		id       int
	}
	var out []TraceNode
	visited := map[key]bool{}
	for _, rt := range roots {
		visited[key{rt.NodeType, rt.ID}] = true
	}
	var walk func(id int, nodeType string, depth int) error
	walk = func(id int, nodeType string, depth int) error {
		neighbors, err := s.neighbors(ctx, id, nodeType, ancestors)
		if err != nil {
			return err
		}
		for _, n := range neighbors {
			n.Depth = depth
			out = append(out, n)
			k := key{n.NodeType, n.ID}
			if !visited[k] {
				visited[k] = true
				if err := walk(n.ID, n.NodeType, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, rt := range roots {
		if err := walk(rt.ID, rt.NodeType, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ── Units ──────────────────────────────────────────────────────────────────────

// UnitRow is one serialized unit joined to its part and (nullable) lot; LotNumber is "" when
// LotID is nil. Source is "test" or "manual".
type UnitRow struct {
	ID              int
	SerialNumber    string
	PartID          int
	PartNumber      string
	PartDescription string
	LotID           *int
	LotNumber       string
	BuildID         *int
	IsActive        bool
	CreatedAt       time.Time
	Source          string
}

// IsManual reports whether this unit was manually back-filled.
func (u UnitRow) IsManual() bool { return u.Source == "manual" }

// LotIDVal / BuildIDVal return the dereferenced id (0 when nil) for template links; templates
// gate on {{if .LotID}} first, so 0 is never rendered as a live link.
func (u UnitRow) LotIDVal() int {
	if u.LotID != nil {
		return *u.LotID
	}
	return 0
}
func (u UnitRow) BuildIDVal() int {
	if u.BuildID != nil {
		return *u.BuildID
	}
	return 0
}

// ListPartUnits returns every unit of a part, newest first.
func (s *Service) ListPartUnits(ctx context.Context, partID int) ([]UnitRow, error) {
	rows, err := s.q.ListPartUnits(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]UnitRow, len(rows))
	for i, r := range rows {
		out[i] = UnitRow(r)
	}
	return out, nil
}

// ListRecentPartUnits returns a part's n newest units.
func (s *Service) ListRecentPartUnits(ctx context.Context, partID, n int) ([]UnitRow, error) {
	rows, err := s.q.ListRecentPartUnits(ctx, dbq.ListRecentPartUnitsParams{PartID: partID, N: n})
	if err != nil {
		return nil, err
	}
	out := make([]UnitRow, len(rows))
	for i, r := range rows {
		out[i] = UnitRow(r)
	}
	return out, nil
}

// GetUnit loads one unit; found is false (nil error) when it does not exist.
func (s *Service) GetUnit(ctx context.Context, id int) (unit UnitRow, found bool, err error) {
	r, err := s.q.GetUnit(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return UnitRow{}, false, nil
	}
	if err != nil {
		return UnitRow{}, false, err
	}
	return UnitRow(r), true, nil
}

// CountPartUnits returns the number of units of a part.
func (s *Service) CountPartUnits(ctx context.Context, partID int) (int, error) {
	n, err := s.q.CountPartUnits(ctx, partID)
	return int(n), err
}

// UnitSerialLocked reports whether a unit's serial is frozen: true once any locked test
// record points at it.
func (s *Service) UnitSerialLocked(ctx context.Context, unitID int) (bool, error) {
	n, err := s.q.CountLockedUnitRecords(ctx, &unitID)
	return n > 0, err
}

// CreateManualUnit mints a source='manual' unit and returns its id. A duplicate serial fails
// with the unmodified database error (constraint uq_unit_serial).
func (s *Service) CreateManualUnit(ctx context.Context, partID int, serial string, lotID, buildID *int) (int, error) {
	return s.q.CreateManualUnit(ctx, dbq.CreateManualUnitParams{PartID: partID, SerialNumber: serial, LotID: lotID, BuildID: buildID})
}

// SetUnitActive saves only the scrap flag.
func (s *Service) SetUnitActive(ctx context.Context, unitID, partID int, active bool) error {
	return s.q.SetUnitActive(ctx, dbq.SetUnitActiveParams{IsActive: active, ID: unitID, PartID: partID})
}

// UpdateUnit saves the scrap flag and the serial.
func (s *Service) UpdateUnit(ctx context.Context, unitID, partID int, serial string, active bool) error {
	return s.q.UpdateUnit(ctx, dbq.UpdateUnitParams{IsActive: active, SerialNumber: serial, ID: unitID, PartID: partID})
}

// UpsertTestUnit finds or creates the unit for (partID, serial). Provenance is set only on
// creation, so a retest re-links the existing unit unchanged.
func (s *Service) UpsertTestUnit(ctx context.Context, partID int, serial string, buildID, lotID *int) (int, error) {
	id, err := s.q.GetUnitIDBySerial(ctx, dbq.GetUnitIDBySerialParams{PartID: partID, SerialNumber: serial})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return s.q.CreateTestUnit(ctx, dbq.CreateTestUnitParams{PartID: partID, SerialNumber: serial, BuildID: buildID, LotID: lotID})
}

// UnitProvenance returns a unit's lot and build; both nil when the unit doesn't exist.
func (s *Service) UnitProvenance(ctx context.Context, unitID int) (lotID, buildID *int, err error) {
	r, err := s.q.GetUnitProvenance(ctx, unitID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	return r.LotID, r.BuildID, err
}

// ── Builds ─────────────────────────────────────────────────────────────────────

// BuildOption is one build, labelled for a picker or badge.
type BuildOption struct {
	ID    int
	Label string
}

// buildOptionLabel formats a build, e.g. "Build #12 — qty 1 — 2026-05-25".
func buildOptionLabel(id int, qty float64, date time.Time) string {
	return fmt.Sprintf("Build #%d — qty %g — %s", id, qty, date.Format("2006-01-02"))
}

// ListBuildOptions returns a part's builds, newest first. Empty (not an error) when none.
func (s *Service) ListBuildOptions(ctx context.Context, partID int) ([]BuildOption, error) {
	rows, err := s.q.ListPartBuilds(ctx, partID)
	if err != nil {
		return nil, err
	}
	var out []BuildOption
	for _, r := range rows {
		out = append(out, BuildOption{r.ID, buildOptionLabel(r.ID, r.Qty, r.BuildDate)})
	}
	return out, nil
}

// GetBuildOption loads one build; nil (no error) when it does not exist.
func (s *Service) GetBuildOption(ctx context.Context, id int) (*BuildOption, error) {
	r, err := s.q.GetBuild(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &BuildOption{r.ID, buildOptionLabel(r.ID, r.Qty, r.BuildDate)}, nil
}

// BuildHistoryRow is one past build. TestedCount is the serialized units minted for it so
// far, excluding manual back-fills.
type BuildHistoryRow struct {
	ID          int
	Qty         float64
	Date        time.Time
	Username    string
	Note        string
	TestedCount int
}

// ListBuildHistory returns a part's builds, newest first.
func (s *Service) ListBuildHistory(ctx context.Context, partID int) ([]BuildHistoryRow, error) {
	rows, err := s.q.ListBuildHistory(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]BuildHistoryRow, len(rows))
	for i, r := range rows {
		out[i] = BuildHistoryRow{r.ID, r.Qty, r.BuildDate, r.Username, r.Note, r.TestedCount}
	}
	return out, nil
}

// BuildComponent is one BOM line of an output part with the component's on-hand.
type BuildComponent struct {
	PartID       int
	PartNumber   string
	Description  string
	Category     string
	QtyPer       float64
	StockOnHand  float64
	TrackingMode string
}

// ListBuildComponents returns the output part's BOM lines in line order.
func (s *Service) ListBuildComponents(ctx context.Context, outputPartID int) ([]BuildComponent, error) {
	rows, err := s.q.ListBuildComponents(ctx, outputPartID)
	if err != nil {
		return nil, err
	}
	out := make([]BuildComponent, len(rows))
	for i, r := range rows {
		out[i] = BuildComponent{r.ComponentPartID, r.PartNumber, r.Description, r.Category, r.Qty, r.StockOnHand, r.TrackingMode}
	}
	return out, nil
}

// BuildLine is one BOM component line loaded for a build.
type BuildLine struct {
	ComponentPartID int
	PartNumber      string
	Qty             float64
	Category        string
	TrackingMode    string
}

// ListBuildLines returns the output part's BOM component lines. Empty when it has no BOM.
func (s *Service) ListBuildLines(ctx context.Context, partID int) ([]BuildLine, error) {
	rows, err := s.q.ListBuildLines(ctx, partID)
	if err != nil {
		return nil, err
	}
	out := make([]BuildLine, len(rows))
	for i, r := range rows {
		out[i] = BuildLine{r.ComponentPartID, r.PartNumber, r.Qty, r.Category, r.TrackingMode}
	}
	return out, nil
}

// CreateBuild records a build event (output lot unset) and returns its id.
func (s *Service) CreateBuild(ctx context.Context, partID int, qty float64, date time.Time, username, note string) (int, error) {
	return s.q.CreateBuild(ctx, dbq.CreateBuildParams{PartID: partID, Qty: qty, BuildDate: date, Username: username, Note: nullText(note)})
}

// SetBuildOutputLot links a build to the lot it produced.
func (s *Service) SetBuildOutputLot(ctx context.Context, buildID, lotID int) error {
	return s.q.SetBuildOutputLot(ctx, dbq.SetBuildOutputLotParams{OutputLotID: &lotID, ID: buildID})
}

// LockRecord takes a row lock on a test record for the rest of the transaction. A missing
// record is not an error.
func (s *Service) LockRecord(ctx context.Context, recordID int) error {
	if _, err := s.q.LockRecord(ctx, recordID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// LinkRecordToBuild points a test record of partID at a build (and its output lot, if any),
// only while the record is unlocked. linked is false when nothing matched: missing, a
// different part, or locked.
func (s *Service) LinkRecordToBuild(ctx context.Context, recordID, partID int, lotID *int, buildID int) (linked bool, err error) {
	n, err := s.q.LinkRecordToBuild(ctx, dbq.LinkRecordToBuildParams{LotID: lotID, BuildID: &buildID, ID: recordID, PartID: &partID})
	return n > 0, err
}
