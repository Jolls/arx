package main

import (
	"context"
	"database/sql"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA tz database: time.LoadLocation must work on machines with no Go toolchain (#847)

	"fyne.io/systray"

	arxbase "arx/internal/config"
	arxdb "arx/internal/db"
)

// AppVersion is set at build time via -ldflags from the top entry in CHANGELOG.md.
var AppVersion = "dev"

var h *Handler

func main() {
	if headlessEnabled() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runHeadless(ctx, startServer, func() { h.CloseDB() })
		stop()
		os.Exit(code)
	}
	// Ctrl+C / SIGTERM quit via the tray so onExit closes the DB instead of the process dying mid-flight.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(onReady, onExit)
}

// startServer does the startup shared by the tray and headless paths and returns the
// server and its port. onStopped runs if the listener stops.
func startServer(onStopped func()) (*http.Server, string) {
	cfg := arxbase.Load(AppVersion)

	var database *sql.DB
	if dsn := cfg.DSN(); dsn != "" {
		if conn, err := arxdb.Connect(dsn); err == nil {
			database = conn
			log.Println("arx: auto-connected to database")
		} else {
			log.Printf("arx: auto-connect failed (open Settings to reconnect): %v", err)
		}
	} else {
		log.Println("arx: no database password configured — open Settings to connect")
	}

	h = New(database, cfg, templatesFS, releaseNotesData)
	if err := h.loadTemplates(); err != nil {
		log.Fatalf("arx: template parse error: %v", err)
	}
	h.CheckSchemaVersion(context.Background())
	h.loadCompanyLogo(context.Background())
	h.loadPartCategories(context.Background())
	h.loadDigiKeyCredentials(context.Background())

	if cfg.DebugMode && !headlessEnabled() {
		openDebugConsole()
	}

	router, _ := buildRouter(h)
	server := &http.Server{
		Addr:    "127.0.0.1:" + cfg.Port,
		Handler: router,
	}
	go func() {
		log.Printf("Arx: starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Arx: server stopped: %v", err)
			onStopped()
		}
	}()
	return server, cfg.Port
}

