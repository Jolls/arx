Arx v0.8.1 — September 2026
========================

BUG FIXES

  Parts can now use part categories added in Settings.
  Removing a part category that parts still use now shows a clear message.
  Saving test results, receiving a PO or converting an RFQ no longer half-saves on an error.
  Created/changed times now show in your own timezone.

Arx v0.7.60 — September 2026
========================

NEW FEATURES

  Create RFQs from a BOM
  Builds one RFQ per default supplier for N assemblies, netted against stock.

  Resizable and Reorderable Columns
  Drag list-table columns to resize or reorder them; Arx remembers your layout.

  Test Report Upgrades
  The test report gets column reordering, date-range filters, and copy-all for Excel.

  Override Default Results
  Double-click a blue default_result field on a test record to override it.

  Automatic Primary Attachment
  The first attachment on a part or supplier becomes primary automatically.

BUG FIXES

  Test step defaults like {record.pn} no longer get evaluated as math.

SECURITY

  Named Queries, backup, database settings and Utilities are now admin-only.
  Settings backup no longer includes the DigiKey secret.
  File access no longer follows symlinks outside configured folders.
  Login errors no longer reveal database details.

Arx v0.7.48 — September 2026
========================

NEW FEATURES

  DigiKey Import
  Fetch pricing, datasheet, photo, and manufacturer info from DigiKey when
  adding or editing a supplier link.

  Start RFQ from a Purchase Order
  A "Start RFQ" button on draft POs copies the lines into a new RFQ.

  Copy for Ordering
  Copies part number and quantity pairs from a PO for pasting into a
  supplier's bulk order form.

  PO Line Autofill
  Picking a part on a PO line fills in quantity and unit cost.

  Import BOM from Excel
  Paste part numbers and quantities from Excel to add or update BOM lines.

  BOM Search by Description
  BOM lines can be searched by description as well as part number.

  Multiple File Uploads
  Attach several files to a part at once, each with its own category.

  Duplicate Attachment Warning
  Warns before attaching a file or link that is already attached elsewhere.

  Vendor-Scoped Attachments
  Part attachments can be tied to a specific supplier or manufacturer part.

  Preferred Supplier Card
  The part page now shows the preferred supplier's part number and price.

  Quick-Add New Part
  New Part opens a quick popup for part number and description first.

  Filter Wildcards
  Table filters accept * and ? wildcards, such as 65*-013*.

  Formula Functions
  Test formulas can use min, max, abs, mod, round, floor, ceil, sqrt, pow.

  Delete Old Prices
  Deactivated prices can now be permanently deleted.

BUG FIXES

  Wrong price saved when accepting a PO's suggested new unit cost.
  Large file uploads now show a clear "file too large" message.
  Sub-tabs and PO buttons now wrap on narrow windows.
  Error page "Back" link returns to the page you came from.
  Copy for Excel buttons on Build Cost and Test Report pages.
  Missing breadcrumb on the part Records tab.

Arx v0.7.30 — September 2026
========================

BUG FIXES

  Supplier Link Preference Field
  Fixed a crash when saving a supplier link's preference; it's now a
  Primary/Alternate/Backup dropdown instead of free text.

  Supplier Link Forms Keep Your Input
  Add/Edit Supplier Link forms no longer lose what you typed if
  validation or a save fails.

  Edit Supplier Link Shows Supplier Name
  The Edit Supplier Link form no longer shows a blank supplier name
  for a valid link.

  Consistent Part Description Labeling
  The part field previously labeled inconsistently as Title or Name
  is now labeled Description everywhere.

Arx v0.7.27 — September 2026
========================

NEW FEATURES

  Unsaved Changes Warning on Purchase Orders
  Warns before you navigate away from a PO with unsaved edits.

  Receiver Setup Reminder
  Prompts new POs/RFQs to finish setup if no default receiver is set.

  Upload Files Directly to a Folder
  PO, supplier, and document-control folders now accept direct uploads.

Arx v0.7.22 — August 2026
========================

NEW FEATURES

  Test Records Tab on Part, Lot, and Unit Pages
  Part, lot, and unit pages now have a Records subtab listing every
  active test record, linking into each one.

  Lot Notes
  Lots can now carry a free-text note, editable from the lot page and
  shown wherever lot info appears, including on linked test records.

  Test Record Session Notes
  Test records can now carry a session-wide note, separate from
  per-step comments, that locks with the record.

  Linked Build Shown on Test Records
  A test record's view now shows the build that produced its unit,
  without needing to open Edit.

  Receive All on Purchase Orders
  A "Receive All" button on the PO receive form fills every open line
  with its full remaining quantity in one click.

