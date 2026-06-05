package testrecords

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"arx/test_records_go/config"
	"arx/test_records_go/handlers"
)

// App holds the configured HTTP handler for Test Records.
type App struct {
	Server    *http.Server
	URL       string
	Port      string
	Name      string
	DebugMode bool
	h         *handlers.Handler
	router    http.Handler
}

// New builds the Test Records app around a DB pool and config injected by arx_go.
// The pool is owned by Parts Master and shared, so New neither loads config nor connects.
func New(database *sql.DB, cfg *config.Config) *App {
	h := handlers.New(database, cfg, templatesFS)
	h.CheckSchemaVersion(context.Background())
	router := buildRouter(h)

	return &App{
		Server:    &http.Server{Addr: "0.0.0.0:" + cfg.Port, Handler: router},
		URL:       "http://localhost:" + cfg.Port,
		Port:      cfg.Port,
		Name:      "Test Records",
		DebugMode: cfg.DebugMode,
		h:         h,
		router:    router,
	}
}

// Handler returns the HTTP handler for this app (used by arx_go to mount as fallback).
func (a *App) Handler() http.Handler {
	return a.router
}

// SetDBAndConfig swaps in the shared pool + config after a settings save (called by arx_go).
func (a *App) SetDBAndConfig(database *sql.DB, cfg *config.Config) {
	a.h.SetDBAndConfig(database, cfg)
	a.DebugMode = cfg.DebugMode
}

// Close is a no-op: the DB pool is owned and closed by Parts Master (shared pool).
func (a *App) Close() {}

func buildRouter(h *handlers.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(h.RequireCsrfOnPost)

	// Static assets served under /tr-static/* (PM owns /static/*).
	subStatic, _ := fs.Sub(staticFS, "static")
	r.Handle("/tr-static/*", http.StripPrefix("/tr-static/", http.FileServer(http.FS(subStatic))))

	// All routes require a live database connection.
	r.Group(func(r chi.Router) {
		r.Use(h.RequireAuth)

		r.Get("/images/*", h.ServeImage)

		// Forms list is the TR home — mounted at /records (PM owns /).
		r.Get("/records", h.FormsList)
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
