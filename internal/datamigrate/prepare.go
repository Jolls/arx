package datamigrate

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Column is a target column; DataType is information_schema's data_type.
type Column struct{ Name, DataType string }

// FK is a single-column foreign key of the target schema.
type FK struct{ Child, ChildCol, Parent, ParentCol string }

// Schema is what the loader needs to know about the target database.
type Schema struct {
	Columns  map[string][]Column // base table -> columns in ordinal order
	Identity []tableColumn       // identity columns, sequences to reset
	FKs      []FK
}

// Table is one target table's rows, already transformed, in COPY column order.
type Table struct {
	Name     string
	Cols     []string
	Types    []string
	Rows     [][]string // NullMarker = NULL
	SourceRows int      // rows in the CSV, before any filtering
}

func (t *Table) colIndex(name string) int {
	for i, c := range t.Cols {
		if c == name {
			return i
		}
	}
	return -1
}

// Options are the zones the two source clocks ran in.
type Options struct{ ServerZone, DesktopZone string }

// Prepared is the CSV export mapped onto the target schema.
type Prepared struct {
	Tables        []*Table // in load order
	SkippedConfig []string // "key (reason)" for app_config rows not carried over
	CarriedConfig []string // app_config keys carried over (values are never printed)
}

// Prepare reads <dir>/<table>.csv for every target table, maps source columns onto
// target columns, converts datetimes, and orders the tables for loading. Problems are
// collected so one run reports all of them.
func Prepare(dir string, s *Schema, o Options) (*Prepared, error) {
	zones := map[string]*time.Location{}
	for clock, name := range map[string]string{serverClock: o.ServerZone, desktopClock: o.DesktopZone} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			return nil, fmt.Errorf("%s clock zone %q: %w", clock, name, err)
		}
		zones[clock] = loc
	}

	var names []string
	for t := range s.Columns {
		if !skipTables[t] && !derivedTables[t] {
			names = append(names, t)
		}
	}
	sort.Strings(names)

	var problems []string
	p := &Prepared{}
	byName := map[string]*Table{}
	for _, name := range names {
		t, probs := readTable(dir, name, s.Columns[name], zones)
		problems = append(problems, probs...)
		if t == nil {
			continue
		}
		if name == "app_config" {
			var version string
			p.SkippedConfig, p.CarriedConfig, version = filterAppConfig(t)
			if version != sourceSchemaVersion {
				problems = append(problems, fmt.Sprintf("app_config.csv: source schema_version is %q; this tool loads version %s exports only", version, sourceSchemaVersion))
			}
		}
		byName[name] = t
	}
	if len(problems) > 0 {
		return nil, problemsError("CSV export does not fit the target schema", problems)
	}

	order, err := LoadOrder(names, s.FKs)
	if err != nil {
		return nil, err
	}
	for _, n := range order {
		p.Tables = append(p.Tables, byName[n])
	}
	return p, nil
}

func readTable(dir, name string, target []Column, zones map[string]*time.Location) (*Table, []string) {
	data, err := os.ReadFile(filepath.Join(dir, name+".csv"))
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", name, err)}
	}
	recs, err := readCSV(data)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s.csv: %v", name, err)}
	}
	if len(recs) == 0 {
		return nil, []string{fmt.Sprintf("%s.csv: empty file, it needs at least a header row", name)}
	}
	header := recs[0]

	types := map[string]string{}
	for _, c := range target {
		types[c.Name] = c.DataType
	}
	t := &Table{Name: name}
	var problems []string
	var keep []int // header positions of the kept columns
	for i, src := range header {
		col := src
		if to, ok := columnRenames[name][src]; ok {
			col = to
		} else if droppedColumns[name][src] {
			continue
		}
		typ, ok := types[col]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: source column %q has no target column (add it to spec.go renames/drops)", name, src))
			continue
		}
		t.Cols = append(t.Cols, col)
		t.Types = append(t.Types, typ)
		keep = append(keep, i)
	}

	for n, rec := range recs[1:] {
		line := n + 2
		if len(rec) != len(header) {
			problems = append(problems, fmt.Sprintf("%s.csv record %d: %d fields, header has %d", name, line, len(rec), len(header)))
			continue
		}
		row := make([]string, len(keep))
		for j, i := range keep {
			v, err := convert(name, t.Cols[j], t.Types[j], rec[i], zones)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s.csv record %d column %s: %v", name, line, t.Cols[j], err))
				continue
			}
			row[j] = v
		}
		t.Rows = append(t.Rows, row)
	}
	if len(problems) > 0 {
		return nil, limit(problems)
	}
	t.SourceRows = len(t.Rows)
	return t, nil
}

