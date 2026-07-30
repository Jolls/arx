package main

import (
	"context"
	"database/sql"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"
	_ "time/tzdata" // embed the IANA tz database: time.LoadLocation must work on machines with no Go toolchain (#847)

	"github.com/getlantern/systray"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pkg/browser"

	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
)

// AppVersion is set at build time via -ldflags from the top entry in CHANGELOG.md.
var AppVersion = "dev"

var h *Handler

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	cfg := arxbase.Load(AppVersion)

	var database *sql.DB
	var dbDialect arxdb.Dialect
	if dsn := cfg.DSN(); dsn != "" {
		if conn, dialect, err := arxdb.Connect(cfg.DBEngine(), dsn); err == nil {
			database = conn
			dbDialect = dialect
			log.Println("arx: auto-connected to database")
		} else {
			log.Printf("arx: auto-connect failed (open Settings to reconnect): %v", err)
		}
	} else {
		log.Println("arx: no database password configured — open Settings to connect")
	}

	h = New(database, dbDialect, cfg, templatesFS, releaseNotesData)
	h.CheckSchemaVersion(context.Background())
	h.loadCompanyLogo(context.Background())
	h.loadPartCategories(context.Background())

	if cfg.DebugMode {
		openDebugConsole()
	}

	router := buildRouter(h)
	server := &http.Server{
		Addr:    "127.0.0.1:" + cfg.Port,
		Handler: router,
	}
	go func() {
		log.Printf("Arx: starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Arx: server stopped: %v", err)
			systray.Quit()
		}
	}()

	url := "http://localhost:" + cfg.Port
	go openWhenReady(url, cfg.Port)

	systray.SetIcon(appIcon())
	systray.SetTooltip("Arx")

	mOpen := systray.AddMenuItem("Open Arx", "Open Arx in browser")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Arx")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				_ = browser.OpenURL(url)
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onExit() {
	if h != nil {
		h.CloseDB()
	}
}

