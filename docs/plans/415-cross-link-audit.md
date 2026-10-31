# #415 plan: cross-link audit (part / test record / PO receipt / lot / unit / build)

Audit done by reading templates, `static/shared/app.js` row builders, handlers and queries on `main` (2a1d2e5). No code changed. Gaps are IDs (G1...) for the maintainer to approve a subset. Post the Findings section as the comment on #415.

## Scope notes
- Already planned, not counted as gaps: #417 (part Summary "Traceability" card to /lots,/units,/builds filtered by part number), #414 (dashboard Recent Lots/Builds cards).
- There is NO build detail page. Builds are reachable only as a hover popout (`/popout/build/{id}`, `popouts.html:164`), the `/builds` list (`app.js:231`), and plain-text labels. Every "-> build" gap below is therefore blocked on Open Question Q1.
- "Receipt" = `inventory_transaction` row, `txn_type='receipt'`, `po_line_id` set (`SQL/inventory_transaction.sql`). A purchased lot links to its PO line via `lot.po_line_id` (`SQL/lot.sql`). Receipt rows also carry `lot_id`.
- Data links available in schema: `form_record.unit_id/lot_id/build_id`; `unit.lot_id/build_id`; `build.output_lot_id`; `lot.po_line_id`; `inventory_transaction.po_line_id/lot_id/build_id`.

## Findings: link matrix
Legend: YES = link exists (file:line). NO = gap (ID). N/A = no relationship in schema.

### Part detail (`parts/part_detail.html`, tabs in `shared/partials.html:202`)
| From -> To | Exists? | Where / gap |
|---|---|---|
| Part -> its records | YES | "Records" detail tab `partials.html:246`; Summary has no records card (see G11) |
| Part -> its lots | YES | Lots tab `partials.html:240`; Summary lots card rows `part_detail.html:199`; View all `:188` |
| Part -> its units | YES | Units tab `partials.html:243`; Summary rows `part_detail.html:230` |
| Part -> its builds | NO (list) | covered by #417 (/builds filtered); Build tab is the "do a build" form, not history |
| Part -> PO / receipts | YES | Summary POs `part_detail.html:166`; Order History `part_orders.html:25`; Transactions receipt reference `part_transactions.html:70` |
| Part -> filtered /lots,/units,/builds | planned | #417 |

### Test record (`records/records_show.html`, `record_edit.html`)
| From -> To | Exists? | Where / gap |
|---|---|---|
| Record -> part | YES | `records_show.html:33`, `record_edit.html:17` |
| Record -> form / form records | YES | breadcrumb `records_show.html:7` |
| Record -> lot | YES (show + edit) | `records_show.html:147` (lot card), `record_edit.html:79` ("trace"), `:120` ("edit in full") |
| Record -> unit | NO | G1. Record has `UnitID` + `UnitSerial` (`models/trmodels.go:78-79`) but no template renders a link; serial text at `records_show.html:34` is plain |
| Record -> build | NO | G2. Plain popout label `records_show.html:132` (comment at `:125` says no detail page); blocked on Q1 |
| Record -> PO / receipt (via lot) | NO | G3. Lot card (`records_show.html:143-160`) shows `LotDescription` text ("PO ...") but no PO link |
| Record rows in part/lot/unit Records tables -> unit/lot/build | NO | G4. `scopedRecordRow` (`records.go:361`) has no unit/lot ids; `app.js:344-352` renders SN -> /records/{id} only |
| Record rows in form records list -> unit/lot | NO | G4 (same: `/api/forms/{id}/records/rows`, `app.js:318-330`) |

### PO line receipts (`pos/po_detail.html`)
| From -> To | Exists? | Where / gap |
|---|---|---|
| PO line -> part | YES | `po_detail.html:229` |
| Receipt row -> part transactions | YES | `po_detail.html:321` (links `/part/{id}/transactions`) |
| Receipt row -> lot created | NO | G5. `POReceipt` (`internal/purchasing/purchasing.go:371`) and `ListPOReceipts` (`purchasing.sql:183`) don't select `it.lot_id`/lot number |
| PO line -> lots received against it | NO | G5 (same data; lot has `po_line_id`) |
| Receipt/PO -> test records of that lot | NO | G6 (transitively via lot; see G5) |
| Part transactions: receipt -> PO | YES | `part_transactions.html:70` |
| Part transactions: row -> lot | YES | `part_transactions.html:71` |
| Part transactions: row -> build | NO | G7. `inventory_transaction.build_id` not selected/rendered; blocked on Q1 |
| Part Order History -> PO | YES | `part_orders.html:25` |
| PO popout -> part | YES | `popouts.html:101` |

