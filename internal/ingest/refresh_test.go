package ingest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

type stubSyncer struct {
	stubAvailability
	syncErr   error
	syncCalls int
}

func (s *stubSyncer) SyncCycle(ctx context.Context) error {
	s.syncCalls++
	return s.syncErr
}

func newRefresherDeps(t *testing.T, p providers.MetadataProvider, avail ...providers.AvailabilityProvider) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	registry := providers.NewRegistry()
	if p != nil {
		registry.Register(store.TypeMovie, p)
	}
	d := Deps{
		Store:        st,
		Registry:     registry,
		Availability: avail,
		HTTPClient:   http.DefaultClient,
		DataDir:      t.TempDir(),
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Now:          time.Now,
	}
	return d, st
}

func TestRunCycleRefreshesActiveItemsOnly(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})

	active, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "Active", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	done, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "Done", Provider: "tmdb", ProviderID: "2"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if err := st.UpdateState(ctx, done.ID, store.StateDone); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	sum, err := r.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if sum.Items != 1 {
		t.Errorf("sum.Items = %d, want 1 (done item excluded)", sum.Items)
	}

	got, err := st.GetItem(ctx, active.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt == nil {
		t.Error("active item RefreshedAt still nil after RunCycle")
	}
	avail, err := st.GetAvailability(ctx, active.ID)
	if err != nil || len(avail) != 1 {
		t.Errorf("availability = %+v, err %v, want one row", avail, err)
	}

	gotDone, err := st.GetItem(ctx, done.ID)
	if err != nil {
		t.Fatalf("GetItem done: %v", err)
	}
	if gotDone.RefreshedAt != nil {
		t.Error("done item RefreshedAt touched, want it left frozen")
	}
}

func TestRunCycleSyncsCatalogsBeforeItems(t *testing.T) {
	syncer := &stubSyncer{}
	d, _ := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)}, syncer)

	r := NewRefresher(d, time.Hour)
	if _, err := r.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if syncer.syncCalls != 1 {
		t.Errorf("SyncCycle called %d times, want 1", syncer.syncCalls)
	}
}

func TestRunCycleCountsRatingsFailure(t *testing.T) {
	d, st := newRefresherDeps(t, stubProvider{err: errors.New("upstream down")})
	if _, _, err := st.CreateItem(context.Background(), store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	sum, err := r.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if sum.RatingsFailed != 1 {
		t.Errorf("sum.RatingsFailed = %d, want 1", sum.RatingsFailed)
	}
}

func TestRunCyclePersistsLastRefreshAt(t *testing.T) {
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)})
	r := NewRefresher(d, time.Hour)
	if _, err := r.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	_, ok, err := st.GetSetting(context.Background(), "last_refresh_at")
	if err != nil || !ok {
		t.Errorf("last_refresh_at not persisted: ok=%v err=%v", ok, err)
	}
}

func TestRefreshItemRunsSameCodePathAsCycle(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})
	item, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	outcome, err := r.RefreshItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}
	if outcome.RatingsFailed || outcome.AvailabilityFailed {
		t.Errorf("outcome = %+v, want no failures", outcome)
	}
	got, err := st.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt == nil {
		t.Error("RefreshedAt still nil after RefreshItem")
	}
	avail, err := st.GetAvailability(ctx, item.ID)
	if err != nil || len(avail) != 1 {
		t.Errorf("availability = %+v, err %v, want one row", avail, err)
	}
}

func TestRefreshItemRejectsFrozenItem(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})
	item, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if err := st.UpdateState(ctx, item.ID, store.StateDone); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	_, err = r.RefreshItem(ctx, item.ID)
	if !errors.Is(err, ErrItemNotActive) {
		t.Fatalf("RefreshItem error = %v, want ErrItemNotActive", err)
	}

	got, err := st.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt != nil {
		t.Error("done item RefreshedAt touched, want it left frozen")
	}
	avail, err := st.GetAvailability(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetAvailability: %v", err)
	}
	if len(avail) != 0 {
		t.Errorf("availability = %+v, want no rows written", avail)
	}
}

func TestRunCycleDoesNotWipeRatingsOnEmptyHydrate(t *testing.T) {
	ctx := context.Background()
	// Hydrate succeeds (err == nil) but returns no ratings, as it does
	// when a sub-source (OMDb, Hardcover, IGDB) is transiently down.
	emptyRatingsDetails := &providers.ItemDetails{
		MediaType:   store.TypeMovie,
		Title:       "Heat",
		ReleaseYear: intPtr(1995),
		Genres:      []string{"Crime"},
		Provider:    "tmdb",
		ProviderID:  "949",
		Metadata:    map[string]any{"overview": "A cop and a thief."},
		Ratings:     nil,
	}
	d, st := newRefresherDeps(t, stubProvider{details: emptyRatingsDetails})

	item, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if err := st.ReplaceRatings(ctx, item.ID, []store.Rating{{Source: "imdb", Score: 82, Display: "8.2/10"}}); err != nil {
		t.Fatalf("seed ReplaceRatings: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	sum, err := r.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if sum.RatingsFailed != 0 {
		t.Errorf("sum.RatingsFailed = %d, want 0 (empty ratings on success is not a failure)", sum.RatingsFailed)
	}

	ratings, err := st.GetRatings(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetRatings: %v", err)
	}
	if len(ratings) != 1 || ratings[0].Source != "imdb" {
		t.Errorf("ratings = %+v, want seeded imdb rating preserved", ratings)
	}
}

func TestRefreshItemRecordsProviderLastSuccess(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)})
	fixed := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return fixed }
	item, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	if _, err := r.RefreshItem(ctx, item.ID); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}

	got, ok, err := st.GetSetting(ctx, "provider_last_success_tmdb")
	if err != nil || !ok {
		t.Fatalf("provider_last_success_tmdb not set: ok=%v err=%v", ok, err)
	}
	if want := fixed.Format(store.TimeFormat); got != want {
		t.Errorf("provider_last_success_tmdb = %q, want %q", got, want)
	}
}

func TestRefreshItemHydrateFailureDoesNotRecordProviderSuccess(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{err: errors.New("upstream down")})
	item, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	if _, err := r.RefreshItem(ctx, item.ID); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}

	_, ok, err := st.GetSetting(ctx, "provider_last_success_tmdb")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if ok {
		t.Error("provider_last_success_tmdb set despite hydrate failure")
	}
}

func TestOverdueWhenNeverRun(t *testing.T) {
	d, _ := newRefresherDeps(t, nil)
	r := NewRefresher(d, time.Hour)
	if !r.overdue(context.Background()) {
		t.Error("want overdue=true when last_refresh_at was never set")
	}
}

func TestOverdueFalseWithinInterval(t *testing.T) {
	d, st := newRefresherDeps(t, nil)
	if err := st.SetSetting(context.Background(), "last_refresh_at", time.Now().UTC().Format(store.TimeFormat)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	r := NewRefresher(d, time.Hour)
	if r.overdue(context.Background()) {
		t.Error("want overdue=false right after a cycle completed")
	}
}

func TestNewRefresherDefaultsNowWhenUnset(t *testing.T) {
	d, _ := newRefresherDeps(t, nil)
	d.Now = nil // exercise the zero-value Deps.Now gap NewRefresher must close

	r := NewRefresher(d, time.Hour)

	// Both call sites that invoke r.deps.Now() must not panic.
	if !r.overdue(context.Background()) {
		t.Error("want overdue=true when last_refresh_at was never set")
	}
	if _, err := r.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
}
