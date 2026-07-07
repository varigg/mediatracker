// probecheck fires one canned query per configured live provider and
// prints the resulting shapes. Manual utility for verifying fixtures
// against reality — never a CI dependency, never run by tests.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/providers/setup"
	"github.com/varigg/mediatracker/internal/store"
)

var probes = []struct {
	mediaType store.MediaType
	query     string
}{
	{store.TypeMovie, "the matrix"},
	{store.TypeTV, "breaking bad"},
	{store.TypeBook, "the hobbit"},
	{store.TypeGame, "the witcher 3"},
}

func main() {
	dataDir := flag.String("data", defaultDataDir(), "data directory containing config.toml")
	flag.Parse()

	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	registry := setup.FromConfig(cfg.Providers, slog.Default())
	avail := setup.AvailabilityFromConfig(cfg.Providers, *dataDir, slog.Default())

	exitCode := 0
	syncCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	fmt.Println("== availability cycle sync")
	for _, ap := range avail {
		if syncer, ok := ap.(providers.CycleSyncer); ok {
			if err := syncer.SyncCycle(syncCtx); err != nil {
				fmt.Printf("   sync FAILED: %v\n", err)
				exitCode = 1
			}
		}
	}
	cancel()

	for _, probe := range probes {
		fmt.Printf("== %s: %q\n", probe.mediaType, probe.query)
		p, err := registry.Get(probe.mediaType)
		if err != nil {
			fmt.Println("   skipped: not configured")
			continue
		}
		if err := runProbe(p, probe.query, avail); err != nil {
			fmt.Printf("   FAILED: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func runProbe(p providers.MetadataProvider, query string, avail []providers.AvailabilityProvider) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := p.Search(ctx, query)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("search returned no candidates")
	}
	for i, c := range candidates {
		if i == 3 {
			break
		}
		fmt.Printf("   candidate %s year=%s thumb=%t %q\n",
			c.ProviderID, fmtYear(c.Year), c.ThumbnailURL != nil, c.Title)
	}

	details, err := p.Hydrate(ctx, candidates[0].ProviderID)
	if err != nil {
		return fmt.Errorf("hydrate %s: %w", candidates[0].ProviderID, err)
	}
	fmt.Printf("   hydrated %q year=%s genres=%v cover=%t ratings=%d\n",
		details.Title, fmtYear(details.ReleaseYear), details.Genres,
		details.CoverURL != nil, len(details.Ratings))
	for _, r := range details.Ratings {
		fmt.Printf("     rating %s %d (%s)\n", r.Source, r.Score, r.Display)
	}

	metaJSON, err := json.Marshal(details.Metadata)
	if err != nil {
		metaJSON = nil
	}
	item := &store.MediaItem{
		MediaType:  details.MediaType,
		Title:      details.Title,
		Provider:   details.Provider,
		ProviderID: details.ProviderID,
		Metadata:   metaJSON,
	}
	for _, ap := range avail {
		rows, err := ap.Refresh(ctx, item)
		if err != nil {
			fmt.Printf("   availability FAILED: %v\n", err)
			continue
		}
		for _, a := range rows {
			fmt.Printf("   available %s/%s url=%t\n", a.ServiceSlug, a.Kind, a.URL != nil)
		}
	}
	return nil
}

func fmtYear(y *int) string {
	if y == nil {
		return "?"
	}
	return fmt.Sprint(*y)
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "mediatracker")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "mediatracker")
}
