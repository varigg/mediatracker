package store

import "context"

// ReplaceRatings replaces all rating rows for an item atomically.
func (s *Store) ReplaceRatings(ctx context.Context, itemID int64, ratings []Rating) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM ratings WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for _, r := range ratings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ratings
			(item_id, source, score, display, url) VALUES (?, ?, ?, ?, ?)`,
			itemID, r.Source, r.Score, r.Display, r.URL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetRatings(ctx context.Context, itemID int64) ([]Rating, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, source, score, display, url
		FROM ratings WHERE item_id = ? ORDER BY source`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Rating
	for rows.Next() {
		var r Rating
		if err := rows.Scan(&r.ItemID, &r.Source, &r.Score, &r.Display, &r.URL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertAvailability inserts or refreshes availability rows. Existing rows
// keep first_seen_at (it powers the "newly available" diff) and get
// fetched_at bumped.
func (s *Store) UpsertAvailability(ctx context.Context, itemID int64, avail []Availability) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, a := range avail {
		if _, err := tx.ExecContext(ctx, `INSERT INTO availability
			(item_id, service_slug, kind, url) VALUES (?, ?, ?, ?)
			ON CONFLICT (item_id, service_slug, kind)
			DO UPDATE SET url = excluded.url, fetched_at = CURRENT_TIMESTAMP`,
			itemID, a.ServiceSlug, a.Kind, a.URL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAvailability(ctx context.Context, itemID int64) ([]Availability, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, service_slug, kind, url,
		first_seen_at, fetched_at FROM availability WHERE item_id = ?
		ORDER BY service_slug, kind`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Availability
	for rows.Next() {
		var a Availability
		if err := rows.Scan(&a.ItemID, &a.ServiceSlug, &a.Kind, &a.URL,
			&a.FirstSeenAt, &a.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListServices(ctx context.Context) ([]Service, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT slug, name, media_kind, subscribed FROM services ORDER BY media_kind, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Service
	for rows.Next() {
		var svc Service
		if err := rows.Scan(&svc.Slug, &svc.Name, &svc.MediaKind, &svc.Subscribed); err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

func (s *Store) SetServiceSubscribed(ctx context.Context, slug string, subscribed bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE services SET subscribed = ? WHERE slug = ?`, subscribed, slug)
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
