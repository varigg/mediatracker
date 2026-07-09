package server

import (
	"encoding/json"
	"net/http"

	"github.com/varigg/mediatracker/internal/store"
)

// Nav is the layout-level view model: active tab and per-group counts.
type Nav struct {
	Active string // "" (home) | "movies-tv" | "books" | "games"
	Counts map[string]int
}

// groupTypes maps a URL group to the media types it contains. The
// movies-tv group is the only multi-type group (spec Section 4).
var groupTypes = map[string][]store.MediaType{
	"movies-tv": {store.TypeMovie, store.TypeTV},
	"books":     {store.TypeBook},
	"games":     {store.TypeGame},
}

var groupLabels = map[string]string{
	"movies-tv": "Movies & TV", "books": "Books", "games": "Games",
}

func (s *site) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.Ping(r.Context()); err != nil {
		s.deps.Logger.Error("health check failed", "error", err)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// nav builds the layout model; total per group = sum across states.
func (s *site) nav(r *http.Request, active string) (Nav, error) {
	counts, err := s.deps.Store.GroupStateCounts(r.Context())
	if err != nil {
		return Nav{}, err
	}
	byGroup := map[string]int{}
	for group, types := range groupTypes {
		for _, mt := range types {
			for _, n := range counts[mt] {
				byGroup[group] += n
			}
		}
	}
	return Nav{Active: active, Counts: byGroup}, nil
}
