// Package books implements the book MetadataProvider: Open Library for
// search and hydrate, composed with a miss-tolerant Hardcover community-
// rating match (hardcover.go).
package books

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const (
	defaultOpenLibraryBaseURL = "https://openlibrary.org"
	defaultCoversBaseURL      = "https://covers.openlibrary.org"
	defaultHardcoverURL       = "https://api.hardcover.app/v1/graphql"

	maxGenres  = 6
	maxAuthors = 3
)

type Provider struct {
	hardcoverKey  string // empty ⇒ Hardcover enrichment skipped
	olBaseURL     string
	coversBaseURL string
	hardcoverURL  string
	httpClient    *http.Client
	logger        *slog.Logger
}

type Option func(*Provider)

func WithOpenLibraryBaseURL(u string) Option { return func(p *Provider) { p.olBaseURL = u } }
func WithCoversBaseURL(u string) Option      { return func(p *Provider) { p.coversBaseURL = u } }
func WithHardcoverURL(u string) Option       { return func(p *Provider) { p.hardcoverURL = u } }
func WithHTTPClient(h *http.Client) Option   { return func(p *Provider) { p.httpClient = h } }
func WithLogger(l *slog.Logger) Option       { return func(p *Provider) { p.logger = l } }

func New(hardcoverKey string, opts ...Option) *Provider {
	p := &Provider{
		hardcoverKey:  hardcoverKey,
		olBaseURL:     defaultOpenLibraryBaseURL,
		coversBaseURL: defaultCoversBaseURL,
		hardcoverURL:  defaultHardcoverURL,
		httpClient:    http.DefaultClient,
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type olSearchResponse struct {
	Docs []struct {
		Key              string   `json:"key"` // "/works/OL262758W"
		Title            string   `json:"title"`
		FirstPublishYear *int     `json:"first_publish_year"`
		AuthorName       []string `json:"author_name"`
		CoverI           *int64   `json:"cover_i"`
	} `json:"docs"`
}

func (p *Provider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	params := url.Values{
		"q":      {query},
		"fields": {"key,title,first_publish_year,author_name,cover_i"},
		"limit":  {"10"},
	}
	var resp olSearchResponse
	if err := p.getJSON(ctx, p.olBaseURL+"/search.json?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	candidates := make([]providers.Candidate, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		candidates = append(candidates, providers.Candidate{
			Provider:       "openlibrary",
			ProviderID:     strings.TrimPrefix(d.Key, "/works/"),
			MediaType:      store.TypeBook,
			Title:          d.Title,
			Year:           d.FirstPublishYear,
			ThumbnailURL:   p.coverURL(d.CoverI, "M"),
			Disambiguation: strings.Join(d.AuthorName, ", "),
		})
	}
	return candidates, nil
}

type olWork struct {
	Title            string          `json:"title"`
	Description      json.RawMessage `json:"description"` // string OR {"value": ...}
	Subjects         []string        `json:"subjects"`
	Covers           []int64         `json:"covers"`
	FirstPublishDate string          `json:"first_publish_date"`
	Authors          []struct {
		Author struct {
			Key string `json:"key"` // "/authors/OL26320A"
		} `json:"author"`
	} `json:"authors"`
}

func (p *Provider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	var work olWork
	if err := p.getJSON(ctx, p.olBaseURL+"/works/"+providerID+".json", &work); err != nil {
		return nil, err
	}

	genres := work.Subjects
	if len(genres) > maxGenres {
		genres = genres[:maxGenres]
	}
	var coverID *int64
	if len(work.Covers) > 0 {
		coverID = &work.Covers[0]
	}
	coverURL := p.coverURL(coverID, "L")
	authors := p.authorNames(ctx, work)

	metadata := map[string]any{
		"openlibrary_key": "/works/" + providerID,
		"authors":         authors,
	}
	if desc := decodeDescription(work.Description); desc != "" {
		metadata["description"] = desc
	}
	return &providers.ItemDetails{
		MediaType:   store.TypeBook,
		Title:       work.Title,
		ReleaseYear: yearOf(work.FirstPublishDate),
		Genres:      genres,
		CoverURL:    coverURL,
		Provider:    "openlibrary",
		ProviderID:  providerID,
		Metadata:    metadata,
		Ratings:     p.hardcoverRating(ctx, work.Title),
	}, nil
}

// authorNames resolves author keys to names, best-effort: a failed
// author fetch is logged and skipped, never fails the hydrate.
func (p *Provider) authorNames(ctx context.Context, work olWork) []string {
	names := []string{}
	for i, a := range work.Authors {
		if i == maxAuthors {
			break
		}
		var author struct {
			Name string `json:"name"`
		}
		if err := p.getJSON(ctx, p.olBaseURL+a.Author.Key+".json", &author); err != nil {
			p.logger.Warn("open library author fetch failed", "key", a.Author.Key, "error", err)
			continue
		}
		if author.Name != "" {
			names = append(names, author.Name)
		}
	}
	return names
}

func (p *Provider) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openlibrary: %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openlibrary: %s returned %s", u, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("openlibrary: decode %s: %w", u, err)
	}
	return nil
}

func (p *Provider) coverURL(id *int64, size string) *string {
	if id == nil || *id <= 0 {
		return nil
	}
	u := fmt.Sprintf("%s/b/id/%d-%s.jpg", p.coversBaseURL, *id, size)
	return &u
}

// decodeDescription handles Open Library's two description shapes:
// a bare string or {"type": "/type/text", "value": "..."}.
func decodeDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Value
	}
	return ""
}

var yearRe = regexp.MustCompile(`\b\d{4}\b`)

// yearOf extracts a year from Open Library's free-form first_publish_date
// ("1937", "September 21, 1937").
func yearOf(date string) *int {
	m := yearRe.FindString(date)
	if m == "" {
		return nil
	}
	y, err := strconv.Atoi(m)
	if err != nil {
		return nil
	}
	return &y
}
