package main

import (
	"errors"
	"math"
	"regexp"
	"strconv"
)

// evalMath evaluates an arithmetic expression: numbers, + - * /, parentheses, unary +/-, and calls to
// the functions in mathFuncs (#95, #132, #468). It is the Go twin of evalMath in static/records/app.js
// and must accept and compute exactly the same things: a recursive-descent parser that only ever calls
// the named functions below, with validated arg counts. ok is false for invalid input or a non-finite
// result (division by zero, sqrt of a negative).
func evalMath(expr string) (float64, bool) {
	// Reject date-like strings (e.g. "12/29/2026") — slashes would be evaluated as division.
	if dateLikeRE.MatchString(expr) {
		return 0, false
	}
	p := mathParser{s: expr}
	v, err := p.expression()
	if err != nil {
		return 0, false
	}
	p.skipSpace()
	if p.i != len(p.s) || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// formatMathResult renders an evalMath result the way the edit page always has
// (String(parseFloat(result.toFixed(10))) in JS): rounded to 10 decimals, no trailing zeros.
func formatMathResult(v float64) string {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 10, 64), 64)
	if r == 0 {
		r = 0 // normalize -0
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

var dateLikeRE = regexp.MustCompile(`^\s*\d{1,2}/\d{1,2}/\d{2,4}`)

var errMathSyntax = errors.New("invalid expression")

var mathFuncs = map[string]func([]float64) (float64, bool){
	"min": func(a []float64) (float64, bool) {
		if len(a) < 2 {
			return 0, false
		}
		m := a[0]
		for _, v := range a[1:] {
			m = math.Min(m, v)
		}
		return m, true
	},
	"max": func(a []float64) (float64, bool) {
		if len(a) < 2 {
			return 0, false
		}
		m := a[0]
		for _, v := range a[1:] {
			m = math.Max(m, v)
		}
		return m, true
	},
	"abs": unaryFunc(math.Abs),
	"mod": func(a []float64) (float64, bool) {
		if len(a) != 2 {
			return 0, false
		}
		r := math.Mod(a[0], a[1])
		if r != 0 && (r < 0) != (a[1] < 0) {
			r += a[1] // result takes the divisor's sign, as in the JS twin
		}
		return r, true
	},
	// round(x) or round(x, digits); halves round up, as JS Math.round did.
	"round": func(a []float64) (float64, bool) {
		if len(a) != 1 && len(a) != 2 {
			return 0, false
		}
		scale := 1.0
		if len(a) == 2 {
			scale = math.Pow(10, math.Trunc(a[1]))
		}
		return math.Floor(a[0]*scale+0.5) / scale, true
	},
	"floor": unaryFunc(math.Floor),
	"ceil":  unaryFunc(math.Ceil),
	"sqrt":  unaryFunc(math.Sqrt),
	"pow": func(a []float64) (float64, bool) {
		if len(a) != 2 {
			return 0, false
		}
		return math.Pow(a[0], a[1]), true
	},
}

func unaryFunc(f func(float64) float64) func([]float64) (float64, bool) {
	return func(a []float64) (float64, bool) {
		if len(a) != 1 {
			return 0, false
		}
		return f(a[0]), true
	}
}

type mathParser struct {
	s string
	i int
}

func (p *mathParser) skipSpace() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\n' || p.s[p.i] == '\r') {
		p.i++
	}
}

// peek returns the current byte, or 0 at end of input.
func (p *mathParser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *mathParser) expression() (float64, error) {
	v, err := p.term()
	for err == nil {
		p.skipSpace()
		op := p.peek()
		if op != '+' && op != '-' {
			break
		}
		p.i++
		var r float64
		if r, err = p.term(); err == nil {
			if op == '+' {
				v += r
			} else {
				v -= r
			}
		}
	}
	return v, err
}

func (p *mathParser) term() (float64, error) {
	v, err := p.unary()
	for err == nil {
		p.skipSpace()
		op := p.peek()
		if op != '*' && op != '/' {
			break
		}
		p.i++
		var r float64
		if r, err = p.unary(); err == nil {
			if op == '*' {
				v *= r
			} else {
				v /= r
			}
		}
	}
	return v, err
}

func (p *mathParser) unary() (float64, error) {
	p.skipSpace()
	switch p.peek() {
	case '-':
		p.i++
		v, err := p.unary()
		return -v, err
	case '+':
		p.i++
		return p.unary()
	}
	return p.atom()
}

func (p *mathParser) atom() (float64, error) {
	p.skipSpace()
	ch := p.peek()
	switch {
	case ch == '(':
		p.i++
		v, err := p.expression()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.peek() != ')' {
			return 0, errMathSyntax
		}
		p.i++
		return v, nil
	case ch >= '0' && ch <= '9':
		start, sawDot := p.i, false
		for p.i < len(p.s) && ((p.s[p.i] >= '0' && p.s[p.i] <= '9') || (p.s[p.i] == '.' && !sawDot)) {
			if p.s[p.i] == '.' {
				sawDot = true
			}
			p.i++
		}
		return strconv.ParseFloat(p.s[start:p.i], 64)
	case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'):
		start := p.i
		for p.i < len(p.s) && ((p.s[p.i] >= 'a' && p.s[p.i] <= 'z') || (p.s[p.i] >= 'A' && p.s[p.i] <= 'Z')) {
			p.i++
		}
		fn, known := mathFuncs[p.s[start:p.i]]
		p.skipSpace()
		if !known || p.peek() != '(' {
			return 0, errMathSyntax
		}
		p.i++
		args, err := p.args()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.peek() != ')' {
			return 0, errMathSyntax
		}
		p.i++
		v, ok := fn(args)
		if !ok {
			return 0, errMathSyntax
		}
		return v, nil
	}
	return 0, errMathSyntax
}

func (p *mathParser) args() ([]float64, error) {
	p.skipSpace()
	if p.peek() == ')' {
		return nil, nil
	}
	var args []float64
	for {
		v, err := p.expression()
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		p.skipSpace()
		if p.peek() != ',' {
			return args, nil
		}
		p.i++
	}
}
