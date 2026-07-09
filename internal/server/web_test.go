package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/ingest"
	"github.com/varigg/mediatracker/internal/providers"
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

func TestTabGenreFilter(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	ctx := context.Background()
	// seedWeb's want_to movies-tv item is Dune: Part Two (Sci-Fi). Add a
	// second want_to movie with a different genre so the tab has two
	// distinct genres to pick from.
	if _, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "Paddington",
		ReleaseYear: intp(2014), Genres: []string{"Comedy"}, Provider: "tmdb", ProviderID: "movie:3"}); err != nil {
		t.Fatal(err)
	}

	resp, body := get(t, srv, "/movies-tv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{`name="genre"`, `>All genres<`, `>Sci-Fi<`, `>Comedy<`} {
		if !strings.Contains(body, needle) {
			t.Errorf("toolbar missing %q", needle)
		}
	}

	_, body = get(t, srv, "/movies-tv?genre=Comedy")
	if !strings.Contains(body, "Paddington") {
		t.Error("genre=Comedy must include Paddington")
	}
	if strings.Contains(body, "Dune: Part Two") {
		t.Error("genre=Comedy must exclude Dune: Part Two")
	}
	if !strings.Contains(body, `<option value="Comedy" selected>`) {
		t.Error("selected genre option must reflect the current filter")
	}

	// State links must carry the active genre so switching state doesn't
	// silently drop the filter (tabURL's preservation, asserted at the
	// rendered-body level rather than just the unit-tested helper).
	if !strings.Contains(body, `genre=Comedy`) {
		t.Error("rendered toolbar links must preserve ?genre= when active")
	}
}

func TestTabDensityWhitelist(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	ctx := context.Background()
	// Set a bogus density value in the store
	if err := st.SetSetting(ctx, "row_density", "gigantic"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	_, body := get(t, srv, "/games")
	if !strings.Contains(body, "density-l") {
		t.Error("body missing density-l (invalid value should default to l)")
	}
	if strings.Contains(body, "density-gigantic") {
		t.Error("body contains density-gigantic (invalid value should be rejected)")
	}
}

func TestDetailRenders(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	ctx := context.Background()
	if err := st.UpdateNotes(ctx, ids["game"], "Heat 16. **Coach Skelly** believes."); err != nil {
		t.Fatal(err)
	}
	resp, body := get(t, srv, fmt.Sprintf("/items/%d", ids["game"]))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{
		"Hades",
		"93/100",                        // rating display string
		"Steam",                         // availability badge
		"<strong>Coach Skelly</strong>", // markdown rendered
		"Start",                         // legal transition from want_to
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("detail missing %q", needle)
		}
	}
	if strings.Contains(body, "Re-watch") { // done-only transition must not render for want_to
		t.Error("illegal transition rendered")
	}
}

func TestDetailNotFound(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, _ := get(t, srv, "/items/99999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestNotesEscapeRawHTML(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	if err := st.UpdateNotes(context.Background(), ids["game"], `<script>alert(1)</script>`); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, srv, fmt.Sprintf("/items/%d", ids["game"]))
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("raw HTML in notes must be escaped")
	}
}

func TestCoversServedAndHardened(t *testing.T) {
	srv, st, dataDir := newTestServer(t)
	ids := seedWeb(t, st)
	dir := filepath.Join(dataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%d.jpg", ids["game"])
	if err := os.WriteFile(filepath.Join(dir, name), []byte("\xff\xd8fakejpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, _ := get(t, srv, "/covers/"+name)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cover: status = %d", resp.StatusCode)
	}
	for _, bad := range []string{"/covers/../app.db", "/covers/evil.txt", "/covers/1.png"} {
		resp, _ := get(t, srv, bad)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s must not be served", bad)
		}
	}
}

