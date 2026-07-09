package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/ingest"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	refresher := ingest.NewRefresher(ingest.Deps{
		Store:      st,
		Registry:   providers.NewRegistry(),
		Logger:     logger,
		DataDir:    dataDir,
		HTTPClient: http.DefaultClient,
	}, time.Hour)
	srv := httptest.NewServer(New(Deps{
		Store:           st,
		Logger:          logger,
		DataDir:         dataDir,
		RefreshInterval: 7 * 24 * time.Hour,
		Refresher:       refresher,
	}))
	t.Cleanup(srv.Close)
	return srv, st, dataDir
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	if _, err := io.Copy(&b, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, b.String()
}

func TestHealthzOK(t *testing.T) {
	srv, _, _ := newTestServer(t)
	resp, body := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil || m["status"] != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestHealthzFailsLoudly(t *testing.T) {
	srv, st, _ := newTestServer(t)
	st.Close() // simulate a dead database
	resp, _ := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestAssetsServed(t *testing.T) {
	srv, _, _ := newTestServer(t)
	for path, needle := range map[string]string{
		"/assets/htmx.min.js": "htmx",
		"/assets/app.css":     "--accent",
	} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, needle) {
			t.Errorf("%s: status %d, contains(%q)=%v", path, resp.StatusCode, needle, strings.Contains(body, needle))
		}
	}
}
