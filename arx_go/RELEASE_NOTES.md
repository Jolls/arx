Arx v0.5.88 — July 2026
========================

NEW FEATURES

  Part Numbering — Next Available Number Suggestion
  The New Part form now suggests the next available base number as you
  type, based on rules you set in Settings (separator, segment position,
  zero-padding, and whether to fill gaps in the sequence or always jump
  to max+1).

  Company Logo
  Upload your company logo in Settings and it appears in the app header,
  the login page, and on printed PO PDFs.

  Attachments — "Where Used" and Faster Actions
  See every part and vendor linked to a given file from a new "Where
  used" view. The per-attachment actions (Set default, Edit, Where used,
  Delete) are now grouped into a single menu instead of a row of buttons.
  Attachments also support a free-text comment, and part/test-record
  photos can be added by pasting directly from the clipboard.

  Contacts Linked to Purchase Orders
  A contact's detail page now lists every PO tied to that contact, and a
  PO's supplier/receiver contact links back to their contact record. The
  vendor detail page also gained an "Other Contacts" card for
  non-default contacts, and the contacts list has a show/hide inactive
  toggle.

  Settings — Utilities and Named Queries
  A new Utilities section under Settings runs read-only data-integrity
  checks: dead attachment links, orphaned part pointers, soft-deleted
  primary attachments, and PO status drift. A new Named Queries editor
  lets you build and test-drive `spec_nom` queries without writing SQL.

  Attachments — Smarter Link Handling
  Pasting a file path now auto-detects whether it's a URL, absolute
  path, or UNC path, and shows a copy-path control for local paths.
  Internal `LOCAL:` links are now hidden from all views.

  BOM and Purchasing Improvements
  Assembly BOMs support Expand All / Collapse All to view nested
  sub-assemblies inline. The PO add-item form now searches by
  description as well as part number. The two post-save PO suggestion
  banners (new vendor part number, new price) are merged into one so
  dismissing one no longer discards the other.

  Small Touches
  Per-tab icons on the main nav bar, the Arx icon in the header, an
  auto-filled Order Number when adding attachments, and more of the
  Part Details page's attachments shown at a glance.

BUG FIXES

  Fixed a crash on the PO detail page after saving when only one kind
  of post-save suggestion (new vendor part number or new price) applied.
  Fixed a crash on Settings when the database auto-connect fails on
  startup. Fixed the new BOM line quantity field showing the wrong
  default hint.

Arx v0.5.66 — July 2026
========================

NEW FEATURES

  Purchasing — Request for Quotation (RFQ)
  Request a quote from a supplier, add competing quotes from other
  suppliers to the same RFQ, and enter each one's unit price and lead
  time on a side-by-side comparison grid. Award the winner with one click
  to turn it into a purchase order; the RFQ and its quotes are kept on
  record.

  Purchasing — Receiving and Goods Receipt
  Receive line items on a PO, in full or partially, straight from the PO
  page. Each receipt updates stock on hand, advances the PO status, and is
  recorded in a receipt history that cross-links to the part's stock
  transactions.

  Parts — Price History
  A new Price History tab on each part charts its unit cost over time,
  with a point for every purchase-order line and price-list entry. Hover
  any point to see the PO number, supplier, date, and cost.

  Parts — BOM Cost Rollup and Labor
  Assembly cost rollups now use each part's preferred supplier price, set
  with a "Set preferred" control on the Pricing tab. A new Operation/Labor
  part type lets you add a labor job to a BOM (quantity = hours) so the
  rollup includes hours times the hourly rate.

  Parts — Duplicate a Part
  Duplicate an existing part, including its full bill of materials, from
  the part page — a fast start for a similar part.

  Parts — Attachment Browse and Import
  A Browse button on part attachments copies (or moves) a file you pick
  into Doc Control, automatically renaming it from the part number,
  revision, title, and category. If a matching file already exists, it
  offers to link to that one instead.

  Test Records — Approval Lifecycle
  Test records now move through WIP, Complete, and Approved. Anyone can
  mark a record Complete; a designated TR Reviewer approves it, after
  which only a reviewer can unlock it. Every complete, approve, and unlock
  event is kept in an audit log with the user, time, and reason.

  Test Records — Result Snapshots and Frozen Definitions
  A saved record is frozen to a snapshot of the form as it was at
  creation, so later edits to a form never change existing records. Each
  time a record is completed it also captures a snapshot of its results,
  viewable in the audit log with changes since the previous completion
  highlighted.

  Test Records — Form Revisions and Step Archiving
  Form definitions now carry a formal revision number that is captured on
  each record. Individual test steps can be archived and restored, so they
  drop off new records while staying on historical ones.

  Test Records — Faster Data Entry
  Duplicate a record to quickly re-test with the same serial number, lock
  several records Complete at once, auto-save partial results as you go
  with a restore prompt if you leave and come back, and press Enter to
  advance to the next input.

  Lists — Export, Sharing, and Columns
  Export the Parts, PO, and BOM lists to CSV. Filter, sort, and page
  settings are now stored in the page address, so a filtered view can be
  bookmarked or shared and a pasted link reproduces exactly what the
  sender saw. Every list has a Columns dropdown to hide or show columns,
  new Attachments and PO Lines counts, a "Show inactive" toggle, and
  From/To date-range filters.

  Detail Pages Redesigned
  Part, Supplier, and Contact pages are now dashboard-style summaries that
  pull together highlights from each of their sub-tabs at a glance.

  Ease-of-Use Improvements
  Supplier pickers across pricing, contact, sourcing, and settings forms
  now search as you type. New prices and new PO dates default to today,
  and the part's title now appears beside its number throughout the part
  pages.