func postForm(t *testing.T, srv *httptest.Server, method, path string, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestTransitionHappyPath(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["game"]),
		url.Values{"to": {"in_progress"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "In progress") {
		t.Error("fragment must reflect the new state")
	}
	it, err := st.GetItem(context.Background(), ids["game"])
	if err != nil || it.State != store.StateInProgress {
		t.Errorf("state = %v, err %v", it.State, err)
	}
}

func TestTransitionRejectsIllegal(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	// book is done: done→abandoned is illegal (only done→in_progress).
	resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["book"]),
		url.Values{"to": {"abandoned"}}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	it, _ := st.GetItem(context.Background(), ids["book"])
	if it.State != store.StateDone {
		t.Error("illegal transition must not change state")
	}
}

func TestTransitionUnknownStateAndItem(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	if resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["game"]),
		url.Values{"to": {"vaporized"}}, true); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown state: %d, want 400", resp.StatusCode)
	}
	if resp, _ := postForm(t, srv, "POST", "/items/99999/state",
		url.Values{"to": {"done"}}, true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown item: %d, want 404", resp.StatusCode)
	}
}

func TestReviewOnTerminalItem(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/review", ids["book"]),
		url.Values{"verdict": {"liked"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	// non-terminal rejection
	resp, _ = postForm(t, srv, "POST", fmt.Sprintf("/items/%d/review", ids["tv"]),
		url.Values{"verdict": {"liked"}}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-terminal review: %d, want 400", resp.StatusCode)
	}
}

func TestNotesSaveAndPreview(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, _ := postForm(t, srv, "PUT", fmt.Sprintf("/items/%d/notes", ids["game"]),
		url.Values{"notes": {"**bold** move"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: status = %d", resp.StatusCode)
	}
	it, _ := st.GetItem(context.Background(), ids["game"])
	if it.Notes != "**bold** move" {
		t.Errorf("notes = %q", it.Notes)
	}
	// preview renders markdown but must NOT save
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/notes/preview", ids["game"]),
		url.Values{"notes": {"*draft*"}}, true)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<em>draft</em>") {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	it, _ = st.GetItem(context.Background(), ids["game"])
	if it.Notes != "**bold** move" {
		t.Error("preview must not persist")
	}
}

func TestNonHTMXMutationRedirects(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["game"]),
		url.Values{"to": {"in_progress"}}, false)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
}

func TestItemRefresh(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	before, err := st.GetItem(context.Background(), ids["movie"])
	if err != nil {
		t.Fatal(err)
	}
	if before.RefreshedAt != nil {
		t.Fatalf("refreshed_at = %v, want nil before refresh", before.RefreshedAt)
	}
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/refresh", ids["movie"]),
		url.Values{}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	after, err := st.GetItem(context.Background(), ids["movie"])
	if err != nil {
		t.Fatal(err)
	}
	if after.RefreshedAt == nil {
		t.Error("refreshed_at still nil after refresh")
	}
}

func TestItemRefreshFrozen(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/refresh", ids["book"]),
		url.Values{}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestItemRefreshNotFound(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, _ := postForm(t, srv, "POST", "/items/99999/refresh", url.Values{}, true)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGlobalRefreshAccepted(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := postForm(t, srv, "POST", "/refresh", url.Values{}, true)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Refresh") {
		t.Errorf("body = %q, want it to mention Refresh", body)
	}
	// A second, immediate POST must also succeed regardless of whether
	// the first cycle has finished — no strict sequencing assumed.
	resp2, _ := postForm(t, srv, "POST", "/refresh", url.Values{}, true)
	if resp2.StatusCode != http.StatusAccepted {
		t.Errorf("second refresh status = %d, want 202", resp2.StatusCode)
	}
}

func TestLayoutEnables4xxSwaps(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := get(t, srv, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "htmx-config") {
		t.Error("layout must include htmx-config meta tag")
	}
	if !strings.Contains(body, `"4..","swap":true`) {
		t.Error("htmx-config must enable swaps for 4xx responses")
	}
}

func TestGlobalRefreshTracksWaitGroup(t *testing.T) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	refresher := ingest.NewRefresher(ingest.Deps{
		Store:      st,
		Registry:   providers.NewRegistry(),
		Logger:     logger,
		DataDir:    dataDir,
		HTTPClient: http.DefaultClient,
	}, time.Hour)

	// Create server with a real wait group
	var wg sync.WaitGroup
	srv := httptest.NewServer(New(Deps{
		Store:           st,
		Logger:          logger,
		DataDir:         dataDir,
		RefreshInterval: 7 * 24 * time.Hour,
		Refresher:       refresher,
		Background:      &wg,
	}))
	t.Cleanup(srv.Close)

	seedWeb(t, st)

	// POST /refresh starts the goroutine
	resp, body := postForm(t, srv, "POST", "/refresh", url.Values{}, true)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body %s", resp.StatusCode, body)
	}

	// Wait for the goroutine to complete with a 5-second timeout
	done := make(chan bool, 1)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Success: goroutine completed and registered with wait group
	case <-time.After(5 * time.Second):
		t.Fatal("wait group never completed; goroutine not properly tracked")
	}
}

