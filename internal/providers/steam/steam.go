// Package steam implements the ownership enricher via the official
// GetOwnedGames Web API. The owned list is fetched once per cycle,
// snapshotted alongside the game catalogs, and matched per item by the
// IGDB-supplied Steam app ID with a normalized-name fallback.
package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/providers/names"
	"github.com/varigg/mediatracker/internal/store"
)

const defaultBaseURL = "https://api.steampowered.com"

type Provider struct {
	apiKey     string
	steamID    string
	baseURL    string
	cacheFile  string // {cacheDir}/steam_owned.json
	httpClient *http.Client
	logger     *slog.Logger
	now        func() time.Time

	mu    sync.Mutex
	owned *ownedIndex
}

type Option func(*Provider)

func WithBaseURL(u string) Option          { return func(p *Provider) { p.baseURL = u } }
func WithHTTPClient(h *http.Client) Option { return func(p *Provider) { p.httpClient = h } }
func WithLogger(l *slog.Logger) Option     { return func(p *Provider) { p.logger = l } }
func WithNow(now func() time.Time) Option  { return func(p *Provider) { p.now = now } }

func New(apiKey, steamID, cacheDir string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:     apiKey,
		steamID:    steamID,
		baseURL:    defaultBaseURL,
		cacheFile:  filepath.Join(cacheDir, "steam_owned.json"),
		httpClient: http.DefaultClient,
		logger:     slog.Default(),
		now:        time.Now,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type ownedGame struct {
	AppID int64  `json:"appid"`
	Name  string `json:"name"`
}

type snapshot struct {
	FetchedAt string      `json:"fetched_at"` // UTC "2006-01-02 15:04:05"
	Games     []ownedGame `json:"games"`
}

type ownedIndex struct {
	byAppID map[int64]ownedGame
	byName  *names.Set
}

func newIndex(games []ownedGame) *ownedIndex {
	idx := &ownedIndex{byAppID: make(map[int64]ownedGame, len(games)), byName: names.NewSet()}
	for _, g := range games {
		idx.byAppID[g.AppID] = g
		u := storeURL(g.AppID)
		idx.byName.Add(g.Name, &u)
	}
	return idx
}

func storeURL(appID int64) string {
	return fmt.Sprintf("https://store.steampowered.com/app/%d", appID)
}

// SyncCycle re-fetches the owned-games list. A failed fetch retains the
// previous snapshot (stale beats none); only an unusable cache dir is an
// error.
func (p *Provider) SyncCycle(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(p.cacheFile), 0o755); err != nil {
		return fmt.Errorf("steam: create cache dir: %w", err)
	}
	games, err := p.fetchOwned(ctx)
	if err != nil {
		p.logger.Warn("steam owned-games fetch failed, keeping stale list", "error", err)
		return nil
	}
	snap := snapshot{FetchedAt: p.now().UTC().Format(store.TimeFormat), Games: games}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.cacheFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		p.logger.Warn("steam snapshot write failed", "error", err)
		return nil
	}
	if err := os.Rename(tmp, p.cacheFile); err != nil {
		p.logger.Warn("steam snapshot write failed", "error", err)
		return nil
	}
	p.mu.Lock()
	p.owned = newIndex(games)
	p.mu.Unlock()
	return nil
}

type ownedResponse struct {
	Response struct {
		GameCount int         `json:"game_count"`
		Games     []ownedGame `json:"games"`
	} `json:"response"`
}

func (p *Provider) fetchOwned(ctx context.Context) ([]ownedGame, error) {
	params := url.Values{
		"key":             {p.apiKey},
		"steamid":         {p.steamID},
		"include_appinfo": {"1"},
		"format":          {"json"},
	}
	u := p.baseURL + "/IPlayerService/GetOwnedGames/v0001/?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("steam: owned games: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam: owned games returned %s", resp.Status)
	}
	var body ownedResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("steam: decode owned games: %w", err)
	}
	return body.Response.Games, nil
}

// index returns the owned-games lookup, lazily loading the snapshot from
// disk (startup before first sync). Missing snapshot ⇒ nil: no ownership
// facts, not an error.
func (p *Provider) index() *ownedIndex {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owned != nil {
		return p.owned
	}
	data, err := os.ReadFile(p.cacheFile)
	if err != nil {
		return nil
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	p.owned = newIndex(snap.Games)
	return p.owned
}

// Refresh reports ownership for game items: metadata steam_appid first
// (IGDB external-ID mapping), then normalized-name fallback.
func (p *Provider) Refresh(ctx context.Context, item *store.MediaItem) ([]providers.Availability, error) {
	if item.MediaType != store.TypeGame {
		return nil, nil
	}
	idx := p.index()
	if idx == nil {
		return nil, nil
	}
	if appID, ok := steamAppID(item); ok {
		if _, owned := idx.byAppID[appID]; owned {
			u := storeURL(appID)
			return []providers.Availability{{ServiceSlug: "steam", Kind: "owned", URL: &u}}, nil
		}
	}
	if entry, ok := idx.byName.Lookup(providers.NameCandidates(item)...); ok {
		return []providers.Availability{{ServiceSlug: "steam", Kind: "owned", URL: entry.URL}}, nil
	}
	return nil, nil
}

func steamAppID(item *store.MediaItem) (int64, bool) {
	if len(item.Metadata) == 0 {
		return 0, false
	}
	var meta struct {
		SteamAppID int64 `json:"steam_appid"`
	}
	if err := json.Unmarshal(item.Metadata, &meta); err != nil || meta.SteamAppID == 0 {
		return 0, false
	}
	return meta.SteamAppID, true
}
