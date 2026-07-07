# M3 — Availability & Ownership Enrichers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Availability rows produced for all three sources — TMDB watch
providers, Game Pass/PS+ catalog snapshots, Steam ownership — from
cycle-cached data, with the game-name matcher working against snapshot
fixtures.

**Architecture:** The `providers` package gains an
`AvailabilityProvider` interface (per-item `Refresh`) and an optional
`CycleSyncer` interface for once-per-cycle upstream fetches. `tmdbWatch`
is a per-item enricher on the existing TMDB client. `gamecatalogs` is the
quarantined unofficial adapter: paginated-fetch-tolerant catalog sync
behind a per-catalog circuit breaker, normalized snapshots persisted in
`catalogs/` and retained on failure, local matching via a shared name
normalizer (`internal/providers/names`). `steam` syncs the official
owned-games list to the same snapshot format and matches by IGDB-supplied
Steam app ID with normalized-name fallback.

**Tech Stack:** Go stdlib only. Fixtures + `httptest`, real temp dirs for
snapshot persistence.

## Global Constraints

- Official APIs only, **except** Game Pass / PS+ catalogs — unofficial,
  quarantined in `gamecatalogs`, circuit-broken, aggressive timeouts.
- API keys in `config.toml` in the data dir — never env vars, never
  committed; never read `.env` files.
- Adapter tests run against `testdata/` fixtures, never live APIs.
- Provider failures degrade, never cascade; catalog snapshots are
  retained on fetch failure — stale beats none.
- All state under one data dir; catalog snapshots live in `catalogs/`.
- Conventional Commits; no AI attribution anywhere.

## Design Decisions (flagged for sign-off)

1. **Interface split.** `AvailabilityProvider.Refresh(ctx,
   *store.MediaItem) ([]providers.Availability, error)` where
   `providers.Availability{ServiceSlug, Kind, URL}` is a lightweight fact
   — `item_id`, `first_seen_at`, `fetched_at` are persistence concerns
   the M4 ingest layer adds via `store.UpsertAvailability`. Providers
   needing a once-per-cycle upstream fetch additionally implement
   `CycleSyncer{SyncCycle(ctx) error}`; the M4 orchestrator calls
   `SyncCycle` on each syncer at cycle start, so per-item `Refresh` on
   game providers is pure local matching. Every enricher **self-filters**
   — items it doesn't handle (wrong provider or media type) yield
   `(nil, nil)`, so the orchestrator can run every enricher on every item
   without routing logic.
2. **tmdbWatch mapping.** Region US only. Kinds: `flatrate` →
   `subscription`, `free`/`ads` → `stream`; `rent`/`buy` are ignored
   (neither streaming nor ownership). TMDB provider names map to seeded
   service slugs via an explicit alias table ("Amazon Prime Video" →
   `prime_video`); unmapped names fall back to a generic slugify — legal
   because `availability.service_slug` deliberately has no FK. The row
   URL is the region's JustWatch-backed `link` page.
3. **gamecatalogs endpoints are configurable placeholders.** The
   unofficial Game Pass/PS+ response shapes cannot be verified from this
   environment, so M3 ships the full quarantine structure — fetch →
   parse → normalized snapshot → breaker → matcher — with fetch shapes
   defined by our fixtures and default URLs marked as placeholders
   behind `WithGamePassURL`/`WithPSPlusURL` options. Live-endpoint
   verification is a probecheck exercise (it will print FAILED for
   gamecatalogs until then) and is tracked as a prerequisite of the M7
   release pass. Snapshots are stored in **our own normalized format**
   (`{fetched_at, entries:[{name,url}]}`), so upstream drift is contained
   in the fetchers.
4. **Circuit breaker semantics.** Per-catalog breaker, threshold 3
   consecutive failed requests, reset at cycle start. A fetch attempts up
   to the threshold within a cycle; once open, the catalog is skipped for
   the rest of that cycle and the stale snapshot keeps serving matches.
5. **Steam matching order.** IGDB hydrate is extended to capture
   `external_games` (category 1 = Steam) as metadata `steam_appid`;
   `steam.Refresh` matches by app ID first, then falls back to
   normalized-name matching. Items hydrated before this change rely on
   the fallback until re-hydrated. Owned rows link to
   `https://store.steampowered.com/app/{appid}`. The owned-games list is
   snapshotted to `catalogs/steam_owned.json` with the same retention
   semantics as the catalog snapshots.
6. **Name normalizer policy.** Lowercase; keep ASCII alphanumerics;
   `-:_/` and spaces become word separators; every other rune (™®©,
   apostrophes, accented letters) is dropped; whitespace collapsed; one
   trailing edition suffix stripped from an enumerated list (deluxe,
   definitive, GOTY, complete, ultimate, enhanced, …). Matching is exact
   on normalized keys — deterministic and offline-testable by design; no
   fuzzy distance. Item candidates are the title plus IGDB
   `alternative_names` from metadata (`providers.NameCandidates`).

## File Structure

