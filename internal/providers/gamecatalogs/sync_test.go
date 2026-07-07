package gamecatalogs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Errorf("read fixture %s: %v", name, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func newTestProvider(t *testing.T, mux *http.ServeMux, opts ...Option) *Provider {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	dir := filepath.Join(t.TempDir(), "catalogs")
	all := append([]Option{
		WithGamePassSiglsURL(srv.URL + "/sigls"),
		WithGamePassProductsURL(srv.URL + "/products"),
		WithPSPlusURL(srv.URL + "/psplus"),
	}, opts...)
	return New(dir, all...)
}

func healthyMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/sigls", serveFixture(t, "gamepass_sigls.json"))
	mux.HandleFunc("/products", serveFixture(t, "gamepass_products.json"))
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	return mux
}

func TestSyncCycleWritesSnapshots(t *testing.T) {
	p := newTestProvider(t, healthyMux(t))
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	gp, err := p.loadSnapshot("game_pass")
	if err != nil {
		t.Fatalf("load game_pass snapshot: %v", err)
	}
	if len(gp.Entries) != 3 || gp.FetchedAt == "" {
		t.Errorf("game_pass snapshot = %d entries, fetched_at %q", len(gp.Entries), gp.FetchedAt)
	}
	ps, err := p.loadSnapshot("ps_plus")
	if err != nil {
		t.Fatalf("load ps_plus snapshot: %v", err)
	}
	if len(ps.Entries) != 2 {
		t.Errorf("ps_plus snapshot = %d entries, want 2", len(ps.Entries))
	}
}

func TestSyncCycleRetainsStaleSnapshotOnFailure(t *testing.T) {
	failing := false
	mux := http.NewServeMux()
	mux.HandleFunc("/sigls", func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		serveFixture(t, "gamepass_sigls.json")(w, r)
	})
	mux.HandleFunc("/products", serveFixture(t, "gamepass_products.json"))
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	p := newTestProvider(t, mux)

	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("first SyncCycle error = %v", err)
	}
	failing = true
	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("failing SyncCycle must degrade, not error; got %v", err)
	}
	snap, err := p.loadSnapshot("game_pass")
	if err != nil {
		t.Fatalf("stale snapshot must survive fetch failure: %v", err)
	}
	if len(snap.Entries) != 3 {
		t.Errorf("stale snapshot = %d entries, want original 3", len(snap.Entries))
	}
}

func TestSyncCycleBreakerLimitsRequests(t *testing.T) {
	var gamePassCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/sigls", func(w http.ResponseWriter, r *http.Request) {
		gamePassCalls++
		w.WriteHeader(http.StatusBadGateway)
	})
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	p := newTestProvider(t, mux)

	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	if gamePassCalls != 3 {
		t.Errorf("breaker allowed %d requests, want exactly threshold 3", gamePassCalls)
	}
	if _, err := p.loadSnapshot("ps_plus"); err != nil {
		t.Errorf("healthy catalog must still sync when the other trips: %v", err)
	}
}

func TestSyncCycleTreatsEmptyCatalogAsFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sigls", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/psplus", serveFixture(t, "psplus_catalog.json"))
	p := newTestProvider(t, mux)

	if err := p.SyncCycle(context.Background()); err != nil {
		t.Fatalf("SyncCycle error = %v", err)
	}
	if _, err := p.loadSnapshot("game_pass"); err == nil {
		t.Error("empty catalog (likely schema drift) must not produce a snapshot")
	}
}
