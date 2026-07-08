package gamecatalogs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/providers/names"
	"github.com/varigg/mediatracker/internal/store"
)

// Game Pass catalog fetch is a two-step call against Microsoft's real
// catalog services, verified live in Milestone 3.5: the sigls endpoint
// lists product IDs for a curated list, then displaycatalog hydrates
// those IDs into titles. gamePassListID is the "All PC Games" list —
// this deployment only tracks PC Game Pass.
//
// PS+ remains a placeholder: the PS Store's real Game Catalog page
// renders its grid client-side with no discoverable API call, and the
// one reverse-engineered category ID found (via a third-party library)
// resolves to the legacy, near-always-empty "PS Plus Monthly Games"
// list rather than the Extra/Premium catalog. Deferred pending a
// browser-captured network trace of the real request.
const (
	defaultGamePassSiglsURL    = "https://catalog.gamepass.com/sigls/v2"
	defaultGamePassProductsURL = "https://displaycatalog.mp.microsoft.com/v7.0/products"
	gamePassListID             = "fdd9e2a7-0fee-49f6-ad69-4354098401ff" // All PC Games
	gamePassBatchSize          = 100

	defaultPSPlusURL = "https://catalog.playstation.com/psplus/games"

	breakerThreshold = 3

	slugGamePass = "game_pass"
	slugPSPlus   = "ps_plus"
)

type Provider struct {
	dir                 string // {dataDir}/catalogs
	gamePassSiglsURL    string
	gamePassProductsURL string
	psPlusURL           string
	httpClient          *http.Client
	logger              *slog.Logger
	now                 func() time.Time
	breakers            map[string]*breaker

	mu   sync.Mutex
	sets map[string]*names.Set
}

type Option func(*Provider)

func WithGamePassSiglsURL(u string) Option    { return func(p *Provider) { p.gamePassSiglsURL = u } }
func WithGamePassProductsURL(u string) Option { return func(p *Provider) { p.gamePassProductsURL = u } }
func WithPSPlusURL(u string) Option           { return func(p *Provider) { p.psPlusURL = u } }
func WithHTTPClient(h *http.Client) Option    { return func(p *Provider) { p.httpClient = h } }
func WithLogger(l *slog.Logger) Option        { return func(p *Provider) { p.logger = l } }
func WithNow(now func() time.Time) Option     { return func(p *Provider) { p.now = now } }

func New(dir string, opts ...Option) *Provider {
	p := &Provider{
		dir:                 dir,
		gamePassSiglsURL:    defaultGamePassSiglsURL,
		gamePassProductsURL: defaultGamePassProductsURL,
		psPlusURL:           defaultPSPlusURL,
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
				Kind:        store.KindSubscription,
				URL:         entry.URL,
			})
		}
	}
	return out, nil
}

type sigl struct {
	ID string `json:"id"`
}

type gamePassProductsResponse struct {
	Products []struct {
		ProductID           string `json:"ProductId"`
		LocalizedProperties []struct {
			ProductTitle string `json:"ProductTitle"`
		} `json:"LocalizedProperties"`
	} `json:"Products"`
}

// fetchGamePass lists product IDs for the PC Game Pass catalog, then
// hydrates them into titles in batches (630+ IDs in one query risks
// exceeding practical URL-length limits on the products endpoint).
func (p *Provider) fetchGamePass(ctx context.Context) ([]catalogEntry, error) {
	siglsURL := fmt.Sprintf("%s?id=%s&language=en-us&market=US", p.gamePassSiglsURL, gamePassListID)
	var sigls []sigl
	if err := p.getJSON(ctx, siglsURL, &sigls); err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range sigls {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("gamecatalogs: game pass sigls list empty — likely schema drift")
	}

	var entries []catalogEntry
	for start := 0; start < len(ids); start += gamePassBatchSize {
		batch := ids[start:min(start+gamePassBatchSize, len(ids))]
		productsURL := fmt.Sprintf("%s?bigIds=%s&market=US&languages=en-us", p.gamePassProductsURL, strings.Join(batch, ","))
		var resp gamePassProductsResponse
		if err := p.getJSON(ctx, productsURL, &resp); err != nil {
			return nil, err
		}
		for _, pr := range resp.Products {
			if pr.ProductID == "" || len(pr.LocalizedProperties) == 0 || pr.LocalizedProperties[0].ProductTitle == "" {
				continue
			}
			title := pr.LocalizedProperties[0].ProductTitle
			u := gamePassStoreURL(title, pr.ProductID)
			entries = append(entries, catalogEntry{Name: title, URL: &u})
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("gamecatalogs: game pass catalog empty — likely schema drift")
	}
	return entries, nil
}

// gamePassStoreURL mirrors the slugification xbox.com itself uses for
// product-page URLs: lowercase, non-alphanumerics collapsed to single
// dashes, leading/trailing dashes trimmed.
func gamePassStoreURL(title, productID string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteRune('-')
			dash = true
		}
	}
	slug := strings.TrimSuffix(b.String(), "-")
	return fmt.Sprintf("https://www.xbox.com/en-us/games/store/%s/%s", slug, productID)
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
