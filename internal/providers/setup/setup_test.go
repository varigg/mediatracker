package setup

import (
	"log/slog"
	"testing"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/store"
)

func TestFromConfigRegistersByConfiguredKeys(t *testing.T) {
	logger := slog.Default()

	r := FromConfig(config.Providers{}, logger)
	if _, err := r.Get(store.TypeBook); err != nil {
		t.Errorf("books must register without keys: %v", err)
	}
	for _, mt := range []store.MediaType{store.TypeMovie, store.TypeTV, store.TypeGame} {
		if _, err := r.Get(mt); err == nil {
			t.Errorf("%s must not register without keys", mt)
		}
	}

	full := config.Providers{
		TMDBKey:          "k",
		OMDBKey:          "k",
		IGDBClientID:     "id",
		IGDBClientSecret: "secret",
		HardcoverKey:     "k",
	}
	r = FromConfig(full, logger)
	for _, mt := range []store.MediaType{store.TypeMovie, store.TypeTV, store.TypeBook, store.TypeGame} {
		if _, err := r.Get(mt); err != nil {
			t.Errorf("%s must register with full keys: %v", mt, err)
		}
	}
}
