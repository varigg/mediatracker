package store

import (
	"context"
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

// BuildListQuery translates URL query parameters into SQL + args.
//
// Params: state (lifecycle state) · type (media type, repeatable) · genre
// (exact match against the genres JSON array) · available=1 (has a row on
// a subscribed service, or is owned) · sort = added (default) | year |
// rating | title. Unrecognized values are user-input errors (→ 400).
func BuildListQuery(v url.Values) (string, []any, error) {
	var where []string
	var args []any

	if s := v.Get("state"); s != "" {
		switch State(s) {
		case StateWantTo, StateInProgress, StateDone, StateAbandoned:
			where = append(where, "mi.state = ?")
			args = append(args, s)
		default:
			return "", nil, fmt.Errorf("invalid state %q", s)
		}
	}

	if types := v["type"]; len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			switch MediaType(t) {
			case TypeMovie, TypeTV, TypeBook, TypeGame:
				placeholders[i] = "?"
				args = append(args, t)
			default:
				return "", nil, fmt.Errorf("invalid type %q", t)
			}
		}
		where = append(where, fmt.Sprintf("mi.media_type IN (%s)", strings.Join(placeholders, ", ")))
	}

	if g := v.Get("genre"); g != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(mi.genres) WHERE json_each.value = ?)")
		args = append(args, g)
	}

	if v.Get("available") == "1" {
		where = append(where, `EXISTS (SELECT 1 FROM availability a
			JOIN services s ON s.slug = a.service_slug
			WHERE a.item_id = mi.id AND (s.subscribed = 1 OR a.kind = 'owned'))`)
	}

	var orderBy string
	switch v.Get("sort") {
	case "", "added":
		orderBy = "mi.added_at DESC, mi.id DESC"
	case "year":
		orderBy = "mi.release_year DESC NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "rating":
		orderBy = "r.avg_score DESC NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "title":
		orderBy = "mi.title COLLATE NOCASE ASC"
	default:
		return "", nil, fmt.Errorf("invalid sort %q", v.Get("sort"))
	}

	q := selectItemList
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY " + orderBy
	return q, args, nil
}

// ListItems runs the query BuildListQuery produces for the given params.
func (s *Store) ListItems(ctx context.Context, v url.Values) ([]MediaItem, error) {
	q, args, err := BuildListQuery(v)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}