### Lot page (`parts/part_lot_trace.html`) and Lot list rows
| From -> To | Exists? | Where / gap |
|---|---|---|
| Lot -> part | YES | `part_lot_trace.html:24`, breadcrumb `:5` |
| Lot -> source/descendant lots + units | YES | `part_lot_trace.html:56-59, 95-98` (genealogy) |
| Lot -> its test records | YES | Records table `part_lot_trace.html:113`, rows -> /records/{id} (`app.js:345`) |
| Lot -> units that live in it (`unit.lot_id`) | NO | G8. Page lists genealogy only, not units of this lot; unit page links up to lot (`part_unit_trace.html:23`) but not down |
| Lot -> PO / PO line it was received on | NO | G9. `LotRow` (`internal/inventory/inventory.go:183`) lacks `po_line_id`/PO number; `GetLot`/`ListLots` (`inventory.sql:73,83`) don't select it. `lot_description` "PO x" is plain text |
| Lot -> build that produced it (`build.output_lot_id`) | NO | G10. Blocked on Q1 (target) |
| Lot -> inventory transactions of the lot | NO | G12. Part Transactions tab links to lot, not back |
| Lot list rows (part Lots tab / `/lots`) -> PO/build | NO | covered by G9/G10 if wanted on the lists (Q4) |

### Unit page (`parts/part_unit_trace.html`) and Unit list rows
| From -> To | Exists? | Where / gap |
|---|---|---|
| Unit -> part | YES | `part_unit_trace.html:21` |
| Unit -> lot | YES | `part_unit_trace.html:23`; Units tab `part_units.html:34`; `/units` rows `app.js:226` |
| Unit -> its test records | YES | table `part_unit_trace.html:107`, `app.js:345`; popout `popouts.html:138` |
| Unit -> build | NO (href) | G10. Plain popout label `part_unit_trace.html:25`; Q1 |
| Unit -> consumers / ancestors | YES | `part_unit_trace.html:52-55, 89-92` |
| `/units` list rows -> build | NO | not in list (Q4) |

### Build (`/builds` list, popouts, build form)
| From -> To | Exists? | Where / gap |
|---|---|---|
| Build row -> part | YES | `app.js:233` |
| Build row -> output lot | YES | `app.js:232, 237` (build id link goes to lot if present else Build tab) |
| Build -> units produced | NO (page) | popout only (`popouts.html:187`). G10 if Q1 = new page |
| Build -> test records of produced units / of linked record | NO | G10 (Q1) |
| Build form -> originating record | YES | `part_build.html:22` (`ReturnRecord` -> `/records/{id}/edit`) |
| Popout unit -> lot/records/consumers | YES | `popouts.html:127-155` |
| Popout unit -> build | NO | G10 (plain `BuildLabel`) |
| Popout build -> part/output lot/units/consumers | YES | `popouts.html:166-187` |

### Other gaps noticed
- G11: Part Summary has no recent-records card (Records is a tab only). Not in #417's scope (that card links lots/units/builds).
- G12: no Lot -> transactions link (above).
- G13: Part Details/Summary `/part/{id}/records`: "Form" column links `/forms/{id}/records` (OK), but `/records` rows give no way to reach the unit page (see G4).

## Gap list (for approval)
| ID | Gap | Depends on |
|---|---|---|
| G1 | Record show/edit -> unit page | none |
| G2 | Record -> build | Q1 |
| G3 | Record lot card -> PO (via lot.po_line_id) | G9 data |
| G4 | Scoped/form record lists -> unit and lot of each record | Q3 |
| G5 | PO Receipts table -> lot received (and line -> lot) | none |
| G6 | PO receipt / PO -> test records of that lot | G5; Q5 |
| G7 | Part Transactions row -> build | Q1 |
| G8 | Lot page -> units in the lot | none |
| G9 | Lot page -> PO of origin | none |
| G10 | Unit / Lot / popout -> build | Q1 |
| G11 | Part Summary recent-records card | Q6 |
| G12 | Lot page -> its transactions (Part Transactions tab) | Q7 |
| G13 | (merged into G4; ID retired) | |

