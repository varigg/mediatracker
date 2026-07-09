package store

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// seedListFixture creates four items with distinct types, states, genres,
// ratings, and availability. added-order (and thus id-order) is Alpha,
// Bravo, Charlie, Delta.
func seedListFixture(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	alpha := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Alpha",
		ReleaseYear: intPtr(2001), Genres: []string{"Drama"}, Provider: "tmdb", ProviderID: "1"})
	bravo := mustCreate(t, s, NewItem{MediaType: TypeTV, Title: "Bravo",
		ReleaseYear: intPtr(1999), Genres: []string{"Comedy"}, Provider: "tmdb", ProviderID: "2"})
	charlie := mustCreate(t, s, NewItem{MediaType: TypeBook, Title: "Charlie",
		ReleaseYear: intPtr(2010), Genres: []string{"Drama"}, Provider: "openlibrary", ProviderID: "3"})
	delta := mustCreate(t, s, NewItem{MediaType: TypeGame, Title: "Delta",
		ReleaseYear: intPtr(2020), Provider: "igdb", ProviderID: "4"})

	if err := s.UpdateState(ctx, charlie.ID, StateInProgress); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRatings(ctx, alpha.ID, []Rating{{Source: "imdb", Score: 90, Display: "9.0/10"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRatings(ctx, bravo.ID, []Rating{{Source: "imdb", Score: 70, Display: "7.0/10"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAvailability(ctx, bravo.ID, []Availability{
		{ServiceSlug: "netflix", Kind: "subscription"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAvailability(ctx, delta.ID, []Availability{
		{ServiceSlug: "steam", Kind: "owned", URL: strPtr("https://store.steampowered.com/app/4")},
	}); err != nil {
		t.Fatal(err)
	}
}

func titles(items []MediaItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func TestListItems(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s)
	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		params url.Values
		want   []string // titles in expected order
	}{
		{"no filters, default added-desc sort", url.Values{},
			[]string{"Delta", "Charlie", "Bravo", "Alpha"}},
		{"state filter", url.Values{"state": {"want_to"}},
			[]string{"Delta", "Bravo", "Alpha"}},
		{"movies-tv tab types", url.Values{"type": {"movie", "tv"}},
			[]string{"Bravo", "Alpha"}},
		{"genre filter", url.Values{"genre": {"Drama"}},
			[]string{"Charlie", "Alpha"}},
		{"available to me: subscription + owned", url.Values{"available": {"1"}},
			[]string{"Delta", "Bravo"}},
		{"sort title", url.Values{"sort": {"title"}},
			[]string{"Alpha", "Bravo", "Charlie", "Delta"}},
		{"sort year desc", url.Values{"sort": {"year"}},
			[]string{"Delta", "Charlie", "Alpha", "Bravo"}},
		{"sort rating desc, unrated last by title", url.Values{"sort": {"rating"}},
			[]string{"Alpha", "Bravo", "Charlie", "Delta"}},
		{"combined state+type+sort", url.Values{"state": {"want_to"}, "type": {"movie", "tv"}, "sort": {"title"}},
			[]string{"Alpha", "Bravo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := ParseListFilter(c.params)
			if err != nil {
				t.Fatalf("ParseListFilter(%v): %v", c.params, err)
			}
			items, err := s.ListItems(ctx, f)
			if err != nil {
				t.Fatalf("ListItems(%v): %v", c.params, err)
			}
			got := titles(items)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestListItemsUnsubscribedServiceNotAvailable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s)
	// netflix NOT subscribed: only the owned game counts.
	f, err := ParseListFilter(url.Values{"available": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListItems(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	got := titles(items)
	if len(got) != 1 || got[0] != "Delta" {
		t.Errorf("got %v, want [Delta]", got)
	}
}

func TestParseListFilterRejectsInvalidParams(t *testing.T) {
	for _, v := range []url.Values{
		{"state": {"pending"}},
		{"type": {"podcast"}},
		{"sort": {"popularity"}},
	} {
		if _, err := ParseListFilter(v); err == nil {
			t.Errorf("ParseListFilter(%v): want error, got nil", v)
		} else if !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("ParseListFilter(%v): err = %v, want errors.Is(err, ErrInvalidQuery)", v, err)
		}
	}
}

func TestParseListFilterDir(t *testing.T) {
	f, err := ParseListFilter(url.Values{"sort": {"year"}, "dir": {"asc"}})
	if err != nil || f.Dir != "asc" {
		t.Fatalf("ParseListFilter dir=asc = (%+v, %v)", f, err)
	}
	if _, err := ParseListFilter(url.Values{"dir": {"sideways"}}); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("invalid dir: err = %v, want ErrInvalidQuery", err)
	}
}

func TestListItemsSortDirection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s)

	items, err := s.ListItems(ctx, ListFilter{Sort: "year", Dir: "asc"})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	got := titles(items)
	want := []string{"Bravo", "Alpha", "Charlie", "Delta"} // 1999,2001,2010,2020
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("year asc: got %v, want %v", got, want)
		}
	}

	items, err = s.ListItems(ctx, ListFilter{Sort: "title", Dir: "desc"})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if got := titles(items); got[0] != "Delta" {
		t.Fatalf("title desc: got %v, want Delta first", got)
	}
}

func TestDefaultDir(t *testing.T) {
	// title is the one ascending-by-default sort; everything else
	// (including the empty/unset sort) defaults to descending. Pinned
	// directly since buildListQuery's ORDER BY and internal/server's
	// sortLink both derive their default from this single function.
	cases := map[string]string{
		"title": "asc", "": "desc", "added": "desc", "year": "desc", "rating": "desc",
	}
	for sort, want := range cases {
		if got := DefaultDir(sort); got != want {
			t.Errorf("DefaultDir(%q) = %q, want %q", sort, got, want)
		}
	}
}

func TestBuildListQueryNormalizesBogusDir(t *testing.T) {
	// A hand-constructed filter bypassing ParseListFilter must not be
	// able to inject into ORDER BY: bogus directions fall back to the
	// sort's default.
	q, _ := buildListQuery(ListFilter{Sort: "title", Dir: "evil; DROP TABLE"})
	if !strings.Contains(q, "COLLATE NOCASE ASC") || strings.Contains(q, "evil") {
		t.Errorf("bogus dir leaked into SQL: %q", q)
	}
}