```
internal/providers/
  availability.go        Availability, AvailabilityProvider, CycleSyncer,
                         NameCandidates
  names/
    names.go             Normalize, Set (normalized-name lookup)
  tmdb/
    watch.go             WatchProvider (per-item, region US)
    testdata/watch_movie.json, watch_tv_noregion.json
  gamecatalogs/
    breaker.go           per-catalog circuit breaker
    snapshot.go          normalized snapshot persist/load (catalogs/)
    gamecatalogs.go      Provider, fetchers, SyncCycle, Refresh
    testdata/gamepass_catalog.json, psplus_catalog.json
  steam/
    steam.go             Provider, SyncCycle (GetOwnedGames), Refresh
    testdata/owned_games.json
  igdb/
    igdb.go              (modify) external_games → metadata steam_appid
  setup/
    setup.go             (modify) AvailabilityFromConfig
cmd/probecheck/
  main.go                (modify) availability probes per hydrated item
```

---

### Task 1: Availability Types, NameCandidates & Name Normalizer

**Files:**
- Create: `internal/providers/availability.go`
- Create: `internal/providers/names/names.go`
- Test: `internal/providers/availability_test.go`
- Test: `internal/providers/names/names_test.go`

**Interfaces:**
- Consumes: `store.MediaItem` (M1), `providers` package (M2).
- Produces: `providers.Availability{ServiceSlug string; Kind string; URL
  *string}`, `providers.AvailabilityProvider`, `providers.CycleSyncer`,
  `providers.NameCandidates(item *store.MediaItem) []string`;
  `names.Normalize(title string) string`, `names.NewSet() *Set`,
  `(*Set).Add(name string, url *string)`,
  `(*Set).Lookup(candidates ...string) (Entry, bool)` with
  `names.Entry{Name string; URL *string}`. Tasks 2–6 depend on these
  exact names.

- [ ] **Step 1: Write the failing tests**

`internal/providers/names/names_test.go`:

```go
package names

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"The Witcher® 3: Wild Hunt", "the witcher 3 wild hunt"},
		{"The Witcher 3: Wild Hunt – Complete Edition", "the witcher 3 wild hunt"},
		{"Forza Horizon 5 Deluxe Edition", "forza horizon 5"},
		{"HALO: The Master Chief Collection", "halo the master chief collection"},
		{"Wiedźmin 3: Dziki Gon", "wiedmin 3 dziki gon"},
		{"Control Ultimate Edition", "control"},
		{"Persona 5 Royal", "persona 5 royal"},
		{"Fallout 4: Game of the Year Edition", "fallout 4"},
		{"Grounded", "grounded"},
		{"  spaced   out  ", "spaced out"},
		{"™®©", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSetLookup(t *testing.T) {
	url := "https://example.com/witcher"
	s := NewSet()
	s.Add("The Witcher 3: Wild Hunt – Complete Edition", &url)
	s.Add("Halo Infinite", nil)
	s.Add("", nil) // must not panic or register an empty key

	// Item title matches a catalog entry that carries an edition suffix.
	e, ok := s.Lookup("The Witcher 3: Wild Hunt")
	if !ok {
		t.Fatal("expected Deluxe/Complete Edition catalog entry to match base title")
	}
	if e.URL == nil || *e.URL != url {
		t.Errorf("entry URL = %v, want %s", e.URL, url)
	}

	// First candidate misses, alternative name hits.
	if _, ok := s.Lookup("Nonexistent Game", "HALO INFINITE"); !ok {
		t.Error("expected alternative-name candidate to match")
	}

	if _, ok := s.Lookup("Starfield"); ok {
		t.Error("expected no match for absent title")
	}
}
```

`internal/providers/availability_test.go`:

```go
package providers

import (
	"encoding/json"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func TestNameCandidates(t *testing.T) {
	item := &store.MediaItem{
		Title:    "The Witcher 3: Wild Hunt",
		Metadata: json.RawMessage(`{"alternative_names": ["TW3", "Wiedźmin 3: Dziki Gon"], "summary": "x"}`),
	}
	got := NameCandidates(item)
	want := []string{"The Witcher 3: Wild Hunt", "TW3", "Wiedźmin 3: Dziki Gon"}
	if len(got) != len(want) {
		t.Fatalf("NameCandidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNameCandidatesToleratesBadMetadata(t *testing.T) {
	for _, meta := range []json.RawMessage{nil, json.RawMessage(`not json`), json.RawMessage(`{}`)} {
		item := &store.MediaItem{Title: "Grounded", Metadata: meta}
		got := NameCandidates(item)
		if len(got) != 1 || got[0] != "Grounded" {
			t.Errorf("NameCandidates(meta=%s) = %v, want just the title", meta, got)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/... 2>&1 | head -10`
Expected: FAIL — `names` package missing, `NameCandidates` undefined.

- [ ] **Step 3: Write the implementation**

`internal/providers/names/names.go`:

