package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func readTemplate(t *testing.T, path string) string {
	t.Helper()
	b, err := fs.ReadFile(templatesFS, path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The browser-tab icon stays per-section (#22 only fixes the header brand).
func TestLayout_TabFaviconIsPerSection(t *testing.T) {
	if !strings.Contains(readTemplate(t, "templates/shared/layout.html"), `<link rel="icon" type="{{.FaviconType}}" href="{{.Favicon}}">`) {
		t.Error("layout <link rel=icon> no longer uses the per-section {{.Favicon}}")
	}
}

// #22: the header home link shows a fixed Arx brand icon, not the section favicon.
func TestLayout_HeaderBrandIconIsFixed(t *testing.T) {
	src := readTemplate(t, "templates/shared/layout.html")
	m := regexp.MustCompile(`class="app-header-home"[^>]*><img src="([^"]*)"`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("app-header-home <img> not found")
	}
	if m[1] != "/static/shared/icons/arx-brand.svg" {
		t.Errorf("header brand icon src = %q, want /static/shared/icons/arx-brand.svg", m[1])
	}
	if _, err := fs.Stat(staticFS, "static/shared/icons/arx-brand.svg"); err != nil {
		t.Errorf("brand icon not embedded: %v", err)
	}
}

// #202: theme.js must run in <head>, synchronously, before Bootstrap CSS, so
// the right data-bs-theme is set before first paint (no flash).
func TestLayout_ThemeScriptInHeadBeforeCSS(t *testing.T) {
	for _, p := range []string{"templates/shared/layout.html", "templates/shared/login.html"} {
		src := readTemplate(t, p)
		head, _, _ := strings.Cut(src, "</head>")
		tag := regexp.MustCompile(`<script[^>]*src="/static/shared/theme\.js[^"]*"[^>]*>`).FindString(head)
		if tag == "" {
			t.Errorf("%s: theme.js not loaded in <head>", p)
			continue
		}
		if strings.Contains(tag, "defer") || strings.Contains(tag, "async") {
			t.Errorf("%s: theme.js must load synchronously: %s", p, tag)
		}
		if strings.Index(head, tag) > strings.Index(head, "bootstrap.min.css") {
			t.Errorf("%s: theme.js must precede bootstrap.min.css", p)
		}
	}
	if _, err := fs.Stat(staticFS, "static/shared/theme.js"); err != nil {
		t.Errorf("theme.js not embedded: %v", err)
	}
}

func TestLayout_HasThemeToggle(t *testing.T) {
	if !strings.Contains(readTemplate(t, "templates/shared/layout.html"), "data-theme-toggle") {
		t.Error("layout has no [data-theme-toggle] button")
	}
}

// #202 regression guard: light-only colors in non-print templates break dark mode.
// Use Bootstrap's theme-aware classes (text-body-secondary, bg-body-tertiary) instead.
func TestTemplates_NoHardcodedLightColors(t *testing.T) {
	bad := regexp.MustCompile(`(?i)\bbg-light\b|style="[^"]*(?:color|background):\s*#(?:333|444|555|666|888|999|aaa|ddd|f5f5f5|fafafa|f8f9fa|f8f8f8|f0f4ff)\b`)
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "_print.html") {
			return err
		}
		for i, line := range strings.Split(readTemplate(t, p), "\n") {
			if m := bad.FindString(line); m != "" {
				t.Errorf("%s:%d: light-only color %q", p, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
