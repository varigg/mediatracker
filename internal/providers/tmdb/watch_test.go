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
