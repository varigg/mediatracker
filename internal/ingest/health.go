package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/varigg/mediatracker/internal/store"
)

// providerLastSuccessKey builds the settings key ingest uses to record a
// metadata provider's last successful Hydrate call (M7 plan decision 1).
// provider is providers.ItemDetails.Provider — "tmdb" | "openlibrary" |
// "igdb" today. The server layer reads this same key shape to render
// the Settings page's per-provider health line.
func providerLastSuccessKey(provider string) string {
	return "provider_last_success_" + provider
}

// recordProviderSuccess best-effort records the timestamp of a
// successful metadata-provider Hydrate call, shared by both the Add and
// refresh paths. Health bookkeeping must never break ingestion, so a
// write failure here is logged and swallowed — the same degrade-not-fail
// convention Add and refreshItem already use for cover/ratings/
// availability writes.
func recordProviderSuccess(ctx context.Context, st *store.Store, logger *slog.Logger, provider string, t time.Time) {
	if err := st.SetSetting(ctx, providerLastSuccessKey(provider), t.UTC().Format(store.TimeFormat)); err != nil {
		logger.Warn("record provider last-success failed", "provider", provider, "error", err)
	}
}
