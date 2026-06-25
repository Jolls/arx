package main

import (
	"context"
	"database/sql"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"

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
	h.CheckSchemaVersion(context.Background())

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
	r.Use(h.RequireCsrfOnPost)

	// Static assets: PM under /static/pm/, TR under /static/tr/.
	subStatic, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(subStatic))))

	// Always accessible — no DB connection required.
	r.Get("/login", h.LoginGet)
	r.Post("/login", h.LoginPost)
	r.Post("/logout", h.Logout)
	r.Get("/settings", h.Settings)
	r.Post("/settings", h.SettingsSave)
	r.Post("/settings/attachment-categories", h.SettingsAttachmentCategoriesSave)
	r.Post("/settings/categories", h.SettingsCategoriesSave)
	r.Get("/whats-new", h.WhatsNew)
	r.Get("/api/browse-folder", h.APIBrowseFolder)

	// All other routes require a live database connection.
	r.Group(func(r chi.Router) {
		r.Use(h.RequireAuth)

		// User management (Settings → Users tab)
		r.Post("/settings/users", h.SettingsUsersCreate)
		r.Post("/settings/users/{userID}/password", h.SettingsUsersResetPassword)
		r.Post("/settings/users/{userID}/toggle-active", h.SettingsUsersToggleActive)
		r.Post("/settings/users/{userID}/toggle-approve", h.SettingsUsersToggleApprove)

		// Data backup
		r.Get("/settings/backup", h.SettingsBackup)

		// Local file serving (Parts Master)
		r.Get("/local/*", h.ServeLocalFile)
		r.Get("/local-dir/*", h.ServeLocalDir)
		r.Get("/supplier-local/*", h.ServeSupplierFile)
		r.Get("/supplier-local-dir/*", h.ServeSupplierDir)

		// Test Records image serving
		r.Get("/images/*", h.ServeImage)

		// Parts Master — Parts
		r.Get("/", h.PartsList)
		r.Get("/parts/new", h.PartsNew)
		r.Get("/parts/export.csv", h.PartsExportCSV)
		r.Post("/parts", h.PartsCreate)
		r.Get("/part/{id}", h.PartDetail)
		r.Get("/part/{id}/details", h.PartDetail)
		r.Get("/part/{id}/edit", h.PartEdit)
		r.Post("/part/{id}", h.PartUpdate)
		r.Get("/part/{id}/bom", h.PartBOM)
		r.Get("/part/{id}/bom/edit", h.PartBOMEdit)
		r.Get("/part/{id}/bom/export.csv", h.BOMExportCSV)
		r.Post("/part/{id}/bom", h.PartBOMSave)
		r.Post("/part/{id}/rollup-cost", h.PartRollupCost)
		r.Get("/part/{id}/where-used", h.PartWhereUsed)
		r.Get("/part/{id}/attachments", h.PartAttachments)
		r.Post("/part/{id}/attachments", h.PartAttachmentCreate)
		r.Post("/part/{id}/attachments/{attID}", h.PartAttachmentUpdate)
		r.Post("/part/{id}/primary_attachment", h.PartSetPrimaryAttachment)
		r.Post("/part/{id}/attachments/{attID}/delete", h.PartAttachmentDelete)
		r.Get("/part/{id}/orders", h.PartOrders)
		r.Get("/part/{id}/transactions", h.PartTransactions)
		r.Post("/part/{id}/adjust-stock", h.PartStockAdjust)
		r.Get("/part/{id}/pricing", h.PartPricing)
		r.Get("/part/{id}/pricing/new", h.PriceNew)
		r.Post("/part/{id}/pricing", h.PriceCreate)
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
		r.Post("/po/{id}/add-supplier-links", h.POAddSupplierLinks)
		r.Post("/po/{id}/add-prices", h.POAddPrices)
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
		r.Get("/api/supplier-part", h.APISupplierPN)
		r.Get("/api/part/{id}/local-attachments", h.APIPartLocalAttachments)
		r.Get("/api/parts/rows", h.PartsRows)
		r.Get("/api/suppliers/rows", h.SuppliersRows)
		r.Get("/api/contacts/rows", h.ContactsRows)
		r.Get("/api/pos/rows", h.PORows)

		// Test Records — Forms and Records
		r.Get("/records", h.FormsList)
		r.Get("/forms/{id}/records", h.RecordsList)
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
		r.Post("/records/{id}/resync", h.ResyncRecord)
		r.Post("/records/{id}/lock", h.LockRecord)
		r.Post("/records/{id}/unlock", h.UnlockRecord)
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
