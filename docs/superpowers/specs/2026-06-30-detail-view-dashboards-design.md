# Detail-view dashboards (Parts / Supplier / Contact)

**Issue:** [#521](https://github.com/Jolls/arx-legacy/issues/521)
**Date:** 2026-06-30
**Status:** Approved design — pending implementation plan

## Problem

The Details sub-tab of each entity (Part, Supplier, Contact) is a vertical stack of
full-width blocks. Everything interesting about a part — purchase orders, price trend,
inventory, suppliers — lives one click away in sibling sub-tabs and never surfaces on the
landing view. The Details page is the default view a user lands on, but it gives no
at-a-glance summary of the record's activity.

Goal: replace the stacked blocks with a **2-wide dashboard grid** of summary cards, each
card summarizing a sibling sub-tab and linking to it.

## Scope caveat by entity

The "summarize the sub-tabs" premise applies unevenly:

| Entity | Sub-tabs | Dashboard character |
|---|---|---|
| **Part** | Details, BOM, Where Used, Order History, Transactions, Attachments, Pricing, Price History, Mfg Parts, Suppliers | Full activity dashboard |
| **Supplier** | Details, Linked Parts, Attachments, Folder (+ existing Statistics block) | Activity dashboard |
| **Contact** | **none** — Details is the only view | Re-flow existing reference blocks into a grid + a "Related" card. No activity to summarize. |

Contact does **not** get invented activity data. Its improvement is purely layout: the
existing sections (Basic Info, Contact Details, Address, Status) re-flowed into a 2-col
grid, plus a Related card (linked supplier + sibling contacts).

## Shared pattern

A reusable card-grid layout, Bootstrap-native:

- Full-width **identity/status header strip** on top (unchanged content per entity).
- A responsive grid: `<div class="row row-cols-1 row-cols-md-2 g-3">`.
- Each card is a `.card` with:
  - header row: card title + a right-aligned `View all →` link to the sibling sub-tab
    (omit the link when there is no sub-tab, e.g. Contact's Related card).
  - compact body (`card-body`), using `table table-sm` for row lists.
- **Cards omit themselves entirely when their data is absent or the category flag is off**
  (`ShowOrders` / `ShowInventory` / `ShowPricing` already exist on the Part model). No empty
  boxes.
- **Notes** span full width (`col-12`) at the bottom when present.
- Prefer Bootstrap classes over custom CSS per project convention. Existing
  `.detail-section` / `.detail-row` styling may be retained inside cards where it reads well;
  no new bespoke CSS unless a card needs it.

## Part dashboard

Header: part number, title, and the existing status bar (Status / Category / Rev / Last
Modified / Primary Attachment) — unchanged.

Grid cards:

1. **Pricing snapshot** — current cost, preferred-supplier price, rollup Δ (with the
   existing `RollupSignificant` badge). *Data already loaded in the handler.* Links to Pricing.
2. **Inventory** ✦ — stock on hand + last 3–5 transactions (date, type, qty, running or
   resulting balance). Shown only when `ShowInventory`. Links to Transactions.
3. **Recent POs** ✦ — last 5 POs for this part: date, PO#, supplier, qty, unit price,
   status badge. Shown only when `ShowOrders`. Links to Order History.
4. **Price trend** ✦ — compact sparkline reusing `window.PRICE_HISTORY` JSON and a compact
   mode of `static/pm/price_history.js`. Shown only when `ShowPricing` and data exists.
   Links to Price History.
5. **Suppliers** — preferred supplier, supplier count, best active price. *Phase 2.*
   Links to Suppliers.
6. **Reference** — Basic Information (part #, title, category, rev, detail, unit) +
   Additional Fields (`Requested By`, user fields), folded into one compact card.
7. **Attachments** — primary attachment + count. Links to Attachments.

✦ = the three cards prioritized by the user for the first cut. Suppliers is explicitly
phase 2. The Reference and Attachments cards preserve today's Details content so nothing is
lost.

## Supplier dashboard

Header: name, supplier code, status + roles (Supplier / Manufacturer).

Grid cards:

1. **Default contact** — name (linked), phone, email, city. Links to the contact.
2. **Statistics** — linked parts count, PO count. *Already computed on the model
   (`SUNumOfLNKs`, `SUNumOfPOs`).*
3. **Linked parts** — top 5 linked parts + total count. Links to Linked Parts.
4. **Recent POs** — last 5 POs to this supplier: date, PO#, total/status. Data exists in
   the PO table (filter by supplier) even though there is no PO sub-tab; no `View all →`
   link, or link to a filtered PO list if one exists.
5. **Attachments** — default attachment + count. Links to Attachments.
6. **Notes** — full width when present.

## Contact dashboard (layout re-flow only)

Header: name, linked supplier, active badge.

Grid cards (re-flow of existing sections — no new data queries beyond the Related card):

1. **Contact details** — phone 1, phone 2, fax.
2. **Address** — street, city, state, zip, country.
3. **Status & dates** — active badge, last modified.
4. **Related** — linked supplier (name + quick summary) and other contacts at the same
   supplier. New query for sibling contacts; no `View all →` (no sub-tab).
5. **Notes** — full width when present.

## Data / handler changes

New queries needed (all via the `h.queryContext` / `h.queryRowContext` wrappers, using
`cfg.*Table()` helpers — never hardcode table names):

- **Part / Recent POs** — last 5 PO lines for the part (join PO header for date/supplier/status).
- **Part / Inventory** — last 3–5 transactions for the part.
- **Part / Price trend** — reuse the existing price-history data assembly (same JSON the
  Price History sub-tab builds); factor it into a shared helper so both the sub-tab and the
  dashboard card call it.
- **Supplier / Linked parts** — top 5 linked parts.
- **Supplier / Recent POs** — last 5 POs for the supplier.
- **Contact / Siblings** — other contacts sharing the same `CNSUID`.

Phase 2 (Part Suppliers card) adds a preferred-supplier + best-price query.

Each new query should be cheap (TOP 5, indexed by the parent FK). Guard each so a failure or
empty result simply omits the card rather than erroring the page.

## Testing

- Template rendering is not unit-tested today; verify visually via `go run .` for:
  - a part with full activity (POs, inventory, pricing) — all cards present.
  - a part with a category that disables orders/inventory/pricing — those cards omitted, no
    empty boxes.
  - a supplier with and without linked parts / POs / default contact.
  - a contact with and without a linked supplier and with/without siblings.
- Any new handler helper that assembles card data (e.g. the shared price-history helper, or
  a "recent POs for part" query) is a good candidate for a small unit/integration test if it
  contains non-trivial logic. Decide per helper during implementation.

## Out of scope

- Editing from the dashboard (cards are read-only summaries; Edit stays on its own sub-tab).
- Reordering/customizing which cards appear (no per-user configurability).
- New sub-tabs. Supplier still has no PO sub-tab; the Recent POs card just reads PO data.
- Part Suppliers card and Supplier best-price analytics beyond a simple count (phase 2).
