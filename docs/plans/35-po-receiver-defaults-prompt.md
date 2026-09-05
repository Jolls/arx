# Prompt to finish account setup when no PO receiver default is set

**Issue:** #35 — "If no defaults set for reciever should it prompt the user to finish
setting up account?" (title is the entire spec, no body/comments)

## Confirmed facts from current code

- `User.DefaultPOReceiverID` (`arx_go/auth.go:35`) is an `int`; `0` means unset
  (`arx_go/auth.go:33` comment: "0 = unset, falls back to global config").
  `userByID` (`auth.go:50-66`) scans `default_po_receiver_id` via `sql.NullInt64` and
  coerces null to `0`.
- Two entry points build a fresh `models.PurchaseOrder` and call
  `h.applyPODefaults(r, &po)`, then render `pos/po_edit.html` with `IsNew: true`:
  - `PONew` — GET `/pos/new` (`pos.go:456-469`)
  - `RFQNew` — GET `/rfqs/new` (`pos.go:2018-2031`)
- `applyPODefaults` (`pos.go:409-454`) reads `u.DefaultPOReceiverID`/`u.DefaultPOContactID`
  from `h.currentUser(r)` and only populates `po.ReceiverID`/`po.ReceiverName`/etc. when
  `receiverID > 0`. It currently returns only `(supplierContacts, receiverContacts
  []ContactSummary)` — no signal about whether a default was found.
- Settings UI: `arx_go/templates/settings/settings.html:646-697`, tab
  `id="tab-preferences"`, section header "PO Defaults" (line 652). The preferences save
  handler (`SettingsPreferencesSave`, `settings.go:573-604`) redirects to
  `/settings#preferences` on success (line 603) — confirms `#preferences` is the
  established URL fragment for this tab.
- `po_edit.html` banner pattern (confirmed at `templates/pos/po_edit.html:22-33`):
  ```
  {{if not .IsNew}}
    {{if .DuplicateFrom}}<div class="alert alert-info">Duplicating from PO #...</div>{{end}}
  {{end}}
  {{if .RFQAddFrom}}
    <div class="alert alert-info">Adding another supplier's quote...</div>
  {{else if and .IsRFQ .IsNew}}
    <div class="alert alert-info">New Request for Quotation...</div>
  {{end}}
  ```
  A new banner slots in alongside these `alert alert-info` blocks.

## Change

Footprint: 1 return-value addition, 2 call-site edits, 1 template block.

1. **`arx_go/pos.go` — `applyPODefaults`** (`pos.go:409-454`): add a `bool` return
   indicating whether the user has no default receiver configured, e.g.
   `hasNoDefaultReceiver` — true when `h.currentUser(r) != nil` and
   `u.DefaultPOReceiverID <= 0`. (If there's no logged-in user at all, `RequireAuth`
   already would have redirected before reaching these handlers, so this should only
   occur for an authenticated user with the field genuinely unset — but guard both
   cases defensively: only surface the nudge when a user is present and unset, not
   when `h.currentUser(r) == nil`.)

2. **`PONew`** (`pos.go:462`) and **`RFQNew`** (`pos.go:2024`): capture the new return
   value and pass it into the template map as e.g. `"NoDefaultReceiver": noDefault`.

3. **`templates/pos/po_edit.html`**: add a banner conditioned on `.IsNew` and
   `.NoDefaultReceiver`, following the existing `alert-info` pattern, e.g.:
   ```html
   {{if and .IsNew .NoDefaultReceiver}}
   <div class="alert alert-info">
     No default receiver is set for your account &mdash; pick one below, or
     <a href="/settings#preferences">finish setting up your PO defaults</a> so new
     POs/RFQs pre-fill automatically.
   </div>
   {{end}}
   ```
   Exact wording is an open question (see below); this is a placeholder consistent
   with the sibling banners' tone.

Nothing else changes: no schema, no new column, no JS.

## Open questions (not decided by this plan — issue text doesn't specify)

- **Dismissible vs. persistent banner.** Options:
  a) Plain persistent `alert-info` (matches existing sibling banners exactly, no JS) —
     disappears on its own once the user sets a default, since the condition becomes
     false.
  b) Bootstrap dismissible alert (`alert-dismissible` + close button) — user can hide
     it per-session without setting a default; reappears on next New PO/RFQ load since
     there's no stored dismissal state.
  Recommend (a) for consistency with the existing banners in this same file, unless
  the user wants a closable version.

- **Show every time vs. show once (persistent dismissal).** There is no
  dismissed-tracking column on the users table today. Options:
  a) Show every time the condition is true (no new state) — simplest, matches "nudge
     until you fix it" semantics, consistent with how the Duplicate/RFQ banners work
     (unconditional on their trigger).
  b) Add a `po_defaults_prompt_dismissed` (or similar) column + a dismiss endpoint so
     it only shows once — meaningfully more code (migration, handler, template JS) for
     a one-line issue with no stated requirement for permanent dismissal.
  Recommend (a): CLAUDE.md rule 2 (Simplicity First) and the issue's own scope (a
  single sentence, no persistence requirement) point away from adding new state for
  this until a user actually asks for "stop showing this."

- **Exact wording + settings anchor.** Placeholder text/link above
  (`/settings#preferences`, "finish setting up your PO defaults") is a best guess
  matching the Settings tab's existing "PO Defaults" heading and the confirmed
  `#preferences` fragment used elsewhere in this codebase. Confirm wording and anchor
  target before implementing.

## Resolved decisions

- **Banner style: Option (a).** Plain persistent `alert-info`, no dismiss button, no JS.
- **Show frequency: Option (a).** Show every time `.NoDefaultReceiver` is true — no new dismissed-state column/endpoint.
- **Wording + anchor: confirmed.** Use:
  ```html
  {{if and .IsNew .NoDefaultReceiver}}
  <div class="alert alert-info">
    No default PO receiver set. Finish account setup in
    <a href="/settings#preferences">Settings</a> to auto-fill new POs.
  </div>
  {{end}}
  ```

## Manual verification plan

1. Set a test user's `default_po_receiver_id` to `NULL`/`0` (via Settings UI: clear
   the Default Receiver field and save, or directly in ArxDev). Load `/pos/new` —
   banner should appear.
2. Set a default receiver via Settings → My Preferences → save. Reload `/pos/new` —
   banner should be gone, receiver should pre-fill as today.
3. Repeat both checks on `/rfqs/new`.
4. Confirm `/po/{id}` (edit existing PO) and RFQ duplicate/add-supplier flows are
   unaffected — banner is gated on `.IsNew`, so existing-PO edits and duplicates never
   show it.

**Regression test suggestion (not written, per CLAUDE.md rule 5 — offer only):** a
table-driven unit test on `applyPODefaults`'s new bool return (receiver unset → true,
receiver set → false, no current user → false) would be cheap and catch a future
refactor silently dropping the flag. Existing `pos_test.go`/`settings_test.go` (if any)
should be checked for a natural home before adding a new file.
