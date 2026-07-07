package gamecatalogs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/providers/names"
	"github.com/varigg/mediatracker/internal/store"
)

// Placeholder defaults — the unofficial endpoints must be verified live
// via cmd/probecheck in Milestone 3.5 (M3 design decision 3).
const (
	defaultGamePassURL = "https://catalog.gamepass.com/products"
	defaultPSPlusURL   = "https://catalog.playstation.com/psplus/games"

	breakerThreshold = 3

	slugGamePass = "game_pass"
	slugPSPlus   = "ps_plus"
)

type Provider struct {
	dir         string // {dataDir}/catalogs
	gamePassURL string
	psPlusURL   string
	httpClient  *http.Client
	logger      *slog.Logger
	now         func() time.Time
	breakers    map[string]*breaker

	mu   sync.Mutex
	sets map[string]*names.Set
}

type Option func(*Provider)

func WithGamePassURL(u string) Option      { return func(p *Provider) { p.gamePassURL = u } }
func WithPSPlusURL(u string) Option        { return func(p *Provider) { p.psPlusURL = u } }
func WithHTTPClient(h *http.Client) Option { return func(p *Provider) { p.httpClient = h } }
func WithLogger(l *slog.Logger) Option     { return func(p *Provider) { p.logger = l } }
func WithNow(now func() time.Time) Option  { return func(p *Provider) { p.now = now } }

func New(dir string, opts ...Option) *Provider {
	p := &Provider{
		dir:         dir,
		gamePassURL: defaultGamePassURL,
		psPlusURL:   defaultPSPlusURL,
		// aggressive timeout: unofficial endpoints must not stall a cycle
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     slog.Default(),
		now:        time.Now,
		breakers: map[string]*breaker{
			slugGamePass: newBreaker(breakerThreshold),
			slugPSPlus:   newBreaker(breakerThreshold),
		},
		sets: make(map[string]*names.Set),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// SyncCycle re-fetches both catalog snapshots. A failed catalog fetch
// retains the previous snapshot (stale beats none) and never cascades;
// only an unusable catalogs dir (system failure) is an error.
func (p *Provider) SyncCycle(ctx context.Context) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("gamecatalogs: create %s: %w", p.dir, err)
	}
	p.syncCatalog(ctx, slugGamePass, p.fetchGamePass)
	p.syncCatalog(ctx, slugPSPlus, p.fetchPSPlus)
	return nil
}

func (p *Provider) syncCatalog(ctx context.Context, slug string, fetch func(context.Context) ([]catalogEntry, error)) {
	b := p.breakers[slug]
	b.Reset()
	for b.Allow() {
		entries, err := fetch(ctx)
		if err != nil {
			b.Failure()
			p.logger.Warn("catalog fetch failed", "catalog", slug, "error", err)
			continue
		}
		b.Success()
		if err := p.saveSnapshot(slug, entries); err != nil {
			p.logger.Warn("catalog snapshot write failed", "catalog", slug, "error", err)
			return
		}
		p.setSet(slug, buildSet(entries))
		return
	}
	p.logger.Warn("catalog circuit open, keeping stale snapshot", "catalog", slug)
}

// Refresh matches a tracked game against the cached catalog snapshots.
// Non-game items yield nothing; a missing snapshot degrades to no facts.
func (p *Provider) Refresh(ctx context.Context, item *store.MediaItem) ([]providers.Availability, error) {
	if item.MediaType != store.TypeGame {
		return nil, nil
	}
	candidates := providers.NameCandidates(item)
	var out []providers.Availability
	for _, slug := range []string{slugGamePass, slugPSPlus} {
		set := p.set(slug)
		if set == nil {
			continue
		}
		if entry, ok := set.Lookup(candidates...); ok {
			out = append(out, providers.Availability{
				ServiceSlug: slug,
				Kind:        "subscription",
				URL:         entry.URL,
			})
		}
	}
	return out, nil
}

type gamePassResponse struct {
	Products []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"products"`
}

func (p *Provider) fetchGamePass(ctx context.Context) ([]catalogEntry, error) {
	var resp gamePassResponse
	if err := p.getJSON(ctx, p.gamePassURL, &resp); err != nil {
		return nil, err
	}
	entries := make([]catalogEntry, 0, len(resp.Products))
	for _, pr := range resp.Products {
		e := catalogEntry{Name: pr.Title}
		if pr.URL != "" {
			u := pr.URL
			e.URL = &u
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("gamecatalogs: game pass catalog empty — likely schema drift")
	}
	return entries, nil
}

type psPlusResponse struct {
	Games []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"games"`
}

func (p *Provider) fetchPSPlus(ctx context.Context) ([]catalogEntry, error) {
	var resp psPlusResponse
	if err := p.getJSON(ctx, p.psPlusURL, &resp); err != nil {
		return nil, err
	}
	entries := make([]catalogEntry, 0, len(resp.Games))
	for _, g := range resp.Games {
		e := catalogEntry{Name: g.Name}
		if g.URL != "" {
			u := g.URL
			e.URL = &u
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("gamecatalogs: ps plus catalog empty — likely schema drift")
	}
	return entries, nil
}

func (p *Provider) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gamecatalogs: %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gamecatalogs: %s returned %s", u, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("gamecatalogs: decode %s: %w", u, err)
	}
	return nil
}
