// Package igdb implements the game MetadataProvider against the IGDB v4
// API, authenticating via the Twitch client-credentials flow (token.go).
package igdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const (
	defaultBaseURL  = "https://api.igdb.com/v4"
	defaultImageURL = "https://images.igdb.com/igdb/image/upload"
)

type Provider struct {
	clientID   string
	baseURL    string
	imageURL   string
	httpClient *http.Client
	logger     *slog.Logger
	tokens     *tokenSource
}

type Option func(*Provider)

func WithBaseURL(u string) Option  { return func(p *Provider) { p.baseURL = u } }
func WithTokenURL(u string) Option { return func(p *Provider) { p.tokens.tokenURL = u } }
func WithHTTPClient(h *http.Client) Option {
	return func(p *Provider) { p.httpClient = h; p.tokens.httpClient = h }
}
func WithLogger(l *slog.Logger) Option    { return func(p *Provider) { p.logger = l } }
func WithNow(now func() time.Time) Option { return func(p *Provider) { p.tokens.now = now } }

func New(clientID, clientSecret string, opts ...Option) *Provider {
	p := &Provider{
		clientID:   clientID,
		baseURL:    defaultBaseURL,
		imageURL:   defaultImageURL,
		httpClient: http.DefaultClient,
		logger:     slog.Default(),
		tokens: &tokenSource{
			clientID:     clientID,
			clientSecret: clientSecret,
			tokenURL:     defaultTokenURL,
			httpClient:   http.DefaultClient,
			now:          time.Now,
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type game struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	FirstReleaseDate int64   `json:"first_release_date"` // unix seconds
	Summary          string  `json:"summary"`
	URL              string  `json:"url"`
	Rating           float64 `json:"rating"` // community, 0–100
	RatingCount      int     `json:"rating_count"`
	AggregatedRating float64 `json:"aggregated_rating"` // critics, 0–100
	Cover            struct {
		ImageID string `json:"image_id"`
	} `json:"cover"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Platforms []struct {
		Abbreviation string `json:"abbreviation"`
	} `json:"platforms"`
	AlternativeNames []struct {
		Name string `json:"name"`
	} `json:"alternative_names"`
	ExternalGames []struct {
		Category int    `json:"category"`
		UID      string `json:"uid"`
	} `json:"external_games"`
}

func (p *Provider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	q := strings.ReplaceAll(query, `"`, `\"`)
	body := fmt.Sprintf(`search "%s"; fields name,first_release_date,cover.image_id,platforms.abbreviation; limit 10;`, q)
	var games []game
	if err := p.query(ctx, body, &games); err != nil {
		return nil, err
	}
	candidates := make([]providers.Candidate, 0, len(games))
	for _, g := range games {
		var platforms []string
		for _, pl := range g.Platforms {
			if pl.Abbreviation != "" {
				platforms = append(platforms, pl.Abbreviation)
			}
		}
		candidates = append(candidates, providers.Candidate{
			Provider:       "igdb",
			ProviderID:     strconv.FormatInt(g.ID, 10),
			MediaType:      store.TypeGame,
			Title:          g.Name,
			Year:           yearOf(g.FirstReleaseDate),
			ThumbnailURL:   p.coverURL(g.Cover.ImageID, "t_cover_small"),
			Disambiguation: strings.Join(platforms, ", "),
		})
	}
	return candidates, nil
}

func (p *Provider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("igdb: provider id %q: %w", providerID, err)
	}
	body := fmt.Sprintf(`fields name,first_release_date,summary,url,rating,rating_count,aggregated_rating,cover.image_id,genres.name,alternative_names.name,external_games.category,external_games.uid; where id = %d;`, id)
	var games []game
	if err := p.query(ctx, body, &games); err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("igdb: game %d not found", id)
	}
	g := games[0]

	genres := make([]string, 0, len(g.Genres))
	for _, ge := range g.Genres {
		genres = append(genres, ge.Name)
	}
	altNames := make([]string, 0, len(g.AlternativeNames))
	for _, a := range g.AlternativeNames {
		altNames = append(altNames, a.Name)
	}
	coverURL := p.coverURL(g.Cover.ImageID, "t_cover_big")

	metadata := map[string]any{
		"igdb_id":           g.ID,
		"summary":           g.Summary,
		"alternative_names": altNames, // M3's game-name matcher consumes these
	}
	if g.URL != "" {
		metadata["igdb_url"] = g.URL
	}
	if coverURL != nil {
		metadata["cover_url"] = *coverURL
	}
	for _, eg := range g.ExternalGames {
		if eg.Category == 1 { // Steam; the steam enricher matches by this app ID
			if appID, err := strconv.ParseInt(eg.UID, 10, 64); err == nil {
				metadata["steam_appid"] = appID
				break
			}
		}
	}

	var ratings []providers.Rating
	if g.Rating > 0 {
		if score, err := providers.NormalizeScale(g.Rating, 100); err == nil {
			rating := providers.Rating{
				Source:  "igdb",
				Score:   score,
				Display: fmt.Sprintf("%.0f/100", g.Rating),
			}
			if g.URL != "" {
				u := g.URL
				rating.URL = &u
			}
			ratings = append(ratings, rating)
		}
	}
	if g.AggregatedRating > 0 {
		if score, err := providers.NormalizeScale(g.AggregatedRating, 100); err == nil {
			ratings = append(ratings, providers.Rating{
				Source:  "igdb_critics",
				Score:   score,
				Display: fmt.Sprintf("%.0f/100", g.AggregatedRating),
			})
		}
	}

	return &providers.ItemDetails{
		MediaType:   store.TypeGame,
		Title:       g.Name,
		ReleaseYear: yearOf(g.FirstReleaseDate),
		Genres:      genres,
		CoverURL:    coverURL,
		Provider:    "igdb",
		ProviderID:  strconv.FormatInt(g.ID, 10),
		Metadata:    metadata,
		Ratings:     ratings,
	}, nil
}

// query POSTs an Apicalypse body to /games with Twitch auth headers.
func (p *Provider) query(ctx context.Context, body string, dst any) error {
	token, err := p.tokens.get(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/games", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Client-ID", p.clientID)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("igdb: games query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("igdb: games query returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("igdb: decode games response: %w", err)
	}
	return nil
}

func (p *Provider) coverURL(imageID, size string) *string {
	if imageID == "" {
		return nil
	}
	u := fmt.Sprintf("%s/%s/%s.jpg", p.imageURL, size, imageID)
	return &u
}

func yearOf(unix int64) *int {
	if unix <= 0 {
		return nil
	}
	y := time.Unix(unix, 0).UTC().Year()
	return &y
}
