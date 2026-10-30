package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// #405: root package.json only exists so Dependabot tracks the CDN versions;
// the templates must be updated by hand to match.
func TestTemplateCDNVersionsMatchPackageJSON(t *testing.T) {
	b, err := os.ReadFile("../package.json")
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`cdn\.jsdelivr\.net/npm/(bootstrap|bootstrap-icons)@([^/"]+)/`)
	for _, path := range []string{"templates/shared/layout.html", "templates/shared/login.html"} {
		seen := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(readTemplate(t, path), -1) {
			seen[m[1]] = true
			if m[2] != pkg.Dependencies[m[1]] {
				t.Errorf("%s: %s CDN version %s, package.json has %q", path, m[1], m[2], pkg.Dependencies[m[1]])
			}
		}
		for _, name := range []string{"bootstrap", "bootstrap-icons"} {
			if !seen[name] {
				t.Errorf("%s: no %s CDN link found", path, name)
			}
		}
	}
}
