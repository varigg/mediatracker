package ingest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const lastRefreshSettingKey = "last_refresh_at"

// timeFormat matches the SQLite TEXT timestamp format the store uses
// elsewhere (CURRENT_TIMESTAMP's default rendering).
const timeFormat = "2006-01-02 15:04:05"

// ErrItemNotActive is returned by RefreshItem when the target item is
// not in an active state (want_to/in_progress). Done/abandoned items
// are frozen and must never be touched by a refresh cycle.
var ErrItemNotActive = errors.New("ingest: item is not in an active state")

// Summary reports one refresh cycle's outcome for the per-cycle log
// line. There's no separate "skipped" count: done/abandoned items are
// excluded by the selection query itself, not skipped mid-cycle.
type Summary struct {
	Items              int
	RatingsFailed      int
	AvailabilityFailed int
}

// Refresher runs the weekly background refresh: catalog snapshot sync,
// then per-active-item availability + ratings refresh, sequential with
// a small inter-item delay.
type Refresher struct {
	deps     Deps
	interval time.Duration
}

func NewRefresher(d Deps, interval time.Duration) *Refresher {
	return &Refresher{deps: d, interval: interval}
}

// Start runs an immediate catch-up cycle if overdue, then loops on a
// jittered ticker until ctx is done. Call it in its own goroutine.
func (r *Refresher) Start(ctx context.Context) {
	if r.overdue(ctx) {
		if _, err := r.RunCycle(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.deps.Logger.Error("startup refresh cycle failed", "error", err)
		}
	}

	var jitter time.Duration
	if max := r.interval / 20; max > 0 { // up to 5% of the interval
		jitter = rand.N(max)
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.RunCycle(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.deps.Logger.Error("refresh cycle failed", "error", err)
			}
		}
	}
}

// overdue reports whether the last completed cycle is older than the
// interval (or none is recorded), driving startup catch-up.
func (r *Refresher) overdue(ctx context.Context) bool {
	last, ok, err := r.deps.Store.GetSetting(ctx, lastRefreshSettingKey)
	if err != nil {
		r.deps.Logger.Warn("overdue check: read last_refresh_at failed", "error", err)
		return true
	}
	if !ok {
		return true
	}
	lastT, err := time.Parse(timeFormat, last)
	if err != nil {
		return true
	}
	return r.deps.Now().Sub(lastT) >= r.interval
}

// RunCycle re-syncs catalog snapshots, then refreshes every active item
// sequentially. A failure syncing catalogs or refreshing one item never
// stops the cycle; only a failure listing active items aborts it.
func (r *Refresher) RunCycle(ctx context.Context) (Summary, error) {
	for _, p := range r.deps.Availability {
		if syncer, ok := p.(providers.CycleSyncer); ok {
			if err := syncer.SyncCycle(ctx); err != nil {
				r.deps.Logger.Warn("catalog sync failed", "error", err)
			}
		}
	}

	items, err := r.deps.Store.ActiveItemsByRefreshDue(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("ingest: list active items: %w", err)
	}

	var sum Summary
	for i := range items {
		outcome := r.refreshItem(ctx, &items[i])
		sum.Items++
		if outcome.RatingsFailed {
			sum.RatingsFailed++
		}
		if outcome.AvailabilityFailed {
			sum.AvailabilityFailed++
		}
		if i < len(items)-1 && r.deps.ItemDelay > 0 {
			select {
			case <-ctx.Done():
				return sum, ctx.Err()
			case <-time.After(r.deps.ItemDelay):
			}
		}
	}

	if err := r.deps.Store.SetSetting(ctx, lastRefreshSettingKey, r.deps.Now().UTC().Format(timeFormat)); err != nil {
		r.deps.Logger.Warn("persist last_refresh_at failed", "error", err)
	}
	r.deps.Logger.Info("refresh cycle complete", "items", sum.Items,
		"ratings_failed", sum.RatingsFailed, "availability_failed", sum.AvailabilityFailed)
	return sum, nil
}

// RefreshItem refreshes one item via the same per-item logic RunCycle
// uses, for the manual per-item refresh entry point.
func (r *Refresher) RefreshItem(ctx context.Context, itemID int64) (ItemOutcome, error) {
	item, err := r.deps.Store.GetItem(ctx, itemID)
	if err != nil {
		return ItemOutcome{}, err
	}
	if !item.State.Active() {
		return ItemOutcome{}, ErrItemNotActive
	}
	return r.refreshItem(ctx, item), nil
}

// ItemOutcome reports one item's per-provider refresh result.
type ItemOutcome struct {
	RatingsFailed      bool
	AvailabilityFailed bool
}

func (r *Refresher) refreshItem(ctx context.Context, item *store.MediaItem) ItemOutcome {
	var out ItemOutcome

	if p, err := r.deps.Registry.Get(item.MediaType); err == nil {
		details, err := p.Hydrate(ctx, item.ProviderID)
		if err != nil {
			out.RatingsFailed = true
			r.deps.Logger.Warn("refresh: hydrate failed", "item_id", item.ID, "error", err)
		} else if len(details.Ratings) > 0 {
			// Hydrate can succeed with an empty Ratings slice when a
			// sub-source is transiently degraded (e.g. OMDb down,
			// Hardcover miss). ReplaceRatings is delete-then-insert, so
			// only call it when there's something new to write — an
			// empty result here is "nothing new," not a failure, and
			// must not wipe previously-good ratings rows.
			if err := r.deps.Store.ReplaceRatings(ctx, item.ID, toStoreRatings(item.ID, details.Ratings)); err != nil {
				out.RatingsFailed = true
				r.deps.Logger.Warn("refresh: replace ratings failed", "item_id", item.ID, "error", err)
			}
		}
	} else {
		r.deps.Logger.Warn("refresh: no provider registered for media type", "media_type", item.MediaType, "item_id", item.ID)
	}

	var avail []providers.Availability
	failures := 0
	for _, ap := range r.deps.Availability {
		rows, err := ap.Refresh(ctx, item)
		if err != nil {
			failures++
			r.deps.Logger.Warn("refresh: availability provider failed", "item_id", item.ID, "error", err)
			continue
		}
		avail = append(avail, rows...)
	}
	if len(r.deps.Availability) > 0 && failures == len(r.deps.Availability) {
		out.AvailabilityFailed = true
	}
	if err := r.deps.Store.UpsertAvailability(ctx, item.ID, toStoreAvailability(item.ID, avail)); err != nil {
		out.AvailabilityFailed = true
		r.deps.Logger.Warn("refresh: upsert availability failed", "item_id", item.ID, "error", err)
	}

	if err := r.deps.Store.TouchRefreshed(ctx, item.ID); err != nil {
		r.deps.Logger.Warn("refresh: touch refreshed_at failed", "item_id", item.ID, "error", err)
	}
	return out
}
