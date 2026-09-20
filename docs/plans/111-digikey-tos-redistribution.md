# 111 — Stop redistributing DigiKey's API agreement and catalogue data

Issue: [#111](https://github.com/Jolls/arx/issues/111). Part of the pre-publication
review, [#102](https://github.com/Jolls/arx/issues/102).

## Problem

Two files carry third-party content that the repo's blanket AGPL-3.0 licence has no
right to relicense:

1. `docs/digikey-api-terms-of-service.md` — a tracked 30 KB verbatim copy of DigiKey's
   API User Agreement.
2. `arx_go/testdata/digikey_productdetails.json` — a real API response fixture holding
   live catalogue data (manufacturer, MPN, datasheet/photo URLs, price breaks). No
   account identifiers, but it is "DigiKey Data" as the agreement defines it.

## Decision (confirmed with user)

Do both halves: stub the agreement doc, and make the fixture synthetic.

Not doing a history rewrite for the agreement — it is a public document, not a secret,
and rewriting history again on this repo is disproportionate.

**The same applies to the fixture, explicitly.** Both files are scrubbed in the working
tree only; the original blobs stay reachable in history after publication. That is
accepted for the same reason in both cases: neither is confidential. The agreement is
published by DigiKey, and the fixture holds public catalogue facts — a real manufacturer
part number, its public datasheet and photo URLs, and list price breaks. The concern
driving this issue is redistribution posture going forward, not secrecy, and a second
history rewrite on a repo that has already had one is not a proportionate answer to it.
If DigiKey's terms are ever read as requiring removal, that is a deliberate decision to
take then, with the agreement and the fixture handled together.

## Changes

### 1. `docs/digikey-api-terms-of-service.md` — replace body with a stub

Keep the existing provenance header (what it is for, which issue/PR added the
integration, the review date). Delete the agreement text itself and point at DigiKey's
developer portal as the canonical source. Rename the top heading so the file does not
read as though it contains the agreement.

### 2. `arx_go/testdata/digikey_productdetails.json` — make synthetic

Keep the JSON **shape** byte-for-byte compatible with `digikeyProductResponse` — the
tests exercise variation matching, price-break selection, lead-time formatting and URL
population, all of which depend on structure, not on the specific values. Replace only
the values:

| Field | New value |
|---|---|
| `Description.ProductDescription` | `CAP CER 0.1UF 50V X7R 0603` → synthetic equivalent |
| `Description.DetailedDescription` | synthetic equivalent |
| `Manufacturer.Id` / `.Name` | `9001` / `Contoso Components` (matches the fictional-company convention already used in `SQL/azure/seed_test_data.sql`) |
| `ManufacturerProductNumber` | `CC0603C104K5R` |
| `DatasheetUrl` / `PhotoUrl` | `https://example.test/...` |
| `ProductVariations[].DigiKeyProductNumber` | `TEST-1096-1-ND`, `TEST-1096-2-ND` |

Leave `ManufacturerLeadWeeks`, `PackageType.Name`, `MinimumOrderQuantity` and all
`StandardPricing` numbers as they are — they are generic and the tests assert on the
MOQ/price values directly.

Field **names** must not change: they are the DigiKey API schema that `digikey.go`
unmarshals against.

### 3. `arx_go/digikey_test.go` — update expectations to match

`TestMapDigiKeyProduct` and `TestMapDigiKeyProductMatchesRequestedVariation` assert on
fixture values. Update:
- `SupplierDesc`, `MfgName`, `MfgPartNumber` expectations.
- The two `mapDigiKeyProduct(parsed, "<DigiKey PN>")` call arguments.

**Do not touch** `TestMapDigiKeyProductNormalizesProtocolRelativeURLs`. Its inline
`//mm.digikey.com/...` URLs are there to document a real API quirk (schemeless URLs
that `net/http` rejects). They are URL-scheme test data, not catalogue data, and
genericising them would weaken what the test documents.

## Success criteria

- `docs/digikey-api-terms-of-service.md` contains no agreement body text.
- `grep -c "KEMET\|C0603C104K5RACTU\|399-1096" arx_go/testdata/digikey_productdetails.json` → 0.
- `go test ./...` passes in `arx_go` (all five `TestMapDigiKeyProduct*` tests).
- `go build ./...` and `go vet ./...` pass in `arx_go`.
