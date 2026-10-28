package main

import (
	"html"
	"io/fs"
	"strings"
	"testing"
)

// filterRowTemplates returns every template file that renders a filter row.
func filterRowTemplates(t *testing.T) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		if strings.Contains(readTemplate(t, p), `class="filter-row"`) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFilterHelp_EveryFilterRowHasHelpButton(t *testing.T) {
	files := filterRowTemplates(t)
	if len(files) != 10 {
		t.Errorf("filter-row templates = %d, want 10 (add the help button to any new one, then update this count): %v", len(files), files)
	}
	for _, f := range files {
		if !strings.Contains(readTemplate(t, f), `{{template "filter_help_btn"}}`) {
			t.Errorf("%s: filter row has no filter_help_btn", f)
		}
	}
}

func TestFilterHelp_SearchBoxFilesHaveHelpButton(t *testing.T) {
	want := map[string]int{
		"templates/parts/part_bom_edit.html":        1,
		"templates/pos/po_edit.html":                3,
		"templates/parts/part_sourcing.html":        2,
		"templates/parts/part_pricing.html":         1,
		"templates/parts/part_pricing_form.html":    1,
		"templates/contacts/contact_edit.html":      1,
		"templates/settings/settings.html":          1,
	}
	for f, n := range want {
		got := strings.Count(readTemplate(t, f), `{{template "search_help_btn"}}`)
		if got < n {
			t.Errorf("%s: search_help_btn count = %d, want >= %d", f, got, n)
		}
	}
}

func TestFilterHelp_RendersSharedContent(t *testing.T) {
	tmpls, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	var filterOut, searchOut []string
	for _, key := range []string{"layout:suppliers/suppliers.html", "records:index.html"} {
		tm := tmpls[key]
		if tm == nil {
			t.Fatalf("template %s not parsed", key)
		}
		filterOut = append(filterOut, renderToString(t, tm, "filter_help_btn", nil))
		searchOut = append(searchOut, renderToString(t, tm, "search_help_btn", nil))
	}
	if filterOut[0] != filterOut[1] || searchOut[0] != searchOut[1] {
		t.Error("help buttons render differently between core and records pages")
	}
	if !strings.Contains(filterOut[0], `data-bs-toggle="popover"`) || !strings.Contains(searchOut[0], `data-bs-toggle="popover"`) {
		t.Error("help button is not a Bootstrap popover trigger")
	}
	f := strings.ToLower(html.UnescapeString(filterOut[0]))
	for _, phrase := range []string{"substring", "any run of characters", "any single character", "whole field", "separated by", "excludes", "cannot be searched", "date", "select"} {
		if !strings.Contains(f, phrase) {
			t.Errorf("filter help missing %q", phrase)
		}
	}
	s := strings.ToLower(html.UnescapeString(searchOut[0]))
	for _, phrase := range []string{"any run of characters", "literal"} {
		if !strings.Contains(s, phrase) {
			t.Errorf("search help missing %q", phrase)
		}
	}
	if strings.Contains(s, "any single character") {
		t.Error("search help must not claim ? is a wildcard")
	}
}

func TestFilterHelp_PopoverInitInLayout(t *testing.T) {
	if !strings.Contains(readTemplate(t, "templates/shared/layout.html"), "bootstrap.Popover") {
		t.Error("layout.html does not initialise bootstrap.Popover")
	}
}

func TestFilterHelp_TextDefinedOnce(t *testing.T) {
	const phrase = "any single character"
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") || p == "templates/shared/layout.html" {
			return err
		}
		if strings.Contains(strings.ToLower(readTemplate(t, p)), phrase) {
			t.Errorf("%s repeats help text; keep it in layout.html", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