## Proposed changes per gap (templates/handlers/queries; nothing applied)

### G1 Record -> unit
- Files: `arx_go/records.go` (`loadRecordTrace`, `RecordTrace` struct `:1387`; add `UnitID`, `UnitSerial` from `record.UnitID`/`record.UnitSerial`, already loaded), `arx_go/templates/records/records_show.html` (near `:33-34` serial, or a "Unit" row in the table beside Build `:131`), `arx_go/templates/records/record_edit.html` (near `:17`).
- Link: `/part/{{.Trace.PartID}}/units/{{.Trace.UnitID}}` (part id of unit equals record part).
- New sqlc query: no. No migration.

### G2 / G10 Build links (blocked on Q1)
- Option A (new page): add `GET /part/{id}/builds/{buildID}` (`main.go`, `build.go` handler, `templates/parts/part_build_trace.html`), reuse `GetBuildDetail`/`ListBuildConsumed`/`ListBuildUnits` (no new query); then link plain labels at `records_show.html:132`, `record_edit.html` (LinkedBuild), `part_unit_trace.html:25`, `popouts.html:127` (needs `BuildID` in `popout_unit` data, `api.go:912`; `u.BuildID` already available), lot page (new G10 row, needs `build_id` by output_lot_id: new sqlc query `GetBuildByOutputLot` in `internal/inventory/inventory.sql`), `/builds` row link `app.js:232`.
- Option B (no new page): link build labels to the build's output lot trace (`/part/{id}/lots/{outputLot}`) when present, else leave plain. Needs `OutputLotID` on `BuildOption` (`fetchBuildOption`, probably an existing field or a join add to `inventory.sql`).
- sqlc: A: 1 new query (lot -> build). B: possibly extend `GetBuild...` select. Run `sqlc generate`.

### G3 Record lot card -> PO
- Depends on G9 data (`LotRow.POLineID`/`PONumber`). Files: `internal/inventory/inventory.sql` + `inventory.go` (see G9), `arx_go/templates/records/records_show.html:143-160`.

### G4 Record list rows -> unit/lot
- Files: `arx_go/records.go` (`scopedRecordRow :361`, `scopedRecordsRows :378`, form-list row struct `~:328`), `internal/records` (`ListScopedRecords`, form records rows query; add `unit_id`, `lot_id`, serial via existing columns in `internal/records/records.sql` + `sqlc generate` + `internal/dbq`), `arx_go/static/shared/app.js:318-352` (render SN/unit link when `unitId` present).
- Needs a new column in existing sqlc queries (no new table; no migration).

### G5 PO Receipts -> lot
- Files: `internal/purchasing/purchasing.sql:183` (`ListPOReceipts` add `it.lot_id`, `l.lot_number` via LEFT JOIN lot), `internal/purchasing/purchasing.go:371` (`POReceipt` add `LotID *int`, `LotNumber string`; note `POReceipt(r)` conversion at `:526` requires field parity with the sqlc row), `arx_go/pos.go:1813` (`POReceiptView` + `fetchPOReceipts`), `arx_go/templates/pos/po_detail.html:307-325` (new "Lot" column, header + cell). Link `/part/{{PartID}}/lots/{{LotID}}`.
- Modified (not new) sqlc query; `sqlc generate`; update existing `TestIntegration_POReads_ReceiptsAndHistory` expectations (`po_reads_integration_test.go:277`).

### G6 Receipt/PO -> lot's test records
- Satisfied transitively once G5 lands (lot page has Records table). Direct link only if Q5 says so; otherwise drop.

### G7 Part Transactions -> build
- Files: transactions query (`internal/inventory/inventory.sql`, ledger view; add `build_id`), `inventory.go` txn view struct, `arx_go/templates/parts/part_transactions.html:70-71` (new link in Note/Reference). Blocked on Q1.

### G8 Lot -> units in lot
- Files: `internal/inventory/inventory.sql` (new query `ListLotUnits` by `unit.lot_id`; or reuse `ListUnits` with a lot filter if present: check `inventory.sql` `ListUnits`), `internal/inventory/inventory.go` wrapper, `arx_go/lot.go:150` `PartLotTrace` (add `"Units"`), `arx_go/templates/parts/part_lot_trace.html` (new "Units in this lot" table, links `/part/{{.PartID}}/units/{{.ID}}` with `data-popout-url="/popout/unit/{{.ID}}"`, same style as `part_units.html:33`).
- New sqlc query likely; run `sqlc generate`.

