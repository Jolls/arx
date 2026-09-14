package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DigiKey Product Information API v4 client (issue #27). Client-credentials
// OAuth2 against a shop-level app registration (developer.digikey.com); the
// client ID/secret are shop-wide, so they live in app_config (set once via
// Settings, shared by every user, #60) rather than .env, config/local.json,
// or the per-user secrets store.
//
// This is the app's first outbound HTTP call to a third party. It must fail
// soft: an offline shop, an expired token, or a 429 surfaces as an inline
// error on the Sourcing tab and never blocks the page or the Save button.

const (
	digikeyTokenURL   = "https://api.digikey.com/v1/oauth2/token"
	digikeyProductURL = "https://api.digikey.com/products/v4/search/%s/productdetails"
	digikeyTimeout    = 10 * time.Second
)

var digikeyHTTPClient = &http.Client{Timeout: digikeyTimeout}

// digikeyTokenCache holds the short-lived OAuth2 access token so a burst of
// lookups (e.g. several parts edited in a row) doesn't re-authenticate every
// time. Guarded by its own mutex since it's shared across requests.
type digikeyTokenCache struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// digikeyPriceBreak is one quantity-break price point from StandardPricing.
// JSON tags let this same type serve as both the API response shape (api.go)
// and the shape decoded back out of the Add Supplier form's dk_prices_json
// hidden field (sourcing.go), instead of three near-identical structs.
type digikeyPriceBreak struct {
	BreakQuantity float64 `json:"break_quantity"`
	UnitPrice     float64 `json:"unit_price"`
	TotalPrice    float64 `json:"total_price"`
}

// digikeyResult is the subset of a DigiKey ProductDetails response mapped to
// Arx's sourcing/pricing/attachment fields.
type digikeyResult struct {
	SupplierDesc  string
	MinIncrement  *float64
	LeadTime      string
	MfgPartNumber string
	MfgName       string
	Prices        []digikeyPriceBreak
	DatasheetURL  string
	PhotoURL      string
}

// digikeyProductResponse mirrors the fields Arx uses from the v4
// ProductDetails endpoint. Unused fields (Parameters, Category, Series, …)
// are intentionally omitted — json.Unmarshal ignores keys we don't declare.
type digikeyProductResponse struct {
	Product struct {
		Description struct {
			ProductDescription string `json:"ProductDescription"`
		} `json:"Description"`
		Manufacturer struct {
			Name string `json:"Name"`
		} `json:"Manufacturer"`
		ManufacturerProductNumber string `json:"ManufacturerProductNumber"`
		ManufacturerLeadWeeks     string `json:"ManufacturerLeadWeeks"`
		DatasheetURL              string `json:"DatasheetUrl"`
		PhotoURL                  string `json:"PhotoUrl"`
		ProductVariations         []struct {
			DigiKeyProductNumber string  `json:"DigiKeyProductNumber"`
			MinimumOrderQuantity float64 `json:"MinimumOrderQuantity"`
			StandardPricing      []struct {
				BreakQuantity float64 `json:"BreakQuantity"`
				UnitPrice     float64 `json:"UnitPrice"`
				TotalPrice    float64 `json:"TotalPrice"`
			} `json:"StandardPricing"`
		} `json:"ProductVariations"`
	} `json:"Product"`
}

// fetchDigiKeyToken returns a cached, still-valid access token, or requests a
// new one via the client-credentials grant, using the app's client ID/secret
// from the per-user secrets store (h.cfg).
func (h *Handler) fetchDigiKeyToken(ctx context.Context) (string, error) {
	h.digikeyToken.mu.Lock()
	defer h.digikeyToken.mu.Unlock()

	if h.digikeyToken.token != "" && time.Now().Before(h.digikeyToken.expires) {
		return h.digikeyToken.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {h.cfg.DigiKeyClientID},
		"client_secret": {h.cfg.DigiKeyClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, digikeyTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := digikeyHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("DigiKey authentication request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DigiKey authentication failed (HTTP %d)", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("DigiKey authentication response could not be parsed: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("DigiKey authentication response had no access token")
	}

	h.digikeyToken.token = tok.AccessToken
	// Refresh a little early so a token doesn't expire mid-request.
	h.digikeyToken.expires = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - 30*time.Second)
	return tok.AccessToken, nil
}

// fetchDigiKeyProduct looks up one product by DigiKey part number and maps
// the response onto Arx's sourcing/pricing/attachment fields.
func (h *Handler) fetchDigiKeyProduct(ctx context.Context, productNumber string) (*digikeyResult, error) {
	if !h.cfg.DigiKeyEnabled() {
		return nil, fmt.Errorf("DigiKey is not configured — add a client ID and secret in Settings")
	}

	token, err := h.fetchDigiKeyToken(ctx)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf(digikeyProductURL, url.PathEscape(productNumber))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-DIGIKEY-Client-Id", h.cfg.DigiKeyClientID)
	req.Header.Set("Accept", "application/json")

	resp, err := digikeyHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DigiKey lookup failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		return nil, fmt.Errorf("no DigiKey product found for %q", productNumber)
	case http.StatusTooManyRequests:
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			return nil, fmt.Errorf("DigiKey rate limit reached; retry after %s seconds", ra)
		}
		return nil, fmt.Errorf("DigiKey rate limit reached; try again shortly")
	default:
		return nil, fmt.Errorf("DigiKey lookup failed (HTTP %d)", resp.StatusCode)
	}

	var parsed digikeyProductResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("DigiKey response could not be parsed: %w", err)
	}

	return mapDigiKeyProduct(&parsed, productNumber), nil
}

// mapDigiKeyProduct converts the raw API response into Arx's sourcing shape.
// A product can have multiple ProductVariations (e.g. cut-tape vs. reel,
// each with its own DigiKey product number, MOQ, and pricing) even though the
// endpoint is keyed to the single product number the user looked up — so the
// variation matching that requested number is used for MOQ/pricing; falls
// back to the first variation if none matches (e.g. requestedPN was a
// manufacturer part number rather than a DigiKey product number).
func mapDigiKeyProduct(parsed *digikeyProductResponse, requestedPN string) *digikeyResult {
	p := parsed.Product
	out := &digikeyResult{
		SupplierDesc:  p.Description.ProductDescription,
		MfgPartNumber: p.ManufacturerProductNumber,
		MfgName:       p.Manufacturer.Name,
		DatasheetURL:  p.DatasheetURL,
		PhotoURL:      p.PhotoURL,
	}
	if weeks := strings.TrimSpace(p.ManufacturerLeadWeeks); weeks != "" {
		if n, err := strconv.Atoi(weeks); err == nil {
			plural := "s"
			if n == 1 {
				plural = ""
			}
			out.LeadTime = fmt.Sprintf("%d week%s", n, plural)
		}
	}
	if len(p.ProductVariations) > 0 {
		v := p.ProductVariations[0]
		for _, candidate := range p.ProductVariations {
			if strings.EqualFold(candidate.DigiKeyProductNumber, requestedPN) {
				v = candidate
				break
			}
		}
		if v.MinimumOrderQuantity > 0 {
			moq := v.MinimumOrderQuantity
			out.MinIncrement = &moq
		}
		for _, pr := range v.StandardPricing {
			out.Prices = append(out.Prices, digikeyPriceBreak{
				BreakQuantity: pr.BreakQuantity,
				UnitPrice:     pr.UnitPrice,
				TotalPrice:    pr.TotalPrice,
			})
		}
	}
	return out
}