BUG FIXES

  Fixed new attachments saving without their selected category.
  Fixed needing to double-click a row link right after filtering a list.
  Fixed the PO/RFQ default contact using the wrong company's contact.
  Fixed simultaneous new test records occasionally sharing a serial number.

Arx v0.5.29 — June 2026
========================

NEW FEATURES

  Parts Master — Inventory and Stock Tracking
  Stockable parts now track stock on hand. A new Transactions tab shows
  every stock movement with a running balance, and you can record manual
  adjustments — a reason is required for each. Which part categories are
  stockable is configurable in Settings.

  Parts Master — Purchase Order Status
  Purchase orders now move through a clear lifecycle — Draft, Open, Sent,
  Partially Received, and Closed (or Cancelled) — with buttons on the PO
  page to advance the status. Every change is logged with who and when,
  the current status is shown prominently, and the PO list can be filtered
  by status.

  Parts Master — Purchase Order Approval
  Purchase orders now go through an approval step. Submit a PO for
  approval, and a designated approver can approve or reject it. A PO
  cannot be sent or printed until it has been approved, and editing an
  approved PO clears its approval so the change gets re-reviewed.
  Approvers are designated with a "PO Approver" toggle in Settings. PO
  status and approval events share one combined history timeline.

  Settings — Download Backup
  The Configuration tab in Settings has a Download Backup button that
  exports all of your data as a ZIP of spreadsheet-friendly CSV files.

Arx v0.5.24 — June 2026
========================

NEW FEATURES

  Both Apps — User Accounts and Sign-In
  Arx now requires signing in with a username and password. On first run,
  you will be prompted to create the initial admin account. Additional users
  can be added, reset, and deactivated from Settings. Your name now appears
  in the header and on lock/unlock events, form-definition history, and PO
  and part creation defaults — giving every change a real author instead of
  the server account.

Arx v0.5.17 — June 2026
=======================

NEW FEATURES

  Both Apps — Faster Navigation
  List pages (Parts, Vendors, POs, Contacts) now load the page frame
  immediately and fill in the rows in the background. Navigating between
  sections no longer shows a blank white flash while the server assembles
  the full page.

  Both Apps — Column Widths and Overflow Tooltips
  Table columns now size proportionally to the viewport. Cells that are
  too narrow to show their full content are truncated with an ellipsis,
  and hovering shows the full text in a tooltip.

  Both Apps — Header and Tab Tooltips
  Column headers and most field labels now show a short tooltip on hover
  to clarify what the field contains.

  Test Records — Form Definition History
  The form definition history view now shows the actual previous values of
  each changed field, not just which rows were modified. You can see
  exactly what was changed and what it said before.

BUG FIXES

  Test Records — Step Name False Positives
  Steps with names like "created_at" or "alternate" no longer get flagged
  incorrectly by the query-safety check.

Arx v0.5.12 — June 2026
=======================

NEW FEATURES

  Parts Master — Records Tab
  Test Records is now a tab in the Parts Master navigation bar alongside
  Parts, Vendors, POs, and Contacts. Both apps share the same header and
  tab bar, so switching between them feels like changing tabs rather than
  opening a different app. The separate cross-app link in the header is gone.

  Parts Master — Part Subtabs per Category
  The detail tabs shown for a part (BOM, Order History, Pricing, Mfg Parts,
  Suppliers) can now be configured per part category. Tabs that don't apply
  to a category are grayed out rather than cluttering the view. Categories
  and their tab visibility are editable in Settings.

Arx v0.5.6 — June 2026
=======================

NEW FEATURES

  Both Apps — Single Address
  Parts Master and Test Records now run together at one address. Open
  Arx and use the Test Records link in the header to switch apps. The
  separate Test Records port is gone; update any bookmarks to the main
  Arx address. Settings now covers both apps in one page.

Arx v0.5.1 — June 2026
=======================

NEW FEATURES

  Both Apps — Single Program
  Arx is now one program with one tray icon instead of two.

