package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTouchRefreshedBumpsTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if it.RefreshedAt != nil {
		t.Fatalf("new item RefreshedAt = %v, want nil", it.RefreshedAt)
	}
	if err := s.TouchRefreshed(ctx, it.ID); err != nil {
		t.Fatalf("TouchRefreshed: %v", err)
	}
	got, err := s.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt == nil {
		t.Fatal("RefreshedAt still nil after TouchRefreshed")
	}
}

func TestTouchRefreshedNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.TouchRefreshed(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestActiveItemsByRefreshDueOrdersOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	a := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	b := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "B", Provider: "tmdb", ProviderID: "2"})
	done := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Done", Provider: "tmdb", ProviderID: "3"})
	if err := s.UpdateState(ctx, done.ID, StateDone); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	// B refreshed once, A never — A (NULL) must sort first.
	if err := s.TouchRefreshed(ctx, b.ID); err != nil {
		t.Fatalf("TouchRefreshed: %v", err)
	}

	items, err := s.ActiveItemsByRefreshDue(ctx)
	if err != nil {
		t.Fatalf("ActiveItemsByRefreshDue: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (done/abandoned excluded): %+v", len(items), items)
	}
	if items[0].ID != a.ID || items[1].ID != b.ID {
		t.Errorf("order = [%d, %d], want [never-refreshed A, then B]", items[0].ID, items[1].ID)
	}
}

func TestNewlyAvailableFiltersBySubscribedAndFirstSeen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	item := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	other := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Old News", Provider: "tmdb", ProviderID: "950"})

	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatalf("SetServiceSubscribed: %v", err)
	}
	cutoff := time.Now().UTC().Add(-time.Hour)

	// item: newly seen on a subscribed service -> must appear.
	if err := s.UpsertAvailability(ctx, item.ID, []Availability{{ServiceSlug: "netflix", Kind: "subscription"}}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}
	// other: seen on a service the user isn't subscribed to -> must not appear.
	if err := s.UpsertAvailability(ctx, other.ID, []Availability{{ServiceSlug: "hulu", Kind: "subscription"}}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}

	got, err := s.NewlyAvailable(ctx, cutoff)
	if err != nil {
		t.Fatalf("NewlyAvailable: %v", err)
	}
	if len(got) != 1 || got[0].ID != item.ID {
		t.Errorf("NewlyAvailable = %+v, want just %q", got, item.Title)
	}
}

func TestNewlyAvailableExcludesBeforeCutoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	item := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatalf("SetServiceSubscribed: %v", err)
	}
	if err := s.UpsertAvailability(ctx, item.ID, []Availability{{ServiceSlug: "netflix", Kind: "subscription"}}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour)

	got, err := s.NewlyAvailable(ctx, future)
	if err != nil {
		t.Fatalf("NewlyAvailable: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("NewlyAvailable with future cutoff = %+v, want none", got)
	}
}
