# M2 — Metadata Provider Adapters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** `Search`/`Hydrate` working for all four media types behind a
`MetadataProvider` registry, exercised entirely by fixtures, plus
`cmd/probecheck` for manual live-shape verification.

**Architecture:** One `internal/providers` package defines `Candidate`,
`ItemDetails`, `Rating`, the `MetadataProvider` interface, the registry, and
rating normalization. Three adapter packages underneath it: `tmdb`
(movies+TV, one shared client exposed as two per-type providers, with OMDb
as an embedded best-effort rating enricher), `books` (Open Library
search/hydrate composed with a miss-tolerant Hardcover rating match), and
`igdb` (Twitch client-credentials token source with expiry caching). All
adapters take base-URL and `*http.Client` overrides so tests run against
`httptest.Server` instances serving `testdata/` fixtures.

**Tech Stack:** Go stdlib only (`net/http`, `encoding/json`, `log/slog`,
`httptest`). No new module dependencies.

## Global Constraints

- API keys in `config.toml` in the data dir — never env vars, never
  committed; never read `.env` files.
- Adapter tests run against `testdata/` fixtures, never live APIs.
- Official APIs only (unofficial game-catalog endpoints are M3, not here).
- Provider failures degrade, never cascade; enrichers are best-effort.
- Meaningful, high-value tests over coverage maximization.
- Conventional Commits; no AI attribution anywhere.
- Rating scores normalized to 0–100 at ingest; `display` keeps the
  original scale string.

## Design Decisions (flagged for sign-off)

1. **TMDB `provider_id` is namespaced**: `movie:603` / `tv:1396`. TMDB
   movie and TV IDs are separate numeric namespaces that can collide, and
   the schema's `UNIQUE(provider, provider_id)` uses a single `tmdb`
   provider value for both types, so the ID must carry the namespace.
2. **`ItemDetails` carries no availability.** The spec allows Hydrate to
   return availability "where the provider knows it", but the TMDB
   watch-providers call belongs to M3's `tmdbWatch` enricher. M2 keeps
   Hydrate to metadata + ratings; nothing is half-built across milestones.
3. **Fixtures are hand-authored from documented API response shapes**, not
   captured live (no API keys exist in this environment, and the global
   constraint forbids live calls in tests regardless). `cmd/probecheck`
   exists precisely to verify real shapes once the user supplies keys; any
   drift found there gets folded back into the fixtures.
4. **Registration policy:** `books` registers unconditionally (Open
   Library needs no key; Hardcover enrichment activates only when its key
   is set). `tmdb` registers movie+TV only when `tmdb_key` is set; `igdb`
   only when both IGDB client ID and secret are set. Registry wiring lives
   in `internal/providers/setup` (a separate package, because
   `providers` ← adapters would otherwise be an import cycle).
5. **Disambiguation line content per provider:** TMDB = overview truncated
   to 120 runes (director would cost an extra credits call per result);
   books = author names; IGDB = platform abbreviations.
6. **Hardcover matches by exact title, not ISBN.** The spec says
   "ISBN/title match", but Open Library work records carry no ISBNs
   (only editions do, an extra call per hydrate). Exact-title match
   against Hardcover, picking the most-tracked book on ties; a miss is
   silent by design. ISBN matching can be added later if title proves
   too lossy.

## File Structure

```
internal/providers/
  providers.go        Candidate, Rating, ItemDetails, MetadataProvider
  registry.go         Registry (media_type → provider)
  ratings.go          NormalizeScale, ParseDisplay
  tmdb/
    tmdb.go           Client, Movies()/TV() providers, search+hydrate
    omdb.go           embedded OMDb rating enricher
    testdata/*.json
  books/
    books.go          Open Library search/hydrate
    hardcover.go      Hardcover rating enricher
    testdata/*.json
  igdb/
    igdb.go           Provider, search+hydrate
    token.go          Twitch client-credentials token source
    testdata/*.json
  setup/
    setup.go          FromConfig → *providers.Registry
cmd/probecheck/
  main.go             manual live-shape verification utility
```

---

### Task 1: Provider Core — Types, Registry, Rating Normalization

**Files:**
- Create: `internal/providers/providers.go`
- Create: `internal/providers/registry.go`
- Create: `internal/providers/ratings.go`
- Test: `internal/providers/ratings_test.go`
- Test: `internal/providers/registry_test.go`

**Interfaces:**
- Consumes: `store.MediaType` constants from M1.
- Produces: `providers.Candidate`, `providers.Rating`,
  `providers.ItemDetails`, `providers.MetadataProvider`,
  `providers.NewRegistry() *Registry`, `(*Registry).Register(mt, p)`,
  `(*Registry).Get(mt) (MetadataProvider, error)`,
  `providers.NormalizeScale(value, max float64) (int, error)`,
  `providers.ParseDisplay(display string) (int, error)`.
  Every adapter task depends on these exact names.

- [ ] **Step 1: Write the failing tests**

`internal/providers/ratings_test.go`:

```go
package providers

import "testing"

func TestNormalizeScale(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		max     float64
		want    int
		wantErr bool
	}{
		{"imdb 10-scale", 7.9, 10, 79, false},
		{"hardcover 5-scale", 4.28, 5, 86, false},
		{"igdb float", 91.234, 100, 91, false},
		{"floor", 0, 10, 0, false},
		{"ceiling", 100, 100, 100, false},
		{"rounds half up", 4.35, 5, 87, false},
		{"negative value", -1, 10, 0, true},
		{"value over max", 11, 10, 0, true},
		{"zero max", 5, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeScale(tt.value, tt.max)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeScale(%v, %v) error = %v, wantErr %v", tt.value, tt.max, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NormalizeScale(%v, %v) = %d, want %d", tt.value, tt.max, got, tt.want)
			}
		})
	}
}

func TestParseDisplay(t *testing.T) {
	tests := []struct {
		display string
		want    int
		wantErr bool
	}{
		{"7.9/10", 79, false},
		{"85%", 85, false},
		{"74/100", 74, false},
		{"4.2/5", 84, false},
		{" 8.7/10 ", 87, false},
		{"12/10", 0, true},
		{"N/A/10", 0, true},
		{"eighty", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.display, func(t *testing.T) {
			got, err := ParseDisplay(tt.display)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDisplay(%q) error = %v, wantErr %v", tt.display, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseDisplay(%q) = %d, want %d", tt.display, got, tt.want)
			}
		})
	}
}
```

`internal/providers/registry_test.go`:

```go
package providers

import (
	"context"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

type stubProvider struct{ name string }

func (s stubProvider) Search(ctx context.Context, q string) ([]Candidate, error) {
	return nil, nil
}

func (s stubProvider) Hydrate(ctx context.Context, id string) (*ItemDetails, error) {
	return nil, nil
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	movie := stubProvider{name: "movie"}
	r.Register(store.TypeMovie, movie)

	got, err := r.Get(store.TypeMovie)
	if err != nil {
		t.Fatalf("Get(movie) error = %v", err)
	}
	if got.(stubProvider).name != "movie" {
		t.Errorf("Get(movie) returned wrong provider")
	}

	if _, err := r.Get(store.TypeGame); err == nil {
		t.Error("Get(game) on empty registration should error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/ 2>&1 | head -20`
Expected: FAIL — compile errors, `NormalizeScale`, `Candidate`, etc. undefined.

- [ ] **Step 3: Write the implementation**

`internal/providers/providers.go`:

```go
// Package providers defines the metadata-provider abstraction: search
// candidates, hydrated item details, rating normalization, and the
// media_type → provider registry. Adapter implementations live in
// subpackages (tmdb, books, igdb).
package providers

import (
	"context"

	"github.com/varigg/mediatracker/internal/store"
)

// Candidate is one search result rendered in the add-flow picker.
type Candidate struct {
	Provider       string // 'tmdb' | 'openlibrary' | 'igdb'
	ProviderID     string
	MediaType      store.MediaType
	Title          string
	Year           *int
	ThumbnailURL   *string
	Disambiguation string // overview / authors / platforms
}

// Rating is a normalized rating from one source, not yet bound to an item.
type Rating struct {
	Source  string
	Score   int // 0–100
	Display string // original scale, e.g. "7.9/10"
	URL     *string
}

// ItemDetails is the result of Hydrate: everything needed to persist a
// media_items row plus its ratings rows. Availability is produced by the
// M3 AvailabilityProvider enrichers, never here.
type ItemDetails struct {
	MediaType   store.MediaType
	Title       string
	ReleaseYear *int
	Genres      []string
	CoverURL    *string // remote URL; download is the M4 add flow's job
	Provider    string
	ProviderID  string
	Metadata    map[string]any // type-specific residue, serialized at persist
	Ratings     []Rating
}

type MetadataProvider interface {
	Search(ctx context.Context, query string) ([]Candidate, error)
	Hydrate(ctx context.Context, providerID string) (*ItemDetails, error)
}
```

