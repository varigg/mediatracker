package store

import (
	"context"
	"fmt"
	"time"
)

// TimeFormat is the TEXT timestamp format SQLite's CURRENT_TIMESTAMP
// produces and the format every stored timestamp comparison must match.
const TimeFormat = "2006-01-02 15:04:05"

// TouchRefreshed bumps refreshed_at to now, marking an item as processed
// by the current refresh cycle.
func (s *Store) TouchRefreshed(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE media_items SET refreshed_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: touch refreshed: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: touch refreshed: %w", err)
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveItemsByRefreshDue returns want_to/in_progress items ordered by
// refreshed_at ascending — SQLite sorts NULL first in ASC order, so
// never-refreshed items are processed before stale ones. done/abandoned
// items are frozen and never selected. The state filter mirrors
// State.Active() (models.go); SQL can't call it directly, so keep them
// in sync by hand.
func (s *Store) ActiveItemsByRefreshDue(ctx context.Context) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, selectItem+
		` WHERE state IN ('want_to', 'in_progress') ORDER BY refreshed_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: active items by refresh due: %w", err)
	}
	items, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("store: active items by refresh due: %w", err)
	}
	return items, nil
}

// NewlyAvailable returns want_to items that gained availability on a
// subscribed service (stream or subscription, not owned — ownership
// isn't a "you pay for this" fact) at or after since.
func (s *Store) NewlyAvailable(ctx context.Context, since time.Time) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, selectItem+` WHERE state = 'want_to' AND id IN (
		SELECT a.item_id FROM availability a
		JOIN services sv ON sv.slug = a.service_slug
		WHERE sv.subscribed = 1 AND a.kind IN ('stream', 'subscription') AND a.first_seen_at >= ?
	) ORDER BY title COLLATE NOCASE ASC`, since.UTC().Format(TimeFormat))
	if err != nil {
		return nil, fmt.Errorf("store: newly available: %w", err)
	}
	items, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("store: newly available: %w", err)
	}
	return items, nil
}
