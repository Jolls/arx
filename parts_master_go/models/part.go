package models

import (
	"fmt"
	"time"
)

type Part struct {
	PNID             int
	PartNumber       string
	Revision         string
	Title            string
	Detail           string
	Category         string
	HasBOM           bool
	ReleaseStatus    string
	Active           bool
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
	UserField1       string
	UserField2       string
	UserField3       string
	UserField4       string
	UserField5       string
	UserField6       string
	UserField7       string
	UserField8       string
	UserField9       string
	UserField10      string
}

// UserFieldsForEdit returns all 10 PNUser fields for the edit form (including empty ones).
func (p Part) UserFieldsForEdit() []struct{ Name, Label, Value string } {
	raw := [10]struct{ name, val string }{
		{"user_field_1", p.UserField1}, {"user_field_2", p.UserField2}, {"user_field_3", p.UserField3},
		{"user_field_4", p.UserField4}, {"user_field_5", p.UserField5}, {"user_field_6", p.UserField6},
		{"user_field_7", p.UserField7}, {"user_field_8", p.UserField8}, {"user_field_9", p.UserField9},
		{"user_field_10", p.UserField10},
	}
	out := make([]struct{ Name, Label, Value string }, 10)
	for i, f := range raw {
		out[i] = struct{ Name, Label, Value string }{f.name, fmt.Sprintf("User %d", i+1), f.val}
	}
	return out
}

// UserFields returns the non-empty user_field_1–10 values with their labels,
// ready for template iteration.
func (p Part) UserFields() []struct{ Label, Value string } {
	raw := [10]string{
		p.UserField1, p.UserField2, p.UserField3, p.UserField4, p.UserField5,
		p.UserField6, p.UserField7, p.UserField8, p.UserField9, p.UserField10,
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
	PartNumber     string
	Title          string
	Revision       string
	Category       string
	PNCurrentCost  float64
}

type Attachment struct {
	FILID       int
	FILPNID     int
	FILFileName string
	FILPNRev    string
	Category    string
	OrderID     *int
}