```go
// Package names normalizes and matches game titles across catalogs:
// casefolding, trademark glyphs, punctuation, and edition suffixes.
// Matching is exact on normalized keys — deterministic and
// offline-testable; no fuzzy distance.
package names

import "strings"

// Suffixes are compared against already-normalized text, so they must be
// written in normalized form themselves (e.g. "directors cut").
var editionSuffixes = []string{
	"game of the year edition",
	"goty edition",
	"goty",
	"digital deluxe edition",
	"deluxe edition",
	"definitive edition",
	"ultimate edition",
	"complete edition",
	"enhanced edition",
	"special edition",
	"anniversary edition",
	"standard edition",
	"directors cut",
}

// Normalize reduces a title to a comparison key: lowercase, ASCII
// alphanumerics kept, separators collapsed to single spaces, every other
// rune dropped, one trailing edition suffix removed.
func Normalize(title string) string {
	lower := strings.ToLower(title)
	var b strings.Builder
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == ':', r == '_', r == '/':
			b.WriteRune(' ')
			// everything else (™®©, apostrophes, accents, dashes beyond
			// ASCII) is dropped
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	for _, suffix := range editionSuffixes {
		if trimmed, ok := strings.CutSuffix(s, " "+suffix); ok {
			s = trimmed
			break
		}
	}
	return s
}

// Entry is one catalog title with its optional store URL.
type Entry struct {
	Name string
	URL  *string
}

// Set is a normalized-name lookup built from catalog entries.
type Set struct {
	m map[string]Entry
}

func NewSet() *Set { return &Set{m: make(map[string]Entry)} }

// Add registers a catalog entry under its normalized key. First entry
// wins on collisions; empty keys are ignored.
func (s *Set) Add(name string, url *string) {
	key := Normalize(name)
	if key == "" {
		return
	}
	if _, exists := s.m[key]; !exists {
		s.m[key] = Entry{Name: name, URL: url}
	}
}

// Lookup tries each candidate name in order (canonical title first, then
// alternatives) and returns the first entry whose normalized key matches.
func (s *Set) Lookup(candidates ...string) (Entry, bool) {
	for _, c := range candidates {
		if e, ok := s.m[Normalize(c)]; ok {
			return e, true
		}
	}
	return Entry{}, false
}
```

`internal/providers/availability.go`:

```go
package providers

import (
	"context"
	"encoding/json"

	"github.com/varigg/mediatracker/internal/store"
)

// Availability is one availability/ownership fact from an enricher, not
// yet bound to an item or timestamps — the ingest layer adds those when
// persisting via store.UpsertAvailability.
type Availability struct {
	ServiceSlug string
	Kind        string // stream | subscription | owned
	URL         *string
}

// AvailabilityProvider produces availability rows for one item. Game
// providers match locally against cycle-cached snapshots; tmdbWatch
// calls upstream per item.
type AvailabilityProvider interface {
	Refresh(ctx context.Context, item *store.MediaItem) ([]Availability, error)
}

// CycleSyncer is implemented by availability providers that need a
// once-per-cycle upstream fetch (catalog snapshots, owned-games list).
// The refresh orchestrator calls SyncCycle before any per-item Refresh.
type CycleSyncer interface {
	SyncCycle(ctx context.Context) error
}

// NameCandidates returns the item title plus any IGDB alternative names
// carried in metadata, in matching-priority order. Malformed metadata
// degrades to just the title.
func NameCandidates(item *store.MediaItem) []string {
	candidates := []string{item.Title}
	if len(item.Metadata) == 0 {
		return candidates
	}
	var meta struct {
		AlternativeNames []string `json:"alternative_names"`
	}
	if err := json.Unmarshal(item.Metadata, &meta); err != nil {
		return candidates
	}
	return append(candidates, meta.AlternativeNames...)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/
git commit -m "feat: add availability provider contract and game-name matcher"
```

---

### Task 2: tmdbWatch — Streaming Availability Enricher

**Files:**
- Create: `internal/providers/tmdb/watch.go`
- Create: `internal/providers/tmdb/testdata/watch_movie.json`
- Create: `internal/providers/tmdb/testdata/watch_tv_noregion.json`
- Test: `internal/providers/tmdb/watch_test.go`

**Interfaces:**
- Consumes: `providers.Availability`, `providers.AvailabilityProvider`
  (Task 1); `Client`, `parseProviderID`, `newTestClient`, `serveFixture`
  (M2).
- Produces: `(*Client).WatchProvider() providers.AvailabilityProvider`.
  Emits `subscription` (flatrate) and `stream` (free/ads) rows with
  seeded-catalog slugs where known.

- [ ] **Step 1: Write the failing tests**

`internal/providers/tmdb/watch_test.go`:

