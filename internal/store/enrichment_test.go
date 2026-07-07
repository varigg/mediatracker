package store

import (
	"context"
	"errors"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestReplaceRatings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})

	first := []Rating{
		{Source: "imdb", Score: 82, Display: "8.2/10", URL: strPtr("https://www.imdb.com/title/tt0113277/")},
		{Source: "rotten_tomatoes", Score: 88, Display: "88%"},
	}
	if err := s.ReplaceRatings(ctx, it.ID, first); err != nil {
		t.Fatalf("ReplaceRatings: %v", err)
	}

	second := []Rating{{Source: "imdb", Score: 83, Display: "8.3/10"}}
	if err := s.ReplaceRatings(ctx, it.ID, second); err != nil {
		t.Fatalf("second ReplaceRatings: %v", err)
	}

	got, err := s.GetRatings(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetRatings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ratings = %d rows, want 1 (replace, not merge)", len(got))
	}
	if got[0].Source != "imdb" || got[0].Score != 83 || got[0].Display != "8.3/10" {
		t.Errorf("rating = %+v", got[0])
	}
}

func TestUpsertAvailabilityPreservesFirstSeen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})

	if err := s.UpsertAvailability(ctx, it.ID, []Availability{
		{ServiceSlug: "netflix", Kind: "subscription", URL: strPtr("https://www.netflix.com/title/1")},
	}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}

	// Backdate so a preserved first_seen_at is distinguishable from a
	// same-second rewrite.
	if _, err := s.db.ExecContext(ctx, `UPDATE availability
		SET first_seen_at = '2000-01-01 00:00:00', fetched_at = '2000-01-01 00:00:00'
		WHERE item_id = ?`, it.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertAvailability(ctx, it.ID, []Availability{
		{ServiceSlug: "netflix", Kind: "subscription", URL: strPtr("https://www.netflix.com/title/2")},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	rows, err := s.GetAvailability(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetAvailability: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("availability = %d rows, want 1", len(rows))
	}
	if rows[0].FirstSeenAt != "2000-01-01 00:00:00" {
		t.Errorf("first_seen_at = %q, want preserved 2000-01-01 00:00:00", rows[0].FirstSeenAt)
	}
	if rows[0].FetchedAt == "2000-01-01 00:00:00" {
		t.Error("fetched_at not bumped on upsert")
	}
	if rows[0].URL == nil || *rows[0].URL != "https://www.netflix.com/title/2" {
		t.Errorf("url = %v, want updated title/2", rows[0].URL)
	}
}

func TestSetServiceSubscribed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatalf("SetServiceSubscribed: %v", err)
	}
	services, err := s.ListServices(ctx)
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	found := false
	for _, svc := range services {
		if svc.Slug == "netflix" {
			found = true
			if !svc.Subscribed {
				t.Error("netflix not marked subscribed")
			}
		} else if svc.Subscribed {
			t.Errorf("%s unexpectedly subscribed", svc.Slug)
		}
	}
	if !found {
		t.Fatal("netflix missing from seeded services")
	}

	if err := s.SetServiceSubscribed(ctx, "no_such_service", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown slug err = %v, want ErrNotFound", err)
	}
}
