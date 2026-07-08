package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func intPtr(i int) *int { return &i }

func mustCreate(t *testing.T, s *Store, n NewItem) *MediaItem {
	t.Helper()
	it, _, err := s.CreateItem(context.Background(), n)
	if err != nil {
		t.Fatalf("CreateItem(%s): %v", n.Title, err)
	}
	return it
}

func TestCreateAndGetItem(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	it, created, err := s.CreateItem(ctx, NewItem{
		MediaType:   TypeMovie,
		Title:       "Heat",
		ReleaseYear: intPtr(1995),
		Genres:      []string{"Crime", "Thriller"},
		Provider:    "tmdb",
		ProviderID:  "949",
		Metadata:    json.RawMessage(`{"tmdb_id":949}`),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if it.State != StateWantTo {
		t.Errorf("State = %q, want want_to", it.State)
	}

	got, err := s.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Title != "Heat" || got.Provider != "tmdb" || got.ProviderID != "949" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.Genres, []string{"Crime", "Thriller"}) {
		t.Errorf("Genres = %v", got.Genres)
	}
	if got.ReleaseYear == nil || *got.ReleaseYear != 1995 {
		t.Errorf("ReleaseYear = %v, want 1995", got.ReleaseYear)
	}
	if got.AddedAt == "" {
		t.Error("AddedAt empty")
	}
	if got.Notes != "" || got.Verdict != nil || got.CompletedAt != nil {
		t.Errorf("fresh item has review fields set: %+v", got)
	}
}

func TestCreateItemIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})

	again, created, err := s.CreateItem(ctx, NewItem{
		MediaType: TypeMovie, Title: "Heat (retitled)", Provider: "tmdb", ProviderID: "949",
	})
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if created {
		t.Error("created = true on duplicate, want false")
	}
	if again.ID != first.ID {
		t.Errorf("duplicate returned ID %d, want existing %d", again.ID, first.ID)
	}
	if again.Title != "Heat" {
		t.Errorf("duplicate overwrote title: %q", again.Title)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_items`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestGetItemNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetItem(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateStateLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeGame, Title: "Hades", Provider: "igdb", ProviderID: "113112"})

	if err := s.UpdateState(ctx, it.ID, StateInProgress); err != nil {
		t.Fatalf("want_to→in_progress: %v", err)
	}
	if err := s.UpdateState(ctx, it.ID, StateDone); err != nil {
		t.Fatalf("in_progress→done: %v", err)
	}
	got, _ := s.GetItem(ctx, it.ID)
	if got.State != StateDone {
		t.Errorf("State = %q, want done", got.State)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt not stamped on done")
	}

	// Reopening clears the review fields.
	if err := s.UpdateReview(ctx, it.ID, VerdictLiked, "2026-07-01"); err != nil {
		t.Fatalf("UpdateReview: %v", err)
	}
	if err := s.UpdateState(ctx, it.ID, StateInProgress); err != nil {
		t.Fatalf("done→in_progress: %v", err)
	}
	got, _ = s.GetItem(ctx, it.ID)
	if got.Verdict != nil || got.CompletedAt != nil {
		t.Errorf("reopen kept review fields: verdict=%v completed=%v", got.Verdict, got.CompletedAt)
	}
}

func TestUpdateStateIllegal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeBook, Title: "Dune", Provider: "openlibrary", ProviderID: "OL893415W"})

	if err := s.UpdateState(ctx, it.ID, StateDone); err != nil {
		t.Fatalf("want_to→done: %v", err)
	}
	if err := s.UpdateState(ctx, it.ID, StateAbandoned); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("done→abandoned err = %v, want ErrIllegalTransition", err)
	}
	if err := s.UpdateState(ctx, 999, StateDone); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item err = %v, want ErrNotFound", err)
	}
}

func TestUpdateReviewRequiresTerminalState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Ran", Provider: "tmdb", ProviderID: "11645"})

	if err := s.UpdateReview(ctx, it.ID, VerdictLiked, "2026-07-01"); !errors.Is(err, ErrNotTerminal) {
		t.Errorf("err = %v, want ErrNotTerminal", err)
	}
	if err := s.UpdateReview(ctx, 999, VerdictLiked, "2026-07-01"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item err = %v, want ErrNotFound", err)
	}

	if err := s.UpdateState(ctx, it.ID, StateDone); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReview(ctx, it.ID, VerdictOK, "2026-07-02"); err != nil {
		t.Fatalf("UpdateReview on done item: %v", err)
	}
	got, _ := s.GetItem(ctx, it.ID)
	if got.Verdict == nil || *got.Verdict != VerdictOK {
		t.Errorf("Verdict = %v, want ok", got.Verdict)
	}
	if got.CompletedAt == nil || *got.CompletedAt != "2026-07-02" {
		t.Errorf("CompletedAt = %v, want 2026-07-02", got.CompletedAt)
	}
}

func TestUpdateNotes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeTV, Title: "Severance", Provider: "tmdb", ProviderID: "95396"})

	if err := s.UpdateNotes(ctx, it.ID, "## S1\nGreat."); err != nil {
		t.Fatalf("UpdateNotes: %v", err)
	}
	got, _ := s.GetItem(ctx, it.ID)
	if got.Notes != "## S1\nGreat." {
		t.Errorf("Notes = %q", got.Notes)
	}
	if err := s.UpdateNotes(ctx, 999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item err = %v, want ErrNotFound", err)
	}
}

func TestSetCoverPath(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if it.CoverPath != nil {
		t.Fatalf("new item CoverPath = %v, want nil", it.CoverPath)
	}
	if err := s.SetCoverPath(ctx, it.ID, "covers/1.jpg"); err != nil {
		t.Fatalf("SetCoverPath: %v", err)
	}
	got, err := s.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.CoverPath == nil || *got.CoverPath != "covers/1.jpg" {
		t.Errorf("CoverPath = %v, want covers/1.jpg", got.CoverPath)
	}
}

func TestSetCoverPathNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetCoverPath(context.Background(), 999, "covers/999.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