```go
package tmdb

import (
	"context"
	"net/http"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func watchItem(mt store.MediaType, providerID string) *store.MediaItem {
	return &store.MediaItem{ID: 1, MediaType: mt, Title: "x", Provider: "tmdb", ProviderID: providerID}
}

func TestWatchRefreshMovie(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/603/watch/providers", serveFixture(t, "watch_movie.json"))
	c := newTestClient(t, mux, "")

	got, err := c.WatchProvider().Refresh(context.Background(), watchItem(store.TypeMovie, "movie:603"))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	byKey := map[string]string{} // slug → kind
	for _, a := range got {
		byKey[a.ServiceSlug] = a.Kind
		wantLink := "https://www.themoviedb.org/movie/603-the-matrix/watch?locale=US"
		if a.URL == nil || *a.URL != wantLink {
			t.Errorf("%s URL = %v, want region link", a.ServiceSlug, a.URL)
		}
	}
	want := map[string]string{
		"netflix":          "subscription", // alias-mapped
		"prime_video":      "subscription", // "Amazon Prime Video" alias
		"some_new_service": "subscription", // unmapped → slugify fallback
		"peacock":          "stream",       // "Peacock Premium" via ads
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows %v, want %d", len(got), byKey, len(want))
	}
	for slug, kind := range want {
		if byKey[slug] != kind {
			t.Errorf("%s = %q, want %q (rows: %v)", slug, byKey[slug], kind, byKey)
		}
	}
	// rent-only "Apple TV" and region DE must not appear
	if _, ok := byKey["apple_tv"]; ok {
		t.Error("rent entries must be ignored")
	}
	if _, ok := byKey["wow"]; ok {
		t.Error("non-US regions must be ignored")
	}
}

func TestWatchRefreshNoUSRegion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/1396/watch/providers", serveFixture(t, "watch_tv_noregion.json"))
	c := newTestClient(t, mux, "")

	got, err := c.WatchProvider().Refresh(context.Background(), watchItem(store.TypeTV, "tv:1396"))
	if err != nil {
		t.Fatalf("Refresh error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("no US region must yield no rows, got %+v", got)
	}
}

func TestWatchRefreshSelfFilters(t *testing.T) {
	c := newTestClient(t, http.NewServeMux(), "")
	// Non-tmdb items are simply not this enricher's concern: (nil, nil),
	// consistent with gamecatalogs/steam ignoring non-game items.
	item := watchItem(store.TypeMovie, "movie:603")
	item.Provider = "igdb"
	got, err := c.WatchProvider().Refresh(context.Background(), item)
	if err != nil || len(got) != 0 {
		t.Errorf("non-tmdb item = (%+v, %v), want (none, nil)", got, err)
	}
	// A tmdb item with a mismatched ID namespace is data corruption: error.
	if _, err := c.WatchProvider().Refresh(context.Background(), watchItem(store.TypeMovie, "tv:1396")); err == nil {
		t.Error("mismatched provider-id namespace must error")
	}
}

func TestWatchRefreshUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/603/watch/providers", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newTestClient(t, mux, "")
	if _, err := c.WatchProvider().Refresh(context.Background(), watchItem(store.TypeMovie, "movie:603")); err == nil {
		t.Error("upstream 500 must surface as error (caller decides degradation)")
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/tmdb/testdata/watch_movie.json`:

```json
{
  "id": 603,
  "results": {
    "US": {
      "link": "https://www.themoviedb.org/movie/603-the-matrix/watch?locale=US",
      "flatrate": [
        {"provider_id": 8, "provider_name": "Netflix"},
        {"provider_id": 9, "provider_name": "Amazon Prime Video"},
        {"provider_id": 999, "provider_name": "Some New Service"}
      ],
      "ads": [
        {"provider_id": 386, "provider_name": "Peacock Premium"}
      ],
      "rent": [
        {"provider_id": 2, "provider_name": "Apple TV"}
      ]
    },
    "DE": {
      "link": "https://www.themoviedb.org/movie/603-the-matrix/watch?locale=DE",
      "flatrate": [{"provider_id": 30, "provider_name": "WOW"}]
    }
  }
}
```

`internal/providers/tmdb/testdata/watch_tv_noregion.json`:

```json
{
  "id": 1396,
  "results": {
    "DE": {
      "link": "https://www.themoviedb.org/tv/1396-breaking-bad/watch?locale=DE",
      "flatrate": [{"provider_id": 30, "provider_name": "WOW"}]
    }
  }
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/tmdb/ 2>&1 | head -6`
Expected: FAIL — `WatchProvider` undefined.

- [ ] **Step 4: Write the implementation**

`internal/providers/tmdb/watch.go`:

```go
package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

// providerSlugs maps TMDB watch-provider names onto the seeded services
// catalog. Unmapped names fall back to slugify — availability rows may
// reference services beyond the seeded set (no FK by design).
var providerSlugs = map[string]string{
	"Netflix":            "netflix",
	"Amazon Prime Video": "prime_video",
	"Disney Plus":        "disney_plus",
	"Disney+":            "disney_plus",
	"Hulu":               "hulu",
	"Max":                "max",
	"Apple TV Plus":      "apple_tv_plus",
	"Apple TV+":          "apple_tv_plus",
	"Paramount Plus":     "paramount_plus",
	"Paramount+":         "paramount_plus",
	"Peacock":            "peacock",
	"Peacock Premium":    "peacock",
}

type watchEntry struct {
	ProviderName string `json:"provider_name"`
}

type watchResponse struct {
	Results map[string]struct {
		Link     string       `json:"link"`
		Flatrate []watchEntry `json:"flatrate"`
		Free     []watchEntry `json:"free"`
		Ads      []watchEntry `json:"ads"`
	} `json:"results"`
}

// WatchProvider returns the streaming-availability enricher backed by
// TMDB's JustWatch-sourced watch/providers endpoint, region US.
func (c *Client) WatchProvider() providers.AvailabilityProvider {
	return watchProvider{c}
}

type watchProvider struct{ c *Client }

func (p watchProvider) Refresh(ctx context.Context, item *store.MediaItem) ([]providers.Availability, error) {
	if item.Provider != "tmdb" {
		return nil, nil // not this enricher's item; self-filter like the game providers
	}
	id, err := parseProviderID(item.MediaType, item.ProviderID)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/movie/%d/watch/providers", id)
	if item.MediaType == store.TypeTV {
		path = fmt.Sprintf("/tv/%d/watch/providers", id)
	}
	var resp watchResponse
	if err := p.c.get(ctx, path, url.Values{}, &resp); err != nil {
		return nil, err
	}
	region, ok := resp.Results["US"]
	if !ok {
		return nil, nil
	}

	var link *string
	if region.Link != "" {
		l := region.Link
		link = &l
	}
	var out []providers.Availability
	seen := map[string]bool{}
	add := func(entries []watchEntry, kind string) {
		for _, e := range entries {
			slug := slugFor(e.ProviderName)
			if slug == "" || seen[slug+"/"+kind] {
				continue
			}
			seen[slug+"/"+kind] = true
			out = append(out, providers.Availability{ServiceSlug: slug, Kind: kind, URL: link})
		}
	}
	add(region.Flatrate, "subscription")
	add(region.Free, "stream")
	add(region.Ads, "stream")
	return out, nil
}

func slugFor(name string) string {
	if slug, ok := providerSlugs[name]; ok {
		return slug
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == ' ', r == '-', r == '+':
			if !lastUnderscore && b.Len() > 0 {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/tmdb/
git commit -m "feat: add TMDB watch-providers availability enricher"
```

