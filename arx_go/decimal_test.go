package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"arx/arx_go/models"
)

// d converts a test-table float literal to a decimal (shortest representation, so d(0.1) is 0.1).
func d(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// dp is d as a pointer, for nullable decimal fields.
func dp(f float64) *decimal.Decimal { v := d(f); return &v }

func TestTemplatePrintf(t *testing.T) {
	cases := []struct {
		format string
		args   []any
		want   string
	}{
		{"%.2f", []any{decimal.RequireFromString("1.005")}, "1.01"}, // float64 would give 1.00
		{"%.4f", []any{decimal.RequireFromString("0.1").Mul(decimal.NewFromInt(3))}, "0.3000"},
		{"$%.2f", []any{dp(12.5)}, "$12.50"},
		{"%+.2f", []any{decimal.RequireFromString("2.5")}, "+2.50"},
		{"%+.2f", []any{decimal.RequireFromString("-2.5")}, "-2.50"},
		{"%g", []any{decimal.RequireFromString("0.125")}, "0.125"},
		{"%.5g", []any{decimal.RequireFromString("1234.5678")}, "1234.6"},
		{"%d / %.2f", []any{3, decimal.RequireFromString("1.5")}, "3 / 1.50"},
		{"%.1f%%", []any{12.34}, "12.3%"}, // non-decimal args pass through
	}
	for _, c := range cases {
		if got := templatePrintf(c.format, c.args...); got != c.want {
			t.Errorf("templatePrintf(%q, %v) = %q, want %q", c.format, c.args, got, c.want)
		}
	}
}

// The BOM-children endpoint's JSON is read by shared/app.js, which calls .toFixed() on
// LineUnitCost/LineExtCost, so decimals must marshal as JSON numbers, not strings.
func TestDecimalMarshalsAsJSONNumber(t *testing.T) {
	b, err := json.Marshal(models.BOMItem{Qty: d(2), LineUnitCost: d(0.1), LineExtCost: d(0.2)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Qty":2`, `"LineUnitCost":0.1`, `"LineExtCost":0.2`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("BOMItem JSON = %s, want it to contain %s", b, want)
		}
	}
}

// The float64 version of these sums drifted (0.1+0.2 = 0.30000000000000004, and a
// qty × unit_cost of 3 × 0.1 = 0.30000000000000004); decimal arithmetic must be exact.
func TestDecimalArithmeticIsExact(t *testing.T) {
	if got := rowLineTotal(map[string]polRow{
		"a": {Qty: "3", Cost: "0.1"},
		"b": {Qty: "1", Cost: "0.2"},
	}); !got.Equal(d(0.5)) {
		t.Errorf("rowLineTotal = %s, want 0.5", got)
	}
}
