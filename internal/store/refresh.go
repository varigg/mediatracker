package store

import "context"

// TouchRefreshed bumps refreshed_at to now, marking an item as processed
// by the current refresh cycle.
func (s *Store) TouchRefreshed(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE media_items SET refreshed_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveItemsByRefreshDue returns want_to/in_progress items ordered by
// refreshed_at ascending — SQLite sorts NULL first in ASC order, so
// never-refreshed items are processed before stale ones. done/abandoned
// items are frozen and never selected.
func (s *Store) ActiveItemsByRefreshDue(ctx context.Context) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, selectItem+
		` WHERE state IN ('want_to', 'in_progress') ORDER BY refreshed_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MediaItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *it)
	}
	return items, rows.Err()
}

// NewlyAvailable returns want_to items that gained availability on a
// subscribed service (stream or subscription, not owned — ownership
// isn't a "you pay for this" fact) at or after since.
func (s *Store) NewlyAvailable(ctx context.Context, since string) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT mi.id, mi.media_type, mi.title, mi.state,
		mi.verdict, mi.completed_at, mi.notes, mi.release_year, mi.genres, mi.cover_path,
		mi.provider, mi.provider_id, mi.metadata, mi.added_at, mi.refreshed_at
		FROM media_items mi
		JOIN availability a ON a.item_id = mi.id
		JOIN services s ON s.slug = a.service_slug
		WHERE mi.state = 'want_to' AND s.subscribed = 1
		  AND a.kind IN ('stream', 'subscription') AND a.first_seen_at >= ?
		ORDER BY mi.title COLLATE NOCASE ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MediaItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *it)
	}
	return items, rows.Err()
}