// fakeJPEGForTest returns a tiny valid JPEG for tests that exercise the
// cover-download path without hitting a real image host (copied from
// M4's internal/ingest/testutil_test.go helper).
func fakeJPEGForTest(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

// stubSearchProvider is a MetadataProvider double for the add-flow
// handlers: Search returns a fixed candidate list (or an error),
// Hydrate returns fixed details (or an error).
type stubSearchProvider struct {
	candidates []providers.Candidate
	searchErr  error
	details    *providers.ItemDetails
	hydrateErr error
}

func (s stubSearchProvider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return s.candidates, nil
}

func (s stubSearchProvider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	if s.hydrateErr != nil {
		return nil, s.hydrateErr
	}
	return s.details, nil
}

func TestSearchRendersCandidates(t *testing.T) {
	reg := providers.NewRegistry()
	reg.Register(store.TypeMovie, stubSearchProvider{candidates: []providers.Candidate{
		{Provider: "tmdb", ProviderID: "1", MediaType: store.TypeMovie, Title: "Heat", Year: intp(1995)},
		{Provider: "tmdb", ProviderID: "2", MediaType: store.TypeMovie, Title: "Heat Wave", Year: intp(2001)},
	}})
	srv, _, _ := newTestServerWithIngest(t, reg)
	resp, body := get(t, srv, "/search?type=movie&q=heat")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	for _, needle := range []string{"Heat", "Heat Wave", `hx-post="/items"`} {
		if !strings.Contains(body, needle) {
			t.Errorf("search body missing %q, got %s", needle, body)
		}
	}
}

func TestSearchUnconfiguredProvider(t *testing.T) {
	srv, _, _ := newTestServerWithIngest(t, providers.NewRegistry())
	resp, body := get(t, srv, "/search?type=game&q=x")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "No provider configured") {
		t.Errorf("body = %q, want the not-configured hint", body)
	}
}

func TestSearchBadType(t *testing.T) {
	srv, _, _ := newTestServerWithIngest(t, providers.NewRegistry())
	resp, _ := get(t, srv, "/search?type=podcast&q=x")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	resp, body := get(t, srv, "/search?type=movie&q=")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(body) != "" {
		t.Errorf("empty q: status = %d, body = %q, want 200 and empty", resp.StatusCode, body)
	}
}

