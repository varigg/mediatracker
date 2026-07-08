package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// selectItemList matches selectItem's column order (scanItem depends on
// it) and always joins per-item average rating for the rating sort.
const selectItemList = `SELECT mi.id, mi.media_type, mi.title, mi.state, mi.verdict,
	mi.completed_at, mi.notes, mi.release_year, mi.genres, mi.cover_path,
	mi.provider, mi.provider_id, mi.metadata, mi.added_at, mi.refreshed_at
	FROM media_items mi
	LEFT JOIN (SELECT item_id, AVG(score) AS avg_score FROM ratings GROUP BY item_id) r
	ON r.item_id = mi.id`

// ErrInvalidQuery marks a user-input error from ParseListFilter, so
// callers can distinguish it from a real persistence failure via
// errors.Is and map it to an HTTP 400 rather than a 500.
var ErrInvalidQuery = errors.New("store: invalid list query")

// ListFilter is the persistence layer's own filter/sort vocabulary for
// ListItems — decoupled from how a caller happens to encode it. Zero
// value means "no filtering, default sort."
type ListFilter struct {
	State     State // "" means no state filter
	Types     []MediaType
	Genre     string
	Available bool
	Sort      string // "" | "added" | "year" | "rating" | "title"
}

// ParseListFilter translates URL query parameters into a ListFilter.
//
// Params: state (lifecycle state) · type (media type, repeatable) · genre
// (exact match against the genres JSON array) · available=1 (has a row on
// a subscribed service, or is owned) · sort = added (default) | year |
// rating | title. Unrecognized values wrap ErrInvalidQuery.
func ParseListFilter(v url.Values) (ListFilter, error) {
	var f ListFilter

	if s := v.Get("state"); s != "" {
		switch State(s) {
		case StateWantTo, StateInProgress, StateDone, StateAbandoned:
			f.State = State(s)
		default:
			return ListFilter{}, fmt.Errorf("%w: invalid state %q", ErrInvalidQuery, s)
		}
	}

	if types := v["type"]; len(types) > 0 {
		f.Types = make([]MediaType, len(types))
		for i, t := range types {
			switch MediaType(t) {
			case TypeMovie, TypeTV, TypeBook, TypeGame:
				f.Types[i] = MediaType(t)
			default:
				return ListFilter{}, fmt.Errorf("%w: invalid type %q", ErrInvalidQuery, t)
			}
		}
	}

	f.Genre = v.Get("genre")
	f.Available = v.Get("available") == "1"

	switch sort := v.Get("sort"); sort {
	case "", "added", "year", "rating", "title":
		f.Sort = sort
	default:
		return ListFilter{}, fmt.Errorf("%w: invalid sort %q", ErrInvalidQuery, sort)
	}

	return f, nil
}

// buildListQuery translates a ListFilter into SQL + args.
func buildListQuery(f ListFilter) (string, []any) {
	var where []string
	var args []any

	if f.State != "" {
		where = append(where, "mi.state = ?")
		args = append(args, string(f.State))
	}

	if len(f.Types) > 0 {
		placeholders := make([]string, len(f.Types))
		for i, t := range f.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		where = append(where, fmt.Sprintf("mi.media_type IN (%s)", strings.Join(placeholders, ", ")))
	}

	if f.Genre != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(mi.genres) WHERE json_each.value = ?)")
		args = append(args, f.Genre)
	}

	if f.Available {
		where = append(where, `EXISTS (SELECT 1 FROM availability a
			JOIN services s ON s.slug = a.service_slug
			WHERE a.item_id = mi.id AND (s.subscribed = 1 OR a.kind = 'owned'))`)
	}

	var orderBy string
	switch f.Sort {
	case "", "added":
		orderBy = "mi.added_at DESC, mi.id DESC"
	case "year":
		orderBy = "mi.release_year DESC NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "rating":
		orderBy = "r.avg_score DESC NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "title":
		orderBy = "mi.title COLLATE NOCASE ASC"
	}

	q := selectItemList
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY " + orderBy
	return q, args
}

// ListItems runs a query for the given filter.
func (s *Store) ListItems(ctx context.Context, f ListFilter) ([]MediaItem, error) {
	q, args := buildListQuery(f)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list items: %w", err)
	}
	items, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("store: list items: %w", err)
	}
	return items, nil
}
