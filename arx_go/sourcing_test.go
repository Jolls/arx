package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestResolvePriceFields(t *testing.T) {
	newReq := func(vals url.Values) *http.Request {
		req := httptest.NewRequest("POST", "/", strings.NewReader(vals.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := req.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		return req
	}

	cases := []struct {
		name             string
		vals             url.Values
		wantEA, wantPack any
	}{
		{
			name:     "price_ea only computes price_pack",
			vals:     url.Values{"pack_size": {"10"}, "price_ea": {"2"}},
			wantEA:   2.0,
			wantPack: 20.0,
		},
		{
			name:     "price_pack only computes price_ea",
			vals:     url.Values{"pack_size": {"10"}, "price_pack": {"20"}},
			wantEA:   2.0,
			wantPack: 20.0,
		},
		{
			name:     "missing pack_size leaves gap unfilled",
			vals:     url.Values{"price_ea": {"2"}},
			wantEA:   2.0,
			wantPack: nil,
		},
		{
			name:     "zero pack_size leaves gap unfilled",
			vals:     url.Values{"pack_size": {"0"}, "price_ea": {"2"}},
			wantEA:   2.0,
			wantPack: nil,
		},
		{
			name:     "both given stay as submitted",
			vals:     url.Values{"pack_size": {"10"}, "price_ea": {"2"}, "price_pack": {"25"}},
			wantEA:   2.0,
			wantPack: 25.0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotEA, gotPack := resolvePriceFields(newReq(c.vals))
			if gotEA != c.wantEA {
				t.Errorf("priceEA = %v, want %v", gotEA, c.wantEA)
			}
			if gotPack != c.wantPack {
				t.Errorf("pricePack = %v, want %v", gotPack, c.wantPack)
			}
		})
	}
}
