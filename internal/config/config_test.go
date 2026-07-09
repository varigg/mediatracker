package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.RefreshInterval.Duration != 7*24*time.Hour {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval.Duration, 7*24*time.Hour)
	}
}

func TestLoadReadsValues(t *testing.T) {
	dir := t.TempDir()
	data := `
listen_addr = ":9090"
log_level = "debug"
refresh_interval = "24h"

[providers]
tmdb_key = "tmdb-secret"
igdb_client_id = "igdb-id"
igdb_client_secret = "igdb-secret"
steam_id = "7656119"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
	if cfg.RefreshInterval.Duration != 24*time.Hour {
		t.Errorf("RefreshInterval = %v, want 24h", cfg.RefreshInterval.Duration)
	}
	if cfg.Providers.TMDBKey != "tmdb-secret" {
		t.Errorf("TMDBKey = %q, want %q", cfg.Providers.TMDBKey, "tmdb-secret")
	}
	if cfg.Providers.IGDBClientSecret != "igdb-secret" {
		t.Errorf("IGDBClientSecret = %q, want %q", cfg.Providers.IGDBClientSecret, "igdb-secret")
	}
	if cfg.Providers.SteamID != "7656119" {
		t.Errorf("SteamID = %q, want %q", cfg.Providers.SteamID, "7656119")
	}
}

func TestLoadMalformedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("listen_addr = [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load: want error for malformed config, got nil")
	}
}

func TestLoadInvalidLogLevelErrors(t *testing.T) {
	dir := t.TempDir()
	data := `
listen_addr = ":8080"
log_level = "invalid_level"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load: want error for invalid log_level, got nil")
	}
}
