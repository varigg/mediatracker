package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

// providerSlugs maps TMDB watch-provider names onto the seeded services
// catalog. Unmapped names fall back to slugify — availability rows may
// reference services beyond the seeded set (no FK by design).
var providerSlugs = map[string]string{
	"Netflix":            "netflix",
	"Amazon Prime Video": "prime_video",
	"Disney Plus":        "disney_plus",
	"Disney+":            "disney_plus",
	"Hulu":               "hulu",
	"Max":                "max",
	"Apple TV Plus":      "apple_tv_plus",
	"Apple TV+":          "apple_tv_plus",
	"Paramount Plus":     "paramount_plus",
	"Paramount+":         "paramount_plus",
	"Peacock":            "peacock",
	"Peacock Premium":    "peacock",
}

type watchEntry struct {
	ProviderName string `json:"provider_name"`
}

type watchResponse struct {
	Results map[string]struct {
		Link     string       `json:"link"`
		Flatrate []watchEntry `json:"flatrate"`
		Free     []watchEntry `json:"free"`
		Ads      []watchEntry `json:"ads"`
	} `json:"results"`
}

// WatchProvider returns the streaming-availability enricher backed by
// TMDB's JustWatch-sourced watch/providers endpoint, region US.
func (c *Client) WatchProvider() providers.AvailabilityProvider {
	return watchProvider{c}
}

type watchProvider struct{ c *Client }

func (p watchProvider) Refresh(ctx context.Context, item *store.MediaItem) ([]providers.Availability, error) {
	if item.Provider != "tmdb" {
		return nil, nil // not this enricher's item; self-filter like the game providers
	}
	id, err := parseProviderID(item.MediaType, item.ProviderID)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/movie/%d/watch/providers", id)
	if item.MediaType == store.TypeTV {
		path = fmt.Sprintf("/tv/%d/watch/providers", id)
	}
	var resp watchResponse
	if err := p.c.get(ctx, path, url.Values{}, &resp); err != nil {
		return nil, err
	}
	region, ok := resp.Results["US"]
	if !ok {
		return nil, nil
	}

	var link *string
	if region.Link != "" {
		l := region.Link
		link = &l
	}
	var out []providers.Availability
	seen := map[string]bool{}
	add := func(entries []watchEntry, kind string) {
		for _, e := range entries {
			slug := slugFor(e.ProviderName)
			if slug == "" || seen[slug+"/"+kind] {
				continue
			}
			seen[slug+"/"+kind] = true
			out = append(out, providers.Availability{ServiceSlug: slug, Kind: kind, URL: link})
		}
	}
	add(region.Flatrate, "subscription")
	add(region.Free, "stream")
	add(region.Ads, "stream")
	return out, nil
}

func slugFor(name string) string {
	if slug, ok := providerSlugs[name]; ok {
		return slug
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == ' ', r == '-', r == '+':
			if !lastUnderscore && b.Len() > 0 {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
