package main

import (
	"testing"

	"arx/arx_go/models"
)

func boolPtr(b bool) *bool { return &b }

// snap builds a snapshot row for tests.
func snap(testID int, result string, pf *bool, comment string) models.RecordResultSnapshot {
	return models.RecordResultSnapshot{TestID: testID, Result: result, PassFail: pf, Comment: comment}
}

func TestDiffSnapshot_NoPriorIsAllUnchanged(t *testing.T) {
	curr := []models.RecordResultSnapshot{snap(1, "5", boolPtr(true), ""), snap(2, "10", nil, "ok")}
	got := diffSnapshot(curr, nil)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, row := range got {
		if row.Status != "unchanged" {
			t.Errorf("test %d: status = %q, want unchanged (no prior to diff)", row.TestID, row.Status)
		}
	}
}

func TestDiffSnapshot_ChangedResultValue(t *testing.T) {
	prev := []models.RecordResultSnapshot{snap(1, "5", boolPtr(true), "")}
	curr := []models.RecordResultSnapshot{snap(1, "6", boolPtr(true), "")}
	got := diffSnapshot(curr, prev)
	if got[0].Status != "changed" {
		t.Errorf("status = %q, want changed", got[0].Status)
	}
}

func TestDiffSnapshot_ChangedPassFail(t *testing.T) {
	prev := []models.RecordResultSnapshot{snap(1, "5", boolPtr(true), "")}
	curr := []models.RecordResultSnapshot{snap(1, "5", boolPtr(false), "")}
	if diffSnapshot(curr, prev)[0].Status != "changed" {
		t.Errorf("pass_fail change not detected")
	}
}

func TestDiffSnapshot_ChangedComment(t *testing.T) {
	prev := []models.RecordResultSnapshot{snap(1, "5", nil, "old")}
	curr := []models.RecordResultSnapshot{snap(1, "5", nil, "new")}
	if diffSnapshot(curr, prev)[0].Status != "changed" {
		t.Errorf("comment change not detected")
	}
}

func TestDiffSnapshot_AddedRow(t *testing.T) {
	prev := []models.RecordResultSnapshot{snap(1, "5", nil, "")}
	curr := []models.RecordResultSnapshot{snap(1, "5", nil, ""), snap(2, "9", nil, "")}
	got := diffSnapshot(curr, prev)
	if got[0].Status != "unchanged" {
		t.Errorf("test 1 status = %q, want unchanged", got[0].Status)
	}
	if got[1].Status != "added" {
		t.Errorf("test 2 status = %q, want added", got[1].Status)
	}
}

func TestDiffSnapshot_IdenticalIsUnchanged(t *testing.T) {
	prev := []models.RecordResultSnapshot{snap(1, "5", boolPtr(true), "note")}
	curr := []models.RecordResultSnapshot{snap(1, "5", boolPtr(true), "note")}
	if diffSnapshot(curr, prev)[0].Status != "unchanged" {
		t.Errorf("identical row flagged as changed")
	}
}