func TestAddCreatesAndRedirects(t *testing.T) {
	imgData := fakeJPEGForTest(t)
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(imgData)
	}))
	defer imgSrv.Close()
	coverURL := imgSrv.URL + "/poster.jpg"

	reg := providers.NewRegistry()
	reg.Register(store.TypeMovie, stubSearchProvider{details: &providers.ItemDetails{
		MediaType: store.TypeMovie, Title: "Heat", ReleaseYear: intp(1995),
		Genres: []string{"Crime"}, CoverURL: &coverURL, Provider: "tmdb", ProviderID: "949",
		Ratings: []providers.Rating{{Source: "imdb", Score: 82, Display: "8.2/10"}},
	}})
	srv, st, dataDir := newTestServerWithIngest(t, reg)

	resp, body := postForm(t, srv, "POST", "/items",
		url.Values{"type": {"movie"}, "provider_id": {"949"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	redirect := resp.Header.Get("HX-Redirect")
	if !strings.HasSuffix(redirect, "?flash=added") || !strings.Contains(redirect, "/items/") {
		t.Fatalf("HX-Redirect = %q, want /items/{id}?flash=added", redirect)
	}

	idStr := strings.TrimSuffix(strings.TrimPrefix(redirect, "/items/"), "?flash=added")
	items, err := st.ListItems(context.Background(), store.ListFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("ListItems = %+v, err %v, want one persisted item", items, err)
	}
	if fmt.Sprint(items[0].ID) != idStr {
		t.Errorf("redirect id = %q, want %d", idStr, items[0].ID)
	}
	ratings, err := st.GetRatings(context.Background(), items[0].ID)
	if err != nil || len(ratings) != 1 {
		t.Fatalf("ratings = %+v, err %v, want one persisted rating", ratings, err)
	}
	if items[0].CoverPath == nil {
		t.Fatal("CoverPath is nil, want a saved cover path")
	}
	if _, err := os.Stat(filepath.Join(dataDir, *items[0].CoverPath)); err != nil {
		t.Errorf("cover file missing on disk: %v", err)
	}
}

func TestAddDuplicateFlash(t *testing.T) {
	reg := providers.NewRegistry()
	reg.Register(store.TypeMovie, stubSearchProvider{details: &providers.ItemDetails{
		MediaType: store.TypeMovie, Title: "Heat", ReleaseYear: intp(1995),
		Provider: "tmdb", ProviderID: "949",
	}})
	srv, _, _ := newTestServerWithIngest(t, reg)

	first, _ := postForm(t, srv, "POST", "/items", url.Values{"type": {"movie"}, "provider_id": {"949"}}, true)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first add: status = %d", first.StatusCode)
	}
	second, _ := postForm(t, srv, "POST", "/items", url.Values{"type": {"movie"}, "provider_id": {"949"}}, true)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second add: status = %d", second.StatusCode)
	}
	redirect := second.Header.Get("HX-Redirect")
	if !strings.HasSuffix(redirect, "?flash=duplicate") {
		t.Errorf("HX-Redirect = %q, want ?flash=duplicate", redirect)
	}
}