BUG FIXES

  Fixed the inline "Build this unit" panel always defaulting to a
  quantity of 1, even for lot/batch records that allow more.

  Fixed saving a test record linked to a serialized unit sometimes
  creating a duplicate unit if the serial number was edited after
  linking.

  Fixed the Suppliers list showing a truncated, falsely-successful
  result on a mid-load database error.

  Fixed inconsistent caching and filenames across the file-download
  routes, which could serve a stale file after a replacement upload.

Arx v0.7.13 — July 2026
========================

NEW FEATURES

  Manual Unit Entry
  Serialized parts can now have units added or edited manually, for
  serials that predate test-based tracking.

  Lots & Units on the Part Dashboard
  A part's detail page now shows Lots and Units cards with counts and
  the most recent items.

BUG FIXES

  Fixed /settings being unreachable when the database connection was
  broken but still configured.

  Fixed PDF attachment thumbnails failing to render in some installs.

  Fixed the Browse file/folder dialog sometimes opening behind the
  main Arx window.

  Fixed replacing a PDF attachment sometimes still showing the old
  file due to caching, and regenerated thumbnails overwriting the
  original.

Arx v0.7.0 — July 2026
========================

NEW FEATURES

  Serialized Unit Tracking
  Parts can be marked None, Lot, Serial, or Lot & Serial tracked;
  testing a serial-tracked part creates a real, traceable unit.

  Build & Unit Traceability View
  A build's Units tab and each unit's "birth certificate" page trace
  components back through its lot and build history, in both
  directions.

  Build-at-Test-Time
  Testing a serial or lot-and-serial part now offers an inline panel
  to build the unit right from the test record.

BUG FIXES

  Fixed Settings folder/file Browse dialogs sometimes opening behind
  the main Arx window.

  Fixed BOM view rows for lazily-expanded sub-assemblies linking to a
  broken part page with a blank quantity.

Arx v0.6.24 — July 2026
========================

SECURITY

  Login is now required for every settings page and save action once
  Arx is connected to a database, with constant-time password checks,
  session rotation, a 7-day idle timeout, and login throttling.

  Added an admin flag; managing users, resetting passwords, or
  changing approval permissions now requires an admin account.

BUG FIXES

  Settings > Backup now includes several inventory and traceability
  tables that were previously left out.

  Fixed Roll Up Cost and the BOM tab disagreeing on parts with a
  $0.00 preferred price.

  Fixed editing or deleting a line on one PO or BOM sometimes
  affecting a different one's rows.

  Fixed the record history view not flagging spec or acceptance-
  criteria changes between test events.

  Fixed several reports and saved queries returning stale or
  mismatched results.

Arx v0.6.4 — July 2026
========================

SECURITY

  Database passwords and the session-signing secret now live in a
  per-user config file instead of the shared, exe-adjacent one,
  migrated automatically on first run.

BUG FIXES

  Fixed folder-root settings saving a path specific to whoever last
  saved Settings, instead of each user's own profile.

  Fixed two stored named queries that had drifted out of sync with
  what they're supposed to return.

Arx v0.6.0 — July 2026
========================

NEW FEATURES

  Inventory — Lot & Batch Tracking
  Batch/lot-controlled parts now track lots through receipts and
  builds, with two-way genealogy tracing and a cross-part All Lots
  page.

  Inventory — Build Flow
  A Build subtab consumes BOM components and produces the output
  part in one transaction, tracking lots and required/on-hand
  quantities.

  Parts — Reorder Points
  Set a Reorder Minimum on any part; parts below it are flagged on
  lists and a Reports dashboard card.

  Reports Dashboard
  A new Reports tab adds a KPI dashboard plus Spend, Yield, Failure
  Modes, On-Time Delivery, PO Cycle Time, and Data Quality views.

  Accent Colors
  Choose a preset accent color theme from Settings.

  Purchasing Improvements
  PO lines link to vendor part attachments, "Supplier Code" is
  renamed to "Folder Stub", contacts show their POs, and PO defaults
  are now set per user.

  Small Touches
  Per-tab favicon, a Type filter dropdown, a tiered Price History
  chart, and a BOM "Cost to Build" calculator.

BUG FIXES

  Fixed colliding auto-issued lot numbers, a missing Lots subtab on
  first load, unreadable badges under some accent themes, dropped PO
  lines with only partial data, and clipped row-action menus.

Arx v0.5.88 — July 2026
========================