`internal/providers/registry.go`:

```go
package providers

import (
	"fmt"

	"github.com/varigg/mediatracker/internal/store"
)

// Registry maps a media type to its MetadataProvider. The HTTP layer
// resolves providers exclusively through it and never names an upstream.
type Registry struct {
	m map[store.MediaType]MetadataProvider
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[store.MediaType]MetadataProvider)}
}

func (r *Registry) Register(mt store.MediaType, p MetadataProvider) {
	r.m[mt] = p
}

// Get returns the provider for mt. The error names the missing type so
// the HTTP layer can render it as a 4xx.
func (r *Registry) Get(mt store.MediaType) (MetadataProvider, error) {
	p, ok := r.m[mt]
	if !ok {
		return nil, fmt.Errorf("no metadata provider registered for media type %q", mt)
	}
	return p, nil
}
```

`internal/providers/ratings.go`:

```go
package providers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// NormalizeScale maps value on [0, max] to an integer 0–100 score.
func NormalizeScale(value, max float64) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("rating scale max %v must be positive", max)
	}
	if value < 0 || value > max {
		return 0, fmt.Errorf("rating %v out of range [0, %v]", value, max)
	}
	return int(math.Round(value / max * 100)), nil
}

// ParseDisplay parses rating strings as OMDb renders them — "7.9/10",
// "85%", "74/100" — and returns the normalized 0–100 score.
func ParseDisplay(display string) (int, error) {
	s := strings.TrimSpace(display)
	if strings.HasSuffix(s, "%") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return 0, fmt.Errorf("parse rating %q: %w", display, err)
		}
		return NormalizeScale(v, 100)
	}
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return 0, fmt.Errorf("parse rating %q: unrecognized format", display)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("parse rating %q: %w", display, err)
	}
	max, err := strconv.ParseFloat(strings.TrimSpace(den), 64)
	if err != nil {
		return 0, fmt.Errorf("parse rating %q: %w", display, err)
	}
	return NormalizeScale(v, max)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/ -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/
git commit -m "feat: add provider core types, registry, rating normalization"
```

---

### Task 2: TMDB Adapter — Client & Search

**Files:**
- Create: `internal/providers/tmdb/tmdb.go`
- Create: `internal/providers/tmdb/testdata/search_movie.json`
- Create: `internal/providers/tmdb/testdata/search_tv.json`
- Test: `internal/providers/tmdb/tmdb_test.go`

**Interfaces:**
- Consumes: `providers.Candidate`, `providers.MetadataProvider`,
  `store.TypeMovie`/`store.TypeTV` (Task 1 / M1).
- Produces: `tmdb.New(apiKey, omdbKey string, opts ...Option) *Client`;
  options `WithBaseURL`, `WithOMDBBaseURL`, `WithHTTPClient`, `WithLogger`;
  `(*Client).Movies() providers.MetadataProvider`,
  `(*Client).TV() providers.MetadataProvider`. `Hydrate` is a stub in this
  task (explicit "not implemented" error) — Task 3 replaces it.

- [ ] **Step 1: Write the failing test**

`internal/providers/tmdb/tmdb_test.go`:

```go
package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Errorf("read fixture %s: %v", name, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func newTestClient(t *testing.T, mux *http.ServeMux, omdbKey string) *Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New("test-key", omdbKey,
		WithBaseURL(srv.URL),
		WithOMDBBaseURL(srv.URL+"/omdb"),
	)
}

func TestSearchMovies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", serveFixture(t, "search_movie.json"))
	c := newTestClient(t, mux, "")

	got, err := c.Movies().Search(context.Background(), "the matrix")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	first := got[0]
	if first.Provider != "tmdb" || first.ProviderID != "movie:603" {
		t.Errorf("candidate identity = %s/%s, want tmdb/movie:603", first.Provider, first.ProviderID)
	}
	if first.MediaType != store.TypeMovie || first.Title != "The Matrix" {
		t.Errorf("candidate = %+v", first)
	}
	if first.Year == nil || *first.Year != 1999 {
		t.Errorf("Year = %v, want 1999", first.Year)
	}
	want := "https://image.tmdb.org/t/p/w185/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg"
	if first.ThumbnailURL == nil || *first.ThumbnailURL != want {
		t.Errorf("ThumbnailURL = %v, want %s", first.ThumbnailURL, want)
	}
	if n := len([]rune(first.Disambiguation)); n > 120 {
		t.Errorf("Disambiguation not truncated: %d runes", n)
	}
	if got[1].ThumbnailURL != nil {
		t.Errorf("missing poster should yield nil ThumbnailURL, got %v", *got[1].ThumbnailURL)
	}
}

func TestSearchTV(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", serveFixture(t, "search_tv.json"))
	c := newTestClient(t, mux, "")

	got, err := c.TV().Search(context.Background(), "breaking bad")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	cand := got[0]
	if cand.ProviderID != "tv:1396" || cand.Title != "Breaking Bad" || cand.MediaType != store.TypeTV {
		t.Errorf("candidate = %+v", cand)
	}
	if cand.Year == nil || *cand.Year != 2008 {
		t.Errorf("Year = %v, want 2008", cand.Year)
	}
}

func TestSearchUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := newTestClient(t, mux, "")
	if _, err := c.Movies().Search(context.Background(), "x"); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/tmdb/testdata/search_movie.json` (second result
deliberately has `poster_path: null` — the missing-poster degenerate case):

```json
{
  "page": 1,
  "results": [
    {
      "id": 603,
      "title": "The Matrix",
      "release_date": "1999-03-31",
      "poster_path": "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg",
      "overview": "Set in the 22nd century, The Matrix tells the story of a computer hacker who joins a group of underground insurgents fighting the vast and powerful computers who now rule the earth."
    },
    {
      "id": 604,
      "title": "The Matrix Reloaded",
      "release_date": "2003-05-15",
      "poster_path": null,
      "overview": "Six months after the events depicted in The Matrix."
    }
  ],
  "total_pages": 1,
  "total_results": 2
}
```

`internal/providers/tmdb/testdata/search_tv.json`:

```json
{
  "page": 1,
  "results": [
    {
      "id": 1396,
      "name": "Breaking Bad",
      "first_air_date": "2008-01-20",
      "poster_path": "/ztkUQFLlC19CCMYHW9o1zWhJRNq.jpg",
      "overview": "Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer and given a prognosis of only two years left to live."
    }
  ],
  "total_pages": 1,
  "total_results": 1
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/tmdb/ 2>&1 | head -20`
Expected: FAIL — package does not exist / `New` undefined.

- [ ] **Step 4: Write the implementation**

`internal/providers/tmdb/tmdb.go`:

```go
// Package tmdb implements the movies and TV MetadataProvider against the
// TMDB v3 API. One Client serves both media types; OMDb acts as an
// embedded best-effort rating enricher during Hydrate (omdb.go, Task 3).
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/tmdb/
git commit -m "feat: add TMDB adapter with movie and TV search"
```

---

### Task 3: TMDB Hydrate + OMDb Rating Enricher

**Files:**
- Create: `internal/providers/tmdb/omdb.go`
- Modify: `internal/providers/tmdb/tmdb.go` (replace the two `Hydrate`
  stubs with calls to `c.hydrate`; add `detailsResponse` + `hydrate`)
- Create: `internal/providers/tmdb/testdata/movie_details.json`
- Create: `internal/providers/tmdb/testdata/tv_details.json`
- Create: `internal/providers/tmdb/testdata/omdb_success.json`
- Create: `internal/providers/tmdb/testdata/omdb_miss.json`
- Test: `internal/providers/tmdb/hydrate_test.go`

**Interfaces:**
- Consumes: `providers.ItemDetails`, `providers.Rating`,
  `providers.ParseDisplay` (Task 1); `Client`/test helpers (Task 2).
- Produces: working `Hydrate` on both provider views. Rating sources
  emitted: `imdb`, `rotten_tomatoes`, `metacritic`. Metadata keys:
  `tmdb_id`, `overview`, `imdb_id`, `runtime_minutes` (movie),
  `seasons` (tv), `poster_url`.

- [ ] **Step 1: Write the failing tests**

`internal/providers/tmdb/hydrate_test.go`:

