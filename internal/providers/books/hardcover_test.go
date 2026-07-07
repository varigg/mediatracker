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
