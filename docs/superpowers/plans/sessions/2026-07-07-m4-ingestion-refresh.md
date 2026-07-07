# M4 — Ingestion & Refresh Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** A genuinely bootable `mediatracker` binary with a working
synchronous add-flow (search-picked candidate → hydrate → persist → cover
→ ratings → availability) and an asynchronous weekly refresh cycle, both
invocable today via temporary debug HTTP endpoints ahead of M6's real
route surface.

**Architecture:** One new `internal/ingest` package holds the orchestration
(`Add`, `Refresher`) behind a `Deps` struct wiring the existing M1 store,
M2 registry, and M3 availability enrichers together. A new
`internal/covers` package owns cover download/resize/save. `cmd/mediatracker`
(never actually created in M1 — see Design Decision 1) wires everything and
exposes temporary `/debug/*` routes.

**Tech Stack:** Go stdlib plus one new dependency, `golang.org/x/image/draw`,
for cover resizing (first non-stdlib code dependency in the project; the
existing `BurntSushi/toml` and `modernc.org/sqlite` are config/storage, not
image processing). SQLite via the existing store layer.

## Global Constraints

- Official APIs only, except Game Pass / PS+ catalogs (unofficial,
  quarantined in `gamecatalogs`, circuit-broken) — unchanged from M1–M3.
- API keys stay in `config.toml` in the data dir — never env vars, never
  committed; never read `.env` files.
- Provider failures degrade, never cascade: only a primary `Hydrate`
  failure aborts `Add`; every other enrichment step (cover, ratings,
  availability) logs and continues. Refresh-cycle failures are per-item
  and never stop the cycle.
- System failures (SQLite errors, unwritable data dir) fail loudly —
  propagated as errors, not swallowed.
- All state (db, covers, catalog snapshots, config) stays under the one
  data dir passed via `-data`.
- SQLite stays in WAL mode; refresher and HTTP handlers share one
  `*sql.DB` (already true since M1) — no new connection pooling needed.
- No job queue, no cron table: a missed cycle runs on next start if
  overdue (startup catch-up), per spec Section 3.
- Conventional Commits; no AI attribution anywhere.
- Automated tests never hit live APIs — stub `providers.MetadataProvider`
  / `providers.AvailabilityProvider` implementations and `httptest`
  servers only, matching M2/M3's fixture discipline.

## Design Decisions (flagged for sign-off)

1. **M1 left `cmd/mediatracker/main.go` unwritten.** M1's own session plan
   (Task 7) specified it in full, but only `internal/server/server.go` and
   its test were ever committed — there has never been a `func main` for
   the actual app, only `cmd/probecheck`. Task 1 below completes that gap
   verbatim (verified the referenced `store.Open`/`config.Load` signatures
   are unchanged) before M4's own work begins, because nothing in this
   milestone is "invocable" without it.
2. **Cover resize needs a new dependency.** Stdlib `image` has no resize
   primitive; `golang.org/x/image/draw` (`draw.ApproxBiLinear`) is the
   standard, dependency-light answer maintained by the Go team itself, no
   CGO. Covers are decoded via stdlib `image/jpeg` + `image/png` (both
   blank-imported for format registration), scaled down to a **600px**
   max width only if wider (never upscaled), re-encoded as JPEG (quality
   85) at `covers/{item_id}.jpg`.
3. **`Add`'s idempotent re-add returns the existing item untouched** — no
   re-enrichment, no re-download. Matches the spec's add-flow contract
   ("duplicate add navigates to the existing item"); re-running enrichment
   on every accidental re-add would silently overwrite a user's read
   ratings/availability snapshot on every duplicate click.
