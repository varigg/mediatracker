package store

import (
	"context"
	"testing"
)

func TestGroupStateCounts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s) // Alpha movie/want_to, Bravo tv/want_to, Charlie book/in_progress, Delta game/want_to

	counts, err := s.GroupStateCounts(ctx)
	if err != nil {
		t.Fatalf("GroupStateCounts: %v", err)
	}
	if counts[TypeMovie][StateWantTo] != 1 || counts[TypeTV][StateWantTo] != 1 {
		t.Errorf("video counts = %v", counts)
	}
	if counts[TypeBook][StateInProgress] != 1 {
		t.Errorf("book counts = %v", counts)
	}
	if counts[TypeGame][StateWantTo] != 1 {
		t.Errorf("game counts = %v", counts)
	}
}
