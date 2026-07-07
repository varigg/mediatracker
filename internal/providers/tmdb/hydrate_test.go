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
