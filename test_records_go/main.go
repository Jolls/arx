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

	"arx/test_records_go/config"
	"arx/test_records_go/db"
	"arx/test_records_go/handlers"
)

var appHandler *handlers.Handler

func main() {
	// systray.Run must own the main thread (calls LockOSThread internally).
	systray.Run(onReady, onExit)
}

func onReady() {
	cfg := config.Load()

	if cfg.DebugMode {
		openDebugConsole()
	}

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

	go func() {
		time.Sleep(600 * time.Millisecond)
		_ = browser.OpenURL(url)
	}()

	systray.SetIcon(appIcon())
	systray.SetTooltip("Arx Test Records")

	mOpen := systray.AddMenuItem("Open", "Open in browser")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Arx Test Records")

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