func TestAddHydrateFailure502(t *testing.T) {
	reg := providers.NewRegistry()
	reg.Register(store.TypeMovie, stubSearchProvider{hydrateErr: fmt.Errorf("upstream down")})
	srv, _, _ := newTestServerWithIngest(t, reg)

	resp, _ := postForm(t, srv, "POST", "/items", url.Values{"type": {"movie"}, "provider_id": {"949"}}, true)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// TestAddUnknownProviderType400 covers a registry miss on POST /items: no
// provider is configured for the media type, which is a user/admin-input
// problem (spec §5 class 2), not an upstream failure — 400, not 502.
func TestAddUnknownProviderType400(t *testing.T) {
	srv, _, _ := newTestServerWithIngest(t, providers.NewRegistry())

	resp, _ := postForm(t, srv, "POST", "/items", url.Values{"type": {"game"}, "provider_id": {"1"}}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestAddStoreFailure500 covers a system failure inside ingest.Add's
// persistence step (spec §5 class 3): hydrate succeeds, but the store is
// unavailable. That must not collapse into the same 502 hydrate failures
// get — it's our own system failing, not an upstream provider.
func TestAddStoreFailure500(t *testing.T) {
	reg := providers.NewRegistry()
	reg.Register(store.TypeMovie, stubSearchProvider{details: &providers.ItemDetails{
		MediaType: store.TypeMovie, Title: "Heat", ReleaseYear: intp(1995),
		Provider: "tmdb", ProviderID: "949",
	}})
	srv, st, _ := newTestServerWithIngest(t, reg)
	st.Close() // simulate a dead database: hydrate succeeds, CreateItem fails

	resp, _ := postForm(t, srv, "POST", "/items", url.Values{"type": {"movie"}, "provider_id": {"949"}}, true)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDetailRendersFlashBanner(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, body := get(t, srv, fmt.Sprintf("/items/%d?flash=duplicate", ids["game"]))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Already in your library") {
		t.Error("detail missing the duplicate flash banner")
	}
}

func TestSettingsRenders(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := get(t, srv, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{
		"Netflix",        // seeded service
		"not configured", // no keys in test Deps
		"Refresh now",
		`value="l" checked`,
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("settings missing %q, got %s", needle, body)
		}
	}
}

func TestSettingsToggleAffectsAvailability(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st) // seedWeb already subscribes netflix
	ctx := context.Background()

	svcs, err := st.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var netflixSubscribed bool
	for _, sv := range svcs {
		if sv.Slug == "netflix" {
			netflixSubscribed = sv.Subscribed
		}
	}
	if !netflixSubscribed {
		t.Fatal("precondition: netflix must start subscribed per seedWeb")
	}

	// Toggle OFF via the HTTP endpoint.
	resp, body := postForm(t, srv, "POST", "/settings/services", url.Values{"slug": {"netflix"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle off: status = %d, body %s", resp.StatusCode, body)
	}
	_, body = get(t, srv, "/movies-tv?available=1")
	if strings.Contains(body, "Dune: Part Two") {
		t.Error("unsubscribed netflix must exclude Dune: Part Two from available-to-me")
	}

	// Toggle back ON via the HTTP endpoint.
	resp, body = postForm(t, srv, "POST", "/settings/services", url.Values{"slug": {"netflix"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle on: status = %d, body %s", resp.StatusCode, body)
	}
	_, body = get(t, srv, "/movies-tv?available=1")
	if !strings.Contains(body, "Dune: Part Two") {
		t.Error("resubscribed netflix must include Dune: Part Two in available-to-me")
	}
}

func TestSettingsDensityPersists(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)

	resp, body := postForm(t, srv, "POST", "/settings/density", url.Values{"density": {"m"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	_, body = get(t, srv, "/games")
	if !strings.Contains(body, "density-m") {
		t.Error("games tab must honor persisted density-m")
	}

	resp, _ = postForm(t, srv, "POST", "/settings/density", url.Values{"density": {"huge"}}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad density: status = %d, want 400", resp.StatusCode)
	}
}

func TestSettingsUnknownSlug404(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, _ := postForm(t, srv, "POST", "/settings/services", url.Values{"slug": {"nope"}}, true)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSettingsProviderStatus(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := httptest.NewServer(New(Deps{
		Store:     st,
		Logger:    logger,
		DataDir:   dataDir,
		Providers: ProviderStatus{TMDB: true},
	}))
	t.Cleanup(srv.Close)

	resp, body := get(t, srv, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "configured") {
		t.Error("settings missing a configured chip for TMDB")
	}
	if !strings.Contains(body, "not configured") {
		t.Error("settings missing not-configured rows for the other four providers")
	}
}

func TestSettingsShowsProviderLastSuccessAndSnapshotAges(t *testing.T) {
	srv, st, dataDir := newTestServer(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "provider_last_success_tmdb", "2026-07-09 08:00:00"); err != nil {
		t.Fatal(err)
	}

	catalogsDir := filepath.Join(dataDir, "catalogs")
	if err := os.MkdirAll(catalogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSnapshot := func(slug, fetchedAt string) {
		data := fmt.Sprintf(`{"fetched_at":%q,"entries":[]}`, fetchedAt)
		if err := os.WriteFile(filepath.Join(catalogsDir, slug+".json"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSnapshot("game_pass", "2026-07-09 07:30:00")
	writeSnapshot("steam_owned", "2026-07-09 07:45:00")
	// ps_plus intentionally left unwritten: the syncer is a placeholder
	// (plan decision 2), so it must honestly render "never".

	resp, body := get(t, srv, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{
		"last success: 2026-07-09 08:00:00", // tmdb provider health
		"2026-07-09 07:30:00",               // game_pass snapshot age
		"2026-07-09 07:45:00",               // steam_owned snapshot age
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("settings missing %q, got %s", needle, body)
		}
	}
	if strings.Count(body, "never") < 1 {
		t.Errorf("settings missing ps_plus 'never' fallback, got %s", body)
	}
}

func TestSettingsSnapshotAgesAndProviderHealthNeverOnFreshDir(t *testing.T) {
	srv, _, _ := newTestServer(t)
	resp, body := get(t, srv, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{"Game Pass", "PS Plus", "Steam owned"} {
		if !strings.Contains(body, needle) {
			t.Errorf("settings missing snapshot label %q, got %s", needle, body)
		}
	}
	// Last refresh + three catalog ages + tmdb/igdb provider health, all
	// unset on a fresh dir.
	if got := strings.Count(body, "never"); got < 5 {
		t.Errorf(`settings has %d "never" occurrences on a fresh dir, want at least 5`, got)
	}
}

func TestDetailAndSettingsShowStaleAvailability(t *testing.T) {
	// A tiny RefreshInterval makes the 2×-interval staleness threshold
	// breach almost immediately, letting the test avoid backdating rows
	// through unexported store internals it can't reach.
	srv, st, _ := newTestServerWithInterval(t, time.Millisecond)
	ctx := context.Background()
	it, _, err := st.CreateItem(ctx, store.NewItem{
		MediaType: store.TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAvailability(ctx, it.ID, []store.Availability{
		{ServiceSlug: "netflix", Kind: store.KindSubscription}}); err != nil {
		t.Fatal(err)
	}
	// fetched_at has 1-second resolution (store.TimeFormat); sleep past a
	// full second so the row is stale regardless of where within the
	// current second the insert landed.
	time.Sleep(1200 * time.Millisecond)

	resp, body := get(t, srv, fmt.Sprintf("/items/%d", it.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, `class="k stalek"`) {
		t.Errorf("detail missing stale chip marker: %s", body)
	}

	_, body = get(t, srv, "/settings")
	if !strings.Contains(body, "1 stale availability rows") {
		t.Errorf("settings missing stale availability count: %s", body)
	}
}

func TestDetailAvailabilityNotStaleByDefault(t *testing.T) {
	srv, st, _ := newTestServer(t) // default week-long RefreshInterval
	ids := seedWeb(t, st)

	_, body := get(t, srv, fmt.Sprintf("/items/%d", ids["movie"]))
	if strings.Contains(body, "stalek") {
		t.Error("freshly seeded availability must not render as stale under the default refresh interval")
	}

	_, body = get(t, srv, "/settings")
	if !strings.Contains(body, "No stale availability") {
		t.Errorf("settings must report no stale availability by default: %s", body)
	}
}

func TestAddNonHTMXRedirects303(t *testing.T) {
	reg := providers.NewRegistry()
	reg.Register(store.TypeMovie, stubSearchProvider{details: &providers.ItemDetails{
		MediaType: store.TypeMovie, Title: "Heat", ReleaseYear: intp(1995),
		Provider: "tmdb", ProviderID: "949",
	}})
	srv, _, _ := newTestServerWithIngest(t, reg)

	resp, _ := postForm(t, srv, "POST", "/items", url.Values{"type": {"movie"}, "provider_id": {"949"}}, false)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "flash=added") {
		t.Errorf("Location = %q, want flash=added", loc)
	}
}
