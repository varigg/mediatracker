package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/varigg/mediatracker/internal/covers"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

func toStoreRatings(itemID int64, in []providers.Rating) []store.Rating {
	out := make([]store.Rating, len(in))
	for i, r := range in {
		out[i] = store.Rating{ItemID: itemID, Source: r.Source, Score: r.Score, Display: r.Display, URL: r.URL}
	}
	return out
}

func toStoreAvailability(itemID int64, in []providers.Availability) []store.Availability {
	out := make([]store.Availability, len(in))
	for i, a := range in {
		out[i] = store.Availability{ItemID: itemID, ServiceSlug: a.ServiceSlug, Kind: a.Kind, URL: a.URL}
	}
	return out
}

// Add runs the synchronous add-flow: hydrate the picked candidate,
// persist it, then best-effort cover download, ratings, and
// availability. Only a Hydrate failure aborts — everything after
// persistence degrades the item with gaps rather than failing the add.
// A duplicate add (same provider/provider_id) returns the existing item
// untouched, with no re-enrichment; the bool reports whether a new item
// was created (false on the duplicate path).
func (d Deps) Add(ctx context.Context, mediaType store.MediaType, providerID string) (*store.MediaItem, bool, error) {
	p, err := d.Registry.Get(mediaType)
	if err != nil {
		return nil, false, err
	}
	details, err := p.Hydrate(ctx, providerID)
	if err != nil {
		return nil, false, fmt.Errorf("ingest: hydrate %s %s: %w", mediaType, providerID, err)
	}

	metadata := make(map[string]any, len(details.Metadata)+1)
	maps.Copy(metadata, details.Metadata)
	if details.CoverURL != nil {
		metadata["cover_url"] = *details.CoverURL
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, false, fmt.Errorf("ingest: marshal metadata: %w", err)
	}

	item, created, err := d.Store.CreateItem(ctx, store.NewItem{
		MediaType:   details.MediaType,
		Title:       details.Title,
		ReleaseYear: details.ReleaseYear,
		Genres:      details.Genres,
		Provider:    details.Provider,
		ProviderID:  details.ProviderID,
		Metadata:    metaJSON,
	})
	if err != nil {
		return nil, false, fmt.Errorf("ingest: persist item: %w", err)
	}
	if !created {
		return item, false, nil
	}

	if details.CoverURL != nil {
		relPath, err := covers.Fetch(ctx, d.HTTPClient, d.DataDir, item.ID, *details.CoverURL)
		if err != nil {
			d.Logger.Warn("add: cover download failed", "item_id", item.ID, "error", err)
		} else if err := d.Store.SetCoverPath(ctx, item.ID, relPath); err != nil {
			d.Logger.Warn("add: persist cover path failed", "item_id", item.ID, "error", err)
		}
	}

	if err := d.Store.ReplaceRatings(ctx, item.ID, toStoreRatings(item.ID, details.Ratings)); err != nil {
		d.Logger.Warn("add: persist ratings failed", "item_id", item.ID, "error", err)
	}

	var avail []providers.Availability
	for _, ap := range d.Availability {
		rows, err := ap.Refresh(ctx, item)
		if err != nil {
			d.Logger.Warn("add: availability provider failed", "item_id", item.ID, "error", err)
			continue
		}
		avail = append(avail, rows...)
	}
	if err := d.Store.UpsertAvailability(ctx, item.ID, toStoreAvailability(item.ID, avail)); err != nil {
		d.Logger.Warn("add: persist availability failed", "item_id", item.ID, "error", err)
	}

	it, err := d.Store.GetItem(ctx, item.ID)
	return it, true, err
}
