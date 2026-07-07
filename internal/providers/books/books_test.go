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