func onReady() {
	_, port := startServer(systray.Quit)
	url := "http://localhost:" + port
	go openWhenReady(url, port)

	systray.SetIcon(appIcon())
	systray.SetTooltip("Arx")

	mOpen := systray.AddMenuItem("Open Arx", "Open Arx in browser")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Arx")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				_ = openURL(url)
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
	for range 40 {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 50*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = openURL(url)
}

// openURL opens url in the system's default browser. Arx only ever opens its own
// localhost URL, so no input-sanitization is needed here.
func openURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func buildRouter(h *Handler) (http.Handler, []registeredRoute) {
	b := newRouteBuilder()

	withAuth := chain(h.RequireAuth)
	withAdmin := chain(h.RequireAuth, h.RequireAdmin)
	withAdminJSON := chain(h.RequireAuth, h.RequireAdminJSON)
	withAuthOnceConnected := chain(h.RequireAuthOnceConnected)
	withAdminOnceConnected := chain(h.RequireAdminOnceConnected)

	// Static assets under /static/<tab>/, plus /static/shared/ for cross-tab assets (icons, nav CSS/JS).
	subStatic, _ := fs.Sub(staticFS, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(subStatic)))
	b.handleWildcard(http.MethodGet, "/static/", nil, staticHandler.ServeHTTP, h.NotFound)

	// Always accessible — no DB connection required.
	b.handle(http.MethodGet, "/login", nil, h.LoginGet)
	b.handle(http.MethodPost, "/login", nil, h.LoginPost)
	b.handle(http.MethodPost, "/logout", nil, h.Logout)
	// GET/POST /settings must stay reachable during first-run setup (no DB connected) but
	// require a logged-in user once a database is connected — GET renders db_server,
	// db_user, and filesystem roots, which is a config-disclosure hole to an
	// unauthenticated caller once connected (#781, read-side sibling of #748).
	// GET stays open to any logged-in user — the page also hosts My Preferences.
	// POST is admin-only once connected: it rewrites the DB connection (#106).
	b.handle(http.MethodGet, "/settings", withAuthOnceConnected, h.Settings)
	b.handle(http.MethodPost, "/settings", withAdminOnceConnected, h.SettingsSave)
	b.handle(http.MethodGet, "/whats-new", nil, h.WhatsNew)
	// Gated the same way as POST /settings: reachable unauthenticated only during
	// first-run setup, since it spawns a native folder-picker dialog and an
	// unauthenticated GET on a connected instance would be a local DoS/nuisance (#757).
	// That means admin-only once connected too (#106) — its only callers are the
	// File Paths pickers on the Connection tab, which is now admin-only, so leaving
	// it on RequireAuthOnceConnected would let a non-admin with no Connection UI
	// still pop a native dialog on the host.
	b.handle(http.MethodGet, "/api/browse-folder", withAdminOnceConnected, h.APIBrowseFolder)

	// Admin-only routes, plain-text 403 for non-admins. RequireAuth runs first so the
	// session user is on the context. Every admin-only endpoint is declared here or
	// behind RequireAdminOnceConnected above — not gated inside the handler (#120).

	// Shop-wide API credentials (#106)
	b.handle(http.MethodPost, "/settings/digikey", withAdmin, h.SettingsDigiKeySave)

	// User management (Settings → Users tab): a non-admin must not be able to
	// self-grant rights, reset passwords, or deactivate others (#750).
	b.handle(http.MethodPost, "/settings/users", withAdmin, h.SettingsUsersCreate)
	b.handle(http.MethodPost, "/settings/users/{userID}/password", withAdmin, h.SettingsUsersResetPassword)
	b.handle(http.MethodPost, "/settings/users/{userID}/toggle-active", withAdmin, h.SettingsUsersToggleActive)
	b.handle(http.MethodPost, "/settings/users/{userID}/toggle-approve", withAdmin, h.SettingsUsersToggleApprove)
	b.handle(http.MethodPost, "/settings/users/{userID}/toggle-approve-records", withAdmin, h.SettingsUsersToggleApproveRecords)
	b.handle(http.MethodPost, "/settings/users/{userID}/toggle-admin", withAdmin, h.SettingsUsersToggleAdmin)

	// Data backup: a full export of every table (#106)
	b.handle(http.MethodGet, "/settings/backup", withAdmin, h.SettingsBackup)

	// Data diagnostics (Settings → Utilities): row counts and data-quality stats
	// across every table (#106)
	b.handle(http.MethodGet, "/settings/utilities", withAdmin, h.UtilitiesReport)

	// Admin-only routes, JSON 403 for non-admins: the Settings page parses every
	// response from these with r.json().

	// Named Queries editor (Settings → Named Queries tab). Admin-only because save
	// persists SQL that execQuery later runs and test runs caller-supplied SQL;
	// isSafeQuery blocks writes but not reads, so any user could otherwise SELECT
	// from any table (#103). Saving is per-row, not a bulk table submit.
	b.handle(http.MethodPost, "/settings/named-queries/save", withAdminJSON, h.SettingsNamedQueryRowSave)
	b.handle(http.MethodPost, "/settings/named-queries/test", withAdminJSON, h.SettingsNamedQueryTest)

	// All other routes require a live database connection.

	// Configuration tab saves (Settings → Configuration: attachment categories,
	// categories, part numbering, company logo). Behind auth because these mutate
	// shop-wide app_config that affects data integrity (#771).
	b.handle(http.MethodPost, "/settings/attachment-categories", withAuth, h.SettingsAttachmentCategoriesSave)
	b.handle(http.MethodPost, "/settings/categories", withAuth, h.SettingsCategoriesSave)
	b.handle(http.MethodPost, "/settings/part-numbering", withAuth, h.SettingsPartNumberingSave)
	b.handle(http.MethodPost, "/settings/company-logo", withAuth, h.SettingsCompanyLogoSave)
	b.handle(http.MethodPost, "/settings/company-logo/remove", withAuth, h.SettingsCompanyLogoRemove)

	// Per-user preferences (Settings → My Preferences tab; PO defaults — issue #463)
	b.handle(http.MethodPost, "/settings/preferences", withAuth, h.SettingsPreferencesSave)
	b.handle(http.MethodPost, "/settings/accent-color", withAuth, h.SettingsAccentColorSave)
	b.handle(http.MethodPost, "/settings/default-route", withAuth, h.SettingsDefaultRouteSave)
	b.handle(http.MethodPost, "/settings/timezone", withAuth, h.SettingsTimezoneSave)

	// Local file serving (Parts Master)
	b.handleWildcard(http.MethodGet, "/local/", withAuth, h.ServeLocalFile, h.NotFound)
	b.handleWildcard(http.MethodGet, "/local-dir/", withAuth, h.ServeLocalDir, h.NotFound)
	b.handleWildcard(http.MethodPost, "/local-dir-upload/", withAuth, h.ServeLocalDirUpload, h.NotFound)
	b.handleWildcard(http.MethodGet, "/supplier-local/", withAuth, h.ServeSupplierFile, h.NotFound)
	b.handleWildcard(http.MethodGet, "/supplier-local-dir/", withAuth, h.ServeSupplierDir, h.NotFound)
	b.handleWildcard(http.MethodPost, "/supplier-local-dir-upload/", withAuth, h.ServeSupplierDirUpload, h.NotFound)

	// Test Records image serving
	b.handleWildcard(http.MethodGet, "/images/", withAuth, h.ServeImage, h.NotFound)

	// Reports (issue #282)
	b.handle(http.MethodGet, "/reports", withAuth, h.ReportsDashboard)
	b.handle(http.MethodGet, "/reports/spend", withAuth, h.ReportsSpend)
	b.handle(http.MethodGet, "/reports/yield", withAuth, h.ReportsYieldPicker)
	b.handle(http.MethodGet, "/reports/failure-modes", withAuth, h.ReportsFailureModesPicker)
	b.handle(http.MethodGet, "/reports/spend/export-suppliers.csv", withAuth, h.ReportsSpendBySupplierExportCSV)
	b.handle(http.MethodGet, "/reports/spend/export-parts.csv", withAuth, h.ReportsSpendByPartExportCSV)

	// Supplier performance and data quality reports (issue #659, RPT-8)
	b.handle(http.MethodGet, "/reports/on-time", withAuth, h.ReportsOnTime)
	b.handle(http.MethodGet, "/reports/on-time/export.csv", withAuth, h.ReportsOnTimeExportCSV)
	b.handle(http.MethodGet, "/reports/cycle-time", withAuth, h.ReportsCycleTime)
	b.handle(http.MethodGet, "/reports/cycle-time/export.csv", withAuth, h.ReportsCycleTimeExportCSV)
	b.handle(http.MethodGet, "/reports/data-quality", withAuth, h.ReportsDataQuality)
	b.handle(http.MethodGet, "/reports/data-quality/export-no-attachments.csv", withAuth, h.ReportsDataQualityNoAttachmentsExportCSV)
	b.handle(http.MethodGet, "/reports/data-quality/export-missing-supplier.csv", withAuth, h.ReportsDataQualityMissingSupplierExportCSV)
	b.handle(http.MethodGet, "/reports/data-quality/export-stale-rollup.csv", withAuth, h.ReportsDataQualityStaleRollupExportCSV)

	// App root: redirect to each user's configured landing page (issue #282).
	// "/{$}" matches only the exact root path — unlike a bare "/" pattern, it
	// is not a subtree wildcard, so it doesn't swallow unmatched paths that
	// should 404 instead (#319).
	b.handle(http.MethodGet, "/{$}", withAuth, h.RootRedirect)

	// Parts Master — Parts
	b.handle(http.MethodGet, "/parts", withAuth, h.PartsList)
	b.handle(http.MethodGet, "/parts/new", withAuth, h.PartsNew)
	b.handle(http.MethodGet, "/parts/export.csv", withAuth, h.PartsExportCSV)
	b.handle(http.MethodGet, "/lots", withAuth, h.AllLots)
	b.handle(http.MethodGet, "/units", withAuth, h.AllUnits)
	b.handle(http.MethodGet, "/builds", withAuth, h.AllBuilds)
	b.handle(http.MethodPost, "/parts", withAuth, h.PartsCreate)
	b.handle(http.MethodGet, "/part/{id}", withAuth, h.PartDetail)
	b.handle(http.MethodGet, "/part/{id}/details", withAuth, h.PartDetail)
	b.handle(http.MethodGet, "/part/{id}/edit", withAuth, h.PartEdit)
	b.handle(http.MethodGet, "/part/{id}/duplicate", withAuth, h.PartDuplicate)
	b.handle(http.MethodPost, "/part/{id}", withAuth, h.PartUpdate)
	b.handle(http.MethodGet, "/part/{id}/bom", withAuth, h.PartBOM)
	b.handle(http.MethodGet, "/part/{id}/bom/edit", withAuth, h.PartBOMEdit)
	b.handle(http.MethodGet, "/part/{id}/bom/export.csv", withAuth, h.BOMExportCSV)
	b.handle(http.MethodPost, "/part/{id}/bom", withAuth, h.PartBOMSave)
	b.handle(http.MethodPost, "/part/{id}/bom/preview", withAuth, h.PartBOMPastePreview)
	b.handle(http.MethodPost, "/part/{id}/rollup-cost", withAuth, h.PartRollupCost)
	b.handle(http.MethodGet, "/part/{id}/create-rfqs", withAuth, h.PartCreateRFQs)
	b.handle(http.MethodPost, "/part/{id}/create-rfqs", withAuth, h.PartCreateRFQsConfirm)
	b.handle(http.MethodGet, "/part/{id}/build-cost", withAuth, h.PartBuildCost)
	b.handle(http.MethodGet, "/part/{id}/where-used", withAuth, h.PartWhereUsed)
	b.handle(http.MethodGet, "/part/{id}/attachments", withAuth, h.PartAttachments)
	b.handle(http.MethodPost, "/part/{id}/attachments", withAuth, h.PartAttachmentCreate)
	b.handle(http.MethodPost, "/part/{id}/attachments/{attID}", withAuth, h.PartAttachmentUpdate)
	b.handle(http.MethodPost, "/part/{id}/primary_attachment", withAuth, h.PartSetPrimaryAttachment)
	b.handle(http.MethodPost, "/part/{id}/attachments/{attID}/delete", withAuth, h.PartAttachmentDelete)
	b.handle(http.MethodGet, "/attachments/where-used", withAuth, h.AttachmentWhereUsed)
	b.handle(http.MethodGet, "/api/part/{id}/attachment-name", withAuth, h.APIPartAttachmentName)
	b.handle(http.MethodPost, "/api/part/{id}/paste-attachment", withAuth, h.APIPartPasteAttachment)
	b.handle(http.MethodPost, "/api/part/{id}/attachments/{attID}/paste-attachment", withAuth, h.APIPartPasteAttachmentReplace)
	b.handle(http.MethodPost, "/api/part/{id}/attachments/{attID}/generate-thumbnail", withAuth, h.APIPartGenerateThumbnail)
	b.handle(http.MethodGet, "/part/{id}/orders", withAuth, h.PartOrders)
	b.handle(http.MethodGet, "/part/{id}/records", withAuth, h.PartRecords)
	b.handle(http.MethodGet, "/api/part/{id}/records/rows", withAuth, h.PartRecordsRows)
	b.handle(http.MethodGet, "/part/{id}/price-history", withAuth, h.PartPriceHistory)
	b.handle(http.MethodGet, "/part/{id}/transactions", withAuth, h.PartTransactions)
	b.handle(http.MethodPost, "/part/{id}/adjust-stock", withAuth, h.PartStockAdjust)
	b.handle(http.MethodGet, "/part/{id}/build", withAuth, h.PartBuild)
	b.handle(http.MethodPost, "/part/{id}/build", withAuth, h.PartBuildCreate)
	b.handle(http.MethodGet, "/part/{id}/lots", withAuth, h.PartLots)
	b.handle(http.MethodGet, "/part/{id}/lots/{lotID}", withAuth, h.PartLotTrace)
	b.handle(http.MethodGet, "/part/{id}/lots/{lotID}/edit", withAuth, h.LotEdit)
	b.handle(http.MethodPost, "/part/{id}/lots/{lotID}", withAuth, h.LotUpdate)
	b.handle(http.MethodGet, "/api/part/{id}/lots/{lotID}/records/rows", withAuth, h.LotRecordsRows)
	b.handle(http.MethodGet, "/api/part/{id}/lots/{lotID}/sources", withAuth, h.APILotSources)
	b.handle(http.MethodGet, "/popout/part/{id}/where-used", withAuth, h.PopoutPartWhereUsed)
	b.handle(http.MethodGet, "/popout/part/{id}/lots/{lotID}/consumers", withAuth, h.PopoutLotConsumers)
	b.handle(http.MethodGet, "/part/{id}/units", withAuth, h.PartUnits)
	b.handle(http.MethodGet, "/part/{id}/units/new", withAuth, h.UnitNew) // before {unitID}
	b.handle(http.MethodPost, "/part/{id}/units", withAuth, h.UnitCreate)
	b.handle(http.MethodGet, "/part/{id}/units/{unitID}", withAuth, h.PartUnitTrace)
	b.handle(http.MethodGet, "/part/{id}/units/{unitID}/edit", withAuth, h.UnitEdit)
	b.handle(http.MethodPost, "/part/{id}/units/{unitID}", withAuth, h.UnitUpdate)
	b.handle(http.MethodGet, "/api/part/{id}/units/{unitID}/records/rows", withAuth, h.UnitRecordsRows)
	b.handle(http.MethodGet, "/part/{id}/pricing", withAuth, h.PartPricing)
	b.handle(http.MethodGet, "/part/{id}/pricing/new", withAuth, h.PriceNew)
	b.handle(http.MethodPost, "/part/{id}/pricing", withAuth, h.PriceCreate)
	b.handle(http.MethodPost, "/part/{id}/pricing/preferred", withAuth, h.PricePreferred)
	b.handle(http.MethodGet, "/part/{id}/pricing/{priceID}/edit", withAuth, h.PriceEdit)
	b.handle(http.MethodPost, "/part/{id}/pricing/{priceID}", withAuth, h.PriceUpdate)
	b.handle(http.MethodPost, "/part/{id}/pricing/{priceID}/deactivate", withAuth, h.PriceDeactivate)
	b.handle(http.MethodPost, "/part/{id}/pricing/{priceID}/activate", withAuth, h.PriceActivate)
	b.handle(http.MethodPost, "/part/{id}/pricing/{priceID}/delete", withAuth, h.PriceDelete)
	b.handle(http.MethodGet, "/part/{id}/mfg-parts", withAuth, h.PartMfgParts)
	b.handle(http.MethodPost, "/part/{id}/mfg-parts", withAuth, h.MfgPartCreate)
	b.handle(http.MethodGet, "/part/{id}/mfg-parts/{mid}/edit", withAuth, h.MfgPartEdit)
	b.handle(http.MethodPost, "/part/{id}/mfg-parts/{mid}", withAuth, h.MfgPartUpdate)
	b.handle(http.MethodPost, "/part/{id}/mfg-parts/{mid}/delete", withAuth, h.MfgPartDelete)
	b.handle(http.MethodGet, "/part/{id}/suppliers", withAuth, h.PartSourcing)
	b.handle(http.MethodPost, "/part/{id}/suppliers", withAuth, h.SupplierPartCreate)
	b.handle(http.MethodGet, "/part/{id}/suppliers/{spID}/edit", withAuth, h.SupplierPartEdit)
	b.handle(http.MethodPost, "/part/{id}/suppliers/{spID}", withAuth, h.SupplierPartUpdate)
	b.handle(http.MethodPost, "/part/{id}/suppliers/{spID}/delete", withAuth, h.SupplierPartDelete)

	// Parts Master — Suppliers / Vendors
	b.handle(http.MethodGet, "/suppliers", withAuth, h.SuppliersList)
	b.handle(http.MethodGet, "/suppliers/new", withAuth, h.SuppliersNew)
	b.handle(http.MethodPost, "/suppliers", withAuth, h.SuppliersCreate)
	b.handle(http.MethodGet, "/supplier/{id}", withAuth, h.SupplierDetail)
	b.handle(http.MethodGet, "/supplier/{id}/edit", withAuth, h.SupplierEdit)
	b.handle(http.MethodPost, "/supplier/{id}", withAuth, h.SupplierUpdate)
	b.handle(http.MethodGet, "/supplier/{id}/parts", withAuth, h.SupplierParts)
	b.handle(http.MethodGet, "/supplier/{id}/pos", withAuth, h.SupplierPOs)
	b.handle(http.MethodGet, "/supplier/{id}/attachments", withAuth, h.SupplierAttachments)
	b.handle(http.MethodPost, "/supplier/{id}/attachments", withAuth, h.SupplierAttachmentCreate)
	b.handle(http.MethodPost, "/supplier/{id}/attachments/{attID}", withAuth, h.SupplierAttachmentUpdate)
	b.handle(http.MethodPost, "/supplier/{id}/attachments/{attID}/delete", withAuth, h.SupplierAttachmentDelete)
	b.handle(http.MethodPost, "/supplier/{id}/primary_attachment", withAuth, h.SupplierSetPrimaryAttachment)
	b.handleWildcard(http.MethodGet, "/supplier/{id}/folder/", withAuth, h.SupplierFolderSub, h.SupplierFolder)
	b.handleWildcard(http.MethodPost, "/supplier/{id}/folder-upload/", withAuth, h.SupplierFolderUploadSub, h.SupplierFolderUpload)
	b.handleWildcard(http.MethodGet, "/supplier/{id}/file/", withAuth, h.SupplierFile, h.NotFound)

	// Parts Master — Contacts
	b.handle(http.MethodGet, "/contacts", withAuth, h.ContactsList)
	b.handle(http.MethodGet, "/contacts/new", withAuth, h.ContactsNew)
	b.handle(http.MethodPost, "/contacts", withAuth, h.ContactsCreate)
	b.handle(http.MethodGet, "/contact/{id}", withAuth, h.ContactDetail)
	b.handle(http.MethodGet, "/contact/{id}/edit", withAuth, h.ContactEdit)
	b.handle(http.MethodPost, "/contact/{id}", withAuth, h.ContactUpdate)

	// Parts Master — Purchase Orders
	b.handle(http.MethodGet, "/pos", withAuth, h.POList)
	b.handle(http.MethodGet, "/pos/new", withAuth, h.PONew)
	b.handle(http.MethodGet, "/pos/export.csv", withAuth, h.POsExportCSV)
	b.handle(http.MethodPost, "/pos", withAuth, h.POCreate)
	// RFQ (issue #270) — an RFQ is a purchase_order with status 'rfq'
	b.handle(http.MethodGet, "/rfqs/new", withAuth, h.RFQNew)
	b.handle(http.MethodGet, "/rfq/{id}/add-supplier", withAuth, h.RFQAddSupplier)
	b.handle(http.MethodGet, "/rfq/{group}/compare", withAuth, h.RFQCompare)
	b.handle(http.MethodPost, "/rfq/{group}/compare", withAuth, h.RFQCompareSave)
	b.handle(http.MethodPost, "/rfq/{id}/convert", withAuth, h.RFQConvert)
	b.handle(http.MethodGet, "/po/{id}", withAuth, h.PODetail)
	b.handle(http.MethodGet, "/po/{id}/edit", withAuth, h.POEdit)
	b.handle(http.MethodPost, "/po/{id}", withAuth, h.POUpdate)
	b.handle(http.MethodPost, "/po/{id}/status", withAuth, h.POStatusTransition)
	b.handle(http.MethodPost, "/po/{id}/receive", withAuth, h.POReceive)
	b.handle(http.MethodPost, "/po/{id}/approval", withAuth, h.POApprovalAction)
	b.handle(http.MethodGet, "/po/{id}/note", withAuth, h.PONote)
	b.handle(http.MethodGet, "/po/{id}/print", withAuth, h.POPrint)
	b.handle(http.MethodPost, "/po/{id}/mark-printed", withAuth, h.POMarkPrinted)
	b.handle(http.MethodPost, "/po/{id}/add-suggestions", withAuth, h.POAddSuggestions)
	b.handle(http.MethodPost, "/po/{id}/open-folder", withAuth, h.POOpenFolder)
	b.handle(http.MethodPost, "/po/{id}/import-part-file", withAuth, h.POImportPartFile)
	b.handle(http.MethodGet, "/po/{id}/duplicate", withAuth, h.PODuplicate)
	b.handle(http.MethodGet, "/po/{id}/start-rfq", withAuth, h.POStartRFQ)
	b.handleWildcard(http.MethodGet, "/po/{id}/folder/", withAuth, h.POFolderSub, h.POFolder)
	b.handleWildcard(http.MethodPost, "/po/{id}/folder-upload/", withAuth, h.POFolderUploadSub, h.POFolderUpload)
	b.handleWildcard(http.MethodGet, "/po/{id}/file/", withAuth, h.POFile, h.NotFound)

	// Parts Master — API
	b.handle(http.MethodGet, "/api/suppliers/search", withAuth, h.APISupplierSearch)
	b.handle(http.MethodGet, "/api/suppliers/{id}/contacts", withAuth, h.APISupplierContacts)
	b.handle(http.MethodGet, "/api/parts/search", withAuth, h.APIPartSearch)
	b.handle(http.MethodGet, "/api/parts/next-number", withAuth, h.PartsNextNumber)
	b.handle(http.MethodGet, "/api/supplier-part", withAuth, h.APISupplierPN)
	b.handle(http.MethodGet, "/api/digikey/lookup", withAuth, h.APIDigiKeyLookup)
	b.handle(http.MethodGet, "/api/part/{id}/local-attachments", withAuth, h.APIPartLocalAttachments)
	b.handle(http.MethodGet, "/api/part/{id}/bom-children", withAuth, h.APIPartBOMChildren)
	b.handle(http.MethodGet, "/api/parts/rows", withAuth, h.PartsRows)
	b.handle(http.MethodGet, "/api/suppliers/rows", withAuth, h.SuppliersRows)
	b.handle(http.MethodGet, "/api/forms/rows", withAuth, h.FormsRows)
	b.handle(http.MethodGet, "/api/contacts/rows", withAuth, h.ContactsRows)
	b.handle(http.MethodGet, "/api/pos/rows", withAuth, h.PORows)

	// Test Records — Forms and Records
	b.handle(http.MethodGet, "/records", withAuth, h.FormsList)
	b.handle(http.MethodGet, "/forms/{id}/records", withAuth, h.RecordsList)
	b.handle(http.MethodGet, "/forms/{id}/yield", withAuth, h.RecordsYieldSummary)
	b.handle(http.MethodGet, "/forms/{id}/failure-modes", withAuth, h.RecordsFailureModes)
	b.handle(http.MethodGet, "/api/forms/{id}/records/rows", withAuth, h.RecordsRows)
	b.handle(http.MethodPost, "/forms/{id}/records/bulk-lock", withAuth, h.BulkLockRecords)
	b.handle(http.MethodGet, "/forms/{id}/records/new", withAuth, h.NewRecord)
	b.handle(http.MethodPost, "/forms/{id}/records/new", withAuth, h.CreateRecord)
	b.handle(http.MethodGet, "/forms/{id}/def", withAuth, h.FormDef)
	b.handle(http.MethodGet, "/forms/{id}/def/edit", withAuth, h.EditFormDef)
	b.handle(http.MethodPost, "/forms/{id}/def/edit", withAuth, h.SaveFormDef)
	b.handle(http.MethodGet, "/api/forms/{id}/def/history", withAuth, h.FormDefHistory)
	b.handle(http.MethodGet, "/forms/{id}/tests/{testID}/report", withAuth, h.TestReport)
	b.handle(http.MethodGet, "/api/forms/{id}/tests/{testID}/report/rows", withAuth, h.TestReportRows)
	b.handle(http.MethodPost, "/forms/{id}/tests/{testID}/archive", withAuth, h.ArchiveStep)
	b.handle(http.MethodGet, "/records/{id}", withAuth, h.RecordDetail)
	b.handle(http.MethodGet, "/records/{id}/print", withAuth, h.RecordPrint)
	b.handle(http.MethodGet, "/records/{id}/edit", withAuth, h.EditRecord)
	b.handle(http.MethodPost, "/records/{id}/edit", withAuth, h.SaveResults)
	b.handle(http.MethodPost, "/api/record/{id}/step/{tid}/paste-image", withAuth, h.APIRecordPasteResultImage)
	b.handle(http.MethodPost, "/records/{id}/resync", withAuth, h.ResyncRecord)
	b.handle(http.MethodPost, "/records/{id}/lock", withAuth, h.LockRecord)
	b.handle(http.MethodPost, "/records/{id}/approve", withAuth, h.ApproveRecord)
	b.handle(http.MethodPost, "/records/{id}/unlock", withAuth, h.UnlockRecord)
	b.handle(http.MethodPost, "/records/{id}/duplicate", withAuth, h.DuplicateRecord)
	b.handle(http.MethodGet, "/forms/new", withAuth, h.NewForm)
	b.handle(http.MethodPost, "/forms/new", withAuth, h.CreateForm)
	b.handle(http.MethodGet, "/forms/{id}/duplicate", withAuth, h.DuplicateForm)
	b.handle(http.MethodPost, "/forms/{id}/duplicate", withAuth, h.CreateDuplicate)
	b.handle(http.MethodPost, "/forms/{id}/lock", withAuth, h.LockForm)
	b.handle(http.MethodPost, "/forms/{id}/unlock", withAuth, h.UnlockForm)
	b.handle(http.MethodGet, "/api/named-query", withAuth, h.APINamedQuery)

	var router http.Handler = withNotFound(b.mux, h.NotFound)
	router = h.RequireCsrfOnPost(router)
	// Must run before RequireCsrfOnPost: its verifyCsrf call reads
	// r.FormValue, which for a multipart POST fully parses the body via
	// r.ParseMultipartForm before any handler runs — a handler's own
	// ParseMultipartForm(maxUploadBytes) call afterward is then a no-op
	// (net/http: "subsequent calls have no effect"), so this is the only
	// place a size ceiling can still apply to multipart uploads (#36).
	router = (func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
			next.ServeHTTP(w, r)
		})
	})(router)
	router = h.profileRequest(router)
	router = recovererMiddleware(router)
	router = loggingMiddleware(router)
	router = h.RequireLocalHost(router)

	return router, b.routes
}

// headlessEnabled reports whether ARX_HEADLESS=1: serve with no systray (container, StartOS).
func headlessEnabled() bool { return os.Getenv("ARX_HEADLESS") == "1" }

// runHeadless starts the server and blocks until ctx is cancelled (clean shutdown, 0)
// or the listener stops on its own (1).
func runHeadless(ctx context.Context, start func(onStopped func()) (*http.Server, string), closeDB func()) int {
	stopped := make(chan struct{}, 1)
	server, _ := start(func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := server.Shutdown(shutdownCtx)
		cancel()
		closeDB()
		if err != nil {
			log.Printf("Arx: shutdown: %v", err)
			return 1
		}
		return 0
	case <-stopped:
		log.Printf("Arx: listener failed, exiting")
		closeDB()
		return 1
	}
}
