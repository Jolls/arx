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