---

### Task 3: gamecatalogs Circuit Breaker

**Files:**
- Create: `internal/providers/gamecatalogs/breaker.go`
- Test: `internal/providers/gamecatalogs/breaker_test.go`

**Interfaces:**
- Produces: `newBreaker(threshold int) *breaker`, `(*breaker).Allow()
  bool`, `(*breaker).Success()`, `(*breaker).Failure()`,
  `(*breaker).Reset()`. Task 4 wraps every catalog request with it.

- [ ] **Step 1: Write the failing test**

`internal/providers/gamecatalogs/breaker_test.go`:

```go
package gamecatalogs

import "testing"

func TestBreakerTripsAtThreshold(t *testing.T) {
	b := newBreaker(3)
	for i := 0; i < 2; i++ {
		b.Failure()
		if !b.Allow() {
			t.Fatalf("breaker open after %d failures, threshold is 3", i+1)
		}
	}
	b.Failure()
	if b.Allow() {
		t.Fatal("breaker still closed after 3 consecutive failures")
	}
}

func TestBreakerSuccessResetsCount(t *testing.T) {
	b := newBreaker(3)
	b.Failure()
	b.Failure()
	b.Success()
	b.Failure()
	b.Failure()
	if !b.Allow() {
		t.Fatal("success must reset the consecutive-failure count")
	}
}

func TestBreakerResetClosesCircuit(t *testing.T) {
	b := newBreaker(1)
	b.Failure()
	if b.Allow() {
		t.Fatal("threshold-1 breaker must open on first failure")
	}
	b.Reset()
	if !b.Allow() {
		t.Fatal("Reset must close the circuit for the next cycle")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/gamecatalogs/ 2>&1 | head -4`
Expected: FAIL — package does not exist / `newBreaker` undefined.

- [ ] **Step 3: Write the implementation**

`internal/providers/gamecatalogs/breaker.go`:

```go
// Package gamecatalogs is the quarantined adapter for the unofficial
// Game Pass and PS+ catalog endpoints: full-catalog snapshots fetched
// once per refresh cycle, persisted under catalogs/, retained on fetch
// failure, and matched locally against tracked games.
package gamecatalogs

// breaker is a per-catalog circuit breaker: after threshold consecutive
// request failures it opens and stays open until Reset (cycle start), so
// a dead unofficial endpoint cannot stall a refresh cycle.
type breaker struct {
	threshold int
	failures  int
	open      bool
}

func newBreaker(threshold int) *breaker { return &breaker{threshold: threshold} }

func (b *breaker) Allow() bool { return !b.open }

func (b *breaker) Success() { b.failures = 0 }

func (b *breaker) Failure() {
	b.failures++
	if b.failures >= b.threshold {
		b.open = true
	}
}

func (b *breaker) Reset() {
	b.failures = 0
	b.open = false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `set -o pipefail; go test ./internal/providers/gamecatalogs/ -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/gamecatalogs/
git commit -m "feat: add per-catalog circuit breaker for gamecatalogs"
```

---

### Task 4: gamecatalogs Snapshots, Fetchers & SyncCycle

**Files:**
- Create: `internal/providers/gamecatalogs/gamecatalogs.go`
- Create: `internal/providers/gamecatalogs/snapshot.go`
- Create: `internal/providers/gamecatalogs/testdata/gamepass_catalog.json`
- Create: `internal/providers/gamecatalogs/testdata/psplus_catalog.json`
- Test: `internal/providers/gamecatalogs/sync_test.go`

**Interfaces:**
- Consumes: `breaker` (Task 3), `names.Set`/`names.NewSet` (Task 1).
- Produces: `gamecatalogs.New(dir string, opts ...Option) *Provider`
  (dir = `{dataDir}/catalogs`); options `WithGamePassURL`,
  `WithPSPlusURL`, `WithHTTPClient`, `WithLogger`, `WithNow`;
  `(*Provider).SyncCycle(ctx) error` satisfying `providers.CycleSyncer`.
  Internal: `snapshotPath`/`saveSnapshot`/`loadSnapshot`, `catalogEntry`,
  `buildSet`, `set(slug)`. `Refresh` arrives in Task 5. Catalog slugs are
  the seeded `game_pass` / `ps_plus`; snapshot files
  `catalogs/game_pass.json`, `catalogs/ps_plus.json`.

- [ ] **Step 1: Write the failing tests**

`internal/providers/gamecatalogs/sync_test.go`:

```go
package gamecatalogs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

