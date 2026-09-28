# #220 — Part pricing tab to sqlc

Part of #220 / #190. Fourth slice of the `arx_go/parts.go` catalog: the pricing tab's price CRUD.

## Scope
- `PartPricing` (price list + default-supplier lookup), `PricePreferred`, `PriceCreate`, `PriceEdit`,
  `PriceUpdate` (tx), `PriceDeactivate`, `PriceDelete`, `PriceActivate`.
- Out of scope, still raw SQL: `ensureDefaultSupplier` (also called from `pos.go` and `sourcing.go` with
  string/int ids; moves with the `pos.go` slice of #190), rollup/build cost, orders/records cards, price
  history, PartDetail's attachment/price queries. `parts.go` does not join `convertedFiles`.

## Changes
- `internal/parts/parts.sql` (+ `sqlc generate`): `ListPartPrices`, `GetPartPrice` (guarded by part),
  `GetDefaultSupplier`, `SetDefaultSupplier`, `CreatePrice` (`sqlc.narg` pack/each/pack price,
  `::text::date` effective date, as `ImportPrice`), `SetPriceActive`, `DeleteInactivePrice`.
- `internal/parts/parts.go`: `type PartPrice` (any price row + supplier name; the existing `Price` stays the
  active-rows type) and the matching service methods.
- `arx_go/sourcing.go`: `nullableFloat` returns `*float64`, `resolvePriceFields` returns `(*float64, *float64)`
  (their only callers are the pricing handlers); `TestResolvePriceFields` compares with `reflect.DeepEqual`.
- `arx_go/parts.go`: handlers parse the price id (`Atoi`; a bad id takes the same error branch Postgres gave
  it) and call the service with `requireTab`'s part id; `PriceUpdate` runs `parts.New(tx)`. `modelPrice`
  maps `parts.PartPrice` → `models.Price`.

## Resolved decision — duplicate-price message
`strings.Contains(err.Error(), "UQ_price")` never matched: Postgres folds `UQ_price_active_combo` to
lowercase, so a duplicate showed the raw "Error saving price: … duplicate key …". Fixed in this PR (user's
call): `isDuplicatePrice` matches `uq_price` case-insensitively, like `unit.go`'s `uq_unit_serial` check.

## Test plan
1. Coverage audit: `smoke_post_test.go` "PriceCreate" (one successful create), `TestResolvePriceFields` (unit).
2. Characterization (`arx_go/pricing_integration_test.go`), passed on unchanged code:
   `PartPricing_Renders` (groups by supplier name, row order, Inactive/Preferred badges, NULL cells),
   `PriceEdit_Renders` (values, another part's price → not found), `PriceCreate_Columns` (columns, pack/each
   fill-in, today default, first-price default supplier, invalid supplier), `PriceUpdate` (deactivate+insert,
   rollback on collision, part guard), `PriceActivateDeactivateDelete` (part guard, inactive-only delete),
   `PricePreferred`.
3. Red: the duplicate cases in `PriceCreate_Columns`, `PriceUpdate` and `PriceActivateDeactivateDelete`
   expect the friendly messages; they failed on the old code (generic error) and pass after the fix.
4. Manual-only: pricing tab layout, add/edit form (supplier typeahead), Set preferred button.
