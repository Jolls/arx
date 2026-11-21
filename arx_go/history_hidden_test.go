package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os/exec"
	"testing"
)

// hiddenChangedSteps (static/records/app.js, #484) drives the "N changed steps are hidden" note.
func TestHiddenChangedStepsJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	b, err := fs.ReadFile(staticFS, "static/records/app.js")
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		ID       int  `json:"id"`
		Changed  bool `json:"changed"`
		Archived bool `json:"archived"`
		Existed  bool `json:"existed"`
	}
	type result struct{ Count, Archived int }
	cases := []struct {
		Name         string `json:"name"`
		Steps        []step `json:"steps"`
		Rows         []int  `json:"rows"`
		ShowArchived bool   `json:"showArchived"`
		want         result
	}{
		{"none hidden", []step{{1, true, false, true}, {2, false, false, true}}, []int{1, 2}, false, result{0, 0}},
		{"changed archived, switch off", []step{{1, true, true, true}}, []int{1}, false, result{1, 1}},
		{"changed archived, switch on", []step{{1, true, true, true}}, []int{1}, true, result{0, 0}},
		{"changed, not in table", []step{{1, true, false, true}}, []int{}, false, result{1, 0}},
		{"changed archived, not in table", []step{{1, true, true, true}}, []int{}, false, result{1, 0}},
		{"unchanged archived", []step{{1, false, true, true}}, []int{1}, false, result{0, 0}},
		{"not yet existing", []step{{1, true, false, false}}, []int{}, false, result{0, 0}},
	}
	js := jsFunc(t, string(b), "hiddenChangedSteps") + `
const cases = JSON.parse(require('fs').readFileSync(0, 'utf8'));
process.stdout.write(JSON.stringify(cases.map(c => hiddenChangedSteps(c.steps, new Set(c.rows), c.showArchived))));
`
	in, _ := json.Marshal(cases)
	cmd := exec.Command(node, "-e", js)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var got []result
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bad node output %q: %v", stdout.String(), err)
	}
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("%s: got %+v, want %+v", c.Name, got[i], c.want)
		}
	}
}
