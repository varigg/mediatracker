// Package config loads mediatracker's config.toml from the data dir.
// API keys live here — never in env vars.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
		return fmt.Errorf("config: parse duration %q: %w", text, err)
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

// Validate checks that the config contains valid values. It returns an error
// if LogLevel is not a valid slog.Level.
func (c Config) Validate() error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return fmt.Errorf("config: invalid log_level %q: %w", c.LogLevel, err)
	}
	return nil
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
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
