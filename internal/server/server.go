// Package server is the HTTP layer: the full route surface rendering
// the M5 winner design via html/template + HTMX.
package server

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/varigg/mediatracker/internal/ingest"
	"github.com/varigg/mediatracker/internal/store"
)

// ProviderStatus reports which metadata providers have configured keys —
// booleans only, so raw keys never reach the HTTP layer. main builds this
// from cfg.Providers, mirroring internal/providers/setup's registration
// conditions.
type ProviderStatus struct {
	TMDB, OMDB, IGDB, Hardcover, Steam bool
}

// Deps wires everything the HTTP layer needs.
type Deps struct {
	Store           *store.Store
	Logger          *slog.Logger
	DataDir         string        // covers are served from {DataDir}/covers
	RefreshInterval time.Duration // bounds the "newly available" window
	Refresher       *ingest.Refresher
	Background      *sync.WaitGroup // tracks request-spawned background work (the manual global refresh) so main can drain it before closing the store; nil is allowed (tests) and means untracked
	Ingest          ingest.Deps     // the add-flow: registry lookups (Ingest.Registry) and Ingest.Add
	Providers       ProviderStatus  // which metadata providers have configured keys, for the Settings page
}

func New(d Deps) http.Handler {
	v := newViews()
	s := &site{deps: d, views: v}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assetsFS())))
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /movies-tv", s.tab("movies-tv"))
	mux.HandleFunc("GET /books", s.tab("books"))
	mux.HandleFunc("GET /games", s.tab("games"))
	mux.HandleFunc("GET /items/{id}", s.detail)
	mux.HandleFunc("POST /items/{id}/state", s.updateState)
	mux.HandleFunc("POST /items/{id}/review", s.updateReview)
	mux.HandleFunc("PUT /items/{id}/notes", s.updateNotes)
	mux.HandleFunc("POST /items/{id}/notes/preview", s.previewNotes)
	mux.HandleFunc("POST /items/{id}/refresh", s.refreshItem)
	mux.HandleFunc("POST /refresh", s.refreshAll)
	mux.HandleFunc("GET /settings", s.settings)
	mux.HandleFunc("POST /settings/services", s.toggleService)
	mux.HandleFunc("POST /settings/density", s.setDensity)
	mux.HandleFunc("GET /covers/{name}", s.cover)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("POST /items", s.addItem)
	return mux
}

type site struct {
	deps  Deps
	views *views

	refreshMu  sync.Mutex
	refreshing bool
}
