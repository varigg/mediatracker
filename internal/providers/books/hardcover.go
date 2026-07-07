package books

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/varigg/mediatracker/internal/providers"
)

// Exact-title match; ties broken by popularity (M2 design decision 6).
const hardcoverQuery = `query BookByTitle($title: citext!) {
  books(where: {title: {_eq: $title}}, order_by: {users_count: desc}, limit: 1) {
    slug
    rating
    ratings_count
  }
}`

type hardcoverResponse struct {
	Data struct {
		Books []struct {
			Slug         string  `json:"slug"`
			Rating       float64 `json:"rating"`
			RatingsCount int     `json:"ratings_count"`
		} `json:"books"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// hardcoverRating fetches the community rating for a title. Best-effort:
// no key, transport error, non-200, GraphQL error, no match, or a zero
// rating all degrade to no rating and never fail the hydrate.
func (p *Provider) hardcoverRating(ctx context.Context, title string) []providers.Rating {
	if p.hardcoverKey == "" || title == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"query":     hardcoverQuery,
		"variables": map[string]any{"title": title},
	})
	if err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.hardcoverURL, bytes.NewReader(payload))
	if err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.hardcoverKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		p.logger.Warn("hardcover enrichment failed", "title", title, "status", resp.Status)
		return nil
	}
	var body hardcoverResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		p.logger.Warn("hardcover enrichment failed", "title", title, "error", err)
		return nil
	}
	if len(body.Errors) > 0 {
		p.logger.Warn("hardcover enrichment failed", "title", title, "graphql_error", body.Errors[0].Message)
		return nil
	}
	if len(body.Data.Books) == 0 {
		return nil // miss: metadata-only item, by design
	}
	b := body.Data.Books[0]
	if b.Rating <= 0 {
		return nil
	}
	score, err := providers.NormalizeScale(b.Rating, 5)
	if err != nil {
		p.logger.Warn("hardcover rating out of range", "title", title, "rating", b.Rating)
		return nil
	}
	rating := providers.Rating{
		Source:  "hardcover",
		Score:   score,
		Display: fmt.Sprintf("%.2f/5", b.Rating),
	}
	if b.Slug != "" {
		u := "https://hardcover.app/books/" + b.Slug
		rating.URL = &u
	}
	return []providers.Rating{rating}
}