func newTestProvider(t *testing.T, mux *http.ServeMux, opts ...Option) *Provider {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	dir := filepath.Join(t.TempDir(), "catalogs")
	all := append([]Option{
		WithGamePassURL(srv.URL + "/gamepass"),
		WithPSPlusURL(srv.URL + "/psplus"),
	}, opts...)
	return New(dir, all...)
}

func healthyMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/gamepass", serveFixture(t, "gamepass_catalog.json"))
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	return mux
}

func TestSyncCycleWritesSnapshots(t *testing.T) {
	p := newTestProvider(t, healthyMux(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	gp, err := p.loadSnapshot("game_pass")
	if err != nil {
		t.Fatalf("load game_pass snapshot: %v", err)
	}
	if len(gp.Entries) != 3 || gp.FetchedAt == "" {
		t.Errorf("game_pass snapshot = %d entries, fetched_at %q", len(gp.Entries), gp.FetchedAt)
	}
	ps, err := p.loadSnapshot("ps_plus")
	if err != nil {
		t.Fatalf("load ps_plus snapshot: %v", err)
	}
	if len(ps.Entries) != 2 {
		t.Errorf("ps_plus snapshot = %d entries, want 2", len(ps.Entries))
	}
}

func TestSyncCycleRetainsStaleSnapshotOnFailure(t *testing.T) {
	failing := false
	mux := http.NewServeMux()
	mux.HandleFunc("/gamepass", func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		serveFixture(t, "gamepass_catalog.json")(w, r)
	})
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	p := newTestProvider(t, mux)

	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("first SyncCycle error = %v", err)
	}
	failing = true
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("failing SyncCycle must degrade, not error; got %v", err)
	}
	snap, err := p.loadSnapshot("game_pass")
	if err != nil {
		t.Fatalf("stale snapshot must survive fetch failure: %v", err)
	}
	if len(snap.Entries) != 3 {
		t.Errorf("stale snapshot = %d entries, want original 3", len(snap.Entries))
	}
}

func TestSyncCycleBreakerLimitsRequests(t *testing.T) {
	var gamePassCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/gamepass", func(w http.ResponseWriter, r *http.Request) {
		gamePassCalls++
		w.WriteHeader(http.StatusBadGateway)
	})
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	p := newTestProvider(t, mux)

	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	if gamePassCalls != 3 {
		t.Errorf("breaker allowed %d requests, want exactly threshold 3", gamePassCalls)
	}
	if _, err := p.loadSnapshot("ps_plus"); err != nil {
		t.Errorf("healthy catalog must still sync when the other trips: %v", err)
	}
}

func TestSyncCycleTreatsEmptyCatalogAsFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gamepass", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"products": []}`))
	})
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	p := newTestProvider(t, mux)

	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	if _, err := p.loadSnapshot("game_pass"); err == nil {
		t.Error("empty catalog (likely schema drift) must not produce a snapshot")
	}
}
```

- [ ] **Step 2: Create the fixtures**

`internal/providers/gamecatalogs/testdata/gamepass_catalog.json`:

```json
{
  "products": [
    {"title": "Forza Horizon 5 Deluxe Edition", "url": "https://www.xbox.com/en-US/games/store/forza-horizon-5/9NKX70BBCDRN"},
    {"title": "Halo Infinite", "url": "https://www.xbox.com/en-US/games/store/halo-infinite/9PP5G1F0C2B6"},
    {"title": "Grounded"}
  ]
}
```

`internal/providers/gamecatalogs/testdata/psplus_catalog.json`:

```json
{
  "games": [
    {"name": "The Witcher 3: Wild Hunt – Complete Edition", "url": "https://store.playstation.com/en-us/product/UP4497-CUSA05725_00-COMPLETEEDITION0"},
    {"name": "Bloodborne"}
  ]
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/gamecatalogs/ 2>&1 | head -6`
Expected: FAIL — `New`, `WithGamePassURL`, etc. undefined.

- [ ] **Step 4: Write the implementation**

`internal/providers/gamecatalogs/snapshot.go`:

```go
package gamecatalogs

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/varigg/mediatracker/internal/providers/names"
)

// snapshot is the normalized on-disk catalog format. Parsing raw
// upstream payloads happens at fetch time, so unofficial-endpoint shape
// drift is contained in the fetchers.
type snapshot struct {
	FetchedAt string         `json:"fetched_at"` // UTC "2006-01-02 15:04:05"
	Entries   []catalogEntry `json:"entries"`
}

type catalogEntry struct {
	Name string  `json:"name"`
	URL  *string `json:"url,omitempty"`
}

func (p *Provider) snapshotPath(slug string) string {
	return filepath.Join(p.dir, slug+".json")
}

func (p *Provider) saveSnapshot(slug string, entries []catalogEntry) error {
	snap := snapshot{
		FetchedAt: p.now().UTC().Format("2006-01-02 15:04:05"),
		Entries:   entries,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.snapshotPath(slug) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.snapshotPath(slug))
}

