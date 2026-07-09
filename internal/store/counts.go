package store

import (
	"context"
	"fmt"
)

// GroupStateCounts returns item counts by media type and lifecycle
// state, for the tab badges and the landing page's library panel.
func (s *Store) GroupStateCounts(ctx context.Context) (map[MediaType]map[State]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT media_type, state, COUNT(*) FROM media_items GROUP BY media_type, state`)
	if err != nil {
		return nil, fmt.Errorf("store: group state counts: %w", err)
	}
	defer rows.Close()

	out := make(map[MediaType]map[State]int)
	for rows.Next() {
		var mt MediaType
		var st State
		var n int
		if err := rows.Scan(&mt, &st, &n); err != nil {
			return nil, fmt.Errorf("store: group state counts: %w", err)
		}
		if out[mt] == nil {
			out[mt] = make(map[State]int)
		}
		out[mt][st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: group state counts: %w", err)
	}
	return out, nil
}
