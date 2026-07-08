// Command mediatracker is the self-hosted media tracker server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/ingest"
	"github.com/varigg/mediatracker/internal/providers/setup"
	"github.com/varigg/mediatracker/internal/server"
	"github.com/varigg/mediatracker/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "mediatracker")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "data"
	}
	return filepath.Join(home, ".local", "share", "mediatracker")
}

func run() error {
	dataDir := flag.String("data", defaultDataDir(), "data directory (db, covers, catalogs, config.toml)")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("invalid log_level %q: %w", cfg.LogLevel, err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(*dataDir, "app.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	registry := setup.FromConfig(cfg.Providers, logger)
	availability := setup.AvailabilityFromConfig(cfg.Providers, *dataDir, logger)
	deps := ingest.Deps{
		Store:        st,
		Registry:     registry,
		Availability: availability,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		DataDir:      *dataDir,
		Logger:       logger,
		Now:          time.Now,
		ItemDelay:    time.Second,
	}
	refresher := ingest.NewRefresher(deps, cfg.RefreshInterval.Duration)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		refresher.Start(ctx)
	}()

	mux := http.NewServeMux()
	registerDebugRoutes(mux, deps, refresher)
	mux.Handle("/", server.New(st))

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("mediatracker started", "addr", cfg.ListenAddr, "data_dir", *dataDir)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		wg.Wait()
		return err
	}
}

// registerDebugRoutes wires temporary, unauthenticated endpoints for
// exercising the add/refresh pipelines before M6 builds the real HTTP
// route surface. Delete this function and its call site once M6 lands.
func registerDebugRoutes(mux *http.ServeMux, deps ingest.Deps, refresher *ingest.Refresher) {
	mux.HandleFunc("GET /debug/search", func(w http.ResponseWriter, r *http.Request) {
		mediaType := store.MediaType(r.URL.Query().Get("type"))
		p, err := deps.Registry.Get(mediaType)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		candidates, err := p.Search(r.Context(), r.URL.Query().Get("q"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(candidates)
	})

	mux.HandleFunc("POST /debug/add", func(w http.ResponseWriter, r *http.Request) {
		mediaType := store.MediaType(r.URL.Query().Get("type"))
		item, err := ingest.Add(r.Context(), deps, mediaType, r.URL.Query().Get("provider_id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(item)
	})

	mux.HandleFunc("POST /debug/refresh", func(w http.ResponseWriter, r *http.Request) {
		sum, err := refresher.RunCycle(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sum)
	})

	mux.HandleFunc("POST /debug/refresh/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		outcome, err := refresher.RefreshItem(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(outcome)
	})
}
