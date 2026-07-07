package gamecatalogs

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

func gameItem(title string, altNames ...string) *store.MediaItem {
	item := &store.MediaItem{ID: 1, MediaType: store.TypeGame, Title: title, Provider: "igdb", ProviderID: "1"}
	if len(altNames) > 0 {
		meta, _ := json.Marshal(map[string]any{"alternative_names": altNames})
		item.Metadata = meta
	}
	return item
}

func syncedProvider(t *testing.T) *Provider {
	t.Helper()
	p := newTestProvider(t, healthyMux(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	return p
}

func TestRefreshMatchesEditionAcrossCatalogs(t *testing.T) {
	p := syncedProvider(t)

	// Base title matches the catalog's "Deluxe Edition" entry.
	got, err := p.Refresh(context.Background(), gameItem("Forza Horizon 5"))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}
	row := got[0]
	if row.ServiceSlug != "game_pass" || row.Kind != "subscription" {
		t.Errorf("row = %+v, want game_pass/subscription", row)
	}
	if row.URL == nil || *row.URL != "https://www.xbox.com/en-US/games/store/forza-horizon-5/9NKX70BBCDRN" {
		t.Errorf("URL = %v, want catalog store URL", row.URL)
	}

	// Title matches the PS+ "Complete Edition" entry.
	got, err = p.Refresh(context.Background(), gameItem("The Witcher 3: Wild Hunt"))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 1 || got[0].ServiceSlug != "ps_plus" {
		t.Errorf("witcher rows = %+v, want one ps_plus row", got)
	}
}

func TestRefreshMatchesViaAlternativeName(t *testing.T) {
	p := syncedProvider(t)
	got, err := p.Refresh(context.Background(), gameItem("Yharnam Simulator", "Bloodborne"))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 1 || got[0].ServiceSlug != "ps_plus" {
		t.Errorf("rows = %+v, want ps_plus match via alternative name", got)
	}
}

func TestRefreshLoadsSnapshotFromDisk(t *testing.T) {
	first := syncedProvider(t)
	// Second provider over the same dir, never synced: must lazy-load.
	second := New(first.dir)
	got, err := second.Refresh(context.Background(), gameItem("Halo Infinite"))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 1 || got[0].ServiceSlug != "game_pass" {
		t.Errorf("rows = %+v, want game_pass via lazily loaded snapshot", got)
	}
}

func TestRefreshWithoutSnapshotsDegrades(t *testing.T) {
	p := newTestProvider(t, http.NewServeMux()) // never synced, empty dir
	got, err := p.Refresh(context.Background(), gameItem("Halo Infinite"))
	if err != nil {
		t.Fatalf("missing snapshots must degrade, got error %v", err)
	}
	if len(got) != 0 {
		t.Errorf("rows = %+v, want none", got)
	}
}

func TestRefreshIgnoresNonGames(t *testing.T) {
	p := syncedProvider(t)
	item := &store.MediaItem{ID: 2, MediaType: store.TypeMovie, Title: "Halo Infinite", Provider: "tmdb", ProviderID: "movie:1"}
	got, err := p.Refresh(context.Background(), item)
	if err != nil || len(got) != 0 {
		t.Errorf("non-game Refresh = (%+v, %v), want (none, nil)", got, err)
	}
}

var _ providers.AvailabilityProvider = (*Provider)(nil)
var _ providers.CycleSyncer = (*Provider)(nil)