```go
package tmdb

import (
	"context"
	"net/http"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func TestHydrateMovieWithOMDbRatings(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/603", serveFixture(t, "movie_details.json"))
	mux.HandleFunc("/omdb/", serveFixture(t, "omdb_success.json"))
	c := newTestClient(t, mux, "omdb-test-key")

	got, err := c.Movies().Hydrate(context.Background(), "movie:603")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if got.MediaType != store.TypeMovie || got.Title != "The Matrix" {
		t.Errorf("details = %s %q", got.MediaType, got.Title)
	}
	if got.Provider != "tmdb" || got.ProviderID != "movie:603" {
		t.Errorf("identity = %s/%s", got.Provider, got.ProviderID)
	}
	if got.ReleaseYear == nil || *got.ReleaseYear != 1999 {
		t.Errorf("ReleaseYear = %v, want 1999", got.ReleaseYear)
	}
	wantGenres := []string{"Action", "Science Fiction"}
	if len(got.Genres) != 2 || got.Genres[0] != wantGenres[0] || got.Genres[1] != wantGenres[1] {
		t.Errorf("Genres = %v, want %v", got.Genres, wantGenres)
	}
	wantCover := "https://image.tmdb.org/t/p/w500/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg"
	if got.CoverURL == nil || *got.CoverURL != wantCover {
		t.Errorf("CoverURL = %v, want %s", got.CoverURL, wantCover)
	}
	if got.Metadata["imdb_id"] != "tt0133093" {
		t.Errorf("metadata imdb_id = %v", got.Metadata["imdb_id"])
	}
	if got.Metadata["runtime_minutes"] != 136 {
		t.Errorf("metadata runtime_minutes = %v", got.Metadata["runtime_minutes"])
	}

	if len(got.Ratings) != 3 {
		t.Fatalf("got %d ratings, want 3: %+v", len(got.Ratings), got.Ratings)
	}
	byScore := map[string]int{}
	for _, r := range got.Ratings {
		byScore[r.Source] = r.Score
		if r.Source == "imdb" {
			if r.URL == nil || *r.URL != "https://www.imdb.com/title/tt0133093/" {
				t.Errorf("imdb URL = %v", r.URL)
			}
			if r.Display != "8.7/10" {
				t.Errorf("imdb Display = %q, want 8.7/10", r.Display)
			}
		}
	}
	want := map[string]int{"imdb": 87, "rotten_tomatoes": 83, "metacritic": 73}
	for source, score := range want {
		if byScore[source] != score {
			t.Errorf("%s score = %d, want %d", source, byScore[source], score)
		}
	}
}

func TestHydrateTVWithOMDbMiss(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/1396", serveFixture(t, "tv_details.json"))
	mux.HandleFunc("/omdb/", serveFixture(t, "omdb_miss.json"))
	c := newTestClient(t, mux, "omdb-test-key")

	got, err := c.TV().Hydrate(context.Background(), "tv:1396")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if got.Title != "Breaking Bad" || got.MediaType != store.TypeTV {
		t.Errorf("details = %s %q", got.MediaType, got.Title)
	}
	if got.Metadata["seasons"] != 5 {
		t.Errorf("metadata seasons = %v, want 5", got.Metadata["seasons"])
	}
	if got.CoverURL != nil {
		t.Errorf("null poster_path should yield nil CoverURL, got %v", *got.CoverURL)
	}
	if len(got.Ratings) != 0 {
		t.Errorf("OMDb miss must degrade to no ratings, got %+v", got.Ratings)
	}
}

func TestHydrateOMDbDownDegrades(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/603", serveFixture(t, "movie_details.json"))
	mux.HandleFunc("/omdb/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newTestClient(t, mux, "omdb-test-key")

	got, err := c.Movies().Hydrate(context.Background(), "movie:603")
	if err != nil {
		t.Fatalf("OMDb 500 must not fail hydrate, got error %v", err)
	}
	if len(got.Ratings) != 0 {
		t.Errorf("OMDb 500 must degrade to no ratings, got %+v", got.Ratings)
	}
}

func TestHydrateWithoutOMDbKeySkipsEnricher(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/603", serveFixture(t, "movie_details.json"))
	mux.HandleFunc("/omdb/", func(w http.ResponseWriter, r *http.Request) {
		t.Error("OMDb must not be called when no key is configured")
	})
	c := newTestClient(t, mux, "")

	got, err := c.Movies().Hydrate(context.Background(), "movie:603")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if len(got.Ratings) != 0 {
		t.Errorf("ratings = %+v, want none", got.Ratings)
	}
}

func TestHydrateRejectsMalformedProviderID(t *testing.T) {
	c := newTestClient(t, http.NewServeMux(), "")
	for _, id := range []string{"603", "tv:1396", "movie:abc"} {
		if _, err := c.Movies().Hydrate(context.Background(), id); err == nil {
			t.Errorf("Hydrate(%q) via movie provider should error", id)
		}
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/tmdb/testdata/movie_details.json`:

```json
{
  "id": 603,
  "title": "The Matrix",
  "release_date": "1999-03-31",
  "genres": [
    {"id": 28, "name": "Action"},
    {"id": 878, "name": "Science Fiction"}
  ],
  "poster_path": "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg",
  "overview": "Set in the 22nd century, The Matrix tells the story of a computer hacker who joins a group of underground insurgents.",
  "runtime": 136,
  "imdb_id": "tt0133093",
  "external_ids": {"imdb_id": "tt0133093"}
}
```

`internal/providers/tmdb/testdata/tv_details.json` (null poster — the
missing-cover degenerate case; IMDB ID arrives only via `external_ids`,
as TMDB does for TV):

```json
{
  "id": 1396,
  "name": "Breaking Bad",
  "first_air_date": "2008-01-20",
  "genres": [{"id": 18, "name": "Drama"}],
  "poster_path": null,
  "overview": "Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer.",
  "number_of_seasons": 5,
  "external_ids": {"imdb_id": "tt0903747"}
}
```

`internal/providers/tmdb/testdata/omdb_success.json`:

```json
{
  "Title": "The Matrix",
  "Ratings": [
    {"Source": "Internet Movie Database", "Value": "8.7/10"},
    {"Source": "Rotten Tomatoes", "Value": "83%"},
    {"Source": "Metacritic", "Value": "73/100"}
  ],
  "Response": "True"
}
```

`internal/providers/tmdb/testdata/omdb_miss.json`:

```json
{"Response": "False", "Error": "Incorrect IMDb ID."}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/tmdb/ 2>&1 | head -20`
Expected: FAIL — `Hydrate` returns "not implemented".

- [ ] **Step 4: Write the implementation**

In `internal/providers/tmdb/tmdb.go`, replace both `Hydrate` stubs:

```go
func (p movieProvider) Hydrate(ctx context.Context, id string) (*providers.ItemDetails, error) {
	return p.c.hydrate(ctx, store.TypeMovie, id)
}
```

```go
func (p tvProvider) Hydrate(ctx context.Context, id string) (*providers.ItemDetails, error) {
	return p.c.hydrate(ctx, store.TypeTV, id)
}
```

Append to `internal/providers/tmdb/tmdb.go`:

```go
type detailsResponse struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"` // movies
	Name         string `json:"name"`  // tv
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
	Genres       []struct {
		Name string `json:"name"`
	} `json:"genres"`
	PosterPath      *string `json:"poster_path"`
	Overview        string  `json:"overview"`
	Runtime         *int    `json:"runtime"`           // movies
	NumberOfSeasons *int    `json:"number_of_seasons"` // tv
	IMDBID          string  `json:"imdb_id"`           // movies only
	ExternalIDs     struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"` // via append_to_response; how TV gets its IMDB ID
}

func (c *Client) hydrate(ctx context.Context, mt store.MediaType, provID string) (*providers.ItemDetails, error) {
	id, err := parseProviderID(mt, provID)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/movie/%d", id)
	if mt == store.TypeTV {
		path = fmt.Sprintf("/tv/%d", id)
	}
	var resp detailsResponse
	if err := c.get(ctx, path, url.Values{"append_to_response": {"external_ids"}}, &resp); err != nil {
		return nil, err
	}

	title, date := resp.Title, resp.ReleaseDate
	if mt == store.TypeTV {
		title, date = resp.Name, resp.FirstAirDate
	}
	genres := make([]string, 0, len(resp.Genres))
	for _, g := range resp.Genres {
		genres = append(genres, g.Name)
	}
	imdbID := resp.IMDBID
	if imdbID == "" {
		imdbID = resp.ExternalIDs.IMDBID
	}

	metadata := map[string]any{
		"tmdb_id":  resp.ID,
		"overview": resp.Overview,
	}
	if imdbID != "" {
		metadata["imdb_id"] = imdbID
	}
	if resp.Runtime != nil {
		metadata["runtime_minutes"] = *resp.Runtime
	}
	if resp.NumberOfSeasons != nil {
		metadata["seasons"] = *resp.NumberOfSeasons
	}
	coverURL := c.imageURL(resp.PosterPath, "w500")
	if coverURL != nil {
		metadata["poster_url"] = *coverURL
	}

	return &providers.ItemDetails{
		MediaType:   mt,
		Title:       title,
		ReleaseYear: yearOf(date),
		Genres:      genres,
		CoverURL:    coverURL,
		Provider:    "tmdb",
		ProviderID:  providerID(mt, resp.ID),
		Metadata:    metadata,
		Ratings:     c.omdbRatings(ctx, imdbID),
	}, nil
}
```

`internal/providers/tmdb/omdb.go`:

```go
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/varigg/mediatracker/internal/providers"
)