### G9 Lot -> PO
- Files: `internal/inventory/inventory.sql` (`GetLot :83` and `ListLots :73`: LEFT JOIN `po_line pol ON pol.id = l.po_line_id` LEFT JOIN `purchase_order po ON po.id = pol.po_id`, select `po.number`), `internal/inventory/inventory.go:183` (`LotRow` add `PONumber string`), then `lot.go` `lotListRow` only if lists wanted (Q4), `arx_go/templates/parts/part_lot_trace.html` (new "Purchase Order" dt/dd after Vendor Lot, link `/po/{{.Lot.PONumber}}` with `data-popout-url="/popout/po/{{.Lot.PONumber}}"`).
- Modified sqlc queries; `sqlc generate`. Note `LotRow` is shared by `ListRecentPartLots`/`ListPartLots` (field is cheap to include).

### G11 Part Summary recent-records card
- Files: `arx_go/parts.go` (`PartDetail` data: recent records + count, may reuse `scopedRecordsRows`/`ListScopedRecords` with limit, or new `ListRecentPartRecords` sqlc query in `internal/records/records.sql`), `arx_go/templates/parts/part_detail.html` (new card, "View all" -> `/part/{id}/records`). Avoid overlap with the #417 "Traceability" card (different card).

### G12 Lot -> transactions
- Files: `arx_go/templates/parts/part_lot_trace.html` only (link `/part/{{.Part.ID}}/transactions` if Q7 = unfiltered), or `part_transactions` lot-filter support (`inventory.go`, handler `PartTransactions`, template) if Q7 = filtered.

## Test plan

### Coverage audit (what exists today)
- `popout_unit_build_test.go:42,69,144` (`TestPopoutUnitFragment`, `TestPopoutBuildFragment`, `TestUnitAndBuildCellTriggers`): unit tests render `popouts` templates via `parseTemplates` + `execPopout` and assert hrefs with `requireAll`. Popout links well covered.
- `inventory_integration_test.go:35-62` asserts the transactions ledger links a receipt row to `/part/3007/lots/8301`.
- `part_summary_details_integration_test.go:42,53,82`: part dashboard characterization (card header text only, no card link hrefs).
- `records_reads_integration_test.go:232,261,313` (`ScopedRecords`, `RecordDetail`, `RecordPrintAndEdit`): data-level; no assertions on lot/unit/build link hrefs in `records_show`/`record_edit`.
- `po_reads_integration_test.go:277`: receipts struct equality only (no HTML).
- No tests render or assert links on: lot trace page (`PartLotTrace`), unit trace page (`PartUnitTrace`), `po_detail` receipts table, `records_show` unit/build/PO links, `app.js` row builders (only `TestUnitAndBuildCellTriggers` greps the JS source for popout triggers).

### Characterization tests to add first (pass today, pin behaviour so fixes don't regress)
- Integration (use `liveHandler`, seed IDs: lot 8301/part 3007; pick a seeded unit with lot + build and a seeded record with `unit_id`; confirm ids in `SQL/seed_test_data.sql:473,511,556`):
  - `PartLotTrace` body contains `/part/{pid}/lots/{lot}/edit` and part link `/part/{pid}`.
  - `PartUnitTrace` body contains lot link `/part/{pid}/lots/{lot}` and records table `data-rows-url`.
  - `RecordShow` body for a lot-linked record contains `/part/{pid}/lots/{lot}` (`records_show.html:147`).
  - `PODetail` receipts section renders part link `/part/{pid}/transactions`.
- Unit (template-only, no DB, `execPopout`-style helper against `m["parts/part_lot_trace.html"]`-like name per `parseTemplates`): skip if page templates need too much layout data; prefer integration.

