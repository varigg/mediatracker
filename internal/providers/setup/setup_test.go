package setup

import (
	"log/slog"
	"testing"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

func TestFromConfigRegistersByConfiguredKeys(t *testing.T) {
	logger := slog.Default()

	r := FromConfig(config.Providers{}, logger)
	if _, err := r.Get(store.TypeBook); err != nil {
		t.Errorf("books must register without keys: %v", err)
	}
	for _, mt := range []store.MediaType{store.TypeMovie, store.TypeTV, store.TypeGame} {
		if _, err := r.Get(mt); err == nil {
			t.Errorf("%s must not register without keys", mt)
		}
	}

	full := config.Providers{
		TMDBKey:          "k",
		OMDBKey:          "k",
		IGDBClientID:     "id",
		IGDBClientSecret: "secret",
		HardcoverKey:     "k",
	}
	r = FromConfig(full, logger)
	for _, mt := range []store.MediaType{store.TypeMovie, store.TypeTV, store.TypeBook, store.TypeGame} {
		if _, err := r.Get(mt); err != nil {
			t.Errorf("%s must register with full keys: %v", mt, err)
		}
	}
}

func TestAvailabilityFromConfig(t *testing.T) {
	logger := slog.Default()
	dataDir := t.TempDir()

	avail := AvailabilityFromConfig(config.Providers{}, dataDir, logger)
	if len(avail) != 1 {
		t.Fatalf("keyless config: %d providers, want 1 (gamecatalogs only)", len(avail))
	}
	if _, ok := avail[0].(providers.CycleSyncer); !ok {
		t.Error("gamecatalogs must be a CycleSyncer")
	}

	full := config.Providers{TMDBKey: "k", SteamKey: "k", SteamID: "76561198000000000"}
	avail = AvailabilityFromConfig(full, dataDir, logger)
	if len(avail) != 3 {
		t.Fatalf("full config: %d providers, want 3", len(avail))
	}
	syncers := 0
	for _, ap := range avail {
		if _, ok := ap.(providers.CycleSyncer); ok {
			syncers++
		}
	}
	if syncers != 2 {
		t.Errorf("full config: %d CycleSyncers, want 2 (gamecatalogs, steam)", syncers)
	}
}