NEW FEATURES

  Part Numbering — Next Available Number Suggestion
  The New Part form suggests the next available base number as you
  type, per your Settings numbering rules.

  Company Logo
  Upload your company logo in Settings; it appears in the header,
  the login page, and printed PO PDFs.

  Attachments — "Where Used" and Faster Actions
  See every part/vendor linked to a file via "Where used"; per-
  attachment actions are now one menu, plus comments and clipboard-
  paste photos.

  Contacts Linked to Purchase Orders
  A contact's page lists its POs, and PO contacts link back; vendors
  gained an "Other Contacts" card.

  Settings — Utilities and Named Queries
  A Utilities section runs data-integrity checks; a Named Queries
  editor builds spec_nom queries without writing SQL.

  Attachments — Smarter Link Handling
  Pasting a file path auto-detects URL/absolute/UNC and shows a
  copy-path control; internal LOCAL: links are hidden.

  BOM and Purchasing Improvements
  BOMs support Expand/Collapse All, PO item search includes
  description, and the two post-save PO suggestion banners are
  merged into one.

  Small Touches
  Per-tab nav icons, a header icon, auto-filled Order Number, and
  more attachments shown at a glance.

BUG FIXES

  Fixed a crash on PO save with only one post-save suggestion, a
  Settings crash on failed auto-connect, and a wrong default hint on
  new BOM line quantity.

Arx v0.5.66 — July 2026
========================

NEW FEATURES

  Purchasing — Request for Quotation (RFQ)
  Request quotes from suppliers, compare them side-by-side, and
  award one with a click to create a PO.

  Purchasing — Receiving and Goods Receipt
  Receive PO line items in full or partially, updating stock, PO
  status, and receipt history.

  Parts — Price History
  A Price History tab charts unit cost over time from POs and price
  lists.

  Parts — BOM Cost Rollup and Labor
  Assembly rollups now use each part's preferred supplier price; a
  new Labor part type adds hours to the rollup.

  Parts — Duplicate a Part
  Duplicate a part, including its BOM, from the part page.

  Parts — Attachment Browse and Import
  Browse copies or moves a file into Doc Control, auto-renamed from
  the part; a matching existing file offers to link instead.

  Test Records — Approval Lifecycle
  Records move through WIP, Complete, and Approved, with a reviewer
  role and an audit log of every event.

  Test Records — Result Snapshots and Frozen Definitions
  Records snapshot their form definition at creation and their
  results on each completion, viewable with changes highlighted.

  Test Records — Form Revisions and Step Archiving
  Forms carry a revision number, and steps can be archived without
  affecting historical records.

  Test Records — Faster Data Entry
  Duplicate a record to retest, bulk-lock records Complete, auto-save
  with a restore prompt, and press Enter to advance fields.

  Lists — Export, Sharing, and Columns
  Export lists to CSV; filter/sort/page settings are shareable via
  the URL; a Columns dropdown, counts, and date-range filters were
  added.

  Detail Pages Redesigned
  Part, Supplier, and Contact pages are now dashboard-style summaries
  of their sub-tabs.

  Ease-of-Use Improvements
  Supplier pickers now search as you type, new prices/PO dates
  default to today, and part titles show alongside their numbers.

BUG FIXES

  Fixed attachments saving without their category, list rows needing
  a double-click after filtering, the PO/RFQ default contact using
  the wrong company, and simultaneous test records sharing a serial
  number.

Arx v0.5.29 — June 2026
========================

NEW FEATURES

  Parts Master — Inventory and Stock Tracking
  Stockable parts now track stock on hand, with a Transactions tab
  and reasoned manual adjustments.

  Parts Master — Purchase Order Status
  POs move through Draft, Open, Sent, Partially Received, and
  Closed/Cancelled, logged and filterable.

  Parts Master — Purchase Order Approval
  POs require approval before sending or printing; an approved PO is
  re-reviewed on edit; approvers are set in Settings.

  Settings — Download Backup
  A Download Backup button in Settings exports all data as a ZIP of
  CSV files.

Arx v0.5.24 — June 2026
========================

NEW FEATURES

  Both Apps — User Accounts and Sign-In
  Arx now requires signing in; users are managed in Settings, and
  changes now carry a real author's name instead of the server
  account.

Arx v0.5.17 — June 2026
=======================

NEW FEATURES

  Both Apps — Faster Navigation
  List pages now load immediately and fill in rows in the
  background, with no blank-page flash.

  Both Apps — Column Widths and Overflow Tooltips
  Columns size to the viewport; truncated cells show full text on
  hover.

  Both Apps — Header and Tab Tooltips
  Column headers and most field labels show a clarifying tooltip on
  hover.

  Test Records — Form Definition History
  The history view now shows each field's actual previous value, not
  just which rows changed.