func (p *Provider) loadSnapshot(slug string) (*snapshot, error) {
	data, err := os.ReadFile(p.snapshotPath(slug))
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func buildSet(entries []catalogEntry) *names.Set {
	s := names.NewSet()
	for _, e := range entries {
		s.Add(e.Name, e.URL)
	}
	return s
}

func (p *Provider) setSet(slug string, s *names.Set) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sets[slug] = s
}

// set returns the in-memory lookup for a catalog, lazily loading the
// snapshot from disk (startup before first sync). Missing snapshot ⇒
// nil: no availability facts, not an error.
func (p *Provider) set(slug string) *names.Set {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sets[slug]; ok {
		return s
	}
	snap, err := p.loadSnapshot(slug)
	if err != nil {
		return nil
	}
	s := buildSet(snap.Entries)
	p.sets[slug] = s
	return s
}
```

`internal/providers/gamecatalogs/gamecatalogs.go`:

```go
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

	"github.com/varigg/mediatracker/internal/providers/names"
)

// Placeholder defaults — the unofficial endpoints must be verified live
// via cmd/probecheck before the M7 release pass (M3 design decision 3).
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/gamecatalogs/ -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/gamecatalogs/
git commit -m "feat: add game catalog snapshot sync with retention and breaker"
```

---

### Task 5: gamecatalogs Refresh — Snapshot Matching

**Files:**
- Modify: `internal/providers/gamecatalogs/gamecatalogs.go` (add `Refresh`)
- Test: `internal/providers/gamecatalogs/refresh_test.go`

**Interfaces:**
- Consumes: `providers.NameCandidates`, `names.Set.Lookup` (Task 1),
  snapshot machinery (Task 4).
- Produces: `(*Provider).Refresh(ctx, item) ([]providers.Availability,
  error)` — `*Provider` now satisfies both `providers.AvailabilityProvider`
  and `providers.CycleSyncer`. Rows: `service_slug` ∈ {`game_pass`,
  `ps_plus`}, `kind = subscription`, URL from the catalog entry.

- [ ] **Step 1: Write the failing tests**

`internal/providers/gamecatalogs/refresh_test.go`:

```go
package gamecatalogs