4. **Refresh's "refresh ratings" re-invokes `Hydrate`.** There's no
   separate ratings-only upstream call in any adapter — ratings are a
   field on `ItemDetails`, produced only by `Hydrate`. The refresh cycle
   therefore calls `Hydrate` again per active item and replaces only the
   ratings rows; title/genres/cover are deliberately left untouched (the
   spec scopes the cycle to "re-run availability providers, refresh
   ratings", not a full re-hydrate of the item).
5. **Cycle summary is `{Items, RatingsFailed, AvailabilityFailed}`**,
   not the spec's literal "refreshed/failed/skipped" wording. There is no
   distinct "skipped" state at the item level in this design —
   done/abandoned items are excluded by the `ActiveItemsByRefreshDue`
   query itself, not skipped mid-loop, so a "skipped" counter would
   always read zero and become dead weight. The two failure counters are
   what's actually observable and log-worthy.
6. **Startup catch-up is driven by one `last_refresh_at` setting**, not
   separately by "newest catalog snapshot age" and "oldest item's
   refreshed_at" as the spec's prose lists them. `last_refresh_at` is
   written only at the end of a fully completed `RunCycle` (which always
   syncs catalogs and touches every active item), so it's a safe
   superset check — if the last cycle finished recently, both catalogs
   and items are recent by construction. This value doubles as the
   `since` bound for the "newly available" diff query.
7. **Temporary `/debug/*` HTTP endpoints** (`GET /debug/search`,
   `POST /debug/add`, `POST /debug/refresh`, `POST /debug/refresh/{id}`)
   make the pipelines invocable today, per the milestone's own allowance
   ("add via a temporary CLI/HTTP stub if M6 not yet started"). They live
   in `cmd/mediatracker/main.go`, not `internal/server`, so M6 can build
   the real route surface in a clean package without inheriting
   throwaway code — delete `registerDebugRoutes` and its call site when
   M6 lands.
8. **The spec's ~10s add-flow budget is the HTTP layer's job.** `Add`
   takes a `context.Context` and respects cancellation/deadlines; setting
   the actual 10-second deadline on the incoming request is M6's
   concern, not `ingest.Add`'s.

## File Structure

```
cmd/mediatracker/
  main.go                 (new) config/store/registry wiring, debug routes
internal/store/
  settings.go              (new) GetSetting, SetSetting
  settings_test.go         (new)
  refresh.go                (new) TouchRefreshed, ActiveItemsByRefreshDue, NewlyAvailable
  refresh_test.go           (new)
  items.go                  (modify) add SetCoverPath
  items_test.go              (modify) add SetCoverPath tests
internal/covers/
  covers.go                 (new) Fetch: download, scale down, save as JPEG
  covers_test.go             (new)
internal/ingest/
  ingest.go                  (new) Deps
  add.go                       (new) Add
  add_test.go                   (new)
  refresh.go                     (new) Refresher, RunCycle, RefreshItem, overdue
  refresh_test.go                 (new)
```

---

### Task 1: Bootable Server Binary (completing M1's leftover)

**Files:**
- Create: `cmd/mediatracker/main.go`

**Interfaces:**
- Consumes: `config.Load(dataDir string) (config.Config, error)`,
  `store.Open(ctx, path string) (*store.Store, error)`,
  `server.New(st server.Store) http.Handler`.
- Produces: a running binary listening on `cfg.ListenAddr`, serving
  `GET /healthz`. No new exported Go symbols — this task only makes the
  existing, already-tested `internal/server` package reachable.

- [ ] **Step 1: Confirm the gap and write the binary**

`cmd/mediatracker/main.go`:

```go
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
	"syscall"
	"time"

	"github.com/varigg/mediatracker/internal/config"
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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(*dataDir, "app.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: server.New(st)}
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
		return srv.Shutdown(shutdownCtx)
	}
}
```

- [ ] **Step 2: Build and run the existing test suite**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: clean build, all packages PASS (this doesn't add new tests —
`internal/server`'s existing tests already cover the handler; this task
only makes it reachable from a real binary).

- [ ] **Step 3: Boot smoke test**

```bash
SMOKE_DIR=$(mktemp -d)
go build -o /tmp/mediatracker-smoke ./cmd/mediatracker
printf 'listen_addr = ":18080"\n' > "$SMOKE_DIR/config.toml"
/tmp/mediatracker-smoke -data "$SMOKE_DIR" &
sleep 1
curl -s http://localhost:18080/healthz
kill %1
ls "$SMOKE_DIR"
```

Expected: `{"status":"ok"}`; `$SMOKE_DIR` contains `app.db` and
`config.toml`.

- [ ] **Step 4: Commit**

```bash
git add cmd/mediatracker/
git commit -m "feat: add bootable server binary"
```

---

### Task 2: Store Additions — Settings, Refresh Bookkeeping, Cover Path

**Files:**
- Create: `internal/store/settings.go`
- Create: `internal/store/settings_test.go`
- Create: `internal/store/refresh.go`
- Create: `internal/store/refresh_test.go`
- Modify: `internal/store/items.go` (add `SetCoverPath`)
- Modify: `internal/store/items_test.go` (add `SetCoverPath` tests)

**Interfaces:**
- Consumes: `Store.db *sql.DB` (unexported, same package), `scanItem`,
  `selectItem`, `ErrNotFound` — all existing M1 internals.
- Produces: `(*Store).GetSetting(ctx, key string) (string, bool, error)`,
  `(*Store).SetSetting(ctx, key, value string) error`,
  `(*Store).TouchRefreshed(ctx, id int64) error`,
  `(*Store).SetCoverPath(ctx, id int64, path string) error`,
  `(*Store).ActiveItemsByRefreshDue(ctx) ([]MediaItem, error)`,
  `(*Store).NewlyAvailable(ctx, since string) ([]MediaItem, error)`.
  Tasks 4 and 5 depend on these exact names.

- [ ] **Step 1: Write the failing tests**

`internal/store/settings_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestSettingRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, ok, err := s.GetSetting(ctx, "steam_id"); err != nil || ok {
		t.Fatalf("GetSetting on unset key = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if err := s.SetSetting(ctx, "steam_id", "76561190000000001"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, ok, err := s.GetSetting(ctx, "steam_id")
	if err != nil || !ok || got != "76561190000000001" {
		t.Fatalf("GetSetting = (%q, %v, %v), want the stored value", got, ok, err)
	}
	if err := s.SetSetting(ctx, "steam_id", "76561190000000002"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	got, _, err = s.GetSetting(ctx, "steam_id")
	if err != nil || got != "76561190000000002" {
		t.Fatalf("GetSetting after overwrite = %q, want updated value", got)
	}
}
```

`internal/store/refresh_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTouchRefreshedBumpsTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if it.RefreshedAt != nil {
		t.Fatalf("new item RefreshedAt = %v, want nil", it.RefreshedAt)
	}
	if err := s.TouchRefreshed(ctx, it.ID); err != nil {
		t.Fatalf("TouchRefreshed: %v", err)
	}
	got, err := s.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt == nil {
		t.Fatal("RefreshedAt still nil after TouchRefreshed")
	}
}

func TestTouchRefreshedNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.TouchRefreshed(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestActiveItemsByRefreshDueOrdersOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	a := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	b := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "B", Provider: "tmdb", ProviderID: "2"})
	done := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Done", Provider: "tmdb", ProviderID: "3"})
	if err := s.UpdateState(ctx, done.ID, StateDone); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	// B refreshed once, A never — A (NULL) must sort first.
	if err := s.TouchRefreshed(ctx, b.ID); err != nil {
		t.Fatalf("TouchRefreshed: %v", err)
	}

	items, err := s.ActiveItemsByRefreshDue(ctx)
	if err != nil {
		t.Fatalf("ActiveItemsByRefreshDue: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (done/abandoned excluded): %+v", len(items), items)
	}
	if items[0].ID != a.ID || items[1].ID != b.ID {
		t.Errorf("order = [%d, %d], want [never-refreshed A, then B]", items[0].ID, items[1].ID)
	}
}

func TestNewlyAvailableFiltersBySubscribedAndFirstSeen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	item := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	other := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Old News", Provider: "tmdb", ProviderID: "950"})

	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatalf("SetServiceSubscribed: %v", err)
	}
	cutoff := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")

	// item: newly seen on a subscribed service -> must appear.
	if err := s.UpsertAvailability(ctx, item.ID, []Availability{{ServiceSlug: "netflix", Kind: "subscription"}}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}
	// other: seen on a service the user isn't subscribed to -> must not appear.
	if err := s.UpsertAvailability(ctx, other.ID, []Availability{{ServiceSlug: "hulu", Kind: "subscription"}}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}

	got, err := s.NewlyAvailable(ctx, cutoff)
	if err != nil {
		t.Fatalf("NewlyAvailable: %v", err)
	}
	if len(got) != 1 || got[0].ID != item.ID {
		t.Errorf("NewlyAvailable = %+v, want just %q", got, item.Title)
	}
}

func TestNewlyAvailableExcludesBeforeCutoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	item := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatalf("SetServiceSubscribed: %v", err)
	}
	if err := s.UpsertAvailability(ctx, item.ID, []Availability{{ServiceSlug: "netflix", Kind: "subscription"}}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05")

	got, err := s.NewlyAvailable(ctx, future)
	if err != nil {
		t.Fatalf("NewlyAvailable: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("NewlyAvailable with future cutoff = %+v, want none", got)
	}
}
```

Add to `internal/store/items_test.go` (append; file already imports
`context`, `errors`, `testing`):

```go
func TestSetCoverPath(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})
	if it.CoverPath != nil {
		t.Fatalf("new item CoverPath = %v, want nil", it.CoverPath)
	}
	if err := s.SetCoverPath(ctx, it.ID, "covers/1.jpg"); err != nil {
		t.Fatalf("SetCoverPath: %v", err)
	}
	got, err := s.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.CoverPath == nil || *got.CoverPath != "covers/1.jpg" {
		t.Errorf("CoverPath = %v, want covers/1.jpg", got.CoverPath)
	}
}

func TestSetCoverPathNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetCoverPath(context.Background(), 999, "covers/999.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/... 2>&1 | head -15`
Expected: FAIL — `GetSetting`, `SetSetting`, `TouchRefreshed`,
`ActiveItemsByRefreshDue`, `NewlyAvailable`, `SetCoverPath` undefined.

- [ ] **Step 3: Write the implementation**

`internal/store/settings.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting reads one key from the settings table. A missing key is not
// an error — ok is false.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting inserts or overwrites one key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
```

`internal/store/refresh.go`:

```go
package store

import "context"

// TouchRefreshed bumps refreshed_at to now, marking an item as processed
// by the current refresh cycle.
func (s *Store) TouchRefreshed(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE media_items SET refreshed_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveItemsByRefreshDue returns want_to/in_progress items ordered by
// refreshed_at ascending — SQLite sorts NULL first in ASC order, so
// never-refreshed items are processed before stale ones. done/abandoned
// items are frozen and never selected.
func (s *Store) ActiveItemsByRefreshDue(ctx context.Context) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, selectItem+
		` WHERE state IN ('want_to', 'in_progress') ORDER BY refreshed_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MediaItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *it)
	}
	return items, rows.Err()
}

