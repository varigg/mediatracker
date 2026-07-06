// Package config loads mediatracker's config.toml from the data dir.
// API keys live here — never in env vars.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ListenAddr      string    `toml:"listen_addr"`
	LogLevel        string    `toml:"log_level"`
	RefreshInterval Duration  `toml:"refresh_interval"`
	Providers       Providers `toml:"providers"`
}

type Providers struct {
	TMDBKey          string `toml:"tmdb_key"`
	OMDBKey          string `toml:"omdb_key"`
	IGDBClientID     string `toml:"igdb_client_id"`
	IGDBClientSecret string `toml:"igdb_client_secret"`
	HardcoverKey     string `toml:"hardcover_key"`
	SteamKey         string `toml:"steam_key"`
	SteamID          string `toml:"steam_id"`
}

// Duration wraps time.Duration so TOML values like "24h" parse.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func Default() Config {
	return Config{
		ListenAddr:      ":8080",
		LogLevel:        "info",
		RefreshInterval: Duration{7 * 24 * time.Hour},
	}
}

// Load reads config.toml from dataDir. A missing file yields defaults;
// an unreadable or malformed file is an error.
func Load(dataDir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dataDir, "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}