type omdbResponse struct {
	Response string `json:"Response"` // "True" | "False"
	Ratings  []struct {
		Source string `json:"Source"`
		Value  string `json:"Value"`
	} `json:"Ratings"`
}

var omdbSources = map[string]string{
	"Internet Movie Database": "imdb",
	"Rotten Tomatoes":         "rotten_tomatoes",
	"Metacritic":              "metacritic",
}

// omdbRatings is a best-effort enricher: every failure — no key, no IMDB
// ID, transport error, non-200, OMDb miss, unparsable value — degrades to
// no ratings and never fails the hydrate.
func (c *Client) omdbRatings(ctx context.Context, imdbID string) []providers.Rating {
	if c.omdbKey == "" || imdbID == "" {
		return nil
	}
	q := url.Values{"apikey": {c.omdbKey}, "i": {imdbID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.omdbBaseURL+"/?"+q.Encode(), nil)
	if err != nil {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "error", err)
		return nil
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "status", resp.Status)
		return nil
	}
	var body omdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "error", err)
		return nil
	}
	if body.Response != "True" {
		return nil // OMDb miss: metadata-only item, by design
	}
	var ratings []providers.Rating
	for _, r := range body.Ratings {
		source, ok := omdbSources[r.Source]
		if !ok {
			continue
		}
		score, err := providers.ParseDisplay(r.Value)
		if err != nil {
			c.logger.Warn("omdb rating unparsable", "imdb_id", imdbID, "source", r.Source, "value", r.Value)
			continue
		}
		rating := providers.Rating{Source: source, Score: score, Display: r.Value}
		if source == "imdb" {
			u := fmt.Sprintf("https://www.imdb.com/title/%s/", imdbID)
			rating.URL = &u
		}
		ratings = append(ratings, rating)
	}
	return ratings
}
```

Note: `TestHydrateMovieWithOMDbRatings` asserts
`got.Metadata["runtime_minutes"] != 136` against an `any` holding an
`int` — this compares `any(int)` to the untyped constant `136`, which
works because the fixture value is stored via `*resp.Runtime` (an `int`),
not via JSON round-trip (which would yield `float64`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/tmdb/
git commit -m "feat: add TMDB hydrate with OMDb rating enrichment"
```

---

### Task 4: Books Adapter — Open Library Search & Hydrate

**Files:**
- Create: `internal/providers/books/books.go`
- Create: `internal/providers/books/testdata/ol_search.json`
- Create: `internal/providers/books/testdata/ol_work.json`
- Create: `internal/providers/books/testdata/ol_author.json`
- Test: `internal/providers/books/books_test.go`

**Interfaces:**
- Consumes: `providers.Candidate`, `providers.ItemDetails`,
  `providers.MetadataProvider`, `store.TypeBook` (Task 1 / M1).
- Produces: `books.New(hardcoverKey string, opts ...Option) *Provider`
  satisfying `providers.MetadataProvider`; options
  `WithOpenLibraryBaseURL`, `WithCoversBaseURL`, `WithHardcoverURL`,
  `WithHTTPClient`, `WithLogger`. `provider` value is `openlibrary`;
  `provider_id` is the bare work ID (`OL262758W`). Hardcover enrichment
  is Task 5; this task's Hydrate returns no ratings.

- [ ] **Step 1: Write the failing tests**

`internal/providers/books/books_test.go`:

```go
package books

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Errorf("read fixture %s: %v", name, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func newTestProvider(t *testing.T, mux *http.ServeMux, hardcoverKey string) *Provider {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(hardcoverKey,
		WithOpenLibraryBaseURL(srv.URL),
		WithHardcoverURL(srv.URL+"/hardcover"),
	)
}

func TestSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search.json", serveFixture(t, "ol_search.json"))
	p := newTestProvider(t, mux, "")

	got, err := p.Search(context.Background(), "the hobbit")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	first := got[0]
	if first.Provider != "openlibrary" || first.ProviderID != "OL262758W" {
		t.Errorf("identity = %s/%s, want openlibrary/OL262758W", first.Provider, first.ProviderID)
	}
	if first.MediaType != store.TypeBook || first.Title != "The Hobbit" {
		t.Errorf("candidate = %+v", first)
	}
	if first.Year == nil || *first.Year != 1937 {
		t.Errorf("Year = %v, want 1937", first.Year)
	}
	if first.Disambiguation != "J.R.R. Tolkien" {
		t.Errorf("Disambiguation = %q", first.Disambiguation)
	}
	want := "https://covers.openlibrary.org/b/id/14625765-M.jpg"
	if first.ThumbnailURL == nil || *first.ThumbnailURL != want {
		t.Errorf("ThumbnailURL = %v, want %s", first.ThumbnailURL, want)
	}

	second := got[1]
	if second.ThumbnailURL != nil || second.Year != nil {
		t.Errorf("doc without cover_i/year must yield nils, got %+v", second)
	}
	if second.Disambiguation != "J.R.R. Tolkien, Douglas A. Anderson" {
		t.Errorf("Disambiguation = %q", second.Disambiguation)
	}
}

func TestHydrate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/works/OL262758W.json", serveFixture(t, "ol_work.json"))
	mux.HandleFunc("/authors/OL26320A.json", serveFixture(t, "ol_author.json"))
	p := newTestProvider(t, mux, "")

	got, err := p.Hydrate(context.Background(), "OL262758W")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if got.MediaType != store.TypeBook || got.Title != "The Hobbit" {
		t.Errorf("details = %s %q", got.MediaType, got.Title)
	}
	if got.Provider != "openlibrary" || got.ProviderID != "OL262758W" {
		t.Errorf("identity = %s/%s", got.Provider, got.ProviderID)
	}
	if got.ReleaseYear == nil || *got.ReleaseYear != 1937 {
		t.Errorf("ReleaseYear = %v, want 1937 (from \"September 21, 1937\")", got.ReleaseYear)
	}
	if len(got.Genres) != 6 || got.Genres[0] != "Fantasy" {
		t.Errorf("Genres = %v, want first 6 subjects", got.Genres)
	}
	wantCover := "https://covers.openlibrary.org/b/id/14625765-L.jpg"
	if got.CoverURL == nil || *got.CoverURL != wantCover {
		t.Errorf("CoverURL = %v, want %s", got.CoverURL, wantCover)
	}
	authors, ok := got.Metadata["authors"].([]string)
	if !ok || len(authors) != 1 || authors[0] != "J.R.R. Tolkien" {
		t.Errorf("metadata authors = %v", got.Metadata["authors"])
	}
	desc, ok := got.Metadata["description"].(string)
	if !ok || desc == "" {
		t.Errorf("metadata description = %v, want non-empty string from {value: ...} form", got.Metadata["description"])
	}
	if len(got.Ratings) != 0 {
		t.Errorf("ratings = %+v, want none before Hardcover enrichment", got.Ratings)
	}
}

func TestHydrateAuthorFetchFailureDegrades(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/works/OL262758W.json", serveFixture(t, "ol_work.json"))
	mux.HandleFunc("/authors/OL26320A.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	p := newTestProvider(t, mux, "")

	got, err := p.Hydrate(context.Background(), "OL262758W")
	if err != nil {
		t.Fatalf("author fetch failure must not fail hydrate, got %v", err)
	}
	if got.Title != "The Hobbit" {
		t.Errorf("Title = %q", got.Title)
	}
	if authors, ok := got.Metadata["authors"].([]string); ok && len(authors) != 0 {
		t.Errorf("authors = %v, want empty on fetch failure", authors)
	}
}

func TestSearchUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	p := newTestProvider(t, mux, "")
	if _, err := p.Search(context.Background(), "x"); err == nil {
		t.Fatal("expected error on 503, got nil")
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/books/testdata/ol_search.json` (second doc lacks
`cover_i` and `first_publish_year` — degenerate case):

```json
{
  "numFound": 2,
  "docs": [
    {
      "key": "/works/OL262758W",
      "title": "The Hobbit",
      "first_publish_year": 1937,
      "author_name": ["J.R.R. Tolkien"],
      "cover_i": 14625765
    },
    {
      "key": "/works/OL27482W",
      "title": "The Annotated Hobbit",
      "author_name": ["J.R.R. Tolkien", "Douglas A. Anderson"]
    }
  ]
}
```

