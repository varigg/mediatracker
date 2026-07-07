// Package setup wires configured provider adapters into a registry. It
// lives outside package providers because the adapters import providers;
// providers importing them back would be a cycle.
package setup

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/providers/books"
	"github.com/varigg/mediatracker/internal/providers/igdb"
	"github.com/varigg/mediatracker/internal/providers/tmdb"
	"github.com/varigg/mediatracker/internal/store"
)

// FromConfig registers every provider whose keys are configured. Books
// registers unconditionally: Open Library needs no key, and Hardcover
// enrichment self-disables without one.
func FromConfig(p config.Providers, logger *slog.Logger) *providers.Registry {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	r := providers.NewRegistry()
	if p.TMDBKey != "" {
		c := tmdb.New(p.TMDBKey, p.OMDBKey,
			tmdb.WithHTTPClient(httpClient), tmdb.WithLogger(logger))
		r.Register(store.TypeMovie, c.Movies())
		r.Register(store.TypeTV, c.TV())
	}
	r.Register(store.TypeBook, books.New(p.HardcoverKey,
		books.WithHTTPClient(httpClient), books.WithLogger(logger)))
	if p.IGDBClientID != "" && p.IGDBClientSecret != "" {
		r.Register(store.TypeGame, igdb.New(p.IGDBClientID, p.IGDBClientSecret,
			igdb.WithHTTPClient(httpClient), igdb.WithLogger(logger)))
	}
	return r
}
