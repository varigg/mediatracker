package steam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

func serveOwned(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("key") != "test-key" || q.Get("steamid") != "76561198000000000" {
			t.Errorf("query = %s, want key and steamid", r.URL.RawQuery)
		}
		data, err := os.ReadFile("testdata/owned_games.json")
		if err != nil {
			t.Errorf("read fixture: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*Provider, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/IPlayerService/GetOwnedGames/v0001/", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cacheDir := filepath.Join(t.TempDir(), "catalogs")
	return New("test-key", "76561198000000000", cacheDir, WithBaseURL(srv.URL)), cacheDir
}

func gameItem(title string, appID int64) *store.MediaItem {
	item := &store.MediaItem{ID: 1, MediaType: store.TypeGame, Title: title, Provider: "igdb", ProviderID: "1"}
	if appID != 0 {
		meta, _ := json.Marshal(map[string]any{"steam_appid": appID})
		item.Metadata = meta
	}
	return item
}

func TestSyncCycleWritesSnapshot(t *testing.T) {
	p, cacheDir := newTestProvider(t, serveOwned(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cacheDir, "steam_owned.json"))
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	var snap struct {
		FetchedAt string `json:"fetched_at"`
		Games     []struct {
			AppID int64 `json:"appid"`
		} `json:"games"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("snapshot unparsable: %v", err)
	}
	if len(snap.Games) != 2 || snap.FetchedAt == "" {
		t.Errorf("snapshot = %d games, fetched_at %q", len(snap.Games), snap.FetchedAt)
	}
}

func TestRefreshMatchesByAppID(t *testing.T) {
	p, _ := newTestProvider(t, serveOwned(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	got, err := p.Refresh(context.Background(), gameItem("Totally Different Title", 292030))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want 1 owned row", got)
	}
	row := got[0]
	if row.ServiceSlug != "steam" || row.Kind != "owned" {
		t.Errorf("row = %+v, want steam/owned", row)
	}
	if row.URL == nil || *row.URL != "https://store.steampowered.com/app/292030" {
		t.Errorf("URL = %v, want store page", row.URL)
	}
}

func TestRefreshFallsBackToNameMatch(t *testing.T) {
	p, _ := newTestProvider(t, serveOwned(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	got, err := p.Refresh(context.Background(), gameItem("HADES", 0))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 1 || got[0].URL == nil || *got[0].URL != "https://store.steampowered.com/app/1145360" {
		t.Errorf("rows = %+v, want owned Hades via name match", got)
	}
}

func TestRefreshNoMatchAndNonGame(t *testing.T) {
	p, _ := newTestProvider(t, serveOwned(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	if got, err := p.Refresh(context.Background(), gameItem("Starfield", 0)); err != nil || len(got) != 0 {
		t.Errorf("unowned game = (%+v, %v), want (none, nil)", got, err)
	}
	movie := &store.MediaItem{ID: 2, MediaType: store.TypeMovie, Title: "Hades", Provider: "tmdb", ProviderID: "movie:1"}
	if got, err := p.Refresh(context.Background(), movie); err != nil || len(got) != 0 {
		t.Errorf("non-game = (%+v, %v), want (none, nil)", got, err)
	}
}

func TestSyncFailureRetainsStaleList(t *testing.T) {
	failing := false
	p, cacheDir := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		serveOwned(t)(w, r)
	})
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("first SyncCycle error = %v", err)
	}
	failing = true
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("failed sync must degrade, got %v", err)
	}
	if got, _ := p.Refresh(context.Background(), gameItem("Hades", 0)); len(got) != 1 {
		t.Errorf("stale in-memory list must keep matching, got %+v", got)
	}
	// A fresh provider over the same cache dir must lazy-load the stale file.
	fresh := New("test-key", "76561198000000000", cacheDir)
	if got, _ := fresh.Refresh(context.Background(), gameItem("Hades", 0)); len(got) != 1 {
		t.Errorf("stale on-disk list must keep matching, got %+v", got)
	}
}

func TestRefreshWithoutCacheDegrades(t *testing.T) {
	p, _ := newTestProvider(t, serveOwned(t)) // never synced
	got, err := p.Refresh(context.Background(), gameItem("Hades", 0))
	if err != nil || len(got) != 0 {
		t.Errorf("no cache = (%+v, %v), want (none, nil)", got, err)
	}
}

var _ providers.AvailabilityProvider = (*Provider)(nil)
var _ providers.CycleSyncer = (*Provider)(nil)
