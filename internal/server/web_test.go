package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

// seedWeb populates a store with one item per interesting situation:
// an in-progress TV show, a want-to movie newly available on a
// subscribed service, a done book, and a want-to game with ownership.
func seedWeb(t *testing.T, st *store.Store) (ids map[string]int64) {
	t.Helper()
	ctx := context.Background()
	ids = map[string]int64{}
	mk := func(key string, n store.NewItem) *store.MediaItem {
		it, _, err := st.CreateItem(ctx, n)
		if err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
		ids[key] = it.ID
		return it
	}
	tv := mk("tv", store.NewItem{MediaType: store.TypeTV, Title: "Severance",
		ReleaseYear: intp(2022), Genres: []string{"Drama"}, Provider: "tmdb", ProviderID: "tv:1"})
	if err := st.UpdateState(ctx, tv.ID, store.StateInProgress); err != nil {
		t.Fatal(err)
	}
	movie := mk("movie", store.NewItem{MediaType: store.TypeMovie, Title: "Dune: Part Two",
		ReleaseYear: intp(2024), Genres: []string{"Sci-Fi"}, Provider: "tmdb", ProviderID: "movie:2"})
	if err := st.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAvailability(ctx, movie.ID, []store.Availability{
		{ServiceSlug: "netflix", Kind: store.KindSubscription}}); err != nil {
		t.Fatal(err)
	}
	book := mk("book", store.NewItem{MediaType: store.TypeBook, Title: "The Hobbit",
		ReleaseYear: intp(1937), Genres: []string{"Fantasy"}, Provider: "openlibrary", ProviderID: "OL1"})
	if err := st.UpdateState(ctx, book.ID, store.StateDone); err != nil {
		t.Fatal(err)
	}
	game := mk("game", store.NewItem{MediaType: store.TypeGame, Title: "Hades",
		ReleaseYear: intp(2020), Genres: []string{"Roguelike"}, Provider: "igdb", ProviderID: "g1"})
	if err := st.ReplaceRatings(ctx, game.ID, []store.Rating{
		{Source: "igdb", Score: 93, Display: "93/100"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAvailability(ctx, game.ID, []store.Availability{
		{ServiceSlug: "steam", Kind: store.KindOwned}}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func intp(i int) *int { return &i }

func TestHomeRendersSections(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := get(t, srv, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{
		"Continue",           // section
		"Severance",          // the in-progress item
		"Newly available",    // section
		"Dune: Part Two",     // newly available on subscribed netflix
		"Library",            // counts panel
		`class="lrow video"`, // type-tinted row
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("home missing %q", needle)
		}
	}
	if strings.Contains(body, "The Hobbit") {
		t.Error("done book must not appear in Continue/Newly available")
	}
}

func TestTabDefaultsToWantTo(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := get(t, srv, "/movies-tv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Dune: Part Two") { // want_to movie
		t.Error("want_to movie missing from default tab view")
	}
	if strings.Contains(body, "Severance") { // in_progress: filtered out by default state
		t.Error("in_progress item leaked into want_to view")
	}
}

func TestTabStateAndTypeFilters(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	_, body := get(t, srv, "/movies-tv?state=in_progress&type=tv")
	if !strings.Contains(body, "Severance") {
		t.Error("tv in_progress filter missed Severance")
	}
	_, body = get(t, srv, "/books?state=done")
	if !strings.Contains(body, "The Hobbit") {
		t.Error("done book missing")
	}
}

func TestTabAvailableToMe(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	_, body := get(t, srv, "/games?available=1")
	if !strings.Contains(body, "Hades") { // steam-owned counts as available
		t.Error("owned game missing from available-to-me")
	}
}

func TestTabInvalidParamsAre400(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	for _, q := range []string{"?state=pending", "?sort=popularity", "?dir=sideways"} {
		resp, _ := get(t, srv, "/games"+q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestTabHTMXFragment(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	req, _ := http.NewRequest("GET", srv.URL+"/games", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if strings.Contains(body, "<!doctype html>") {
		t.Error("HX-Request must return the fragment, not the full page")
	}
	if !strings.Contains(body, "Hades") {
		t.Error("fragment missing table content")
	}
}