// convert returns the value to COPY. Only timestamptz needs work; text passes through
// so numerics keep their exact digits.
func convert(table, col, dataType, v string, zones map[string]*time.Location) (string, error) {
	if v == NullMarker {
		return v, nil
	}
	if dataType != "timestamp with time zone" {
		return v, nil
	}
	clock, ok := timestampClocks[tableColumn{table, col}]
	if !ok {
		return "", fmt.Errorf("timestamptz column has no clock classification in spec.go")
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", v, zones[clock])
	if err != nil {
		return "", fmt.Errorf("datetime %q: %w", v, err)
	}
	return ts.Format("2006-01-02 15:04:05.999999-07:00"), nil
}

// filterAppConfig drops the rows that must not be carried over and reports them, along
// with the source's schema_version (read before that row is dropped).
func filterAppConfig(t *Table) (skipped, carried []string, version string) {
	k, v := t.colIndex("setting_key"), t.colIndex("setting_value")
	if k < 0 || v < 0 {
		return nil, nil, ""
	}
	kept := t.Rows[:0]
	for _, row := range t.Rows {
		if row[k] == "schema_version" {
			version = row[v]
		}
		if reason := appConfigSkip(row[k]); reason != "" {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", row[k], reason))
			continue
		}
		carried = append(carried, row[k])
		kept = append(kept, row)
	}
	t.Rows = kept
	return skipped, carried, version
}

// LoadOrder sorts tables so every FK parent loads before its child. Deferred and
// ignored FK columns are not edges. A remaining cycle is an error.
func LoadOrder(tables []string, fks []FK) ([]string, error) {
	loaded := map[string]bool{}
	for _, t := range tables {
		loaded[t] = true
	}
	deferred := map[tableColumn]bool{}
	for _, d := range deferredColumns {
		deferred[d] = true
	}
	deps := map[string]map[string]bool{}
	for _, fk := range fks {
		key := tableColumn{fk.Child, fk.ChildCol}
		if deferred[key] || ignoredFKs[key] || !loaded[fk.Child] || !loaded[fk.Parent] || fk.Child == fk.Parent {
			continue
		}
		if deps[fk.Child] == nil {
			deps[fk.Child] = map[string]bool{}
		}
		deps[fk.Child][fk.Parent] = true
	}
	var order []string
	done := map[string]bool{}
	remaining := append([]string(nil), tables...)
	sort.Strings(remaining)
	for len(remaining) > 0 {
		var next []string
		progressed := false
		for _, t := range remaining {
			ready := true
			for p := range deps[t] {
				if !done[p] {
					ready = false
					break
				}
			}
			if ready {
				order = append(order, t)
				done[t] = true
				progressed = true
			} else {
				next = append(next, t)
			}
		}
		if !progressed {
			return nil, fmt.Errorf("FK cycle among %s: add a deferred column in spec.go", strings.Join(next, ", "))
		}
		remaining = next
	}
	return order, nil
}

// Orphan is a child row whose foreign key matches no parent row.
type Orphan struct {
	FK       FK
	ID, Want string
}

// FindOrphans reports every child row whose FK value has no parent row, across all
// FKs in one pass (the load would otherwise stop at the first).
func FindOrphans(tables []*Table, fks []FK) []Orphan {
	by := map[string]*Table{}
	for _, t := range tables {
		by[t.Name] = t
	}
	parents := map[tableColumn]map[string]bool{}
	keys := func(table, col string) map[string]bool {
		k := tableColumn{table, col}
		if parents[k] == nil {
			set := map[string]bool{}
			t := by[table]
			if i := t.colIndex(col); i >= 0 {
				for _, row := range t.Rows {
					set[row[i]] = true
				}
			}
			parents[k] = set
		}
		return parents[k]
	}
	var out []Orphan
	for _, fk := range fks {
		child, parent := by[fk.Child], by[fk.Parent]
		if child == nil || parent == nil || ignoredFKs[tableColumn{fk.Child, fk.ChildCol}] {
			continue
		}
		ci, idi := child.colIndex(fk.ChildCol), child.colIndex("id")
		if ci < 0 {
			continue
		}
		set := keys(fk.Parent, fk.ParentCol)
		for n, row := range child.Rows {
			v := row[ci]
			if v == NullMarker || set[v] {
				continue
			}
			id := fmt.Sprintf("row %d", n+1)
			if idi >= 0 {
				id = "id " + row[idi]
			}
			out = append(out, Orphan{fk, id, v})
		}
	}
	return out
}

func (o Orphan) String() string {
	return fmt.Sprintf("%s.%s %s -> %s.%s = %s not found", o.FK.Child, o.FK.ChildCol, o.ID, o.FK.Parent, o.FK.ParentCol, o.Want)
}

// Sums returns the exact sum of every numeric column, for the post-load check.
func (t *Table) Sums() map[string]*big.Rat {
	sums := map[string]*big.Rat{}
	for i, typ := range t.Types {
		if typ != "numeric" {
			continue
		}
		sum := new(big.Rat)
		for _, row := range t.Rows {
			if row[i] == NullMarker {
				continue
			}
			if v, ok := new(big.Rat).SetString(row[i]); ok {
				sum.Add(sum, v)
			}
		}
		sums[t.Cols[i]] = sum
	}
	return sums
}

func problemsError(head string, problems []string) error {
	return fmt.Errorf("%s:\n  %s", head, strings.Join(limit(problems), "\n  "))
}

func limit(problems []string) []string {
	const max = 20
	if len(problems) > max {
		return append(problems[:max:max], fmt.Sprintf("... and %d more", len(problems)-max))
	}
	return problems
}
