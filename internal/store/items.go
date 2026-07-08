package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrIllegalTransition = errors.New("illegal state transition")
	ErrNotTerminal       = errors.New("item not in a terminal state")
)

type NewItem struct {
	MediaType   MediaType
	Title       string
	ReleaseYear *int
	Genres      []string
	Provider    string
	ProviderID  string
	Metadata    json.RawMessage
}

const selectItem = `SELECT id, media_type, title, state, verdict, completed_at,
	notes, release_year, genres, cover_path, provider, provider_id, metadata,
	added_at, refreshed_at FROM media_items`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(r rowScanner) (*MediaItem, error) {
	var it MediaItem
	var genres, metadata string
	err := r.Scan(&it.ID, &it.MediaType, &it.Title, &it.State, &it.Verdict,
		&it.CompletedAt, &it.Notes, &it.ReleaseYear, &genres, &it.CoverPath,
		&it.Provider, &it.ProviderID, &metadata, &it.AddedAt, &it.RefreshedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(genres), &it.Genres); err != nil {
		return nil, fmt.Errorf("item %d: bad genres JSON: %w", it.ID, err)
	}
	it.Metadata = json.RawMessage(metadata)
	return &it, nil
}

// CreateItem inserts a new item in state want_to. If (provider,
// provider_id) already exists, the existing row is returned unmodified and
// the bool is false — re-adding surfaces the existing item.
func (s *Store) CreateItem(ctx context.Context, n NewItem) (*MediaItem, bool, error) {
	genres := []byte("[]")
	if n.Genres != nil {
		var err error
		if genres, err = json.Marshal(n.Genres); err != nil {
			return nil, false, err
		}
	}
	metadata := "{}"
	if len(n.Metadata) > 0 {
		metadata = string(n.Metadata)
	}

	res, err := s.db.ExecContext(ctx, `INSERT INTO media_items
		(media_type, title, release_year, genres, provider, provider_id, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, provider_id) DO NOTHING`,
		n.MediaType, n.Title, n.ReleaseYear, string(genres), n.Provider, n.ProviderID, metadata)
	if err != nil {
		return nil, false, err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return nil, false, err
	} else if rows == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return nil, false, err
		}
		it, err := s.GetItem(ctx, id)
		return it, true, err
	}

	it, err := scanItem(s.db.QueryRowContext(ctx,
		selectItem+` WHERE provider = ? AND provider_id = ?`, n.Provider, n.ProviderID))
	return it, false, err
}

func (s *Store) GetItem(ctx context.Context, id int64) (*MediaItem, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx, selectItem+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

// UpdateState moves an item to a new lifecycle state, enforcing
// CanTransition. Entering done/abandoned stamps completed_at with today;
// entering a non-terminal state clears verdict and completed_at.
func (s *Store) UpdateState(ctx context.Context, id int64, to State) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from State
	err = tx.QueryRowContext(ctx, `SELECT state FROM media_items WHERE id = ?`, id).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, from, to)
	}

	if to == StateDone || to == StateAbandoned {
		_, err = tx.ExecContext(ctx,
			`UPDATE media_items SET state = ?, completed_at = DATE('now') WHERE id = ?`, to, id)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE media_items SET state = ?, verdict = NULL, completed_at = NULL WHERE id = ?`, to, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateReview sets verdict and completion date; legal only in done or
// abandoned states.
func (s *Store) UpdateReview(ctx context.Context, id int64, v Verdict, completedAt string) error {
	switch v {
	case VerdictLiked, VerdictOK, VerdictDisliked:
	default:
		return fmt.Errorf("invalid verdict %q", v)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE media_items SET verdict = ?, completed_at = ?
		WHERE id = ? AND state IN ('done', 'abandoned')`, v, completedAt, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 1 {
		return nil
	}
	if _, err := s.GetItem(ctx, id); err != nil {
		return err // ErrNotFound
	}
	return ErrNotTerminal
}

func (s *Store) UpdateNotes(ctx context.Context, id int64, notes string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE media_items SET notes = ? WHERE id = ?`, notes, id)
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

// SetCoverPath records where a downloaded cover was saved, relative to
// the data dir (e.g. "covers/42.jpg").
func (s *Store) SetCoverPath(ctx context.Context, id int64, path string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE media_items SET cover_path = ? WHERE id = ?`, path, id)
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
