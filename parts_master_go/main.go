package partsmaster

import (
	"context"
	"database/sql"
	"io/fs"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"arx/parts_master_go/config"
	"arx/parts_master_go/db"
	"arx/parts_master_go/handlers"
)

// App holds the configured HTTP server and handler for Parts Master.
type App struct {
	Server    *http.Server
	URL       string
	Port      string
	Name      string
	DebugMode bool
	h         *handlers.Handler
	router    *chi.Mux
}

// New loads config, connects to the database, and returns a ready-to-serve App.
func New() *App {
	cfg := config.Load()

	var database *sql.DB
	if dsn := cfg.DSN(); dsn != "" {
		if conn, err := db.Connect(dsn); err == nil {
			database = conn
			log.Println("parts_master: auto-connected to database")
		} else {
			log.Printf("parts_master: auto-connect failed (open Settings to reconnect): %v", err)
		}
	} else {
		log.Println("parts_master: no database password configured — open Settings to connect")
	}

	h := handlers.New(database, cfg, templatesFS, releaseNotesData)
	h.CheckSchemaVersion(context.Background())
	router := buildRouter(h)

	return &App{
		Server:    &http.Server{Addr: "0.0.0.0:" + cfg.Port, Handler: router},
		URL:       "http://localhost:" + cfg.Port,
		Port:      cfg.Port,
		Name:      "Parts Master",
		DebugMode: cfg.DebugMode,
		h:         h,
		router:    router,
	}
}

// Handler returns the combined HTTP handler (used by arx_go to serve a single port).
func (a *App) Handler() http.Handler {
	return a.router
}

// DB returns the shared database pool (injected into Test Records by arx_go).
func (a *App) DB() *sql.DB { return a.h.DB() }

// Config returns the active config (shared with Test Records).
func (a *App) Config() *config.Config { return a.h.Config() }

// SetFallback installs a fallback handler for paths PM's router doesn't match.
func (a *App) SetFallback(fallback http.Handler) {
	a.router.NotFound(fallback.ServeHTTP)
}

// SetAfterSettingsSave wires a callback invoked after a settings save with the
// freshly reconnected pool, so arx_go can hand it to Test Records.
func (a *App) SetAfterSettingsSave(fn func(newDB *sql.DB)) {
	a.h.AfterSettingsSave = fn
}

// Close shuts down the database connection pool.
func (a *App) Close() {
	a.h.CloseDB()
}

func buildRouter(h *handlers.Handler) *chi.Mux {
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

		// Suppliers / Vendors
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