BUG FIXES

  Test Records — Form Definition View
  View Definition spec_nom now shows the raw stored value, matching the
  editor. Previously cross-reference tokens like {629} were expanded.

  Test Records — Long Comments in Results Table
  Long step comments no longer stretch the Comment column across the
  full table width.

Arx v0.4.1 — June 2026
=======================

BUG FIXES

  Both Apps — Security hardening
  CSRF protection now covers all form submissions. Session key is
  auto-generated per installation. Config file writes are atomic.

  Both Apps — Cleaner shutdown on port conflict
  A second instance that fails to bind now exits cleanly instead of
  terminating abruptly.

  Test Records — Date field formula bug
  Editing a record with a Test Date step no longer overwrites the saved
  result with a raw decimal value.

Arx v0.4.0 — May 2026
======================

NEW FEATURES

  Test Records — Conditional Step Visibility
  Steps in a form can now be shown or hidden based on other step results
  or record context. Enter an expression like {record.type}!=Re-Test or
  {12}=Yes in the hide column of the form definition editor. Steps hidden
  by an expression are excluded from the record view, edit view, and
  print output — no manual editing needed when the same form is used for
  multiple record types.

  Test Records — Result Display Format
  The format field on a step definition now controls how the result
  appears in the record view and print output. The edit input shows a
  placeholder hint to guide data entry to the expected format.

  Parts Master — Units of Measure
  Parts now have a base unit and sourcing relationships can specify a
  purchase unit. A unit reference table manages available units. The
  supplier parts list shows the effective unit, falling back to the
  part's base unit when no purchase unit is set.

  Parts Master — Vendors
  The Suppliers section has been renamed to Vendors throughout the app.
  The vendor detail page now shows a Roles row indicating whether the
  company is flagged as a supplier, manufacturer, or both.

  Parts Master — Manufacturer Page Links
  On a part's Mfg Parts tab, the manufacturer name is now a clickable
  link to that company's detail page.

  Both Apps — Windows Login Auto-Fill
  New POs pre-fill the Orderer field and new part requests pre-fill the
  Requested By field with your Windows login name. The same login is
  recorded in the audit trail for form and record lock and unlock events.

Arx v0.3.46 — May 2026
=======================

NEW FEATURES

  Parts Master — Manufacturer Part Numbers (MPN)
  Each part now has a "Mfg Parts" tab listing the manufacturers who
  make it and their part numbers. Add as many manufacturer/MPN pairs
  as needed. Companies can now be flagged as manufacturers in addition
  to (or instead of) suppliers, making them available in the MPN
  manufacturer dropdown.

Arx v0.3.31 — May 2026
=======================

NEW FEATURES

  Test Records — Print / PDF Export
  Test records can now be printed or saved as a PDF. Click the Print
  button on any record to open a clean print-friendly page. The
  suggested PDF filename is automatically set to the serial number
  and part number.

  Test Records — Image Gallery
  Screenshots now display as a tiled gallery below the results table
  and are included when printing or saving as a PDF. Image rows in
  the results table show "[see gallery]" to keep the table compact.

  Test Records — Prev / Next Navigation
  Arrow buttons on the record view let you step through records in
  the same form without returning to the list.

  Parts Master — BOM Rollup Cost
  A "Run Rollup" button on the BOM tab calculates the total cost of
  a part from its direct components and saves it to the part record.
  The BOM table now shows the unit cost for each component.

  Parts Master — PO Revision Tracking
  The part revision is captured at the time of ordering and shown on
  the PO detail, edit form, and print views.

  Parts Master — PO Print Improvements
  PO print pages suppress browser headers for clean PDF output. The
  suggested PDF filename is set to the PO number and supplier code.
  After marking a PO as printed, a button opens the PO folder in
  Windows Explorer (created automatically if it does not exist).

  Parts Master — BOM Editing
  BOM rows can be added, updated, and removed directly from the web
  UI without using the Excel workbook.

  Both Apps — Cross-App Navigation
  A link in the header lets you switch between Parts Master and Test
  Records without going through Settings.

  Both Apps — Test Mode Toggle in Settings
  Test mode can be switched in the Settings UI without editing the
  .env file. A confirmation prompt warns you before switching.

  Both Apps — Schema Version Check
  A banner is shown if the app detects a database schema mismatch,
  indicating that a migration may be needed.

  Both Apps — What's New
  Release notes are now embedded in the app. A banner appears at the
  top of the page after updating to a new version.

BUG FIXES

  Parts Master — fixed a crash on the new supplier form.
  Parts Master — fixed PO creation failure on databases with triggers.
  Test Records — fixed an issue where the where-used page could fail
  to load when BOM quantity or item number was NULL.
  Both Apps — each app now has its own local config file so Parts
  Master and Test Records no longer overwrite each other's settings
  when run from the same folder.
