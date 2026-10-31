package main

import (
	"strings"
	"testing"
	"time"

	"arx/arx_go/models"
)

// renderUnitPage renders a unit template with a single unit of the given source (#423).
func renderUnitPage(t *testing.T, key, source string) string {
	t.Helper()
	tmpls, err := parseTemplates(templatesFS)
	if err != nil {
		t.Fatal(err)
	}
	tm := tmpls[key]
	if tm == nil {
		t.Fatalf("template %s not parsed", key)
	}
	unit := UnitRow{ID: 1, SerialNumber: "SN-1", PartID: 1, Source: source, CreatedAt: time.Now()}
	return renderToString(t, tm, "layout", coreLayoutFakeData(map[string]any{
		"ActiveTab": "parts", "ActiveDetailTab": "units", "UserLoc": time.UTC,
		"Part": models.Part{ID: 1}, "Units": []UnitRow{unit}, "Unit": unit,
		"TypeOptions": []string{"Acceptance"},
	}))
}

func TestPartUnitsTemplate_ManualBadgeUnchanged(t *testing.T) {
	out := renderUnitPage(t, "layout:parts/part_units.html", "manual")
	if !strings.Contains(out, `<span class="badge bg-secondary">manual</span>`) {
		t.Errorf("manual unit lost its badge:\n%s", out)
	}
}

func TestPartUnitTraceTemplate_ManualBadgeUnchanged(t *testing.T) {
	out := renderUnitPage(t, "layout:parts/part_unit_trace.html", "manual")
	if !strings.Contains(out, `manual</span> — added directly, no test record`) {
		t.Errorf("manual unit lost its explanation:\n%s", out)
	}
}

func TestPartUnitsTemplate_TestRecordBadge(t *testing.T) {
	out := renderUnitPage(t, "layout:parts/part_units.html", "test")
	if !strings.Contains(out, `<span class="badge bg-secondary">test record</span>`) {
		t.Errorf("test unit Source not a 'test record' badge:\n%s", out)
	}
}

func TestPartUnitTraceTemplate_TestRecordBadge(t *testing.T) {
	out := renderUnitPage(t, "layout:parts/part_unit_trace.html", "test")
	if !strings.Contains(out, `test record</span> — created by saving a test record`) {
		t.Errorf("test unit Source missing badge/explanation:\n%s", out)
	}
}
