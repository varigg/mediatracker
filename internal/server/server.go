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

// Deps wires everything the HTTP layer needs.
type Deps struct {
	Store           *store.Store
	Logger          *slog.Logger
	DataDir         string        // covers are served from {DataDir}/covers
	RefreshInterval time.Duration // bounds the "newly available" window
	Refresher       *ingest.Refresher
	Background      *sync.WaitGroup // tracks request-spawned background work (the manual global refresh) so main can drain it before closing the store; nil is allowed (tests) and means untracked
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
	mux.HandleFunc("GET /covers/{name}", s.cover)
	return mux
}

type site struct {
	deps  Deps
	views *views

	refreshMu  sync.Mutex
	refreshing bool
}