`internal/providers/books/testdata/ol_work.json` (description in Open
Library's object form; seven subjects so the six-genre cap is exercised):

```json
{
  "title": "The Hobbit",
  "description": {
    "type": "/type/text",
    "value": "Bilbo Baggins, a respectable, well-to-do hobbit, lives comfortably in his hobbit-hole until the day the wandering wizard Gandalf chooses him to share in an adventure."
  },
  "covers": [14625765],
  "first_publish_date": "September 21, 1937",
  "subjects": ["Fantasy", "Fiction", "Dragons", "Wizards", "Hobbits", "Adventure", "Middle Earth (Imaginary place)"],
  "authors": [
    {"author": {"key": "/authors/OL26320A"}, "type": {"key": "/type/author_role"}}
  ]
}
```

`internal/providers/books/testdata/ol_author.json`:

```json
{"name": "J.R.R. Tolkien", "key": "/authors/OL26320A"}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/books/ 2>&1 | head -20`
Expected: FAIL — package does not exist / `New` undefined.

- [ ] **Step 4: Write the implementation**

`internal/providers/books/books.go`:

```go
// Package books implements the book MetadataProvider: Open Library for
// search and hydrate, composed with a miss-tolerant Hardcover community-
// rating match (hardcover.go, Task 5).
package books

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const (
	defaultOpenLibraryBaseURL = "https://openlibrary.org"
	defaultCoversBaseURL      = "https://covers.openlibrary.org"
	defaultHardcoverURL       = "https://api.hardcover.app/v1/graphql"

	maxGenres  = 6
	maxAuthors = 3
)

type Provider struct {
	hardcoverKey  string // empty ⇒ Hardcover enrichment skipped
	olBaseURL     string
	coversBaseURL string
	hardcoverURL  string
	httpClient    *http.Client
	logger        *slog.Logger
}

type Option func(*Provider)

func WithOpenLibraryBaseURL(u string) Option { return func(p *Provider) { p.olBaseURL = u } }
func WithCoversBaseURL(u string) Option      { return func(p *Provider) { p.coversBaseURL = u } }
func WithHardcoverURL(u string) Option       { return func(p *Provider) { p.hardcoverURL = u } }
func WithHTTPClient(h *http.Client) Option   { return func(p *Provider) { p.httpClient = h } }
func WithLogger(l *slog.Logger) Option       { return func(p *Provider) { p.logger = l } }

func New(hardcoverKey string, opts ...Option) *Provider {
	p := &Provider{
		hardcoverKey:  hardcoverKey,
		olBaseURL:     defaultOpenLibraryBaseURL,
		coversBaseURL: defaultCoversBaseURL,
		hardcoverURL:  defaultHardcoverURL,
		httpClient:    http.DefaultClient,
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type olSearchResponse struct {
	Docs []struct {
		Key              string   `json:"key"` // "/works/OL262758W"
		Title            string   `json:"title"`
		FirstPublishYear *int     `json:"first_publish_year"`
		AuthorName       []string `json:"author_name"`
		CoverI           *int64   `json:"cover_i"`
	} `json:"docs"`
}

func (p *Provider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	params := url.Values{
		"q":      {query},
		"fields": {"key,title,first_publish_year,author_name,cover_i"},
		"limit":  {"10"},
	}
	var resp olSearchResponse
	if err := p.getJSON(ctx, p.olBaseURL+"/search.json?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	candidates := make([]providers.Candidate, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		candidates = append(candidates, providers.Candidate{
			Provider:       "openlibrary",
			ProviderID:     strings.TrimPrefix(d.Key, "/works/"),
			MediaType:      store.TypeBook,
			Title:          d.Title,
			Year:           d.FirstPublishYear,
			ThumbnailURL:   p.coverURL(d.CoverI, "M"),
			Disambiguation: strings.Join(d.AuthorName, ", "),
		})
	}
	return candidates, nil
}

type olWork struct {
	Title            string          `json:"title"`
	Description      json.RawMessage `json:"description"` // string OR {"value": ...}
	Subjects         []string        `json:"subjects"`
	Covers           []int64         `json:"covers"`
	FirstPublishDate string          `json:"first_publish_date"`
	Authors          []struct {
		Author struct {
			Key string `json:"key"` // "/authors/OL26320A"
		} `json:"author"`
	} `json:"authors"`
}

func (p *Provider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	var work olWork
	if err := p.getJSON(ctx, p.olBaseURL+"/works/"+providerID+".json", &work); err != nil {
		return nil, err
	}

	genres := work.Subjects
	if len(genres) > maxGenres {
		genres = genres[:maxGenres]
	}
	var coverID *int64
	if len(work.Covers) > 0 {
		coverID = &work.Covers[0]
	}
	coverURL := p.coverURL(coverID, "L")
	authors := p.authorNames(ctx, work)

	metadata := map[string]any{
		"openlibrary_key": "/works/" + providerID,
		"authors":         authors,
	}
	if desc := decodeDescription(work.Description); desc != "" {
		metadata["description"] = desc
	}
	if coverURL != nil {
		metadata["cover_url"] = *coverURL
	}

	return &providers.ItemDetails{
		MediaType:   store.TypeBook,
		Title:       work.Title,
		ReleaseYear: yearOf(work.FirstPublishDate),
		Genres:      genres,
		CoverURL:    coverURL,
		Provider:    "openlibrary",
		ProviderID:  providerID,
		Metadata:    metadata,
		Ratings:     nil, // Hardcover enrichment lands in Task 5
	}, nil
}

// authorNames resolves author keys to names, best-effort: a failed
// author fetch is logged and skipped, never fails the hydrate.
func (p *Provider) authorNames(ctx context.Context, work olWork) []string {
	names := []string{}
	for i, a := range work.Authors {
		if i == maxAuthors {
			break
		}
		var author struct {
			Name string `json:"name"`
		}
		if err := p.getJSON(ctx, p.olBaseURL+a.Author.Key+".json", &author); err != nil {
			p.logger.Warn("open library author fetch failed", "key", a.Author.Key, "error", err)
			continue
		}
		if author.Name != "" {
			names = append(names, author.Name)
		}
	}
	return names
}

func (p *Provider) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openlibrary: %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openlibrary: %s returned %s", u, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("openlibrary: decode %s: %w", u, err)
	}
	return nil
}

func (p *Provider) coverURL(id *int64, size string) *string {
	if id == nil || *id <= 0 {
		return nil
	}
	u := fmt.Sprintf("%s/b/id/%d-%s.jpg", p.coversBaseURL, *id, size)
	return &u
}

// decodeDescription handles Open Library's two description shapes:
// a bare string or {"type": "/type/text", "value": "..."}.
func decodeDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Value
	}
	return ""
}

var yearRe = regexp.MustCompile(`\b\d{4}\b`)

// yearOf extracts a year from Open Library's free-form first_publish_date
// ("1937", "September 21, 1937").
func yearOf(date string) *int {
	m := yearRe.FindString(date)
	if m == "" {
		return nil
	}
	y, err := strconv.Atoi(m)
	if err != nil {
		return nil
	}
	return &y
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/books/
git commit -m "feat: add Open Library books adapter"
```

---

### Task 5: Hardcover Rating Enricher

**Files:**
- Create: `internal/providers/books/hardcover.go`
- Modify: `internal/providers/books/books.go` (Hydrate's `Ratings: nil`
  becomes `Ratings: p.hardcoverRating(ctx, work.Title)`)
- Create: `internal/providers/books/testdata/hardcover_match.json`
- Create: `internal/providers/books/testdata/hardcover_miss.json`
- Test: `internal/providers/books/hardcover_test.go`

**Interfaces:**
- Consumes: `providers.NormalizeScale`, `providers.Rating` (Task 1);
  `Provider`, `newTestProvider`, `serveFixture` (Task 4).
- Produces: rating source `hardcover` (0–5 scale → 0–100, display
  `"4.28/5"`, URL `https://hardcover.app/books/{slug}`). Design decision
  6 applies: exact-title match, miss-tolerant.

- [ ] **Step 1: Write the failing tests**

`internal/providers/books/hardcover_test.go`:

```go
package books

import (
	"context"
	"net/http"
	"testing"
)

func workAndAuthorRoutes(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("/works/OL262758W.json", serveFixture(t, "ol_work.json"))
	mux.HandleFunc("/authors/OL26320A.json", serveFixture(t, "ol_author.json"))
}

func TestHydrateWithHardcoverRating(t *testing.T) {
	mux := http.NewServeMux()
	workAndAuthorRoutes(t, mux)
	mux.HandleFunc("/hardcover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("hardcover called with %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer hc-test-key" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		serveFixture(t, "hardcover_match.json")(w, r)
	})
	p := newTestProvider(t, mux, "hc-test-key")

	got, err := p.Hydrate(context.Background(), "OL262758W")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if len(got.Ratings) != 1 {
		t.Fatalf("got %d ratings, want 1: %+v", len(got.Ratings), got.Ratings)
	}
	r := got.Ratings[0]
	if r.Source != "hardcover" || r.Score != 86 || r.Display != "4.28/5" {
		t.Errorf("rating = %+v, want hardcover/86/4.28-of-5", r)
	}
	if r.URL == nil || *r.URL != "https://hardcover.app/books/the-hobbit" {
		t.Errorf("URL = %v", r.URL)
	}
}

func TestHydrateHardcoverMiss(t *testing.T) {
	mux := http.NewServeMux()
	workAndAuthorRoutes(t, mux)
	mux.HandleFunc("/hardcover", serveFixture(t, "hardcover_miss.json"))
	p := newTestProvider(t, mux, "hc-test-key")

	got, err := p.Hydrate(context.Background(), "OL262758W")
	if err != nil {
		t.Fatalf("Hardcover miss must not fail hydrate, got %v", err)
	}
	if len(got.Ratings) != 0 {
		t.Errorf("ratings = %+v, want none on miss", got.Ratings)
	}
}

func TestHydrateHardcoverDownDegrades(t *testing.T) {
	mux := http.NewServeMux()
	workAndAuthorRoutes(t, mux)
	mux.HandleFunc("/hardcover", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	p := newTestProvider(t, mux, "hc-test-key")

	got, err := p.Hydrate(context.Background(), "OL262758W")
	if err != nil {
		t.Fatalf("Hardcover 500 must not fail hydrate, got %v", err)
	}
	if len(got.Ratings) != 0 {
		t.Errorf("ratings = %+v, want none on failure", got.Ratings)
	}
}

func TestHydrateWithoutHardcoverKeySkipsEnricher(t *testing.T) {
	mux := http.NewServeMux()
	workAndAuthorRoutes(t, mux)
	mux.HandleFunc("/hardcover", func(w http.ResponseWriter, r *http.Request) {
		t.Error("Hardcover must not be called when no key is configured")
	})
	p := newTestProvider(t, mux, "")

	if _, err := p.Hydrate(context.Background(), "OL262758W"); err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/books/testdata/hardcover_match.json`:

```json
{
  "data": {
    "books": [
      {"slug": "the-hobbit", "rating": 4.28, "ratings_count": 41234}
    ]
  }
}
```

`internal/providers/books/testdata/hardcover_miss.json`:

```json
{"data": {"books": []}}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/books/ 2>&1 | head -20`
Expected: FAIL — `hardcoverRating` undefined /
`TestHydrateWithHardcoverRating` gets 0 ratings.

- [ ] **Step 4: Write the implementation**

In `internal/providers/books/books.go`, change Hydrate's return:

```go
		Ratings:     p.hardcoverRating(ctx, work.Title),
```

`internal/providers/books/hardcover.go`:

```go
package books

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/varigg/mediatracker/internal/providers"
)

// Exact-title match; ties broken by popularity (design decision 6).
const hardcoverQuery = `query BookByTitle($title: citext!) {
  books(where: {title: {_eq: $title}}, order_by: {users_count: desc}, limit: 1) {
    slug
    rating
    ratings_count
  }
}`

type hardcoverResponse struct {
	Data struct {
		Books []struct {
			Slug         string  `json:"slug"`
			Rating       float64 `json:"rating"`
			RatingsCount int     `json:"ratings_count"`
		} `json:"books"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// hardcoverRating fetches the community rating for a title. Best-effort:
// no key, transport error, non-200, GraphQL error, no match, or a zero
// rating all degrade to no rating and never fail the hydrate.
func (p *Provider) hardcoverRating(ctx context.Context, title string) []providers.Rating {
	if p.hardcoverKey == "" || title == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"query":     hardcoverQuery,
		"variables": map[string]any{"title": title},
	})
	if err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.hardcoverURL, bytes.NewReader(payload))
	if err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.hardcoverKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		p.logger.Warn("hardcover enrichment failed", "title", title, "status", resp.Status)
		return nil
	}
	var body hardcoverResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	if len(body.Errors) > 0 {
		p.logger.Warn("hardcover enrichment failed", "title", title, "graphql_error", body.Errors[0].Message)
		return nil
	}
	if len(body.Data.Books) == 0 {
		return nil // miss: metadata-only item, by design
	}
	b := body.Data.Books[0]
	if b.Rating <= 0 {
		return nil
	}
	score, err := providers.NormalizeScale(b.Rating, 5)
	if err != nil {
		p.logger.Warn("hardcover rating out of range", "title", title, "rating", b.Rating)
		return nil
	}
	rating := providers.Rating{
		Source:  "hardcover",
		Score:   score,
		Display: fmt.Sprintf("%.2f/5", b.Rating),
	}
	if b.Slug != "" {
		u := "https://hardcover.app/books/" + b.Slug
		rating.URL = &u
	}
	return []providers.Rating{rating}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS (including Task 4's `TestHydrate`, which uses no key and
still expects zero ratings)

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/books/
git commit -m "feat: add Hardcover rating enrichment to books hydrate"
```

---

### Task 6: IGDB Adapter — Token Flow, Search & Hydrate

**Files:**
- Create: `internal/providers/igdb/token.go`
- Create: `internal/providers/igdb/igdb.go`
- Create: `internal/providers/igdb/testdata/igdb_search.json`
- Create: `internal/providers/igdb/testdata/igdb_game.json`
- Create: `internal/providers/igdb/testdata/igdb_game_norating.json`
- Test: `internal/providers/igdb/igdb_test.go`

**Interfaces:**
- Consumes: `providers.Candidate`, `providers.ItemDetails`,
  `providers.Rating`, `providers.NormalizeScale`, `store.TypeGame`.
- Produces: `igdb.New(clientID, clientSecret string, opts ...Option)
  *Provider` satisfying `providers.MetadataProvider`; options
  `WithBaseURL`, `WithTokenURL`, `WithHTTPClient`, `WithLogger`,
  `WithNow`. `provider` value is `igdb`; `provider_id` is the numeric
  IGDB game ID as a string. Rating sources: `igdb` (community),
  `igdb_critics` (aggregated) — both already 0–100 upstream. Metadata
  key `alternative_names` (`[]string`) feeds M3's game-name matcher.

- [ ] **Step 1: Write the failing tests**

`internal/providers/igdb/igdb_test.go`:

```go
package igdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/store"
)

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Errorf("read fixture %s: %v", name, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func tokenHandler(t *testing.T, calls *int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.Method != http.MethodPost {
			t.Errorf("token endpoint called with %s, want POST", r.Method)
		}
		fmt.Fprintf(w, `{"access_token": "test-token-%d", "expires_in": 5000, "token_type": "bearer"}`, *calls)
	}
}

