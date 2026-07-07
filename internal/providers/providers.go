// Package providers defines the metadata-provider abstraction: search
// candidates, hydrated item details, rating normalization, and the
// media_type → provider registry. Adapter implementations live in
// subpackages (tmdb, books, igdb).
package providers

import (
	"context"

	"github.com/varigg/mediatracker/internal/store"
)

// Candidate is one search result rendered in the add-flow picker.
type Candidate struct {
	Provider       string // 'tmdb' | 'openlibrary' | 'igdb'
	ProviderID     string
	MediaType      store.MediaType
	Title          string
	Year           *int
	ThumbnailURL   *string
	Disambiguation string // overview / authors / platforms
}

// Rating is a normalized rating from one source, not yet bound to an item.
type Rating struct {
	Source  string
	Score   int    // 0–100
	Display string // original scale, e.g. "7.9/10"
	URL     *string
}

// ItemDetails is the result of Hydrate: everything needed to persist a
// media_items row plus its ratings rows. Availability is produced by the
// M3 AvailabilityProvider enrichers, never here.
type ItemDetails struct {
	MediaType   store.MediaType
	Title       string
	ReleaseYear *int
	Genres      []string
	CoverURL    *string // remote URL; download is the M4 add flow's job
	Provider    string
	ProviderID  string
	Metadata    map[string]any // type-specific residue, serialized at persist
	Ratings     []Rating
}

type MetadataProvider interface {
	Search(ctx context.Context, query string) ([]Candidate, error)
	Hydrate(ctx context.Context, providerID string) (*ItemDetails, error)
}
