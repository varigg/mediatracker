package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/varigg/mediatracker/internal/providers"
)

type omdbResponse struct {
	Response string `json:"Response"` // "True" | "False"
	Ratings  []struct {
		Source string `json:"Source"`
		Value  string `json:"Value"`
	} `json:"Ratings"`
}

var omdbSources = map[string]string{
	"Internet Movie Database": "imdb",
	"Rotten Tomatoes":         "rotten_tomatoes",
	"Metacritic":              "metacritic",
}

// omdbRatings is a best-effort enricher: every failure — no key, no IMDB
// ID, transport error, non-200, OMDb miss, unparsable value — degrades to
// no ratings and never fails the hydrate.
func (c *Client) omdbRatings(ctx context.Context, imdbID string) []providers.Rating {
	if c.omdbKey == "" || imdbID == "" {
		return nil
	}
	q := url.Values{"apikey": {c.omdbKey}, "i": {imdbID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.omdbBaseURL+"/?"+q.Encode(), nil)
	if err != nil {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "error", err)
		return nil
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "status", resp.Status)
		return nil
	}
	var body omdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.logger.Warn("omdb enrichment failed", "imdb_id", imdbID, "error", err)
		return nil
	}
	if body.Response != "True" {
		return nil // OMDb miss: metadata-only item, by design
	}
	var ratings []providers.Rating
	for _, r := range body.Ratings {
		source, ok := omdbSources[r.Source]
		if !ok {
			continue
		}
		score, err := providers.ParseDisplay(r.Value)
		if err != nil {
			c.logger.Warn("omdb rating unparsable", "imdb_id", imdbID, "source", r.Source, "value", r.Value)
			continue
		}
		rating := providers.Rating{Source: source, Score: score, Display: r.Value}
		if source == "imdb" {
			u := fmt.Sprintf("https://www.imdb.com/title/%s/", imdbID)
			rating.URL = &u
		}
		ratings = append(ratings, rating)
	}
	return ratings
}
