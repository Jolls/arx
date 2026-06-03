package testrecords

import (
	"context"
	"database/sql"
	"io/fs"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"arx/test_records_go/config"
	"arx/test_records_go/db"
	"arx/test_records_go/handlers"
)

// App holds the configured HTTP server and handler for Test Records.
type App struct {
	Server    *http.Server
	URL       string
	Port      string
	Name      string
	DebugMode bool
	h         *handlers.Handler
}

// New loads config, connects to the database, and returns a ready-to-serve App.
func New() *App {
	cfg := config.Load()

	var database *sql.DB
	if dsn := cfg.DSN(); dsn != "" {
		if conn, err := db.Connect(dsn); err == nil {
			database = conn
			log.Println("test_records: auto-connected to database")
		} else {
			log.Printf("test_records: auto-connect failed (open Settings to reconnect): %v", err)
		}
	} else {
		log.Println("test_records: no database password configured — open Settings to connect")
	}

	h := handlers.New(database, cfg, templatesFS, releaseNotesData)
	h.CheckSchemaVersion(context.Background())

	return &App{
		Server:    &http.Server{Addr: "0.0.0.0:" + cfg.Port, Handler: buildRouter(h)},
		URL:       "http://localhost:" + cfg.Port,
		Port:      cfg.Port,
		Name:      "Test Records",
		DebugMode: cfg.DebugMode,
		h:         h,
	}
}

// Close shuts down the database connection pool.
func (a *App) Close() {
	a.h.CloseDB()
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

		r.Get("/local/*", h.ServeLocalFile)
		r.Get("/images/*", h.ServeImage)

		r.Get("/", h.FormsList)
		r.Get("/forms/{id}/records", h.RecordsList)
		r.Get("/forms/{id}/records/new", h.NewRecord)
		r.Post("/forms/{id}/records/new", h.CreateRecord)
		r.Get("/forms/{id}/def", h.FormDef)
		r.Get("/forms/{id}/def/edit", h.EditFormDef)
		r.Post("/forms/{id}/def/edit", h.SaveFormDef)
		r.Get("/api/forms/{id}/def/history", h.FormDefHistory)
		r.Get("/records/{id}", h.RecordDetail)
		r.Get("/records/{id}/print", h.RecordPrint)
		r.Get("/records/{id}/edit", h.EditRecord)
		r.Post("/records/{id}/edit", h.SaveResults)
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
