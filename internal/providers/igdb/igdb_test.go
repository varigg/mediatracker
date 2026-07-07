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
