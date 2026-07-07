// Package tmdb implements the movies and TV MetadataProvider against the
// TMDB v3 API. One Client serves both media types; OMDb acts as an
// embedded best-effort rating enricher during Hydrate (omdb.go).
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const (
	defaultBaseURL      = "https://api.themoviedb.org/3"
	defaultOMDBBaseURL  = "https://www.omdbapi.com"
	defaultImageBaseURL = "https://image.tmdb.org/t/p"
)

type Client struct {
	apiKey       string
	omdbKey      string // empty ⇒ OMDb enrichment skipped
	baseURL      string
	omdbBaseURL  string
	imageBaseURL string
	httpClient   *http.Client
	logger       *slog.Logger
}

type Option func(*Client)

func WithBaseURL(u string) Option          { return func(c *Client) { c.baseURL = u } }
func WithOMDBBaseURL(u string) Option      { return func(c *Client) { c.omdbBaseURL = u } }
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }
func WithLogger(l *slog.Logger) Option     { return func(c *Client) { c.logger = l } }

func New(apiKey, omdbKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:       apiKey,
		omdbKey:      omdbKey,
		baseURL:      defaultBaseURL,
		omdbBaseURL:  defaultOMDBBaseURL,
		imageBaseURL: defaultImageBaseURL,
		httpClient:   http.DefaultClient,
		logger:       slog.Default(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Movies and TV return per-media-type views of the shared client.
func (c *Client) Movies() providers.MetadataProvider { return movieProvider{c} }
func (c *Client) TV() providers.MetadataProvider     { return tvProvider{c} }

type movieProvider struct{ c *Client }
type tvProvider struct{ c *Client }

func (p movieProvider) Search(ctx context.Context, q string) ([]providers.Candidate, error) {
	return p.c.search(ctx, "/search/movie", store.TypeMovie, q)
}

func (p movieProvider) Hydrate(ctx context.Context, id string) (*providers.ItemDetails, error) {
	return nil, fmt.Errorf("tmdb: hydrate not implemented")
}

func (p tvProvider) Search(ctx context.Context, q string) ([]providers.Candidate, error) {
	return p.c.search(ctx, "/search/tv", store.TypeTV, q)
}

func (p tvProvider) Hydrate(ctx context.Context, id string) (*providers.ItemDetails, error) {
	return nil, fmt.Errorf("tmdb: hydrate not implemented")
}

// TMDB movie and TV IDs are separate numeric namespaces, so provider_id
// carries the namespace: "movie:603", "tv:1396".
func providerID(mt store.MediaType, id int64) string {
	return fmt.Sprintf("%s:%d", mt, id)
}

func parseProviderID(mt store.MediaType, provID string) (int64, error) {
	prefix := string(mt) + ":"
	raw, ok := strings.CutPrefix(provID, prefix)
	if !ok {
		return 0, fmt.Errorf("tmdb: provider id %q lacks %q prefix", provID, prefix)
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("tmdb: provider id %q: %w", provID, err)
	}
	return id, nil
}

type searchResponse struct {
	Results []struct {
		ID           int64   `json:"id"`
		Title        string  `json:"title"` // movies
		Name         string  `json:"name"`  // tv
		ReleaseDate  string  `json:"release_date"`
		FirstAirDate string  `json:"first_air_date"`
		PosterPath   *string `json:"poster_path"`
		Overview     string  `json:"overview"`
	} `json:"results"`
}

func (c *Client) search(ctx context.Context, path string, mt store.MediaType, query string) ([]providers.Candidate, error) {
	var resp searchResponse
	if err := c.get(ctx, path, url.Values{"query": {query}}, &resp); err != nil {
		return nil, err
	}
	candidates := make([]providers.Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		title, date := r.Title, r.ReleaseDate
		if mt == store.TypeTV {
			title, date = r.Name, r.FirstAirDate
		}
		candidates = append(candidates, providers.Candidate{
			Provider:       "tmdb",
			ProviderID:     providerID(mt, r.ID),
			MediaType:      mt,
			Title:          title,
			Year:           yearOf(date),
			ThumbnailURL:   c.imageURL(r.PosterPath, "w185"),
			Disambiguation: truncate(r.Overview, 120),
		})
	}
	return candidates, nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, dst any) error {
	params.Set("api_key", c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb: %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb: %s returned %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) imageURL(posterPath *string, size string) *string {
	if posterPath == nil || *posterPath == "" {
		return nil
	}
	u := c.imageBaseURL + "/" + size + *posterPath
	return &u
}

func yearOf(date string) *int {
	if len(date) < 4 {
		return nil
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return nil
	}
	return &y
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