func newTestProvider(t *testing.T, mux *http.ServeMux, opts ...Option) *Provider {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	all := append([]Option{
		WithBaseURL(srv.URL),
		WithTokenURL(srv.URL + "/token"),
	}, opts...)
	return New("test-client-id", "test-client-secret", all...)
}

func TestTokenCachedAcrossCalls(t *testing.T) {
	var tokenCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(t, &tokenCalls))
	mux.HandleFunc("/games", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Client-ID"); got != "test-client-id" {
			t.Errorf("Client-ID = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token-1" {
			t.Errorf("Authorization = %q", got)
		}
		serveFixture(t, "igdb_search.json")(w, r)
	})
	p := newTestProvider(t, mux)

	for i := 0; i < 2; i++ {
		if _, err := p.Search(context.Background(), "the witcher"); err != nil {
			t.Fatalf("Search %d error = %v", i, err)
		}
	}
	if tokenCalls != 1 {
		t.Errorf("token fetched %d times across two searches, want 1", tokenCalls)
	}
}

func TestTokenRefreshedAfterExpiry(t *testing.T) {
	var tokenCalls int
	current := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(t, &tokenCalls))
	mux.HandleFunc("/games", serveFixture(t, "igdb_search.json"))
	p := newTestProvider(t, mux, WithNow(func() time.Time { return current }))

	if _, err := p.Search(context.Background(), "x"); err != nil {
		t.Fatalf("first Search error = %v", err)
	}
	current = current.Add(5000 * time.Second) // past expires_in
	if _, err := p.Search(context.Background(), "x"); err != nil {
		t.Fatalf("second Search error = %v", err)
	}
	if tokenCalls != 2 {
		t.Errorf("token fetched %d times, want 2 (refresh after expiry)", tokenCalls)
	}
}

func TestTokenEndpointFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	p := newTestProvider(t, mux)
	if _, err := p.Search(context.Background(), "x"); err == nil {
		t.Fatal("expected error when token endpoint fails, got nil")
	}
}

