package providers

import (
	"context"
	"encoding/json"

	"github.com/varigg/mediatracker/internal/store"
)

// Availability is one availability/ownership fact from an enricher, not
// yet bound to an item or timestamps — the ingest layer adds those when
// persisting via store.UpsertAvailability.
type Availability struct {
	ServiceSlug string
	Kind        string // stream | subscription | owned
	URL         *string
}

// AvailabilityProvider produces availability rows for one item. Game
// providers match locally against cycle-cached snapshots; tmdbWatch
// calls upstream per item. Enrichers self-filter: items they don't
// handle yield (nil, nil).
type AvailabilityProvider interface {
	Refresh(ctx context.Context, item *store.MediaItem) ([]Availability, error)
}

// CycleSyncer is implemented by availability providers that need a
// once-per-cycle upstream fetch (catalog snapshots, owned-games list).
// The refresh orchestrator calls SyncCycle before any per-item Refresh.
type CycleSyncer interface {
	SyncCycle(ctx context.Context) error
}

// NameCandidates returns the item title plus any IGDB alternative names
// carried in metadata, in matching-priority order. Malformed metadata
// degrades to just the title.
func NameCandidates(item *store.MediaItem) []string {
	candidates := []string{item.Title}
	if len(item.Metadata) == 0 {
		return candidates
	}
	var meta struct {
		AlternativeNames []string `json:"alternative_names"`
	}
	if err := json.Unmarshal(item.Metadata, &meta); err != nil {
		return candidates
	}
	return append(candidates, meta.AlternativeNames...)
}
