# Issue #27 — Import part metadata from DigiKey

Autofill a part's sourcing link from DigiKey's Product Information API v4, keyed on the
supplier PN the user types. McMaster-Carr is explicitly **out of scope** (see Decisions).

## Goal

On the part Sourcing tab, the user types a DigiKey PN and clicks **Fetch from DigiKey**.
The form populates with catalog data; the existing **Add Supplier** button is the only
confirmation step. Nothing is written until the user saves.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Provider flag on `company` | **None** | The user clicking "Fetch from DigiKey" *is* the provider selection. No schema change, no name-matching magic. |
| Credential storage | **Per-user secrets store** | Two fields on `SecretsConfig`; matches the #732 pattern. Cost: org-level keys re-entered per user. |
| Write scope | **Sourcing fields + prices + datasheet/photo + mfg_part** | All four tables written in one tx by `SupplierPartCreate`. |
| Manufacturer handling | **User picks** | A `<select>` offering existing manufacturers, "create new", or "skip". Never auto-creates silently. |
| McMaster-Carr | **Deferred** | No self-serve API; scraping is ToS-prohibited and fragile, and this repo is slated to go public. |
| Sandbox toggle | **No** | Production base URL only. Add later if needed. |

## API facts (verified 2026-09-05)

- `GET https://api.digikey.com/products/v4/search/{productNumber}/productdetails`
- OAuth2 client-credentials: form POST to `https://api.digikey.com/v1/oauth2/token`.
  Plain `net/http` — no `golang.org/x/oauth2`, no new dependency, nothing cgo/native.
- Headers: `X-DIGIKEY-Client-Id`, `Authorization: Bearer <token>`
- Rate limits: 120/min, 1000/day. 429 carries `Retry-After`. Manual per-part use is nowhere near.
- Tokens are short-lived → small in-memory cache on the client, refreshed on expiry.

## Field mapping

| DigiKey | Arx target | Note |
|---|---|---|
| `Description.ProductDescription` | `supplier_part.supplier_desc` | VARCHAR(100) — **truncated**, lossy by design |
| `MinimumOrderQuantity` | `supplier_part.min_increment` | |
| `ManufacturerLeadWeeks` | `supplier_part.lead_time` | Absent in some payloads → left blank, never guessed |
| `ManufacturerProductNumber` | `mfg_part.mfg_part_number` | |
| `Manufacturer.Name` | `mfg_part.mfg_id` | resolved via the manufacturer select |
| `ProductVariations[].StandardPricing[]` | `price` rows | `BreakQuantity`→`pack_size`, `UnitPrice`→`price_ea`, `TotalPrice`→`price_pack` |
| `DatasheetUrl` | `part_attachment`, category `Datasheet` | plain `https://` row, no download |
| `PhotoUrl` | `part_attachment`, category `Photo` | same |
| `Parameters` | — | skipped; parametric mapping is per-category and messy |

**`pack_size` semantics:** the DDL comment calls it "units per pack", but
[`pickTier`](../../arx_go/parts.go) selects "largest `PackSize` <= qty" — i.e. the cost engine
already treats it as a *quantity break*. So `BreakQuantity`→`pack_size` is correct for how the
value is consumed. Pre-existing comment/behavior mismatch; not touched here.

## Changes

**New files**
- `arx_go/digikey.go` — token cache, product fetch, response→`digikeyResult` mapping
- `arx_go/digikey_test.go` — parser tests against a captured JSON fixture (no network)
- `arx_go/testdata/digikey_productdetails.json` — fixture

**Modified**
- `arxlib/config/secrets.go` — `DigiKeyClientID` / `DigiKeyClientSecret` on `SecretsConfig`
- `arx_go/api.go` — `APIDigiKeyLookup`, returns the mapped result + manufacturer match as JSON
- `arx_go/settings.go` — `SettingsDigiKeySave` (small standalone handler, not the DB-coupled main save)
- `arx_go/sourcing.go` — `SupplierPartCreate` writes the optional extras in one tx; `PartSourcing`/`SupplierPartEdit` pass `Manufacturers` + `DigiKeyEnabled`
- `arx_go/main.go` — two routes
- `arx_go/templates/parts/part_sourcing.html` — Fetch button, results panel, hidden inputs
- `arx_go/templates/settings/settings.html` — DigiKey credentials section
- `CHANGELOG.md`, `arx_go/RELEASE_NOTES.md`

**No migration. No schema change. No new Go dependency.**

## Failure behavior

This is the app's first outbound HTTP. It must fail soft:
- Explicit client timeout; offline/429/404 surface as an inline error on the form
- A failed fetch never blocks the page or the Save
- Credentials absent → the Fetch button is not rendered at all

## Verification

1. `go build ./...`, `go vet ./...`, `go test ./...` in `arx_go/` and `arxlib/`
2. Parser unit tests run against the fixture — `go test ./...` stays hermetic and offline
3. Manual: register a DigiKey app, enter creds in Settings, fetch a real PN, confirm the form
   populates and Save writes sourcing + price + attachment + mfg_part rows
4. Manual: clear the creds, confirm the Fetch button disappears and the page still works
