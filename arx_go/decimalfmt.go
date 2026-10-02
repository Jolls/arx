package main

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/shopspring/decimal"
)

// Marshal decimals as JSON numbers, not strings: the front-end JS (BOM children, PO grid,
// supplier defaults) reads these fields as numbers and calls .toFixed() on them.
func init() { decimal.MarshalJSONWithoutQuotes = true }

// maxDecimalExponent bounds user-typed exponent notation. decimal.NewFromString accepts
// "1e2000000000" (anything within int32), and the next Add/Cmp/String then tries to build a
// 10^2e9 big.Int — hanging the process. strconv.ParseFloat rejected such values; this restores
// that. The DB columns hold at most 16 digits, so 1e15 is already far past anything valid.
const maxDecimalExponent = 15

// parseDecimal parses user-supplied numeric text. Like decimal.NewFromString, but an
// out-of-range exponent is an error; the result is decimal.Zero on any error.
func parseDecimal(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, err
	}
	if e := d.Exponent(); e > maxDecimalExponent || e < -maxDecimalExponent {
		return decimal.Zero, fmt.Errorf("number %q out of range", s)
	}
	return d, nil
}

// printfVerb matches one printf verb: flags, width, precision, verb letter (or a literal %%).
var printfVerb = regexp.MustCompile(`%([-+# 0]*)(\d*)(?:\.(\d+))?([a-zA-Z%])`)

// templatePrintf is the template "printf" with decimal.Decimal support. A decimal has no
// fmt.Formatter, so the built-in printf would print its internals; here %f rounds the decimal
// itself (half away from zero, no float round-trip), %.Ng keeps the float behaviour the
// templates always had, and any other verb prints the exact shortest form. Non-decimal args
// format exactly as fmt.Sprintf would.
func templatePrintf(format string, args ...any) string {
	i := 0
	format = printfVerb.ReplaceAllStringFunc(format, func(m string) string {
		parts := printfVerb.FindStringSubmatch(m)
		flags, width, prec, verb := parts[1], parts[2], parts[3], parts[4]
		if verb == "%" {
			return m
		}
		idx := i
		i++
		if idx >= len(args) {
			return m
		}
		var d decimal.Decimal
		switch v := args[idx].(type) {
		case decimal.Decimal:
			d = v
		case *decimal.Decimal:
			if v != nil {
				d = *v // nil formats as zero, not a %!f(<nil>) error in the page
			}
		default:
			return m
		}
		var s string
		switch {
		case verb == "f" || verb == "F":
			p := 6
			if prec != "" {
				p, _ = strconv.Atoi(prec)
			}
			s = d.StringFixed(int32(p))
		case (verb == "g" || verb == "G") && prec != "":
			p, _ := strconv.Atoi(prec)
			s = strconv.FormatFloat(d.InexactFloat64(), 'g', p, 64)
		default:
			s = d.String()
		}
		if hasFlag(flags, '+') && !d.IsNegative() {
			s = "+" + s
		}
		args[idx] = s
		left := ""
		if hasFlag(flags, '-') {
			left = "-"
		}
		return "%" + left + width + "s"
	})
	return fmt.Sprintf(format, args...)
}

func hasFlag(flags string, f byte) bool {
	for i := 0; i < len(flags); i++ {
		if flags[i] == f {
			return true
		}
	}
	return false
}
