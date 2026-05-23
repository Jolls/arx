package models

import (
	"fmt"
	"time"
)

type Part struct {
	PNID             int
	PNPartNumber     string
	Revision         string
	PNTitle          string
	PNDetail         string
	Category         string
	HasBOM           bool
	PNStatus         string
	PNActive         bool
	PNReqBy          string
	PNNotes          string
	PNDate           *time.Time
	PNDateModified   *time.Time
	PNFILIDPrimary   int
	PNQty            float64
	PNCurrentCost    float64
	PNLastRollupCost float64
	PNLastRollupAt   *time.Time
	PNFILLinks       int
	PNPOLinks        int
	PNUser1          string
	PNUser2          string
	PNUser3          string
	PNUser4          string
	PNUser5          string
	PNUser6          string
	PNUser7          string
	PNUser8          string
	PNUser9          string
	PNUser10         string
}

// UserFieldsForEdit returns all 10 PNUser fields for the edit form (including empty ones).
func (p Part) UserFieldsForEdit() []struct{ Name, Label, Value string } {
	raw := [10]struct{ name, val string }{
		{"PNUser1", p.PNUser1}, {"PNUser2", p.PNUser2}, {"PNUser3", p.PNUser3},
		{"PNUser4", p.PNUser4}, {"PNUser5", p.PNUser5}, {"PNUser6", p.PNUser6},
		{"PNUser7", p.PNUser7}, {"PNUser8", p.PNUser8}, {"PNUser9", p.PNUser9},
		{"PNUser10", p.PNUser10},
	}
	out := make([]struct{ Name, Label, Value string }, 10)
	for i, f := range raw {
		out[i] = struct{ Name, Label, Value string }{f.name, fmt.Sprintf("User %d", i+1), f.val}
	}
	return out
}

// UserFields returns the non-empty PNUser1–10 values with their labels,
// ready for template iteration.
func (p Part) UserFields() []struct{ Label, Value string } {
	raw := [10]string{
		p.PNUser1, p.PNUser2, p.PNUser3, p.PNUser4, p.PNUser5,
		p.PNUser6, p.PNUser7, p.PNUser8, p.PNUser9, p.PNUser10,
	}
	var out []struct{ Label, Value string }
	for i, v := range raw {
		if v != "" {
			out = append(out, struct{ Label, Value string }{
				Label: fmt.Sprintf("User %d", i+1),
				Value: v,
			})
		}
	}
	return out
}

type BOMItem struct {
	PLID           int
	PLItem         int
	PLQty          float64
	PLPartID       int
	PLListID       int
	PNPartNumber   string
	PNTitle        string
	Revision       string
	Category       string
	PNCurrentCost  float64
}

type Attachment struct {
	FILID       int
	FILPNID     int
	FILFileName string
	FILPNRev    string
	FILNotes    string
	OrderID     *int
}
