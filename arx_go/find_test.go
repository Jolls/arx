package main

import (
	"html/template"
	"strings"
	"testing"

	"arx/internal/contacts"
	"arx/internal/inventory"
)

func TestFindURL(t *testing.T) {
	for _, c := range []struct {
		r    inventory.FindResult
		want string
	}{
		{inventory.FindResult{Kind: "lot", PartID: 3012, ID: 8302}, "/part/3012/lots/8302"},
		{inventory.FindResult{Kind: "unit", PartID: 3013, ID: 8501}, "/part/3013/units/8501"},
		{inventory.FindResult{Kind: "build", PartID: 3013, ID: 8203, OutputLotID: 8306}, "/part/3013/lots/8306"},
		{inventory.FindResult{Kind: "build", PartID: 3005, ID: 8201}, "/part/3005/build"},
	} {
		if got := findURL(c.r); got != c.want {
			t.Errorf("findURL(%+v) = %q; want %q", c.r, got, c.want)
		}
	}
}

func TestMatchedLabel(t *testing.T) {
	for _, c := range []struct {
		r    inventory.FindResult
		want string
	}{
		{inventory.FindResult{Kind: "lot", MatchedID: true, MatchedName: true}, "ID, Lot number"},
		{inventory.FindResult{Kind: "lot", MatchedName: true}, "Lot number"},
		{inventory.FindResult{Kind: "unit", MatchedName: true}, "Serial number"},
		{inventory.FindResult{Kind: "unit", MatchedID: true}, "ID"},
		{inventory.FindResult{Kind: "build", MatchedID: true}, "ID"},
	} {
		if got := matchedLabel(c.r); got != c.want {
			t.Errorf("matchedLabel(%+v) = %q; want %q", c.r, got, c.want)
		}
	}
}

func renderFind(t *testing.T, data map[string]any) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS,
		"templates/shared/layout.html",
		"templates/shared/partials.html",
		"templates/parts/find.html",
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data["ActiveTab"] = "records"
	return renderToString(t, tmpl, "layout", coreLayoutFakeData(data))
}

func TestFindTemplateRenders(t *testing.T) {
	out := renderFind(t, map[string]any{"NotFound": true, "Q": "<b>x</b>"})
	if !strings.Contains(out, "No record found for") || !strings.Contains(out, "&lt;b&gt;x&lt;/b&gt;") || strings.Contains(out, "<b>x</b>") {
		t.Errorf("not-found message missing or unescaped:\n%s", out)
	}

	out = renderFind(t, map[string]any{"Q": "8301", "Results": []findRow{
		{Kind: "Lot", Label: "Lot 8301", Number: "LOT-X1", PartNumber: "RAW-1002", PartDescription: "Bar stock", Matched: "ID, Lot number", URL: "/part/3007/lots/8301"},
		{Kind: "Unit", Label: "Unit 8505", Number: "SER-Y2", PartNumber: "ASM-1002", PartDescription: "Sub", Matched: "Serial number", URL: "/part/3012/units/8505"},
	}})
	for _, want := range []string{`href="/part/3007/lots/8301"`, `href="/part/3012/units/8505"`, "Lot 8301", "Unit 8505", "ID, Lot number", "Serial number", "LOT-X1", "SER-Y2"} {
		if !strings.Contains(out, want) {
			t.Errorf("pick list missing %q", want)
		}
	}
}

func TestLayoutHeader_FindForm(t *testing.T) {
	tmpl, err := template.New("").Funcs(coreTemplateFuncs()).ParseFS(templatesFS,
		"templates/shared/layout.html",
		"templates/shared/partials.html",
		"templates/contacts/contact_detail.html",
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	render := func(user *User) string {
		return renderToString(t, tmpl, "layout", coreLayoutFakeData(map[string]any{
			"Contact": contacts.Contact{ID: 1, DisplayName: "C", IsActive: true}, "ActiveTab": "suppliers", "SectionTab": "contacts", "TestMode": false, "CurrentUser": user,
		}))
	}
	signedIn := render(&User{DisplayName: "T"})
	for _, want := range []string{`method="get" action="/find"`, `name="q"`} {
		if !strings.Contains(signedIn, want) {
			t.Errorf("signed-in header missing %q", want)
		}
	}
	// The find form sits in the header, before the tab nav, which is unchanged.
	if i, j := strings.Index(signedIn, `action="/find"`), strings.Index(signedIn, `<nav class="tabs">`); i < 0 || i > j {
		t.Errorf("find form at %d should precede <nav class=\"tabs\"> at %d", i, j)
	}
	if !strings.Contains(signedIn, `--main-tabs:5`) {
		t.Errorf("nav changed: --main-tabs:5 missing")
	}
	if strings.Contains(render(nil), `action="/find"`) {
		t.Errorf("find form rendered for an anonymous user")
	}
}