// NewlyAvailable returns want_to items that gained availability on a
// subscribed service (stream or subscription, not owned — ownership
// isn't a "you pay for this" fact) at or after since.
func (s *Store) NewlyAvailable(ctx context.Context, since string) ([]MediaItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT mi.id, mi.media_type, mi.title, mi.state,
		mi.verdict, mi.completed_at, mi.notes, mi.release_year, mi.genres, mi.cover_path,
		mi.provider, mi.provider_id, mi.metadata, mi.added_at, mi.refreshed_at
		FROM media_items mi
		JOIN availability a ON a.item_id = mi.id
		JOIN services s ON s.slug = a.service_slug
		WHERE mi.state = 'want_to' AND s.subscribed = 1
		  AND a.kind IN ('stream', 'subscription') AND a.first_seen_at >= ?
		ORDER BY mi.title COLLATE NOCASE ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MediaItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *it)
	}
	return items, rows.Err()
}
```

Add to `internal/store/items.go` (append):

```go
// SetCoverPath records where a downloaded cover was saved, relative to
// the data dir (e.g. "covers/42.jpg").
func (s *Store) SetCoverPath(ctx context.Context, id int64, path string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE media_items SET cover_path = ? WHERE id = ?`, path, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/store/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/store/
git commit -m "feat: add settings, refresh bookkeeping, and cover-path store methods"
```

---

### Task 3: Cover Download, Resize, and Save

**Files:**
- Create: `internal/covers/covers.go`
- Create: `internal/covers/covers_test.go`

**Interfaces:**
- Consumes: nothing project-internal — stdlib `net/http`, `image`, plus
  the new `golang.org/x/image/draw` dependency.
- Produces: `covers.Fetch(ctx, client *http.Client, dataDir string,
  itemID int64, url string) (relPath string, err error)`. Task 4 depends
  on this exact signature.

- [ ] **Step 1: Add the new dependency**

```bash
go get golang.org/x/image/draw
```

- [ ] **Step 2: Write the failing tests**

`internal/covers/covers_test.go`:

```go
package covers

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fakeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestFetchScalesDownAndSaves(t *testing.T) {
	data := fakeJPEG(t, 1200, 1800)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	relPath, err := Fetch(context.Background(), srv.Client(), dir, 42, srv.URL+"/poster.jpg")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if relPath != filepath.Join("covers", "42.jpg") {
		t.Errorf("relPath = %q, want covers/42.jpg", relPath)
	}

	saved, err := os.Open(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatalf("open saved cover: %v", err)
	}
	defer saved.Close()
	cfg, _, err := image.DecodeConfig(saved)
	if err != nil {
		t.Fatalf("decode saved cover: %v", err)
	}
	if cfg.Width != maxWidth {
		t.Errorf("saved width = %d, want %d (scaled down)", cfg.Width, maxWidth)
	}
	if cfg.Height != 900 { // 1800 * 600/1200
		t.Errorf("saved height = %d, want 900 (aspect preserved)", cfg.Height)
	}
}

func TestFetchNeverUpscales(t *testing.T) {
	data := fakeJPEG(t, 300, 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	relPath, err := Fetch(context.Background(), srv.Client(), dir, 7, srv.URL+"/small.jpg")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	saved, err := os.Open(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatalf("open saved cover: %v", err)
	}
	defer saved.Close()
	cfg, _, err := image.DecodeConfig(saved)
	if err != nil {
		t.Fatalf("decode saved cover: %v", err)
	}
	if cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("saved size = %dx%d, want unchanged 300x200", cfg.Width, cfg.Height)
	}
}

func TestFetchUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), t.TempDir(), 1, srv.URL+"/missing.jpg"); err == nil {
		t.Error("want error on upstream 404")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/covers/... 2>&1 | head -6`
Expected: FAIL — package/`Fetch` undefined.

- [ ] **Step 4: Write the implementation**

`internal/covers/covers.go`:

```go
// Package covers downloads provider-supplied cover art and saves a
// resized local copy under the data dir, so the app never re-fetches
// the same image and never serves arbitrarily large upstream files.
package covers

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// maxWidth is the longest edge a saved cover is scaled down to. Covers
// narrower than this are left at their original size (never upscaled).
const maxWidth = 600

// Fetch downloads url, decodes it as JPEG or PNG, scales it down to
// maxWidth if wider, and saves it as JPEG at
// {dataDir}/covers/{itemID}.jpg. It returns the path relative to
// dataDir for storage in media_items.cover_path.
func Fetch(ctx context.Context, client *http.Client, dataDir string, itemID int64, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("covers: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("covers: fetch %s: status %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("covers: read %s: %w", url, err)
	}

	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("covers: decode %s: %w", url, err)
	}
	scaled := scaleDown(src, maxWidth)

	dir := filepath.Join(dataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("covers: create %s: %w", dir, err)
	}
	relPath := filepath.Join("covers", fmt.Sprintf("%d.jpg", itemID))
	fullPath := filepath.Join(dataDir, relPath)
	tmp := fullPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("covers: create %s: %w", tmp, err)
	}
	if err := jpeg.Encode(f, scaled, &jpeg.Options{Quality: 85}); err != nil {
		f.Close()
		return "", fmt.Errorf("covers: encode %s: %w", fullPath, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, fullPath); err != nil {
		return "", err
	}
	return relPath, nil
}

// scaleDown returns src unchanged if it's already narrower than
// maxWidth (never upscale); otherwise a bilinear-scaled copy at
// maxWidth wide, preserving aspect ratio.
func scaleDown(src image.Image, maxWidth int) image.Image {
	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= maxWidth {
		return src
	}
	newHeight := height * maxWidth / width
	dst := image.NewRGBA(image.Rect(0, 0, maxWidth, newHeight))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
```

Note: `image/jpeg` is imported both for its `Decode`-registering blank
side effect and for `jpeg.Encode`/`jpeg.Options`, so it's a normal
(non-blank) import; `image/png` is blank-imported for decode-only
registration.

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/covers/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/covers/ go.mod go.sum
git commit -m "feat: add cover download, resize, and save"
```

---

### Task 4: Ingest — Add Flow

**Files:**
- Create: `internal/ingest/ingest.go`
- Create: `internal/ingest/add.go`
- Create: `internal/ingest/add_test.go`

**Interfaces:**
- Consumes: `providers.Registry.Get`, `providers.MetadataProvider`,
  `providers.AvailabilityProvider`, `providers.ItemDetails`,
  `providers.Rating`, `providers.Availability` (M2/M3);
  `store.NewItem`, `store.CreateItem`, `store.GetItem`,
  `store.ReplaceRatings`, `store.UpsertAvailability` (M1);
  `covers.Fetch` (Task 3).
- Produces: `ingest.Deps{Store, Registry, Availability, HTTPClient,
  DataDir, Logger, Now, ItemDelay}`,
  `ingest.Add(ctx, d Deps, mediaType store.MediaType, providerID string)
  (*store.MediaItem, error)`. Task 5 depends on the exact `Deps` shape.

- [ ] **Step 1: Write the failing tests**

`internal/ingest/add_test.go`:

```go
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

type stubProvider struct {
	details *providers.ItemDetails
	err     error
}

func (s stubProvider) Search(ctx context.Context, query string) ([]providers.Candidate, error) {
	return nil, nil
}

func (s stubProvider) Hydrate(ctx context.Context, providerID string) (*providers.ItemDetails, error) {
	return s.details, s.err
}

type stubAvailability struct {
	rows []providers.Availability
	err  error
}

func (s stubAvailability) Refresh(ctx context.Context, item *store.MediaItem) ([]providers.Availability, error) {
	return s.rows, s.err
}

func intPtr(i int) *int { return &i }

func detailsFixture(coverURL *string) *providers.ItemDetails {
	return &providers.ItemDetails{
		MediaType:   store.TypeMovie,
		Title:       "Heat",
		ReleaseYear: intPtr(1995),
		Genres:      []string{"Crime"},
		CoverURL:    coverURL,
		Provider:    "tmdb",
		ProviderID:  "949",
		Metadata:    map[string]any{"overview": "A cop and a thief."},
		Ratings:     []providers.Rating{{Source: "imdb", Score: 82, Display: "8.2/10"}},
	}
}

func newTestDeps(t *testing.T, p providers.MetadataProvider, avail ...providers.AvailabilityProvider) Deps {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	registry := providers.NewRegistry()
	registry.Register(store.TypeMovie, p)

	return Deps{
		Store:        st,
		Registry:     registry,
		Availability: avail,
		HTTPClient:   http.DefaultClient,
		DataDir:      t.TempDir(),
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Now:          time.Now,
	}
}

func TestAddPersistsItemWithRatingsAndAvailability(t *testing.T) {
	d := newTestDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})

	item, err := Add(context.Background(), d, store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.Title != "Heat" || item.Provider != "tmdb" || item.ProviderID != "949" {
		t.Errorf("item = %+v, want Heat/tmdb/949", item)
	}

	ratings, err := d.Store.GetRatings(context.Background(), item.ID)
	if err != nil || len(ratings) != 1 || ratings[0].Source != "imdb" {
		t.Errorf("ratings = %+v, err %v, want one imdb row", ratings, err)
	}
	avail, err := d.Store.GetAvailability(context.Background(), item.ID)
	if err != nil || len(avail) != 1 || avail[0].ServiceSlug != "netflix" {
		t.Errorf("availability = %+v, err %v, want one netflix row", avail, err)
	}
}