import (
	"context"
	"encoding/json"
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
```

(`net/http` needs importing in this file for `http.NewServeMux()`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/gamecatalogs/ 2>&1 | head -6`
Expected: FAIL — `Refresh` undefined.

- [ ] **Step 3: Write the implementation**

Append to `internal/providers/gamecatalogs/gamecatalogs.go` (add
`"github.com/varigg/mediatracker/internal/providers"` and
`"github.com/varigg/mediatracker/internal/store"` to imports):

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/gamecatalogs/
git commit -m "feat: match tracked games against catalog snapshots"
```

---

### Task 6: Steam Ownership Enricher (+ IGDB steam_appid Capture)

**Files:**
- Create: `internal/providers/steam/steam.go`
- Create: `internal/providers/steam/testdata/owned_games.json`
- Test: `internal/providers/steam/steam_test.go`
- Modify: `internal/providers/igdb/igdb.go` (query + parse
  `external_games` → metadata `steam_appid`)
- Modify: `internal/providers/igdb/testdata/igdb_game.json`
- Modify: `internal/providers/igdb/igdb_test.go` (assert `steam_appid`)

**Interfaces:**
- Consumes: `providers.Availability`, `providers.NameCandidates`,
  `names.Set` (Task 1); `config` Steam key/ID fields (M1).
- Produces: `steam.New(apiKey, steamID, cacheDir string, opts ...Option)
  *Provider` satisfying `providers.AvailabilityProvider` +
  `providers.CycleSyncer`; options `WithBaseURL`, `WithHTTPClient`,
  `WithLogger`, `WithNow`. Rows: `service_slug = steam`, `kind = owned`,
  URL `https://store.steampowered.com/app/{appid}`. Owned list snapshot
  at `{cacheDir}/steam_owned.json`. IGDB hydrate now emits metadata
  `steam_appid` (int64) when `external_games` has a category-1 entry.

- [ ] **Step 1: Write the failing tests**

`internal/providers/steam/steam_test.go`:

```go
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
```

- [ ] **Step 2: Create the fixture**

`internal/providers/steam/testdata/owned_games.json`:

```json
{
  "response": {
    "game_count": 2,
    "games": [
      {"appid": 292030, "name": "The Witcher 3: Wild Hunt", "playtime_forever": 9001},
      {"appid": 1145360, "name": "Hades", "playtime_forever": 3600}
    ]
  }
}
```

- [ ] **Step 3: Extend the IGDB hydrate test and fixture**

In `internal/providers/igdb/testdata/igdb_game.json`, add after
`"alternative_names": [...]` (inside the game object):

```json
    "external_games": [
      {"id": 10, "category": 1, "uid": "292030"},
      {"id": 11, "category": 5, "uid": "999999"}
    ]
```

In `internal/providers/igdb/igdb_test.go` `TestHydrate`, after the
`alternative_names` assertion:

```go
	if appID, ok := got.Metadata["steam_appid"].(int64); !ok || appID != 292030 {
		t.Errorf("metadata steam_appid = %v, want int64 292030 (category-1 external game)", got.Metadata["steam_appid"])
	}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/providers/steam/ ./internal/providers/igdb/ 2>&1 | head -8`
Expected: FAIL — `steam` package missing; `steam_appid` metadata absent.

- [ ] **Step 5: Write the implementation**

In `internal/providers/igdb/igdb.go`: add to the `game` struct:

```go
	ExternalGames []struct {
		Category int    `json:"category"`
		UID      string `json:"uid"`
	} `json:"external_games"`
```

In `Hydrate`, extend the Apicalypse field list to
`...,alternative_names.name,external_games.category,external_games.uid; where id = %d;`
and after the `metadata` map is built:

```go
	for _, eg := range g.ExternalGames {
		if eg.Category == 1 { // Steam
			if appID, err := strconv.ParseInt(eg.UID, 10, 64); err == nil {
				metadata["steam_appid"] = appID
				break
			}
		}
	}
```

`internal/providers/steam/steam.go`:

```go
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
	snap := snapshot{FetchedAt: p.now().UTC().Format("2006-01-02 15:04:05"), Games: games}
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
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/providers/... -count=1`
Expected: PASS

- [ ] **Step 7: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/steam/ internal/providers/igdb/
git commit -m "feat: add Steam ownership enricher with IGDB app-id mapping"
```

---

### Task 7: Availability Wiring + probecheck Probes

**Files:**
- Modify: `internal/providers/setup/setup.go` (add `AvailabilityFromConfig`)
- Modify: `internal/providers/setup/setup_test.go`
- Modify: `cmd/probecheck/main.go` (cycle sync + per-item availability)

**Interfaces:**
- Consumes: everything from Tasks 1–6, `config.Providers`.
- Produces: `setup.AvailabilityFromConfig(p config.Providers, dataDir
  string, logger *slog.Logger) []providers.AvailabilityProvider` — the
  wiring point M4's refresh orchestrator reuses; callers type-assert
  `providers.CycleSyncer` for the once-per-cycle syncs. probecheck stays
  test-free (manual live utility); **do not run it during execution** —
  gamecatalogs will print FAILED against placeholder endpoints until
  live verification, which is expected and tracked for M7.

- [ ] **Step 1: Write the failing test**

Append to `internal/providers/setup/setup_test.go`:

```go
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
```

(Add `"github.com/varigg/mediatracker/internal/providers"` to that
file's imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/setup/ 2>&1 | head -4`
Expected: FAIL — `AvailabilityFromConfig` undefined.

- [ ] **Step 3: Write the implementation**

Append to `internal/providers/setup/setup.go` (add imports
`"path/filepath"`, `gamecatalogs`, `steam` packages):

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `set -o pipefail; go test ./internal/providers/setup/ -count=1`
Expected: PASS

- [ ] **Step 5: Extend probecheck**

In `cmd/probecheck/main.go`: add `"encoding/json"` and the `providers`
import is already present. In `main`, after building `registry`:

```go
	avail := setup.AvailabilityFromConfig(cfg.Providers, *dataDir, slog.Default())
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
```

Change `runProbe(p, probe.query)` to `runProbe(p, probe.query, avail)`
and extend it — after the hydrated-details print:

```go
func runProbe(p providers.MetadataProvider, query string, avail []providers.AvailabilityProvider) error {
	// ... existing search + hydrate ...

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
```

- [ ] **Step 6: Full verification, format, commit**

```bash
gofmt -l . && go vet ./... && go build ./... && set -o pipefail && go test ./... -count=1
git add internal/providers/setup/ cmd/probecheck/
git commit -m "feat: wire availability enrichers from config into probecheck"
```

---

## Self-Review Notes

Milestone scope covered task-by-task: `AvailabilityProvider` interface →
T1; `tmdbWatch` (region US, stream/subscription) → T2; `gamecatalogs`
full-catalog snapshot fetch, disk cache in `catalogs/`, retained on
fetch failure, circuit breaker → T3/T4; local matching against snapshots
→ T5; `steam` GetOwnedGames once per cycle, IGDB external-ID mapping
with normalized-name fallback, `kind=owned` with store URL → T6; shared
name normalizer (editions, casefold, alternative names) → T1. Key tests
all present: matcher against snapshot fixtures with "Deluxe Edition" and
"Complete Edition" cases (T1, T5), circuit-breaker trip/skip (T3, T4's
request-count test), stale-snapshot retention on fetch failure (T4, T6).

Type-consistency check: all enrichers return `providers.Availability`
(no ItemID/timestamps); `gamecatalogs.Provider` and `steam.Provider`
carry compile-time interface assertions in their tests. `names.Set`
consumers pass `providers.NameCandidates(item)...` — title first, then
IGDB `alternative_names`. Kinds emitted: `subscription` (tmdb flatrate,
game catalogs), `stream` (tmdb free/ads), `owned` (steam) — matching the
schema CHECK-free but query-relevant vocabulary from M1's
available-to-me filter (`subscribed = 1 OR a.kind = 'owned'`).

## Execution Notes

Work in a `.worktrees/m3-availability-enrichers` worktree branched from
`main`; fast-forward merge back after all tasks are green, then remove
the worktree and branch (per M1/M2 precedent). Never pipe `go test`
through `head`/`tail` before a commit without `set -o pipefail`. Do not
run probecheck during execution (live endpoints).
