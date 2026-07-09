// Command mediatracker is the self-hosted media tracker server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	// Drain both the periodic refresher and request-spawned background work
	// (manual global refreshes) before closing the store, so they finish
	// cleanly before the database shuts down.
	defer wg.Wait()

	// Providers reports configured-ness only (booleans) for the Settings
	// page — raw keys never leave config/main. Mirrors
	// internal/providers/setup's registration conditions.
	providerStatus := server.ProviderStatus{
		TMDB:      cfg.Providers.TMDBKey != "",
		OMDB:      cfg.Providers.OMDBKey != "",
		IGDB:      cfg.Providers.IGDBClientID != "" && cfg.Providers.IGDBClientSecret != "",
		Hardcover: cfg.Providers.HardcoverKey != "",
		Steam:     cfg.Providers.SteamKey != "" && cfg.Providers.SteamID != "",
	}

	mux := http.NewServeMux()
	mux.Handle("/", server.New(server.Deps{
		Store:           st,
		Logger:          logger,
		DataDir:         *dataDir,
		RefreshInterval: cfg.RefreshInterval.Duration,
		Refresher:       refresher,
		Background:      &wg,
		Ingest:          deps,
		Providers:       providerStatus,
	}))

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Info("mediatracker started", "addr", cfg.ListenAddr, "data_dir", *dataDir)

	select {
	case err := <-errc:
		stop() // no signal arrived; cancel ctx ourselves so the refresher goroutine stops before shutdown
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