func TestAddDownloadsCover(t *testing.T) {
	imgData := fakeJPEGForTest(t)
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(imgData)
	}))
	defer imgSrv.Close()
	coverURL := imgSrv.URL + "/poster.jpg"

	d := newTestDeps(t, stubProvider{details: detailsFixture(&coverURL)})
	item, err := Add(context.Background(), d, store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.CoverPath == nil {
		t.Fatal("CoverPath is nil, want a saved cover path")
	}
	if _, err := os.Stat(filepath.Join(d.DataDir, *item.CoverPath)); err != nil {
		t.Errorf("cover file missing on disk: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(item.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["cover_url"] != coverURL {
		t.Errorf("metadata cover_url = %v, want %q (kept for re-fetch)", meta["cover_url"], coverURL)
	}
}

func TestAddHydrateFailureAborts(t *testing.T) {
	d := newTestDeps(t, stubProvider{err: errors.New("upstream down")})
	_, err := Add(context.Background(), d, store.TypeMovie, "949")
	if err == nil {
		t.Fatal("want error when Hydrate fails")
	}
	items, err := d.Store.ListItems(context.Background(), url.Values{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items = %+v, want none persisted on hydrate failure", items)
	}
}

func TestAddDegradesOnCoverAndAvailabilityFailure(t *testing.T) {
	badCoverURL := "http://127.0.0.1:1/missing.jpg" // nothing listens here
	d := newTestDeps(t, stubProvider{details: detailsFixture(&badCoverURL)},
		stubAvailability{err: errors.New("provider down")})

	item, err := Add(context.Background(), d, store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("Add must degrade, not fail: %v", err)
	}
	if item.CoverPath != nil {
		t.Errorf("CoverPath = %v, want nil (cover fetch failed)", item.CoverPath)
	}
	avail, err := d.Store.GetAvailability(context.Background(), item.ID)
	if err != nil || len(avail) != 0 {
		t.Errorf("availability = %+v, err %v, want none (provider failed)", avail, err)
	}
}

func TestAddIdempotentReAddReturnsExistingItem(t *testing.T) {
	d := newTestDeps(t, stubProvider{details: detailsFixture(nil)})
	first, err := Add(context.Background(), d, store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	second, err := Add(context.Background(), d, store.TypeMovie, "949")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("re-add ID = %d, want existing item's ID %d", second.ID, first.ID)
	}
}
```

`internal/ingest/testutil_test.go`:

```go
package ingest

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// fakeJPEGForTest returns a tiny valid JPEG for tests that exercise the
// cover-download path without hitting a real image host.
func fakeJPEGForTest(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode fixture jpeg: %v", err)
	}
	return buf.Bytes()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ingest/... 2>&1 | head -10`
Expected: FAIL — package/`Deps`/`Add` undefined.

- [ ] **Step 3: Write the implementation**

`internal/ingest/ingest.go`:

```go
// Package ingest orchestrates the synchronous add-flow and the
// asynchronous weekly refresh cycle on top of the M1 store, the M2
// metadata-provider registry, and the M3 availability enrichers.
package ingest

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

// Deps wires everything Add and Refresher need. Now defaults to
// time.Now in production; tests override it for determinism. ItemDelay
// is the inter-item pause during a refresh cycle — zero (the test
// default) means no pause.
type Deps struct {
	Store        *store.Store
	Registry     *providers.Registry
	Availability []providers.AvailabilityProvider
	HTTPClient   *http.Client
	DataDir      string
	Logger       *slog.Logger
	Now          func() time.Time
	ItemDelay    time.Duration
}
```

`internal/ingest/add.go`:

```go
package ingest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/varigg/mediatracker/internal/covers"
	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

func toStoreRatings(itemID int64, in []providers.Rating) []store.Rating {
	out := make([]store.Rating, len(in))
	for i, r := range in {
		out[i] = store.Rating{ItemID: itemID, Source: r.Source, Score: r.Score, Display: r.Display, URL: r.URL}
	}
	return out
}

// Add runs the synchronous add-flow: hydrate the picked candidate,
// persist it, then best-effort cover download, ratings, and
// availability. Only a Hydrate failure aborts — everything after
// persistence degrades the item with gaps rather than failing the add.
// A duplicate add (same provider/provider_id) returns the existing item
// untouched, with no re-enrichment.
func Add(ctx context.Context, d Deps, mediaType store.MediaType, providerID string) (*store.MediaItem, error) {
	p, err := d.Registry.Get(mediaType)
	if err != nil {
		return nil, err
	}
	details, err := p.Hydrate(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("ingest: hydrate %s %s: %w", mediaType, providerID, err)
	}

	metadata := details.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	if details.CoverURL != nil {
		metadata["cover_url"] = *details.CoverURL
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("ingest: marshal metadata: %w", err)
	}

	item, created, err := d.Store.CreateItem(ctx, store.NewItem{
		MediaType:   details.MediaType,
		Title:       details.Title,
		ReleaseYear: details.ReleaseYear,
		Genres:      details.Genres,
		Provider:    details.Provider,
		ProviderID:  details.ProviderID,
		Metadata:    metaJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: persist item: %w", err)
	}
	if !created {
		return item, nil
	}

	if details.CoverURL != nil {
		relPath, err := covers.Fetch(ctx, d.HTTPClient, d.DataDir, item.ID, *details.CoverURL)
		if err != nil {
			d.Logger.Warn("add: cover download failed", "item_id", item.ID, "error", err)
		} else if err := d.Store.SetCoverPath(ctx, item.ID, relPath); err != nil {
			d.Logger.Warn("add: persist cover path failed", "item_id", item.ID, "error", err)
		}
	}

	if err := d.Store.ReplaceRatings(ctx, item.ID, toStoreRatings(item.ID, details.Ratings)); err != nil {
		d.Logger.Warn("add: persist ratings failed", "item_id", item.ID, "error", err)
	}

	var avail []providers.Availability
	for _, ap := range d.Availability {
		rows, err := ap.Refresh(ctx, item)
		if err != nil {
			d.Logger.Warn("add: availability provider failed", "item_id", item.ID, "error", err)
			continue
		}
		avail = append(avail, rows...)
	}
	if err := d.Store.UpsertAvailability(ctx, item.ID, avail); err != nil {
		d.Logger.Warn("add: persist availability failed", "item_id", item.ID, "error", err)
	}

	return d.Store.GetItem(ctx, item.ID)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/ingest/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/ingest/
git commit -m "feat: add ingest.Add synchronous add-flow"
```

---

### Task 5: Ingest — Refresh Cycle

**Files:**
- Create: `internal/ingest/refresh.go`
- Create: `internal/ingest/refresh_test.go`

**Interfaces:**
- Consumes: `Deps` (Task 4), `store.ActiveItemsByRefreshDue`,
  `store.TouchRefreshed`, `store.GetSetting`/`SetSetting` (Task 2),
  `providers.CycleSyncer` (M3).
- Produces: `ingest.Summary{Items, RatingsFailed, AvailabilityFailed
  int}`, `ingest.NewRefresher(d Deps, interval time.Duration)
  *Refresher`, `(*Refresher).RunCycle(ctx) (Summary, error)`,
  `(*Refresher).RefreshItem(ctx, itemID int64) error`,
  `(*Refresher).Start(ctx)` (blocks until ctx is done — Task 6 runs it
  in a goroutine).

- [ ] **Step 1: Write the failing tests**

`internal/ingest/refresh_test.go`:

```go
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

type stubSyncer struct {
	stubAvailability
	syncErr   error
	syncCalls int
}

func (s *stubSyncer) SyncCycle(ctx context.Context) error {
	s.syncCalls++
	return s.syncErr
}

func newRefresherDeps(t *testing.T, p providers.MetadataProvider, avail ...providers.AvailabilityProvider) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	registry := providers.NewRegistry()
	if p != nil {
		registry.Register(store.TypeMovie, p)
	}
	d := Deps{
		Store:        st,
		Registry:     registry,
		Availability: avail,
		HTTPClient:   http.DefaultClient,
		DataDir:      t.TempDir(),
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Now:          time.Now,
	}
	return d, st
}

func TestRunCycleRefreshesActiveItemsOnly(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})

	active, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "Active", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	done, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "Done", Provider: "tmdb", ProviderID: "2"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if err := st.UpdateState(ctx, done.ID, store.StateDone); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	sum, err := r.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if sum.Items != 1 {
		t.Errorf("sum.Items = %d, want 1 (done item excluded)", sum.Items)
	}

	got, err := st.GetItem(ctx, active.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt == nil {
		t.Error("active item RefreshedAt still nil after RunCycle")
	}
	avail, err := st.GetAvailability(ctx, active.ID)
	if err != nil || len(avail) != 1 {
		t.Errorf("availability = %+v, err %v, want one row", avail, err)
	}

	gotDone, err := st.GetItem(ctx, done.ID)
	if err != nil {
		t.Fatalf("GetItem done: %v", err)
	}
	if gotDone.RefreshedAt != nil {
		t.Error("done item RefreshedAt touched, want it left frozen")
	}
}

func TestRunCycleSyncsCatalogsBeforeItems(t *testing.T) {
	syncer := &stubSyncer{}
	d, _ := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)}, syncer)

	r := NewRefresher(d, time.Hour)
	if _, err := r.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if syncer.syncCalls != 1 {
		t.Errorf("SyncCycle called %d times, want 1", syncer.syncCalls)
	}
}

