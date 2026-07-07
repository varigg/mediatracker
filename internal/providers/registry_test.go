package providers

import (
	"context"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

type stubProvider struct{ name string }

func (s stubProvider) Search(ctx context.Context, q string) ([]Candidate, error) {
	return nil, nil
}

func (s stubProvider) Hydrate(ctx context.Context, id string) (*ItemDetails, error) {
	return nil, nil
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	movie := stubProvider{name: "movie"}
	r.Register(store.TypeMovie, movie)

	got, err := r.Get(store.TypeMovie)
	if err != nil {
		t.Fatalf("Get(movie) error = %v", err)
	}
	if got.(stubProvider).name != "movie" {
		t.Errorf("Get(movie) returned wrong provider")
	}

	if _, err := r.Get(store.TypeGame); err == nil {
		t.Error("Get(game) on empty registration should error")
	}
}
