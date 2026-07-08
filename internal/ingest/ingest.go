// Package ingest orchestrates the synchronous add-flow and the
// asynchronous weekly refresh cycle on top of the M1 store, the M2
// metadata-provider registry, and the M3 availability enrichers.
package ingest

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

// Deps wires everything Add and Refresher need. Now is used only by
// Refresher; NewRefresher defaults it to time.Now when left unset, so
// tests only need to override it for determinism. ItemDelay is the
// inter-item pause during a refresh cycle — zero (the test default)
// means no pause.
type Deps struct {
	Store        *store.Store
	Registry     *providers.Registry
	Availability []providers.AvailabilityProvider
	HTTPClient   *http.Client
	DataDir      string
	Logger       *slog.Logger
	Now          func() time.Time
	ItemDelay    time.Duration
}
