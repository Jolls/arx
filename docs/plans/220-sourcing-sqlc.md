# #220 — sourcing.go to sqlc

Part of #220 / #190. Converts the 10 SQL sites in `arx_go/sourcing.go` onto `internal/parts`.

## Changes
- `sqlc.yaml`: overrides `pg_catalog.numeric` → `float64` / `*float64` (nullable).
- `internal/parts/parts.sql` + `sqlc generate`:
  - `ListSupplierParts` (part's links + supplier name + effective purchase unit), `GetSupplierPart`,
    `CreateSupplierPart`, `UpdateSupplierPart`, `DeleteSupplierPart` (hard delete, scoped by part).
  - `ListActivePrices` (now also selects `price_pack`, which the template already reads).
  - DigiKey import: `ImportPrice` (`ON CONFLICT ... DO NOTHING`, :execrows), `CreateImportedAttachment`,
    `CreateManufacturer` (`ON CONFLICT (name) DO NOTHING RETURNING id`), `ImportMfgPart`
    (`ON CONFLICT ... DO NOTHING`, NULL description as today).
- `internal/parts/parts.go`: types `SupplierPart`, `Price`; methods wrapping the queries above.
- `arx_go/sourcing.go`: handlers use `h.parts()` / `parts.New(tx)`; `fetchSupplierLinks` and
  `fetchActivePricesBySupplier` stay as thin wrappers (attachments.go / tests call them);
  `supplierPartFromForm` returns `*parts.SupplierPart`. Non-numeric ids from form/URL go down the
  same error branch the DB error used.
- `models.SupplierPart` stays (suppliers.go); `models.Price` stays (parts.go pricing tab).
- `cfg.*Table()` helpers: all still used elsewhere; none deleted.

## Resolved decision — duplicate checks
The `UQ_price` / `UQ_company_name` / `UQ_mfg_part` string checks never match (Postgres lowercases
names) and the tx is aborted anyway. Fix sourcing's three via `ON CONFLICT` in SQL; file a separate
issue for the parts.go pricing-tab `UQ_price` checks.

## Test plan
1. Coverage audit: `sourcing_integration_test.go` (PartSourcing, FetchActivePricesBySupplier,
   Edit/NotFound/WrongPartScope, Update, Update_MissingSupplier, Delete);
   `integration_test.go` `TestIntegration_SupplierPartCreate_DigiKeyImport` (attachments + thumbnail).
2. Characterization: `SupplierPartCreate_Fields` (trim, NULL preference, min_increment, explicit and
   inherited unit, update clearing to NULL), `DigiKeyPrices` (rows + default supplier),
   `DigiKeyNewManufacturer`, `DigiKeyExistingManufacturer`, `SupplierPartDelete_WrongPartScope`.
3. Red: `sourcing.go` in `convertedFiles`; `DigiKeyPriceExists` (existing active pack-size price is
   skipped, rest imported); `DigiKeyMfgPartExists` (existing MPN skipped, link saved);
   `DigiKeyManufacturerNameTaken` (friendly message, no link).
4. Manual-only: sourcing tab render (pricing subrow now shows pack price), add/edit/delete link,
   DigiKey lookup → import flow.
