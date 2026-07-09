// Package server is the HTTP layer: the full route surface rendering
// the M5 winner design via html/template + HTMX.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/varigg/mediatracker/internal/store"
)

// Deps wires everything the HTTP layer needs.
type Deps struct {
	Store           *store.Store
	Logger          *slog.Logger
	DataDir         string        // covers are served from {DataDir}/covers
	RefreshInterval time.Duration // bounds the "newly available" window
}

func New(d Deps) http.Handler {
	v := newViews()
	s := &site{deps: d, views: v}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assetsFS())))
	// Read-only views (this session); mutations land in M6b.
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /movies-tv", s.tab("movies-tv"))
	mux.HandleFunc("GET /books", s.tab("books"))
	mux.HandleFunc("GET /games", s.tab("games"))
	mux.HandleFunc("GET /items/{id}", s.detail)
	mux.HandleFunc("GET /covers/{name}", s.cover)
	return mux
}

type site struct {
	deps  Deps
	views *views
}