### Red tests per gap (each must FAIL today, assert rendered href)
| Gap | Test (new, integration unless noted) | Assertion (fails today) |
|---|---|---|
| G1 | `TestIntegration_RecordShow_LinksUnit` + `..._RecordEdit_LinksUnit` | body of `/records/{id}` for a seeded record with `unit_id` contains `href="/part/{pid}/units/{unitID}"` |
| G2/G10 | per Q1 Option; A: `TestIntegration_BuildTrace_Page`, record show / unit trace / lot trace contain `href="/part/{pid}/builds/{id}"`; B: contain the output-lot href | href present |
| G3 | `TestIntegration_RecordShow_LinksPO` | record whose lot has `po_line_id` contains `href="/po/{number}"` |
| G4 | unit: `TestScopedRecordRow_UnitLot` (struct JSON has `unitId`/`lotId`); JS: source-grep test like `TestUnitAndBuildCellTriggers` for `/part/${r.pnId}/units/${r.unitId}` | JSON keys present; JS contains unit link |
| G5 | `TestIntegration_POReads_ReceiptsAndHistory` extended (struct has `LotID`/`LotNumber`) + `TestIntegration_PODetail_ReceiptLotLink` | `href="/part/{pid}/lots/{lot}"` in Receipts table |
| G7 | `TestIntegration_PartTransactions_BuildLink` | row for a build issue/receipt contains build href (Q1) |
| G8 | `TestIntegration_PartLotTrace_ListsUnits` | body for a lot with units contains `href="/part/{pid}/units/{unitID}"` for each seeded unit in that lot; empty lot shows none |
| G9 | `TestIntegration_PartLotTrace_LinksPO` + `TestIntegration_InventoryGetLot_PONumber` | purchased seeded lot contains `href="/po/{number}"`; manual/build lot shows no PO link |
| G11 | `TestIntegration_PartDetail_RecordsCard` | dashboard contains `href="/part/{id}/records"` card and record href `/records/{id}` |
| G12 | `TestIntegration_PartLotTrace_LinksTransactions` | contains `href="/part/{pid}/transactions"` |

Also add negative assertions: no duplicated Traceability links from #417/#414 (assert Summary does not gain a second `/lots?f.` / `/builds` link block from this work).

### Manual-only
- Hover popouts still open on newly linked unit/lot/build cells (popout JS attaches via `data-popout-url`; can't be asserted headless).
- Link-click navigation from `po_detail` receipts -> lot -> unit -> record -> back (breadcrumb/`NavBack` context via `setNavContext`).
- `app.js` list rendering with real `data-rows-url` data (G4) in a browser; keyboard/focus ordering of new links.
- Dark-theme visual check of the added "Units in this lot" table (G8).

### Pre-commit
`go build/vet/test ./...`; integration suite (SQL/handlers touched) against the local container per CLAUDE.md; `sqlc generate` + commit `internal/dbq` for G3/G4/G5/G8/G9/G11; recommend `/code-review medium`.

## Open questions
- Q1: Build has no detail page. For G2/G7/G10: (A) add a build detail page `/part/{id}/builds/{buildID}`, (B) link build labels to the output lot, or (C) leave build links out of scope?
- Q2: Should the new links open in-place or `target=_blank` on `record_edit.html` (an in-progress test session; existing "edit in full" uses `_blank`)? Default proposed: `_blank` on edit page only.
- Q3: G4 - add unit/lot links inside the Records tables' SN column (extra column) or add separate Unit/Lot columns? Extra columns change the shared filter row/`data-col` keys of `part_records`, `part_lot_trace`, `part_unit_trace`, `records_index` (see #411 plan).
- Q4: Should the lot lists (`/lots`, part Lots tab) also get PO/build columns, or only the lot page (G9/G10)?
- Q5: G6 - is a transitive path (PO -> lot -> records) enough, or add a direct PO -> records link?
- Q6: G11 - is a records card on Part Summary wanted (it is a tab already)?
- Q7: G12 - unfiltered link to the part Transactions tab, or add a lot filter to that tab?
- Q8: Seed coverage - need seed rows that exercise: record with `unit_id`, lot with `po_line_id` and a PO receipt with `lot_id`, unit with `build_id`. Confirm existing seed (`seed_test_data.sql:447,473,511,556`) is enough, or approve seed additions (3 files: seed, SCHEMA.md untouched).

## Resolved decisions (user, 2026-10-30)
- Gaps to fill: G1, G5 (covers G6), G8, G9 (covers G3), G12.
- Deferred / not doing: G2, G7, G10 (no build page; build links out of scope), G4, G11.
- Q2: new links on record_edit.html use target=_blank.
- Q4: no PO/build columns on lot lists; lot page only.
- Q5: PO -> lot -> records is enough; no direct PO -> records link.
- Q7: G12 links to the unfiltered Transactions tab.
- Q8: check existing seed first; if a needed case is missing, ask before adding seed rows.
- Post the Findings section as a comment on #415 before implementing.
