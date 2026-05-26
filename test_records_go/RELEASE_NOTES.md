Arx v0.3.46 — May 2026

NEW FEATURES

  Parts Master — Manufacturer Part Numbers (MPN)
  Each part now has a "Mfg Parts" tab listing the manufacturers who
  make it and their part numbers. Add as many manufacturer/MPN pairs
  as needed. Companies can now be flagged as manufacturers in addition
  to (or instead of) suppliers, making them available in the MPN
  manufacturer dropdown.

Arx v0.3.31 — May 2026

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
