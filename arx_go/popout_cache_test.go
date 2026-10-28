package main

import (
	"io/fs"
	"strings"
	"testing"
)

func readAppJS(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(staticFS, "static/shared/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPopoutJS_CachesByURL pins the per-URL popout cache the TTL change builds on (#386).
func TestPopoutJS_CachesByURL(t *testing.T) {
	src := readAppJS(t)
	for _, want := range []string{"cache[url] !== undefined", "cache[url] ="} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js popout cache: missing %q", want)
		}
	}
}

// TestPopoutJS_CacheHasTTL: stock and PO status go stale, so cached popouts expire (#386).
func TestPopoutJS_CacheHasTTL(t *testing.T) {
	src := readAppJS(t)
	for _, want := range []string{"TTL_MS = 45000", "Date.now() - cache[url].at < TTL_MS"} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js popout cache: missing %q", want)
		}
	}
}

// The parts-grid Below Min badge sits in a single-quoted string inside a template literal, so
// its popout URL must be concatenated; ${r.id} there is sent literally and 404s (#386).
func TestPopoutJS_GridBelowMinURLInterpolated(t *testing.T) {
	if !strings.Contains(readAppJS(t), `data-popout-url="/popout/part/' + r.id + '/below-min"`) {
		t.Error("grid Below Min badge popout URL is not concatenated with r.id")
	}
}