func TestRunCycleCountsRatingsFailure(t *testing.T) {
	d, st := newRefresherDeps(t, stubProvider{err: errors.New("upstream down")})
	if _, _, err := st.CreateItem(context.Background(), store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	sum, err := r.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if sum.RatingsFailed != 1 {
		t.Errorf("sum.RatingsFailed = %d, want 1", sum.RatingsFailed)
	}
}

func TestRunCyclePersistsLastRefreshAt(t *testing.T) {
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)})
	r := NewRefresher(d, time.Hour)
	if _, err := r.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	_, ok, err := st.GetSetting(context.Background(), "last_refresh_at")
	if err != nil || !ok {
		t.Errorf("last_refresh_at not persisted: ok=%v err=%v", ok, err)
	}
}

func TestRefreshItemRunsSameCodePathAsCycle(t *testing.T) {
	ctx := context.Background()
	d, st := newRefresherDeps(t, stubProvider{details: detailsFixture(nil)},
		stubAvailability{rows: []providers.Availability{{ServiceSlug: "netflix", Kind: "subscription"}}})
	item, _, err := st.CreateItem(ctx, store.NewItem{MediaType: store.TypeMovie, Title: "A", Provider: "tmdb", ProviderID: "1"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	r := NewRefresher(d, time.Hour)
	if err := r.RefreshItem(ctx, item.ID); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}
	got, err := st.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.RefreshedAt == nil {
		t.Error("RefreshedAt still nil after RefreshItem")
	}
	avail, err := st.GetAvailability(ctx, item.ID)
	if err != nil || len(avail) != 1 {
		t.Errorf("availability = %+v, err %v, want one row", avail, err)
	}
}

func TestOverdueWhenNeverRun(t *testing.T) {
	d, _ := newRefresherDeps(t, nil)
	r := NewRefresher(d, time.Hour)
	if !r.overdue(context.Background()) {
		t.Error("want overdue=true when last_refresh_at was never set")
	}
}

func TestOverdueFalseWithinInterval(t *testing.T) {
	d, st := newRefresherDeps(t, nil)
	if err := st.SetSetting(context.Background(), "last_refresh_at", time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	r := NewRefresher(d, time.Hour)
	if r.overdue(context.Background()) {
		t.Error("want overdue=false right after a cycle completed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ingest/... 2>&1 | head -10`
Expected: FAIL — `NewRefresher`/`RunCycle`/`RefreshItem`/`overdue` undefined.

- [ ] **Step 3: Write the implementation**

`internal/ingest/refresh.go`:

```go
package ingest

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/varigg/mediatracker/internal/providers"
	"github.com/varigg/mediatracker/internal/store"
)

const lastRefreshSettingKey = "last_refresh_at"

// timeFormat matches the SQLite TEXT timestamp format the store uses
// elsewhere (CURRENT_TIMESTAMP's default rendering).
const timeFormat = "2006-01-02 15:04:05"

// Summary reports one refresh cycle's outcome for the per-cycle log
// line. There's no separate "skipped" count: done/abandoned items are
// excluded by the selection query itself, not skipped mid-cycle.
type Summary struct {
	Items              int
	RatingsFailed      int
	AvailabilityFailed int
}

// Refresher runs the weekly background refresh: catalog snapshot sync,
// then per-active-item availability + ratings refresh, sequential with
// a small inter-item delay.
type Refresher struct {
	deps     Deps
	interval time.Duration
}

func NewRefresher(d Deps, interval time.Duration) *Refresher {
	return &Refresher{deps: d, interval: interval}
}

// Start runs an immediate catch-up cycle if overdue, then loops on a
// jittered ticker until ctx is done. Call it in its own goroutine.
func (r *Refresher) Start(ctx context.Context) {
	if r.overdue(ctx) {
		if _, err := r.RunCycle(ctx); err != nil {
			r.deps.Logger.Error("startup refresh cycle failed", "error", err)
		}
	}

	jitterMax := int64(r.interval / 20) // up to 5% of the interval
	var jitter time.Duration
	if jitterMax > 0 {
		jitter = time.Duration(rand.Int63n(jitterMax))
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.RunCycle(ctx); err != nil {
				r.deps.Logger.Error("refresh cycle failed", "error", err)
			}
		}
	}
}

// overdue reports whether the last completed cycle is older than the
// interval (or none is recorded), driving startup catch-up.
func (r *Refresher) overdue(ctx context.Context) bool {
	last, ok, err := r.deps.Store.GetSetting(ctx, lastRefreshSettingKey)
	if err != nil || !ok {
		return true
	}
	lastT, err := time.Parse(timeFormat, last)
	if err != nil {
		return true
	}
	return r.deps.Now().Sub(lastT) >= r.interval
}

// RunCycle re-syncs catalog snapshots, then refreshes every active item
// sequentially. A failure syncing catalogs or refreshing one item never
// stops the cycle; only a failure listing active items aborts it.
func (r *Refresher) RunCycle(ctx context.Context) (Summary, error) {
	for _, p := range r.deps.Availability {
		if syncer, ok := p.(providers.CycleSyncer); ok {
			if err := syncer.SyncCycle(ctx); err != nil {
				r.deps.Logger.Warn("catalog sync failed", "error", err)
			}
		}
	}

	items, err := r.deps.Store.ActiveItemsByRefreshDue(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("ingest: list active items: %w", err)
	}

	var sum Summary
	for i := range items {
		outcome := r.refreshItem(ctx, &items[i])
		sum.Items++
		if outcome.ratingsFailed {
			sum.RatingsFailed++
		}
		if outcome.availabilityFailed {
			sum.AvailabilityFailed++
		}
		if i < len(items)-1 && r.deps.ItemDelay > 0 {
			select {
			case <-ctx.Done():
				return sum, ctx.Err()
			case <-time.After(r.deps.ItemDelay):
			}
		}
	}

	if err := r.deps.Store.SetSetting(ctx, lastRefreshSettingKey, r.deps.Now().UTC().Format(timeFormat)); err != nil {
		r.deps.Logger.Warn("persist last_refresh_at failed", "error", err)
	}
	r.deps.Logger.Info("refresh cycle complete", "items", sum.Items,
		"ratings_failed", sum.RatingsFailed, "availability_failed", sum.AvailabilityFailed)
	return sum, nil
}

// RefreshItem refreshes one item via the same per-item logic RunCycle
// uses, for the manual per-item refresh entry point.
func (r *Refresher) RefreshItem(ctx context.Context, itemID int64) error {
	item, err := r.deps.Store.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	r.refreshItem(ctx, item)
	return nil
}

type refreshOutcome struct {
	ratingsFailed      bool
	availabilityFailed bool
}

func (r *Refresher) refreshItem(ctx context.Context, item *store.MediaItem) refreshOutcome {
	var out refreshOutcome

	if p, err := r.deps.Registry.Get(item.MediaType); err == nil {
		details, err := p.Hydrate(ctx, item.ProviderID)
		if err != nil {
			out.ratingsFailed = true
			r.deps.Logger.Warn("refresh: hydrate failed", "item_id", item.ID, "error", err)
		} else if err := r.deps.Store.ReplaceRatings(ctx, item.ID, toStoreRatings(item.ID, details.Ratings)); err != nil {
			out.ratingsFailed = true
			r.deps.Logger.Warn("refresh: replace ratings failed", "item_id", item.ID, "error", err)
		}
	}

	var avail []providers.Availability
	failures := 0
	for _, ap := range r.deps.Availability {
		rows, err := ap.Refresh(ctx, item)
		if err != nil {
			failures++
			r.deps.Logger.Warn("refresh: availability provider failed", "item_id", item.ID, "error", err)
			continue
		}
		avail = append(avail, rows...)
	}
	if len(r.deps.Availability) > 0 && failures == len(r.deps.Availability) {
		out.availabilityFailed = true
	}
	if err := r.deps.Store.UpsertAvailability(ctx, item.ID, avail); err != nil {
		out.availabilityFailed = true
		r.deps.Logger.Warn("refresh: upsert availability failed", "item_id", item.ID, "error", err)
	}

	if err := r.deps.Store.TouchRefreshed(ctx, item.ID); err != nil {
		r.deps.Logger.Warn("refresh: touch refreshed_at failed", "item_id", item.ID, "error", err)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/ingest/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/ingest/
git commit -m "feat: add ingest.Refresher weekly refresh cycle"
```

---

### Task 6: Wire Ingest and Refresher into the Binary

**Files:**
- Modify: `cmd/mediatracker/main.go`

**Interfaces:**
- Consumes: `setup.FromConfig`, `setup.AvailabilityFromConfig` (M2/M3
  wiring, unchanged); `ingest.Deps`, `ingest.Add`, `ingest.NewRefresher`,
  `(*Refresher).Start`, `(*Refresher).RunCycle`,
  `(*Refresher).RefreshItem` (Tasks 4–5).
- Produces: temporary routes `GET /debug/search`, `POST /debug/add`,
  `POST /debug/refresh`, `POST /debug/refresh/{id}` — delete in M6 (see
  Design Decision 7).

- [ ] **Step 1: Replace `cmd/mediatracker/main.go`**

```go
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
	go refresher.Start(ctx)

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
		return srv.Shutdown(shutdownCtx)
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
		if err := refresher.RefreshItem(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
```

- [ ] **Step 2: Build, vet, full test suite**

Run: `set -o pipefail; go build ./... && go vet ./... && go test ./... -count=1`
Expected: clean build, all packages PASS.

- [ ] **Step 3: Boot smoke test**

```bash
SMOKE_DIR=$(mktemp -d)
go build -o /tmp/mediatracker-smoke ./cmd/mediatracker
printf 'listen_addr = ":18080"\n' > "$SMOKE_DIR/config.toml"
/tmp/mediatracker-smoke -data "$SMOKE_DIR" &
sleep 1
curl -s http://localhost:18080/healthz
kill %1
```

Expected: `{"status":"ok"}`. This only proves the binary boots cleanly
with no provider keys configured (gamecatalogs still runs live against
the real Game Pass endpoint on the startup catch-up cycle, since it
needs no key — that's expected, not a test failure). Exercising
`/debug/add` end-to-end against real providers is a manual follow-up
once you're ready to point it at your populated `config.toml`, not part
of this automated step.

- [ ] **Step 4: Commit**

```bash
git add cmd/mediatracker/
git commit -m "feat: wire ingest add-flow and refresh cycle into the binary"
```

---

## Self-Review Notes

Spec coverage: Section 3's add-flow (hydrate → persist → cover →
ratings → availability, non-essential failures tolerated) → Task 4;
refresh cycle (catalog sync, active-items-by-refreshed_at-asc,
sequential with inter-item delay, done/abandoned skipped, startup
catch-up, manual global + per-item entry points via the same code path)
→ Task 5; "newly available" diff query → Task 2's `NewlyAvailable`
(not yet called by anything — M6's landing page is its consumer, per
spec Section 4; wiring it into a route is explicitly out of scope here).
Per-cycle summary log line → `RunCycle`'s closing `slog.Info` in Task 5.
"Both invocable" → Task 6's debug routes. The M1 binary gap → Task 1.
No placeholders: every step above has complete code, not a description.
