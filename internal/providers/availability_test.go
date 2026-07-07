package providers

import (
	"encoding/json"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func TestNameCandidates(t *testing.T) {
	item := &store.MediaItem{
		Title:    "The Witcher 3: Wild Hunt",
		Metadata: json.RawMessage(`{"alternative_names": ["TW3", "Wiedźmin 3: Dziki Gon"], "summary": "x"}`),
	}
	got := NameCandidates(item)
	want := []string{"The Witcher 3: Wild Hunt", "TW3", "Wiedźmin 3: Dziki Gon"}
	if len(got) != len(want) {
		t.Fatalf("NameCandidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNameCandidatesToleratesBadMetadata(t *testing.T) {
	for _, meta := range []json.RawMessage{nil, json.RawMessage(`not json`), json.RawMessage(`{}`)} {
		item := &store.MediaItem{Title: "Grounded", Metadata: meta}
		got := NameCandidates(item)
		if len(got) != 1 || got[0] != "Grounded" {
			t.Errorf("NameCandidates(meta=%s) = %v, want just the title", meta, got)
		}
	}
}
