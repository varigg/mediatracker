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
	Kind        store.Kind
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

// Game-metadata keys written by igdb.Hydrate and read by the
// game-catalog/ownership enrichers. Named here, alongside
// NameCandidates, rather than in gamecatalogs or steam, because moving
// them there would create an import cycle (both depend on this package
// for Availability et al.).
const (
	MetadataKeyAlternativeNames = "alternative_names"
	MetadataKeySteamAppID       = "steam_appid"
)

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

// SteamAppID returns the IGDB-supplied Steam app ID from an item's
// metadata, if present.
//
// The struct tag below stays the literal "steam_appid" — Go struct tags
// cannot reference a named constant — but the map-key WRITE side in
// igdb uses MetadataKeySteamAppID.
func SteamAppID(item *store.MediaItem) (int64, bool) {
	if len(item.Metadata) == 0 {
		return 0, false
	}
	var meta struct {
		SteamAppID int64 `json:"steam_appid"`
	}
	if err := json.Unmarshal(item.Metadata, &meta); err != nil || meta.SteamAppID == 0 {
		return 0, false
	}
	return meta.SteamAppID, true
}
