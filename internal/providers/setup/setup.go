// Package setup wires configured provider adapters into a registry. It
// lives outside package providers because the adapters import providers;
// providers importing them back would be a cycle.
package setup

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/providers/books"
	"github.com/varigg/mediatracker/internal/providers/gamecatalogs"
	"github.com/varigg/mediatracker/internal/providers/igdb"
	"github.com/varigg/mediatracker/internal/providers/steam"
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

// AvailabilityFromConfig returns the availability enrichers in refresh
// order. gamecatalogs is always on (no keys needed); tmdbWatch needs the
// TMDB key; steam needs both key and ID. Callers type-assert
// providers.CycleSyncer for the once-per-cycle snapshot syncs.
func AvailabilityFromConfig(p config.Providers, dataDir string, logger *slog.Logger) []providers.AvailabilityProvider {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	catalogsDir := filepath.Join(dataDir, "catalogs")
	var out []providers.AvailabilityProvider
	if p.TMDBKey != "" {
		c := tmdb.New(p.TMDBKey, "", tmdb.WithHTTPClient(httpClient), tmdb.WithLogger(logger))
		out = append(out, c.WatchProvider())
	}
	out = append(out, gamecatalogs.New(catalogsDir, gamecatalogs.WithLogger(logger)))
	if p.SteamKey != "" && p.SteamID != "" {
		out = append(out, steam.New(p.SteamKey, p.SteamID, catalogsDir,
			steam.WithHTTPClient(httpClient), steam.WithLogger(logger)))
	}
	return out
}
