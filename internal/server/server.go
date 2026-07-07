// Package server is the HTTP layer. In M1 it exposes only the health
// endpoint; M6 adds the full route surface.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Store is the subset of the store API the HTTP layer needs.
type Store interface {
	Ping(ctx context.Context) error
}

func New(st Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ping(r.Context()); err != nil {
			slog.Error("health check failed", "error", err)
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}