func TestSearch(t *testing.T) {
	var tokenCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(t, &tokenCalls))
	mux.HandleFunc("/games", serveFixture(t, "igdb_search.json"))
	p := newTestProvider(t, mux)

	got, err := p.Search(context.Background(), "the witcher")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	first := got[0]
	if first.Provider != "igdb" || first.ProviderID != "1942" {
		t.Errorf("identity = %s/%s, want igdb/1942", first.Provider, first.ProviderID)
	}
	if first.MediaType != store.TypeGame || first.Title != "The Witcher 3: Wild Hunt" {
		t.Errorf("candidate = %+v", first)
	}
	if first.Year == nil || *first.Year != 2015 {
		t.Errorf("Year = %v, want 2015", first.Year)
	}
	want := "https://images.igdb.com/igdb/image/upload/t_cover_small/co1wyy.jpg"
	if first.ThumbnailURL == nil || *first.ThumbnailURL != want {
		t.Errorf("ThumbnailURL = %v, want %s", first.ThumbnailURL, want)
	}
	if first.Disambiguation != "PC, PS4" {
		t.Errorf("Disambiguation = %q, want platform list", first.Disambiguation)
	}
	if got[1].ThumbnailURL != nil {
		t.Errorf("game without cover must yield nil ThumbnailURL, got %v", *got[1].ThumbnailURL)
	}
}

func TestHydrate(t *testing.T) {
	var tokenCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(t, &tokenCalls))
	mux.HandleFunc("/games", serveFixture(t, "igdb_game.json"))
	p := newTestProvider(t, mux)

	got, err := p.Hydrate(context.Background(), "1942")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if got.MediaType != store.TypeGame || got.Title != "The Witcher 3: Wild Hunt" {
		t.Errorf("details = %s %q", got.MediaType, got.Title)
	}
	if got.Provider != "igdb" || got.ProviderID != "1942" {
		t.Errorf("identity = %s/%s", got.Provider, got.ProviderID)
	}
	if got.ReleaseYear == nil || *got.ReleaseYear != 2015 {
		t.Errorf("ReleaseYear = %v, want 2015", got.ReleaseYear)
	}
	if len(got.Genres) != 2 || got.Genres[0] != "Role-playing (RPG)" {
		t.Errorf("Genres = %v", got.Genres)
	}
	wantCover := "https://images.igdb.com/igdb/image/upload/t_cover_big/co1wyy.jpg"
	if got.CoverURL == nil || *got.CoverURL != wantCover {
		t.Errorf("CoverURL = %v, want %s", got.CoverURL, wantCover)
	}
	alts, ok := got.Metadata["alternative_names"].([]string)
	if !ok || len(alts) != 2 {
		t.Errorf("metadata alternative_names = %v, want 2 names", got.Metadata["alternative_names"])
	}

	if len(got.Ratings) != 2 {
		t.Fatalf("got %d ratings, want 2: %+v", len(got.Ratings), got.Ratings)
	}
	bySource := map[string]int{}
	for _, r := range got.Ratings {
		bySource[r.Source] = r.Score
		if r.Source == "igdb" {
			if r.Display != "92/100" {
				t.Errorf("igdb Display = %q, want 92/100", r.Display)
			}
			if r.URL == nil || *r.URL != "https://www.igdb.com/games/the-witcher-3-wild-hunt" {
				t.Errorf("igdb URL = %v", r.URL)
			}
		}
	}
	if bySource["igdb"] != 92 || bySource["igdb_critics"] != 91 {
		t.Errorf("scores = %v, want igdb=92 igdb_critics=91", bySource)
	}
}

func TestHydrateWithoutRatingsOrCover(t *testing.T) {
	var tokenCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(t, &tokenCalls))
	mux.HandleFunc("/games", serveFixture(t, "igdb_game_norating.json"))
	p := newTestProvider(t, mux)

	got, err := p.Hydrate(context.Background(), "119388")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if len(got.Ratings) != 0 {
		t.Errorf("ratings = %+v, want none", got.Ratings)
	}
	if got.CoverURL != nil {
		t.Errorf("CoverURL = %v, want nil", *got.CoverURL)
	}
	if got.ReleaseYear == nil || *got.ReleaseYear != 2020 {
		t.Errorf("ReleaseYear = %v, want 2020", got.ReleaseYear)
	}
}

