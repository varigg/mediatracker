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
