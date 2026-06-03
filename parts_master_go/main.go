package main

import (
	"context"
	"database/sql"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/getlantern/systray"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pkg/browser"

	"arx/parts_master_go/config"
	"arx/parts_master_go/db"
	"arx/parts_master_go/handlers"
)

var appHandler *handlers.Handler

func main() {
	// systray.Run must own the main thread (it calls LockOSThread internally).
	systray.Run(onReady, onExit)
}

func onReady() {
	cfg := config.Load()

	if cfg.DebugMode {
		openDebugConsole()
	}

	// Try to auto-connect using the password stored in config/local.json.
	var database *sql.DB
	if dsn := cfg.DSN(); dsn != "" {
		if conn, err := db.Connect(dsn); err == nil {
			database = conn
			log.Println("auto-connected to database")
		} else {
			log.Printf("auto-connect failed (open Settings to reconnect): %v", err)
		}
	} else {
		log.Println("no database password configured — open Settings to connect")
	}

	appHandler = handlers.New(database, cfg, templatesFS, releaseNotesData)
	appHandler.CheckSchemaVersion(context.Background())
	addr := "0.0.0.0:" + cfg.Port
	url := "http://localhost:" + cfg.Port

	go func() {
		log.Printf("starting on %s", addr)
		if err := http.ListenAndServe(addr, buildRouter(appHandler)); err != nil {
			log.Fatal(err)
		}
	}()

	// Open the browser once the server has had a moment to start.
	go func() {
		time.Sleep(600 * time.Millisecond)
		_ = browser.OpenURL(url)
	}()

	// System tray icon and menu.
	systray.SetIcon(appIcon())
	systray.SetTooltip("Arx Parts Master")

	mOpen := systray.AddMenuItem("Open", "Open in browser")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Arx Parts Master")

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
	if appHandler != nil {
		appHandler.CloseDB()
	}
}

func buildRouter(h *handlers.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(h.RequireCsrfOnPost)

	// Always accessible — no DB connection required.
	subStatic, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(subStatic))))
	r.Get("/settings", h.Settings)
	r.Post("/settings", h.SettingsSave)
	r.Get("/whats-new", h.WhatsNew)
	r.Get("/api/browse-folder", h.APIBrowseFolder)

	// All other routes require a live database connection.
	r.Group(func(r chi.Router) {
		r.Use(h.RequireAuth)

		// Local file serving
		r.Get("/local/*", h.ServeLocalFile)
		r.Get("/local-dir/*", h.ServeLocalDir)
		r.Get("/supplier-local/*", h.ServeSupplierFile)
		r.Get("/supplier-local-dir/*", h.ServeSupplierDir)

		// Parts
		r.Get("/", h.PartsList)
		r.Get("/parts/new", h.PartsNew)
		r.Post("/parts", h.PartsCreate)
		r.Get("/part/{id}", h.PartDetail)
		r.Get("/part/{id}/details", h.PartDetail)
		r.Get("/part/{id}/edit", h.PartEdit)
		r.Post("/part/{id}", h.PartUpdate)
		r.Get("/part/{id}/bom", h.PartBOM)
		r.Get("/part/{id}/bom/edit", h.PartBOMEdit)
		r.Post("/part/{id}/bom", h.PartBOMSave)
		r.Post("/part/{id}/rollup-cost", h.PartRollupCost)
		r.Get("/part/{id}/where-used", h.PartWhereUsed)
		r.Get("/part/{id}/attachments", h.PartAttachments)
		r.Post("/part/{id}/attachments", h.PartAttachmentCreate)
		r.Post("/part/{id}/attachments/{attID}", h.PartAttachmentUpdate)
		r.Post("/part/{id}/primary_attachment", h.PartSetPrimaryAttachment)
		r.Post("/part/{id}/attachments/{attID}/delete", h.PartAttachmentDelete)
		r.Get("/part/{id}/orders", h.PartOrders)
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

		// Suppliers
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

		// Contacts
		r.Get("/contacts", h.ContactsList)
		r.Get("/contacts/new", h.ContactsNew)
		r.Post("/contacts", h.ContactsCreate)
		r.Get("/contact/{id}", h.ContactDetail)
		r.Get("/contact/{id}/edit", h.ContactEdit)
		r.Post("/contact/{id}", h.ContactUpdate)

		// Purchase Orders
		r.Get("/pos", h.POList)
		r.Get("/pos/new", h.PONew)
		r.Post("/pos", h.POCreate)
		r.Get("/po/{id}", h.PODetail)
		r.Get("/po/{id}/edit", h.POEdit)
		r.Post("/po/{id}", h.POUpdate)
		r.Get("/po/{id}/note", h.PONote)
		r.Get("/po/{id}/print", h.POPrint)
		r.Post("/po/{id}/mark-printed", h.POMarkPrinted)
		r.Post("/po/{id}/open-folder", h.POOpenFolder)
		r.Get("/po/{id}/duplicate", h.PODuplicate)
		r.Get("/po/{id}/folder", h.POFolder)
		r.Get("/po/{id}/folder/*", h.POFolderSub)
		r.Get("/po/{id}/file/*", h.POFile)

		// API endpoints
		r.Get("/api/suppliers/search", h.APISupplierSearch)
		r.Get("/api/suppliers/{id}/contacts", h.APISupplierContacts)
		r.Get("/api/parts/search", h.APIPartSearch)
	})

	r.NotFound(h.NotFound)

	return r
}
