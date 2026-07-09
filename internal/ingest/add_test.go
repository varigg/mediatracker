package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

type stubProvider struct {
	details *providers.ItemDetails
	err     error
}

func (s stubProvider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	return nil, nil
}

func (s stubProvider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	return s.details, s.err
}

type stubAvailability struct {
	rows []providers.Availability
	err  error
}

func (s stubAvailability) Refresh(ctx context.Context, item *store.MediaItem) ([]providers.Availability, error) {
	return s.rows, s.err
}

func intPtr(i int) *int { return &i }

func detailsFixture(coverURL *string) *providers.ItemDetails {
	return &providers.ItemDetails{
		MediaType:   store.TypeMovie,
		Title:       "Heat",
		ReleaseYear: intPtr(1995),
		Genres:      []string{"Crime"},
		CoverURL:    coverURL,
		Provider:    "tmdb",
		ProviderID:  "949",
		Metadata:    map[string]any{"overview": "A cop and a thief."},
		Ratings:     []providers.Rating{{Source: "imdb", Score: 82, Display: "8.2/10"}},
	}
}

func newTestDeps(t *testing.T, p providers.MetadataProvider, avail ...providers.AvailabilityProvider) Deps {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	registry := providers.NewRegistry()
	registry.Register(store.TypeMovie, p)

	return Deps{
		Store:        st,
		Registry:     registry,
		Availability: avail,
		HTTPClient:   http.DefaultClient,
		DataDir:      t.TempDir(),
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Now:          time.Now,
	}
}

func TestAddPersistsItemWithRatingsAndAvailability(t *testing.T) {
	d := newTestDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})

	item, created, err := d.Add(context.Background(), store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !created {
		t.Error("created = false, want true on first add")
	}
	if item.Title != "Heat" || item.Provider != "tmdb" || item.ProviderID != "949" {
		t.Errorf("item = %+v, want Heat/tmdb/949", item)
	}

	ratings, err := d.Store.GetRatings(context.Background(), item.ID)
	if err != nil || len(ratings) != 1 || ratings[0].Source != "imdb" {
		t.Errorf("ratings = %+v, err %v, want one imdb row", ratings, err)
	}
	avail, err := d.Store.GetAvailability(context.Background(), item.ID)
	if err != nil || len(avail) != 1 || avail[0].ServiceSlug != "netflix" {
		t.Errorf("availability = %+v, err %v, want one netflix row", avail, err)
	}
}

func TestAddDownloadsCover(t *testing.T) {
	imgData := fakeJPEGForTest(t)
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(imgData)
	}))
	defer imgSrv.Close()
	coverURL := imgSrv.URL + "/poster.jpg"

	d := newTestDeps(t, stubProvider{details: detailsFixture(&coverURL)})
	item, _, err := d.Add(context.Background(), store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.CoverPath == nil {
		t.Fatal("CoverPath is nil, want a saved cover path")
	}
	if _, err := os.Stat(filepath.Join(d.DataDir, *item.CoverPath)); err != nil {
		t.Errorf("cover file missing on disk: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(item.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["cover_url"] != coverURL {
		t.Errorf("metadata cover_url = %v, want %q (kept for re-fetch)", meta["cover_url"], coverURL)
	}
}

func TestAddHydrateFailureAborts(t *testing.T) {
	d := newTestDeps(t, stubProvider{err: errors.New("upstream down")})
	_, _, err := d.Add(context.Background(), store.TypeMovie, "949")
	if err == nil {
		t.Fatal("want error when Hydrate fails")
	}
	items, err := d.Store.ListItems(context.Background(), store.ListFilter{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items = %+v, want none persisted on hydrate failure", items)
	}
}

func TestAddDegradesOnCoverAndAvailabilityFailure(t *testing.T) {
	badCoverURL := "http://127.0.0.1:1/missing.jpg" // nothing listens here
	d := newTestDeps(t, stubProvider{details: detailsFixture(&badCoverURL)},
		stubAvailability{err: errors.New("provider down")})

	item, _, err := d.Add(context.Background(), store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("Add must degrade, not fail: %v", err)
	}
	if item.CoverPath != nil {
		t.Errorf("CoverPath = %v, want nil (cover fetch failed)", item.CoverPath)
	}
	avail, err := d.Store.GetAvailability(context.Background(), item.ID)
	if err != nil || len(avail) != 0 {
		t.Errorf("availability = %+v, err %v, want none (provider failed)", avail, err)
	}
}

func TestAddIdempotentReAddReturnsExistingItem(t *testing.T) {
	d := newTestDeps(t, stubProvider{details: detailsFixture(nil)})
	first, created, err := d.Add(context.Background(), store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if !created {
		t.Error("created = false on first add, want true")
	}
	second, created, err := d.Add(context.Background(), store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if created {
		t.Error("created = true on re-add, want false")
	}
	if second.ID != first.ID {
		t.Errorf("re-add ID = %d, want existing item's ID %d", second.ID, first.ID)
	}
}