func openWhenReady(url, port string) {
	for i := 0; i < 40; i++ {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 50*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = browser.OpenURL(url)
}

func buildRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(h.profileRequest)
	r.Use(h.RequireCsrfOnPost)

	// Static assets under /static/<tab>/, plus /static/shared/ for cross-tab assets (icons, nav CSS/JS).
	subStatic, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(subStatic))))

	// Always accessible — no DB connection required.
	r.Get("/login", h.LoginGet)
	r.Post("/login", h.LoginPost)
	r.Post("/logout", h.Logout)
	// GET/POST /settings must stay reachable during first-run setup (no DB connected) but
	// require a logged-in user once a database is connected — GET renders db_server,
	// db_user, and filesystem roots, which is a config-disclosure hole to an
	// unauthenticated caller once connected (#781, read-side sibling of #748).
	r.With(h.RequireAuthOnceConnected).Get("/settings", h.Settings)
	r.With(h.RequireAuthOnceConnected).Post("/settings", h.SettingsSave)
	r.Get("/whats-new", h.WhatsNew)
	// Gated the same way as POST /settings: reachable unauthenticated only during
	// first-run setup, since each spawns a native folder/file picker dialog and an
	// unauthenticated GET on a connected instance would be a local DoS/nuisance (#757).
	r.With(h.RequireAuthOnceConnected).Get("/api/browse-folder", h.APIBrowseFolder)
	r.With(h.RequireAuthOnceConnected).Get("/api/browse-file", h.APIBrowseFile)

	// All other routes require a live database connection.
	r.Group(func(r chi.Router) {
		r.Use(h.RequireAuth)

		// Configuration tab saves (Settings → Configuration: attachment categories,
		// categories, part numbering, company logo). Behind auth because these mutate
		// shop-wide app_config that affects data integrity (#771).
		r.Post("/settings/attachment-categories", h.SettingsAttachmentCategoriesSave)
		r.Post("/settings/categories", h.SettingsCategoriesSave)
		r.Post("/settings/part-numbering", h.SettingsPartNumberingSave)
		r.Post("/settings/company-logo", h.SettingsCompanyLogoSave)
		r.Post("/settings/company-logo/remove", h.SettingsCompanyLogoRemove)

		// Named Queries editor (Settings → Named Queries tab). Behind auth because
		// these routes execute/persist SQL and require a live DB connection.
		// Saving is per-row (one query at a time), not a bulk table submit.
		r.Post("/settings/named-queries/save", h.SettingsNamedQueryRowSave)
		r.Post("/settings/named-queries/test", h.SettingsNamedQueryTest)

		// User management (Settings → Users tab)
		r.Post("/settings/users", h.SettingsUsersCreate)
		r.Post("/settings/users/{userID}/password", h.SettingsUsersResetPassword)
		r.Post("/settings/users/{userID}/toggle-active", h.SettingsUsersToggleActive)
		r.Post("/settings/users/{userID}/toggle-approve", h.SettingsUsersToggleApprove)
		r.Post("/settings/users/{userID}/toggle-approve-records", h.SettingsUsersToggleApproveRecords)
		r.Post("/settings/users/{userID}/toggle-admin", h.SettingsUsersToggleAdmin)

		// Per-user preferences (Settings → My Preferences tab; PO defaults — issue #463)
		r.Post("/settings/preferences", h.SettingsPreferencesSave)
		r.Post("/settings/accent-color", h.SettingsAccentColorSave)
		r.Post("/settings/default-route", h.SettingsDefaultRouteSave)
		r.Post("/settings/timezone", h.SettingsTimezoneSave)

		// Data backup
		r.Get("/settings/backup", h.SettingsBackup)

		// Data diagnostics (Settings → Utilities)
		r.Get("/settings/utilities", h.UtilitiesReport)

		// Local file serving (Parts Master)
		r.Get("/local/*", h.ServeLocalFile)
		r.Get("/local-dir/*", h.ServeLocalDir)
		r.Get("/supplier-local/*", h.ServeSupplierFile)
		r.Get("/supplier-local-dir/*", h.ServeSupplierDir)

		// Test Records image serving
		r.Get("/images/*", h.ServeImage)

		// Reports (issue #282)
		r.Get("/reports", h.ReportsDashboard)
		r.Get("/reports/spend", h.ReportsSpend)
		r.Get("/reports/yield", h.ReportsYieldPicker)
		r.Get("/reports/failure-modes", h.ReportsFailureModesPicker)
		r.Get("/reports/spend/export-suppliers.csv", h.ReportsSpendBySupplierExportCSV)
		r.Get("/reports/spend/export-parts.csv", h.ReportsSpendByPartExportCSV)

		// Supplier performance and data quality reports (issue #659, RPT-8)
		r.Get("/reports/on-time", h.ReportsOnTime)
		r.Get("/reports/on-time/export.csv", h.ReportsOnTimeExportCSV)
		r.Get("/reports/cycle-time", h.ReportsCycleTime)
		r.Get("/reports/cycle-time/export.csv", h.ReportsCycleTimeExportCSV)
		r.Get("/reports/data-quality", h.ReportsDataQuality)
		r.Get("/reports/data-quality/export-no-attachments.csv", h.ReportsDataQualityNoAttachmentsExportCSV)
		r.Get("/reports/data-quality/export-missing-supplier.csv", h.ReportsDataQualityMissingSupplierExportCSV)
		r.Get("/reports/data-quality/export-stale-rollup.csv", h.ReportsDataQualityStaleRollupExportCSV)

		// App root: redirect to each user's configured landing page (issue #282).
		r.Get("/", h.RootRedirect)

		// Parts Master — Parts
		r.Get("/parts", h.PartsList)
		r.Get("/parts/new", h.PartsNew)
		r.Get("/parts/export.csv", h.PartsExportCSV)
		r.Get("/lots", h.AllLots)
		r.Post("/parts", h.PartsCreate)
		r.Get("/part/{id}", h.PartDetail)
		r.Get("/part/{id}/details", h.PartDetail)
		r.Get("/part/{id}/edit", h.PartEdit)
		r.Get("/part/{id}/duplicate", h.PartDuplicate)
		r.Post("/part/{id}", h.PartUpdate)
		r.Get("/part/{id}/bom", h.PartBOM)
		r.Get("/part/{id}/bom/edit", h.PartBOMEdit)
		r.Get("/part/{id}/bom/export.csv", h.BOMExportCSV)
		r.Post("/part/{id}/bom", h.PartBOMSave)
		r.Post("/part/{id}/rollup-cost", h.PartRollupCost)
		r.Get("/part/{id}/build-cost", h.PartBuildCost)
		r.Get("/part/{id}/where-used", h.PartWhereUsed)
		r.Get("/part/{id}/attachments", h.PartAttachments)
		r.Post("/part/{id}/attachments", h.PartAttachmentCreate)
		r.Post("/part/{id}/attachments/{attID}", h.PartAttachmentUpdate)
		r.Post("/part/{id}/primary_attachment", h.PartSetPrimaryAttachment)
		r.Post("/part/{id}/attachments/{attID}/delete", h.PartAttachmentDelete)
		r.Get("/attachments/where-used", h.AttachmentWhereUsed)
		r.Get("/api/part/{id}/attachment-name", h.APIPartAttachmentName)
		r.Post("/api/part/{id}/paste-attachment", h.APIPartPasteAttachment)
		r.Post("/api/part/{id}/attachments/{attID}/paste-attachment", h.APIPartPasteAttachmentReplace)
		r.Post("/api/part/{id}/attachments/{attID}/generate-thumbnail", h.APIPartGenerateThumbnail)
		r.Get("/part/{id}/orders", h.PartOrders)
		r.Get("/part/{id}/price-history", h.PartPriceHistory)
		r.Get("/part/{id}/transactions", h.PartTransactions)
		r.Post("/part/{id}/adjust-stock", h.PartStockAdjust)
		r.Get("/part/{id}/build", h.PartBuild)
		r.Post("/part/{id}/build", h.PartBuildCreate)
		r.Get("/part/{id}/lots", h.PartLots)
		r.Get("/part/{id}/lots/{lotID}", h.PartLotTrace)
		r.Get("/part/{id}/lots/{lotID}/edit", h.LotEdit)
		r.Post("/part/{id}/lots/{lotID}", h.LotUpdate)
		r.Get("/part/{id}/units", h.PartUnits)
		r.Get("/part/{id}/units/new", h.UnitNew) // before {unitID}
		r.Post("/part/{id}/units", h.UnitCreate)
		r.Get("/part/{id}/units/{unitID}", h.PartUnitTrace)
		r.Get("/part/{id}/units/{unitID}/edit", h.UnitEdit)
		r.Post("/part/{id}/units/{unitID}", h.UnitUpdate)
		r.Get("/part/{id}/pricing", h.PartPricing)
		r.Get("/part/{id}/pricing/new", h.PriceNew)
		r.Post("/part/{id}/pricing", h.PriceCreate)
		r.Post("/part/{id}/pricing/preferred", h.PricePreferred)
		r.Get("/part/{id}/pricing/{priceID}/edit", h.PriceEdit)
		r.Post("/part/{id}/pricing/{priceID}", h.PriceUpdate)
		r.Post("/part/{id}/pricing/{priceID}/deactivate", h.PriceDeactivate)
		r.Post("/part/{id}/pricing/{priceID}/activate", h.PriceActivate)
		r.Get("/part/{id}/mfg-parts", h.PartMfgParts)
		r.Post("/part/{id}/mfg-parts", h.MfgPartCreate)
		r.Get("/part/{id}/mfg-parts/{mid}/edit", h.MfgPartEdit)
		r.Post("/part/{id}/mfg-parts/{mid}", h.MfgPartUpdate)
		r.Post("/part/{id}/mfg-parts/{mid}/delete", h.MfgPartDelete)
		r.Get("/part/{id}/suppliers", h.PartSourcing)
		r.Post("/part/{id}/suppliers", h.SupplierPartCreate)
		r.Get("/part/{id}/suppliers/{spID}/edit", h.SupplierPartEdit)
		r.Post("/part/{id}/suppliers/{spID}", h.SupplierPartUpdate)
		r.Post("/part/{id}/suppliers/{spID}/delete", h.SupplierPartDelete)

		// Parts Master — Suppliers / Vendors
		r.Get("/suppliers", h.SuppliersList)
		r.Get("/suppliers/new", h.SuppliersNew)
		r.Post("/suppliers", h.SuppliersCreate)
		r.Get("/supplier/{id}", h.SupplierDetail)
		r.Get("/supplier/{id}/edit", h.SupplierEdit)
		r.Post("/supplier/{id}", h.SupplierUpdate)
		r.Get("/supplier/{id}/parts", h.SupplierParts)
		r.Get("/supplier/{id}/pos", h.SupplierPOs)
		r.Get("/supplier/{id}/attachments", h.SupplierAttachments)
		r.Post("/supplier/{id}/attachments", h.SupplierAttachmentCreate)
		r.Post("/supplier/{id}/attachments/{attID}", h.SupplierAttachmentUpdate)
		r.Post("/supplier/{id}/attachments/{attID}/delete", h.SupplierAttachmentDelete)
		r.Post("/supplier/{id}/primary_attachment", h.SupplierSetPrimaryAttachment)
		r.Get("/supplier/{id}/folder", h.SupplierFolder)
		r.Get("/supplier/{id}/folder/*", h.SupplierFolderSub)
		r.Get("/supplier/{id}/file/*", h.SupplierFile)

		// Parts Master — Contacts
		r.Get("/contacts", h.ContactsList)
		r.Get("/contacts/new", h.ContactsNew)
		r.Post("/contacts", h.ContactsCreate)
		r.Get("/contact/{id}", h.ContactDetail)
		r.Get("/contact/{id}/edit", h.ContactEdit)
		r.Post("/contact/{id}", h.ContactUpdate)

		// Parts Master — Purchase Orders
		r.Get("/pos", h.POList)
		r.Get("/pos/new", h.PONew)
		r.Get("/pos/export.csv", h.POsExportCSV)
		r.Post("/pos", h.POCreate)
		// RFQ (issue #270) — an RFQ is a purchase_order with status 'rfq'
		r.Get("/rfqs/new", h.RFQNew)
		r.Get("/rfq/{id}/add-supplier", h.RFQAddSupplier)
		r.Get("/rfq/{group}/compare", h.RFQCompare)
		r.Post("/rfq/{group}/compare", h.RFQCompareSave)
		r.Post("/rfq/{id}/convert", h.RFQConvert)
		r.Get("/po/{id}", h.PODetail)
		r.Get("/po/{id}/edit", h.POEdit)
		r.Post("/po/{id}", h.POUpdate)
		r.Post("/po/{id}/status", h.POStatusTransition)
		r.Post("/po/{id}/receive", h.POReceive)
		r.Post("/po/{id}/approval", h.POApprovalAction)
		r.Get("/po/{id}/note", h.PONote)
		r.Get("/po/{id}/print", h.POPrint)
		r.Post("/po/{id}/mark-printed", h.POMarkPrinted)
		r.Post("/po/{id}/add-suggestions", h.POAddSuggestions)
		r.Post("/po/{id}/open-folder", h.POOpenFolder)
		r.Post("/po/{id}/import-part-file", h.POImportPartFile)
		r.Get("/po/{id}/duplicate", h.PODuplicate)
		r.Get("/po/{id}/folder", h.POFolder)
		r.Get("/po/{id}/folder/*", h.POFolderSub)
		r.Get("/po/{id}/file/*", h.POFile)

		// Parts Master — API
		r.Get("/api/suppliers/search", h.APISupplierSearch)
		r.Get("/api/suppliers/{id}/contacts", h.APISupplierContacts)
		r.Get("/api/parts/search", h.APIPartSearch)
		r.Get("/api/parts/next-number", h.PartsNextNumber)
		r.Get("/api/supplier-part", h.APISupplierPN)
		r.Get("/api/part/{id}/local-attachments", h.APIPartLocalAttachments)
		r.Get("/api/part/{id}/bom-children", h.APIPartBOMChildren)
		r.Get("/api/parts/rows", h.PartsRows)
		r.Get("/api/suppliers/rows", h.SuppliersRows)
		r.Get("/api/contacts/rows", h.ContactsRows)
		r.Get("/api/pos/rows", h.PORows)

		// Test Records — Forms and Records
		r.Get("/records", h.FormsList)
		r.Get("/forms/{id}/records", h.RecordsList)
		r.Get("/forms/{id}/yield", h.RecordsYieldSummary)
		r.Get("/forms/{id}/failure-modes", h.RecordsFailureModes)
		r.Get("/api/forms/{id}/records/rows", h.RecordsRows)
		r.Post("/forms/{id}/records/bulk-lock", h.BulkLockRecords)
		r.Get("/forms/{id}/records/new", h.NewRecord)
		r.Post("/forms/{id}/records/new", h.CreateRecord)
		r.Get("/forms/{id}/def", h.FormDef)
		r.Get("/forms/{id}/def/edit", h.EditFormDef)
		r.Post("/forms/{id}/def/edit", h.SaveFormDef)
		r.Get("/api/forms/{id}/def/history", h.FormDefHistory)
		r.Get("/forms/{id}/tests/{testID}/report", h.TestReport)
		r.Post("/forms/{id}/tests/{testID}/archive", h.ArchiveStep)
		r.Get("/records/{id}", h.RecordDetail)
		r.Get("/records/{id}/print", h.RecordPrint)
		r.Get("/records/{id}/edit", h.EditRecord)
		r.Post("/records/{id}/edit", h.SaveResults)
		r.Post("/api/record/{id}/step/{tid}/paste-image", h.APIRecordPasteResultImage)
		r.Post("/records/{id}/resync", h.ResyncRecord)
		r.Post("/records/{id}/lock", h.LockRecord)
		r.Post("/records/{id}/approve", h.ApproveRecord)
		r.Post("/records/{id}/unlock", h.UnlockRecord)
		r.Post("/records/{id}/duplicate", h.DuplicateRecord)
		r.Get("/forms/new", h.NewForm)
		r.Post("/forms/new", h.CreateForm)
		r.Get("/forms/{id}/duplicate", h.DuplicateForm)
		r.Post("/forms/{id}/duplicate", h.CreateDuplicate)
		r.Post("/forms/{id}/lock", h.LockForm)
		r.Post("/forms/{id}/unlock", h.UnlockForm)
		r.Get("/api/named-query", h.APINamedQuery)
	})

	r.NotFound(h.NotFound)

	return r
}
