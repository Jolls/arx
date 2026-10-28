package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
)

// jsFunc slices `function <name>(` through its closing brace at column 0.
func jsFunc(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "\nfunction "+name+"(")
	if start < 0 {
		t.Fatalf("function %s not found in app.js", name)
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("end of function %s not found", name)
	}
	return src[start : start+end+3]
}

type filterCol struct {
	Raw  string `json:"raw,omitempty"`
	Date *struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"date,omitempty"`
}

type filterCase struct {
	Name  string      `json:"name"`
	Cols  []filterCol `json:"cols"`
	Texts []string    `json:"texts"`
	Want  bool        `json:"want"`
}

const filterDriverJS = `
const cases = JSON.parse(require('fs').readFileSync(0, 'utf8'));
const out = cases.map(c => {
    const cols = c.cols.map(k => k.date
        ? { type: 'date', from: k.date.from, to: k.date.to }
        : makeTextFilter(k.raw || ''));
    return matchesRow({ _text: c.texts }, cols);
});
process.stdout.write(JSON.stringify(out));
`

func runFilterCases(t *testing.T, cases []filterCase) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	b, err := fs.ReadFile(staticFS, "static/shared/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	js := ""
	for _, fn := range []string{"compileGlob", "termMatcher", "parseFilterTerms", "makeTextFilter", "matchesRow"} {
		js += jsFunc(t, src, fn)
	}
	js += filterDriverJS

	in, _ := json.Marshal(cases)
	cmd := exec.Command(node, "-e", js)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var got []bool
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bad node output %q: %v", stdout.String(), err)
	}
	for i, c := range cases {
		if got[i] != c.Want {
			t.Errorf("%s: filter %+v texts %q: got %v, want %v", c.Name, c.Cols, c.Texts, got[i], c.Want)
		}
	}
}

func txt(raw, text string, want bool, name string) filterCase {
	return filterCase{Name: name, Cols: []filterCol{{Raw: raw}}, Texts: []string{text}, Want: want}
}

// Current (pre-#382) behavior that must survive the change.
func TestFilterTermsJS_Characterization(t *testing.T) {
	date := func(from, to string) filterCol {
		return filterCol{Date: &struct {
			From string `json:"from"`
			To   string `json:"to"`
		}{from, to}}
	}
	runFilterCases(t, []filterCase{
		txt("", "anything", true, "empty"),
		txt("   ", "anything", true, "whitespace only"),
		txt("obs", "abc-obs", true, "substring hit"),
		txt("obs", "abc-01", false, "substring miss"),
		txt("OBS", "abc-obs", true, "substring case-insensitive"),
		txt("standard drone", "skyrunner standard drone", true, "inner space hit"),
		txt("standard drone", "standard  drone", false, "inner space exact"),
		txt("abc*", "abc-01", true, "glob prefix hit"),
		txt("abc*", "xabc-01", false, "glob is whole-field"),
		txt("*-01", "abc-01", true, "glob suffix"),
		txt("a?c", "abc", true, "glob ? hit"),
		txt("a?c", "abbc", false, "glob ? single char"),
		txt("a.c*", "abc", false, "glob escapes regex specials"),
		{Name: "date in range", Cols: []filterCol{date("2026-01-01", "2026-01-31")}, Texts: []string{"2026-01-15"}, Want: true},
		{Name: "date out of range", Cols: []filterCol{date("2026-01-01", "2026-01-31")}, Texts: []string{"2026-02-01"}, Want: false},
		{Name: "date empty text", Cols: []filterCol{date("2026-01-01", "2026-01-31")}, Texts: []string{""}, Want: false},
		{Name: "multi-col both match", Cols: []filterCol{{Raw: "a"}, {Raw: "x"}}, Texts: []string{"a1", "x1"}, Want: true},
		{Name: "multi-col one fails", Cols: []filterCol{{Raw: "a"}, {Raw: "x"}}, Texts: []string{"a1", "y1"}, Want: false},
	})
}

// #382: `;`-separated terms, leading `!` excludes.
func TestFilterTermsJS_Terms(t *testing.T) {
	runFilterCases(t, []filterCase{
		txt("!obs", "abc-obs", false, "exclude substring hit"),
		txt("!obs", "abc-01", true, "exclude substring miss"),
		txt("!obs", "", true, "exclude-only, empty text"),
		txt("!*-obs", "abc-obs", false, "exclude glob hit"),
		txt("!*-obs", "abc-obs-2", true, "exclude glob whole-field"),
		txt("!*-obs", "abc-01", true, "exclude glob miss"),
		txt("a; b", "a-b", true, "AND both"),
		txt("a; b", "a-c", false, "AND only a"),
		txt("a; b", "b-c", false, "AND only b"),
		txt("a; !b", "a1", true, "include+exclude ok"),
		txt("a; !b", "ab", false, "include+exclude excluded"),
		txt("a; !b", "b1", false, "include+exclude no include"),
		txt("abc*; *-01; !*-obs", "abc-01", true, "mixed ok"),
		txt("abc*; *-01; !*-obs", "abc-obs", false, "mixed excluded"),
		txt("abc*; *-01; !*-obs", "abc-02", false, "mixed missing include"),
		txt("abc*; *-01; !*-obs", "xabc-01", false, "mixed whole-field"),
		txt("a;b", "a-b", true, "ws none hit"),
		txt("a;b", "a-c", false, "ws none miss"),
		txt("a ; b", "a-b", true, "ws around hit"),
		txt(" a;  b ", "a-b", true, "ws padded hit"),
		txt(" a;  b ", "a-c", false, "ws padded miss"),
		txt("standard drone; !obs", "skyrunner standard drone", true, "inner spaces kept"),
		txt("standard drone; !obs", "standard  drone", false, "inner spaces exact"),
		txt("standard drone; !obs", "standard drone obs", false, "inner spaces excluded"),
		txt("a;;b", "a-b", true, "empty middle term hit"),
		txt("a;;b", "a-c", false, "empty middle term miss"),
		txt("a;", "a1", true, "trailing ; hit"),
		txt("a;", "b1", false, "trailing ; miss"),
		txt(";a", "a1", true, "leading ; hit"),
		txt(";a", "b1", false, "leading ; miss"),
		txt("; ;", "anything", true, "only separators"),
		txt("a; !", "a1", true, "lone ! ignored hit"),
		txt("a; !", "b1", false, "lone ! ignored miss"),
		txt("! obs", "abc-obs", false, "space after ! trimmed, hit"),
		txt("! obs", "abc-01", true, "space after ! trimmed, miss"),
		txt("!*", "anything", false, "!* hides all"),
		txt("a!b", "xa!bx", true, "non-leading ! literal hit"),
		txt("a!b", "ab", false, "non-leading ! literal miss"),
		{Name: "multi-col both pass", Cols: []filterCol{{Raw: "a; !b"}, {Raw: "x"}}, Texts: []string{"a1", "x1"}, Want: true},
		{Name: "multi-col text col fails", Cols: []filterCol{{Raw: "a; !b"}, {Raw: "x"}}, Texts: []string{"ab", "x1"}, Want: false},
		{Name: "multi-col other col fails", Cols: []filterCol{{Raw: "a; !b"}, {Raw: "x"}}, Texts: []string{"a1", "y1"}, Want: false},
	})
}