BUG FIXES

  Test Records — Step Name False Positives
  Steps named like "created_at" or "alternate" no longer get flagged
  by the query-safety check.

Arx v0.5.12 — June 2026
=======================

NEW FEATURES

  Parts Master — Records Tab
  Test Records is now a tab in the shared nav bar alongside Parts,
  Vendors, POs, and Contacts.

  Parts Master — Part Subtabs per Category
  Part detail tabs (BOM, Order History, Pricing, Mfg Parts,
  Suppliers) are now configurable per category, editable in
  Settings.

Arx v0.5.6 — June 2026
=======================

NEW FEATURES

  Both Apps — Single Address
  Parts Master and Test Records now run together at one address;
  update any bookmarks to the main Arx address.

Arx v0.5.1 — June 2026
=======================

NEW FEATURES

  Both Apps — Single Program
  Arx is now one program with one tray icon instead of two.

BUG FIXES

  Test Records — Form Definition View
  View Definition spec_nom now shows the raw stored value, matching
  the editor, instead of expanding cross-reference tokens.

  Test Records — Long Comments in Results Table
  Long step comments no longer stretch the Comment column across the
  full table width.

Arx v0.4.1 — June 2026
=======================

BUG FIXES

  Both Apps — Security hardening
  CSRF protection now covers all form submissions, the session key
  auto-generates per install, and config file writes are atomic.

  Both Apps — Cleaner shutdown on port conflict
  A second instance that fails to bind now exits cleanly instead of
  terminating abruptly.

  Test Records — Date field formula bug
  Editing a record with a Test Date step no longer overwrites the
  saved result with a raw decimal value.

Arx v0.4.0 — May 2026
======================

NEW FEATURES

  Test Records — Conditional Step Visibility
  Steps can now be shown or hidden by an expression based on other
  results or record context.

  Test Records — Result Display Format
  A step's format field now controls how its result displays, with
  a placeholder hint to guide entry.

  Parts Master — Units of Measure
  Parts now have a base unit, and sourcing can specify a purchase
  unit via a unit reference table.

  Parts Master — Vendors
  The Suppliers section is renamed to Vendors, with a Roles row for
  supplier/manufacturer.

  Parts Master — Manufacturer Page Links
  Manufacturer names on the Mfg Parts tab now link to that company's
  detail page.

  Both Apps — Windows Login Auto-Fill
  New POs and part requests pre-fill your Windows login name, which
  is also recorded in the audit trail.

Arx v0.3.46 — May 2026
=======================

NEW FEATURES

  Parts Master — Manufacturer Part Numbers (MPN)
  A "Mfg Parts" tab lists a part's manufacturers and their part
  numbers; companies can now be flagged as manufacturers.

Arx v0.3.31 — May 2026
=======================

NEW FEATURES

  Test Records — Print / PDF Export
  Test records can now be printed or saved as a PDF, with an
  auto-suggested filename.

  Test Records — Image Gallery
  Screenshots display as a tiled gallery below the results table and
  are included when printing or saving as a PDF.

  Test Records — Prev / Next Navigation
  Arrow buttons step through records in the same form without
  returning to the list.

  Parts Master — BOM Rollup Cost
  A "Run Rollup" button calculates a part's cost from its components
  and shows unit cost per line.

  Parts Master — PO Revision Tracking
  The part revision is captured at order time and shown on the PO.

  Parts Master — PO Print Improvements
  PO print pages suppress browser headers, auto-name the PDF, and
  can open the PO folder after printing.

  Parts Master — BOM Editing
  BOM rows can be added, updated, and removed directly in the web
  UI without the Excel workbook.

  Both Apps — Cross-App Navigation
  A header link switches between Parts Master and Test Records.

  Both Apps — Test Mode Toggle in Settings
  Test mode can be switched in Settings, with a confirmation prompt,
  instead of editing the .env file.

  Both Apps — Schema Version Check
  A banner warns if the database schema doesn't match, signaling a
  needed migration.

  Both Apps — What's New
  Release notes are now embedded in-app, shown via a banner after
  updating.

BUG FIXES

  Fixed a crash on the new supplier form and a PO creation failure
  on databases with triggers.

  Fixed the where-used page failing to load when BOM quantity or
  item number was NULL.

  Each app now has its own local config file, so Parts Master and
  Test Records no longer overwrite each other's settings when run
  from the same folder.