func TestHydrateRejectsMalformedProviderID(t *testing.T) {
	p := newTestProvider(t, http.NewServeMux())
	if _, err := p.Hydrate(context.Background(), "not-a-number"); err == nil {
		t.Fatal("expected error for non-numeric provider id")
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/igdb/testdata/igdb_search.json` (IGDB returns a bare
array; second game deliberately lacks a cover):

```json
[
  {
    "id": 1942,
    "name": "The Witcher 3: Wild Hunt",
    "first_release_date": 1431993600,
    "cover": {"id": 89386, "image_id": "co1wyy"},
    "platforms": [
      {"id": 6, "abbreviation": "PC"},
      {"id": 48, "abbreviation": "PS4"}
    ]
  },
  {
    "id": 22439,
    "name": "The Witcher 3: Wild Hunt - Blood and Wine",
    "first_release_date": 1464652800,
    "platforms": [{"id": 6, "abbreviation": "PC"}]
  }
]
```

`internal/providers/igdb/testdata/igdb_game.json`:

```json
[
  {
    "id": 1942,
    "name": "The Witcher 3: Wild Hunt",
    "first_release_date": 1431993600,
    "summary": "The Witcher 3: Wild Hunt is a story-driven, next-generation open world role-playing game.",
    "url": "https://www.igdb.com/games/the-witcher-3-wild-hunt",
    "rating": 92.3456,
    "rating_count": 2801,
    "aggregated_rating": 91.2,
    "cover": {"id": 89386, "image_id": "co1wyy"},
    "genres": [
      {"id": 12, "name": "Role-playing (RPG)"},
      {"id": 31, "name": "Adventure"}
    ],
    "alternative_names": [
      {"id": 1, "name": "TW3"},
      {"id": 2, "name": "Wiedźmin 3: Dziki Gon"}
    ]
  }
]
```

`internal/providers/igdb/testdata/igdb_game_norating.json` (absent
ratings and cover — the degenerate case):

```json
[
  {
    "id": 119388,
    "name": "Obscure Indie Game",
    "first_release_date": 1600000000,
    "summary": "A quiet game nobody has rated yet.",
    "url": "https://www.igdb.com/games/obscure-indie-game",
    "genres": [{"id": 32, "name": "Indie"}]
  }
]
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/igdb/ 2>&1 | head -20`
Expected: FAIL — package does not exist / `New` undefined.

- [ ] **Step 4: Write the implementation**

`internal/providers/igdb/token.go`:

```go
package igdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultTokenURL = "https://id.twitch.tv/oauth2/token"

// tokenSource caches a Twitch client-credentials token and refreshes it
// one minute before expiry.
type tokenSource struct {
	clientID     string
	clientSecret string
	tokenURL     string
	httpClient   *http.Client
	now          func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (t *tokenSource) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.now().Before(t.expiresAt.Add(-time.Minute)) {
		return t.token, nil
	}
	params := url.Values{
		"client_id":     {t.clientID},
		"client_secret": {t.clientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("igdb: token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("igdb: token endpoint returned %s", resp.Status)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("igdb: decode token: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("igdb: token endpoint returned empty access_token")
	}
	t.token = body.AccessToken
	t.expiresAt = t.now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return t.token, nil
}
```

`internal/providers/igdb/igdb.go`:

```go
// Package igdb implements the game MetadataProvider against the IGDB v4
// API, authenticating via the Twitch client-credentials flow (token.go).
package igdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const (
	defaultBaseURL  = "https://api.igdb.com/v4"
	defaultImageURL = "https://images.igdb.com/igdb/image/upload"
)

type Provider struct {
	clientID   string
	baseURL    string
	imageURL   string
	httpClient *http.Client
	logger     *slog.Logger
	tokens     *tokenSource
}

type Option func(*Provider)

func WithBaseURL(u string) Option  { return func(p *Provider) { p.baseURL = u } }
func WithTokenURL(u string) Option { return func(p *Provider) { p.tokens.tokenURL = u } }
func WithHTTPClient(h *http.Client) Option {
	return func(p *Provider) { p.httpClient = h; p.tokens.httpClient = h }
}
func WithLogger(l *slog.Logger) Option    { return func(p *Provider) { p.logger = l } }
func WithNow(now func() time.Time) Option { return func(p *Provider) { p.tokens.now = now } }

func New(clientID, clientSecret string, opts ...Option) *Provider {
	p := &Provider{
		clientID:   clientID,
		baseURL:    defaultBaseURL,
		imageURL:   defaultImageURL,
		httpClient: http.DefaultClient,
		logger:     slog.Default(),
		tokens: &tokenSource{
			clientID:     clientID,
			clientSecret: clientSecret,
			tokenURL:     defaultTokenURL,
			httpClient:   http.DefaultClient,
			now:          time.Now,
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type game struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	FirstReleaseDate int64   `json:"first_release_date"` // unix seconds
	Summary          string  `json:"summary"`
	URL              string  `json:"url"`
	Rating           float64 `json:"rating"`            // community, 0–100
	RatingCount      int     `json:"rating_count"`
	AggregatedRating float64 `json:"aggregated_rating"` // critics, 0–100
	Cover            struct {
		ImageID string `json:"image_id"`
	} `json:"cover"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Platforms []struct {
		Abbreviation string `json:"abbreviation"`
	} `json:"platforms"`
	AlternativeNames []struct {
		Name string `json:"name"`
	} `json:"alternative_names"`
}

func (p *Provider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	q := strings.ReplaceAll(query, `"`, `\"`)
	body := fmt.Sprintf(`search "%s"; fields name,first_release_date,cover.image_id,platforms.abbreviation; limit 10;`, q)
	var games []game
	if err := p.query(ctx, body, &games); err != nil {
		return nil, err
	}
	candidates := make([]providers.Candidate, 0, len(games))
	for _, g := range games {
		var platforms []string
		for _, pl := range g.Platforms {
			if pl.Abbreviation != "" {
				platforms = append(platforms, pl.Abbreviation)
			}
		}
		candidates = append(candidates, providers.Candidate{
			Provider:       "igdb",
			ProviderID:     strconv.FormatInt(g.ID, 10),
			MediaType:      store.TypeGame,
			Title:          g.Name,
			Year:           yearOf(g.FirstReleaseDate),
			ThumbnailURL:   p.coverURL(g.Cover.ImageID, "t_cover_small"),
			Disambiguation: strings.Join(platforms, ", "),
		})
	}
	return candidates, nil
}

func (p *Provider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("igdb: provider id %q: %w", providerID, err)
	}
	body := fmt.Sprintf(`fields name,first_release_date,summary,url,rating,rating_count,aggregated_rating,cover.image_id,genres.name,alternative_names.name; where id = %d;`, id)
	var games []game
	if err := p.query(ctx, body, &games); err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("igdb: game %d not found", id)
	}
	g := games[0]

	genres := make([]string, 0, len(g.Genres))
	for _, ge := range g.Genres {
		genres = append(genres, ge.Name)
	}
	altNames := make([]string, 0, len(g.AlternativeNames))
	for _, a := range g.AlternativeNames {
		altNames = append(altNames, a.Name)
	}
	coverURL := p.coverURL(g.Cover.ImageID, "t_cover_big")

	metadata := map[string]any{
		"igdb_id":           g.ID,
		"summary":           g.Summary,
		"alternative_names": altNames, // M3's game-name matcher consumes these
	}
	if g.URL != "" {
		metadata["igdb_url"] = g.URL
	}
	if coverURL != nil {
		metadata["cover_url"] = *coverURL
	}

	var ratings []providers.Rating
	if g.Rating > 0 {
		if score, err := providers.NormalizeScale(g.Rating, 100); err == nil {
			rating := providers.Rating{
				Source:  "igdb",
				Score:   score,
				Display: fmt.Sprintf("%.0f/100", g.Rating),
			}
			if g.URL != "" {
				u := g.URL
				rating.URL = &u
			}
			ratings = append(ratings, rating)
		}
	}
	if g.AggregatedRating > 0 {
		if score, err := providers.NormalizeScale(g.AggregatedRating, 100); err == nil {
			ratings = append(ratings, providers.Rating{
				Source:  "igdb_critics",
				Score:   score,
				Display: fmt.Sprintf("%.0f/100", g.AggregatedRating),
			})
		}
	}

	return &providers.ItemDetails{
		MediaType:   store.TypeGame,
		Title:       g.Name,
		ReleaseYear: yearOf(g.FirstReleaseDate),
		Genres:      genres,
		CoverURL:    coverURL,
		Provider:    "igdb",
		ProviderID:  strconv.FormatInt(g.ID, 10),
		Metadata:    metadata,
		Ratings:     ratings,
	}, nil
}

// query POSTs an Apicalypse body to /games with Twitch auth headers.
func (p *Provider) query(ctx context.Context, body string, dst any) error {
	token, err := p.tokens.get(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/games", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Client-ID", p.clientID)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("igdb: games query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("igdb: games query returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("igdb: decode games response: %w", err)
	}
	return nil
}

func (p *Provider) coverURL(imageID, size string) *string {
	if imageID == "" {
		return nil
	}
	u := fmt.Sprintf("%s/%s/%s.jpg", p.imageURL, size, imageID)
	return &u
}

func yearOf(unix int64) *int {
	if unix <= 0 {
		return nil
	}
	y := time.Unix(unix, 0).UTC().Year()
	return &y
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/igdb/
git commit -m "feat: add IGDB adapter with Twitch token flow"
```

---

### Task 7: Registry Wiring from Config + cmd/probecheck

**Files:**
- Create: `internal/providers/setup/setup.go`
- Create: `cmd/probecheck/main.go`
- Test: `internal/providers/setup/setup_test.go`

**Interfaces:**
- Consumes: `config.Providers` (M1), `providers.NewRegistry`,
  `tmdb.New`, `books.New`, `igdb.New` (Tasks 1–6).
- Produces: `setup.FromConfig(p config.Providers, logger *slog.Logger)
  *providers.Registry` — the single wiring point M4's add flow and the
  server will reuse. `cmd/probecheck` has no tests by design (spec §6:
  manual live utility, never a CI dependency). **Do not run probecheck
  during plan execution** — it exists for the user to fire against live
  providers once keys are configured; even the keyless books probe would
  hit Open Library live.

- [ ] **Step 1: Write the failing test**

`internal/providers/setup/setup_test.go`:

```go
package setup

import (
	"log/slog"
	"testing"

	"github.com/varigg/mediatracker/internal/config"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/setup/ 2>&1 | head -20`
Expected: FAIL — package does not exist / `FromConfig` undefined.

- [ ] **Step 3: Write the implementation**

`internal/providers/setup/setup.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 5: Write cmd/probecheck**

`cmd/probecheck/main.go`:

```go
// probecheck fires one canned query per configured live provider and
// prints the resulting shapes. Manual utility for verifying fixtures
// against reality — never a CI dependency, never run by tests.
package main

import (
	"context"
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

	exitCode := 0
	for _, probe := range probes {
		fmt.Printf("== %s: %q\n", probe.mediaType, probe.query)
		p, err := registry.Get(probe.mediaType)
		if err != nil {
			fmt.Println("   skipped: not configured")
			continue
		}
		if err := runProbe(p, probe.query); err != nil {
			fmt.Printf("   FAILED: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func runProbe(p providers.MetadataProvider, query string) error {
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
```

(`defaultDataDir` intentionally duplicates `cmd/mediatracker`'s helper —
two ~10-line main-package functions; extracting a shared package for it
is not yet warranted.)

- [ ] **Step 6: Verify probecheck compiles (do not run it)**

Run: `go build ./... && go vet ./...`
Expected: clean exit, `probecheck` binary buildable.

- [ ] **Step 7: Full-suite verification, format, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/setup/ cmd/probecheck/ .gitignore
git commit -m "feat: wire provider registry from config, add probecheck utility"
```

(If `go build` dropped a `probecheck` binary in the repo root, add
`probecheck` to `.gitignore` alongside `mediatracker` first.)

---

## Self-Review Notes

Milestone scope covered task-by-task: interface + registry → T1;
tmdb with embedded omdb enricher → T2/T3; books = Open Library +
Hardcover, miss-tolerant → T4/T5; igdb with Twitch client-credentials
flow → T6; rating normalization to 0–100 with original display → T1
(consumed by T3/T5/T6); fixture capture incl. degenerate cases —
missing poster (T2), OMDb miss + enricher-down degradation (T3),
missing cover_i/year (T4), Hardcover miss + down (T5), absent
ratings/cover (T6); `cmd/probecheck` → T7. Key tests all present:
parse/normalize per fixture, hydrate-succeeds-with-gaps degradation,
normalization table across source scales (10-point, percent, 100-point,
5-point).

Type-consistency check: adapters emit `providers.Rating` (no ItemID) —
distinct from `store.Rating`; the M4 add flow maps one to the other.
`newTestClient`/`newTestProvider`/`serveFixture` helper names are
consistent within each package. Metadata `any` values that tests compare
against untyped constants are stored as `int` (not JSON-round-tripped),
so the comparisons hold.

## Execution Notes

Work in a `.worktrees/m2-metadata-providers` worktree branched from
`main`; fast-forward merge back after all tasks are green, then remove
the worktree and branch (per M1 precedent). Verification commands avoid
piping `go test` through `tail`/`head` before commits; where output is
truncated for reading, `set -o pipefail` guards the exit status.
